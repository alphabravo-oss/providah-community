package core

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"

	"connectrpc.com/connect"
	"github.com/alphabravo-oss/providah-community/internal/database"
	pb "github.com/alphabravo-oss/providah-community/internal/gen/providah/v1"
	"github.com/alphabravo-oss/providah-community/sdk/provider"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
)

func newOperationID() string { return fmt.Sprintf("%016x%s", time.Now().UnixMilli(), randomID()[:48]) }
func operationProto(o database.Operation, uid string) *pb.Operation {
	return &pb.Operation{KeyCreation: keyCreationProto(o.Creation), ExpectedTags: resourceTags(o.ExpectedTags), TargetTags: resourceTags(o.TargetTags), TemplateId: o.TemplateID.String, ResourceKind: o.ResourceKind, ExpectedSize: o.ExpectedSize, TargetSize: o.TargetSize, DeletionImpact: o.DeletionImpact, Id: o.ID, ResourceId: o.ResourceID.String, Creation: creationProto(o.Creation), ResourceName: o.ResourceName, NativeId: o.NativeID, Provider: o.Provider, Region: o.Region, Action: o.Action, Status: pb.OperationStatus(pb.OperationStatus_value["OPERATION_STATUS_"+strings.ToUpper(o.Status)]), Requester: o.RequesterEmail, Reason: o.Reason, CreatedAt: stamp(o.CreatedAt), UpdatedAt: stamp(o.UpdatedAt), ApprovalExpiresAt: stamp(o.ApprovalExpiresAt), ProviderActionId: o.ProviderActionID, ObservedStatus: o.ObservedStatus, Detail: o.Detail, MaintenanceExceptionReason: o.MaintenanceExceptionReason, MaintenanceWindowEndsAt: stamp(o.MaintenanceExpiresAt), CanReview: o.RequesterID != uid && o.Status == "awaiting_approval"}
}
func powerPermission(ctx context.Context, q *database.Queries, org, uid, permission string) bool {
	p, err := q.Permissions(ctx, database.PermissionsParams{OrgID: org, UserID: uid})
	return err == nil && slices.Contains(p, permission) && slices.Contains(p, "resources.read")
}

