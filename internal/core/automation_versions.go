package core

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5"
	"regexp"
	"strings"

	"connectrpc.com/connect"
	"github.com/alphabravo-oss/providah-community/internal/database"
	pb "github.com/alphabravo-oss/providah-community/internal/gen/providah/v1"
)

func (s *Service) automationVersion(row database.AutomationVersion) *pb.AutomationVersion {
	available := false
	for _, r := range s.automationRuntimes {
		available = available || r.Matches(row.Runtime, row.RuntimeImage, row.RuntimeVersion, row.RuntimePolicy)
	}
	return &pb.AutomationVersion{Id: row.ID, Name: row.Name, Version: row.Version, SourceId: row.SourceID, Runtime: row.Runtime, RuntimeImage: row.RuntimeImage, RuntimeVersion: row.RuntimeVersion, ConnectionId: row.ConnectionID, ConnectionRevision: row.ConnectionRevision, Region: row.Region, ReviewNote: row.ReviewNote, Status: row.Status, RuntimeAvailable: available, RuntimePolicy: row.RuntimePolicy, DependencyHosts: row.DependencyHosts}
}
func (s *Service) GetAutomationVersion(ctx context.Context, r *connect.Request[pb.GetAutomationVersionRequest]) (*connect.Response[pb.AutomationVersion], error) {
	if !regexp.MustCompile(`^[a-f0-9]{64}$`).MatchString(r.Msg.Id) {
		return nil, invalid("Invalid automation version ID.")
	}
	row, e := s.q.GetAutomationVersion(ctx, database.GetAutomationVersionParams{OrgID: r.Msg.OrganizationId, ID: r.Msg.Id})
	if errors.Is(e, pgx.ErrNoRows) {
		return nil, denied()
	}
	if e != nil {
		return nil, e
	}
	return connect.NewResponse(s.automationVersion(row)), nil
}
func (s *Service) ListAutomationVersions(ctx context.Context, r *connect.Request[pb.ListAutomationVersionsRequest]) (*connect.Response[pb.ListAutomationVersionsResponse], error) {
	rows, e := s.q.ListAutomationVersions(ctx, r.Msg.OrganizationId)
	if e != nil {
		return nil, e
	}
	out := &pb.ListAutomationVersionsResponse{}
	for _, row := range rows {
		out.Versions = append(out.Versions, s.automationVersion(row))
	}
	for _, runtime := range s.automationRuntimes {
		out.Runtimes = append(out.Runtimes, &pb.AutomationRuntime{Runtime: runtime.Runtime, Image: runtime.Image, Version: runtime.Version, DependencyHosts: runtime.DependencyHosts})
	}
	return connect.NewResponse(out), nil
}
func (s *Service) PublishAutomationVersion(ctx context.Context, r *connect.Request[pb.PublishAutomationVersionRequest]) (*connect.Response[pb.AutomationVersion], error) {
	v := r.Msg
	name, note := strings.TrimSpace(v.Name), strings.TrimSpace(v.ReviewNote)
	if name == "" || len(name) > 80 || len(note) < 3 || len(note) > 1000 || v.ExpectedVersion < 0 || !regexp.MustCompile(`^[a-z0-9-]{1,64}$`).MatchString(v.Region) {
		return nil, invalid("Choose a name, region, and review note.")
	}
	var out *pb.AutomationVersion
	e := s.transaction(ctx, func(q *database.Queries) error {
		if _, e := s.accessLock(ctx, q, v.OrganizationId, "templates.publish"); e != nil {
			return e
		}
		if !identityAllowed(ctx, q, v.OrganizationId, actor(ctx).UserID, actor(ctx).OIDC) {
			return denied()
		}
		rows, e := q.ListAutomationVersions(ctx, v.OrganizationId)
		if e != nil {
			return e
		}
		var latest int32
		for _, row := range rows {
			if row.Name == name {
				if row.Version > latest {
					latest = row.Version
				}
				if row.Version == v.ExpectedVersion+1 && row.SourceID == v.SourceId && row.RuntimeImage == v.RuntimeImage && row.ConnectionID == v.ConnectionId && row.Region == v.Region && row.ReviewNote == note {
					out = s.automationVersion(row)
					return nil
				}
			}
		}
		if latest != v.ExpectedVersion {
			return conflict("A newer version exists. Refresh the catalog before publishing.")
		}
		if len(rows) >= 200 {
			return conflict("The organization catalog is limited to 200 automation versions.")
		}
		sources, e := q.ListAutomationSources(ctx, v.OrganizationId)
		if e != nil {
			return e
		}
		engine := ""
		for _, source := range sources {
			if source.ID == v.SourceId {
				engine = source.Runtime
			}
		}
		if engine == "" {
			return denied()
		}
		runtimeVersion, runtimePolicy := "", ""
		dependencyHosts := []string{}
		for _, runtime := range s.automationRuntimes {
			if runtime.Runtime == engine && runtime.Image == v.RuntimeImage {
				runtimeVersion = runtime.Version
				runtimePolicy = runtime.Policy()
				dependencyHosts = runtime.DependencyHosts
				if dependencyHosts == nil {
					dependencyHosts = []string{}
				}
			}
		}
		if runtimeVersion == "" {
			return conflict("Select an allowed image for this source runtime.")
		}
		c, e := q.LockOperationConnection(ctx, database.LockOperationConnectionParams{OrgID: v.OrganizationId, ID: v.ConnectionId})
		if e != nil {
			return denied()
		}
		if !c.Enabled || c.DeletedAt.Valid || c.Region != "" && c.Region != v.Region {
			return conflict("Choose an enabled connection in the selected region.")
		}
		row, e := q.PublishAutomationVersion(ctx, database.PublishAutomationVersionParams{ID: randomID(), OrgID: v.OrganizationId, Name: name, Version: latest + 1, SourceID: v.SourceId, Runtime: engine, RuntimeImage: v.RuntimeImage, RuntimeVersion: runtimeVersion, RuntimePolicy: runtimePolicy, DependencyHosts: dependencyHosts, ConnectionID: c.ID, ConnectionRevision: c.Revision, Region: v.Region, ReviewNote: note})
		if e != nil {
			return e
		}
		out = s.automationVersion(row)
		return audit(ctx, q, v.OrganizationId, actor(ctx).Email, "automation.version_published", row.ID, map[string]any{"source_id": row.SourceID, "runtime_image": row.RuntimeImage, "connection_id": c.ID, "connection_revision": c.Revision, "version": row.Version})
	})
	return connect.NewResponse(out), e
}
func (s *Service) SetAutomationVersionStatus(ctx context.Context, r *connect.Request[pb.SetAutomationVersionStatusRequest]) (*connect.Response[pb.AutomationVersion], error) {
	v := r.Msg
	if v.Status != "retired" && v.Status != "revoked" {
		return nil, invalid("Choose retirement or revocation.")
	}
	var out *pb.AutomationVersion
	e := s.transaction(ctx, func(q *database.Queries) error {
		if _, e := s.accessLock(ctx, q, v.OrganizationId, "templates.publish"); e != nil {
			return e
		}
		if !identityAllowed(ctx, q, v.OrganizationId, actor(ctx).UserID, actor(ctx).OIDC) {
			return denied()
		}
		row, e := q.SetAutomationVersionStatus(ctx, database.SetAutomationVersionStatusParams{OrgID: v.OrganizationId, ID: v.Id, Status: v.Status})
		if e != nil {
			return conflict("This version is unavailable or already has that status.")
		}
		out = s.automationVersion(row)
		return audit(ctx, q, v.OrganizationId, actor(ctx).Email, "automation.version_"+v.Status, row.ID, map[string]any{"version": row.Version})
	})
	return connect.NewResponse(out), e
}
