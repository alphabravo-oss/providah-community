package core

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/alphabravo-oss/providah-community/internal/inputspec"
	"github.com/jackc/pgx/v5"
	"regexp"
	"strings"

	"connectrpc.com/connect"
	"github.com/alphabravo-oss/providah-community/internal/database"
	pb "github.com/alphabravo-oss/providah-community/internal/gen/providah/v1"
	"github.com/alphabravo-oss/providah-community/internal/sourcebundle"
)

func inputProto(raw []byte) []*pb.AutomationInput {
	var fields []inputspec.Field
	if json.Unmarshal(raw, &fields) != nil {
		return nil
	}
	var out []*pb.AutomationInput
	for _, f := range fields {
		p := &pb.AutomationInput{Name: f.Name, Label: f.Label, Type: f.Type, Choices: f.Choices}
		if f.Min != nil {
			p.Min = *f.Min
		}
		if f.Max != nil {
			p.Max = *f.Max
		}
		out = append(out, p)
	}
	return out
}

func automationSource(r database.ListAutomationSourcesRow) *pb.AutomationSource {
	return &pb.AutomationSource{Inputs: inputProto(r.InputSchema), Id: r.ID, Name: r.Name, Runtime: r.Runtime, Entrypoint: r.Entrypoint, Sha256: r.Sha256, Files: r.Files, ArchiveBytes: r.ArchiveBytes, ExpandedBytes: r.ExpandedBytes}
}
func (s *Service) ListAutomationSources(ctx context.Context, r *connect.Request[pb.ListAutomationSourcesRequest]) (*connect.Response[pb.ListAutomationSourcesResponse], error) {
	rows, e := s.q.ListAutomationSources(ctx, r.Msg.OrganizationId)
	if e != nil {
		return nil, e
	}
	out := &pb.ListAutomationSourcesResponse{}
	for _, row := range rows {
		out.Sources = append(out.Sources, automationSource(row))
	}
	return connect.NewResponse(out), nil
}
func (s *Service) ImportAutomationSource(ctx context.Context, r *connect.Request[pb.ImportAutomationSourceRequest]) (*connect.Response[pb.AutomationSource], error) {
	v := r.Msg
	name := strings.TrimSpace(v.Name)
	if name == "" || len(name) > 80 {
		return nil, invalid("Choose a source name of at most 80 characters.")
	}
	if e := s.limit(ctx, "source-import:"+actor(ctx).UserID); e != nil {
		return nil, e
	}
	manifest, e := sourcebundle.Validate(v.Archive, v.Runtime, v.Entrypoint)
	if e != nil {
		return nil, invalid(e.Error())
	}
	var out *pb.AutomationSource
	e = s.transaction(ctx, func(q *database.Queries) error {
		if _, e := s.accessLock(ctx, q, v.OrganizationId, "templates.publish"); e != nil {
			return e
		}
		if !identityAllowed(ctx, q, v.OrganizationId, actor(ctx).UserID, actor(ctx).OIDC) {
			return denied()
		}
		rows, e := q.ListAutomationSources(ctx, v.OrganizationId)
		if e != nil {
			return e
		}
		for _, row := range rows {
			if row.Sha256 == manifest.SHA256 && row.Runtime == v.Runtime && row.Entrypoint == v.Entrypoint {
				out = automationSource(row)
				return nil
			}
		}
		// ponytail: cap the unpaginated catalog at 50 versions until source lifecycle and pagination land.
		if len(rows) >= 50 {
			return conflict("The organization source catalog is limited to 50 immutable bundles.")
		}
		sourceID := randomID()
		ciphertext, storage, e := s.sealArtifact(ctx, "sources/"+v.OrganizationId+"/"+sourceID, v.Archive)
		if e != nil {
			return e
		}
		schema, _ := json.Marshal(manifest.Inputs)
		if manifest.Inputs == nil {
			schema = []byte("[]")
		}
		id, e := q.ImportAutomationSource(ctx, database.ImportAutomationSourceParams{InputSchema: schema, ID: sourceID, OrgID: v.OrganizationId, Name: name, Runtime: v.Runtime, Entrypoint: v.Entrypoint, Sha256: manifest.SHA256, Files: manifest.Files, ArchiveBytes: int32(len(v.Archive)), ExpandedBytes: manifest.ExpandedBytes, Ciphertext: ciphertext, Storage: storage}) // #nosec G115 -- sourcebundle validation caps archives at 4 MiB before conversion.
		if e != nil {
			return e
		}
		out = &pb.AutomationSource{Inputs: inputProto(schema), Id: id, Name: name, Runtime: v.Runtime, Entrypoint: v.Entrypoint, Sha256: manifest.SHA256, Files: manifest.Files, ArchiveBytes: int32(len(v.Archive)), ExpandedBytes: manifest.ExpandedBytes} // #nosec G115 -- sourcebundle validation caps archives at 4 MiB before conversion.
		return audit(ctx, q, v.OrganizationId, actor(ctx).Email, "automation.source_imported", id, map[string]any{"runtime": v.Runtime, "sha256": manifest.SHA256, "file_count": len(manifest.Files)})
	})
	return connect.NewResponse(out), e
}

func (s *Service) GetAutomationSource(ctx context.Context, r *connect.Request[pb.GetAutomationSourceRequest]) (*connect.Response[pb.AutomationSource], error) {
	if !regexp.MustCompile(`^[a-f0-9]{64}$`).MatchString(r.Msg.Id) {
		return nil, invalid("Invalid record ID.")
	}
	row, e := s.q.GetAutomationSourceMetadata(ctx, database.GetAutomationSourceMetadataParams{OrgID: r.Msg.OrganizationId, ID: r.Msg.Id})
	if errors.Is(e, pgx.ErrNoRows) {
		return nil, denied()
	}
	if e != nil {
		return nil, e
	}
	return connect.NewResponse(automationSource(database.ListAutomationSourcesRow(row))), nil
}
