// Package provider defines the bounded discovery contract between the core and provider workers.
package provider

import (
	"bytes"
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

const Protocol = 1
const MaxResources = 100000
const MaxResponseBytes = 32 << 20

type Request struct {
	Metrics        *MetricsRequest `json:"metrics,omitempty"`
	InventoryKinds []string        `json:"inventory_kinds,omitempty"`
	Version        int             `json:"version"`
	RuntimeID      string          `json:"runtime_id,omitempty"`
	OrganizationID string          `json:"organization_id"`
	ConnectionID   string          `json:"connection_id"`
	Provider       string          `json:"provider"`
	Region         string          `json:"region"`
	Credential     string          `json:"credential"`
	Power          *PowerRequest   `json:"power,omitempty"`
}
type Resource struct {
	Catalog          bool   `json:"catalog,omitempty"`
	Tags             *Tags  `json:"tags,omitempty"`
	ProviderIdentity string `json:"provider_identity,omitempty"`
	Kind             string `json:"kind,omitempty"`
	NativeID         string `json:"native_id"`
	Name             string `json:"name"`
	Region           string `json:"region"`
	Status           string `json:"status"`
	PublicIP         string `json:"public_ip"`
	PrivateIP        string `json:"private_ip"`
	Size             string `json:"size"`
}
type Response struct {
	Metrics        *MetricsResult `json:"metrics,omitempty"`
	InventoryKinds []string       `json:"inventory_kinds,omitempty"`
	Version        int            `json:"version"`
	Complete       bool           `json:"complete"`
	Resources      []Resource     `json:"resources"`
	Error          string         `json:"error,omitempty"`
	Power          *PowerResult   `json:"power,omitempty"`
}

var identifier = regexp.MustCompile(`^[a-f0-9]{64}$`)
var region = regexp.MustCompile(`^[a-z0-9-]{1,64}$`)

func InventoryKinds(cloud string) []string {
	kinds := []string{"compute.server", "network.network", "network.firewall", "storage.volume", "storage.snapshot", "compute.image", "access.ssh_key", "compute.type", "network.load_balancer"}
	if cloud != "aws" {
		kinds = append(kinds, "storage.backup")
	}
	if cloud == "aws" || cloud == "" {
		kinds = append(kinds, "storage.bucket", "network.route_table", "network.internet_gateway", "network.nat_gateway", "network.subnet", "network.ip", "database.instance", "database.snapshot", "database.cluster_snapshot")
	}
	if cloud == "digitalocean" {
		kinds = append(kinds, "network.ip")
	}
	if cloud == "digitalocean" || cloud == "" {
		kinds = append(kinds, "network.reserved_ipv6")
	}
	if cloud == "hetzner" || cloud == "" {
		kinds = append(kinds, "network.primary_ip", "network.floating_ip")
	}
	if cloud == "aws" || cloud == "hetzner" || cloud == "" {
		kinds = append(kinds, "compute.placement_group")
	}
	if cloud == "aws" || cloud == "digitalocean" || cloud == "" {
		kinds = append(kinds, "kubernetes.cluster", "kubernetes.node_group")
	}
	if cloud == "digitalocean" || cloud == "" {
		kinds = append(kinds, "organization.project", "application.app")
	}
	if cloud == "digitalocean" || cloud == "hetzner" || cloud == "" {
		kinds = append(kinds, "network.certificate")
	}
	if cloud == "aws" || cloud == "digitalocean" || cloud == "" {
		kinds = append(kinds, "database.cluster")
	}
	kinds = append(kinds, "dns.zone", "dns.record")
	return kinds
}
func (r Resource) ResourceKind() string {
	if r.Kind == "" {
		return "compute.server"
	}
	return r.Kind
}
func (r Response) CoveredKinds() []string {
	if len(r.InventoryKinds) == 0 {
		return []string{"compute.server"}
	}
	return r.InventoryKinds
}
func validKinds(kinds []string) bool {
	seen := map[string]bool{}
	for _, kind := range kinds {
		if !slices.Contains(InventoryKinds(""), kind) || seen[kind] {
			return false
		}
		seen[kind] = true
	}
	return true
}
func (r Request) Validate() error {
	if r.Metrics != nil {
		if r.Power != nil || len(r.InventoryKinds) > 0 {
			return errors.New("mixed metrics request")
		}
		if err := r.Metrics.Validate(r.Provider); err != nil {
			return err
		}
	}
	if !validKinds(r.InventoryKinds) || (r.Power != nil && len(r.InventoryKinds) != 0) {
		return errors.New("invalid inventory scope")
	}
	for _, kind := range r.InventoryKinds {
		if !slices.Contains(InventoryKinds(r.Provider), kind) {
			return errors.New("unsupported inventory scope")
		}
	}

	if r.RuntimeID != "" && !imageID.MatchString(r.RuntimeID) {
		return errors.New("invalid runtime identity")
	}
	if r.Power != nil {
		if r.Power.Action == "create" && r.Region == "" {
			return errors.New("creation requires an explicit region")
		}
		if err := r.Power.Validate(r.Provider); err != nil {
			return err
		}
	}
	if r.Version != Protocol || !identifier.MatchString(r.OrganizationID) || !identifier.MatchString(r.ConnectionID) {
		return errors.New("invalid request identity or protocol")
	}
	if r.Provider != "aws" && r.Provider != "hetzner" && r.Provider != "digitalocean" {
		return errors.New("unsupported provider")
	}
	if len(r.Credential) < 8 || len(r.Credential) > 16384 {
		return errors.New("invalid credential size")
	}
	if (r.Provider == "aws" && r.Region == "") || (r.Region != "" && !region.MatchString(r.Region)) {
		return errors.New("invalid region")
	}
	return nil
}
func (r Response) Validate() error {
	if r.Metrics != nil {
		if r.Version != Protocol || r.Power != nil || r.Complete || len(r.Resources) > 0 || len(r.InventoryKinds) > 0 || r.Error != "" {
			return errors.New("mixed metrics response")
		}
		return r.Metrics.Validate()
	}
	if !validKinds(r.InventoryKinds) || (r.Power != nil && len(r.InventoryKinds) != 0) {
		return errors.New("invalid inventory scope")
	}
	if r.Power != nil {
		if r.Version != Protocol || r.Complete || len(r.Resources) != 0 || r.Error != "" {
			return errors.New("invalid action response")
		}
		return r.Power.Validate()
	}
	if r.Version != Protocol || !r.Complete || r.Error != "" || len(r.Resources) > MaxResources {
		return errors.New("incomplete discovery")
	}
	seen := make(map[string]bool, len(r.Resources))
	for _, v := range r.Resources {
		if e := v.Tags.Validate(); e != nil {
			return e
		}
		key := v.ResourceKind() + "\n" + v.Region + "\n" + v.NativeID
		if !slices.Contains(r.CoveredKinds(), v.ResourceKind()) {
			return errors.New("resource outside covered scope")
		}
		if len(v.ProviderIdentity) > 128 || v.NativeID == "" || len(v.NativeID) > 512 || len(v.Name) > 512 || len(v.Status) > 64 || len(v.Size) > 120 || !region.MatchString(v.Region) || seen[key] {
			return errors.New("invalid resource")
		}
		seen[key] = true
		for _, ip := range []string{v.PublicIP, v.PrivateIP} {
			if ip != "" && net.ParseIP(ip) == nil {
				return errors.New("invalid resource address")
			}
		}
	}
	return nil
}

// Client connects only to the configured local launcher socket. Request data never controls the destination.
func Client(socket string) func(context.Context, Request) (Response, error) {
	client := launcherHTTP(socket, 150*time.Second)
	return func(ctx context.Context, r Request) (Response, error) {
		var out Response
		if err := r.Validate(); err != nil {
			return out, err
		}
		body, err := json.Marshal(r)
		if err != nil {
			return out, err
		}
		path := "/discover"
		if r.Metrics != nil {
			path = "/metrics"
		}
		if r.Power != nil {
			path = "/execute"
		}
		req, err := http.NewRequestWithContext(ctx, "POST", "http://launcher"+path, bytes.NewReader(body))
		if err != nil {
			return out, err
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := client.Do(req)
		if err != nil {
			return out, errors.New("provider launcher unavailable")
		}
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode != 200 {
			return out, errors.New("provider launcher rejected request")
		}
		b, err := io.ReadAll(io.LimitReader(resp.Body, MaxResponseBytes+1))
		if err != nil || len(b) > MaxResponseBytes {
			return out, errors.New("invalid worker response")
		}
		if json.Unmarshal(b, &out) != nil {
			return out, errors.New("invalid worker response")
		}
		return out, nil
	}
}
