package tfstate

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"regexp"
	"slices"
)

// ResourceIdentity is an allowlisted state reference, not proof of cloud account ownership.
// Callers must match the provider configuration and account/region scope before binding it.
type ResourceIdentity struct {
	Provider       string
	ProviderConfig string
	Kind           string
	NativeID       string
	Region         string
}

var providerConfig = regexp.MustCompile(`^(?:module\.[A-Za-z_][A-Za-z0-9_]*\.)*provider\["(registry\.(?:terraform\.io|opentofu\.org)/(?:hashicorp/aws|digitalocean/digitalocean|hetznercloud/hcloud))"\](?:\.[A-Za-z_][A-Za-z0-9_]*)?$`)
var stateDecimalID = regexp.MustCompile(`^[1-9][0-9]{0,17}$`)
var stateRegion = regexp.MustCompile(`^[a-z]{2}(?:-[a-z]+)+-[0-9]+$`)
var stateLBARN = regexp.MustCompile(`^arn:aws(?:-[a-z]+)*:elasticloadbalancing:([a-z]{2}(?:-[a-z]+)+-[0-9]+):[0-9]{12}:loadbalancer/(?:app|net|gwy)/[a-zA-Z0-9-]{1,32}/[a-f0-9]{16}$`)
var stateBucket = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{1,253}[A-Za-z0-9]$`)
var stateAMI = regexp.MustCompile(`^ami-(?:[a-f0-9]{8}|[a-f0-9]{17})$`)
var stateSnapshotName = regexp.MustCompile(`^[a-zA-Z][a-zA-Z0-9-]{0,254}$`)

// Explicit identity fields avoid confusing names/import identifiers with inventory IDs.
var stateResources = map[string]struct {
	cloud, kind, field string
	id                 *regexp.Regexp
}{
	"aws_s3_bucket":                         {"aws", "storage.bucket", "id", stateBucket},
	"aws_s3_bucket_policy":                  {"aws", "storage.bucket", "bucket", stateBucket},
	"aws_s3_bucket_versioning":              {"aws", "storage.bucket", "bucket", stateBucket},
	"aws_s3_bucket_lifecycle_configuration": {"aws", "storage.bucket", "bucket", stateBucket},
	"aws_placement_group":                   {"aws", "compute.placement_group", "placement_group_id", regexp.MustCompile(`^pg-(?:[a-f0-9]{8}|[a-f0-9]{17})$`)},
	"hcloud_placement_group":                {"hetzner", "compute.placement_group", "id", stateDecimalID},
	"digitalocean_database_cluster":         {"digitalocean", "database.cluster", "id", UUID},
	"aws_eks_cluster":                       {"aws", "kubernetes.cluster", "id", regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,99}$`)},
	"aws_eks_node_group":                    {"aws", "kubernetes.node_group", "id", regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,99}:[A-Za-z0-9][A-Za-z0-9_-]{0,62}$`)},
	"aws_lb":                                {"aws", "network.load_balancer", "id", stateLBARN},
	"aws_alb":                               {"aws", "network.load_balancer", "id", stateLBARN},
	"aws_elb":                               {"aws", "network.load_balancer", "id", regexp.MustCompile(`^[a-zA-Z0-9](?:[a-zA-Z0-9-]{0,30}[a-zA-Z0-9])?$`)},
	"aws_route_table":                       {"aws", "network.route_table", "id", regexp.MustCompile(`^rtb-(?:[a-f0-9]{8}|[a-f0-9]{17})$`)},
	"aws_internet_gateway":                  {"aws", "network.internet_gateway", "id", regexp.MustCompile(`^igw-(?:[a-f0-9]{8}|[a-f0-9]{17})$`)},
	"aws_nat_gateway":                       {"aws", "network.nat_gateway", "id", regexp.MustCompile(`^nat-(?:[a-f0-9]{8}|[a-f0-9]{17})$`)},
	"aws_instance":                          {"aws", "compute.server", "id", regexp.MustCompile(`^i-(?:[a-f0-9]{8}|[a-f0-9]{17})$`)},
	"aws_ami_copy":                          {"aws", "compute.image", "id", stateAMI},
	"aws_ami_from_instance":                 {"aws", "compute.image", "id", stateAMI},
	"aws_ami":                               {"aws", "compute.image", "id", stateAMI},
	"aws_db_instance":                       {"aws", "database.instance", "identifier", stateSnapshotName},
	"aws_rds_cluster_instance":              {"aws", "database.instance", "identifier", stateSnapshotName},
	"aws_rds_cluster":                       {"aws", "database.cluster", "cluster_identifier", stateSnapshotName},
	"aws_ebs_volume":                        {"aws", "storage.volume", "id", regexp.MustCompile(`^vol-(?:[a-f0-9]{8}|[a-f0-9]{17})$`)},
	"aws_ebs_snapshot":                      {"aws", "storage.snapshot", "id", regexp.MustCompile(`^snap-(?:[a-f0-9]{8}|[a-f0-9]{17})$`)},
	"aws_security_group":                    {"aws", "network.firewall", "id", regexp.MustCompile(`^sg-(?:[a-f0-9]{8}|[a-f0-9]{17})$`)},
	"aws_key_pair":                          {"aws", "access.ssh_key", "key_pair_id", regexp.MustCompile(`^key-(?:[a-f0-9]{8}|[a-f0-9]{17})$`)},
	"aws_db_snapshot":                       {"aws", "database.snapshot", "db_snapshot_identifier", stateSnapshotName},
	"aws_db_cluster_snapshot":               {"aws", "database.cluster_snapshot", "db_cluster_snapshot_identifier", stateSnapshotName},
	"digitalocean_kubernetes_cluster":       {"digitalocean", "kubernetes.cluster", "id", UUID},
	"digitalocean_kubernetes_node_pool":     {"digitalocean", "kubernetes.node_group", "id", UUID},
	"digitalocean_app":                      {"digitalocean", "application.app", "id", UUID},
	"digitalocean_project":                  {"digitalocean", "organization.project", "id", UUID},
	"digitalocean_droplet":                  {"digitalocean", "compute.server", "id", stateDecimalID},
	"digitalocean_volume":                   {"digitalocean", "storage.volume", "id", UUID},
	"digitalocean_volume_snapshot":          {"digitalocean", "storage.snapshot", "id", UUID},
	"digitalocean_droplet_snapshot":         {"digitalocean", "storage.snapshot", "id", stateDecimalID},
	"digitalocean_firewall":                 {"digitalocean", "network.firewall", "id", UUID},
	"digitalocean_vpc":                      {"digitalocean", "network.network", "id", UUID},
	"digitalocean_loadbalancer":             {"digitalocean", "network.load_balancer", "id", UUID},
	"digitalocean_ssh_key":                  {"digitalocean", "access.ssh_key", "id", stateDecimalID},
	"hcloud_server":                         {"hetzner", "compute.server", "id", stateDecimalID},
	"hcloud_network":                        {"hetzner", "network.network", "id", stateDecimalID},
	"hcloud_firewall":                       {"hetzner", "network.firewall", "id", stateDecimalID},
	"hcloud_volume":                         {"hetzner", "storage.volume", "id", stateDecimalID},
	"hcloud_load_balancer":                  {"hetzner", "network.load_balancer", "id", stateDecimalID},
	"hcloud_snapshot":                       {"hetzner", "storage.snapshot", "id", stateDecimalID},
	"hcloud_ssh_key":                        {"hetzner", "access.ssh_key", "id", stateDecimalID},
}

// ResourceIdentities extracts allowlisted managed resource identities. Unsupported types are
// counted so callers cannot mistake partial coverage for an ownership-free state.
// Duplicate JSON fields and excessive nesting are rejected rather than guessed.
func ResourceIdentities(raw []byte) ([]ResourceIdentity, int, error) {
	if _, err := Parse(raw); err != nil {
		return nil, 0, err
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	if err := uniqueJSON(d, 0); err != nil {
		return nil, 0, err
	}
	if _, err := d.Token(); err != io.EOF {
		return nil, 0, errors.New("invalid state JSON")
	}
	var state struct {
		Resources []struct {
			Mode      string `json:"mode"`
			Type      string `json:"type"`
			Provider  string `json:"provider"`
			Instances []struct {
				Attributes json.RawMessage `json:"attributes"`
			} `json:"instances"`
		} `json:"resources"`
	}
	if json.Unmarshal(raw, &state) != nil {
		return nil, 0, errors.New("invalid state resource identities")
	}
	refs := []ResourceIdentity{}
	unsupported := 0
	for _, r := range state.Resources {
		if r.Mode == "data" {
			continue
		}
		if r.Mode != "managed" {
			return nil, 0, errors.New("invalid state resource mode")
		}
		mapping, supported := stateResources[r.Type]
		cloud := mapping.cloud
		match := providerConfig.FindStringSubmatch(r.Provider)
		if !supported || len(match) != 2 {
			unsupported += len(r.Instances)
			continue
		}
		source := map[string]string{"aws": "hashicorp/aws", "digitalocean": "digitalocean/digitalocean", "hetzner": "hetznercloud/hcloud"}[cloud]
		if len(match[1]) < len(source) || match[1][len(match[1])-len(source):] != source {
			unsupported += len(r.Instances)
			continue
		}
		for _, instance := range r.Instances {
			var attributes map[string]json.RawMessage
			if json.Unmarshal(instance.Attributes, &attributes) != nil {
				unsupported++
				continue
			}
			var id string
			value := attributes[mapping.field]
			if json.Unmarshal(value, &id) != nil {
				// Numeric schemas are accepted only as exact positive decimal IDs, never floats.
				if mapping.id != stateDecimalID {
					unsupported++
					continue
				}
				id = string(value)
			}
			if !mapping.id.MatchString(id) {
				unsupported++
				continue
			}
			var region string
			if cloud == "aws" {
				if value, present := attributes["region"]; present {
					if json.Unmarshal(value, &region) != nil || (region != "" && (len(region) > 64 || !stateRegion.MatchString(region))) {
						unsupported++
						continue
					}
				}
			}
			if mapping.id == stateLBARN {
				arnRegion := stateLBARN.FindStringSubmatch(id)[1]
				if len(id) > 512 || len(arnRegion) > 64 || (region != "" && region != arnRegion) {
					unsupported++
					continue
				}
				region = arnRegion
			}
			refs = append(refs, ResourceIdentity{Region: region, Provider: cloud, ProviderConfig: r.Provider, Kind: mapping.kind, NativeID: id})
			if r.Type == "digitalocean_kubernetes_cluster" {
				var pools []json.RawMessage
				if json.Unmarshal(attributes["node_pool"], &pools) != nil || len(pools) == 0 {
					unsupported++
				} else {
					for _, rawPool := range pools {
						var pool struct {
							ID string `json:"id"`
						}
						if json.Unmarshal(rawPool, &pool) != nil || !UUID.MatchString(pool.ID) {
							unsupported++
							continue
						}
						refs = append(refs, ResourceIdentity{Provider: cloud, ProviderConfig: r.Provider, Kind: "kubernetes.node_group", NativeID: pool.ID})
						if len(refs) > 100000 {
							return nil, 0, errors.New("state resource identity limit exceeded")
						}
					}
				}
			}
			if len(refs) > 100000 {
				return nil, 0, errors.New("state resource identity limit exceeded")
			}
		}
	}
	slices.SortFunc(refs, func(a, b ResourceIdentity) int {
		if a.ProviderConfig < b.ProviderConfig {
			return -1
		}
		if a.ProviderConfig > b.ProviderConfig {
			return 1
		}
		if a.Kind < b.Kind {
			return -1
		}
		if a.Kind > b.Kind {
			return 1
		}
		if a.Region < b.Region {
			return -1
		}
		if a.Region > b.Region {
			return 1
		}
		if a.NativeID < b.NativeID {
			return -1
		}
		if a.NativeID > b.NativeID {
			return 1
		}
		return 0
	})
	return slices.Compact(refs), unsupported, nil
}

func uniqueJSON(d *json.Decoder, depth int) error {
	if depth > 64 {
		return errors.New("state JSON nesting limit exceeded")
	}
	token, err := d.Token()
	if err != nil {
		return errors.New("invalid state JSON")
	}
	delim, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delim {
	case '{':
		seen := map[string]bool{}
		for d.More() {
			key, err := d.Token()
			if err != nil {
				return errors.New("invalid state JSON")
			}
			s, ok := key.(string)
			if !ok || seen[s] {
				return errors.New("duplicate state JSON field")
			}
			seen[s] = true
			if err := uniqueJSON(d, depth+1); err != nil {
				return err
			}
		}
	case '[':
		for d.More() {
			if err := uniqueJSON(d, depth+1); err != nil {
				return err
			}
		}
	default:
		return errors.New("invalid state JSON")
	}
	_, err = d.Token()
	return err
}
