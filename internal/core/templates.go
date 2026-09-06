package core

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/jackc/pgx/v5"
	"regexp"
	"strings"

	"connectrpc.com/connect"
	"github.com/alphabravo-oss/providah-community/internal/database"
	pb "github.com/alphabravo-oss/providah-community/internal/gen/providah/v1"
	"github.com/alphabravo-oss/providah-community/sdk/provider"
)

func serverTemplate(row database.ServerTemplate) *pb.ServerTemplate {
	return &pb.ServerTemplate{Id: row.ID, Name: row.Name, Version: row.Version, ConnectionId: row.ConnectionID, Region: row.Region, Creation: creationProto(row.Creation), Status: row.Status}
}
func (s *Service) ListServerTemplates(ctx context.Context, r *connect.Request[pb.ListServerTemplatesRequest]) (*connect.Response[pb.ListServerTemplatesResponse], error) {
	rows, e := s.q.ListServerTemplates(ctx, r.Msg.OrganizationId)
	if e != nil {
		return nil, e
	}
	out := &pb.ListServerTemplatesResponse{}
	for _, row := range rows {
		out.Templates = append(out.Templates, serverTemplate(row))
	}
	return connect.NewResponse(out), nil
}
func (s *Service) PublishServerTemplate(ctx context.Context, r *connect.Request[pb.PublishServerTemplateRequest]) (*connect.Response[pb.ServerTemplate], error) {
	v := r.Msg
	name := strings.TrimSpace(v.Name)
	if name == "" || len(name) > 80 || v.Creation == nil || !regexp.MustCompile("^[a-z0-9-]{1,64}$").MatchString(v.Region) {
		return nil, invalid("Choose a template name, region, and configuration.")
	}
	input := provider.ServerCreate{Name: "template", Image: v.Creation.Image, Size: v.Creation.Size, SSHKey: v.Creation.SshKey, Subnet: v.Creation.Subnet, Network: v.Creation.Network, SecurityGroup: v.Creation.SecurityGroup}
	var out *pb.ServerTemplate
	e := s.transaction(ctx, func(q *database.Queries) error {
		if _, e := s.accessLock(ctx, q, v.OrganizationId, "templates.publish"); e != nil {
			return e
		}
		if !identityAllowed(ctx, q, v.OrganizationId, actor(ctx).UserID, actor(ctx).OIDC) {
			return denied()
		}
		c, e := q.LockOperationConnection(ctx, database.LockOperationConnectionParams{OrgID: v.OrganizationId, ID: v.ConnectionId})
		if e != nil {
			return denied()
		}
		if !c.Enabled || c.DeletedAt.Valid || c.Region != "" && c.Region != v.Region {
			return conflict("Choose an enabled connection in this region.")
		}
		if e = input.Validate(c.Provider); e != nil {
			return invalid(e.Error())
		}
		rows, e := q.ListServerTemplates(ctx, v.OrganizationId)
		if e != nil {
			return e
		}
		if len(rows) >= 200 {
			return conflict("The organization catalog is limited to 200 published versions.")
		}
		raw, _ := json.Marshal(input)
		row, e := q.PublishServerTemplate(ctx, database.PublishServerTemplateParams{ID: randomID(), OrgID: v.OrganizationId, ConnectionID: c.ID, Name: name, Region: v.Region, Creation: raw})
		if e != nil {
			return e
		}
		out = serverTemplate(row)
		return audit(ctx, q, v.OrganizationId, actor(ctx).Email, "template.published", row.ID, map[string]any{"name": name, "version": row.Version, "connection_id": c.ID})
	})
	return connect.NewResponse(out), e
}
func (s *Service) SetServerTemplateStatus(ctx context.Context, r *connect.Request[pb.SetServerTemplateStatusRequest]) (*connect.Response[pb.ServerTemplate], error) {
	v := r.Msg
	if v.Status != "retired" && v.Status != "revoked" {
		return nil, invalid("Choose retirement or security revocation.")
	}
	var out *pb.ServerTemplate
	e := s.transaction(ctx, func(q *database.Queries) error {
		if _, e := s.accessLock(ctx, q, v.OrganizationId, "templates.publish"); e != nil {
			return e
		}
		if !identityAllowed(ctx, q, v.OrganizationId, actor(ctx).UserID, actor(ctx).OIDC) {
			return denied()
		}
		row, e := q.SetServerTemplateStatus(ctx, database.SetServerTemplateStatusParams{OrgID: v.OrganizationId, ID: v.Id, Status: v.Status})
		if e != nil {
			return conflict("This template is unavailable or already has that status.")
		}
		out = serverTemplate(row)
		return audit(ctx, q, v.OrganizationId, actor(ctx).Email, "template."+v.Status, row.ID, map[string]any{"version": row.Version})
	})
	return connect.NewResponse(out), e
}

func (s *Service) GetServerTemplate(ctx context.Context, r *connect.Request[pb.GetServerTemplateRequest]) (*connect.Response[pb.ServerTemplate], error) {
	if !regexp.MustCompile(`^[a-f0-9]{64}$`).MatchString(r.Msg.Id) {
		return nil, invalid("Invalid record ID.")
	}
	row, e := s.q.GetServerTemplate(ctx, database.GetServerTemplateParams{OrgID: r.Msg.OrganizationId, ID: r.Msg.Id})
	if errors.Is(e, pgx.ErrNoRows) {
		return nil, denied()
	}
	if e != nil {
		return nil, e
	}
	return connect.NewResponse(serverTemplate(row)), nil
}
