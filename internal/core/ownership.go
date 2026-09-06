package core

import (
	"connectrpc.com/connect"
	"context"
	"encoding/json"
	"errors"
	"github.com/alphabravo-oss/providah-community/internal/database"
	pb "github.com/alphabravo-oss/providah-community/internal/gen/providah/v1"
	"github.com/alphabravo-oss/providah-community/internal/tfstate"
	"github.com/jackc/pgx/v5"
	"slices"
)

// Claims preserve conservative protection: replacing state never releases a claim.
// Matching IDs in the project's bound account/region are state evidence, not cloud attestation.
func (s *Service) indexOwnership(ctx context.Context, q *database.Queries, state database.AutomationState, raw []byte) error {
	scope, err := q.OwnershipProjectScope(ctx, database.OwnershipProjectScopeParams{OrgID: state.OrgID, ID: state.ProjectID})
	if err != nil {
		return err
	}
	refs, unsupported, scanErr := tfstate.ResourceIdentities(raw)
	status := "complete"
	if scanErr != nil {
		status = "invalid"
		refs = nil
	} else if unsupported > 0 {
		status = "partial"
	}
	type claim struct {
		Kind     string `json:"kind"`
		NativeID string `json:"native_id"`
		Region   string `json:"region"`
	}
	claims := []claim{}
	for _, ref := range refs {
		if ref.Provider != scope.Provider {
			unsupported++
			status = "partial"
			continue
		}
		region := scope.Region
		if ref.Provider == "aws" && ref.Region != "" {
			region = ref.Region
		}
		claims = append(claims, claim{ref.Kind, ref.NativeID, region})
	}
	b, _ := json.Marshal(claims)
	if err = q.InsertOwnershipClaims(ctx, database.InsertOwnershipClaimsParams{OrgID: state.OrgID, ConnectionID: scope.ConnectionID, ProjectID: state.ProjectID, Provider: scope.Provider, StateID: state.ID, Refs: b}); err != nil {
		return err
	}
	return q.RecordOwnershipScan(ctx, database.RecordOwnershipScanParams{StateID: state.ID, Status: status, Unsupported: int32(unsupported)}) // #nosec G115 -- Unsupported count is bounded by the parsed state resource limit.
}

// IndexStateOwnership backfills retained state before the server accepts mutations.
func (s *Service) IndexStateOwnership(ctx context.Context) error {
	for {
		state, err := s.q.UnindexedOwnershipState(ctx)
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		raw, err := s.openArtifact(ctx, "states/"+state.OrgID+"/"+state.ProjectID+"/"+state.ID, state.Storage, state.Ciphertext, tfstate.MaxBytes)
		if err != nil {
			return err
		}
		meta, err := tfstate.Parse([]byte(raw))
		if err != nil || meta.SHA256 != state.Sha256 || meta.Lineage != state.Lineage || meta.Serial != state.Serial || len(raw) != int(state.StateBytes) {
			return errors.New("state ownership integrity check failed")
		}
		if err = s.transaction(ctx, func(q *database.Queries) error { return s.indexOwnership(ctx, q, state, []byte(raw)) }); err != nil {
			return err
		}
	}
}
func ownershipActionAllowed(action string) bool {
	return slices.Contains([]string{"start", "shutdown", "restart", "snapshot"}, action)
}
func ownershipGuard(ctx context.Context, q *database.Queries, org, connection, kind, native, region, action string) error {
	if ownershipActionAllowed(action) {
		return nil
	}
	owners, err := q.ResourceOwnership(ctx, database.ResourceOwnershipParams{OrgID: org, ConnectionID: connection, Kind: kind, NativeID: native, Region: region})
	if err != nil {
		return err
	}
	if len(owners) > 0 {
		return conflict("This resource is referenced by managed IaC state. Change its owning configuration instead of editing or deleting it directly.")
	}
	return nil
}

func (s *Service) ListProjectOwnership(ctx context.Context, r *connect.Request[pb.ListProjectOwnershipRequest]) (*connect.Response[pb.ListProjectOwnershipResponse], error) {
	v := r.Msg
	if !notificationRecordID.MatchString(v.ProjectId) || len(v.PageToken) > 2048 {
		return nil, invalid("Invalid project or page token.")
	}
	permissions, e := s.q.Permissions(ctx, database.PermissionsParams{OrgID: v.OrganizationId, UserID: actor(ctx).UserID})
	if errors.Is(e, pgx.ErrNoRows) {
		return nil, denied()
	}
	if e != nil {
		return nil, e
	}
	if !slices.Contains(permissions, "resources.read") {
		return nil, denied()
	}
	if _, e = s.q.GetAutomationProject(ctx, database.GetAutomationProjectParams{OrgID: v.OrganizationId, ID: v.ProjectId}); errors.Is(e, pgx.ErrNoRows) {
		return nil, denied()
	} else if e != nil {
		return nil, e
	}
	filter := "project-ownership:" + v.ProjectId
	after, e := parseCursor(v.PageToken, v.OrganizationId, filter)
	if e != nil {
		return nil, e
	}
	rows, e := s.q.ListProjectOwnership(ctx, database.ListProjectOwnershipParams{OrgID: v.OrganizationId, ProjectID: v.ProjectId, AfterKey: after})
	if e != nil {
		return nil, e
	}
	out := &pb.ListProjectOwnershipResponse{}
	if len(rows) > 100 {
		out.NextPageToken = cursor(v.OrganizationId, filter, rows[99].PageKey)
		rows = rows[:100]
	}
	for _, row := range rows {
		out.References = append(out.References, &pb.ProjectOwnershipReference{Kind: row.Kind, NativeId: row.NativeID, Region: row.Region, FirstStateId: row.FirstStateID, ResourceId: row.ResourceID, Conflicting: row.Conflicting})
	}
	return connect.NewResponse(out), nil
}
