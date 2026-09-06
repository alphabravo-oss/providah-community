package providers

import (
	"context"
	"errors"
	"github.com/alphabravo-oss/providah-community/sdk/provider"
	"github.com/hetznercloud/hcloud-go/v2/hcloud"
	"io"
	"net"
	"net/http"
	"strings"
	"testing"
)

func TestDeletionSDKs(t *testing.T) {
	for _, tc := range []struct{ cloud, id, region, state, response string }{
		{"aws", "i-12345678", "us-east-1", `<DescribeInstancesResponse><reservationSet><item><instancesSet><item><instanceId>i-12345678</instanceId><instanceState><name>running</name></instanceState><blockDeviceMapping><item><deviceName>/dev/sda1</deviceName><ebs><volumeId>vol-123</volumeId><deleteOnTermination>true</deleteOnTermination></ebs></item></blockDeviceMapping></item></instancesSet></item></reservationSet></DescribeInstancesResponse>`, `<TerminateInstancesResponse><instancesSet/></TerminateInstancesResponse>`},
		{"digitalocean", "123", "nyc3", `{"droplet":{"id":123,"name":"delete-me","status":"active","region":{"slug":"nyc3"},"volume_ids":["volume-123"],"backup_ids":[42]}}`, ``},
		{"hetzner", "123", "fsn1", `{"server":{"id":123,"name":"delete-me","status":"running","location":{"name":"fsn1"},"volumes":[42]}}`, `{"action":{"id":42,"status":"running"}}`},
	} {
		t.Run(tc.cloud, func(t *testing.T) {
			mode := "preview"
			mutations := 0
			client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				body := tc.state
				code := 200
				ct := "application/json"
				if tc.cloud == "aws" {
					ct = "text/xml"
				}
				payload := ""
				if r.Body != nil {
					b, _ := io.ReadAll(r.Body)
					payload = string(b)
				}
				mutation := r.Method == http.MethodDelete || strings.Contains(payload, "Action=TerminateInstances")
				if mutation {
					mutations++
					body = tc.response
					if tc.cloud == "digitalocean" {
						code = 204
					}
					if mode == "lost" {
						return nil, errors.New("lost response")
					}
				}
				if mode == "observe" || mode == "denied" {
					if mutation {
						t.Fatal("observation resubmitted delete")
					}
					code = 404
					body = `{"error":{"code":"not_found"}}`
					if tc.cloud == "aws" {
						code = 400
						body = `<Response><Errors><Error><Code>InvalidInstanceID.NotFound</Code></Error></Errors></Response>`
					}
					if mode == "denied" {
						code = 403
						body = `{"error":{"code":"forbidden"}}`
					}
				}
				if mode == "changed" {
					body = strings.ReplaceAll(body, "volume-123", "volume-new")
					body = strings.ReplaceAll(body, "vol-123", "vol-new")
					body = strings.ReplaceAll(body, "[42]", "[43]")
				}
				return &http.Response{StatusCode: code, Header: http.Header{"Content-Type": []string{ct}}, Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
			})}
			raw := "secret-test"
			status := "running"
			if tc.cloud == "aws" {
				raw = `{"access_key_id":"key","secret_access_key":"secret"}`
			}
			if tc.cloud == "digitalocean" {
				status = "active"
			}
			p := &provider.PowerRequest{OperationID: strings.Repeat("c", 64), Phase: "preview", Action: "delete", NativeID: tc.id, ExpectedStatus: status}
			req := provider.Request{Version: provider.Protocol, OrganizationID: strings.Repeat("a", 64), ConnectionID: strings.Repeat("b", 64), Provider: tc.cloud, Region: tc.region, Credential: raw, Power: p}
			out := Power(context.Background(), req, client)
			if out.Validate() != nil || out.Power.Outcome != "preview" || mutations != 0 {
				t.Fatalf("preview failed: %+v", out.Power)
			}
			if tc.cloud == "aws" && !strings.Contains(out.Power.DeletionImpact, "Delete EBS volume vol-123") {
				t.Fatal("missing cascade")
			}
			p.DeletionImpact = out.Power.DeletionImpact
			p.Phase = "submit"
			mode = "changed"
			out = Power(context.Background(), req, client)
			if out.Power.Outcome != "failed" || mutations != 0 {
				t.Fatal("changed dependencies reached deletion")
			}
			mode = "submit"
			out = Power(context.Background(), req, client)
			if out.Power.Outcome != "accepted" || mutations != 1 {
				t.Fatalf("submission failed: %+v", out.Power)
			}
			mode = "lost"
			out = Power(context.Background(), req, client)
			if out.Power.Outcome != "uncertain" || mutations != 2 {
				t.Fatal("lost response retried or reported success")
			}
			p.Phase = "observe"
			mode = "denied"
			out = Power(context.Background(), req, client)
			if out.Power.Outcome == "succeeded" {
				t.Fatal("access denied treated as deletion")
			}
			mode = "observe"
			out = Power(context.Background(), req, client)
			if out.Power.Outcome != "succeeded" || mutations != 2 {
				t.Fatalf("absence not observed: %+v", out.Power)
			}
		})
	}
}

func TestPrimaryIPDeletionImpact(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Path != "/v1/primary_ips/7" {
			t.Fatal("wrong primary IP")
		}
		return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(`{"primary_ip":{"id":7,"ip":"192.0.2.7","assignee_id":123,"auto_delete":true}}`)), Request: r}, nil
	})}
	svc := hcloud.NewClient(hcloud.WithToken("test-token"), hcloud.WithHTTPClient(client))
	server := &hcloud.Server{ID: 123, PublicNet: hcloud.ServerPublicNet{IPv4: hcloud.ServerPublicNetIPv4{ID: 7, IP: net.ParseIP("192.0.2.7")}}}
	lines, err := hetznerDeletionImpact(context.Background(), svc, server)
	if err != nil || !strings.Contains(strings.Join(lines, "\n"), "Delete primary IP 7") {
		t.Fatal("auto-deleted primary IP omitted", err)
	}
	server.PublicNet.IPv4.ID = 0
	if _, err = hetznerDeletionImpact(context.Background(), svc, server); err == nil {
		t.Fatal("missing primary IP identity accepted")
	}
}
