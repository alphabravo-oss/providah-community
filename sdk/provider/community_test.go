package provider

import "testing"

func TestCommunityBoundary(t *testing.T) {
	for _, cloud := range []string{"aws", "digitalocean", "hetzner"} {
		r := CommunityRuntime(Runtime{Provider: cloud, DatabasePower: true, DatabaseSnapshotDelete: true, LoadBalancerDelete: true})
		if cloud == "aws" && (r.DatabasePower || r.DatabaseSnapshotDelete || r.LoadBalancerDelete) {
			t.Fatal("unsupported capabilities survived filtering")
		}
		for _, kind := range []string{"database.instance", "database.cluster", "database.snapshot", "database.cluster_snapshot", "network.load_balancer"} {
			if CommunityAction(cloud, kind) != (cloud != "aws") {
				t.Fatal("incorrect provider boundary")
			}
		}
		if !CommunityAction(cloud, "compute.server") {
			t.Fatal("Community compute blocked")
		}
	}
}
