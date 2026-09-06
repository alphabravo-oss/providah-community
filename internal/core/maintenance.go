package core

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"time"

	"connectrpc.com/connect"
	"github.com/alphabravo-oss/providah-community/internal/database"
	pb "github.com/alphabravo-oss/providah-community/internal/gen/providah/v1"
)

// Windows use elapsed duration, inclusive start and exclusive end. Exceptions
// close the entire local date, including windows carried over from the prior day.
func maintenanceWindow(spec *pb.ScheduleSpec, minutes int32, at time.Time) (time.Time, error) {
	if err := validateScheduleSpec(spec); err != nil {
		return time.Time{}, err
	}
	if minutes < 1 || minutes > 1440 {
		return time.Time{}, invalid("Use a duration from 1 to 1,440 minutes.")
	}
	loc, _ := time.LoadLocation(spec.Timezone)
	local := at.In(loc)
	if slices.Contains(spec.Exceptions, local.Format("2006-01-02")) {
		return time.Time{}, nil
	}
	after := at.Add(-time.Duration(minutes) * time.Minute)
	parsed, err := scheduleParser.Parse(spec.Cron)
	if err != nil {
		return time.Time{}, err
	}
	// Enumerate nominal starts, then resolve each independently. A skipped DST
	// start must not hide a later valid start whose UTC instant occurs earlier.
	_, minimum := at.In(loc).Zone()
	maximum := minimum
	for _, anchor := range []time.Time{after, at} {
		for _, hours := range []int{-48, 0, 48} {
			_, offset := anchor.Add(time.Duration(hours) * time.Hour).In(loc).Zone()
			minimum = min(minimum, offset)
			maximum = max(maximum, offset)
		}
	}
	padding := time.Duration(maximum-minimum) * time.Second
	wallClock := func(t time.Time) time.Time {
		v := t.In(loc)
		return time.Date(v.Year(), v.Month(), v.Day(), v.Hour(), v.Minute(), v.Second(), v.Nanosecond(), time.UTC)
	}
	wall, limit := wallClock(after).Add(-padding), wallClock(at).Add(padding)
	var until time.Time
	for i := 0; i < 6000; i++ {
		wall = parsed.Next(wall)
		if wall.IsZero() || wall.After(limit) {
			break
		}
		due, missing := resolveWall(wall, loc)
		if !missing && due.After(after) && !due.After(at) && !slices.Contains(spec.Exceptions, wall.Format("2006-01-02")) {
			end := due.Add(time.Duration(minutes) * time.Minute)
			if end.After(until) {
				until = end
			}
		}
	}
	// A closed next local date clips windows that otherwise cross midnight.
	nextDay := time.Date(local.Year(), local.Month(), local.Day()+1, 0, 0, 0, 0, time.UTC)
	if !until.IsZero() && slices.Contains(spec.Exceptions, nextDay.Format("2006-01-02")) {
		midnight, _ := resolveWall(nextDay, loc)
		if midnight.Before(until) {
			until = midnight
		}
	}
	return until, nil
}
func maintenanceOpen(spec *pb.ScheduleSpec, minutes int32, at time.Time) (bool, error) {
	until, err := maintenanceWindow(spec, minutes, at)
	return until.After(at), err
}
func maintenanceRestriction(policies []database.ListMaintenancePoliciesRow, connection string, at time.Time) (string, time.Time, error) {
	var until time.Time
	for _, p := range policies {
		if len(p.ConnectionIds) > 0 && !slices.Contains(p.ConnectionIds, connection) {
			continue
		}
		spec := new(pb.ScheduleSpec)
		if err := json.Unmarshal(p.Spec, spec); err != nil {
			return "", until, err
		}
		end, err := maintenanceWindow(spec, p.DurationMinutes, at)
		if err != nil {
			return "", until, err
		}
		if !end.After(at) {
			return "Maintenance window closed: " + p.Name + ". Request fresh review; this action will not wait for a later window.", time.Time{}, nil
		}
		if until.IsZero() || end.Before(until) {
			until = end
		}
	}
	return "", until, nil
}

