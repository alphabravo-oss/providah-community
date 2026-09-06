package providers

import (
	"context"
	"encoding/json"
	"github.com/alphabravo-oss/providah-community/sdk/provider"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestInfrastructureSDKs(t *testing.T) {
	cases := []struct{ cloud, kind, path, body, region string }{
		{"digitalocean", "dns.zone", "/v2/domains", `{"domains":[{"name":"example.test","ttl":3600,"zone_file":"omitted-sensitive-zone"}]}`, "global"},
		{"hetzner", "dns.zone", "/v1/zones", `{"zones":[{"id":123,"name":"example.test","ttl":3600,"mode":"primary","status":"ok","record_count":2,"primary_nameservers":[{"address":"192.0.2.1","port":53,"tsig_key":"omitted-sensitive-key"}]}],"meta":{"pagination":{"next_page":null}}}`, "global"},
		{"digitalocean", "database.cluster", "/v2/databases", `{"databases":[{"id":"db-123","name":"production","engine":"pg","version":"17","size":"db-s-1vcpu-1gb","num_nodes":2,"region":"nyc3","status":"online","connection":{"uri":"omitted-sensitive-uri","password":"omitted-sensitive-password"},"users":[{"name":"dbadmin","password":"omitted-sensitive-user-password"}]}]}`, "nyc3"},
		{"digitalocean", "kubernetes.cluster", "/v2/kubernetes/clusters", `{"kubernetes_clusters":[{"id":"cluster-123","name":"production","region":"nyc3","version":"1.33.1-do.0","ipv4":"192.0.2.9","ha":true,"status":{"state":"running","message":"omitted-sensitive-status"},"node_pools":[{"id":"pool-123"}],"endpoint":"https://omitted-sensitive-endpoint"}]}`, "nyc3"},
		{"digitalocean", "network.certificate", "/v2/certificates", `{"certificates":[{"id":"cert-123","name":"public web","type":"lets_encrypt","state":"verified","dns_names":["web.example.test"],"not_after":"2030-01-01T12:00:00Z","private_key":"omitted-sensitive-key"}]}`, "global"},
		{"hetzner", "network.certificate", "/v1/certificates", `{"certificates":[{"id":123,"name":"public web","type":"managed","domain_names":["web.example.test"],"not_valid_after":"2030-01-01T12:00:00Z","certificate":"omitted-sensitive-pem","status":{"issuance":"completed","renewal":"scheduled"}}],"meta":{"pagination":{"next_page":null}}}`, "global"},

		{"aws", "network.subnet", "DescribeSubnets", `<DescribeSubnetsResponse><subnetSet><item><subnetId>subnet-123</subnetId><vpcId>vpc-123</vpcId><state>available</state><cidrBlock>10.0.0.0/24</cidrBlock><availabilityZone>us-east-1a</availabilityZone></item></subnetSet></DescribeSubnetsResponse>`, "us-east-1"},
		{"aws", "network.ip", "DescribeAddresses", `<DescribeAddressesResponse><addressesSet><item><allocationId>eipalloc-123</allocationId><publicIp>192.0.2.1</publicIp><associationId>eipassoc-123</associationId></item></addressesSet></DescribeAddressesResponse>`, "us-east-1"},
		{"digitalocean", "network.ip", "/v2/reserved_ips", `{"reserved_ips":[{"ip":"192.0.2.1","region":{"slug":"nyc3"},"droplet":{"id":123}}]}`, "nyc3"},
		{"hetzner", "network.primary_ip", "/v1/primary_ips", `{"primary_ips":[{"id":123,"name":"primary","type":"ipv4","ip":"192.0.2.1","assignee_id":456,"location":{"name":"fsn1"}}],"meta":{"pagination":{"next_page":null}}}`, "fsn1"},
		{"hetzner", "network.floating_ip", "/v1/floating_ips", `{"floating_ips":[{"id":123,"name":"floating","type":"ipv4","ip":"192.0.2.1","server":456,"home_location":{"name":"fsn1"}}],"meta":{"pagination":{"next_page":null}}}`, "fsn1"},
		{"hetzner", "compute.placement_group", "/v1/placement_groups", `{"placement_groups":[{"id":123,"name":"spread","type":"spread","servers":[456]}],"meta":{"pagination":{"next_page":null}}}`, "global"},

		{"aws", "storage.snapshot", "DescribeSnapshots", `<DescribeSnapshotsResponse><snapshotSet><item><snapshotId>snap-123</snapshotId><volumeId>vol-123</volumeId><volumeSize>40</volumeSize><status>completed</status></item></snapshotSet></DescribeSnapshotsResponse>`, "us-east-1"},
		{"digitalocean", "storage.snapshot", "/v2/snapshots", `{"snapshots":[{"id":"123","name":"saved","resource_id":"vol-123","resource_type":"volume","regions":["nyc3"],"size_gigabytes":3,"min_disk_size":40}]}`, "nyc3"},
		{"digitalocean", "storage.backup", "/v2/images", `{"images":[{"id":123,"name":"backup","type":"backup","status":"available","regions":["nyc3"]},{"id":124,"name":"exclude","type":"snapshot","regions":["nyc3"]}]}`, "nyc3"},
		{"hetzner", "storage.snapshot", "/v1/images", `{"images":[{"id":123,"description":"saved","type":"snapshot","status":"available"},{"id":124,"type":"system","status":"available"}],"meta":{"pagination":{"next_page":null}}}`, "global"},
		{"hetzner", "storage.backup", "/v1/images", `{"images":[{"id":123,"description":"backup","type":"backup","status":"available"}],"meta":{"pagination":{"next_page":null}}}`, "global"},
		{"aws", "network.network", "DescribeVpcs", `<DescribeVpcsResponse><vpcSet><item><vpcId>vpc-123</vpcId><state>available</state><cidrBlock>10.0.0.0/16</cidrBlock></item></vpcSet></DescribeVpcsResponse>`, "us-east-1"},
		{"aws", "network.firewall", "DescribeSecurityGroups", `<DescribeSecurityGroupsResponse><securityGroupInfo><item><groupId>sg-123</groupId><groupName>web</groupName></item></securityGroupInfo></DescribeSecurityGroupsResponse>`, "us-east-1"},
		{"aws", "storage.volume", "DescribeVolumes", `<DescribeVolumesResponse><volumeSet><item><volumeId>vol-123</volumeId><size>10</size><status>available</status></item></volumeSet></DescribeVolumesResponse>`, "us-east-1"},
		{"digitalocean", "network.network", "/v2/vpcs", `{"vpcs":[{"id":"123","name":"web","region":"nyc3","ip_range":"10.0.0.0/16"}]}`, "nyc3"},
		{"digitalocean", "network.firewall", "/v2/firewalls", `{"firewalls":[{"id":"123","name":"web","status":"succeeded"}]}`, "global"},
		{"digitalocean", "storage.volume", "/v2/volumes", `{"volumes":[{"id":"123","name":"web","region":{"slug":"nyc3"},"size_gigabytes":10}]}`, "nyc3"},
		{"digitalocean", "network.load_balancer", "/v2/load_balancers", `{"load_balancers":[{"id":"123","name":"web","region":{"slug":"nyc3"},"status":"active","ip":"192.0.2.1"}]}`, "nyc3"},
		{"hetzner", "network.network", "/v1/networks", `{"networks":[{"id":123,"name":"web","ip_range":"10.0.0.0/16"}],"meta":{"pagination":{"next_page":null}}}`, "global"},
		{"hetzner", "network.firewall", "/v1/firewalls", `{"firewalls":[{"id":123,"name":"web"}],"meta":{"pagination":{"next_page":null}}}`, "global"},
		{"hetzner", "storage.volume", "/v1/volumes", `{"volumes":[{"id":123,"name":"web","size":10,"status":"available","location":{"name":"fsn1"}}],"meta":{"pagination":{"next_page":null}}}`, "fsn1"},
		{"hetzner", "network.load_balancer", "/v1/load_balancers", `{"load_balancers":[{"id":123,"name":"web","location":{"name":"fsn1"},"public_net":{"ipv4":{"ip":"192.0.2.1"}}}],"meta":{"pagination":{"next_page":null}}}`, "fsn1"},
		{"aws", "compute.image", "DescribeImages", `<DescribeImagesResponse><imagesSet><item><imageId>ami-123</imageId><name>image</name><imageState>available</imageState><architecture>x86_64</architecture></item></imagesSet></DescribeImagesResponse>`, "us-east-1"},
		{"aws", "access.ssh_key", "DescribeKeyPairs", `<DescribeKeyPairsResponse><keySet><item><keyPairId>key-123</keyPairId><keyName>ssh-key</keyName><keyFingerprint>fingerprint</keyFingerprint></item></keySet></DescribeKeyPairsResponse>`, "us-east-1"},
		{"aws", "compute.type", "DescribeInstanceTypes", `<DescribeInstanceTypesResponse><instanceTypeSet><item><instanceType>t3.micro</instanceType><vCpuInfo><defaultVCpus>2</defaultVCpus></vCpuInfo><memoryInfo><sizeInMiB>1024</sizeInMiB></memoryInfo></item></instanceTypeSet></DescribeInstanceTypesResponse>`, "us-east-1"},
		{"digitalocean", "compute.image", "/v2/images", `{"images":[{"id":123,"name":"image","distribution":"Ubuntu","status":"available","regions":["nyc3"]}]}`, "nyc3"},
		{"digitalocean", "access.ssh_key", "/v2/account/keys", `{"ssh_keys":[{"id":123,"name":"ssh-key","fingerprint":"fingerprint","public_key":"omitted-public-key"}]}`, "global"},
		{"digitalocean", "compute.type", "/v2/sizes", `{"sizes":[{"slug":"s-1vcpu-1gb","vcpus":1,"memory":1024,"disk":25,"available":true,"regions":["nyc3"]}]}`, "nyc3"},
		{"hetzner", "compute.image", "/v1/images", `{"images":[{"id":123,"name":"image","status":"available","type":"system","architecture":"x86","os_flavor":"ubuntu"}],"meta":{"pagination":{"next_page":null}}}`, "global"},
		{"hetzner", "access.ssh_key", "/v1/ssh_keys", `{"ssh_keys":[{"id":123,"name":"ssh-key","fingerprint":"fingerprint","public_key":"omitted-public-key"}],"meta":{"pagination":{"next_page":null}}}`, "global"},
		{"hetzner", "compute.type", "/v1/server_types", `{"server_types":[{"id":123,"name":"cx23","cores":2,"memory":4,"disk":40,"architecture":"x86","locations":[{"id":1,"name":"fsn1","available":true}]}],"meta":{"pagination":{"next_page":null}}}`, "fsn1"},
	}
	for _, tc := range cases {
		t.Run(tc.cloud+tc.kind, func(t *testing.T) {
			ownTags := tc.kind != "compute.type" && (tc.cloud != "digitalocean" || tc.kind == "database.cluster" || tc.kind == "kubernetes.cluster" || tc.kind == "storage.volume" || tc.kind == "compute.image" || tc.kind == "storage.backup")
			if tc.cloud == "aws" && ownTags {
				tc.body = strings.Replace(tc.body, "<item>", "<item><tagSet><item><key>env</key><value>production</value></item></tagSet>", 1)
			} else if tc.cloud != "aws" {
				metadata := `"tags":["env=production"],`
				if tc.cloud == "hetzner" {
					metadata = `"labels":{"env":"production"},`
				}
				tc.body = strings.Replace(tc.body, "[{", "[{"+metadata, 1)
			}
			calls := 0
			client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				calls++
				if r.Header.Get("Authorization") == "" {
					t.Fatal("missing SDK authorization")
				}
				ct := "application/json"
				if tc.cloud == "aws" {
					b, _ := io.ReadAll(r.Body)
					if (tc.kind == "compute.image" || tc.kind == "storage.snapshot") && !strings.Contains(string(b), "Owner.1=self") {
						t.Fatal("AWS image discovery was not restricted to owned images")
					}
					if !strings.Contains(string(b), "Action="+tc.path) {
						t.Fatal("wrong EC2 action")
					}
					ct = "text/xml"
				} else if r.URL.Path != tc.path {
					t.Fatalf("wrong path: %s", r.URL.Path)
				}
				if tc.cloud == "digitalocean" && tc.kind == "storage.backup" && r.URL.Query().Get("private") != "true" {
					t.Fatal("backup discovery was not private")
				}
				if tc.cloud == "hetzner" && strings.HasPrefix(tc.kind, "storage.") && tc.kind != "storage.volume" && r.URL.Query().Get("type") != strings.TrimPrefix(tc.kind, "storage.") {
					t.Fatal("missing image type filter")
				}
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{ct}}, Body: io.NopCloser(strings.NewReader(tc.body)), Request: r}, nil
			})}
			credential := "test-token"
			region := ""
			if tc.cloud == "aws" {
				credential = `{"access_key_id":"key","secret_access_key":"secret"}`
				region = "us-east-1"
			}
			req := provider.Request{Version: provider.Protocol, OrganizationID: strings.Repeat("a", 64), ConnectionID: strings.Repeat("b", 64), Provider: tc.cloud, Credential: credential, Region: region, InventoryKinds: []string{tc.kind}}
			out := Discover(context.Background(), req, client)
			if out.Validate() != nil || len(out.Resources) != 1 || calls != 1 {
				t.Fatalf("invalid discovery: %+v", out)
			}
			if out.Resources[0].Kind != tc.kind || out.Resources[0].Region != tc.region {
				t.Fatalf("incorrect mapping: %+v", out.Resources[0])
			}

			tags := out.Resources[0].Tags
			if ownTags {
				if tags == nil {
					t.Fatal("resource tags missing")
				}
				if tc.cloud == "digitalocean" {
					if len(tags.Names) != 1 || tags.Names[0] != "env=production" {
						t.Fatalf("named tags lost: %+v", tags)
					}
				} else if tags.Labels["env"] != "production" {
					t.Fatalf("labels lost: %+v", tags)
				}
			} else if tags != nil {
				t.Fatal("unsupported or selector tags became resource tags")
			}

			if tc.kind == "network.ip" || tc.kind == "network.primary_ip" || tc.kind == "network.floating_ip" {
				if out.Resources[0].PublicIP != "192.0.2.1" || out.Resources[0].Status != "assigned" {
					t.Fatalf("incorrect IP assignment: %+v", out.Resources[0])
				}
			}
			if tc.cloud != "aws" && tc.region != "global" {
				filtered := req
				filtered.Region = "other-region"
				result := Discover(context.Background(), filtered, client)
				if result.Validate() != nil || len(result.Resources) != 0 {
					t.Fatalf("region filtering failed: %+v", result)
				}
			}
			encoded, _ := json.Marshal(out)
			if strings.Contains(string(encoded), "omitted-public-key") || strings.Contains(string(encoded), "omitted-sensitive") {
				t.Fatal("catalog returned SSH key material")
			}
			if tc.cloud != "aws" && (tc.kind == "storage.snapshot" || tc.kind == "storage.backup") {
				malformed := strings.ReplaceAll(tc.body, `"type":"backup"`, `"type":""`)
				malformed = strings.ReplaceAll(malformed, `"type":"snapshot"`, `"type":""`)
				if tc.cloud == "digitalocean" && tc.kind == "storage.snapshot" {
					malformed = strings.ReplaceAll(malformed, `"regions":["nyc3"]`, `"regions":[]`)
				}
				client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
					return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(malformed)), Request: r}, nil
				})
				bad := Discover(context.Background(), req, client)
				if bad.Complete || len(bad.Resources) > 0 || bad.Error == "" {
					t.Fatal("malformed recovery metadata accepted")
				}
			}

			if tc.kind == "network.ip" || tc.kind == "network.primary_ip" || tc.kind == "network.floating_ip" {
				unassigned := strings.NewReplacer("<associationId>eipassoc-123</associationId>", "", `"droplet":{"id":123}`, `"droplet":null`, `"assignee_id":456`, `"assignee_id":0`, `"server":456`, `"server":null`).Replace(tc.body)
				body := unassigned
				client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
					return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
				})
				result := Discover(context.Background(), req, client)
				if result.Validate() != nil || len(result.Resources) != 1 || result.Resources[0].Status != "unassigned" {
					t.Fatalf("unassigned mapping: %+v", result)
				}
				body = strings.ReplaceAll(tc.body, "192.0.2.1", "invalid")
				result = Discover(context.Background(), req, client)
				if result.Complete || len(result.Resources) > 0 || result.Error == "" {
					t.Fatal("invalid address published")
				}
			}

			if tc.kind == "database.cluster" || tc.kind == "kubernetes.cluster" || tc.kind == "network.certificate" {
				malformed := strings.NewReplacer(`"engine":"pg"`, `"engine":""`, `"status":{"state":"running","message":"omitted-sensitive-status"}`, `"status":null`, "2030-01-01T12:00:00Z", "invalid-time").Replace(tc.body)
				client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
					return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(malformed)), Request: r}, nil
				})
				bad := Discover(context.Background(), req, client)
				if bad.Complete || len(bad.Resources) > 0 || bad.Error == "" {
					t.Fatal("malformed service metadata accepted")
				}
			}
			// An unsuccessful scope cannot publish an authoritative empty result.
			client.Transport = roundTripFunc(func(r *http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: 403, Header: http.Header{}, Body: io.NopCloser(strings.NewReader("sensitive")), Request: r}, nil
			})
			out = Discover(context.Background(), req, client)
			if out.Complete || len(out.Resources) > 0 || out.Error == "" {
				t.Fatal("failed scan accepted")
			}
		})
	}
}

