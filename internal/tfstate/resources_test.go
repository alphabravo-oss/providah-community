package tfstate

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestResourceIdentities(t *testing.T) {
	state := func(resources string) []byte {
		return []byte(`{"version":4,"lineage":"00112233-4455-6677-8899-aabbccddeeff","serial":1,"outputs":{"secret":{"value":"never-return"}},"resources":` + resources + `}`)
	}
	for _, tc := range []struct{ typ, cloud, source, id string }{
		{"aws_instance", "aws", "hashicorp/aws", "i-1234567890abcdef0"},
		{"digitalocean_droplet", "digitalocean", "digitalocean/digitalocean", "123"},
		{"hcloud_server", "hetzner", "hetznercloud/hcloud", "456"},
	} {
		config := `provider["registry.terraform.io/` + tc.source + `"].reviewed`
		quoted, _ := json.Marshal(config)
		r := `{"mode":"managed","type":"` + tc.typ + `","provider":` + string(quoted) + `,"instances":[{"attributes":{"id":"` + tc.id + `","password":"never-return"}},{"attributes":{"id":"` + tc.id + `"}}]}`
		refs, unknown, err := ResourceIdentities(state("[" + r + "]"))
		if err != nil || unknown != 0 || len(refs) != 1 || refs[0].Provider != tc.cloud || refs[0].NativeID != tc.id || refs[0].ProviderConfig != config {
			t.Fatal(refs, unknown, err)
		}
		output, _ := json.Marshal(refs)
		if strings.Contains(string(output), "never-return") {
			t.Fatal("state values escaped")
		}
		if _, _, err = ResourceIdentities(state("[" + strings.Replace(r, `"id":"`+tc.id+`"`, `"id":"`+tc.id+`","id":"other"`, 1) + "]")); err == nil {
			t.Fatal("duplicate identity accepted")
		}
		refs, unknown, err = ResourceIdentities(state("[" + strings.Replace(r, tc.typ, "unsupported_type", 1) + "]"))
		if err != nil || len(refs) != 0 || unknown != 2 {
			t.Fatal("unsupported resources hidden", refs, unknown, err)
		}
		refs, unknown, err = ResourceIdentities(state("[" + strings.Replace(r, `"managed"`, `"data"`, 1) + "]"))
		if err != nil || len(refs) != 0 || unknown != 0 {
			t.Fatal("data source claimed ownership")
		}
	}
	refs, unknown, err := ResourceIdentities(state(`[{"mode":"managed","type":"unsupported","provider":"other","instances":[{"attributes":{"id":123}}]},{"mode":"data","type":"hcloud_server","instances":[{"attributes":{"id":{}}}]}]`))
	if err != nil || len(refs) != 0 || unknown != 1 {
		t.Fatal("unsupported attribute shape corrupted scan", refs, unknown, err)
	}
	if _, _, err := ResourceIdentities(state(`[]`)); err != nil {
		t.Fatal("empty state", err)
	}
	nested := `{"x":` + strings.Repeat(`[`, 65) + `0` + strings.Repeat(`]`, 65) + `}`
	if _, _, err := ResourceIdentities([]byte(strings.Replace(string(state(`[]`)), `"resources":[]`, `"resources":[],"extra":`+nested, 1))); err == nil {
		t.Fatal("excessive nesting accepted")
	}
	if _, _, err := ResourceIdentities(state(`[ {"mode":"managed","type":"hcloud_server","provider":"provider[\"registry.terraform.io/hetznercloud/hcloud\"]","instances":[{"attributes":{"id":"bad"}}]} ]`)); err != nil {
		t.Fatal("invalid ID should be reported as uncovered", err)
	}
}

