package providers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/alphabravo-oss/providah-community/sdk/provider"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestResizeSDKs(t *testing.T) {
	for _, cloud := range []string{"aws", "digitalocean", "hetzner"} {
		t.Run(cloud, func(t *testing.T) {
			native, region, credential, off, source, target := "123", "fsn1", "fake-secret", "off", "cx23", "cx33"
			if cloud == "aws" {
				native, region, credential, off, source, target = "i-12345678", "us-east-1", `{"access_key_id":"fake","secret_access_key":"fake"}`, "stopped", "t3.small", "t3.medium"
			}
			if cloud == "digitalocean" {
				region, source, target = "nyc3", "s-1vcpu-1gb", "s-2vcpu-2gb"
			}
			p := &provider.PowerRequest{OperationID: strings.Repeat("c", 64), Phase: "submit", Action: "resize", NativeID: native, ExpectedStatus: off, ExpectedSize: source, TargetSize: target}
			req := provider.Request{Version: provider.Protocol, OrganizationID: strings.Repeat("a", 64), ConnectionID: strings.Repeat("b", 64), Provider: cloud, Region: region, Credential: credential, Power: p}
			mode := "submit"
			mutations := 0
			size := source
			state := off
			client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				if r.Header.Get("Authorization") == "" {
					t.Fatal("unsigned SDK request")
				}
				payload := []byte{}
				if r.Body != nil {
					payload, _ = io.ReadAll(r.Body)
				}
				isMutation := r.Method == "POST"
				body, ct := "", "application/json"
				if cloud == "aws" {
					values, _ := url.ParseQuery(string(payload))
					isMutation = values.Get("Action") == "ModifyInstanceAttribute"
					ct = "text/xml"
					if isMutation {
						if values.Get("InstanceType.Value") != target || values.Get("InstanceId") != native {
							t.Fatal("wrong instance resize input")
						}
						body = `<ModifyInstanceAttributeResponse xmlns="http://ec2.amazonaws.com/doc/2016-11-15/"><return>true</return></ModifyInstanceAttributeResponse>`
					} else {
						body = fmt.Sprintf(`<DescribeInstancesResponse xmlns="http://ec2.amazonaws.com/doc/2016-11-15/"><reservationSet><item><instancesSet><item><instanceId>%s</instanceId><instanceState><name>%s</name></instanceState><instanceType>%s</instanceType><rootDeviceType>ebs</rootDeviceType></item></instancesSet></item></reservationSet></DescribeInstancesResponse>`, native, state, size)
					}
				} else if isMutation {
					var data map[string]any
					if json.Unmarshal(payload, &data) != nil {
						t.Fatal("bad mutation payload")
					}
					if cloud == "digitalocean" {
						if data["type"] != "resize" || data["size"] != target || data["disk"] != false {
							t.Fatal("disk preservation lost", data)
						}
					} else {
						if !strings.HasSuffix(r.URL.Path, "/actions/change_type") || data["server_type"] != target || data["upgrade_disk"] != false {
							t.Fatal("disk preservation lost", data)
						}
					}
					body = `{"action":{"id":42,"status":"running"}}`
				} else if strings.Contains(r.URL.Path, "/actions/") {
					status := "success"
					if cloud == "digitalocean" {
						status = "completed"
					}
					if mode == "failed action" {
						status = "errored"
					}
					body = fmt.Sprintf(`{"action":{"id":42,"status":%q}}`, status)
				} else if cloud == "digitalocean" {
					body = fmt.Sprintf(`{"droplet":{"id":123,"status":%q,"region":{"slug":%q},"size_slug":%q}}`, state, region, size)
				} else {
					body = fmt.Sprintf(`{"server":{"id":123,"status":%q,"location":{"name":%q},"server_type":{"name":%q}}}`, state, region, size)
				}
				if isMutation {
					mutations++
					if mode == "lost" {
						return nil, errors.New("lost response")
					}
				}
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {ct}}, Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
			})}
			call := func(want string) {
				t.Helper()
				out := Power(context.Background(), req, client)
				if out.Validate() != nil || out.Power == nil || out.Power.Outcome != want {
					t.Fatalf("%s: %+v", mode, out.Power)
				}
			}
			call("accepted")
			if mutations != 1 {
				t.Fatal("resize not sent exactly once")
			}
			mode = "lost"
			call("uncertain")
			if mutations != 2 {
				t.Fatal("uncertain mutation retried")
			}
			size = target
			mode = "changed source"
			call("failed")
			if mutations != 2 {
				t.Fatal("changed size mutated")
			}
			size = source
			state = "running"
			mode = "running"
			call("failed")
			if mutations != 2 {
				t.Fatal("running server resized")
			}
			p.Phase = "observe"
			state = off
			mode = "unchanged"
			if cloud != "aws" {
				p.ActionID = "42"
			}
			call("accepted")
			size = target
			mode = "complete"
			call("succeeded")
			if mutations != 2 {
				t.Fatal("observation mutated server")
			}
			if cloud != "aws" {
				mode = "failed action"
				call("failed")
			}
		})
	}
}
