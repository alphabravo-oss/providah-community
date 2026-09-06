package core

import (
	"connectrpc.com/connect"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/alphabravo-oss/providah-community/internal/database"
	pb "github.com/alphabravo-oss/providah-community/internal/gen/providah/v1"
	"github.com/alphabravo-oss/providah-community/sdk/provider"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

func dbTime(t time.Time) pgtype.Timestamptz { return pgtype.Timestamptz{Time: t, Valid: !t.IsZero()} }
func (s *Service) scheduleProto(ctx context.Context, row database.Schedule) (*pb.Schedule, error) {
	spec := new(pb.ScheduleSpec)
	if err := json.Unmarshal(row.Spec, spec); err != nil {
		return nil, err
	}
	targets, err := s.q.ListScheduleTargets(ctx, database.ListScheduleTargetsParams{OrgID: row.OrgID, ScheduleID: row.ID})
	if err != nil {
		return nil, err
	}
	maintenanceRevision, err := s.q.GetMaintenanceRevision(ctx, row.OrgID)
	if err != nil {
		return nil, err
	}
	out := &pb.Schedule{Revision: row.WriteRevision, Id: row.ID, Name: row.Name, Spec: spec, IdentityId: row.IdentityID, IdentityEnabled: row.IdentityEnabled, Enabled: row.Enabled, Approved: row.ApprovedRevision.Valid && row.ApprovedRevision.Int64 == row.Revision && row.MaintenanceRevision == maintenanceRevision, NextDue: stamp(row.NextDue), NextAction: row.NextAction, NextLocal: row.NextLocal, NextSkip: row.NextSkip, LastOutcome: row.LastOutcome, LastDetail: row.LastDetail, Editor: row.EditorEmail, CanApprove: actor(ctx).UserID != row.EditorID}
	for _, t := range targets {
		allowed, e := moduleAllowed(ctx, s.q, row.OrgID, t.Provider, t.ModuleRevision)
		if e != nil {
			return nil, e
		}
		if !allowed {
			out.Approved = false
		}
		out.ResourceIds = append(out.ResourceIds, t.ResourceID)
	}
	return out, nil
}
func (s *Service) scheduleResponse(ctx context.Context, org, id string) (*connect.Response[pb.ScheduleResponse], error) {
	row, err := s.q.GetSchedule(ctx, database.GetScheduleParams{OrgID: org, ID: id})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, denied()
	}
	if err != nil {
		return nil, err
	}
	out, err := s.scheduleProto(ctx, row)
	return connect.NewResponse(&pb.ScheduleResponse{Schedule: out}), err
}
func (s *Service) GetSchedule(ctx context.Context, req *connect.Request[pb.GetScheduleRequest]) (*connect.Response[pb.ScheduleResponse], error) {
	if !regexp.MustCompile(`^[a-f0-9]{64}$`).MatchString(req.Msg.Id) {
		return nil, invalid("Invalid schedule ID.")
	}
	return s.scheduleResponse(ctx, req.Msg.OrganizationId, req.Msg.Id)
}
func (s *Service) ListSchedules(ctx context.Context, req *connect.Request[pb.ListSchedulesRequest]) (*connect.Response[pb.ListSchedulesResponse], error) {
	rows, err := s.q.ListSchedules(ctx, req.Msg.OrganizationId)
	if err != nil {
		return nil, err
	}
	if len(rows) > 1000 {
		return nil, conflict("The current schedule directory limit is 1,000.")
	}
	out := &pb.ListSchedulesResponse{}
	for _, row := range rows {
		o, err := s.scheduleProto(ctx, row)
		if err != nil {
			return nil, err
		}
		out.Schedules = append(out.Schedules, o)
	}
	return connect.NewResponse(out), nil
}
func (s *Service) scheduleRuntimeSupported(ctx context.Context, q *database.Queries, org, cloud, action string) (bool, error) {
	state, err := q.GetProviderModule(ctx, database.GetProviderModuleParams{OrgID: org, Provider: cloud})
	if err != nil {
		return false, err
	}
	runtime := s.runtimeCapabilities(cloud, s.selectedRuntime(cloud, state.RuntimeID))
	if action == "window" {
		return runtime.SupportsAction("start") && runtime.SupportsAction("shutdown"), nil
	}
	return runtime.SupportsAction(action), nil
}

