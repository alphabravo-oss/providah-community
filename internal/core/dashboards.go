package core

import (
	"connectrpc.com/connect"
	"context"
	"github.com/alphabravo-oss/providah-community/internal/database"
	pb "github.com/alphabravo-oss/providah-community/internal/gen/providah/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"slices"
	"strings"
)

func validateDashboard(spec *pb.DashboardSpec) error {
	if spec == nil || len(spec.Widgets) > 12 {
		return invalid("A dashboard supports up to 12 resource widgets.")
	}
	for _, w := range spec.Widgets {
		if w == nil || (w.Activity && w.SummaryBy != "") || !slices.Contains([]string{"", "provider", "kind", "status"}, w.SummaryBy) || strings.TrimSpace(w.Title) == "" || len(w.Title) > 80 {
			return invalid("Choose a widget title of at most 80 characters.")
		}
		if e := validateInventoryView(w.Filters); e != nil {
			return e
		}
	}
	return nil
}
func dashboard(row database.Dashboard, user string) (*pb.Dashboard, error) {
	spec := &pb.DashboardSpec{}
	if err := protojson.Unmarshal(row.Spec, spec); err != nil {
		return nil, err
	}
	return &pb.Dashboard{Id: row.ID, Name: row.Name, Spec: spec, Revision: row.Revision, Shared: row.Shared, TeamIds: row.TeamIds, Owned: row.UserID == user}, nil
}
func (s *Service) ListDashboards(ctx context.Context, req *connect.Request[pb.ListDashboardsRequest]) (*connect.Response[pb.ListDashboardsResponse], error) {
	rows, err := s.q.ListDashboards(ctx, database.ListDashboardsParams{OrgID: req.Msg.OrganizationId, UserID: actor(ctx).UserID})
	if err != nil {
		return nil, err
	}
	out := &pb.ListDashboardsResponse{}
	for _, row := range rows {
		v, err := dashboard(row, actor(ctx).UserID)
		if err != nil {
			return nil, err
		}
		out.Dashboards = append(out.Dashboards, v)
	}
	return connect.NewResponse(out), nil
}
func (s *Service) SaveDashboard(ctx context.Context, req *connect.Request[pb.SaveDashboardRequest]) (*connect.Response[pb.Dashboard], error) {
	r := req.Msg
	name := strings.TrimSpace(r.Name)
	if len(r.TeamIds) > 20 || (r.Shared && len(r.TeamIds) > 0) {
		return nil, invalid("Choose organization sharing or up to 20 teams.")
	}
	teamIDs := slices.Clone(r.TeamIds)
	slices.Sort(teamIDs)
	teamIDs = slices.Compact(teamIDs)
	if teamIDs == nil {
		teamIDs = []string{}
	}
	for _, id := range teamIDs {
		if len(id) != 64 {
			return nil, invalid("Choose valid organization teams.")
		}
	}
	publishing := r.Shared || len(teamIDs) > 0
	if name == "" || len(name) > 80 || (r.Id == "" && r.ExpectedRevision != 0) || (r.Id != "" && (len(r.Id) != 64 || r.ExpectedRevision < 1)) {
		return nil, invalid("Choose a dashboard name of at most 80 characters.")
	}
	if err := validateDashboard(r.Spec); err != nil {
		return nil, err
	}
	raw, err := protojson.Marshal(r.Spec)
	if err != nil {
		return nil, err
	}
	var out *pb.Dashboard
	err = s.transaction(ctx, func(q *database.Queries) error {
		p := actor(ctx)
		perms, e := s.accessLock(ctx, q, r.OrganizationId, "resources.read")
		if e != nil {
			return e
		}
		manage := slices.Contains(perms, "dashboards.manage")
		if !identityAllowed(ctx, q, r.OrganizationId, p.UserID, p.OIDC) {
			return denied()
		}
		var old database.Dashboard
		if r.Id != "" {
			old, e = q.GetOwnedOrSharedDashboard(ctx, database.GetOwnedOrSharedDashboardParams{OrgID: r.OrganizationId, ID: r.Id, UserID: p.UserID})
			if e != nil {
				return viewWriteError(e)
			}
		}
		if (publishing || old.Shared || len(old.TeamIds) > 0) && !manage {
			return denied()
		}
		for _, id := range teamIDs {
			if _, e := q.GetTeam(ctx, database.GetTeamParams{OrgID: r.OrganizationId, ID: id}); e != nil {
				return invalid("A selected team is unavailable in this organization. Reload the choices.")
			}
		}
		if publishing && !old.Shared && len(old.TeamIds) == 0 {
			count, e := q.CountSharedDashboards(ctx, r.OrganizationId)
			if e != nil {
				return e
			}
			if count >= 50 {
				return conflict("An organization can share up to 50 dashboards.")
			}
		}
		var row database.Dashboard
		if r.Id == "" {
			count, e := q.CountDashboards(ctx, database.CountDashboardsParams{OrgID: r.OrganizationId, UserID: p.UserID})
			if e != nil {
				return e
			}
			if count >= 50 {
				return conflict("You can save up to 50 dashboards per organization.")
			}
			row, e = q.CreateDashboard(ctx, database.CreateDashboardParams{ID: randomID(), OrgID: r.OrganizationId, UserID: p.UserID, Name: name, Spec: raw, Shared: r.Shared, TeamIds: teamIDs})
			if e != nil {
				return viewWriteError(e)
			}
		} else {
			row, e = q.UpdateDashboard(ctx, database.UpdateDashboardParams{ID: r.Id, OrgID: r.OrganizationId, UserID: p.UserID, Name: name, Spec: raw, Revision: r.ExpectedRevision, Shared: r.Shared, TeamIds: teamIDs, ManageShared: manage})
			if e != nil {
				return viewWriteError(e)
			}
		}
		out, e = dashboard(row, actor(ctx).UserID)
		if e != nil {
			return e
		}
		return audit(ctx, q, r.OrganizationId, p.Email, "dashboard.saved", row.ID, map[string]any{"revision": row.Revision, "shared": row.Shared, "team_ids": row.TeamIds})
	})
	return connect.NewResponse(out), err
}
func (s *Service) DeleteDashboard(ctx context.Context, req *connect.Request[pb.DeleteDashboardRequest]) (*connect.Response[pb.AccessMutationResponse], error) {
	r := req.Msg
	if len(r.Id) != 64 || r.ExpectedRevision < 1 {
		return nil, invalid("Choose a dashboard.")
	}
	err := s.transaction(ctx, func(q *database.Queries) error {
		p := actor(ctx)
		perms, e := s.accessLock(ctx, q, r.OrganizationId, "resources.read")
		if e != nil {
			return e
		}
		manage := slices.Contains(perms, "dashboards.manage")
		if !identityAllowed(ctx, q, r.OrganizationId, p.UserID, p.OIDC) {
			return denied()
		}
		old, e := q.GetOwnedOrSharedDashboard(ctx, database.GetOwnedOrSharedDashboardParams{OrgID: r.OrganizationId, ID: r.Id, UserID: p.UserID})
		if e != nil {
			return viewWriteError(e)
		}
		if (old.Shared || len(old.TeamIds) > 0) && !manage {
			return denied()
		}
		n, e := q.DeleteDashboard(ctx, database.DeleteDashboardParams{ID: r.Id, OrgID: r.OrganizationId, UserID: p.UserID, Revision: r.ExpectedRevision, ManageShared: manage})
		if e != nil {
			return e
		}
		if n != 1 {
			return conflict("Dashboard changed or is unavailable. Reload before deleting.")
		}
		return audit(ctx, q, r.OrganizationId, p.Email, "dashboard.deleted", r.Id, map[string]any{})
	})
	return connect.NewResponse(&pb.AccessMutationResponse{}), err
}

