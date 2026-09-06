package providers

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/alphabravo-oss/providah-community/sdk/provider"
)

func TestRoute53Discovery(t *testing.T) {
	for _, mode := range []string{"zones", "records", "record failure", "missing zone marker", "repeated zone marker", "repeated record marker", "missing TTL", "unsafe zone"} {
		t.Run(mode, func(t *testing.T) {
			calls := 0
			client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				calls++
				if req.Method != "GET" || req.URL.Host != "route53.amazonaws.com" || !strings.Contains(req.Header.Get("Authorization"), "AWS4-HMAC-SHA256") {
					t.Fatal("unexpected unsigned or non-Route53 request")
				}
				body := ""
				status := 200
				switch req.URL.Path {
				case "/2013-04-01/hostedzone":
					id, private, tail := "ZA", "false", "<IsTruncated>true</IsTruncated><NextMarker>ZB</NextMarker>"
					if req.URL.Query().Get("marker") != "" {
						if req.URL.Query().Get("marker") != "ZB" {
							t.Fatal("incorrect zone marker")
						}
						id, private, tail = "ZB", "true", "<IsTruncated>false</IsTruncated>"
					}
					if mode == "repeated zone marker" {
						tail = "<IsTruncated>true</IsTruncated><NextMarker>ZB</NextMarker>"
					}
					if mode == "missing zone marker" {
						tail = "<IsTruncated>true</IsTruncated>"
					}
					if mode == "unsafe zone" {
						id = "../../bad"
					}
					body = fmt.Sprintf(`<ListHostedZonesResponse xmlns="https://route53.amazonaws.com/doc/2013-04-01/"><HostedZones><HostedZone><Id>/hostedzone/%s</Id><Name>example.test.</Name><CallerReference>omitted-sensitive-caller</CallerReference><Config><PrivateZone>%s</PrivateZone><Comment>omitted-sensitive-comment</Comment></Config><ResourceRecordSetCount>3</ResourceRecordSetCount></HostedZone></HostedZones>%s</ListHostedZonesResponse>`, id, private, tail)
				case "/2013-04-01/hostedzone/ZA/rrset", "/2013-04-01/hostedzone/ZB/rrset":
					set := `<ResourceRecordSet><Name>www.example.test.</Name><Type>A</Type><SetIdentifier>blue</SetIdentifier><Weight>10</Weight><TTL>300</TTL><ResourceRecords><ResourceRecord><Value>omitted-sensitive-value</Value></ResourceRecord></ResourceRecords></ResourceRecordSet>`
					tail := `<IsTruncated>true</IsTruncated><NextRecordName>www.example.test.</NextRecordName><NextRecordType>A</NextRecordType><NextRecordIdentifier>green</NextRecordIdentifier>`
					if req.URL.Query().Get("name") != "" {
						if req.URL.Query().Get("name") != "www.example.test." || req.URL.Query().Get("type") != "A" || req.URL.Query().Get("identifier") != "green" {
							t.Fatal("record pagination lost composite cursor")
						}
						set = strings.ReplaceAll(set, "blue", "green") + `<ResourceRecordSet><Name>alias.example.test.</Name><Type>A</Type><AliasTarget><HostedZoneId>ZALIAS</HostedZoneId><DNSName>omitted-sensitive-alias</DNSName><EvaluateTargetHealth>false</EvaluateTargetHealth></AliasTarget></ResourceRecordSet>`
						if mode != "repeated record marker" {
							tail = "<IsTruncated>false</IsTruncated>"
						}
						if mode == "record failure" {
							status = 403
							set = ""
						}
					}
					if mode == "missing TTL" {
						set = strings.ReplaceAll(set, "<TTL>300</TTL>", "")
					}
					body = `<ListResourceRecordSetsResponse xmlns="https://route53.amazonaws.com/doc/2013-04-01/"><ResourceRecordSets>` + set + `</ResourceRecordSets>` + tail + `</ListResourceRecordSetsResponse>`
				default:
					t.Fatalf("unexpected path %s", req.URL.Path)
				}
				return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": {"application/xml"}}, Body: io.NopCloser(strings.NewReader(body)), Request: req}, nil
			})}
			kind := "dns.record"
			if mode == "zones" {
				kind = "dns.zone"
			}
			request := provider.Request{Version: provider.Protocol, OrganizationID: strings.Repeat("a", 64), ConnectionID: strings.Repeat("b", 64), Provider: "aws", Credential: `{"access_key_id":"test","secret_access_key":"test"}`, Region: "us-east-1", InventoryKinds: []string{kind}}
			response := Discover(context.Background(), request, client)
			if mode != "zones" && mode != "records" {
				if calls == 0 || response.Complete || response.Error == "" || len(response.Resources) != 0 {
					t.Fatalf("bad scan accepted: %+v", response)
				}
				return
			}
			want, count := 6, 6
			if mode == "zones" {
				want, count = 2, 2
			}
			if !response.Complete || len(response.Resources) != want || calls != count {
				t.Fatalf("discovery: %+v calls=%d", response, calls)
			}
			seen := map[string]bool{}
			for _, r := range response.Resources {
				if r.Region != "global" || r.Kind != kind || seen[r.NativeID] {
					t.Fatal("lost global scope or record identity")
				}
				seen[r.NativeID] = true
			}
			encoded, _ := json.Marshal(response)
			if strings.Contains(string(encoded), "omitted-sensitive") {
				t.Fatal("provider payload leaked into inventory")
			}
			if mode == "zones" && (!strings.Contains(response.Resources[0].Size, "public") || !strings.Contains(response.Resources[1].Size, "private")) {
				t.Fatal("zone visibility lost")
			}
			if mode == "records" && !strings.Contains(response.Resources[2].Size, "TTL managed by target") {
				t.Fatal("alias represented with an invented TTL")
			}
		})
	}
}
