package worker

import (
	"context"
	"encoding/json"
	"github.com/alphabravo-oss/providah-community/internal/providers"
	"github.com/alphabravo-oss/providah-community/sdk/provider"
	"io"
	"net/http"
	"os"
	"runtime/debug"
	"time"
)

func Run(version string, power func(context.Context, provider.Request, *http.Client) provider.Response, policy func(provider.Runtime) provider.Runtime) {
	if len(os.Args) == 2 && os.Args[1] == "--describe" {
		paths := map[string]string{"aws": "github.com/aws/aws-sdk-go-v2/service/ec2", "digitalocean": "github.com/digitalocean/godo", "hetzner": "github.com/hetznercloud/hcloud-go/v2"}
		info, ok := debug.ReadBuildInfo()
		if !ok {
			os.Exit(1)
		}
		out := []provider.Runtime{}
		for _, cloud := range []string{"aws", "digitalocean", "hetzner"} {
			for _, dep := range info.Deps {
				if dep.Path == paths[cloud] {
					out = append(out, policy(provider.Runtime{PlacementGroupDelete: cloud == "aws" || cloud == "hetzner", ProjectDelete: cloud == "digitalocean", ImageDelete: cloud == "aws", PrivateNetworkCreate: cloud != "aws", LoadBalancerDelete: true, DatabaseSnapshotDelete: cloud == "aws", SecurityGroupDelete: cloud == "aws", SSHKeyCreate: true, SSHKeyDelete: true, NetworkDelete: cloud != "aws", DatabasePower: cloud == "aws", VolumeDelete: true, SnapshotDelete: true, Metrics: true, CapabilitiesVersion: 1, InventoryKinds: provider.InventoryKinds(cloud), Actions: []string{"start", "shutdown", "restart", "delete", "create", "resize", "snapshot", "tags"}, Provider: cloud, Protocol: provider.Protocol, Version: version, SDKVersion: dep.Version}))
				}
			}
		}
		_ = json.NewEncoder(os.Stdout).Encode(out)
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	var req provider.Request
	decoder := json.NewDecoder(io.LimitReader(os.Stdin, 64<<10))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&req) != nil {
		_ = json.NewEncoder(os.Stdout).Encode(provider.Response{Version: provider.Protocol, Error: "invalid_configuration"})
		return
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	client := &http.Client{Transport: transport, Timeout: 30 * time.Second}
	if req.Metrics != nil {
		_ = json.NewEncoder(os.Stdout).Encode(providers.Metrics(ctx, req, client))
		return
	}
	if req.Power != nil {
		_ = json.NewEncoder(os.Stdout).Encode(power(ctx, req, client))
		return
	}
	_ = json.NewEncoder(os.Stdout).Encode(providers.Discover(ctx, req, client))
}
