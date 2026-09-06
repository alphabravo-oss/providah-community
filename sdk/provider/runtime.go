package provider

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"regexp"
	"slices"
	"time"
)

type Runtime struct {
	SSHKeyCreate           bool     `json:"ssh_key_create,omitempty"`
	PlacementGroupDelete   bool     `json:"placement_group_delete,omitempty"`
	ProjectDelete          bool     `json:"project_delete,omitempty"`
	ImageDelete            bool     `json:"image_delete,omitempty"`
	PrivateNetworkCreate   bool     `json:"private_network_create,omitempty"`
	PublisherKeyID         string   `json:"publisher_key_id,omitempty"`
	ApprovalExpiresAt      string   `json:"approval_expires_at,omitempty"`
	LoadBalancerDelete     bool     `json:"load_balancer_delete,omitempty"`
	DatabaseSnapshotDelete bool     `json:"database_snapshot_delete,omitempty"`
	SecurityGroupDelete    bool     `json:"security_group_delete,omitempty"`
	SSHKeyDelete           bool     `json:"ssh_key_delete,omitempty"`
	NetworkDelete          bool     `json:"network_delete,omitempty"`
	DatabasePower          bool     `json:"database_power,omitempty"`
	VolumeDelete           bool     `json:"volume_delete,omitempty"`
	SnapshotDelete         bool     `json:"snapshot_delete,omitempty"`
	Metrics                bool     `json:"metrics,omitempty"`
	CapabilitiesVersion    int      `json:"capabilities_version,omitempty"`
	InventoryKinds         []string `json:"inventory_kinds,omitempty"`
	Actions                []string `json:"actions,omitempty"`
	Provider               string   `json:"provider"`
	Image                  string   `json:"image"`
	Version                string   `json:"version"`
	SDKVersion             string   `json:"sdk_version"`
	Protocol               int      `json:"protocol"`
}

var imageID = regexp.MustCompile(`^sha256:[a-f0-9]{64}$`)