// Keep connection -> operation -> audit lock ordering identical to discovery.
func (s *Service) operationTransaction(ctx context.Context, org, id string, fn func(*database.Queries, database.Operation, database.Connection) error) error {
	original, err := s.q.GetOperation(ctx, database.GetOperationParams{OrgID: org, ID: id})
	if errors.Is(err, pgx.ErrNoRows) {
		return denied()
	}
	if err != nil {
		return err
	}
	return s.transaction(ctx, func(q *database.Queries) error {
		c, err := q.LockOperationConnection(ctx, database.LockOperationConnectionParams{OrgID: org, ID: original.ConnectionID})
		if err != nil {
			return err
		}
		o, err := q.LockOperation(ctx, database.LockOperationParams{OrgID: org, ID: id})
		if err != nil {
			return err
		}
		return fn(q, o, c)
	})
}
func (s *Service) operationResponse(ctx context.Context, org, id string) (*connect.Response[pb.OperationResponse], error) {
	o, err := s.q.GetOperation(ctx, database.GetOperationParams{OrgID: org, ID: id})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, denied()
	}
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(&pb.OperationResponse{Operation: operationProto(o, actor(ctx).UserID)}), nil
}
func (s *Service) GetOperation(ctx context.Context, req *connect.Request[pb.GetOperationRequest]) (*connect.Response[pb.OperationResponse], error) {
	if !regexp.MustCompile(`^[a-f0-9]{64}$`).MatchString(req.Msg.Id) {
		return nil, invalid("Invalid operation ID.")
	}
	return s.operationResponse(ctx, req.Msg.OrganizationId, req.Msg.Id)
}
func (s *Service) ListOperations(ctx context.Context, req *connect.Request[pb.ListOperationsRequest]) (*connect.Response[pb.ListOperationsResponse], error) {
	filter := "operations"
	f := req.Msg.ResourceFilters
	if f != nil {
		if e := validateInventoryView(f); e != nil {
			return nil, e
		}
		permissions, e := s.q.Permissions(ctx, database.PermissionsParams{OrgID: req.Msg.OrganizationId, UserID: actor(ctx).UserID})
		if e != nil || !slices.Contains(permissions, "resources.read") {
			return nil, denied()
		}
		raw, e := json.Marshal(f)
		if e != nil {
			return nil, e
		}
		filter += fmt.Sprintf(":resources:%x", sha256.Sum256(raw))
	} else {
		f = &pb.InventoryViewSpec{}
	}
	after, err := parseCursor(req.Msg.PageToken, req.Msg.OrganizationId, filter)
	if err != nil {
		return nil, err
	}
	if after == "" {
		after = strings.Repeat("f", 64)
	}
	rows, err := s.q.ListOperations(ctx, database.ListOperationsParams{ConnectionID: f.ConnectionId, FilterRegion: f.Region, FilterStatus: f.Status, TagConditions: tagConditionsJSON(f.TagConditions), TagMatchAny: f.TagMatchAny, OrgID: req.Msg.OrganizationId, ID: after, FilterResources: req.Msg.ResourceFilters != nil, Search: f.Search, Provider: f.Provider, Kind: f.Kind, TagKey: f.TagKey, TagValue: f.TagValue, TagName: f.TagName, TagExists: f.TagExists})
	if err != nil {
		return nil, err
	}
	out := &pb.ListOperationsResponse{}
	if len(rows) > 100 {
		out.NextPageToken = cursor(req.Msg.OrganizationId, filter, rows[99].ID)
		rows = rows[:100]
	}
	for _, o := range rows {
		out.Operations = append(out.Operations, operationProto(o, actor(ctx).UserID))
	}
	return connect.NewResponse(out), nil
}
func (s *Service) RequestOperation(ctx context.Context, req *connect.Request[pb.RequestOperationRequest]) (*connect.Response[pb.OperationResponse], error) {
	return s.requestOperation(ctx, req.Msg, nil)
}
func (s *Service) requestOperation(ctx context.Context, r *pb.RequestOperationRequest, review *powerReview) (*connect.Response[pb.OperationResponse], error) {
	if (r.Action == "resize" && !provider.ValidResize(r.ExpectedSize, r.TargetSize)) || (r.Action != "resize" && (r.ExpectedSize != "" || r.TargetSize != "")) {
		return nil, invalid("Choose different valid source and target server types.")
	}
	if len(r.Confirmation) > 200 || len(r.DeletionDigest) > 64 {
		return nil, invalid("Invalid deletion review.")
	}
	r.Reason = strings.TrimSpace(r.Reason)
	r.MaintenanceExceptionReason = strings.TrimSpace(r.MaintenanceExceptionReason)
	if r.MaintenanceExceptionReason != "" && (len(r.MaintenanceExceptionReason) < 10 || len(r.MaintenanceExceptionReason) > 500) {
		return nil, invalid("Explain the maintenance exception in 10–500 characters.")
	}
	if s.cfg.ProviderCall == nil {
		return nil, conflict("Provider execution is not configured. Start the provider launcher.")
	}
	if !slices.Contains([]string{"start", "shutdown", "restart", "resize", "delete", "snapshot", "tags"}, r.Action) || len(r.Reason) < 3 || len(r.Reason) > 500 || !regexp.MustCompile(`^[a-f0-9-]{16,80}$`).MatchString(r.IdempotencyKey) {
		return nil, invalid("Choose an eligible action, a reason, and a valid request key.")
	}
	var expectedTags, targetTags []byte
	if r.Action == "tags" {
		if r.ExpectedTags == nil || r.TargetTags == nil {
			return nil, invalid("Refresh and review complete tag sets.")
		}
		expectedTags, _ = json.Marshal(providerTags(r.ExpectedTags))
		targetTags, _ = json.Marshal(providerTags(r.TargetTags))
	} else if r.ExpectedTags != nil || r.TargetTags != nil {
		return nil, invalid("Unexpected tag inputs.")
	}
	id := ""
	err := s.transaction(ctx, func(q *database.Queries) error {
		resource, err := q.GetResource(ctx, database.GetResourceParams{OrgID: r.OrganizationId, ID: r.ResourceId})
		if err != nil {
			return denied()
		}
		c, err := q.LockOperationConnection(ctx, database.LockOperationConnectionParams{OrgID: r.OrganizationId, ID: resource.ConnectionID})
		if err != nil {
			return err
		}
		if !identityAllowed(ctx, q, r.OrganizationId, actor(ctx).UserID, actor(ctx).OIDC) || !powerPermission(ctx, q, r.OrganizationId, actor(ctx).UserID, "operations.request") {
			return denied()
		}
		if !creationPermitted(ctx, q, r.OrganizationId, r.Action) {
			return conflict("This organization only manages existing resources.")
		}
		if r.Action == "snapshot" && !powerPermission(ctx, q, r.OrganizationId, actor(ctx).UserID, "operations.create") {
			return denied()
		}
		if r.Action == "delete" && (!powerPermission(ctx, q, r.OrganizationId, actor(ctx).UserID, "operations.delete") || r.Confirmation != resource.NativeID) {
			return invalid("Deletion requires delete permission and exact resource ID confirmation.")
		}
		if err := ownershipGuard(ctx, q, r.OrganizationId, c.ID, resource.Kind, resource.NativeID, resource.Region, r.Action); err != nil {
			return err
		}
		if r.Action == "tags" && (resource.Kind != "compute.server" || provider.ValidateTagChange(c.Provider, providerTags(r.ExpectedTags), providerTags(r.TargetTags)) != nil) {
			return invalid("Invalid provider tag change.")
		}
		previous, err := q.OperationByKey(ctx, database.OperationByKeyParams{OrgID: r.OrganizationId, RequesterID: actor(ctx).UserID, IdempotencyKey: r.IdempotencyKey})
		if err == nil {
			if !provider.TagsEqual(storedTags(previous.ExpectedTags), providerTags(r.ExpectedTags)) || !provider.TagsEqual(storedTags(previous.TargetTags), providerTags(r.TargetTags)) || previous.ExpectedSize != r.ExpectedSize || previous.TargetSize != r.TargetSize || previous.ResourceID.String != r.ResourceId || previous.Action != r.Action || previous.ExpectedStatus != r.ExpectedStatus || previous.Reason != r.Reason || previous.MaintenanceExceptionReason != r.MaintenanceExceptionReason || (r.Action == "delete" && provider.ImpactDigest(previous.DeletionImpact) != r.DeletionDigest) {
				return conflict("This request key belongs to different input.")
			}
			id = previous.ID
			return nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		resource, err = q.GetResource(ctx, database.GetResourceParams{OrgID: r.OrganizationId, ID: r.ResourceId})
		if err != nil {
			return denied()
		}
		if err = requireModule(ctx, q, r.OrganizationId, c.Provider, 0); err != nil {
			return err
		}
		if (provider.DatabaseKind(resource.Kind) && (c.Provider != "aws" || resource.ProviderIdentity == "")) || (r.Action == "resize" && resource.Size != r.ExpectedSize) || !c.Enabled || c.DeletedAt.Valid || !provider.ResourceActionAllowed(resource.Kind, r.Action, resource.Status) || resource.Status != r.ExpectedStatus || time.Since(resource.ObservedAt.Time) > 6*time.Minute {
			return conflict("Refresh the resource and review its current state before requesting this action.")
		}
		if r.Action == "tags" && !provider.TagsEqual(storedTags(resource.TagMetadata), providerTags(r.ExpectedTags)) {
			return conflict("Resource tags changed or were not collected. Refresh before editing.")
		}
		maintenanceRevision, restriction, maintenanceEnd, err := maintenanceCheck(ctx, q, r.OrganizationId, c.ID, time.Now())
		if err != nil {
			return err
		}
		if r.MaintenanceExceptionReason != "" {
			if !hasPermission(ctx, q, r.OrganizationId, actor(ctx).UserID, "maintenance.override") {
				return denied()
			}
		} else if restriction != "" {
			return conflict(restriction)
		}
		status := "queued"
		if approvalRequired(ctx, q, r.OrganizationId, r.Action, r.MaintenanceExceptionReason != "") {
			status = "awaiting_approval"
		}
		id = newOperationID()
		runtime, revision, err := s.runtimeFor(ctx, q, r.OrganizationId, c.Provider, 0)
		if err != nil {
			return err
		}
		if !s.runtimeCapabilities(c.Provider, runtime).SupportsResourceAction(resource.Kind, r.Action) {
			return conflict("Selected runtime does not support this action.")
		}
		if review != nil && (review.ConnectionRevision != c.Revision || review.ModuleRevision != revision || review.Runtime != runtime || review.MaintenanceRevision != maintenanceRevision || review.WindowEnd != maintenanceEnd.Unix()) {
			return conflict("The reviewed credential, runtime, or maintenance policy changed. Preview this target again.")
		}
		impact := ""
		if r.Action == "delete" {
			impact, err = s.deletionImpact(ctx, q, c, resource, runtime)
			if err != nil {
				return err
			}
			if provider.ImpactDigest(impact) != r.DeletionDigest {
				return conflict("Deletion impact changed. Preview and review it again.")
			}
		}
		_, err = q.CreateOperation(ctx, database.CreateOperationParams{TraceParent: traceParent(ctx), ExpectedTags: expectedTags, TargetTags: targetTags, ProviderIdentity: resource.ProviderIdentity, ExpectedSize: r.ExpectedSize, TargetSize: r.TargetSize, ResourceKind: resource.Kind, DeletionImpact: impact, RuntimeID: runtime, ModuleRevision: revision, ID: id, OrgID: r.OrganizationId, ResourceID: pgtype.Text{String: r.ResourceId, Valid: true}, ConnectionID: c.ID, ConnectionRevision: c.Revision, Provider: c.Provider, NativeID: resource.NativeID, Region: resource.Region, ResourceName: resource.Name, Action: r.Action, ExpectedStatus: r.ExpectedStatus, Reason: r.Reason, RequesterID: actor(ctx).UserID, RequesterEmail: actor(ctx).Email, Status: status, IdempotencyKey: r.IdempotencyKey, MaintenanceRevision: maintenanceRevision, MaintenanceExceptionReason: r.MaintenanceExceptionReason, MaintenanceExpiresAt: dbTime(maintenanceEnd)})
		if err != nil {
			var pe *pgconn.PgError
			if errors.As(err, &pe) && pe.Code == "23505" {
				return conflict("This resource already has pending or uncertain work. Resolve it before requesting another action.")
			}
			return err
		}
		if err = q.StampOperationIdentity(ctx, database.StampOperationIdentityParams{OrgID: r.OrganizationId, ID: id, RequesterOidcID: actor(ctx).OIDC}); err != nil {
			return err
		}
		return audit(ctx, q, r.OrganizationId, actor(ctx).Email, "operation.requested", id, map[string]any{"resource": resource.NativeID, "action": r.Action, "status": status, "reason": r.Reason, "maintenance_exception": r.MaintenanceExceptionReason, "maintenance_revision": maintenanceRevision, "deletion_impact": impact, "expected_size": r.ExpectedSize, "target_size": r.TargetSize})
	})
	if err != nil {
		return nil, err
	}
	return s.operationResponse(ctx, r.OrganizationId, id)
}
func (s *Service) ReviewOperation(ctx context.Context, req *connect.Request[pb.ReviewOperationRequest]) (*connect.Response[pb.OperationResponse], error) {
	r := req.Msg
	reason := strings.TrimSpace(r.Reason)
	if len(reason) < 3 || len(reason) > 500 {
		return nil, invalid("Provide a review reason.")
	}
	err := s.operationTransaction(ctx, r.OrganizationId, r.Id, func(q *database.Queries, o database.Operation, c database.Connection) error {
		if !powerPermission(ctx, q, o.OrgID, actor(ctx).UserID, "operations.approve") || o.RequesterID == actor(ctx).UserID {
			return denied()
		}
		if (o.Action == "delete" || o.Action == "create" || o.Action == "snapshot") && !powerPermission(ctx, q, o.OrgID, actor(ctx).UserID, operationAuthority(o.Action)) {
			return denied()
		}
		if o.Status != "awaiting_approval" || o.ExpiresAt.Time.Before(time.Now()) {
			return conflict("This request is no longer awaiting approval.")
		}
		status := "rejected"
		if r.Approve {
			if !creationPermitted(ctx, q, o.OrgID, o.Action) {
				return conflict("This organization only manages existing resources.")
			}
			if err := requireModule(ctx, q, o.OrgID, c.Provider, o.ModuleRevision); err != nil {
				return err
			}
			revision, restriction, _, err := maintenanceCheck(ctx, q, o.OrgID, c.ID, time.Now())
			if err != nil {
				return err
			}
			if o.MaintenanceExceptionReason == "" && o.MaintenanceExpiresAt.Valid && !time.Now().Before(o.MaintenanceExpiresAt.Time) {
				return conflict("The originally reviewed maintenance window ended. Request a new operation.")
			}
			if revision != o.MaintenanceRevision {
				return conflict("Maintenance policy changed. Request a new operation for fresh review.")
			}
			if o.MaintenanceExceptionReason != "" {
				if !hasPermission(ctx, q, o.OrgID, actor(ctx).UserID, "maintenance.override") {
					return denied()
				}
			} else if restriction != "" {
				return conflict(restriction)
			}
			resource, err := q.GetResource(ctx, database.GetResourceParams{OrgID: o.OrgID, ID: o.ResourceID.String})
			if (o.Action != "create" && (err != nil || resource.Status != o.ExpectedStatus)) || c.Revision != o.ConnectionRevision || !c.Enabled || c.DeletedAt.Valid {
				return conflict("The target or credential changed. Request a new operation.")
			}
			status = "queued"
		}
		if err := q.ReviewOperation(ctx, database.ReviewOperationParams{OidcID: actor(ctx).OIDC, OrgID: o.OrgID, ID: o.ID, Status: status, ApproverID: pgtype.Text{String: actor(ctx).UserID, Valid: true}, Detail: reason}); err != nil {
			return err
		}
		return audit(ctx, q, o.OrgID, actor(ctx).Email, "operation.reviewed", o.ID, map[string]any{"decision": status, "reason": reason})
	})
	if err != nil {
		return nil, err
	}
	return s.operationResponse(ctx, r.OrganizationId, r.Id)
}
func (s *Service) savePowerOutcome(ctx context.Context, q *database.Queries, o database.Operation, status, detail string, result *provider.PowerResult) error {
	actionID, observed := o.ProviderActionID, o.ObservedStatus
	if result != nil {
		if result.ActionID != "" {
			actionID = result.ActionID
		}
		if result.Status != "" {
			observed = result.Status
		}
	}
	if err := q.SaveOperationOutcome(ctx, database.SaveOperationOutcomeParams{OrgID: o.OrgID, ID: o.ID, Status: status, ProviderActionID: actionID, ObservedStatus: observed, Detail: detail}); err != nil {
		return err
	}
	if o.Action == "tags" && status == "succeeded" && result != nil && provider.TagsEqual(result.Tags, storedTags(o.TargetTags)) {
		raw, e := json.Marshal(result.Tags)
		if e != nil {
			return e
		}
		if e = q.UpdateObservedServerTags(ctx, database.UpdateObservedServerTagsParams{OrgID: o.OrgID, ConnectionID: o.ConnectionID, NativeID: o.NativeID, Region: o.Region, TagMetadata: raw}); e != nil {
			return e
		}
	}
	if o.Action == "delete" && status == "succeeded" {
		var err error
		if o.ResourceKind == "compute.placement_group" {
			err = q.MarkPlacementGroupDeleted(ctx, database.MarkPlacementGroupDeletedParams{OrgID: o.OrgID, ConnectionID: o.ConnectionID, NativeID: o.NativeID, Region: o.Region})
		} else if o.ResourceKind == "organization.project" {
			err = q.MarkCloudProjectDeleted(ctx, database.MarkCloudProjectDeletedParams{OrgID: o.OrgID, ConnectionID: o.ConnectionID, NativeID: o.NativeID})
		} else if o.ResourceKind == "compute.image" {
			err = q.MarkImageDeleted(ctx, database.MarkImageDeletedParams{OrgID: o.OrgID, ConnectionID: o.ConnectionID, NativeID: o.NativeID, Region: o.Region})
		} else if provider.DatabaseSnapshotKind(o.ResourceKind) {
			err = q.MarkDatabaseSnapshotDeleted(ctx, database.MarkDatabaseSnapshotDeletedParams{OrgID: o.OrgID, ConnectionID: o.ConnectionID, NativeID: o.NativeID, Kind: o.ResourceKind, Region: o.Region})
		} else if o.ResourceKind == "storage.snapshot" {
			err = q.MarkSnapshotDeleted(ctx, database.MarkSnapshotDeletedParams{OrgID: o.OrgID, ConnectionID: o.ConnectionID, NativeID: o.NativeID})
		} else if o.ResourceKind == "access.ssh_key" {
			err = q.MarkSSHKeyDeleted(ctx, database.MarkSSHKeyDeletedParams{OrgID: o.OrgID, ConnectionID: o.ConnectionID, NativeID: o.NativeID, Region: o.Region})
		} else if provider.NetworkDeleteKind(o.ResourceKind) || o.ResourceKind == "network.load_balancer" {
			err = q.MarkNetworkDeleted(ctx, database.MarkNetworkDeletedParams{OrgID: o.OrgID, ConnectionID: o.ConnectionID, NativeID: o.NativeID, Kind: o.ResourceKind, Region: o.Region})
		} else if o.ResourceKind == "storage.volume" {
			err = q.MarkVolumeDeleted(ctx, database.MarkVolumeDeletedParams{OrgID: o.OrgID, ConnectionID: o.ConnectionID, NativeID: o.NativeID})
		} else {
			err = q.MarkServerDeleted(ctx, database.MarkServerDeletedParams{OrgID: o.OrgID, ID: o.ResourceID.String})
		}
		if err != nil {
			return err
		}
	}
	return audit(ctx, q, o.OrgID, "system:operations", "operation."+status, o.ID, map[string]any{"detail": detail, "provider_action_id": actionID, "observed_status": observed})
}
func (s *Service) CancelOperation(ctx context.Context, req *connect.Request[pb.CancelOperationRequest]) (*connect.Response[pb.OperationResponse], error) {
	r := req.Msg
	err := s.operationTransaction(ctx, r.OrganizationId, r.Id, func(q *database.Queries, o database.Operation, c database.Connection) error {
		if !powerPermission(ctx, q, o.OrgID, actor(ctx).UserID, "operations.request") || o.RequesterID != actor(ctx).UserID {
			return denied()
		}
		if o.Status != "queued" && o.Status != "awaiting_approval" {
			return conflict("Only work not yet dispatched can be canceled.")
		}
		if err := s.savePowerOutcome(ctx, q, o, "canceled", "Canceled before dispatch.", nil); err != nil {
			return err
		}
		return audit(ctx, q, o.OrgID, actor(ctx).Email, "operation.canceled_by_requester", o.ID, nil)
	})
	if err != nil {
		return nil, err
	}
	return s.operationResponse(ctx, r.OrganizationId, r.Id)
}
func (s *Service) ReconcileOperation(ctx context.Context, req *connect.Request[pb.ReconcileOperationRequest]) (*connect.Response[pb.OperationResponse], error) {
	r := req.Msg
	err := s.operationTransaction(ctx, r.OrganizationId, r.Id, func(q *database.Queries, o database.Operation, c database.Connection) error {
		if !powerPermission(ctx, q, o.OrgID, actor(ctx).UserID, "operations.request") {
			return denied()
		}
		if o.Status != "uncertain" || o.LeaseUntil.Time.After(time.Now()) {
			return conflict("Wait for the worker lease to expire before checking uncertain work.")
		}
		if !c.Enabled || c.Revision != o.ConnectionRevision || c.DeletedAt.Valid {
			return conflict("The connection changed. Verify the provider state and resolve this operation manually.")
		}
		if err := q.RecheckOperation(ctx, database.RecheckOperationParams{OrgID: o.OrgID, ID: o.ID}); err != nil {
			return err
		}
		return audit(ctx, q, o.OrgID, actor(ctx).Email, "operation.reconciliation_requested", o.ID, nil)
	})
	if err != nil {
		return nil, err
	}
	return s.operationResponse(ctx, r.OrganizationId, r.Id)
}
func (s *Service) ResolveOperation(ctx context.Context, req *connect.Request[pb.ResolveOperationRequest]) (*connect.Response[pb.OperationResponse], error) {
	r := req.Msg
	reason := strings.TrimSpace(r.Reason)
	if len(reason) < 10 || len(reason) > 500 {
		return nil, invalid("Describe how you verified the provider state (10–500 characters).")
	}
	err := s.operationTransaction(ctx, r.OrganizationId, r.Id, func(q *database.Queries, o database.Operation, c database.Connection) error {
		if !powerPermission(ctx, q, o.OrgID, actor(ctx).UserID, "operations.approve") || o.RequesterID == actor(ctx).UserID || ((o.Action == "delete" || o.Action == "create" || o.Action == "snapshot") && !powerPermission(ctx, q, o.OrgID, actor(ctx).UserID, operationAuthority(o.Action))) {
			return denied()
		}
		if o.Status != "uncertain" || o.LeaseUntil.Time.After(time.Now()) {
			return conflict("Wait until uncertain work is outside its worker lease before resolving it.")
		}
		if err := s.savePowerOutcome(ctx, q, o, "resolved", reason, nil); err != nil {
			return err
		}
		return audit(ctx, q, o.OrgID, actor(ctx).Email, "operation.manually_resolved", o.ID, map[string]any{"reason": reason})
	})
	if err != nil {
		return nil, err
	}
	return s.operationResponse(ctx, r.OrganizationId, r.Id)
}
func (s *Service) StartOperations(ctx context.Context) {
	if s.cfg.ProviderCall == nil {
		return
	}
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := s.operationTick(ctx); err != nil && !errors.Is(err, context.Canceled) {
				s.log.Error().Msg("operation coordinator failed; ambiguous actions will not be resubmitted")
			}
		}
	}
}
func (s *Service) operationTick(ctx context.Context) error {
	pending, err := s.q.ExpiredPendingOperations(ctx)
	if err != nil {
		return err
	}
	dispatched, err := s.q.ExpiredDispatches(ctx)
	if err != nil {
		return err
	}
	for _, job := range append(pending, dispatched...) {
		if err = s.operationTransaction(ctx, job.OrgID, job.ID, func(q *database.Queries, o database.Operation, c database.Connection) error {
			if o.Status == "dispatching" && o.LeaseUntil.Time.Before(time.Now()) {
				return s.savePowerOutcome(ctx, q, o, "uncertain", "Worker ended without a confirmed outcome. Check provider state; do not retry blindly.", nil)
			}
			if (o.Status == "queued" || o.Status == "awaiting_approval") && (o.ExpiresAt.Time.Before(time.Now()) || o.ApprovalExpiresAt.Valid && o.ApprovalExpiresAt.Time.Before(time.Now())) {
				return s.savePowerOutcome(ctx, q, o, "expired", "Request or approval expired before dispatch.", nil)
			}
			return nil
		}); err != nil {
			return err
		}
	}
	job, err := s.q.ClaimOperation(ctx)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	return s.runOperation(ctx, job)
}
func (s *Service) runOperation(ctx context.Context, job database.Operation) error {
	ctx, span := s.telemetry.tracer.Start(workContext(ctx, job.TraceParent), "operation.run")
	defer span.End()
	var request provider.Request
	dispatch := job.Status == "dispatching"
	ready := false
	err := s.operationTransaction(ctx, job.OrgID, job.ID, func(q *database.Queries, o database.Operation, c database.Connection) error {
		if !o.LeaseUntil.Valid || !o.LeaseUntil.Time.Equal(job.LeaseUntil.Time) || o.Status != job.Status || o.LeaseUntil.Time.Before(time.Now()) {
			return nil
		}
		failure := ""
		if o.ScheduleID.Valid {
			allowed, e := q.ScheduleAuthorizesOperation(ctx, database.ScheduleAuthorizesOperationParams{OrgID: o.OrgID, ID: o.ScheduleID.String, Revision: o.ScheduleRevision.Int64, IdentityID: o.AutomationIdentityID.String, ResourceID: o.ResourceID.String, ConnectionRevision: o.ConnectionRevision, Action: o.Action})
			if e != nil {
				return e
			}
			if !allowed {
				failure = "Schedule or scoped automation identity was revoked or changed."
			}
		} else if !powerPermission(ctx, q, o.OrgID, o.RequesterID, "operations.request") {
			failure = "Requester access was revoked."
		}
		if (o.Action == "delete" || o.Action == "create" || o.Action == "snapshot") && (!powerPermission(ctx, q, o.OrgID, o.RequesterID, operationAuthority(o.Action)) || (o.ApproverID.Valid && !powerPermission(ctx, q, o.OrgID, o.ApproverID.String, operationAuthority(o.Action)))) {
			failure = "Action authority was revoked."
		}
		if !c.Enabled || c.DeletedAt.Valid || c.Revision != o.ConnectionRevision {
			failure = "Connection or credential changed."
		}
		if dispatch && !creationPermitted(ctx, q, o.OrgID, o.Action) {
			failure = "Organization resource creation was disabled."
		}
		if dispatch && !s.runtimeCapabilities(o.Provider, o.RuntimeID).SupportsResourceAction(o.ResourceKind, o.Action) {
			failure = "Original runtime does not support this action."
		}
		if dispatch {
			allowed, e := moduleAllowed(ctx, q, o.OrgID, c.Provider, o.ModuleRevision)
			if e != nil {
				return e
			}
			if !allowed {
				failure = "Provider module was disabled or changed; create a fresh request."
			}
		}

		if dispatch {
			if o.TemplateID.Valid {
				template, e := q.GetServerTemplate(ctx, database.GetServerTemplateParams{OrgID: o.OrgID, ID: o.TemplateID.String})
				if e != nil || template.Status == "revoked" || !hasPermission(ctx, q, o.OrgID, o.RequesterID, "templates.read") {
					failure = "Template was revoked or template access was removed."
				}
			}
			revision, restriction, _, e := maintenanceCheck(ctx, q, o.OrgID, c.ID, time.Now())
			if e != nil {
				return e
			}
			if o.MaintenanceExceptionReason == "" && o.MaintenanceExpiresAt.Valid && !time.Now().Before(o.MaintenanceExpiresAt.Time) {
				failure = "The originally reviewed maintenance window ended; fresh review is required."
			}
			if revision != o.MaintenanceRevision {
				failure = "Maintenance policy changed; fresh review is required."
			} else if o.MaintenanceExceptionReason == "" && restriction != "" {
				failure = restriction
			}
			if o.MaintenanceExceptionReason != "" && (!hasPermission(ctx, q, o.OrgID, o.RequesterID, "maintenance.override") || (o.ApproverID.Valid && !hasPermission(ctx, q, o.OrgID, o.ApproverID.String, "maintenance.override"))) {
				failure = "Maintenance exception authority was revoked."
			}
			resource, e := q.GetResource(ctx, database.GetResourceParams{OrgID: o.OrgID, ID: o.ResourceID.String})
			if o.Action != "create" && (e != nil || resource.Kind != o.ResourceKind || resource.ProviderIdentity != o.ProviderIdentity || resource.Status != o.ExpectedStatus || (o.Action == "resize" && resource.Size != o.ExpectedSize) || (o.Action == "tags" && !provider.TagsEqual(storedTags(resource.TagMetadata), storedTags(o.ExpectedTags)))) {
				failure = "Resource state changed before dispatch."
			}
			if o.ExpiresAt.Time.Before(time.Now()) || o.PolicyVersion != "power-v1" {
				failure = "Request or policy expired."
			}
			if (approvalRequired(ctx, q, o.OrgID, o.Action, o.MaintenanceExceptionReason != "") || o.ApproverID.Valid || o.ScheduleID.Valid) && (!o.ApproverID.Valid || o.ApproverID.String == o.RequesterID || !o.ApprovalExpiresAt.Valid || o.ApprovalExpiresAt.Time.Before(time.Now()) || !powerPermission(ctx, q, o.OrgID, o.ApproverID.String, "operations.approve")) {
				failure = "Approval is required by current policy, expired, or approver access was revoked. Submit a fresh request."
			}
		} else if o.ObservationDeadline.Valid && o.ObservationDeadline.Time.Before(time.Now()) {
			failure = "Provider completion could not be confirmed within fifteen minutes."
		}
		if !s.runtimeAvailable(o.Provider, o.RuntimeID) {
			failure = "The original provider runtime is unavailable; restore its approved image before observing this operation."
		}
		if dispatch && (!identityAllowed(ctx, q, o.OrgID, o.RequesterID, o.RequesterOidcID) || o.ApproverID.Valid && !identityAllowed(ctx, q, o.OrgID, o.ApproverID.String, o.ApproverOidcID)) {
			failure = "Organization SSO policy no longer authorizes the request or approval identity."
		}
		if dispatch && o.Action != "create" {
			if err := ownershipGuard(ctx, q, o.OrgID, o.ConnectionID, o.ResourceKind, o.NativeID, o.Region, o.Action); err != nil {
				if connect.CodeOf(err) != connect.CodeFailedPrecondition {
					return err
				}
				failure = "Managed IaC state protects this resource. Change its owning configuration."
			}
		}
		if failure != "" {
			status := "uncertain"
			if dispatch {
				status = "canceled"
			}
			return s.savePowerOutcome(ctx, q, o, status, failure, nil)
		}
		credential, e := s.open(c.Ciphertext)
		if e != nil {
			return e
		}
		credential, e = s.providerCredential(ctx, o.Provider, credential, o.Region, o.OrgID, o.ConnectionID)
		if e != nil {
			status := "uncertain"
			if dispatch {
				status = "failed"
			}
			return s.savePowerOutcome(ctx, q, o, status, "AWS authentication failed; no provider request sent.", nil)
		}
		phase := "observe"
		if dispatch {
			phase = "submit"
		}
		request = provider.Request{RuntimeID: o.RuntimeID, Version: provider.Protocol, OrganizationID: o.OrgID, ConnectionID: o.ConnectionID, Provider: o.Provider, Region: o.Region, Credential: credential, Power: &provider.PowerRequest{ExpectedTags: storedTags(o.ExpectedTags), TargetTags: storedTags(o.TargetTags), ExpectedIdentity: o.ProviderIdentity, ExpectedSize: o.ExpectedSize, TargetSize: o.TargetSize, DeletionImpact: o.DeletionImpact, OperationID: o.ID, Phase: phase, Action: o.Action, NativeID: o.NativeID, ExpectedStatus: o.ExpectedStatus, ActionID: o.ProviderActionID}}
		if o.ResourceKind != "compute.server" {
			request.Power.ResourceKind = o.ResourceKind
		}
		if o.Action == "create" && o.ResourceKind == "access.ssh_key" {
			request.Power.KeyCreate = &provider.SSHKeyCreate{}
			if e = json.Unmarshal(o.Creation, request.Power.KeyCreate); e != nil {
				return e
			}
		} else if o.Action == "create" {
			request.Power.Create = &provider.ServerCreate{}
			if e = json.Unmarshal(o.Creation, request.Power.Create); e != nil {
				return e
			}
		}
		if e = request.Validate(); e != nil {
			return s.savePowerOutcome(ctx, q, o, "failed", "Provider target could not be validated; no request sent.", nil)
		}
		ready = true
		if dispatch {
			return audit(ctx, q, o.OrgID, "system:operations", "operation.dispatching", o.ID, map[string]any{"resource": o.NativeID, "action": o.Action})
		}
		return nil
	})
	if err != nil || !ready {
		return err
	}
	callCtx, cancel := context.WithTimeout(ctx, 140*time.Second)
	result, callErr := s.callProvider(callCtx, request)
	cancel()
	return s.operationTransaction(ctx, job.OrgID, job.ID, func(q *database.Queries, o database.Operation, c database.Connection) error {
		if o.Status != job.Status || !o.LeaseUntil.Time.Equal(job.LeaseUntil.Time) || o.LeaseUntil.Time.Before(time.Now()) {
			return nil
		}
		status, detail := "uncertain", "Provider response unavailable; external effects are uncertain."
		var power *provider.PowerResult
		if callErr == nil && result.Validate() == nil && result.Power != nil && result.Power.Outcome != "preview" {
			power = result.Power
			status = power.Outcome
			detail = map[string]string{"invalid_configuration": "Provider configuration is invalid; no action was submitted.", "preflight_failed": "Could not read the target before dispatch; no action was submitted.", "state_changed": "The provider state changed after review; no action was submitted.", "submission_uncertain": "Submission response was lost. External effects are uncertain; do not retry blindly.", "observation_failed": "Provider observation is temporarily unavailable.", "provider_failed": "The provider reported that this action failed."}[power.Error]
			if status == "accepted" {
				status = "observing"
				detail = "Submitted; waiting for provider confirmation."
				if power.Error != "" {
					detail = "Provider observation unavailable; only reads will be retried."
				}
			}
			if status == "succeeded" {
				detail = "Provider completion or the requested power state was observed."
			}
			if power.Error == "restart_unverifiable" {
				detail = "Restart was submitted, but completion cannot be proved from the available provider response. Verify it before resolving."
			}
		} else if !dispatch {
			status = "observing"
			detail = "Provider observation unavailable; no action will be resubmitted."
		}
		if slices.Contains([]string{"start", "shutdown", "restart"}, o.Action) && status == "succeeded" && (power == nil || !provider.ResourceActionReached(o.ResourceKind, o.Action, power.Status)) {
			status = "uncertain"
			detail = "The requested resource state was not confirmed by the provider."
		}
		if o.Action == "snapshot" && (status == "succeeded" || status == "observing") && power != nil {
			if power.Error == "" && (power.ActionID == "" || (o.ProviderActionID != "" && power.ActionID != o.ProviderActionID) || (status == "succeeded" && power.Status != "snapshot_available")) {
				status = "uncertain"
				detail = "Snapshot completion could not be confirmed."
				power = nil
			}
		}
		if o.Action == "tags" && status == "succeeded" && (power == nil || !provider.TagsEqual(power.Tags, storedTags(o.TargetTags))) {
			status = "uncertain"
			detail = "Requested tags were not confirmed by the provider."
		}
		if o.Action == "resize" && status == "succeeded" && (power == nil || power.Size != o.TargetSize) {
			status = "uncertain"
			detail = "Requested server type was not confirmed by the provider."
		}
		if o.Action == "delete" && status == "succeeded" && (power == nil || !provider.PowerReached("delete", power.Status)) {
			status = "uncertain"
			detail = "Deletion could not be confirmed from the provider observation."
		}
		if o.Action == "create" && power != nil {
			if dispatch && o.ResourceKind == "access.ssh_key" && status == "succeeded" {
				status, power.Outcome = "observing", "accepted"
				detail = "Public key import submitted; waiting for read-only confirmation."
			}
			if (power.NativeID != "" && o.NativeID != "" && power.NativeID != o.NativeID) || ((status == "succeeded" || status == "observing") && power.Error == "" && power.NativeID == "") || (status == "succeeded" && ((o.ResourceKind == "access.ssh_key" && power.Status != "present") || (o.ResourceKind != "access.ssh_key" && !provider.PowerReached("start", power.Status)))) {
				status = "uncertain"
				detail = "Created resource identity or completion could not be confirmed."
				power = nil
			}
			if power != nil && (status == "observing" || status == "succeeded") {
				if err := bindCreatedResource(ctx, q, o, power); err != nil {
					return err
				}
			}
		}
		if err := s.savePowerOutcome(ctx, q, o, status, detail, power); err != nil {
			return err
		}
		return q.RefreshAfterOperation(ctx, database.RefreshAfterOperationParams{OrgID: o.OrgID, ID: o.ConnectionID})
	})
}

