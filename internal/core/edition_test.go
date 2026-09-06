package core

import (
	"github.com/alphabravo-oss/providah-community/sdk/provider"
	"testing"
)

func TestCommunityFiltersWorkerClaims(t *testing.T) {
	runtime := provider.Runtime{Provider: "aws", Image: "test", CapabilitiesVersion: 1, InventoryKinds: provider.InventoryKinds("aws"), Actions: []string{"start", "shutdown", "delete"}, DatabasePower: true, DatabaseSnapshotDelete: true, LoadBalancerDelete: true}
	s := &Service{cfg: Config{ProviderRuntimes: []provider.Runtime{runtime}}}
	filtered := s.runtimeCapabilities("aws", "test")
	for _, kind := range []string{"database.instance", "database.cluster", "database.snapshot", "database.cluster_snapshot", "network.load_balancer"} {
		for _, action := range []string{"start", "shutdown", "delete"} {
			if filtered.SupportsResourceAction(kind, action) {
				t.Fatal("worker claims enabled an unsupported action")
			}
		}
	}
	if !filtered.SupportsResourceAction("compute.server", "start") {
		t.Fatal("Community compute was removed")
	}
}
