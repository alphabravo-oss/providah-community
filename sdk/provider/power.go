package provider

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"regexp"
	"slices"
	"strings"
)

type PowerRequest struct {
	KeyCreate        *SSHKeyCreate `json:"key_create,omitempty"`
	ExpectedTags     *Tags         `json:"expected_tags,omitempty"`
	TargetTags       *Tags         `json:"target_tags,omitempty"`
	ExpectedIdentity string        `json:"expected_identity,omitempty"`
	ExpectedSize     string        `json:"expected_size,omitempty"`
	TargetSize       string        `json:"target_size,omitempty"`
	ResourceKind     string        `json:"resource_kind,omitempty"`
	Create           *ServerCreate `json:"create,omitempty"`
	DeletionImpact   string        `json:"deletion_impact,omitempty"`
	OperationID      string        `json:"operation_id"`
	Phase            string        `json:"phase"`
	Action           string        `json:"action"`
	NativeID         string        `json:"native_id"`
	ExpectedStatus   string        `json:"expected_status"`
	ActionID         string        `json:"action_id,omitempty"`
}
type PowerResult struct {
	Tags           *Tags  `json:"tags,omitempty"`
	Size           string `json:"size,omitempty"`
	NativeID       string `json:"native_id,omitempty"`
	DeletionImpact string `json:"deletion_impact,omitempty"`
	Outcome        string `json:"outcome"`
	ActionID       string `json:"action_id,omitempty"`
	Status         string `json:"status,omitempty"`
	Error          string `json:"error,omitempty"`
}

