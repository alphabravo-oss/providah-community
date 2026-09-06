package provider

import (
	"strings"
	"testing"
)

func TestRuntimeCatalog(t *testing.T) {
	r := Runtime{Provider: "aws", Image: "sha256:" + strings.Repeat("a", 64), Version: "1", SDKVersion: "1", Protocol: Protocol}
	if ValidateRuntimes([]Runtime{r}) != nil {
		t.Fatal("valid catalog rejected")
	}
	if ValidateRuntimes([]Runtime{r, r}) == nil || ValidateRuntimes(nil) == nil {
		t.Fatal("invalid catalog accepted")
	}
	r.Image = "untrusted:latest"
	if r.Validate() == nil {
		t.Fatal("mutable image accepted")
	}
}

func TestRuntimeCapabilities(t *testing.T) {
	legacy := Runtime{Provider: "hetzner", Image: "sha256:" + strings.Repeat("a", 64), Version: "1", SDKVersion: "1", Protocol: Protocol}
	if !legacy.Accepts(Request{}) || legacy.Accepts(Request{InventoryKinds: []string{"compute.server"}}) || legacy.SupportsAction("delete") || legacy.SupportsAction("create") || legacy.SupportsAction("tags") {
		t.Fatal("legacy contract not preserved")
	}
	current := legacy
	current.CapabilitiesVersion = 1
	current.InventoryKinds = []string{"compute.server", "compute.image"}
	if current.Validate() != nil || !current.Accepts(Request{InventoryKinds: []string{"compute.image"}}) || current.Accepts(Request{Power: &PowerRequest{Action: "start"}}) {
		t.Fatal("read-only manifest not enforced")
	}
	current.Actions = []string{"delete"}
	if !current.SupportsAction("delete") || current.SupportsAction("restart") {
		t.Fatal("action declaration not enforced")
	}
	current.InventoryKinds = []string{"compute.image", "compute.image"}
	if current.Validate() == nil {
		t.Fatal("duplicate capability accepted")
	}
}

func TestSnapshotDeletionCapability(t *testing.T) {
	r := Runtime{CapabilitiesVersion: 1, Actions: []string{"delete"}}
	request := Request{Power: &PowerRequest{Action: "delete", ResourceKind: "storage.snapshot"}}
	if r.Accepts(request) {
		t.Fatal("old runtime accepted snapshot deletion")
	}
	r.SnapshotDelete = true
	if !r.Accepts(request) {
		t.Fatal("declared snapshot deletion rejected")
	}
	request.Power.ResourceKind = "storage.backup"
	if r.Accepts(request) {
		t.Fatal("undeclared backup deletion accepted")
	}
}

func TestVolumeDeletionCapability(t *testing.T) {
	r := Runtime{CapabilitiesVersion: 1, Actions: []string{"delete"}}
	request := Request{Power: &PowerRequest{Action: "delete", ResourceKind: "storage.volume"}}
	if r.Accepts(request) {
		t.Fatal("old runtime accepted volume deletion")
	}
	r.VolumeDelete = true
	if !r.Accepts(request) {
		t.Fatal("volume capability ignored")
	}
	if ResourceActionAllowed("storage.volume", "delete", "attached") || ResourceActionAllowed("storage.volume", "start", "available") {
		t.Fatal("invalid volume action permitted")
	}
}

func TestDatabasePowerCapability(t *testing.T) {
	r := Runtime{Provider: "aws", CapabilitiesVersion: 1, InventoryKinds: []string{"database.instance", "database.cluster"}, Actions: []string{"start", "shutdown"}}
	request := Request{Power: &PowerRequest{ResourceKind: "database.instance", Action: "start"}}
	if r.Accepts(request) {
		t.Fatal("old runtime accepted database power")
	}
	r.DatabasePower = true
	if !r.Accepts(request) {
		t.Fatal("declared database action rejected")
	}
	request.Power.Action = "delete"
	if r.Accepts(request) {
		t.Fatal("database deletion unexpectedly enabled")
	}
	r.Provider = "digitalocean"
	request.Power.Action = "start"
	if r.Accepts(request) {
		t.Fatal("AWS database capability granted to another provider")
	}
}

