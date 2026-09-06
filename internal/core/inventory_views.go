package core

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"

	"connectrpc.com/connect"
	"github.com/alphabravo-oss/providah-community/internal/database"
	pb "github.com/alphabravo-oss/providah-community/internal/gen/providah/v1"
	"github.com/alphabravo-oss/providah-community/sdk/provider"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"google.golang.org/protobuf/encoding/protojson"
)

func validateInventoryView(spec *pb.InventoryViewSpec) error {
	if spec == nil || (spec.SortBy == "" && spec.Descending) || len(spec.Search) > 200 || !slices.Contains([]string{"", "aws", "digitalocean", "hetzner"}, spec.Provider) ||
		(spec.Kind != "" && !slices.Contains(provider.InventoryKinds(""), spec.Kind)) ||
		!slices.Contains([]string{"", "id", "name", "provider", "kind", "region", "status"}, spec.SortBy) {
		return invalid("Invalid inventory view filters.")
	}
	if e := validateResourceScope(spec.ConnectionId, spec.Region, spec.Status); e != nil {
		return e
	}
	if e := validateTagFilter(spec.TagKey, spec.TagValue, spec.TagName, spec.TagExists); e != nil {
		return e
	}
	if e := validateTagConditions(spec.TagConditions, spec.TagMatchAny, spec.TagKey, spec.TagName); e != nil {
		return e
	}
	seen := map[string]bool{}
	for _, column := range spec.HiddenColumns {
		if !slices.Contains([]string{"name", "provider", "kind", "region", "status", "observedAt"}, column) || seen[column] {
			return invalid("Invalid view columns.")
		}
		seen[column] = true
	}
	if len(seen) >= 6 {
		return invalid("Keep at least one column visible.")
	}
	return nil
}
func inventoryView(row database.InventoryView) (*pb.InventoryView, error) {
	spec := &pb.InventoryViewSpec{}
	if err := protojson.Unmarshal(row.Spec, spec); err != nil {
		return nil, err
	}
	return &pb.InventoryView{Id: row.ID, Name: row.Name, Spec: spec, Revision: row.Revision}, nil
}
func (s *Service) ListInventoryViews(ctx context.Context, req *connect.Request[pb.ListInventoryViewsRequest]) (*connect.Response[pb.ListInventoryViewsResponse], error) {
	rows, err := s.q.ListInventoryViews(ctx, database.ListInventoryViewsParams{OrgID: req.Msg.OrganizationId, UserID: actor(ctx).UserID})
	if err != nil {
		return nil, err
	}
	out := &pb.ListInventoryViewsResponse{}
	for _, row := range rows {
		v, err := inventoryView(row)
		if err != nil {
			return nil, err
		}
		out.Views = append(out.Views, v)
	}
	return connect.NewResponse(out), nil
}
func (s *Service) SaveInventoryView(ctx context.Context, req *connect.Request[pb.SaveInventoryViewRequest]) (*connect.Response[pb.InventoryView], error) {
	r := req.Msg
	name := strings.TrimSpace(r.Name)
	if name == "" || len(name) > 80 || (r.Id == "" && r.ExpectedRevision != 0) || (r.Id != "" && (len(r.Id) != 64 || r.ExpectedRevision < 1)) {
		return nil, invalid("Choose a view name of at most 80 characters.")
	}
	if err := validateInventoryView(r.Spec); err != nil {
		return nil, err
	}
	raw, err := protojson.Marshal(r.Spec)
	if err != nil {
		return nil, err
	}
	var out *pb.InventoryView
	err = s.transaction(ctx, func(q *database.Queries) error {
		p := actor(ctx)
		if _, e := s.accessLock(ctx, q, r.OrganizationId, "resources.read"); e != nil {
			return e
		}
		if !identityAllowed(ctx, q, r.OrganizationId, p.UserID, p.OIDC) {
			return denied()
		}
		var row database.InventoryView
		var e error
		if r.Id == "" {
			count, e := q.CountInventoryViews(ctx, database.CountInventoryViewsParams{OrgID: r.OrganizationId, UserID: p.UserID})
			if e != nil {
				return e
			}
			if count >= 50 {
				return conflict("You can save up to 50 inventory views per organization.")
			}
			row, e = q.CreateInventoryView(ctx, database.CreateInventoryViewParams{ID: randomID(), OrgID: r.OrganizationId, UserID: p.UserID, Name: name, Spec: raw})
			if e != nil {
				return viewWriteError(e)
			}
		} else {
			row, e = q.UpdateInventoryView(ctx, database.UpdateInventoryViewParams{ID: r.Id, OrgID: r.OrganizationId, UserID: p.UserID, Name: name, Spec: raw, Revision: r.ExpectedRevision})
			if e != nil {
				return viewWriteError(e)
			}
		}
		out, e = inventoryView(row)
		if e != nil {
			return e
		}
		return audit(ctx, q, r.OrganizationId, p.Email, "inventory_view.saved", row.ID, map[string]any{"revision": row.Revision})
	})
	return connect.NewResponse(out), err
}
func viewWriteError(err error) error {
	var pgerr *pgconn.PgError
	if errors.Is(err, pgx.ErrNoRows) {
		return conflict("Saved item changed or is unavailable. Reload before saving.")
	}
	if errors.As(err, &pgerr) && pgerr.Code == "23505" {
		return conflict("You already have a saved item with that name.")
	}
	return err
}
func (s *Service) DeleteInventoryView(ctx context.Context, req *connect.Request[pb.DeleteInventoryViewRequest]) (*connect.Response[pb.AccessMutationResponse], error) {
	r := req.Msg
	if len(r.Id) != 64 || r.ExpectedRevision < 1 {
		return nil, invalid("Choose a saved view.")
	}
	err := s.transaction(ctx, func(q *database.Queries) error {
		p := actor(ctx)
		if _, e := s.accessLock(ctx, q, r.OrganizationId, "resources.read"); e != nil {
			return e
		}
		if !identityAllowed(ctx, q, r.OrganizationId, p.UserID, p.OIDC) {
			return denied()
		}
		n, e := q.DeleteInventoryView(ctx, database.DeleteInventoryViewParams{ID: r.Id, OrgID: r.OrganizationId, UserID: p.UserID, Revision: r.ExpectedRevision})
		if e != nil {
			return e
		}
		if n != 1 {
			return conflict("Saved item changed or is unavailable. Reload before deleting.")
		}
		return audit(ctx, q, r.OrganizationId, p.Email, "inventory_view.deleted", r.Id, map[string]any{})
	})
	return connect.NewResponse(&pb.AccessMutationResponse{}), err
}

