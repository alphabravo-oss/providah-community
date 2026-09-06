package core

import (
	"connectrpc.com/connect"
	"context"
	"github.com/alphabravo-oss/providah-community/internal/database"
	pb "github.com/alphabravo-oss/providah-community/internal/gen/providah/v1"
	"github.com/jackc/pgx/v5/pgtype"
	"slices"
	"strconv"
	"time"
)

func (s *Service) ListInstallationDirectory(ctx context.Context, req *connect.Request[pb.ListInstallationDirectoryRequest]) (*connect.Response[pb.ListInstallationDirectoryResponse], error) {
	if e := s.installationReadAccess(ctx); e != nil {
		return nil, e
	}
	r := req.Msg
	if !slices.Contains([]string{"users", "organizations"}, r.Kind) || !slices.Contains([]string{"", "active", "disabled"}, r.State) || len(r.Search) > 200 || len(r.PageToken) > 2048 {
		return nil, invalid("Choose a directory and valid filters.")
	}
	filter := "installation:" + r.Kind + ":" + r.State + ":" + strconv.Quote(r.Search)
	after, e := parseCursor(r.PageToken, "", filter)
	if e != nil {
		return nil, e
	}
	out := &pb.ListInstallationDirectoryResponse{}
	if r.Kind == "users" {
		rows, e := s.q.InstallationUsers(ctx, database.InstallationUsersParams{AfterID: after, Search: r.Search, State: r.State})
		if e != nil {
			return nil, e
		}
		if len(rows) > 100 {
			out.NextPageToken = cursor("", filter, rows[99].ID)
			rows = rows[:100]
		}
		for _, u := range rows {
			out.Users = append(out.Users, &pb.InstallationUser{Id: u.ID, Email: u.Email, Active: u.Active, GlobalAdmin: u.GlobalAdmin, MfaEnabled: u.MfaEnabled, Revision: u.AdminRevision})
		}
	} else {
		rows, e := s.q.InstallationOrganizations(ctx, database.InstallationOrganizationsParams{AfterID: after, Search: r.Search, State: r.State})
		if e != nil {
			return nil, e
		}
		if len(rows) > 100 {
			out.NextPageToken = cursor("", filter, rows[99].ID)
			rows = rows[:100]
		}
		for _, o := range rows {
			out.Organizations = append(out.Organizations, &pb.InstallationOrganization{Id: o.ID, Name: o.Name, Active: o.Active, Revision: o.AdminRevision})
		}
	}
	return connect.NewResponse(out), nil
}

// SetInstallationUser requires fresh proof and never permits changing the caller's
// own installation grant, so a successful change always leaves an active admin.
func (s *Service) SetInstallationUser(ctx context.Context, req *connect.Request[pb.SetInstallationUserRequest]) (*connect.Response[pb.AccessMutationResponse], error) {
	r, p := req.Msg, actor(ctx)
	if len(r.Id) != 64 || r.Id == p.UserID || r.ExpectedRevision < 1 {
		return nil, invalid("Choose another user and reload their current settings.")
	}
	e := s.accountChange(ctx, r.Password, r.Code, func(q *database.Queries) error {
		if e := s.mfaPolicyAccess(ctx, q, ""); e != nil {
			return e
		}
		policy, e := q.GetMFAPolicy(ctx, "")
		if e != nil {
			return e
		}
		if policy.GlobalRequired && p.MFADisabled {
			return denied()
		}
		n, e := q.UpdateInstallationUser(ctx, database.UpdateInstallationUserParams{ID: r.Id, Active: r.Active, GlobalAdmin: r.GlobalAdmin, ExpectedRevision: r.ExpectedRevision})
		if e != nil {
			return e
		}
		if n != 1 {
			return conflict("User settings changed or the user is unavailable. Reload before saving.")
		}
		if e = q.DeleteUserSessions(ctx, r.Id); e != nil {
			return e
		}
		return q.AddInstallationUserEvent(ctx, database.AddInstallationUserEventParams{ActorID: pgtype.Text{String: p.UserID, Valid: true}, TargetID: pgtype.Text{String: r.Id, Valid: true}, Active: r.Active, GlobalAdmin: r.GlobalAdmin})
	})
	if e != nil {
		return nil, e
	}
	return connect.NewResponse(&pb.AccessMutationResponse{}), nil
}

func (s *Service) installationReadAccess(ctx context.Context) error {
	if e := s.mfaPolicyAccess(ctx, s.q, ""); e != nil {
		return e
	}
	policy, e := s.q.GetMFAPolicy(ctx, "")
	if e != nil {
		return e
	}
	p := actor(ctx)
	if (policy.GlobalRequired && p.MFADisabled) || (!p.MFADisabled && p.MFAAt.IsZero()) {
		return denied()
	}
	return nil
}

func (s *Service) SetInstallationOrganization(ctx context.Context, req *connect.Request[pb.SetInstallationOrganizationRequest]) (*connect.Response[pb.AccessMutationResponse], error) {
	r, p := req.Msg, actor(ctx)
	if len(r.Id) != 64 || r.ExpectedRevision < 1 {
		return nil, invalid("Reload the organization's current settings.")
	}
	e := s.accountChange(ctx, r.Password, r.Code, func(q *database.Queries) error {
		if e := s.mfaPolicyAccess(ctx, q, ""); e != nil {
			return e
		}
		policy, e := q.GetMFAPolicy(ctx, "")
		if e != nil {
			return e
		}
		if policy.GlobalRequired && p.MFADisabled {
			return denied()
		}
		n, e := q.UpdateInstallationOrganization(ctx, database.UpdateInstallationOrganizationParams{ID: r.Id, Active: r.Active, ExpectedRevision: r.ExpectedRevision})
		if e != nil {
			return e
		}
		if n != 1 {
			return conflict("Organization settings changed or are unavailable. Reload before saving.")
		}
		return audit(ctx, q, r.Id, p.Email, "organization.state_changed", r.Id, map[string]any{"active": r.Active})
	})
	if e != nil {
		return nil, e
	}
	return connect.NewResponse(&pb.AccessMutationResponse{}), nil
}

func (s *Service) GetInstallationHealth(ctx context.Context, _ *connect.Request[pb.GetInstallationHealthRequest]) (*connect.Response[pb.InstallationHealth], error) {
	if e := s.installationReadAccess(ctx); e != nil {
		return nil, e
	}
	rows, e := s.workSnapshot(ctx)
	if e != nil {
		return nil, e
	}
	storage, e := s.databaseStorage(ctx)
	if e != nil {
		return nil, e
	}
	stat := s.pool.Stat()
	out := &pb.InstallationHealth{Storage: storage, ObservedAt: time.Now().UTC().Format(time.RFC3339Nano), DatabaseConnections: stat.TotalConns(), DatabaseCapacity: stat.MaxConns(), ProviderExecutionConfigured: s.cfg.ProviderCall != nil}
	for _, v := range rows {
		out.Work = append(out.Work, &pb.InstallationWorkState{Kind: v.kind, Status: v.status, Count: v.count, OldestSeconds: v.age})
	}
	return connect.NewResponse(out), nil
}