// Serialize policy snapshots with edits, after connection locks and before audit.
// Release the transaction before contacting the provider.
func maintenanceCheck(ctx context.Context, q *database.Queries, org, connection string, at time.Time) (int64, string, time.Time, error) {
	if _, err := q.LockAccess(ctx, org); err != nil {
		return 0, "", time.Time{}, err
	}
	revision, err := q.GetMaintenanceRevision(ctx, org)
	if err != nil {
		return 0, "", time.Time{}, err
	}
	policies, err := q.ListMaintenancePolicies(ctx, org)
	if err != nil {
		return 0, "", time.Time{}, err
	}
	detail, until, err := maintenanceRestriction(policies, connection, at)
	return revision, detail, until, err
}
func hasPermission(ctx context.Context, q *database.Queries, org, uid, permission string) bool {
	perms, err := q.Permissions(ctx, database.PermissionsParams{OrgID: org, UserID: uid})
	return err == nil && slices.Contains(perms, permission)
}
func (s *Service) ListMaintenancePolicies(ctx context.Context, req *connect.Request[pb.ListMaintenancePoliciesRequest]) (*connect.Response[pb.ListMaintenancePoliciesResponse], error) {
	rows, err := s.q.ListMaintenancePolicies(ctx, req.Msg.OrganizationId)
	if err != nil {
		return nil, err
	}
	out := &pb.ListMaintenancePoliciesResponse{}
	for _, p := range rows {
		spec := new(pb.ScheduleSpec)
		if err = json.Unmarshal(p.Spec, spec); err != nil {
			return nil, err
		}
		loc, _ := time.LoadLocation(spec.Timezone)
		open, err := maintenanceOpen(spec, p.DurationMinutes, time.Now())
		if err != nil {
			return nil, err
		}
		next, err := nextSchedule(spec, time.Now())
		if err != nil {
			return nil, err
		}
		out.Policies = append(out.Policies, &pb.MaintenancePolicy{Id: p.ID, Name: p.Name, Spec: spec, DurationMinutes: p.DurationMinutes, ConnectionIds: p.ConnectionIds, OpenNow: open, NextStart: next.Due.In(loc).Format(time.RFC3339), NextEnd: next.Due.Add(time.Duration(p.DurationMinutes) * time.Minute).In(loc).Format(time.RFC3339), NextSkip: next.Skip})
	}
	return connect.NewResponse(out), nil
}
func (s *Service) SaveMaintenancePolicy(ctx context.Context, req *connect.Request[pb.SaveMaintenancePolicyRequest]) (*connect.Response[pb.AccessMutationResponse], error) {
	r := req.Msg
	r.Name = strings.TrimSpace(r.Name)
	if len(r.Name) < 1 || len(r.Name) > 120 || len(r.ConnectionIds) > 100 || r.DurationMinutes < 1 || r.DurationMinutes > 1440 {
		return nil, invalid("Name the policy, use 1–1,440 minutes, and select at most 100 connections.")
	}
	spec := &pb.ScheduleSpec{Action: "start", Timezone: r.Timezone, Cron: r.Cron, Exceptions: r.Exceptions}
	if _, err := nextSchedule(spec, time.Now()); err != nil {
		return nil, err
	}
	raw, err := json.Marshal(spec)
	if err != nil {
		return nil, err
	}
	id := r.Id
	if id == "" {
		id = randomID()
	}
	connections := slices.Clone(r.ConnectionIds)
	slices.Sort(connections)
	connections = slices.Compact(connections)
	err = s.transaction(ctx, func(q *database.Queries) error {
		// Scope FKs lock connections; take those locks before the organization lock.
		for _, connection := range connections {
			c, e := q.LockOperationConnection(ctx, database.LockOperationConnectionParams{OrgID: r.OrganizationId, ID: connection})
			if e != nil || c.DeletedAt.Valid {
				return denied()
			}
		}
		if _, err := q.LockAccess(ctx, r.OrganizationId); err != nil {
			return denied()
		}
		if !hasPermission(ctx, q, r.OrganizationId, actor(ctx).UserID, "maintenance.manage") {
			return denied()
		}
		if r.Id == "" {
			policies, e := q.ListMaintenancePolicies(ctx, r.OrganizationId)
			if e != nil {
				return e
			}
			if len(policies) >= 100 {
				return conflict("The current limit is 100 maintenance policies per organization.")
			}
			if e = q.CreateMaintenancePolicy(ctx, database.CreateMaintenancePolicyParams{ID: id, OrgID: r.OrganizationId, Name: r.Name, Spec: raw, DurationMinutes: r.DurationMinutes}); e != nil {
				return e
			}
		} else {
			n, e := q.UpdateMaintenancePolicy(ctx, database.UpdateMaintenancePolicyParams{OrgID: r.OrganizationId, ID: id, Name: r.Name, Spec: raw, DurationMinutes: r.DurationMinutes})
			if e != nil {
				return e
			}
			if n != 1 {
				return denied()
			}
		}
		if err := q.ClearMaintenanceConnections(ctx, database.ClearMaintenanceConnectionsParams{OrgID: r.OrganizationId, PolicyID: id}); err != nil {
			return err
		}
		for _, connection := range connections {
			if err := q.AddMaintenanceConnection(ctx, database.AddMaintenanceConnectionParams{OrgID: r.OrganizationId, PolicyID: id, ConnectionID: connection}); err != nil {
				return err
			}
		}
		if err := q.BumpMaintenanceRevision(ctx, r.OrganizationId); err != nil {
			return err
		}
		return audit(ctx, q, r.OrganizationId, actor(ctx).Email, "maintenance.policy_saved", id, map[string]any{"name": r.Name, "connections": connections, "approval_effect": "fresh review required"})
	})
	return connect.NewResponse(&pb.AccessMutationResponse{}), err
}
func (s *Service) DeleteMaintenancePolicy(ctx context.Context, req *connect.Request[pb.DeleteMaintenancePolicyRequest]) (*connect.Response[pb.AccessMutationResponse], error) {
	r := req.Msg
	err := s.transaction(ctx, func(q *database.Queries) error {
		if _, err := q.LockAccess(ctx, r.OrganizationId); err != nil {
			return denied()
		}
		if !hasPermission(ctx, q, r.OrganizationId, actor(ctx).UserID, "maintenance.manage") {
			return denied()
		}
		n, err := q.DeleteMaintenancePolicy(ctx, database.DeleteMaintenancePolicyParams{OrgID: r.OrganizationId, ID: r.Id, Name: r.Confirmation})
		if err != nil {
			return err
		}
		if n != 1 {
			return invalid("Type the exact policy name to remove it.")
		}
		if err = q.BumpMaintenanceRevision(ctx, r.OrganizationId); err != nil {
			return err
		}
		return audit(ctx, q, r.OrganizationId, actor(ctx).Email, "maintenance.policy_deleted", r.Id, nil)
	})
	return connect.NewResponse(&pb.AccessMutationResponse{}), err
}
