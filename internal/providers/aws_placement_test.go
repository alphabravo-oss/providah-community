package providers

import (
	"context"
	"github.com/alphabravo-oss/providah-community/sdk/provider"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestAWSPlacementInventory(t *testing.T) {
	for _, mode := range []string{"complete", "empty", "denied", "missing-id", "missing-state", "negative", "duplicate", "deleted"} {
		t.Run(mode, func(t *testing.T) {
			calls := 0
			client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				calls++
				if e := r.ParseForm(); e != nil {
					t.Fatal(e)
				}
				if calls != 1 || r.Method != "POST" || r.Form.Get("Action") != "DescribePlacementGroups" || r.URL.Host != "ec2.us-east-1.amazonaws.com" || !strings.Contains(r.Header.Get("Authorization"), "/us-east-1/ec2/aws4_request") {
					t.Fatal("unexpected SDK request")
				}
				entry := `<item><groupId>pg-1234567890abcdef0</groupId><groupName>production</groupName><state>available</state><strategy>partition</strategy><partitionCount>3</partitionCount><tagSet><item><key>env</key><value>production</value></item></tagSet></item>`
				switch mode {
				case "missing-id":
					entry = strings.ReplaceAll(entry, "pg-1234567890abcdef0", "")
				case "missing-state":
					entry = strings.ReplaceAll(entry, "available", "")
				case "negative":
					entry = strings.ReplaceAll(entry, ">3<", ">-1<")
				case "duplicate":
					entry += entry
				case "deleted":
					entry = strings.ReplaceAll(entry, "available", "deleted")
				case "empty":
					entry = ""
				}
				body := "<DescribePlacementGroupsResponse><placementGroupSet>" + entry + "</placementGroupSet></DescribePlacementGroupsResponse>"
				status := 200
				if mode == "denied" {
					status = 403
					body = `<Response><Errors><Error><Code>UnauthorizedOperation</Code><Message>private-error</Message></Error></Errors></Response>`
				}
				return &http.Response{StatusCode: status, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body))}, nil
			})}
			out := Discover(context.Background(), provider.Request{Version: provider.Protocol, OrganizationID: strings.Repeat("a", 64), ConnectionID: strings.Repeat("b", 64), Provider: "aws", Region: "us-east-1", Credential: `{"access_key_id":"fake","secret_access_key":"fake-secret"}`, InventoryKinds: []string{"compute.placement_group"}}, client)
			if mode == "complete" || mode == "empty" || mode == "deleted" {
				want := 0
				if mode == "complete" {
					want = 1
				}
				if !out.Complete || out.Validate() != nil || len(out.Resources) != want {
					t.Fatal(out)
				}
				if want == 1 && (out.Resources[0].Name != "production" || out.Resources[0].Size != "partition · 3 partitions" || out.Resources[0].Tags.Labels["env"] != "production") {
					t.Fatal("metadata missing", out)
				}
			} else if out.Complete || len(out.Resources) != 0 || out.Error == "" || strings.Contains(out.Error, "private-error") {
				t.Fatal("invalid inventory accepted", out)
			}
		})
	}
}
