package providers

import (
	"context"
	"fmt"
	"github.com/alphabravo-oss/providah-community/sdk/provider"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestPlacementGroupDeletion(t *testing.T) {
	for _, cloud := range []string{"aws", "hetzner"} {
		t.Run(cloud, func(t *testing.T) {
			mode, writes := "", 0
			client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				body, code, content := "", 200, "application/json"
				if cloud == "hetzner" {
					if r.URL.Path != "/v1/placement_groups/123" || r.Header.Get("Authorization") != "Bearer fake-token" {
						t.Fatal("incorrect Hetzner target", r.URL)
					}
					if r.Method == "DELETE" {
						writes++
						code = 204
						if mode == "bad-ack" {
							code = 202
						}
						if mode == "lost" {
							code = 500
							body = `{"error":{"code":"server_error","message":"private"}}`
						}
					} else {
						if r.Method != "GET" {
							t.Fatal("unexpected mutation")
						}
						servers := "[]"
						if mode == "occupied" {
							servers = "[1]"
						}
						name := "Empty group"
						if mode == "changed" {
							name = "Changed group"
						}
						body = fmt.Sprintf(`{"placement_group":{"id":123,"name":%q,"type":"spread","created":"2026-01-01T00:00:00Z","servers":%s}}`, name, servers)
						if mode == "missing" {
							code = 404
							body = `{"error":{"code":"not_found","message":"missing"}}`
						}
						if mode == "denied" {
							code = 403
							body = `{"error":{"code":"forbidden","message":"private"}}`
						}
					}
				} else {
					content = "text/xml"
					if err := r.ParseForm(); err != nil {
						t.Fatal(err)
					}
					action := r.Form.Get("Action")
					if !strings.Contains(r.Header.Get("Authorization"), "/us-east-1/ec2/aws4_request") {
						t.Fatal("unsigned regional target")
					}
					switch action {
					case "DescribePlacementGroups":
						if r.Form.Get("GroupId.1") != "pg-1234567890abcdef0" {
							t.Fatal("wrong placement ID")
						}
						name := "Empty group"
						if mode == "changed" {
							name = "Changed group"
						}
						body = fmt.Sprintf(`<placementGroupSet><item><groupId>pg-1234567890abcdef0</groupId><groupName>%s</groupName><groupArn>arn:aws:ec2:us-east-1:123456789012:placement-group/Empty</groupArn><state>available</state><strategy>spread</strategy></item></placementGroupSet>`, name)
						if mode == "missing" {
							body = `<placementGroupSet/>`
						}
						if mode == "denied" {
							code = 403
							body = `<Errors><Error><Code>UnauthorizedOperation</Code><Message>private</Message></Error></Errors>`
						}
					case "DescribeInstances":
						if r.Form.Get("Filter.1.Name") != "placement-group-id" || r.Form.Get("Filter.1.Value.1") != "pg-1234567890abcdef0" {
							t.Fatal("wrong membership filter")
						}
						body = `<reservationSet/>`
						if mode == "occupied" {
							body = `<reservationSet><item><instancesSet><item><instanceId>i-12345678</instanceId></item></instancesSet></item></reservationSet>`
						}
						if mode == "more" {
							body += `<nextToken>more</nextToken>`
						}
					case "DeletePlacementGroup":
						if r.Form.Get("GroupName") != "Empty group" {
							t.Fatal("wrong deletion name")
						}
						writes++
						if mode == "bad-ack" {
							code = 202
						}
						body = `<return>true</return>`
						if mode == "lost" {
							code = 500
							body = `<Errors><Error><Code>InternalError</Code><Message>private</Message></Error></Errors>`
						}
					default:
						t.Fatal("unexpected API", action)
					}
					body = "<" + action + "Response>" + body + "</" + action + "Response>"
				}
				return &http.Response{StatusCode: code, Header: http.Header{"Content-Type": {content}}, Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
			})}
			req := provider.Request{Provider: cloud, Region: "global", Credential: "fake-token", Power: &provider.PowerRequest{NativeID: "123", ResourceKind: "compute.placement_group", OperationID: strings.Repeat("a", 64), Action: "delete", Phase: "preview", ExpectedStatus: "present"}}
			if cloud == "aws" {
				req.Region = "us-east-1"
				req.Credential = `{"access_key_id":"fake","secret_access_key":"fake-secret"}`
				req.Power.NativeID = "pg-1234567890abcdef0"
				req.Power.ExpectedStatus = "available"
			}
			run := func() *provider.PowerResult {
				if err := req.Power.Validate(cloud); err != nil {
					t.Fatal(err)
				}
				return deletePlacementGroup(context.Background(), req, client)
			}
			preview := run()
			if preview.Outcome != "preview" || writes != 0 || !strings.Contains(preview.DeletionImpact, "Empty group") {
				t.Fatal("missing preview", preview)
			}
			req.Power.Phase = "submit"
			req.Power.DeletionImpact = preview.DeletionImpact
			for _, m := range []string{"occupied", "changed", "missing", "denied", "more"} {
				if m == "more" && cloud != "aws" {
					continue
				}
				mode = m
				if got := run(); got.Outcome != "failed" || writes != 0 {
					t.Fatal("unsafe deletion", m, got)
				}
			}
			for _, m := range []string{"", "lost", "bad-ack"} {
				mode = m
				before := writes
				want := "accepted"
				if m != "" {
					want = "uncertain"
				}
				if got := run(); got.Outcome != want || writes != before+1 {
					t.Fatal("write retried or misreported", m, got)
				}
			}
			req.Power.Phase = "observe"
			for _, m := range []string{"", "missing", "denied"} {
				mode = m
				before := writes
				want := "accepted"
				if m == "missing" {
					want = "succeeded"
				}
				if got := run(); got.Outcome != want || writes != before {
					t.Fatal("unsafe observation", m, got)
				}
			}
		})
	}
}