func ImpactDigest(impact string) string {
	h := sha256.Sum256([]byte(impact))
	return hex.EncodeToString(h[:])
}
func NetworkDeleteKind(kind string) bool {
	return kind == "network.network" || kind == "network.firewall"
}
func DatabaseSnapshotKind(kind string) bool {
	return kind == "database.snapshot" || kind == "database.cluster_snapshot"
}
func DatabaseKind(kind string) bool { return kind == "database.instance" || kind == "database.cluster" }
func ResourceActionReached(kind, action, status string) bool {
	if DatabaseKind(kind) {
		return (action == "start" && status == "available") || (action == "shutdown" && status == "stopped")
	}
	return PowerReached(action, status)
}
func ResourceActionAllowed(kind, action, status string) bool {
	if kind == "compute.placement_group" {
		return action == "delete" && (status == "present" || status == "available")
	}
	if kind == "organization.project" {
		return action == "delete" && status == "present"
	}
	if kind == "network.load_balancer" {
		return action == "delete" && slices.Contains([]string{"active", "errored", "present"}, status)
	}
	if kind == "compute.image" {
		return action == "delete" && status == "available"
	}
	if DatabaseSnapshotKind(kind) {
		return action == "delete" && status == "available"
	}
	if DatabaseKind(kind) {
		return (action == "start" && status == "stopped") || (action == "shutdown" && status == "available")
	}
	if kind == "" || kind == "compute.server" {
		return PowerAllowed(action, status)
	}
	if kind == "access.ssh_key" {
		return action == "delete" && status == "present"
	}
	if NetworkDeleteKind(kind) {
		return action == "delete" && (status == "present" || kind == "network.firewall" && status == "succeeded")
	}
	if kind == "storage.volume" {
		return action == "delete" && status == "available"
	}
	return kind == "storage.snapshot" && action == "delete" && slices.Contains([]string{"available", "completed", "present"}, status)
}
func (p PowerRequest) Validate(cloud string) error {
	if p.Action == "tags" {
		if p.ExpectedTags == nil || p.TargetTags == nil || ValidateTagChange(cloud, p.ExpectedTags, p.TargetTags) != nil {
			return errors.New("invalid tag change")
		}
	} else if p.ExpectedTags != nil || p.TargetTags != nil {
		return errors.New("unexpected tag change")
	}

	if p.Action == "resize" {
		if !ValidResize(p.ExpectedSize, p.TargetSize) {
			return errors.New("invalid resize types")
		}
	} else if p.ExpectedSize != "" || p.TargetSize != "" {
		return errors.New("unexpected resize input")
	}

	if DatabaseKind(p.ResourceKind) {
		if cloud != "aws" || (p.Action != "start" && p.Action != "shutdown") || !regexp.MustCompile(`^[a-zA-Z0-9-]{1,128}$`).MatchString(p.ExpectedIdentity) {
			return errors.New("invalid database power request")
		}
	} else if p.ExpectedIdentity != "" {
		return errors.New("unexpected database identity")
	}
	if p.ResourceKind != "" && (p.ResourceKind != "access.ssh_key" || p.Action != "create") && !DatabaseKind(p.ResourceKind) && (!slices.Contains([]string{"compute.placement_group", "organization.project", "compute.image", "network.load_balancer", "database.snapshot", "database.cluster_snapshot", "storage.snapshot", "storage.volume", "network.network", "network.firewall", "access.ssh_key"}, p.ResourceKind) || p.Action != "delete") {
		return errors.New("unsupported resource action")
	}

	if p.ResourceKind == "compute.placement_group" && cloud != "aws" && cloud != "hetzner" {
		return errors.New("unsupported placement group provider")
	}
	if p.ResourceKind == "organization.project" && cloud != "digitalocean" {
		return errors.New("unsupported project provider")
	}
	if p.ResourceKind == "network.load_balancer" && cloud != "aws" && cloud != "digitalocean" && cloud != "hetzner" {
		return errors.New("unsupported load balancer provider")
	}
	if (p.ResourceKind == "compute.image" || DatabaseSnapshotKind(p.ResourceKind)) && cloud != "aws" {
		return errors.New("unsupported database snapshot provider")
	}
	if NetworkDeleteKind(p.ResourceKind) && cloud != "digitalocean" && cloud != "hetzner" && (cloud != "aws" || p.ResourceKind != "network.firewall") {
		return errors.New("unsupported network provider")
	}
	if p.KeyCreate != nil && (p.Action != "create" || p.ResourceKind != "access.ssh_key") {
		return errors.New("unexpected key creation input")
	}
	if p.Action == "create" && p.ResourceKind == "access.ssh_key" {
		if p.KeyCreate == nil || p.KeyCreate.Validate() != nil || p.Create != nil || p.Phase == "preview" || p.DeletionImpact != "" || (p.Phase == "submit" && p.NativeID != "") {
			return errors.New("invalid public key creation")
		}
	} else if p.Action == "create" {
		if p.Create == nil || p.Create.Validate(cloud) != nil || p.DeletionImpact != "" || p.Phase == "preview" || p.Phase == "submit" && p.NativeID != "" {
			return errors.New("invalid creation input")
		}
	} else if p.Create != nil {
		return errors.New("unexpected creation input")
	}

	if len(p.DeletionImpact) > 16384 || (p.Phase == "preview" && p.Action != "delete") || (p.Action == "delete" && p.Phase == "submit" && p.DeletionImpact == "") || (p.Action != "delete" && p.DeletionImpact != "") {
		return errors.New("invalid deletion review")
	}

	if !identifier.MatchString(p.OperationID) || !slices.Contains([]string{"submit", "observe", "preview"}, p.Phase) || !slices.Contains([]string{"start", "shutdown", "restart", "delete", "create", "resize", "snapshot", "tags"}, p.Action) {
		return errors.New("invalid power action")
	}
	pattern := `^[1-9][0-9]{0,17}$`
	if cloud == "aws" {
		pattern = `^i-[a-f0-9]{8,17}$`
	}
	if DatabaseKind(p.ResourceKind) {
		pattern = `^[a-zA-Z][a-zA-Z0-9-]{0,62}$`
		if strings.Contains(p.NativeID, "--") || strings.HasSuffix(p.NativeID, "-") {
			return errors.New("invalid database identifier")
		}
	}
	if DatabaseSnapshotKind(p.ResourceKind) {
		pattern = `^[a-zA-Z][a-zA-Z0-9-]{0,254}$`
		if strings.Contains(p.NativeID, "--") || strings.HasSuffix(p.NativeID, "-") {
			return errors.New("invalid snapshot identifier")
		}
	}
	if p.ResourceKind == "organization.project" {
		pattern = `^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$`
	}
	if p.ResourceKind == "network.load_balancer" && cloud == "aws" {
		pattern = `^(?:[a-zA-Z0-9](?:[a-zA-Z0-9-]{0,30}[a-zA-Z0-9])?|arn:aws(?:-[a-z]+)*:elasticloadbalancing:[a-z]{2}(?:-[a-z]+)+-[0-9]+:[0-9]{12}:loadbalancer/(?:app|net|gwy)/[a-zA-Z0-9-]{1,32}/[a-f0-9]{16})$`
	}
	if p.ResourceKind == "compute.placement_group" && cloud == "aws" {
		pattern = `^pg-[a-f0-9]{17}$`
	}
	if p.ResourceKind == "compute.image" {
		pattern = `^ami-([a-f0-9]{8}|[a-f0-9]{17})$`
	}
	if p.ResourceKind == "network.firewall" && cloud == "aws" {
		pattern = `^sg-[a-f0-9]{8,17}$`
	}
	if p.ResourceKind == "access.ssh_key" && cloud == "aws" {
		pattern = `^key-[a-f0-9]{8,17}$`
	}
	if p.ResourceKind == "storage.snapshot" {
		switch cloud {
		case "aws":
			pattern = `^snap-[a-f0-9]{8,17}$`
		case "digitalocean":
			pattern = `^([1-9][0-9]{0,17}|[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12})$`
		}
	}
	if (NetworkDeleteKind(p.ResourceKind) || p.ResourceKind == "network.load_balancer") && cloud == "digitalocean" {
		pattern = `^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$`
	}
	if p.ResourceKind == "storage.volume" {
		switch cloud {
		case "aws":
			pattern = `^vol-[a-f0-9]{8,17}$`
		case "digitalocean":
			pattern = `^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$`
		}
	}
	if (p.Action != "create" || p.NativeID != "") && !regexp.MustCompile(pattern).MatchString(p.NativeID) || len(p.ExpectedStatus) > 64 || len(p.ActionID) > 128 {
		return errors.New("invalid power target")
	}
	if p.ActionID != "" && !regexp.MustCompile(`^[a-zA-Z0-9-]+$`).MatchString(p.ActionID) {
		return errors.New("invalid action identity")
	}
	return nil
}
func (p PowerResult) Validate() error {
	if p.Tags != nil && p.Tags.Validate() != nil {
		return errors.New("invalid observed tags")
	}

	if p.NativeID != "" && !regexp.MustCompile(`^((?:i|key)-[a-f0-9]{8,17}|[1-9][0-9]{0,17})$`).MatchString(p.NativeID) {
		return errors.New("invalid created identity")
	}

	if len(p.Size) > 64 {
		return errors.New("invalid observed size")
	}
	if len(p.DeletionImpact) > 16384 || (p.Outcome == "preview" && (p.DeletionImpact == "" || p.Error != "")) || (p.Outcome != "preview" && p.DeletionImpact != "") {
		return errors.New("invalid deletion preview")
	}

	if !slices.Contains([]string{"accepted", "succeeded", "failed", "uncertain", "preview"}, p.Outcome) || len(p.ActionID) > 128 || len(p.Status) > 64 {
		return errors.New("invalid power result")
	}
	if p.ActionID != "" && !regexp.MustCompile(`^[a-zA-Z0-9-]+$`).MatchString(p.ActionID) {
		return errors.New("invalid action identity")
	}
	if !slices.Contains([]string{"", "invalid_configuration", "preflight_failed", "state_changed", "submission_uncertain", "observation_failed", "provider_failed", "restart_unverifiable"}, p.Error) {
		return errors.New("invalid action error")
	}
	return nil
}
func PowerAllowed(action, status string) bool {
	switch action {
	case "delete", "tags":
		return status == "running" || status == "active" || status == "off" || status == "stopped"
	case "start", "resize", "snapshot":
		return status == "off" || status == "stopped"
	case "shutdown", "restart":
		return status == "running" || status == "active"
	}
	return false
}
func PowerReached(action, status string) bool {
	switch action {
	case "delete":
		return status == "terminated" || status == "deleted"
	case "start", "restart":
		return status == "running" || status == "active"
	case "shutdown":
		return status == "off" || status == "stopped"
	}
	return false
}

var serverSize = regexp.MustCompile(`^[a-z][a-z0-9.-]{0,63}$`)

func ValidResize(from, to string) bool {
	return serverSize.MatchString(from) && serverSize.MatchString(to) && from != to
}