func (s *Service) PreviewSchedule(ctx context.Context, req *connect.Request[pb.PreviewScheduleRequest]) (*connect.Response[pb.PreviewScheduleResponse], error) {
	out := &pb.PreviewScheduleResponse{}
	if len(req.Msg.ResourceIds) > 50 {
		return nil, invalid("Preview at most 50 exact targets.")
	}
	connections := []string{}
	moduleRestriction := ""
	if len(req.Msg.ResourceIds) > 0 && !hasPermission(ctx, s.q, req.Msg.OrganizationId, actor(ctx).UserID, "resources.read") {
		return nil, denied()
	}
	for _, id := range req.Msg.ResourceIds {
		r, err := s.q.GetResource(ctx, database.GetResourceParams{OrgID: req.Msg.OrganizationId, ID: id})
		if err != nil {
			return nil, denied()
		}
		allowed, e := moduleAllowed(ctx, s.q, req.Msg.OrganizationId, r.Provider, 0)
		if e != nil {
			return nil, e
		}
		if !allowed {
			moduleRestriction = "A selected provider module is disabled."
		}
		if req.Msg.Spec != nil {
			supported, err := s.scheduleRuntimeSupported(ctx, s.q, req.Msg.OrganizationId, r.Provider, req.Msg.Spec.Action)
			if err != nil {
				return nil, err
			}
			if !supported {
				moduleRestriction = "A selected provider runtime does not support this schedule action."
			}
		}
		if !slices.Contains(connections, r.ConnectionID) {
			connections = append(connections, r.ConnectionID)
		}
	}
	policies, err := s.q.ListMaintenancePolicies(ctx, req.Msg.OrganizationId)
	if err != nil {
		return nil, err
	}
	after := time.Now()
	priorLocal, priorAction := "", ""
	for i := 0; i < 8; i++ {
		o, err := nextScheduleAfter(req.Msg.Spec, after, priorLocal, priorAction)
		if err != nil {
			return nil, invalid(err.Error())
		}
		if o.Due.IsZero() {
			break
		}
		restrictions := []string{}
		for _, connection := range connections {
			detail, _, err := maintenanceRestriction(policies, connection, o.Due)
			if err != nil {
				return nil, err
			}
			if detail != "" && !slices.Contains(restrictions, detail) {
				restrictions = append(restrictions, detail)
			}
		}
		if o.Skip == "" {
			o.Skip = moduleRestriction
		}
		out.Occurrences = append(out.Occurrences, &pb.SchedulePreview{Due: o.Due.Format(time.RFC3339), Local: o.Local, Action: o.Action, SkippedReason: o.Skip, MaintenanceRestrictions: strings.Join(restrictions, " ")})
		after = o.Due
		priorLocal, priorAction = o.Local, o.Action
	}
	return connect.NewResponse(out), nil
}
func (s *Service) SaveSchedule(ctx context.Context, req *connect.Request[pb.SaveScheduleRequest]) (*connect.Response[pb.ScheduleResponse], error) {
	r := req.Msg
	r.Name = strings.TrimSpace(r.Name)
	if r.Id == "" && r.ExpectedRevision != 0 {
		return nil, invalid("New schedules cannot specify an existing revision.")
	}
	if len(r.Name) < 1 || len(r.Name) > 120 || len(r.ResourceIds) < 1 || len(r.ResourceIds) > 50 {
		return nil, invalid("Name the schedule and choose 1–50 exact server targets.")
	}
	next, err := nextSchedule(r.Spec, time.Now())
	if err != nil {
		return nil, invalid(err.Error())
	}
	if next.Due.IsZero() {
		return nil, invalid("Choose a future occurrence.")
	}
	spec, err := json.Marshal(r.Spec)
	if err != nil {
		return nil, err
	}
	id := r.Id
	if id == "" {
		id = randomID()
	}
	err = s.transaction(ctx, func(q *database.Queries) error {
		permissions, err := q.Permissions(ctx, database.PermissionsParams{OrgID: r.OrganizationId, UserID: actor(ctx).UserID})
		if err != nil || !slices.Contains(permissions, "schedules.manage") || !powerPermission(ctx, q, r.OrganizationId, actor(ctx).UserID, "operations.request") {
			return denied()
		}
		if r.Id != "" {
			if _, err := lockReviewedSchedule(ctx, q, r.OrganizationId, id, r.ExpectedRevision); err != nil {
				return err
			}
		}
		// Snapshot targets before locking connections, then lock in a consistent order.
		targets := []database.GetResourceRow{}
		seen := map[string]bool{}
		for _, target := range r.ResourceIds {
			if seen[target] {
				continue
			}
			seen[target] = true
			row, err := q.GetResource(ctx, database.GetResourceParams{OrgID: r.OrganizationId, ID: target})
			if err != nil || row.Kind != "compute.server" {
				return denied()
			}
			targets = append(targets, row)
		}
		slices.SortFunc(targets, func(a, b database.GetResourceRow) int { return strings.Compare(a.ConnectionID, b.ConnectionID) })
		revisions := map[string]int64{}
		for _, target := range targets {
			if _, ok := revisions[target.ConnectionID]; ok {
				continue
			}
			c, err := q.LockOperationConnection(ctx, database.LockOperationConnectionParams{OrgID: r.OrganizationId, ID: target.ConnectionID})
			if err != nil {
				return err
			}
			if c.DeletedAt.Valid {
				return denied()
			}
			supported, err := s.scheduleRuntimeSupported(ctx, q, r.OrganizationId, c.Provider, r.Spec.Action)
			if err != nil {
				return err
			}
			if !supported {
				return invalid("A selected provider runtime does not support this schedule action.")
			}
			revisions[c.ID] = c.Revision
		}
		if r.Id == "" {
			err = q.CreateSchedule(ctx, database.CreateScheduleParams{ID: id, OrgID: r.OrganizationId, Name: r.Name, EditorID: actor(ctx).UserID, EditorEmail: actor(ctx).Email, IdentityID: randomID(), Spec: spec, NextDue: dbTime(next.Due), NextAction: next.Action, NextLocal: next.Local, NextSkip: next.Skip})
		} else {
			err = q.UpdateSchedule(ctx, database.UpdateScheduleParams{OrgID: r.OrganizationId, ID: id, Name: r.Name, EditorID: actor(ctx).UserID, EditorEmail: actor(ctx).Email, Spec: spec, NextDue: dbTime(next.Due), NextAction: next.Action, NextLocal: next.Local, NextSkip: next.Skip})
		}
		if err != nil {
			return err
		}
		if err = q.ClearScheduleTargets(ctx, database.ClearScheduleTargetsParams{OrgID: r.OrganizationId, ScheduleID: id}); err != nil {
			return err
		}
		for _, t := range targets {
			if err = q.AddScheduleTarget(ctx, database.AddScheduleTargetParams{OrgID: r.OrganizationId, ScheduleID: id, ResourceID: t.ID, ConnectionID: t.ConnectionID, ConnectionRevision: revisions[t.ConnectionID]}); err != nil {
				return err
			}
		}
		if err = q.StampScheduleIdentity(ctx, database.StampScheduleIdentityParams{OrgID: r.OrganizationId, ID: id, EditorOidcID: actor(ctx).OIDC}); err != nil {
			return err
		}
		return audit(ctx, q, r.OrganizationId, actor(ctx).Email, "schedule.saved", id, map[string]any{"name": r.Name, "resources": r.ResourceIds, "action": r.Spec.Action, "approval": "required"})
	})
	if err != nil {
		return nil, err
	}
	return s.scheduleResponse(ctx, r.OrganizationId, id)
}
func (s *Service) ApproveSchedule(ctx context.Context, req *connect.Request[pb.ApproveScheduleRequest]) (*connect.Response[pb.ScheduleResponse], error) {
	r := req.Msg
	err := s.transaction(ctx, func(q *database.Queries) error {
		row, err := lockReviewedSchedule(ctx, q, r.OrganizationId, r.Id, r.ExpectedRevision)
		if err != nil {
			return err
		}
		if row.EditorID == actor(ctx).UserID || !powerPermission(ctx, q, row.OrgID, actor(ctx).UserID, "operations.approve") {
			return denied()
		}
		targets, err := q.ListScheduleTargets(ctx, database.ListScheduleTargetsParams{OrgID: row.OrgID, ScheduleID: row.ID})
		if err != nil {
			return err
		}
		for _, t := range targets {
			c, err := q.LockOperationConnection(ctx, database.LockOperationConnectionParams{OrgID: row.OrgID, ID: t.ConnectionID})
			if err != nil {
				return err
			}
			if err = requireModule(ctx, q, r.OrganizationId, c.Provider, t.ModuleRevision); err != nil {
				return err
			}
			if c.DeletedAt.Valid || t.DeletedAt.Valid || c.Revision != t.ConnectionRevision {
				return conflict("A target or credential changed. Edit and review the schedule again.")
			}
		}
		if _, err = q.LockAccess(ctx, row.OrgID); err != nil {
			return err
		}
		maintenanceRevision, err := q.GetMaintenanceRevision(ctx, row.OrgID)
		if err != nil {
			return err
		}
		if err = q.ApproveSchedule(ctx, database.ApproveScheduleParams{OidcID: actor(ctx).OIDC, OrgID: row.OrgID, ID: row.ID, ApproverID: pgtype.Text{String: actor(ctx).UserID, Valid: true}, MaintenanceRevision: maintenanceRevision}); err != nil {
			return err
		}
		return audit(ctx, q, row.OrgID, actor(ctx).Email, "schedule.approved", row.ID, map[string]any{"revision": row.Revision, "identity": row.IdentityID})
	})
	if err != nil {
		return nil, err
	}
	return s.scheduleResponse(ctx, r.OrganizationId, r.Id)
}
func (s *Service) SetScheduleEnabled(ctx context.Context, req *connect.Request[pb.SetScheduleEnabledRequest]) (*connect.Response[pb.ScheduleResponse], error) {
	r := req.Msg
	err := s.transaction(ctx, func(q *database.Queries) error {
		row, err := lockReviewedSchedule(ctx, q, r.OrganizationId, r.Id, r.ExpectedRevision)
		if err != nil {
			return err
		}
		p, err := q.Permissions(ctx, database.PermissionsParams{OrgID: r.OrganizationId, UserID: actor(ctx).UserID})
		if err != nil || !slices.Contains(p, "schedules.manage") {
			return denied()
		}
		if ((r.Enabled && !row.Enabled) || (r.IdentityEnabled && !row.IdentityEnabled)) && !powerPermission(ctx, q, row.OrgID, actor(ctx).UserID, "operations.request") {
			return denied()
		}
		if err = q.SetScheduleEnabled(ctx, database.SetScheduleEnabledParams{OrgID: row.OrgID, ID: row.ID, Enabled: r.Enabled, IdentityEnabled: r.IdentityEnabled}); err != nil {
			return err
		}
		return audit(ctx, q, row.OrgID, actor(ctx).Email, "schedule.state_changed", row.ID, map[string]any{"enabled": r.Enabled, "identity_enabled": r.IdentityEnabled})
	})
	if err != nil {
		return nil, err
	}
	return s.scheduleResponse(ctx, r.OrganizationId, r.Id)
}
func (s *Service) ListScheduleOccurrences(ctx context.Context, req *connect.Request[pb.ListScheduleOccurrencesRequest]) (*connect.Response[pb.ListScheduleOccurrencesResponse], error) {
	v := req.Msg
	if len(v.PageToken) > 2048 || (v.ScheduleId != "" && !notificationRecordID.MatchString(v.ScheduleId)) || !slices.Contains([]string{"", "queued", "skipped"}, v.Outcome) {
		return nil, invalid("Invalid occurrence filter.")
	}
	filter := "occurrences"
	if v.ScheduleId != "" || v.Outcome != "" {
		filter += ":" + v.ScheduleId + ":" + v.Outcome
	}
	token, err := parseCursor(v.PageToken, v.OrganizationId, filter)
	if err != nil {
		return nil, err
	}
	after := int64(9223372036854775807)
	if token != "" {
		after, err = strconv.ParseInt(token, 10, 64)
		if err != nil || after <= 0 {
			return nil, invalid("Invalid occurrence cursor.")
		}
	}
	rows, err := s.q.ListScheduleOccurrences(ctx, database.ListScheduleOccurrencesParams{OrgID: req.Msg.OrganizationId, ID: after, ScheduleID: v.ScheduleId, Outcome: v.Outcome})
	if err != nil {
		return nil, err
	}
	out := &pb.ListScheduleOccurrencesResponse{}
	if len(rows) > 100 {
		out.NextPageToken = cursor(req.Msg.OrganizationId, filter, strconv.FormatInt(rows[99].ID, 10))
		rows = rows[:100]
	}
	for _, r := range rows {
		out.Occurrences = append(out.Occurrences, &pb.ScheduleOccurrence{Id: strconv.FormatInt(r.ID, 10), ScheduleId: r.ScheduleID, ResourceName: r.ResourceName, ScheduledFor: stamp(r.ScheduledFor), Action: r.Action, Outcome: r.Outcome, Detail: r.Detail, OperationId: r.OperationID.String, OperationStatus: r.OperationStatus, LocalTime: r.LocalTime})
	}
	return connect.NewResponse(out), nil
}
func (s *Service) StartScheduler(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := s.scheduleTick(ctx, time.Now()); err != nil && !errors.Is(err, context.Canceled) {
				s.log.Error().Msg("schedule coordinator failed; missed occurrences will not be replayed")
			}
		}
	}
}
func (s *Service) scheduleTick(ctx context.Context, now time.Time) error {
	return s.transaction(ctx, func(q *database.Queries) error {
		row, err := q.ClaimDueSchedule(ctx)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		spec := new(pb.ScheduleSpec)
		if err = json.Unmarshal(row.Spec, spec); err != nil {
			return err
		}
		if row.NextDue.Time.After(now) {
			if row.ClockVersion >= 2 {
				return nil
			}
			next, err := nextSchedule(spec, now)
			if err != nil {
				return err
			}
			if err = q.RefreshScheduleClock(ctx, database.RefreshScheduleClockParams{OrgID: row.OrgID, ID: row.ID, NextDue: dbTime(next.Due), NextAction: next.Action, NextLocal: next.Local, NextSkip: next.Skip}); err != nil {
				return err
			}
			return audit(ctx, q, row.OrgID, "system:scheduler", "schedule.clock_refreshed", row.ID, nil)
		}
		var next occurrence
		if now.Sub(row.NextDue.Time) > 30*time.Second {
			next, err = nextSchedule(spec, now)
		} else {
			next, err = nextScheduleAfter(spec, row.NextDue.Time, row.NextLocal, row.NextAction)
		}
		if err != nil {
			return err
		}
		targets, err := q.ListScheduleTargets(ctx, database.ListScheduleTargetsParams{OrgID: row.OrgID, ScheduleID: row.ID})
		if err != nil {
			return err
		}
		skip := row.NextSkip
		if !identityAllowed(ctx, q, row.OrgID, row.EditorID, row.EditorOidcID) || !identityAllowed(ctx, q, row.OrgID, row.ApproverID.String, row.ApproverOidcID) {
			skip = "Organization SSO policy no longer authorizes this schedule's publisher or approver."
		}
		if now.Sub(row.NextDue.Time) > 30*time.Second {
			skip = "Missed dispatch window; all overdue occurrences through now were skipped without catch-up."
		}
		if !row.Enabled {
			skip = "Schedule is paused."
		}
		if !row.IdentityEnabled {
			skip = "Automation identity is disabled."
		}
		if !row.ApprovedRevision.Valid || row.ApprovedRevision.Int64 != row.Revision || !row.ApproverID.Valid || row.ApproverID.String == row.EditorID || !powerPermission(ctx, q, row.OrgID, row.ApproverID.String, "operations.approve") {
			skip = "Standing approval is missing, stale, or its approver lost access."
		}
		if row.PolicyVersion != "power-v1" {
			skip = "Schedule policy changed; fresh review required."
		}
		if s.cfg.ProviderCall == nil {
			skip = "Provider executor unavailable."
		}
		connections := map[string]database.Connection{}
		for _, target := range targets {
			if _, ok := connections[target.ConnectionID]; ok {
				continue
			}
			c, e := q.LockOperationConnection(ctx, database.LockOperationConnectionParams{OrgID: row.OrgID, ID: target.ConnectionID})
			if e != nil {
				return e
			}
			connections[c.ID] = c
		}
		queued, skipped := 0, 0
		for _, t := range targets {
			maintenanceRevision, restriction, maintenanceEnd, e := maintenanceCheck(ctx, q, row.OrgID, t.ConnectionID, now)
			if e != nil {
				return e
			}
			detail := skip
			if restriction != "" {
				detail = restriction
			}
			if row.MaintenanceRevision != maintenanceRevision {
				detail = "Maintenance policy changed; fresh schedule approval required."
			}
			operationID := ""
			c := connections[t.ConnectionID]
			allowed, e := moduleAllowed(ctx, q, row.OrgID, c.Provider, t.ModuleRevision)
			if e != nil {
				return e
			}
			if !allowed {
				detail = "Provider module is disabled or changed; edit and review this schedule again."
			}
			supported, e := s.scheduleRuntimeSupported(ctx, q, row.OrgID, c.Provider, row.NextAction)
			if e != nil {
				return e
			}
			if !supported {
				detail = "Provider runtime does not support this action; occurrence skipped."
			}
			if !c.Enabled || c.DeletedAt.Valid || t.DeletedAt.Valid {
				detail = "Connection or resource is disabled or removed."
			}
			if c.Revision != t.ConnectionRevision {
				detail = "Credential scope changed; fresh schedule review required."
			}
			current, e := q.GetResource(ctx, database.GetResourceParams{OrgID: row.OrgID, ID: t.ResourceID})
			if e != nil {
				detail = "Resource no longer available."
			} else if now.Sub(current.ObservedAt.Time) > 6*time.Minute {
				detail = "Inventory is stale; occurrence skipped."
			} else if !provider.PowerAllowed(row.NextAction, current.Status) {
				detail = "Resource is already in the requested state or is not eligible for this action."
			}
			busy, e := q.ResourceOperationBusy(ctx, database.ResourceOperationBusyParams{OrgID: row.OrgID, ResourceID: pgtype.Text{String: t.ResourceID, Valid: true}})
			if e != nil {
				return e
			}
			if busy {
				detail = "Overlapping or uncertain operation; occurrence skipped."
			}
			outcome := "skipped"
			if detail == "" {
				runtime, revision, e := s.runtimeFor(ctx, q, row.OrgID, current.Provider, t.ModuleRevision)
				if e != nil {
					return e
				}
				operationID = newOperationID()
				_, e = q.CreateOperation(ctx, database.CreateOperationParams{TraceParent: traceParent(ctx), RuntimeID: runtime, ModuleRevision: revision, ID: operationID, OrgID: row.OrgID, ResourceID: pgtype.Text{String: t.ResourceID, Valid: true}, ConnectionID: t.ConnectionID, ConnectionRevision: t.ConnectionRevision, Provider: current.Provider, NativeID: current.NativeID, Region: current.Region, ResourceName: current.Name, Action: row.NextAction, ExpectedStatus: current.Status, Reason: "Schedule: " + row.Name, RequesterID: row.EditorID, RequesterEmail: "automation: " + row.Name, Status: "queued", MaintenanceRevision: maintenanceRevision, MaintenanceExpiresAt: dbTime(maintenanceEnd), IdempotencyKey: fmt.Sprintf("%s:%d:%d:%s:%s:%s", row.ID, row.Revision, row.NextDue.Time.UnixMicro(), t.ResourceID, row.NextAction, row.NextLocal)})
				if e != nil {
					return e
				}
				deadline := dbTime(row.NextDue.Time.Add(30 * time.Second))
				if e = q.BindScheduledOperation(ctx, database.BindScheduledOperationParams{OrgID: row.OrgID, ID: operationID, ScheduleID: pgtype.Text{String: row.ID, Valid: true}, ScheduleRevision: pgtype.Int8{Int64: row.Revision, Valid: true}, AutomationIdentityID: pgtype.Text{String: row.IdentityID, Valid: true}, ScheduledFor: row.NextDue, ApproverID: row.ApproverID, ApprovalExpiresAt: deadline}); e != nil {
					return e
				}
				if e = q.StampOperationIdentity(ctx, database.StampOperationIdentityParams{OrgID: row.OrgID, ID: operationID, RequesterOidcID: row.EditorOidcID, ApproverOidcID: row.ApproverOidcID}); e != nil {
					return e
				}
				outcome = "queued"
				detail = "Queued under the reviewed schedule and scoped automation identity."
				queued++
			} else {
				skipped++
			}
			if e = q.AddScheduleOccurrence(ctx, database.AddScheduleOccurrenceParams{OrgID: row.OrgID, ScheduleID: row.ID, Revision: row.Revision, ResourceID: t.ResourceID, ScheduledFor: row.NextDue, Action: row.NextAction, Outcome: outcome, Detail: detail, LocalTime: row.NextLocal, OperationID: pgtype.Text{String: operationID, Valid: operationID != ""}}); e != nil {
				return e
			}
			if e = audit(ctx, q, row.OrgID, "automation:"+row.IdentityID, "schedule.occurrence", row.ID, map[string]any{"resource": t.NativeID, "scheduled_for": stamp(row.NextDue), "local_time": row.NextLocal, "outcome": outcome, "detail": detail, "operation": operationID}); e != nil {
				return e
			}
		}
		outcome := "skipped"
		if queued > 0 {
			outcome = "queued"
			if skipped > 0 {
				outcome = "partial"
			}
		}
		return q.AdvanceSchedule(ctx, database.AdvanceScheduleParams{OrgID: row.OrgID, ID: row.ID, NextDue: dbTime(next.Due), NextAction: next.Action, NextLocal: next.Local, NextSkip: next.Skip, LastOutcome: outcome, LastDetail: fmt.Sprintf("%d queued, %d skipped", queued, skipped)})
	})
}