func TestNetworkDeleteCapability(t *testing.T) {
	for _, cloud := range []string{"digitalocean", "hetzner"} {
		r := Runtime{Provider: cloud, Image: "sha256:" + strings.Repeat("a", 64), Version: "1", SDKVersion: "1", Protocol: Protocol, CapabilitiesVersion: 1, Actions: []string{"delete"}, InventoryKinds: []string{"network.network", "network.firewall"}}
		for _, kind := range r.InventoryKinds {
			if r.SupportsResourceAction(kind, "delete") {
				t.Fatal("legacy runtime gained deletion")
			}
		}
		r.NetworkDelete = true
		if r.Validate() != nil {
			t.Fatal("valid network capability rejected")
		}
		for _, kind := range r.InventoryKinds {
			if !r.SupportsResourceAction(kind, "delete") || r.SupportsResourceAction(kind, "start") {
				t.Fatal("wrong network actions")
			}
		}
		r.Provider = "aws"
		if r.Validate() == nil || r.SupportsResourceAction("network.network", "delete") {
			t.Fatal("AWS network delete advertised")
		}
	}
}

func TestSSHKeyDeleteCapability(t *testing.T) {
	for _, cloud := range []string{"aws", "digitalocean", "hetzner"} {
		r := Runtime{Provider: cloud, Image: "sha256:" + strings.Repeat("a", 64), Version: "1", SDKVersion: "1", Protocol: Protocol, CapabilitiesVersion: 1, Actions: []string{"delete"}, InventoryKinds: []string{"access.ssh_key"}}
		if r.SupportsResourceAction("access.ssh_key", "delete") {
			t.Fatal("legacy runtime gained deletion")
		}
		r.SSHKeyDelete = true
		if r.Validate() != nil || !r.SupportsResourceAction("access.ssh_key", "delete") || r.SupportsResourceAction("access.ssh_key", "start") {
			t.Fatal("incorrect key capability")
		}
		r.CapabilitiesVersion = 0
		if r.Validate() == nil {
			t.Fatal("unversioned key capability accepted")
		}
	}
}

func TestSecurityGroupCapability(t *testing.T) {
	r := Runtime{Provider: "aws", Image: "sha256:" + strings.Repeat("a", 64), Version: "1", SDKVersion: "1", Protocol: Protocol, CapabilitiesVersion: 1, Actions: []string{"delete"}, InventoryKinds: []string{"network.firewall", "network.network"}}
	if r.SupportsResourceAction("network.firewall", "delete") {
		t.Fatal("old runtime gained deletion")
	}
	r.SecurityGroupDelete = true
	if r.Validate() != nil || !r.SupportsResourceAction("network.firewall", "delete") || r.SupportsResourceAction("network.network", "delete") {
		t.Fatal("wrong capability")
	}
	p := PowerRequest{ResourceKind: "network.firewall", Action: "delete", Phase: "preview", OperationID: strings.Repeat("a", 64), NativeID: "sg-12345678", ExpectedStatus: "present"}
	if p.Validate("aws") != nil {
		t.Fatal("valid group rejected")
	}
	p.NativeID = "i-12345678"
	if p.Validate("aws") == nil {
		t.Fatal("wrong identifier accepted")
	}
	p.ResourceKind = "network.network"
	p.NativeID = "vpc-12345678"
	if p.Validate("aws") == nil {
		t.Fatal("VPC deletion accepted")
	}
	r.Provider = "hetzner"
	if r.Validate() == nil {
		t.Fatal("AWS capability accepted for another provider")
	}
}