func TestDNSRecordPagination(t *testing.T) {
	for _, cloud := range []string{"digitalocean", "hetzner"} {
		t.Run(cloud, func(t *testing.T) {
			fail := false
			client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				body := ""
				status := 200
				second := r.URL.Query().Get("page") == "2"
				switch r.URL.Path {
				case "/v2/domains":
					body = `{"domains":[{"name":"example.test","ttl":3600},{"name":"other.test","ttl":1800}]}`
				case "/v2/domains/example.test/records":
					if second {
						body = `{"domain_records":[{"id":8,"name":"www","type":"A","ttl":300,"data":"192.0.2.1"}]}`
					} else {
						body = `{"domain_records":[{"id":7,"name":"@","type":"TXT","ttl":3600,"data":"omitted-sensitive-record"}],"links":{"pages":{"next":"https://api.digitalocean.com/v2/domains/example.test/records?page=2"}}}`
					}
				case "/v2/domains/other.test/records":
					body = `{"domain_records":[{"id":7,"name":"@","type":"TXT","ttl":1800,"data":"omitted-sensitive-record"}]}`
				case "/v1/zones":
					body = `{"zones":[{"id":123,"name":"example.test","ttl":3600,"mode":"primary","status":"ok"},{"id":124,"name":"other.test","ttl":1800,"mode":"primary","status":"ok"}],"meta":{"pagination":{"next_page":null}}}`
				case "/v1/zones/123/rrsets":
					if second {
						body = `{"rrsets":[{"name":"www","type":"A","ttl":300,"records":[{"value":"192.0.2.1"}]}],"meta":{"pagination":{"next_page":null}}}`
					} else {
						body = `{"rrsets":[{"name":"@","type":"TXT","records":[{"value":"omitted-sensitive-record"}]}],"meta":{"pagination":{"next_page":2}}}`
					}
				case "/v1/zones/124/rrsets":
					body = `{"rrsets":[{"name":"@","type":"TXT","records":[{"value":"omitted-sensitive-record"}]}],"meta":{"pagination":{"next_page":null}}}`
				default:
					t.Fatalf("unexpected DNS API path %s", r.URL.Path)
				}
				if fail && second {
					status = 403
					body = `{"message":"omitted-sensitive-error"}`
				}
				return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
			})}
			req := provider.Request{Version: provider.Protocol, OrganizationID: strings.Repeat("a", 64), ConnectionID: strings.Repeat("b", 64), Provider: cloud, Credential: "fake-token", InventoryKinds: []string{"dns.record"}}
			out := Discover(context.Background(), req, client)
			if out.Validate() != nil || len(out.Resources) != 3 {
				t.Fatalf("invalid DNS discovery: %+v", out)
			}
			raw, _ := json.Marshal(out)
			if strings.Contains(string(raw), "omitted-sensitive") || !strings.Contains(string(raw), "TTL 3600") || !strings.Contains(string(raw), "TTL 1800") {
				t.Fatal("DNS metadata mapping failed")
			}
			fail = true
			out = Discover(context.Background(), req, client)
			if out.Complete || len(out.Resources) > 0 || out.Error == "" {
				t.Fatal("partial DNS scan accepted")
			}
		})
	}
}