func TestInfrastructureStateIdentities(t *testing.T) {
	cases := []struct{ typ, kind, field, id string }{
		{"aws_s3_bucket", "storage.bucket", "id", "bucket-0"},
		{"aws_s3_bucket_policy", "storage.bucket", "bucket", "bucket-1"},
		{"aws_s3_bucket_versioning", "storage.bucket", "bucket", "bucket-2"},
		{"aws_s3_bucket_lifecycle_configuration", "storage.bucket", "bucket", "bucket-3"},

		{"aws_placement_group", "compute.placement_group", "placement_group_id", "pg-1234567890abcdef0"},
		{"hcloud_placement_group", "compute.placement_group", "id", "445566"},
		{"digitalocean_database_cluster", "database.cluster", "id", "506f78a4-e098-11e5-ad9f-000f53306ae1"},
		{"aws_eks_cluster", "kubernetes.cluster", "id", "production"},
		{"aws_eks_node_group", "kubernetes.node_group", "id", "production:workers"},
		{"aws_route_table", "network.route_table", "id", "rtb-1234567890abcdef0"},
		{"aws_internet_gateway", "network.internet_gateway", "id", "igw-1234567890abcdef0"},
		{"aws_nat_gateway", "network.nat_gateway", "id", "nat-1234567890abcdef0"},
		{"aws_ami", "compute.image", "id", "ami-1234567890abcdef0"},
		{"aws_ami_copy", "compute.image", "id", "ami-1234567890abcdef1"},
		{"aws_ami_from_instance", "compute.image", "id", "ami-1234567890abcdef2"},
		{"digitalocean_kubernetes_node_pool", "kubernetes.node_group", "id", "506f78a4-e098-11e5-ad9f-000f53306ae2"},
		{"digitalocean_app", "application.app", "id", "506f78a4-e098-11e5-ad9f-000f53306ae1"},
		{"digitalocean_project", "organization.project", "id", "506f78a4-e098-11e5-ad9f-000f53306ae1"},
		{"aws_db_instance", "database.instance", "identifier", "production-db"},
		{"aws_rds_cluster_instance", "database.instance", "identifier", "production-member"},
		{"aws_rds_cluster", "database.cluster", "cluster_identifier", "production-cluster"},
		{"aws_ebs_volume", "storage.volume", "id", "vol-1234567890abcdef0"},
		{"aws_ebs_snapshot", "storage.snapshot", "id", "snap-1234567890abcdef0"},
		{"aws_security_group", "network.firewall", "id", "sg-1234567890abcdef0"},
		{"aws_key_pair", "access.ssh_key", "key_pair_id", "key-1234567890abcdef0"},
		{"aws_db_snapshot", "database.snapshot", "db_snapshot_identifier", "my-snapshot"},
		{"aws_db_cluster_snapshot", "database.cluster_snapshot", "db_cluster_snapshot_identifier", "my-cluster-snapshot"},
		{"digitalocean_volume", "storage.volume", "id", "506f78a4-e098-11e5-ad9f-000f53306ae1"},
		{"digitalocean_volume_snapshot", "storage.snapshot", "id", "506f78a4-e098-11e5-ad9f-000f53306ae1"},
		{"digitalocean_droplet_snapshot", "storage.snapshot", "id", "123456"},
		{"digitalocean_firewall", "network.firewall", "id", "506f78a4-e098-11e5-ad9f-000f53306ae1"},
		{"digitalocean_vpc", "network.network", "id", "506f78a4-e098-11e5-ad9f-000f53306ae1"},
		{"digitalocean_loadbalancer", "network.load_balancer", "id", "506f78a4-e098-11e5-ad9f-000f53306ae1"},
		{"digitalocean_ssh_key", "access.ssh_key", "id", "123456"},
		{"hcloud_network", "network.network", "id", "123456"},
		{"hcloud_firewall", "network.firewall", "id", "123456"},
		{"hcloud_volume", "storage.volume", "id", "123456"},
		{"hcloud_load_balancer", "network.load_balancer", "id", "123456"},
		{"hcloud_snapshot", "storage.snapshot", "id", "123456"},
		{"hcloud_ssh_key", "access.ssh_key", "id", "123456"},
	}
	resources := []any{}
	for _, tc := range cases {
		source := "hetznercloud/hcloud"
		if strings.HasPrefix(tc.typ, "aws_") {
			source = "hashicorp/aws"
		} else if strings.HasPrefix(tc.typ, "digitalocean_") {
			source = "digitalocean/digitalocean"
		}
		attrs := map[string]any{"id": "not-the-inventory-id", tc.field: tc.id, "secret": "never-return"}
		resources = append(resources, map[string]any{"mode": "managed", "type": tc.typ, "provider": `provider["registry.terraform.io/` + source + `"]`, "instances": []any{map[string]any{"attributes": attrs}, map[string]any{"attributes": attrs}}})
	}
	// Bad numeric values and missing identities must not erase the valid references.
	for _, id := range []any{nil, -1, 1.5, "bad", json.Number("1e2"), json.Number("100000000000000000000000")} {
		resources = append(resources, map[string]any{"mode": "managed", "type": "hcloud_volume", "provider": `provider["registry.terraform.io/hetznercloud/hcloud"]`, "instances": []any{map[string]any{"attributes": map[string]any{"id": id}}}})
	}
	resources = append(resources, map[string]any{"mode": "managed", "type": "hcloud_volume", "provider": `provider["registry.terraform.io/hetznercloud/hcloud"]`, "instances": []any{map[string]any{"attributes": map[string]any{"id": json.Number("998877")}}}})
	raw, err := json.Marshal(map[string]any{"version": 4, "serial": 1, "lineage": "00112233-4455-6677-8899-aabbccddeeff", "resources": resources})
	if err != nil {
		t.Fatal(err)
	}
	refs, unknown, err := ResourceIdentities(raw)
	if err != nil || unknown != 6 || len(refs) != len(cases)+1 {
		t.Fatal("mapping or partial coverage failed", len(refs), unknown, err)
	}
	for _, tc := range cases {
		found := false
		for _, ref := range refs {
			if ref.Kind == tc.kind && ref.NativeID == tc.id {
				found = true
			}
		}
		if !found {
			t.Fatal("missing mapping", tc.typ)
		}
	}
}

