package providers

import (
	"context"
	"errors"
	"github.com/alphabravo-oss/providah-community/sdk/provider"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestPowerSDKBoundaries(t *testing.T) {
	for _, tc := range []struct{ cloud, native, region, credential, state, submit string }{
		{"aws", "i-12345678", "us-east-1", `{"access_key_id":"test","secret_access_key":"secret-not-logged"}`, `<DescribeInstancesResponse xmlns="http://ec2.amazonaws.com/doc/2016-11-15/"><reservationSet><item><instancesSet><item><instanceId>i-12345678</instanceId><instanceState><name>running</name></instanceState></item></instancesSet></item></reservationSet></DescribeInstancesResponse>`, `<StopInstancesResponse xmlns="http://ec2.amazonaws.com/doc/2016-11-15/"><instancesSet/></StopInstancesResponse>`},
		{"digitalocean", "123", "nyc3", "secret-not-logged", `{"droplet":{"id":123,"status":"active","region":{"slug":"nyc3"}}}`, `{"action":{"id":42,"status":"in-progress"}}`},
		{"hetzner", "123", "fsn1", "secret-not-logged", `{"server":{"id":123,"status":"running","location":{"name":"fsn1"}}}`, `{"action":{"id":42,"status":"running"}}`},
	} {
		t.Run(tc.cloud, func(t *testing.T) {
			p := &provider.PowerRequest{OperationID: strings.Repeat("c", 64), Phase: "submit", Action: "shutdown", NativeID: tc.native, ExpectedStatus: "running"}
			if tc.cloud == "digitalocean" {
				p.ExpectedStatus = "active"
			}
			req := provider.Request{Version: provider.Protocol, OrganizationID: strings.Repeat("a", 64), ConnectionID: strings.Repeat("b", 64), Provider: tc.cloud, Region: tc.region, Credential: tc.credential, Power: p}
			calls := 0
			failMutation := false
			observing := false
			client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				calls++
				body := tc.state
				if observing {
					body = strings.ReplaceAll(body, "running", "off")
					body = strings.ReplaceAll(body, "active", "off")
					if tc.cloud == "aws" {
						body = strings.ReplaceAll(body, "off", "stopped")
					}
				}
				if !observing && calls == 2 {
					var payload []byte
					if r.Body != nil {
						payload, _ = io.ReadAll(r.Body)
					}
					switch tc.cloud {
					case "aws":
						if !strings.Contains(string(payload), "Action=StopInstances") {
							t.Fatal("wrong AWS action")
						}
					case "digitalocean":
						if !strings.Contains(string(payload), `"type":"shutdown"`) {
							t.Fatal("shutdown used a forceful action")
						}
					case "hetzner":
						if !strings.HasSuffix(r.URL.Path, "/actions/shutdown") {
							t.Fatal("shutdown used a forceful action")
						}
					}
					if failMutation {
						return nil, errors.New("secret-not-logged")
					}
					body = tc.submit
				}
				if calls > 2 {
					t.Fatal("mutation was retried")
				}
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
			})}
			result := Power(context.Background(), req, client)
			if result.Validate() != nil || result.Power.Outcome != "accepted" || calls != 2 {
				t.Fatalf("SDK submission: %+v calls=%d", result.Power, calls)
			}
			calls = 0
			failMutation = true
			result = Power(context.Background(), req, client)
			if result.Power.Outcome != "uncertain" || result.Power.Error != "submission_uncertain" || calls != 2 {
				t.Fatalf("ambiguous submission lost: %+v", result.Power)
			}
			calls = 0
			failMutation = false
			p.ExpectedStatus = "off"
			result = Power(context.Background(), req, client)
			if result.Power.Outcome != "failed" || result.Power.Error != "state_changed" || calls != 1 {
				t.Fatal("changed state reached mutation")
			}
			calls = 0
			observing = true
			p.Phase = "observe"
			p.ExpectedStatus = "running"
			result = Power(context.Background(), req, client)
			if result.Power.Outcome != "succeeded" || calls != 1 {
				t.Fatalf("observation: %+v", result.Power)
			}
		})
	}
}