func validateTagFilter(key, value, name string, exists bool) error {
	if len(key) > 256 || len(value) > 2048 || len(name) > 256 || (key == "" && (value != "" || exists)) || (key != "" && name != "") || (exists && value != "") {
		return invalid("Choose one valid label or named-tag filter.")
	}
	return nil
}

func validateTagConditions(conditions []*pb.TagCondition, any bool, key, name string) error {
	if len(conditions) > 8 || (len(conditions) > 0 && (key != "" || name != "")) || (any && len(conditions) == 0) {
		return invalid("Choose up to eight combined tag conditions, without a separate single-tag filter.")
	}
	for _, c := range conditions {
		if c == nil || (c.Key == "" && c.Name == "") {
			return invalid("Choose a label or named tag for each condition.")
		}
		if e := validateTagFilter(c.Key, c.Value, c.Name, c.Exists); e != nil {
			return e
		}
	}
	return nil
}
func tagConditionsJSON(conditions []*pb.TagCondition) []byte {
	if len(conditions) == 0 {
		return []byte("[]")
	}
	raw, _ := json.Marshal(conditions) // Only validated string/bool fields.
	return raw
}

func validateResourceScope(connection, region, status string) error {
	if len(connection) > 64 || len(region) > 80 || len(status) > 80 {
		return invalid("Resource scope is too long.")
	}
	return nil
}