func TestDatabaseSnapshotCapability(t *testing.T) {
	r := Runtime{Provider: "aws", Image: "sha256:" + strings.Repeat("a", 64), Version: "1", SDKVersion: "1", Protocol: Protocol, CapabilitiesVersion: 1, Actions: []string{"delete"}, InventoryKinds: []string{"database.snapshot", "database.cluster_snapshot"}}
	for _, kind := range r.InventoryKinds {
		if r.SupportsResourceAction(kind, "delete") {
			t.Fatal("old runtime gained deletion")
		}
	}
	r.DatabaseSnapshotDelete = true
	if r.Validate() != nil {
		t.Fatal("valid capability rejected")
	}
	for _, kind := range r.InventoryKinds {
		if !r.SupportsResourceAction(kind, "delete") || r.SupportsResourceAction(kind, "start") {
			t.Fatal("wrong snapshot capability")
		}
		p := PowerRequest{ResourceKind: kind, Action: "delete", Phase: "preview", OperationID: strings.Repeat("a", 64), NativeID: "manual-one", ExpectedStatus: "available"}
		if p.Validate("aws") != nil || p.Validate("hetzner") == nil {
			t.Fatal("wrong snapshot request provider")
		}
		p.NativeID = "rds:automated-snapshot"
		if p.Validate("aws") == nil {
			t.Fatal("automated snapshot identifier accepted")
		}
	}
	r.Provider = "hetzner"
	if r.Validate() == nil {
		t.Fatal("foreign capability accepted")
	}
}

func TestLoadBalancerCapability(t *testing.T) {
	for _, cloud := range []string{"aws", "digitalocean", "hetzner"} {
		r := Runtime{Provider: cloud, Image: "sha256:" + strings.Repeat("a", 64), Version: "1", SDKVersion: "1", Protocol: Protocol, CapabilitiesVersion: 1, Actions: []string{"delete"}, InventoryKinds: []string{"network.load_balancer"}}
		if r.SupportsResourceAction("network.load_balancer", "delete") {
			t.Fatal("old runtime gained deletion")
		}
		r.LoadBalancerDelete = true
		if r.Validate() != nil || !r.SupportsResourceAction("network.load_balancer", "delete") || r.SupportsResourceAction("network.load_balancer", "start") {
			t.Fatal("wrong capability")
		}
		r.CapabilitiesVersion = 0
		if r.Validate() == nil {
			t.Fatal("legacy capability version accepted")
		}
	}
}

func TestServerImageRuntimeActions(t *testing.T) {
	r := Runtime{Provider: "hetzner", Image: "sha256:" + strings.Repeat("a", 64), Version: "test", SDKVersion: "test", Protocol: Protocol, CapabilitiesVersion: 1, InventoryKinds: []string{"compute.server"}, Actions: []string{"start", "shutdown", "restart", "delete", "create", "resize", "snapshot"}}
	if e := r.Validate(); e != nil {
		t.Fatal("complete action catalog rejected", e)
	}
	if !r.SupportsResourceAction("compute.server", "snapshot") || (Runtime{Provider: "hetzner"}).SupportsAction("snapshot") {
		t.Fatal("snapshot capability compatibility failed")
	}
	r.Actions = append(r.Actions, "snapshot")
	if r.Validate() == nil {
		t.Fatal("duplicate action accepted")
	}
}

