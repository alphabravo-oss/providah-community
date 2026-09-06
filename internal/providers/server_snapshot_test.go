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

func TestServerSnapshotSDKs(t *testing.T) {
	for _, cloud := range []string{"aws", "digitalocean", "hetzner"} {
		t.Run(cloud, func(t *testing.T) {
			native, region, credential, state, id := "123", "fsn1", "test-token", "off", "456"
			if cloud == "aws" {
				native, region, credential, state, id = "i-12345678", "us-east-1", `{"access_key_id":"test","secret_access_key":"test"}`, "stopped", "ami-12345678"
			}
			if cloud == "digitalocean" {
				region = "nyc3"
			}
			p := &provider.PowerRequest{OperationID: strings.Repeat("a", 64), Action: "snapshot", Phase: "submit", NativeID: native, ExpectedStatus: state}
			request := provider.Request{Version: provider.Protocol, Provider: cloud, OrganizationID: strings.Repeat("b", 64), ConnectionID: strings.Repeat("c", 64), Region: region, Credential: credential, Power: p}
			name := "providah-" + p.OperationID
			mutations := 0
			lost, wrong := false, false
			imageState := "available"
			client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				raw := []byte{}
				if r.Body != nil {
					raw, _ = io.ReadAll(r.Body)
				}
				body, ct := "", "application/json"
				mutation := r.Method == "POST"
				switch cloud {
				case "aws":
					values, _ := url.ParseQuery(string(raw))
					ct = "text/xml"
					mutation = values.Get("Action") == "CreateImage"
					switch values.Get("Action") {
					case "CreateImage":
						if values.Get("Name") != name || values.Get("NoReboot") != "true" || values.Get("InstanceId") != native {
							t.Fatal("wrong AMI request")
						}
						body = `<CreateImageResponse><imageId>` + id + `</imageId></CreateImageResponse>`
					case "DescribeImages":
						source := native
						if wrong {
							source = "i-87654321"
						}
						body = fmt.Sprintf(`<DescribeImagesResponse><imagesSet><item><imageId>%s</imageId><name>%s</name><sourceInstanceId>%s</sourceInstanceId><imageState>%s</imageState></item></imagesSet></DescribeImagesResponse>`, id, name, source, imageState)
					case "DescribeInstances":
						body = fmt.Sprintf(`<DescribeInstancesResponse><reservationSet><item><instancesSet><item><instanceId>%s</instanceId><instanceState><name>%s</name></instanceState><rootDeviceType>ebs</rootDeviceType></item></instancesSet></item></reservationSet></DescribeInstancesResponse>`, native, state)
					default:
						t.Fatal("unexpected AWS action", values.Get("Action"))
					}
				case "digitalocean":
					if mutation {
						var v map[string]any
						if json.Unmarshal(raw, &v) != nil || v["type"] != "snapshot" || v["name"] != name {
							t.Fatal("wrong snapshot body")
						}
						body = `{"action":{"id":456}}`
					} else if strings.Contains(r.URL.Path, "/actions/") {
						source := "123"
						if wrong {
							source = "321"
						}
						status := "in-progress"
						if imageState == "available" {
							status = "completed"
						}
						body = fmt.Sprintf(`{"action":{"id":456,"resource_id":%s,"resource_type":"droplet","type":"snapshot","status":%q}}`, source, status)
					} else {
						body = fmt.Sprintf(`{"droplet":{"id":123,"status":%q,"region":{"slug":"nyc3"}}}`, state)
					}
				default:
					if mutation {
						var v map[string]any
						if json.Unmarshal(raw, &v) != nil || v["type"] != "snapshot" || v["description"] != name || !strings.HasSuffix(r.URL.Path, "/actions/create_image") {
							t.Fatal("wrong Hetzner image request")
						}
						body = `{"action":{"id":789},"image":{"id":456}}`
					} else if strings.Contains(r.URL.Path, "/images/") {
						source := 123
						if wrong {
							source = 321
						}
						body = fmt.Sprintf(`{"image":{"id":456,"description":%q,"type":"snapshot","status":%q,"created_from":{"id":%d}}}`, name, imageState, source)
					} else {
						body = fmt.Sprintf(`{"server":{"id":123,"status":%q,"location":{"name":"fsn1"}}}`, state)
					}
				}
				if mutation {
					mutations++
					if lost {
						return nil, errors.New("lost response")
					}
				}
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{ct}}, Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
			})}
			if e := request.Validate(); e != nil {
				t.Fatal(e)
			}
			out := Power(context.Background(), request, client).Power
			if out == nil || out.Outcome != "accepted" || out.ActionID != id || mutations != 1 {
				t.Fatal("snapshot submission", out, mutations)
			}
			p.Phase = "observe"
			p.ActionID = out.ActionID
			imageState = "pending"
			if out = Power(context.Background(), request, client).Power; out.Outcome != "accepted" {
				t.Fatal("pending image reported complete", out)
			}
			imageState = "available"
			wrong = true
			if out = Power(context.Background(), request, client).Power; out.Outcome == "succeeded" {
				t.Fatal("wrong source accepted", out)
			}
			wrong = false
			if out = Power(context.Background(), request, client).Power; out.Outcome != "succeeded" || out.Status != "snapshot_available" || mutations != 1 {
				t.Fatal("snapshot observation", out)
			}
			p.Phase = "submit"
			p.ActionID = ""
			state = "running"
			if out = Power(context.Background(), request, client).Power; out.Outcome != "failed" || mutations != 1 {
				t.Fatal("running server snapshotted", out)
			}
			state = p.ExpectedStatus
			lost = true
			if out = Power(context.Background(), request, client).Power; out.Outcome != "uncertain" || mutations != 2 {
				t.Fatal("ambiguous snapshot retried", out, mutations)
			}
		})
	}
}
