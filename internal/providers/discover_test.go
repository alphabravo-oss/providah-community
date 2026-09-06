package providers

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/alphabravo-oss/providah-community/sdk/provider"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// Exercise the real SDK serializers and pagination without contacting cloud accounts.
func TestSDKDiscovery(t *testing.T) {
	for _, tc := range []struct {
		name, host, region, credential, first, last string
	}{
		{"aws", "ec2.us-east-1.amazonaws.com", "us-east-1", `{"access_key_id":"test-key","secret_access_key":"never-expose-this-secret"}`,
			`<DescribeInstancesResponse xmlns="http://ec2.amazonaws.com/doc/2016-11-15/"><nextToken>next</nextToken><reservationSet><item><instancesSet><item><instanceId>i-123</instanceId><instanceState><name>running</name></instanceState><instanceType>t3.micro</instanceType><ipAddress>192.0.2.1</ipAddress><tagSet><item><key>Name</key><value>web</value></item><item><key>env</key><value>production</value></item></tagSet></item></instancesSet></item></reservationSet></DescribeInstancesResponse>`,
			`<DescribeInstancesResponse xmlns="http://ec2.amazonaws.com/doc/2016-11-15/"><reservationSet/></DescribeInstancesResponse>`},
		{"digitalocean", "api.digitalocean.com", "", "never-expose-this-secret",
			`{"droplets":[{"tags":["env:production"],"id":123,"name":"web","status":"active","region":{"slug":"nyc3"},"size_slug":"s-1vcpu-1gb","networks":{"v4":[{"ip_address":"192.0.2.1","type":"public"}]}}],"links":{"pages":{"next":"https://api.digitalocean.com/v2/droplets?page=2","last":"https://api.digitalocean.com/v2/droplets?page=2"}}}`,
			`{"droplets":[],"links":{}}`},
		{"hetzner", "api.hetzner.cloud", "", "never-expose-this-secret",
			`{"servers":[{"labels":{"env":"production"},"id":123,"name":"web","status":"running","location":{"name":"fsn1"},"server_type":{"name":"cx23"},"private_net":[{"ip":"10.0.0.2"}],"public_net":{"ipv4":{"ip":"192.0.2.1"}}}],"meta":{"pagination":{"page":1,"next_page":2,"last_page":2}}}`,
			`{"servers":[],"meta":{"pagination":{"page":2,"next_page":null,"last_page":2}}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				calls++
				if r.URL.Host != tc.host || r.Header.Get("Authorization") == "" {
					t.Fatalf("unexpected SDK destination or missing authentication: %s", r.URL.Host)
				}
				body := tc.first
				if calls == 2 {
					body = tc.last
					if tc.name != "aws" && r.URL.Query().Get("page") != "2" {
						t.Fatal("SDK did not request the next page")
					}
				}
				if calls > 2 {
					t.Fatal("pagination did not terminate")
				}
				header := http.Header{"Content-Type": []string{"application/json"}}
				if tc.name == "aws" {
					header.Set("Content-Type", "text/xml")
				}
				return &http.Response{StatusCode: 200, Header: header, Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
			})}
			req := provider.Request{Version: provider.Protocol, OrganizationID: strings.Repeat("a", 64), ConnectionID: strings.Repeat("b", 64), Provider: tc.name, Region: tc.region, Credential: tc.credential}
			out := Discover(context.Background(), req, client)
			if out.Validate() != nil || calls != 2 || len(out.Resources) != 1 {
				t.Fatalf("SDK discovery failed: calls=%d result=%+v", calls, out)
			}
			if out.Resources[0].Name != "web" || out.Resources[0].PublicIP != "192.0.2.1" {
				t.Fatalf("resource mapping: %+v", out.Resources[0])
			}
			if tc.name == "hetzner" && (out.Resources[0].Size != "cx23" || out.Resources[0].PrivateIP != "10.0.0.2" || out.Resources[0].Region != "fsn1") {
				t.Fatalf("shared collector lost operational fields: %+v", out.Resources[0])
			}
			tags := out.Resources[0].Tags
			if tags == nil || (tc.name == "digitalocean" && (len(tags.Names) != 1 || tags.Names[0] != "env:production" || len(tags.Labels) != 0)) || (tc.name != "digitalocean" && tags.Labels["env"] != "production") {
				t.Fatal("tag semantics lost", tags)
			}
			// A later-page failure must discard the earlier page and its sensitive error body.
			calls = 0
			base := client.Transport
			client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
				if calls > 0 {
					return &http.Response{StatusCode: 403, Header: make(http.Header), Body: io.NopCloser(strings.NewReader("never-expose-this-secret")), Request: r}, nil
				}
				return base.RoundTrip(r)
			})
			out = Discover(context.Background(), req, client)
			encoded, _ := json.Marshal(out)
			if out.Complete || out.Error == "" || len(out.Resources) != 0 || strings.Contains(string(encoded), "never-expose") {
				t.Fatalf("partial or sensitive result escaped: %s", encoded)
			}
		})
	}
}

func TestMissingDigitalOceanRegionFailsSafely(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(`{"droplets":[{"id":123}]}`)), Request: r}, nil
	})}
	out := Discover(context.Background(), provider.Request{Version: provider.Protocol, OrganizationID: strings.Repeat("a", 64), ConnectionID: strings.Repeat("b", 64), Provider: "digitalocean", Credential: "test-token"}, client)
	if out.Complete || out.Error == "" || len(out.Resources) != 0 {
		t.Fatalf("malformed provider response accepted: %+v", out)
	}
}

func TestAWSWorkerRejectsUnbrokeredPolicy(t *testing.T) {
	for _, field := range []string{`"role_arn":"arn:aws:iam::123456789012:role/test"`, `"account_id":"123456789012"`} {
		_, err := AWSClient(provider.Request{Region: "us-east-1", Credential: `{"access_key_id":"source","secret_access_key":"secret",` + field + `}`}, http.DefaultClient, 1)
		if err == nil {
			t.Fatal("worker accepted unbrokered credentials")
		}
	}
}