func TestImageDeletionCapability(t *testing.T) {
	r := Runtime{Provider: "aws", Image: "sha256:" + strings.Repeat("a", 64), Version: "1", SDKVersion: "1", Protocol: Protocol, CapabilitiesVersion: 1, InventoryKinds: []string{"compute.image"}, Actions: []string{"delete"}}
	if r.SupportsResourceAction("compute.image", "delete") {
		t.Fatal("legacy runtime gained deletion")
	}
	r.ImageDelete = true
	if r.Validate() != nil || !r.SupportsResourceAction("compute.image", "delete") || r.SupportsResourceAction("compute.image", "start") {
		t.Fatal("invalid capability")
	}
	p := PowerRequest{ResourceKind: "compute.image", Action: "delete", Phase: "preview", OperationID: strings.Repeat("a", 64), NativeID: "ami-12345678", ExpectedStatus: "available"}
	if p.Validate("aws") != nil || p.Validate("hetzner") == nil {
		t.Fatal("invalid provider gate")
	}
	p.NativeID = "ami-123456789"
	if p.Validate("aws") == nil {
		t.Fatal("invalid AMI accepted")
	}
	r.Provider = "hetzner"
	if r.Validate() == nil {
		t.Fatal("non AWS capability accepted")
	}
}

func TestCloudProjectCapability(t *testing.T) {
	r := Runtime{Provider: "digitalocean", Image: "sha256:" + strings.Repeat("a", 64), Version: "1", SDKVersion: "1", Protocol: Protocol, CapabilitiesVersion: 1, InventoryKinds: []string{"organization.project"}, Actions: []string{"delete"}}
	if r.SupportsResourceAction("organization.project", "delete") {
		t.Fatal("old runtime gained deletion")
	}
	r.ProjectDelete = true
	if r.Validate() != nil || !r.SupportsResourceAction("organization.project", "delete") || r.SupportsResourceAction("organization.project", "start") {
		t.Fatal("invalid capability")
	}
	p := PowerRequest{ResourceKind: "organization.project", NativeID: "506f78a4-e098-11e5-ad9f-000f53306ae1", Action: "delete", Phase: "preview", ExpectedStatus: "present", OperationID: strings.Repeat("a", 64)}
	if p.Validate("digitalocean") != nil || p.Validate("aws") == nil || p.Validate("hetzner") == nil {
		t.Fatal("incorrect provider validation")
	}
	p.NativeID = "default"
	if p.Validate("digitalocean") == nil {
		t.Fatal("default alias accepted")
	}
	r.Provider = "aws"
	if r.Validate() == nil {
		t.Fatal("non DigitalOcean capability accepted")
	}
}

func TestSSHKeyCreationCapability(t *testing.T) {
	for _, cloud := range []string{"aws", "digitalocean", "hetzner"} {
		r := Runtime{Provider: cloud, Image: "sha256:" + strings.Repeat("a", 64), Version: "1", SDKVersion: "1", Protocol: Protocol, CapabilitiesVersion: 1, InventoryKinds: []string{"access.ssh_key"}, Actions: []string{"create"}}
		if r.SupportsResourceAction("access.ssh_key", "create") {
			t.Fatal("old runtime gained key import")
		}
		r.SSHKeyCreate = true
		if r.Validate() != nil || !r.SupportsResourceAction("access.ssh_key", "create") || r.SupportsResourceAction("access.ssh_key", "delete") {
			t.Fatal("wrong key import capability")
		}
		r.CapabilitiesVersion = 0
		if r.Validate() == nil || r.SupportsResourceAction("access.ssh_key", "create") {
			t.Fatal("legacy capability accepted")
		}
	}
}

func TestPlacementDeletionCapability(t *testing.T) {
	for _, cloud := range []string{"aws", "hetzner"} {
		r := Runtime{Provider: cloud, CapabilitiesVersion: 1, Actions: []string{"delete"}, InventoryKinds: []string{"compute.placement_group"}}
		if r.SupportsResourceAction("compute.placement_group", "delete") {
			t.Fatal("old runtime gained deletion")
		}
		r.PlacementGroupDelete = true
		if !r.SupportsResourceAction("compute.placement_group", "delete") || r.SupportsResourceAction("compute.placement_group", "start") {
			t.Fatal("incorrect placement capability")
		}
		r.Provider = "digitalocean"
		if r.SupportsResourceAction("compute.placement_group", "delete") || r.Validate() == nil {
			t.Fatal("unsupported provider accepted")
		}
	}
}
