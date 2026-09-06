package core

import (
	"connectrpc.com/connect"
	"context"
	"github.com/alphabravo-oss/providah-community/internal/database"
	pb "github.com/alphabravo-oss/providah-community/internal/gen/providah/v1"
	"slices"
	"strings"
)

// ponytail: bounded to 100 teams and 1,000 members per team; paginate before raising these directory limits.
func (s *Service) ListTeams(ctx context.Context, r *connect.Request[pb.ListTeamsRequest]) (*connect.Response[pb.ListTeamsResponse], error) {
	rows, e := s.q.ListTeams(ctx, r.Msg.OrganizationId)
	if e != nil {
		return nil, e
	}
	if len(rows) > 100 {
		return nil, conflict("Team directory exceeds the 100-team limit.")
	}
	out := &pb.ListTeamsResponse{}
	for _, t := range rows {
		out.Teams = append(out.Teams, &pb.Team{Id: t.ID, Name: t.Name, RoleId: t.RoleID, Active: t.Active, Revision: t.Revision, UserIds: t.UserIds})
	}
	return connect.NewResponse(out), nil
}
func (s *Service) SaveTeam(ctx context.Context, req *connect.Request[pb.SaveTeamRequest]) (*connect.Response[pb.AccessMutationResponse], error) {
	r := req.Msg
	name := strings.TrimSpace(r.Name)
	if name == "" || len(name) > 80 || len(r.UserIds) > 1000 || (r.Id != "" && (len(r.Id) != 64 || r.ExpectedRevision < 1)) {
		return nil, invalid("Choose a team name, current revision and up to 1,000 organization members.")
	}
	ids := slices.Clone(r.UserIds)
	slices.Sort(ids)
	ids = slices.Compact(ids)
	for _, id := range ids {
		if len(id) != 64 {
			return nil, invalid("Choose valid organization members.")
		}
	}
	e := s.transaction(ctx, func(q *database.Queries) error {
		own, e := s.accessLock(ctx, q, r.OrganizationId, "members.manage")
		if e != nil {
			return e
		}
		role, e := q.GetRole(ctx, database.GetRoleParams{OrgID: r.OrganizationId, ID: r.RoleId})
		if e != nil || !subset(role.Permissions, own) {
			return denied()
		}
		id := r.Id
		if id == "" {
			teams, e := q.ListTeams(ctx, r.OrganizationId)
			if e != nil {
				return e
			}
			if len(teams) >= 100 {
				return conflict("This organization already has 100 teams.")
			}
			id = randomID()
			if e = q.CreateTeam(ctx, database.CreateTeamParams{OrgID: r.OrganizationId, ID: id, Name: name, RoleID: r.RoleId, Active: r.Active}); e != nil {
				return e
			}
		} else {
			old, e := q.GetTeam(ctx, database.GetTeamParams{OrgID: r.OrganizationId, ID: id})
			if e != nil {
				return denied()
			}
			oldRole, e := q.GetRole(ctx, database.GetRoleParams{OrgID: r.OrganizationId, ID: old.RoleID})
			if e != nil || !subset(oldRole.Permissions, own) {
				return denied()
			}
			n, e := q.UpdateTeam(ctx, database.UpdateTeamParams{OrgID: r.OrganizationId, ID: id, Name: name, RoleID: r.RoleId, Active: r.Active, Revision: r.ExpectedRevision})
			if e != nil {
				return e
			}
			if n != 1 {
				return conflict("Team changed. Reload before saving.")
			}
		}
		if e = q.ReplaceTeamMembers(ctx, database.ReplaceTeamMembersParams{OrgID: r.OrganizationId, TeamID: id}); e != nil {
			return e
		}
		for _, uid := range ids {
			n, e := q.AddTeamMember(ctx, database.AddTeamMemberParams{OrgID: r.OrganizationId, TeamID: id, UserID: uid})
			if e != nil {
				return e
			}
			if n != 1 {
				return invalid("Every team member must already belong to this organization.")
			}
		}
		return audit(ctx, q, r.OrganizationId, actor(ctx).Email, "team.saved", id, map[string]any{"name": name, "role": r.RoleId, "active": r.Active, "members": ids})
	})
	return connect.NewResponse(&pb.AccessMutationResponse{}), e
}

func (s *Service) DeleteTeam(ctx context.Context, req *connect.Request[pb.DeleteTeamRequest]) (*connect.Response[pb.AccessMutationResponse], error) {
	r := req.Msg
	if len(r.Id) != 64 || r.ExpectedRevision < 1 {
		return nil, invalid("Reload the team's current settings.")
	}
	e := s.transaction(ctx, func(q *database.Queries) error {
		own, e := s.accessLock(ctx, q, r.OrganizationId, "members.manage")
		if e != nil {
			return e
		}
		team, e := q.GetTeam(ctx, database.GetTeamParams{OrgID: r.OrganizationId, ID: r.Id})
		if e != nil {
			return denied()
		}
		role, e := q.GetRole(ctx, database.GetRoleParams{OrgID: r.OrganizationId, ID: team.RoleID})
		if e != nil || !subset(role.Permissions, own) {
			return denied()
		}
		if team.Revision != r.ExpectedRevision {
			return conflict("Team changed. Reload before deleting.")
		}
		if e = q.ReplaceTeamMembers(ctx, database.ReplaceTeamMembersParams{OrgID: r.OrganizationId, TeamID: r.Id}); e != nil {
			return e
		}
		n, e := q.DeleteTeam(ctx, database.DeleteTeamParams{OrgID: r.OrganizationId, ID: r.Id, Revision: r.ExpectedRevision})
		if e != nil {
			return e
		}
		if n != 1 {
			return conflict("Team changed. Reload before deleting.")
		}
		return audit(ctx, q, r.OrganizationId, actor(ctx).Email, "team.deleted", r.Id, map[string]any{"name": team.Name, "role": team.RoleID})
	})
	return connect.NewResponse(&pb.AccessMutationResponse{}), e
}
