package core

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
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

func creationProto(raw []byte) *pb.ServerCreate {
	var c provider.ServerCreate
	if json.Unmarshal(raw, &c) != nil || c.Name == "" || c.Image == "" {
		return nil
	}
	return &pb.ServerCreate{Name: c.Name, Image: c.Image, Size: c.Size, SshKey: c.SSHKey, Subnet: c.Subnet, Network: c.Network, SecurityGroup: c.SecurityGroup}
}
func (s *Service) RequestServerCreation(ctx context.Context, req *connect.Request[pb.RequestServerCreationRequest]) (*connect.Response[pb.OperationResponse], error) {
	return s.requestCreation(ctx, req.Msg, nil)
}
func (s *Service) RequestSSHKeyCreation(ctx context.Context, req *connect.Request[pb.RequestSSHKeyCreationRequest]) (*connect.Response[pb.OperationResponse], error) {
	v := req.Msg
	if v.Creation == nil {
		return nil, invalid("Supply public key configuration.")
	}
	return s.requestCreation(ctx, &pb.RequestServerCreationRequest{OrganizationId: v.OrganizationId, ConnectionId: v.ConnectionId, Region: v.Region, Creation: &pb.ServerCreate{Name: v.Creation.Name}, Reason: v.Reason, IdempotencyKey: v.IdempotencyKey}, &provider.SSHKeyCreate{Name: v.Creation.Name, PublicKey: v.Creation.PublicKey})
}
func (s *Service) requestCreation(ctx context.Context, r *pb.RequestServerCreationRequest, key *provider.SSHKeyCreate) (*connect.Response[pb.OperationResponse], error) {
	if s.cfg.ProviderCall == nil {
		return nil, conflict("Provider execution is not configured.")
	}
	if r.Creation == nil || !regexp.MustCompile(`^[a-z0-9-]{1,64}$`).MatchString(r.Region) || !regexp.MustCompile(`^[a-f0-9-]{16,80}$`).MatchString(r.IdempotencyKey) {
		return nil, invalid("Choose a region and creation configuration.")
	}
	reason := strings.TrimSpace(r.Reason)
	if len(reason) < 3 || len(reason) > 500 {
		return nil, invalid("Provide a creation reason in 3–500 characters.")
	}
	input := provider.ServerCreate{Name: r.Creation.Name, Image: r.Creation.Image, Size: r.Creation.Size, SSHKey: r.Creation.SshKey, Subnet: r.Creation.Subnet, Network: r.Creation.Network, SecurityGroup: r.Creation.SecurityGroup}
	kind := "compute.server"
	var config any = input
	if key != nil {
		kind = "access.ssh_key"
		config = key
	}
	raw, _ := json.Marshal(config)
	id := ""
	err := s.transaction(ctx, func(q *database.Queries) error {
		c, err := q.LockOperationConnection(ctx, database.LockOperationConnectionParams{OrgID: r.OrganizationId, ID: r.ConnectionId})
		if err != nil {
			return denied()
		}
		if !powerPermission(ctx, q, r.OrganizationId, actor(ctx).UserID, "operations.request") || !powerPermission(ctx, q, r.OrganizationId, actor(ctx).UserID, "operations.create") {
			return denied()
		}

		templateStatus := ""
		if r.TemplateId != "" {
			if !hasPermission(ctx, q, r.OrganizationId, actor(ctx).UserID, "templates.read") {
				return denied()
			}
			template, e := q.GetServerTemplate(ctx, database.GetServerTemplateParams{OrgID: r.OrganizationId, ID: r.TemplateId})
			if e != nil {
				return denied()
			}
			var fixed provider.ServerCreate
			if json.Unmarshal(template.Creation, &fixed) != nil {
				return conflict("Template configuration is unavailable.")
			}
			fixed.Name = input.Name
			if template.ConnectionID != c.ID || template.Region != r.Region || fixed != input {
				return invalid("Deployment must match the published template; only the server name may change.")
			}
			templateStatus = template.Status
		}
		if key != nil {
			err = key.Validate()
			if c.Provider == "aws" && r.Region == "global" {
				return invalid("AWS SSH keys require an EC2 region.")
			}
			if c.Provider != "aws" && r.Region != "global" {
				return invalid("SSH keys use global scope for this provider.")
			}
		} else {
			err = input.Validate(c.Provider)
		}
		if err != nil {
			return invalid(err.Error())
		}
		previous, err := q.OperationByKey(ctx, database.OperationByKeyParams{OrgID: r.OrganizationId, RequesterID: actor(ctx).UserID, IdempotencyKey: r.IdempotencyKey})
		if err == nil {
			var old provider.ServerCreate
			sameInput := json.Unmarshal(previous.Creation, &old) == nil && old == input
			if key != nil {
				var oldKey provider.SSHKeyCreate
				sameInput = json.Unmarshal(previous.Creation, &oldKey) == nil && oldKey == *key
			}
			if previous.ResourceKind != kind || previous.TemplateID.String != r.TemplateId || previous.Action != "create" || previous.ConnectionID != c.ID || previous.Region != r.Region || previous.Reason != reason || !sameInput {
				return conflict("This request key belongs to different input.")
			}
			id = previous.ID
			return nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		if r.TemplateId != "" && templateStatus != "published" {
			return conflict("This template no longer accepts new deployments.")
		}
		if !c.Enabled || c.DeletedAt.Valid || (key == nil || c.Provider == "aws") && c.Region != "" && c.Region != r.Region {
			return conflict("The region must match an enabled connection.")
		}
		runtime, revision, err := s.runtimeFor(ctx, q, c.OrgID, c.Provider, 0)
		if err != nil {
			return err
		}
		if input.Network != "" && !s.runtimeCapabilities(c.Provider, runtime).PrivateNetworkCreate {
			return conflict("This runtime does not support private network selection.")
		}
		if !s.runtimeCapabilities(c.Provider, runtime).SupportsResourceAction(kind, "create") {
			return conflict("This provider runtime does not support this resource creation.")
		}
		maintenance, restriction, end, err := maintenanceCheck(ctx, q, c.OrgID, c.ID, time.Now())
		if err != nil {
			return err
		}
		if restriction != "" {
			return conflict(restriction)
		}
		id = newOperationID()
		_, err = q.CreateOperation(ctx, database.CreateOperationParams{TraceParent: traceParent(ctx), ResourceKind: kind, ID: id, OrgID: c.OrgID, ConnectionID: c.ID, ConnectionRevision: c.Revision, Provider: c.Provider, Region: r.Region, ResourceName: input.Name, Action: "create", Reason: reason, RequesterID: actor(ctx).UserID, RequesterEmail: actor(ctx).Email, Status: "awaiting_approval", IdempotencyKey: r.IdempotencyKey, MaintenanceRevision: maintenance, MaintenanceExpiresAt: dbTime(end), ModuleRevision: revision, RuntimeID: runtime})
		if err != nil {
			return err
		}

		if r.TemplateId != "" {
			if err = q.BindOperationTemplate(ctx, database.BindOperationTemplateParams{OrgID: c.OrgID, ID: id, TemplateID: pgtype.Text{String: r.TemplateId, Valid: true}}); err != nil {
				return err
			}
		}
		if err = q.SaveCreationInput(ctx, database.SaveCreationInputParams{OrgID: c.OrgID, ID: id, Creation: raw}); err != nil {
			return err
		}
		if err = q.StampOperationIdentity(ctx, database.StampOperationIdentityParams{OrgID: c.OrgID, ID: id, RequesterOidcID: actor(ctx).OIDC}); err != nil {
			return err
		}
		return audit(ctx, q, c.OrgID, actor(ctx).Email, "operation.requested", id, map[string]any{"action": "create", "creation": config, "resource_kind": kind, "region": r.Region, "reason": reason, "status": "awaiting_approval"})
	})
	if err != nil {
		var pe *pgconn.PgError
		if errors.As(err, &pe) && pe.Code == "23505" {
			return nil, conflict("This resource name already has pending or uncertain creation work. Resolve it before another request.")
		}
		return nil, err
	}
	return s.operationResponse(ctx, r.OrganizationId, id)
}

func bindCreatedResource(ctx context.Context, q *database.Queries, o database.Operation, result *provider.PowerResult) error {
	if result == nil || result.NativeID == "" {
		return nil
	}
	if o.ResourceKind == "access.ssh_key" {
		if result.Outcome != "succeeded" {
			return q.BindCreatedServer(ctx, database.BindCreatedServerParams{OrgID: o.OrgID, ID: o.ID, NativeID: result.NativeID, ResourceID: o.ResourceID})
		}
		var key provider.SSHKeyCreate
		if err := json.Unmarshal(o.Creation, &key); err != nil {
			return err
		}
		id := hex.EncodeToString(hash(fmt.Sprintf("%s\n%s\naccess.ssh_key\n%s\n%s", o.OrgID, o.ConnectionID, o.Region, result.NativeID)))
		id, err := q.UpsertCreatedSSHKey(ctx, database.UpsertCreatedSSHKeyParams{ID: id, OrgID: o.OrgID, ConnectionID: o.ConnectionID, NativeID: result.NativeID, Name: key.Name + "-" + o.ID, Provider: o.Provider, Region: o.Region, Status: result.Status})
		if err != nil {
			return err
		}
		return q.BindCreatedServer(ctx, database.BindCreatedServerParams{OrgID: o.OrgID, ID: o.ID, NativeID: result.NativeID, ResourceID: pgtype.Text{String: id, Valid: true}})
	}
	id := hex.EncodeToString(hash(fmt.Sprintf("%s\n%s\ncompute.server\n%s\n%s", o.OrgID, o.ConnectionID, o.Region, result.NativeID)))
	var input provider.ServerCreate
	if err := json.Unmarshal(o.Creation, &input); err != nil {
		return err
	}
	id, err := q.UpsertCreatedServer(ctx, database.UpsertCreatedServerParams{ID: id, OrgID: o.OrgID, ConnectionID: o.ConnectionID, NativeID: result.NativeID, Name: input.Name, Provider: o.Provider, Region: o.Region, Status: result.Status, Size: input.Size})
	if err != nil {
		return err
	}
	return q.BindCreatedServer(ctx, database.BindCreatedServerParams{OrgID: o.OrgID, ID: o.ID, NativeID: result.NativeID, ResourceID: pgtype.Text{String: id, Valid: true}})
}

func keyCreationProto(raw []byte) *pb.SSHKeyCreate {
	var c provider.SSHKeyCreate
	if json.Unmarshal(raw, &c) != nil || c.PublicKey == "" {
		return nil
	}
	return &pb.SSHKeyCreate{Name: c.Name, PublicKey: c.PublicKey}
}