func TestAWSResourceRegionIdentity(t *testing.T) {
	resources := []any{}
	for _, region := range []any{"us-west-2", "us-east-1", "us-west-2", "us-gov-west-1", "cn-north-1", "", nil, 7, "us-east-1\n", "us-east-1a", "https://example.com"} {
		resources = append(resources, map[string]any{"mode": "managed", "type": "aws_instance", "provider": `provider["registry.terraform.io/hashicorp/aws"]`, "instances": []any{map[string]any{"attributes": map[string]any{"id": "i-1234567890abcdef0", "region": region}}}})
	}
	raw, err := json.Marshal(map[string]any{"version": 4, "serial": 1, "lineage": "00112233-4455-6677-8899-aabbccddeeff", "resources": resources})
	if err != nil {
		t.Fatal(err)
	}
	refs, unknown, err := ResourceIdentities(raw)
	if err != nil || unknown != 4 || len(refs) != 5 {
		t.Fatal("region coverage/deduplication", refs, unknown, err)
	}
	expected := []string{"", "cn-north-1", "us-east-1", "us-gov-west-1", "us-west-2"}
	for i, ref := range refs {
		if ref.Region != expected[i] {
			t.Fatal("region identity ordering", refs)
		}
	}
	// A provider-specific location field cannot change non-AWS identity scope.
	raw = []byte(strings.ReplaceAll(strings.ReplaceAll(string(raw), "aws_instance", "hcloud_server"), "hashicorp/aws", "hetznercloud/hcloud"))
	raw = []byte(strings.ReplaceAll(string(raw), "i-1234567890abcdef0", "123"))
	refs, unknown, err = ResourceIdentities(raw)
	if err != nil || unknown != 0 || len(refs) != 1 || refs[0].Region != "" {
		t.Fatal("non-AWS scope changed", refs, unknown, err)
	}
}