func (s *Service) GetOperationsOverview(ctx context.Context, r *connect.Request[pb.GetOperationsOverviewRequest]) (*connect.Response[pb.OperationsOverview], error) {
	counts, e := s.q.OperationOverviewCounts(ctx, r.Msg.OrganizationId)
	if e != nil {
		return nil, e
	}
	approvals, e := s.q.ListOverviewOperations(ctx, database.ListOverviewOperationsParams{OrgID: r.Msg.OrganizationId, Statuses: []string{"awaiting_approval"}})
	if e != nil {
		return nil, e
	}
	problems, e := s.q.ListOverviewOperations(ctx, database.ListOverviewOperationsParams{OrgID: r.Msg.OrganizationId, Statuses: []string{"failed", "uncertain"}})
	if e != nil {
		return nil, e
	}
	out := &pb.OperationsOverview{Pending: counts.Pending, Active: counts.Active, Failed: counts.Failed, Uncertain: counts.Uncertain}
	for _, row := range approvals {
		out.Approvals = append(out.Approvals, operationProto(row, actor(ctx).UserID))
	}
	for _, row := range problems {
		out.Problems = append(out.Problems, operationProto(row, actor(ctx).UserID))
	}
	return connect.NewResponse(out), nil
}

func operationAuthority(action string) string {
	if action == "snapshot" {
		return "operations.create"
	}
	return "operations." + action
}

func providerTags(tags *pb.ResourceTags) *provider.Tags {
	if tags == nil {
		return nil
	}
	return &provider.Tags{Labels: tags.Labels, Names: tags.Names}
}
func storedTags(raw []byte) *provider.Tags {
	if len(raw) == 0 {
		return nil
	}
	var tags *provider.Tags
	if json.Unmarshal(raw, &tags) != nil {
		return nil
	}
	return tags
}
