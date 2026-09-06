package providers

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/alphabravo-oss/providah-community/sdk/provider"
)

func TestAWSLoadBalancerInventory(t *testing.T) {
	for _, mode := range []string{"complete", "empty", "late-denied", "tag-denied", "missing-tags", "foreign-tags", "duplicate-tags", "missing-state", "cycle", "duplicate-resource"} {
		t.Run(mode, func(t *testing.T) {
			calls := map[string]int{}
			client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				if req.Method != "POST" || req.URL.Host != "elasticloadbalancing.us-east-1.amazonaws.com" || !strings.Contains(req.Header.Get("Authorization"), "/us-east-1/elasticloadbalancing/aws4_request") {
					t.Fatal("wrong SDK endpoint or unsigned request")
				}
				if err := req.ParseForm(); err != nil {
					t.Fatal(err)
				}
				version, action := req.Form.Get("Version"), req.Form.Get("Action")
				legacy := version == "2012-06-01"
				if !legacy && version != "2015-12-01" {
					t.Fatal("wrong API version", version)
				}
				key := version + action
				calls[key]++
				if calls[key] > 4 {
					t.Fatal("unbounded pagination")
				}
				status := 200
				payload := ""
				if (mode == "late-denied" && legacy) || (mode == "tag-denied" && action == "DescribeTags") {
					status = 403
					payload = `<ErrorResponse><Error><Code>AccessDenied</Code><Message>private-error</Message></Error></ErrorResponse>`
				} else if action == "DescribeLoadBalancers" {
					if req.Form.Get("PageSize") != "20" {
						t.Fatal("tag batch bound missing")
					}
					page := calls[key]
					if (page == 1 && req.Form.Get("Marker") != "") || (page > 1 && req.Form.Get("Marker") != "next") {
						t.Fatal("marker lost")
					}
					count, offset, tail := 20, 0, "<NextMarker>next</NextMarker>"
					if page > 1 {
						count, offset, tail = 1, 20, ""
					}
					if mode == "duplicate-resource" {
						offset = 0
					}
					if mode == "empty" {
						count, tail = 0, ""
					}
					if mode == "cycle" {
						count, tail = 0, "<NextMarker>next</NextMarker>"
					}
					members := ""
					for i := 0; i < count; i++ {
						name := fmt.Sprintf("lb-%d", offset+i)
						if legacy {
							members += `<member><LoadBalancerName>` + name + `</LoadBalancerName><Scheme>internal</Scheme><Instances><member><InstanceId>i-12345678</InstanceId></member></Instances></member>`
						} else {
							typ := []string{"application", "network", "gateway"}[i%3]
							state := "<State><Code>active</Code></State>"
							if mode == "missing-state" {
								state = ""
							}
							members += `<member><LoadBalancerArn>arn:aws:elasticloadbalancing:us-east-1:123456789012:loadbalancer/app/` + name + `/0123456789abcdef</LoadBalancerArn><LoadBalancerName>` + name + `</LoadBalancerName><Type>` + typ + `</Type><Scheme>internal</Scheme><IpAddressType>ipv4</IpAddressType>` + state + `</member>`
						}
					}
					field := "LoadBalancers"
					if legacy {
						field = "LoadBalancerDescriptions"
					}
					payload = "<DescribeLoadBalancersResponse><DescribeLoadBalancersResult><" + field + ">" + members + "</" + field + ">" + tail + "</DescribeLoadBalancersResult></DescribeLoadBalancersResponse>"
				} else if action == "DescribeTags" {
					members := ""
					field, prefix := "ResourceArn", "ResourceArns"
					if legacy {
						field, prefix = "LoadBalancerName", "LoadBalancerNames"
					}
					for i := 1; ; i++ {
						id := req.Form.Get(fmt.Sprintf("%s.member.%d", prefix, i))
						if id == "" {
							break
						}
						if i > 20 {
							t.Fatal("tag limit exceeded")
						}
						if mode == "missing-tags" && i == 1 {
							continue
						}
						if mode == "foreign-tags" {
							id = "foreign"
						}
						member := `<member><` + field + `>` + id + `</` + field + `><Tags><member><Key>env</Key><Value>production</Value></member></Tags></member>`
						members += member
						if mode == "duplicate-tags" {
							members += member
						}
					}
					payload = "<DescribeTagsResponse><DescribeTagsResult><TagDescriptions>" + members + "</TagDescriptions></DescribeTagsResult></DescribeTagsResponse>"
				} else {
					t.Fatal("unexpected action", action)
				}
				return &http.Response{StatusCode: status, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(payload)), Request: req}, nil
			})}
			r := provider.Request{Version: provider.Protocol, OrganizationID: strings.Repeat("a", 64), ConnectionID: strings.Repeat("b", 64), Provider: "aws", Region: "us-east-1", Credential: `{"access_key_id":"test","secret_access_key":"test"}`, InventoryKinds: []string{"network.load_balancer"}}
			result := Discover(context.Background(), r, client)
			if mode != "complete" && mode != "empty" {
				if result.Complete || result.Error == "" || len(result.Resources) != 0 {
					t.Fatal("partial inventory published", result)
				}
				return
			}
			want := 42
			if mode == "empty" {
				want = 0
			}
			if !result.Complete || result.Validate() != nil || len(result.Resources) != want {
				t.Fatal("incomplete discovery", result.Error, len(result.Resources))
			}
			for _, row := range result.Resources {
				if row.Kind != "network.load_balancer" || row.Tags == nil || row.Tags.Labels["env"] != "production" || row.Region != "us-east-1" {
					t.Fatal("lost labels/scope", row)
				}
			}
			if mode == "complete" && (calls["2012-06-01DescribeTags"] != 2 || calls["2015-12-01DescribeTags"] != 2) {
				t.Fatal("both generations not tagged", calls)
			}
		})
	}
}
