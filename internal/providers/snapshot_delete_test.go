package providers

import (
	"context"
	"fmt"
	"github.com/alphabravo-oss/providah-community/sdk/provider"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestSnapshotDeletionSDKs(t *testing.T) {
	for _, cloud := range []string{"aws", "digitalocean", "hetzner"} {
		t.Run(cloud, func(t *testing.T) {
			id, status, region, credential := "123", "available", "global", "test-token"
			if cloud == "aws" {
				id, status, region, credential = "snap-12345678", "completed", "us-east-1", `{"access_key_id":"key","secret_access_key":"secret"}` // gitleaks:allow -- synthetic cloud identifier/credential fixture
			}
			if cloud == "digitalocean" {
				status, region = "present", "nyc3"
			}
			missing, ambiguous, changed, blocked := false, false, false, false
			writes := 0
			client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				body, code, ct := "", 200, "application/json"
				write := r.Method == "DELETE"
				switch cloud {
				case "aws":
					ct = "text/xml"
					raw, _ := io.ReadAll(r.Body)
					values, _ := url.ParseQuery(string(raw))
					action := values.Get("Action")
					write = action == "DeleteSnapshot"
					switch action {
					case "DescribeSnapshots":
						body = fmt.Sprintf(`<DescribeSnapshotsResponse><snapshotSet><item><snapshotId>%s</snapshotId><status>completed</status><description>saved</description><volumeId>vol-12345678</volumeId><volumeSize>40</volumeSize></item></snapshotSet></DescribeSnapshotsResponse>`, id)
					case "DescribeImages":
						body = `<DescribeImagesResponse><imagesSet/></DescribeImagesResponse>`
						if blocked {
							body = `<DescribeImagesResponse><imagesSet><item><imageId>ami-12345678</imageId></item></imagesSet></DescribeImagesResponse>`
						}
					case "DescribeSnapshotAttribute":
						body = `<DescribeSnapshotAttributeResponse><createVolumePermission/></DescribeSnapshotAttributeResponse>`
					case "DeleteSnapshot":
						body = `<DeleteSnapshotResponse><return>true</return></DeleteSnapshotResponse>`
					default:
						t.Fatal("unexpected AWS API")
					}
					if missing {
						code = 400
						body = `<Response><Errors><Error><Code>InvalidSnapshot.NotFound</Code></Error></Errors></Response>`
					}
				case "digitalocean":
					if r.URL.Path != "/v2/snapshots/123" {
						t.Fatal("wrong DO snapshot endpoint")
					}
					body = `{"snapshot":{"id":"123","name":"saved","resource_id":"456","resource_type":"droplet","regions":["nyc3","ams3"]}}`
				default:
					if r.URL.Path != "/v1/images/123" {
						t.Fatal("wrong HZ image endpoint")
					}
					body = `{"image":{"id":123,"type":"snapshot","description":"saved","status":"available"}}`
				}
				if blocked && cloud == "hetzner" {
					body = strings.ReplaceAll(body, `"type":"snapshot"`, `"type":"snapshot","protection":{"delete":true}`)
				}
				if blocked && cloud == "digitalocean" {
					body = strings.ReplaceAll(body, `"resource_id":"456"`, `"resource_id":""`)
				}
				if changed {
					body = strings.ReplaceAll(body, "saved", "changed")
				}
				if write {
					writes++
					code = 204
					body = ""
					if ambiguous {
						code = 500
						body = `{"error":{"code":"server_error","message":"sensitive"}}`
					}
				}
				if missing && cloud != "aws" {
					code = 404
					body = `{"error":{"code":"not_found","message":"gone"}}`
				}
				return &http.Response{StatusCode: code, Header: http.Header{"Content-Type": []string{ct}}, Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
			})}
			req := provider.Request{Version: provider.Protocol, Provider: cloud, OrganizationID: strings.Repeat("a", 64), ConnectionID: strings.Repeat("b", 64), Region: region, Credential: credential, Power: &provider.PowerRequest{ResourceKind: "storage.snapshot", OperationID: strings.Repeat("c", 64), NativeID: id, Action: "delete", Phase: "preview", ExpectedStatus: status}}
			run := func() *provider.PowerResult {
				out := Power(context.Background(), req, client)
				if out.Validate() != nil || out.Power == nil {
					t.Fatal("invalid snapshot result")
				}
				return out.Power
			}
			preview := run()
			if preview.Outcome != "preview" || writes != 0 {
				t.Fatal("preview failed", preview)
			}
			req.Power.DeletionImpact = preview.DeletionImpact
			req.Power.Phase = "submit"
			blocked = true
			if run().Outcome != "failed" || writes != 0 {
				t.Fatal("protected or incomplete snapshot submitted")
			}
			blocked = false
			changed = true
			if run().Error != "state_changed" || writes != 0 {
				t.Fatal("changed review submitted")
			}
			changed = false
			if run().Outcome != "accepted" || writes != 1 {
				t.Fatal("delete was not submitted once")
			}
			req.Power.Phase = "observe"
			if run().Outcome != "accepted" || writes != 1 {
				t.Fatal("present snapshot reported deleted")
			}
			missing = true
			if run().Outcome != "succeeded" || writes != 1 {
				t.Fatal("missing snapshot not confirmed")
			}
			missing = false
			req.Power.Phase = "submit"
			ambiguous = true
			if run().Outcome != "uncertain" || writes != 2 {
				t.Fatal("ambiguous delete retried or misreported")
			}
		})
	}
}
