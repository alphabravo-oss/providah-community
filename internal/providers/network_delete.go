package providers

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/alphabravo-oss/providah-community/sdk/provider"
	"github.com/digitalocean/godo"
	"github.com/hetznercloud/hcloud-go/v2/hcloud"
	"golang.org/x/oauth2"
	"net/http"
	"strconv"
)

func deleteNetworkResource(ctx context.Context, r provider.Request, client *http.Client) *provider.PowerResult {
	p := r.Power
	if r.Provider == "aws" {
		return deleteSecurityGroup(ctx, r, client)
	}
	fail := func() *provider.PowerResult { out := PowerReadFailure(p.Phase); return &out }
	gone := func() *provider.PowerResult {
		if p.Phase == "observe" {
			return &provider.PowerResult{Outcome: "succeeded", Status: "deleted"}
		}
		return fail()
	}
	lines := []string{"Delete this unused network configuration. No attached resource is detached or deleted automatically. No backup or undo is provided."}
	status := "present"
	var remove func() error
	impactInvalid := false
	add := func(label string, value any) {
		b, e := json.Marshal(value)
		if e != nil {
			impactInvalid = true
		}
		if e == nil {
			lines = append(lines, label+": "+string(b))
		}
	}
	switch r.Provider {
	case "digitalocean":
		svc := godo.NewClient(&http.Client{Transport: &oauth2.Transport{Source: oauth2.StaticTokenSource(&oauth2.Token{AccessToken: r.Credential}), Base: client.Transport}, Timeout: client.Timeout})
		if p.ResourceKind == "network.network" {
			v, response, e := svc.VPCs.Get(ctx, p.NativeID)
			if response != nil && response.StatusCode == 404 {
				return gone()
			}
			if e != nil || v == nil || v.ID != p.NativeID || v.RegionSlug != r.Region || v.IPRange == "" {
				return fail()
			}
			if p.Phase == "observe" {
				return &provider.PowerResult{Outcome: "accepted", Status: status}
			}
			if v.Default {
				return fail()
			}
			members, response, e := svc.VPCs.ListMembers(ctx, p.NativeID, nil, &godo.ListOptions{Page: 1, PerPage: 1})
			if e != nil || len(members) != 0 || response == nil || response.Links != nil && response.Links.Pages != nil && response.Links.Pages.Next != "" {
				return fail()
			}
			peers, response, e := svc.VPCs.ListVPCPeeringsByVPCID(ctx, p.NativeID, &godo.ListOptions{Page: 1, PerPage: 1})
			if e != nil || len(peers) != 0 || response == nil || response.Links != nil && response.Links.Pages != nil && response.Links.Pages.Next != "" {
				return fail()
			}
			lines = append(lines, fmt.Sprintf("Delete DigitalOcean VPC %s (%s), CIDR %s in %s. Default VPCs, VPC members and peerings block deletion.", v.ID, v.Name, v.IPRange, v.RegionSlug))
			remove = func() error { _, e := svc.VPCs.Delete(ctx, p.NativeID); return e }
		} else {
			v, response, e := svc.Firewalls.Get(ctx, p.NativeID)
			if response != nil && response.StatusCode == 404 {
				return gone()
			}
			if e != nil || v == nil || v.ID != p.NativeID || r.Region != "global" {
				return fail()
			}
			status = v.Status
			if p.Phase == "observe" {
				return &provider.PowerResult{Outcome: "accepted", Status: status}
			}
			if len(v.DropletIDs) != 0 || len(v.Tags) != 0 || len(v.PendingChanges) != 0 || status != "succeeded" {
				return fail()
			}
			lines = append(lines, fmt.Sprintf("Delete DigitalOcean firewall %s (%s) and its rules. Droplet assignments, tag selectors and pending changes block deletion.", v.ID, v.Name))
			add("Inbound rules", v.InboundRules)
			add("Outbound rules", v.OutboundRules)
			remove = func() error { _, e := svc.Firewalls.Delete(ctx, p.NativeID); return e }
		}
	case "hetzner":
		svc := hcloud.NewClient(hcloud.WithToken(r.Credential), hcloud.WithHTTPClient(client), hcloud.WithRetryOpts(hcloud.RetryOpts{MaxRetries: 0}))
		id, e := strconv.ParseInt(p.NativeID, 10, 64)
		if e != nil || r.Region != "global" {
			return fail()
		}
		if p.ResourceKind == "network.network" {
			v, response, e := svc.Network.GetByID(ctx, id)
			if response != nil && response.StatusCode == 404 {
				return gone()
			}
			if e != nil || v == nil || v.ID != id || v.IPRange == nil {
				return fail()
			}
			if p.Phase == "observe" {
				return &provider.PowerResult{Outcome: "accepted", Status: status}
			}
			if len(v.Servers) != 0 || len(v.LoadBalancers) != 0 || v.Protection.Delete || v.ExposeRoutesToVSwitch {
				return fail()
			}
			for _, subnet := range v.Subnets {
				if subnet.VSwitchID != 0 {
					return fail()
				}
			}
			lines = append(lines, fmt.Sprintf("Delete Hetzner network %d (%s), CIDR %s, including its subnet and route configuration. Servers, load balancers, vSwitch connections and deletion protection block deletion.", v.ID, v.Name, v.IPRange))
			for _, subnet := range v.Subnets {
				add("Subnet", subnet)
			}
			for _, route := range v.Routes {
				add("Route", route)
			}
			remove = func() error { _, e := svc.Network.Delete(ctx, v); return e }
		} else {
			v, response, e := svc.Firewall.GetByID(ctx, id)
			if response != nil && response.StatusCode == 404 {
				return gone()
			}
			if e != nil || v == nil || v.ID != id {
				return fail()
			}
			if p.Phase == "observe" {
				return &provider.PowerResult{Outcome: "accepted", Status: status}
			}
			if len(v.AppliedTo) != 0 {
				return fail()
			}
			lines = append(lines, fmt.Sprintf("Delete Hetzner firewall %d (%s) and its rules. Server assignments and label selectors block deletion.", v.ID, v.Name))
			for _, rule := range v.Rules {
				add("Rule", rule)
			}
			remove = func() error { _, e := svc.Firewall.Delete(ctx, v); return e }
		}
	default:
		return fail()
	}
	if impactInvalid {
		return fail()
	}
	return StorageDeletionResult(p, status, lines, remove)
}
