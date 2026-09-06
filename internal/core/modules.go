package core

import (
	"context"
	"slices"

	"connectrpc.com/connect"
	"github.com/alphabravo-oss/providah-community/internal/database"
	pb "github.com/alphabravo-oss/providah-community/internal/gen/providah/v1"
	"github.com/alphabravo-oss/providah-community/sdk/provider"
)

func moduleAllowed(ctx context.Context, q *database.Queries, org, cloud string, revision int64) (bool, error) {
	state, err := q.GetProviderModule(ctx, database.GetProviderModuleParams{OrgID: org, Provider: cloud})
	return state.Enabled && (revision == 0 || revision == state.Revision), err
}
func requireModule(ctx context.Context, q *database.Queries, org, cloud string, revision int64) error {
	allowed, err := moduleAllowed(ctx, q, org, cloud, revision)
	if err != nil {
		return err
	}
	if !allowed {
		return conflict("Provider module is disabled or changed; review fresh work after enabling it.")
	}
	return nil
}
func (s *Service) ListProviderModules(ctx context.Context, req *connect.Request[pb.ListProviderModulesRequest]) (*connect.Response[pb.ListProviderModulesResponse], error) {
	rows, err := s.q.ListProviderModules(ctx, req.Msg.OrganizationId)
	if err != nil {
		return nil, err
	}
	out := &pb.ListProviderModulesResponse{}
	for _, row := range rows {
		entry := &pb.ProviderModule{Provider: row.Provider, Enabled: row.Enabled, Revision: row.Revision, Connections: row.Connections, ActiveOperations: row.ActiveOperations, ContractVersion: provider.Protocol, RuntimeId: s.selectedRuntime(row.Provider, row.RuntimeID)}
		capabilities := s.runtimeCapabilities(row.Provider, entry.RuntimeId)
		entry.PublisherKeyId = capabilities.PublisherKeyID
		entry.ApprovalExpiresAt = capabilities.ApprovalExpiresAt
		for _, kind := range capabilities.DiscoveryKinds() {
			entry.Capabilities = append(entry.Capabilities, kind+".discover")
		}
		if capabilities.SupportsResourceAction("access.ssh_key", "create") {
			entry.Capabilities = append(entry.Capabilities, "access.ssh_key.create")
		}
		for _, kind := range capabilities.DiscoveryKinds() {
			if kind != "compute.server" && capabilities.SupportsResourceAction(kind, "delete") {
				entry.Capabilities = append(entry.Capabilities, kind+".delete")
			}
		}
		for _, kind := range []string{"database.instance", "database.cluster"} {
			for _, action := range []string{"start", "shutdown"} {
				if capabilities.SupportsResourceAction(kind, action) {
					entry.Capabilities = append(entry.Capabilities, kind+"."+action)
				}
			}
		}
		for _, action := range capabilities.OperationActions() {
			entry.Capabilities = append(entry.Capabilities, "compute.server."+action)
		}
		for _, r := range s.cfg.ProviderRuntimes {
			if r.Provider == row.Provider {
				entry.Runtimes = append(entry.Runtimes, &pb.ProviderRuntime{Image: r.Image, Version: r.Version, SdkVersion: r.SDKVersion, PublisherKeyId: r.PublisherKeyID, ApprovalExpiresAt: r.ApprovalExpiresAt})
			}
		}
		out.Modules = append(out.Modules, entry)
	}
	return connect.NewResponse(out), nil
}
func (s *Service) SetProviderModule(ctx context.Context, req *connect.Request[pb.SetProviderModuleRequest]) (*connect.Response[pb.AccessMutationResponse], error) {
	r := req.Msg
	if !slices.Contains([]string{"aws", "digitalocean", "hetzner"}, r.Provider) || r.Confirmation != r.Provider {
		return nil, invalid("Confirm the provider ID exactly.")
	}
	err := s.transaction(ctx, func(q *database.Queries) error {
		if _, err := q.LockAccess(ctx, r.OrganizationId); err != nil {
			return denied()
		}
		if !hasPermission(ctx, q, r.OrganizationId, actor(ctx).UserID, "modules.manage") {
			return denied()
		}
		state, err := q.GetProviderModule(ctx, database.GetProviderModuleParams{OrgID: r.OrganizationId, Provider: r.Provider})
		if err != nil {
			return err
		}
		if state.Enabled == r.Enabled {
			return nil
		}
		if err = q.SetProviderModule(ctx, database.SetProviderModuleParams{OrgID: r.OrganizationId, Provider: r.Provider, Enabled: r.Enabled}); err != nil {
			return err
		}
		return audit(ctx, q, r.OrganizationId, actor(ctx).Email, "provider.module_state_changed", r.Provider, map[string]any{"provider": r.Provider, "enabled": r.Enabled, "revision": state.Revision + 1})
	})
	return connect.NewResponse(&pb.AccessMutationResponse{}), err
}

