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

func TestVolumeDeletionSDKs(t *testing.T) {
	for _, cloud := range []string{"aws", "digitalocean", "hetzner"} {
		t.Run(cloud, func(t *testing.T) {
			id, region, credential := "123", "fsn1", "test-token"
			if cloud == "aws" {
				id, region, credential = "vol-12345678", "us-east-1", `{"access_key_id":"key","secret_access_key":"secret"}` // gitleaks:allow -- synthetic cloud identifier/credential fixture
			}
			if cloud == "digitalocean" {
				id, region = "12345678-1234-1234-1234-123456789abc", "nyc3"
			}
			writes := 0
			attached, missing, ambiguous, changed, protected := false, false, false, false, false
			client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				body, code, ct := "", 200, "application/json"
				write := r.Method == "DELETE"
				switch cloud {
				case "aws":
					ct = "text/xml"
					raw, _ := io.ReadAll(r.Body)
					values, _ := url.ParseQuery(string(raw))
					write = values.Get("Action") == "DeleteVolume"
					if values.Get("Action") != "DescribeVolumes" && !write {
						t.Fatal("unexpected AWS action")
					}
					body = fmt.Sprintf(`<DescribeVolumesResponse><volumeSet><item><volumeId>%s</volumeId><size>40</size><status>available</status><availabilityZone>us-east-1a</availabilityZone><attachmentSet>%s</attachmentSet></item></volumeSet></DescribeVolumesResponse>`, id, map[bool]string{true: "<item><instanceId>i-12345678</instanceId></item>"}[attached])
					if missing {
						code = 400
						body = `<Response><Errors><Error><Code>InvalidVolume.NotFound</Code></Error></Errors></Response>`
					}
				case "digitalocean":
					if r.URL.Path != "/v2/volumes/"+id {
						t.Fatal("wrong DO volume endpoint")
					}
					body = fmt.Sprintf(`{"volume":{"id":"%s","name":"data","size_gigabytes":40,"region":{"slug":"nyc3"},"droplet_ids":[%s]}}`, id, map[bool]string{true: "456"}[attached])
				default:
					if r.URL.Path != "/v1/volumes/123" {
						t.Fatal("wrong HZ volume endpoint")
					}
					body = fmt.Sprintf(`{"volume":{"id":123,"name":"data","size":40,"status":"available","location":{"name":"fsn1"},"server":%s}}`, map[bool]string{true: "456", false: "null"}[attached])
				}
				if protected && cloud == "hetzner" {
					body = strings.ReplaceAll(body, `"status":"available"`, `"status":"available","protection":{"delete":true}`)
				}
				if changed {
					body = strings.ReplaceAll(body, "40", "80")
				}
				if write {
					writes++
					body = ""
					code = 204
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
			req := provider.Request{Version: provider.Protocol, Provider: cloud, OrganizationID: strings.Repeat("a", 64), ConnectionID: strings.Repeat("b", 64), Region: region, Credential: credential, Power: &provider.PowerRequest{ResourceKind: "storage.volume", OperationID: strings.Repeat("c", 64), NativeID: id, Action: "delete", Phase: "preview", ExpectedStatus: "available"}}
			run := func() *provider.PowerResult {
				out := Power(context.Background(), req, client)
				if out.Validate() != nil || out.Power == nil {
					t.Fatal("invalid volume result")
				}
				return out.Power
			}
			preview := run()
			if preview.Outcome != "preview" || writes != 0 {
				t.Fatal("preview failed", preview)
			}
			req.Power.DeletionImpact = preview.DeletionImpact
			req.Power.Phase = "submit"
			attached = true
			if run().Outcome != "failed" || writes != 0 {
				t.Fatal("attached volume submitted")
			}
			attached = false
			if cloud == "hetzner" {
				protected = true
				if run().Outcome != "failed" || writes != 0 {
					t.Fatal("protected volume submitted")
				}
				protected = false
			}
			changed = true
			if run().Error != "state_changed" || writes != 0 {
				t.Fatal("changed volume submitted")
			}
			changed = false
			if run().Outcome != "accepted" || writes != 1 {
				t.Fatal("delete not submitted once")
			}
			req.Power.Phase = "observe"
			if run().Outcome != "accepted" || writes != 1 {
				t.Fatal("present volume marked deleted")
			}
			missing = true
			if run().Outcome != "succeeded" || writes != 1 {
				t.Fatal("not-found volume not confirmed")
			}
			missing = false
			req.Power.Phase = "submit"
			ambiguous = true
			if run().Outcome != "uncertain" || writes != 2 {
				t.Fatal("ambiguous volume deletion retried")
			}
		})
	}
}
