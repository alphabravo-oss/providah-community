package providers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/alphabravo-oss/providah-community/sdk/provider"
	"github.com/digitalocean/godo"
	"github.com/hetznercloud/hcloud-go/v2/hcloud"
	"golang.org/x/oauth2"
	"net/http"
	"strconv"
	"time"
)

func deleteLoadBalancer(ctx context.Context, r provider.Request, client *http.Client) *provider.PowerResult {
	p := r.Power
	fail := func() *provider.PowerResult { v := PowerReadFailure(p.Phase); return &v }
	gone := func() *provider.PowerResult {
		if p.Phase == "observe" {
			return &provider.PowerResult{Outcome: "succeeded", Status: "deleted"}
		}
		return fail()
	}
	lines := []string{"Delete this load balancer and its routing configuration. Traffic through its addresses stops; update DNS and clients before execution.", "Backend servers, target load balancers and certificates are not deleted. No backup or automatic undo is provided; external controllers may recreate this resource."}
	invalid := false
	add := func(label string, value any) {
		b, e := json.Marshal(value)
		if e != nil {
			invalid = true
			return
		}
		lines = append(lines, label+": "+string(b))
	}
	status := "present"
	var remove func() error
	switch r.Provider {
	case "digitalocean":
		svc := godo.NewClient(&http.Client{Transport: &oauth2.Transport{Source: oauth2.StaticTokenSource(&oauth2.Token{AccessToken: r.Credential}), Base: client.Transport}, Timeout: client.Timeout})
		v, response, e := svc.LoadBalancers.Get(ctx, p.NativeID)
		if response != nil && response.StatusCode == 404 {
			return gone()
		}
		if e != nil || v == nil || v.ID != p.NativeID {
			return fail()
		}
		region := ""
		if v.Region != nil {
			region = v.Region.Slug
		} else if v.Type == "GLOBAL" {
			region = "global"
		}
		if region != r.Region || v.Status == "" {
			return fail()
		}
		status = v.Status
		if p.Phase == "observe" {
			return &provider.PowerResult{Outcome: "accepted", Status: status}
		}
		if v.Created == "" {
			return fail()
		}
		add("Load balancer", map[string]any{"id": v.ID, "name": v.Name, "created": v.Created, "region": region, "type": v.Type, "ipv4": v.IP, "ipv6": v.IPv6, "network": v.Network, "vpc": v.VPCUUID, "subnet": v.VPCSubnetUUID, "algorithm": v.Algorithm})
		for _, id := range v.DropletIDs {
			add("Backend server", id)
		}
		add("Backend tag selector", v.Tag)
		for _, tag := range v.Tags {
			add("Backend tag selector", tag)
		}
		for _, id := range v.TargetLoadBalancerIDs {
			add("Backend load balancer", id)
		}
		for _, rule := range v.ForwardingRules {
			add("Forwarding rule", rule)
		}
		for _, domain := range v.Domains {
			if domain == nil {
				invalid = true
				continue
			}
			add("Domain", map[string]any{"name": domain.Name, "managed": domain.IsManaged, "certificate_id": domain.CertificateID})
		}
		add("Global routing", v.GLBSettings)
		remove = func() error {
			response, e := svc.LoadBalancers.Delete(ctx, p.NativeID)
			if e == nil && (response == nil || response.StatusCode != 204) {
				return errors.New("unconfirmed deletion")
			}
			return e
		}
	case "hetzner":
		svc := hcloud.NewClient(hcloud.WithToken(r.Credential), hcloud.WithHTTPClient(client), hcloud.WithRetryOpts(hcloud.RetryOpts{MaxRetries: 0}))
		id, e := strconv.ParseInt(p.NativeID, 10, 64)
		if e != nil {
			return fail()
		}
		v, response, e := svc.LoadBalancer.GetByID(ctx, id)
		if response != nil && response.StatusCode == 404 {
			return gone()
		}
		if e != nil || v == nil || v.ID != id || v.Location == nil || v.Location.Name != r.Region {
			return fail()
		}
		if p.Phase == "observe" {
			return &provider.PowerResult{Outcome: "accepted", Status: status}
		}
		if v.Protection.Delete || v.Created.IsZero() {
			return fail()
		}
		add("Load balancer", map[string]any{"id": v.ID, "name": v.Name, "region": r.Region, "created": v.Created.UTC().Format(time.RFC3339Nano), "public_enabled": v.PublicNet.Enabled, "ipv4": v.PublicNet.IPv4.IP.String(), "ipv6": v.PublicNet.IPv6.IP.String(), "algorithm": v.Algorithm.Type})
		for _, net := range v.PrivateNet {
			if net.Network == nil {
				invalid = true
				continue
			}
			add("Private network", fmt.Sprintf("%d / %s", net.Network.ID, net.IP))
		}
		for _, s := range v.Services {
			certificates := []int64{}
			for _, c := range s.HTTP.Certificates {
				if c == nil {
					invalid = true
					continue
				}
				certificates = append(certificates, c.ID)
			}
			add("Service", map[string]any{"protocol": s.Protocol, "listen_port": s.ListenPort, "destination_port": s.DestinationPort, "proxy_protocol": s.Proxyprotocol, "certificate_ids": certificates, "redirect_http": s.HTTP.RedirectHTTP})
		}
		count := 0
		var targets func([]hcloud.LoadBalancerTarget, int)
		targets = func(values []hcloud.LoadBalancerTarget, depth int) {
			if depth > 4 {
				invalid = true
				return
			}
			for _, target := range values {
				count++
				if count > 1000 {
					invalid = true
					return
				}
				value := ""
				switch target.Type {
				case hcloud.LoadBalancerTargetTypeServer:
					if target.Server == nil || target.Server.Server == nil {
						invalid = true
						continue
					}
					value = strconv.FormatInt(target.Server.Server.ID, 10)
				case hcloud.LoadBalancerTargetTypeLabelSelector:
					if target.LabelSelector == nil {
						invalid = true
						continue
					}
					value = target.LabelSelector.Selector
				case hcloud.LoadBalancerTargetTypeIP:
					if target.IP == nil {
						invalid = true
						continue
					}
					value = target.IP.IP
				default:
					invalid = true
					continue
				}
				if value == "" {
					invalid = true
				}
				add("Backend target", map[string]any{"type": target.Type, "value": value, "private_ip": target.UsePrivateIP})
				targets(target.Targets, depth+1)
			}
		}
		targets(v.Targets, 0)
		remove = func() error {
			response, e := svc.LoadBalancer.Delete(ctx, v)
			if e == nil && (response == nil || response.StatusCode != 204) {
				return errors.New("unconfirmed deletion")
			}
			return e
		}
	default:
		return fail()
	}
	if invalid {
		return fail()
	}
	return StorageDeletionResult(p, status, lines, remove)
}