func (r Runtime) DiscoveryKinds() []string {
	if r.CapabilitiesVersion == 0 {
		return []string{"compute.server"}
	}
	return slices.Clone(r.InventoryKinds)
}
func (r Runtime) OperationActions() []string {
	if r.CapabilitiesVersion == 0 {
		return []string{"start", "shutdown", "restart"}
	}
	return slices.Clone(r.Actions)
}
func (r Runtime) SupportsAction(action string) bool {
	return slices.Contains(r.OperationActions(), action)
}
func (r Runtime) SupportsResourceAction(kind, action string) bool {
	if kind == "" || kind == "compute.server" {
		return r.SupportsAction(action)
	}
	if kind == "compute.placement_group" {
		return r.PlacementGroupDelete && (r.Provider == "aws" || r.Provider == "hetzner") && r.CapabilitiesVersion == 1 && slices.Contains(r.InventoryKinds, kind) && action == "delete" && r.SupportsAction("delete")
	}
	if kind == "network.load_balancer" {
		return r.LoadBalancerDelete && (r.Provider == "aws" || r.Provider == "digitalocean" || r.Provider == "hetzner") && slices.Contains(r.InventoryKinds, kind) && action == "delete" && r.SupportsAction("delete")
	}
	if kind == "organization.project" {
		return r.ProjectDelete && r.Provider == "digitalocean" && r.CapabilitiesVersion == 1 && slices.Contains(r.InventoryKinds, kind) && action == "delete" && r.SupportsAction("delete")
	}
	if kind == "compute.image" {
		return r.ImageDelete && r.Provider == "aws" && r.CapabilitiesVersion == 1 && slices.Contains(r.InventoryKinds, kind) && action == "delete" && r.SupportsAction("delete")
	}
	if DatabaseSnapshotKind(kind) {
		return r.Provider == "aws" && r.DatabaseSnapshotDelete && slices.Contains(r.InventoryKinds, kind) && action == "delete" && r.SupportsAction("delete")
	}
	if DatabaseKind(kind) {
		return r.Provider == "aws" && r.DatabasePower && slices.Contains(r.InventoryKinds, kind) && (action == "start" || action == "shutdown") && r.SupportsAction(action)
	}
	if kind == "access.ssh_key" && action == "create" {
		return r.SSHKeyCreate && r.CapabilitiesVersion == 1 && slices.Contains(r.InventoryKinds, kind) && r.SupportsAction("create")
	}
	if kind == "access.ssh_key" {
		return r.SSHKeyDelete && slices.Contains(r.InventoryKinds, kind) && action == "delete" && r.SupportsAction("delete")
	}
	if r.Provider == "aws" && kind == "network.firewall" {
		return r.SecurityGroupDelete && slices.Contains(r.InventoryKinds, kind) && action == "delete" && r.SupportsAction("delete")
	}
	if NetworkDeleteKind(kind) {
		return r.NetworkDelete && r.Provider != "aws" && slices.Contains(r.InventoryKinds, kind) && action == "delete" && r.SupportsAction("delete")
	}
	if kind == "storage.volume" {
		return action == "delete" && r.VolumeDelete && r.SupportsAction("delete")
	}
	return kind == "storage.snapshot" && action == "delete" && r.SnapshotDelete && r.SupportsAction("delete")
}
func (r Runtime) Validate() error {
	if r.SSHKeyCreate && (r.CapabilitiesVersion != 1 || !r.SupportsAction("create") || !slices.Contains(r.InventoryKinds, "access.ssh_key")) {
		return errors.New("invalid public key creation capability")
	}
	if r.PlacementGroupDelete && (r.CapabilitiesVersion != 1 || (r.Provider != "aws" && r.Provider != "hetzner") || !r.SupportsAction("delete") || !slices.Contains(r.InventoryKinds, "compute.placement_group")) {
		return errors.New("invalid placement deletion capability")
	}
	if r.ProjectDelete && (r.Provider != "digitalocean" || r.CapabilitiesVersion != 1) {
		return errors.New("invalid project deletion capability")
	}
	if r.ImageDelete && (r.Provider != "aws" || r.CapabilitiesVersion != 1) {
		return errors.New("invalid image deletion capability")
	}
	if r.PrivateNetworkCreate && (r.CapabilitiesVersion != 1 || (r.Provider != "digitalocean" && r.Provider != "hetzner") || !r.SupportsAction("create")) {
		return errors.New("invalid private network creation capability")
	}
	if (r.PublisherKeyID == "") != (r.ApprovalExpiresAt == "") || len(r.PublisherKeyID) > 80 {
		return errors.New("invalid runtime approval metadata")
	}
	if r.ApprovalExpiresAt != "" {
		if _, e := time.Parse(time.RFC3339Nano, r.ApprovalExpiresAt); e != nil {
			return errors.New("invalid runtime approval expiry")
		}
	}

	if r.LoadBalancerDelete && (r.CapabilitiesVersion != 1 || (r.Provider != "aws" && r.Provider != "digitalocean" && r.Provider != "hetzner")) {
		return errors.New("invalid load balancer deletion capability")
	}
	if r.DatabaseSnapshotDelete && (r.Provider != "aws" || r.CapabilitiesVersion != 1) {
		return errors.New("invalid database snapshot deletion capability")
	}
	if r.SecurityGroupDelete && (r.Provider != "aws" || r.CapabilitiesVersion != 1) {
		return errors.New("invalid security group deletion capability")
	}
	if r.SSHKeyDelete && r.CapabilitiesVersion != 1 {
		return errors.New("invalid SSH key deletion capability")
	}
	if r.NetworkDelete && (r.CapabilitiesVersion != 1 || (r.Provider != "digitalocean" && r.Provider != "hetzner")) {
		return errors.New("invalid network delete capability")
	}
	if r.DatabasePower && (r.Provider != "aws" || r.CapabilitiesVersion != 1) {
		return errors.New("invalid database power capability")
	}
	if r.CapabilitiesVersion < 0 || r.CapabilitiesVersion > 1 || !validKinds(r.InventoryKinds) || (r.CapabilitiesVersion == 0 && (r.VolumeDelete || r.SnapshotDelete || r.Metrics || len(r.InventoryKinds) > 0 || len(r.Actions) > 0)) {
		return errors.New("invalid runtime capabilities")
	}
	seen := map[string]bool{}
	for _, action := range r.Actions {
		if !slices.Contains([]string{"start", "shutdown", "restart", "delete", "create", "resize", "snapshot", "tags"}, action) || seen[action] {
			return errors.New("invalid runtime action")
		}
		seen[action] = true
	}
	for _, kind := range r.InventoryKinds {
		if !slices.Contains(InventoryKinds(r.Provider), kind) {
			return errors.New("unsupported runtime inventory kind")
		}
	}

	if !slices.Contains([]string{"aws", "digitalocean", "hetzner"}, r.Provider) || !imageID.MatchString(r.Image) || r.Protocol != Protocol || len(r.Version) < 1 || len(r.Version) > 80 || len(r.SDKVersion) < 1 || len(r.SDKVersion) > 80 {
		return errors.New("incompatible provider runtime")
	}
	return nil
}
func ValidateRuntimes(rs []Runtime) error {
	if len(rs) == 0 || len(rs) > 32 {
		return errors.New("runtime catalog must contain 1–32 entries")
	}
	seen := map[string]bool{}
	for _, r := range rs {
		if err := r.Validate(); err != nil {
			return err
		}
		key := r.Provider + ":" + r.Image
		if seen[key] {
			return errors.New("duplicate provider runtime")
		}
		seen[key] = true
	}
	return nil
}
func launcherHTTP(socket string, timeout time.Duration) *http.Client {
	return &http.Client{Timeout: timeout, Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", socket)
	}}}
}
func LoadRuntimes(ctx context.Context, socket string) ([]Runtime, error) {
	client := launcherHTTP(socket, 5*time.Second)
	defer client.CloseIdleConnections()
	req, err := http.NewRequestWithContext(ctx, "GET", "http://launcher/runtimes", nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, errors.New("provider runtime catalog unavailable")
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != 200 {
		return nil, errors.New("provider runtime catalog rejected")
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 65537))
	if err != nil || len(raw) > 65536 {
		return nil, errors.New("invalid runtime catalog size")
	}
	var out []Runtime
	if json.Unmarshal(raw, &out) != nil {
		return nil, errors.New("invalid runtime catalog")
	}
	return out, ValidateRuntimes(out)
}

func (r Runtime) Accepts(request Request) bool {
	if request.Metrics != nil {
		return r.Metrics
	}
	if request.Power != nil {
		return r.SupportsResourceAction(request.Power.ResourceKind, request.Power.Action)
	}
	kinds := request.InventoryKinds
	if r.CapabilitiesVersion == 0 && len(kinds) > 0 {
		return false
	}
	if len(kinds) == 0 {
		kinds = []string{"compute.server"}
	}
	for _, kind := range kinds {
		if !slices.Contains(r.DiscoveryKinds(), kind) {
			return false
		}
	}
	return true
}