func (s *Service) runtimeAvailable(cloud, id string) bool {
	// In-process test providers have no external runtime catalog. Production startup
	// always loads a validated catalog whenever a launcher socket is configured.
	if len(s.cfg.ProviderRuntimes) == 0 {
		return id == ""
	}
	for _, r := range s.cfg.ProviderRuntimes {
		if r.Provider == cloud && r.Image == id {
			return true
		}
	}
	return false
}
func (s *Service) selectedRuntime(cloud, selected string) string {
	if selected != "" {
		return selected
	}
	for _, r := range s.cfg.ProviderRuntimes {
		if r.Provider == cloud {
			return r.Image
		}
	}
	return ""
}
func (s *Service) runtimeFor(ctx context.Context, q *database.Queries, org, cloud string, expected int64) (string, int64, error) {
	state, err := q.GetProviderModule(ctx, database.GetProviderModuleParams{OrgID: org, Provider: cloud})
	if err != nil {
		return "", 0, err
	}
	if !state.Enabled || (expected != 0 && state.Revision != expected) {
		return "", 0, conflict("Provider module changed; fresh review is required.")
	}
	id := s.selectedRuntime(cloud, state.RuntimeID)
	if !s.runtimeAvailable(cloud, id) {
		return "", 0, conflict("The selected provider runtime is unavailable. Choose an approved runtime.")
	}
	return id, state.Revision, nil
}
func (s *Service) SetProviderRuntime(ctx context.Context, req *connect.Request[pb.SetProviderRuntimeRequest]) (*connect.Response[pb.AccessMutationResponse], error) {
	r := req.Msg
	if r.RuntimeId == "" || !s.runtimeAvailable(r.Provider, r.RuntimeId) || r.Confirmation != r.Provider {
		return nil, invalid("Choose a deployment-approved runtime and confirm the provider ID.")
	}
	err := s.transaction(ctx, func(q *database.Queries) error {
		if _, err := q.LockAccess(ctx, r.OrganizationId); err != nil {
			return denied()
		}
		if !hasPermission(ctx, q, r.OrganizationId, actor(ctx).UserID, "modules.manage") {
			return denied()
		}
		state, err := q.GetProviderModule(ctx, database.GetProviderModuleParams{OrgID: r.OrganizationId, Provider: r.Provider})
		if err != nil {
			return err
		}
		if state.Revision != r.ExpectedRevision {
			return conflict("The module changed. Reload and review the runtime selection again.")
		}
		if state.RuntimeID == r.RuntimeId {
			return nil
		}
		previous := s.selectedRuntime(r.Provider, state.RuntimeID)
		if err = q.SetProviderRuntime(ctx, database.SetProviderRuntimeParams{OrgID: r.OrganizationId, Provider: r.Provider, RuntimeID: r.RuntimeId}); err != nil {
			return err
		}
		return audit(ctx, q, r.OrganizationId, actor(ctx).Email, "provider.runtime_changed", r.Provider, map[string]any{"provider": r.Provider, "revision": state.Revision + 1, "previous_runtime": previous, "runtime": r.RuntimeId})
	})
	return connect.NewResponse(&pb.AccessMutationResponse{}), err
}

func (s *Service) runtimeCapabilities(cloud, image string) (out provider.Runtime) {
	defer func() {
		if s.cfg.RuntimePolicy != nil {
			out = s.cfg.RuntimePolicy(out)
		} else {
			out = provider.CommunityRuntime(out)
		}
	}()
	if len(s.cfg.ProviderRuntimes) == 0 {
		return provider.Runtime{PlacementGroupDelete: cloud == "aws" || cloud == "hetzner", ProjectDelete: cloud == "digitalocean", ImageDelete: cloud == "aws", PrivateNetworkCreate: cloud != "aws", LoadBalancerDelete: true, DatabaseSnapshotDelete: cloud == "aws", SecurityGroupDelete: cloud == "aws", SSHKeyCreate: true, SSHKeyDelete: true, NetworkDelete: cloud != "aws", DatabasePower: cloud == "aws", VolumeDelete: true, SnapshotDelete: true, Metrics: true, Provider: cloud, CapabilitiesVersion: 1, InventoryKinds: provider.InventoryKinds(cloud), Actions: []string{"start", "shutdown", "restart", "delete", "create", "resize", "snapshot", "tags"}}
	}
	for _, r := range s.cfg.ProviderRuntimes {
		if r.Provider == cloud && r.Image == image {
			return r
		}
	}
	return provider.Runtime{Provider: cloud, CapabilitiesVersion: 1}
}
