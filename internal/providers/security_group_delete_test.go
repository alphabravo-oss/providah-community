package providers

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/alphabravo-oss/providah-community/sdk/provider"
)

func TestSecurityGroupDeletion(t *testing.T) {
	const id = "sg-12345678"
	mode := ""
	writes := 0
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if r.URL.Host != "ec2.us-east-1.amazonaws.com" || !strings.Contains(r.Header.Get("Authorization"), "/us-east-1/ec2/aws4_request") {
			t.Fatal("wrong endpoint or unsigned request")
		}
		if err := r.ParseForm(); err != nil {
			t.Fatal(err)
		}
		action := r.Form.Get("Action")
		body, code := "", 200
		switch action {
		case "DescribeSecurityGroups":
			body = "<DescribeSecurityGroupsResponse><securityGroupInfo/></DescribeSecurityGroupsResponse>"
			if r.Form.Get("GroupId.1") != "" {
				if r.Form.Get("GroupId.1") != id {
					t.Fatal("wrong target")
				}
				body = "<DescribeSecurityGroupsResponse><securityGroupInfo><item><groupId>" + id + "</groupId><groupName>unused</groupName><ownerId>123456789012</ownerId><vpcId>vpc-12345678</vpcId><ipPermissions><item><ipProtocol>tcp</ipProtocol><fromPort>443</fromPort><toPort>443</toPort></item></ipPermissions></item></securityGroupInfo></DescribeSecurityGroupsResponse>"
				switch mode {
				case "default":
					body = strings.ReplaceAll(body, "unused", "default")
				case "wrong":
					body = strings.ReplaceAll(body, id, "sg-87654321")
				case "changed":
					body = strings.ReplaceAll(body, "443", "80")
				case "missing":
					code, body = 400, "<Response><Errors><Error><Code>InvalidGroup.NotFound</Code></Error></Errors></Response>"
				}
			} else {
				filter := r.Form.Get("Filter.1.Name")
				if (filter != "ip-permission.group-id" && filter != "egress.ip-permission.group-id") || r.Form.Get("Filter.1.Value.1") != id || r.Form.Get("MaxResults") != "5" {
					t.Fatal("unscoped reference query")
				}
				if mode == filter {
					body = "<DescribeSecurityGroupsResponse><securityGroupInfo><item><groupId>sg-87654321</groupId></item></securityGroupInfo></DescribeSecurityGroupsResponse>"
				}
				if mode == "self" {
					body = "<DescribeSecurityGroupsResponse><securityGroupInfo><item><groupId>" + id + "</groupId></item></securityGroupInfo></DescribeSecurityGroupsResponse>"
				}
				if mode == "group-page" {
					body = "<DescribeSecurityGroupsResponse><nextToken>more</nextToken></DescribeSecurityGroupsResponse>"
				}
			}
		case "DescribeNetworkInterfaces":
			if r.Form.Get("Filter.1.Name") != "group-id" || r.Form.Get("Filter.1.Value.1") != id || r.Form.Get("MaxResults") != "5" {
				t.Fatal("unscoped interface query")
			}
			body = "<DescribeNetworkInterfacesResponse/>"
			if mode == "attached" {
				body = "<DescribeNetworkInterfacesResponse><networkInterfaceSet><item><networkInterfaceId>eni-12345678</networkInterfaceId></item></networkInterfaceSet></DescribeNetworkInterfacesResponse>"
			}
			if mode == "interface-page" {
				body = "<DescribeNetworkInterfacesResponse><nextToken>more</nextToken></DescribeNetworkInterfacesResponse>"
			}
		case "DescribeSecurityGroupReferences":
			if r.Form.Get("GroupId.1") != id {
				t.Fatal("unscoped cross-VPC query")
			}
			body = "<DescribeSecurityGroupReferencesResponse/>"
			if mode == "cross-vpc" {
				body = "<DescribeSecurityGroupReferencesResponse><securityGroupReferenceSet><item><groupId>" + id + "</groupId></item></securityGroupReferenceSet></DescribeSecurityGroupReferencesResponse>"
			}
		case "DescribeSecurityGroupVpcAssociations":
			if r.Form.Get("Filter.1.Name") != "group-id" || r.Form.Get("Filter.1.Value.1") != id || r.Form.Get("MaxResults") != "5" {
				t.Fatal("unscoped association query")
			}
			body = "<DescribeSecurityGroupVpcAssociationsResponse/>"
			if mode == "association" {
				body = "<DescribeSecurityGroupVpcAssociationsResponse><securityGroupVpcAssociationSet><item><groupId>" + id + "</groupId></item></securityGroupVpcAssociationSet></DescribeSecurityGroupVpcAssociationsResponse>"
			}
			if mode == "association-page" {
				body = "<DescribeSecurityGroupVpcAssociationsResponse><nextToken>more</nextToken></DescribeSecurityGroupVpcAssociationsResponse>"
			}
		case "DeleteSecurityGroup":
			writes++
			if r.Form.Get("GroupId") != id || r.Form.Get("GroupName") != "" {
				t.Fatal("wrong deletion target")
			}
			body = "<DeleteSecurityGroupResponse><return>true</return></DeleteSecurityGroupResponse>"
			if mode == "false" {
				body = strings.ReplaceAll(body, "true", "false")
			}
			if mode == "ambiguous" {
				code, body = 500, "<Response><Errors><Error><Code>InternalError</Code></Error></Errors></Response>"
			}
		default:
			t.Fatal("unexpected action", action)
		}
		if mode == "denied" || mode == "deny-"+action {
			code, body = 403, "<Response><Errors><Error><Code>UnauthorizedOperation</Code></Error></Errors></Response>"
		}
		return &http.Response{StatusCode: code, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
	})}
	req := provider.Request{Version: provider.Protocol, Provider: "aws", OrganizationID: strings.Repeat("a", 64), ConnectionID: strings.Repeat("b", 64), Region: "us-east-1", Credential: `{"access_key_id":"test","secret_access_key":"test"}`, Power: &provider.PowerRequest{ResourceKind: "network.firewall", OperationID: strings.Repeat("c", 64), Action: "delete", Phase: "preview", NativeID: id, ExpectedStatus: "present"}}
	run := func() *provider.PowerResult {
		t.Helper()
		if err := req.Power.Validate("aws"); err != nil {
			t.Fatal(err)
		}
		out := Power(context.Background(), req, client)
		if out.Validate() != nil || out.Power == nil {
			t.Fatal("invalid response", out)
		}
		return out.Power
	}
	preview := run()
	if preview.Outcome != "preview" || writes != 0 || !strings.Contains(preview.DeletionImpact, "443") {
		t.Fatal("missing review", preview)
	}
	req.Power.Phase, req.Power.DeletionImpact = "submit", preview.DeletionImpact
	for _, value := range []string{"default", "wrong", "changed", "missing", "attached", "ip-permission.group-id", "egress.ip-permission.group-id", "cross-vpc", "association", "group-page", "interface-page", "association-page", "denied", "deny-DescribeNetworkInterfaces", "deny-DescribeSecurityGroupReferences", "deny-DescribeSecurityGroupVpcAssociations"} {
		mode = value
		if run().Outcome != "failed" || writes != 0 {
			t.Fatal("unsafe submit", value)
		}
	}
	mode = "self"
	if run().Outcome != "accepted" || writes != 1 {
		t.Fatal("self reference prevented otherwise valid deletion")
	}
	for _, value := range []string{"ambiguous", "false"} {
		mode = value
		before := writes
		if run().Outcome != "uncertain" || writes != before+1 {
			t.Fatal("unconfirmed write retried or accepted")
		}
	}
	req.Power.Phase = "observe"
	mode = ""
	if run().Outcome != "accepted" {
		t.Fatal("presence treated as deletion")
	}
	mode = "denied"
	if run().Outcome == "succeeded" {
		t.Fatal("denial treated as absence")
	}
	mode = "missing"
	if run().Outcome != "succeeded" || writes != 3 {
		t.Fatal("absence not confirmed")
	}
}