func (s *Service) DeleteSchedule(ctx context.Context, req *connect.Request[pb.DeleteScheduleRequest]) (*connect.Response[pb.AccessMutationResponse], error) {
	r := req.Msg
	err := s.transaction(ctx, func(q *database.Queries) error {
		row, err := lockReviewedSchedule(ctx, q, r.OrganizationId, r.Id, r.ExpectedRevision)
		if err != nil {
			return err
		}
		if r.Confirmation != row.Name {
			return invalid("Type the exact schedule name to confirm removal.")
		}
		p, err := q.Permissions(ctx, database.PermissionsParams{OrgID: row.OrgID, UserID: actor(ctx).UserID})
		if err != nil || !slices.Contains(p, "schedules.manage") {
			return denied()
		}
		if err = q.DeleteSchedule(ctx, database.DeleteScheduleParams{OrgID: row.OrgID, ID: row.ID}); err != nil {
			return err
		}
		return audit(ctx, q, row.OrgID, actor(ctx).Email, "schedule.deleted", row.ID, nil)
	})
	return connect.NewResponse(&pb.AccessMutationResponse{}), err
}

func lockReviewedSchedule(ctx context.Context, q *database.Queries, org, id string, expected int64) (database.Schedule, error) {
	row, e := q.LockSchedule(ctx, database.LockScheduleParams{OrgID: org, ID: id})
	if e != nil {
		return row, denied()
	}
	if expected < 1 || row.WriteRevision != expected {
		return row, conflict("Schedule changed. Reload and review it before continuing.")
	}
	return row, nil
}