func TestLoadBalancerStateIdentity(t *testing.T) {
	const lb = "arn:aws:elasticloadbalancing:us-west-2:123456789012:loadbalancer/app/production/0123456789abcdef"
	for _, tc := range []struct {
		typ, id, region, wantRegion string
		valid                       bool
	}{
		{"aws_lb", lb, "", "us-west-2", true},
		{"aws_alb", lb, "us-west-2", "us-west-2", true},
		{"aws_lb", strings.Replace(lb, "/app/", "/net/", 1), "", "us-west-2", true},
		{"aws_lb", strings.Replace(lb, "/app/", "/gwy/", 1), "", "us-west-2", true},
		{"aws_lb", strings.ReplaceAll(strings.Replace(lb, "arn:aws:", "arn:aws-us-gov:", 1), "us-west-2", "us-gov-west-1"), "", "us-gov-west-1", true},
		{"aws_lb", lb, "us-east-1", "", false},
		{"aws_lb", strings.Replace(lb, "loadbalancer/app", "listener/app", 1), "", "", false},
		{"aws_lb", strings.Replace(lb, "123456789012", "invalid", 1), "", "", false},
		{"aws_lb", lb + "/listener", "", "", false},
		{"aws_lb", strings.Replace(lb, "us-west-2", "us-"+strings.Repeat("a", 65)+"-1", 1), "", "", false},
		{"aws_elb", "classic-production", "", "", true},
		{"aws_elb", "classic-production", "us-east-1", "us-east-1", true},
		{"aws_elb", lb, "", "", false},
		{"aws_elb", strings.Repeat("x", 33), "", "", false},
	} {
		raw, err := json.Marshal(map[string]any{"version": 4, "serial": 1, "lineage": "00112233-4455-6677-8899-aabbccddeeff", "resources": []any{map[string]any{"mode": "managed", "type": tc.typ, "provider": `provider["registry.terraform.io/hashicorp/aws"]`, "instances": []any{map[string]any{"attributes": map[string]any{"id": tc.id, "region": tc.region}}}}}})
		if err != nil {
			t.Fatal(err)
		}
		refs, unknown, err := ResourceIdentities(raw)
		if err != nil {
			t.Fatal(err)
		}
		if tc.valid {
			if len(refs) != 1 || unknown != 0 || refs[0].Region != tc.wantRegion || refs[0].NativeID != tc.id || refs[0].Kind != "network.load_balancer" {
				t.Fatal("missing load balancer identity", tc, refs, unknown)
			}
		} else if len(refs) != 0 || unknown != 1 {
			t.Fatal("invalid load balancer claimed", tc, refs, unknown)
		}
	}
}

func TestDigitalOceanClusterPoolIdentities(t *testing.T) {
	const cluster = "506f78a4-e098-11e5-ad9f-000f53306ae1"
	const pool = "506f78a4-e098-11e5-ad9f-000f53306ae2"
	for _, tc := range []struct {
		pools          string
		count, unknown int
	}{
		{`[{"id":"` + pool + `","nodes":[{"droplet_id":"123"}]}]`, 2, 0},
		{`[{"id":"` + pool + `"},{"id":"` + pool + `"}]`, 2, 0},
		{`[{"id":"bad"},{"id":"` + pool + `"}]`, 2, 1},
		{`null`, 1, 1}, {`[]`, 1, 1}, {`{}`, 1, 1}, {`[null]`, 1, 1},
	} {
		raw := []byte(`{"version":4,"serial":1,"lineage":"00112233-4455-6677-8899-aabbccddeeff","resources":[{"mode":"managed","type":"digitalocean_kubernetes_cluster","provider":"provider[\"registry.terraform.io/digitalocean/digitalocean\"]","instances":[{"attributes":{"id":"` + cluster + `","node_pool":` + tc.pools + `,"kube_config":[{"token":"private-token"}]}}]}]}`)
		refs, unknown, err := ResourceIdentities(raw)
		if err != nil || unknown != tc.unknown || len(refs) != tc.count {
			t.Fatal(tc, refs, unknown, err)
		}
		for _, ref := range refs {
			if (ref.Kind == "kubernetes.cluster" && ref.NativeID != cluster) || (ref.Kind == "kubernetes.node_group" && ref.NativeID != pool) || ref.Provider != "digitalocean" {
				t.Fatal("wrong identity", ref)
			}
		}
	}
}