func (s *Service) GetResourceSummary(ctx context.Context, req *connect.Request[pb.GetResourceSummaryRequest]) (*connect.Response[pb.GetResourceSummaryResponse], error) {
	r := req.Msg
	if !slices.Contains([]string{"provider", "kind", "status"}, r.GroupBy) {
		return nil, invalid("Choose provider, type or status for the summary.")
	}
	if e := validateInventoryView(r.Filters); e != nil {
		return nil, e
	}
	f := r.Filters
	rows, e := s.q.ResourceSummary(ctx, database.ResourceSummaryParams{ConnectionID: f.ConnectionId, FilterRegion: f.Region, FilterStatus: f.Status, TagConditions: tagConditionsJSON(f.TagConditions), TagMatchAny: f.TagMatchAny, OrgID: r.OrganizationId, Search: f.Search, Provider: f.Provider, Kind: f.Kind, TagKey: f.TagKey, TagValue: f.TagValue, TagName: f.TagName, TagExists: f.TagExists, GroupBy: r.GroupBy})
	if e != nil {
		return nil, e
	}
	if len(rows) > 1000 {
		return nil, conflict("Too many summary groups. Narrow the resource filters.")
	}
	out := &pb.GetResourceSummaryResponse{}
	for _, row := range rows {
		out.Groups = append(out.Groups, &pb.ResourceCount{Label: row.Label, Total: row.Total, OldestObservation: stamp(row.OldestObservation)})
		out.Total += row.Total
	}
	return connect.NewResponse(out), nil
}

func (s *Service) ListResourceScopes(ctx context.Context, req *connect.Request[pb.ListResourceScopesRequest]) (*connect.Response[pb.ListResourceScopesResponse], error) {
	rows, e := s.q.ListResourceScopes(ctx, req.Msg.OrganizationId)
	if e != nil {
		return nil, e
	}
	if len(rows) > 1000 {
		return nil, conflict("Too many scope choices to display.")
	}
	out := &pb.ListResourceScopesResponse{}
	for _, r := range rows {
		out.Scopes = append(out.Scopes, &pb.ResourceScope{Kind: r.Kind, Value: r.Value, Label: r.Label})
	}
	return connect.NewResponse(out), nil
}

func (s *Service) ListDashboardTeams(ctx context.Context, req *connect.Request[pb.ListDashboardsRequest]) (*connect.Response[pb.ListDashboardTeamsResponse], error) {
	rows, e := s.q.ListDashboardTeams(ctx, req.Msg.OrganizationId)
	if e != nil {
		return nil, e
	}
	if len(rows) > 100 {
		return nil, conflict("Team directory exceeds the 100-team limit.")
	}
	out := &pb.ListDashboardTeamsResponse{}
	for _, row := range rows {
		out.Teams = append(out.Teams, &pb.DashboardTeam{Id: row.ID, Name: row.Name, Active: row.Active})
	}
	return connect.NewResponse(out), nil
}
