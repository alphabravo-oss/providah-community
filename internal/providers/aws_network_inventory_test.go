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

func TestAWSNetworkInventory(t *testing.T) {
	for _, tc := range []struct{ kind, action, set, body string }{
		{"network.route_table", "DescribeRouteTables", "routeTableSet", `<routeTableId>rtb-%s</routeTableId><vpcId>vpc-12345678</vpcId><routeSet><item><state>blackhole</state></item></routeSet><associationSet><item><main>true</main></item></associationSet>`},
		{"network.internet_gateway", "DescribeInternetGateways", "internetGatewaySet", `<internetGatewayId>igw-%s</internetGatewayId><attachmentSet><item><vpcId>vpc-12345678</vpcId><state>available</state></item></attachmentSet>`},
		{"network.nat_gateway", "DescribeNatGateways", "natGatewaySet", `<natGatewayId>nat-%s</natGatewayId><vpcId>vpc-12345678</vpcId><state>available</state><connectivityType>public</connectivityType><availabilityMode>regional</availabilityMode>`},
	} {
		for _, mode := range []string{"success", "empty", "late-error", "invalid", "cycle", "duplicate", "deleted"} {
			t.Run(tc.kind+"/"+mode, func(t *testing.T) {
				calls := 0
				client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
					calls++
					if calls > 3 {
						t.Fatal("pagination did not stop")
					}
					if e := r.ParseForm(); e != nil {
						t.Fatal(e)
					}
					if r.Form.Get("Action") != tc.action || r.Form.Get("MaxResults") != "100" || r.URL.Host != "ec2.us-east-1.amazonaws.com" || !strings.Contains(r.Header.Get("Authorization"), "/us-east-1/ec2/aws4_request") {
						t.Fatal("incorrect signed discovery")
					}
					id := "12345678"
					if calls > 1 && mode != "duplicate" {
						id = "87654321"
					}
					record := fmt.Sprintf(tc.body, id) + `<tagSet><item><key>Name</key><value>Production edge</value></item><item><key>environment</key><value>prod</value></item></tagSet>`
					if mode == "invalid" {
						record = ""
					}
					if mode == "deleted" && tc.kind == "network.nat_gateway" {
						record = strings.ReplaceAll(record, "<state>available</state>", "<state>deleted</state>")
					}
					body := "<" + tc.set + "><item>" + record + "</item></" + tc.set + ">"
					if mode == "empty" || mode == "cycle" {
						body = "<" + tc.set + "/>"
					}
					if mode != "empty" && (calls == 1 || mode == "cycle") {
						body += "<nextToken>next</nextToken>"
					}
					body = "<" + tc.action + "Response>" + body + "</" + tc.action + "Response>"
					status := 200
					if mode == "late-error" && calls == 2 {
						status = 403
						body = `<Response><Errors><Error><Code>UnauthorizedOperation</Code><Message>private-error</Message></Error></Errors></Response>`
					}
					return &http.Response{StatusCode: status, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body))}, nil
				})}
				req := provider.Request{Version: provider.Protocol, OrganizationID: strings.Repeat("a", 64), ConnectionID: strings.Repeat("b", 64), Provider: "aws", Region: "us-east-1", Credential: `{"access_key_id":"fake","secret_access_key":"fake-secret"}`, InventoryKinds: []string{tc.kind}}
				out := Discover(context.Background(), req, client)
				switch mode {
				case "success", "empty", "deleted":
					want := 2
					if mode == "empty" || mode == "deleted" && tc.kind == "network.nat_gateway" {
						want = 0
					}
					if !out.Complete || out.Validate() != nil || len(out.Resources) != want {
						t.Fatal("invalid discovery", out)
					}
					if want > 0 {
						row := out.Resources[0]
						if row.Name != "Production edge" || row.Tags == nil || row.Tags.Labels["environment"] != "prod" || !strings.Contains(row.Size, "vpc-12345678") {
							t.Fatal("metadata lost", row)
						}
					}
				default:
					if out.Complete || len(out.Resources) != 0 || strings.Contains(out.Error, "private-error") {
						t.Fatal("partial or invalid discovery published", out)
					}
				}
			})
		}
	}
}
