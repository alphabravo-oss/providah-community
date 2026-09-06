package providers

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/alphabravo-oss/providah-community/sdk/provider"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestServerCreationSDKs(t *testing.T) {
	for _, scenario := range []string{"aws", "digitalocean", "hetzner", "digitalocean-network", "hetzner-network"} {
		t.Run(scenario, func(t *testing.T) {
			cloud := strings.TrimSuffix(scenario, "-network")
			op := strings.Repeat("c", 64)
			input := &provider.ServerCreate{Name: "new-server", Image: "11", Size: "cx23", SSHKey: "12"}
			region, credential, native, status := "fsn1", "test-token", "123", "running"
			fixtures := map[string]string{
				"/v1/images/11":    `{"image":{"id":11,"status":"available","architecture":"x86","disk_size":10}}`,
				"/v1/server_types": `{"server_types":[{"id":1,"name":"cx23","architecture":"x86","disk":40,"locations":[{"id":1,"name":"fsn1","available":true}]}]}`,
				"/v1/locations":    `{"locations":[{"id":1,"name":"fsn1"}]}`,
				"/v1/ssh_keys/12":  `{"ssh_key":{"id":12,"public_key":"never-return-key"}}`,
			}
			mutationPath := "/v1/servers"
			observed := `{"servers":[{"id":123,"status":"running","location":{"name":"fsn1"},"labels":{"providah-operation":"` + op[:32] + `","providah-request":"` + op[32:] + `"}}]}`
			created := `{"server":{"id":123},"action":{"id":5},"root_password":"never-return-password"}`
			if cloud == "digitalocean" {
				region, status, input.Size = "nyc3", "active", "s-1vcpu-1gb"
				fixtures = map[string]string{
					"/v2/images/11":       `{"image":{"id":11,"status":"available","regions":["nyc3"],"min_disk_size":10}}`,
					"/v2/account/keys/12": `{"ssh_key":{"id":12,"public_key":"never-return-key"}}`,
					"/v2/sizes":           `{"sizes":[{"slug":"s-1vcpu-1gb","available":true,"disk":25,"regions":["nyc3"]}]}`,
				}
				mutationPath = "/v2/droplets"
				created = `{"droplet":{"id":123}}`
				observed = `{"droplets":[{"id":123,"status":"active","region":{"slug":"nyc3"},"tags":["providah-operation-` + op + `"]}]}`
			}
			if cloud == "aws" {
				region, credential, native = "us-east-1", `{"access_key_id":"key","secret_access_key":"secret"}`, "i-12345678"
				input.Image, input.Size, input.SSHKey, input.Subnet, input.SecurityGroup = "ami-12345678", "t3.micro", "key-12345678", "subnet-12345678", "sg-12345678"
				fixtures = map[string]string{
					"DescribeImages":                `<DescribeImagesResponse><imagesSet><item><imageId>ami-12345678</imageId><imageState>available</imageState><architecture>x86_64</architecture><rootDeviceType>ebs</rootDeviceType><rootDeviceName>/dev/xvda</rootDeviceName></item></imagesSet></DescribeImagesResponse>`,
					"DescribeInstanceTypes":         `<DescribeInstanceTypesResponse><instanceTypeSet><item><instanceType>t3.micro</instanceType><processorInfo><supportedArchitectures><item>x86_64</item></supportedArchitectures></processorInfo></item></instanceTypeSet></DescribeInstanceTypesResponse>`,
					"DescribeKeyPairs":              `<DescribeKeyPairsResponse><keySet><item><keyPairId>key-12345678</keyPairId><keyName>test</keyName></item></keySet></DescribeKeyPairsResponse>`,
					"DescribeSubnets":               `<DescribeSubnetsResponse><subnetSet><item><subnetId>subnet-12345678</subnetId><vpcId>vpc-123</vpcId><state>available</state><availableIpAddressCount>5</availableIpAddressCount><availabilityZone>us-east-1a</availabilityZone></item></subnetSet></DescribeSubnetsResponse>`,
					"DescribeSecurityGroups":        `<DescribeSecurityGroupsResponse><securityGroupInfo><item><groupId>sg-12345678</groupId><vpcId>vpc-123</vpcId></item></securityGroupInfo></DescribeSecurityGroupsResponse>`,
					"DescribeInstanceTypeOfferings": `<DescribeInstanceTypeOfferingsResponse><instanceTypeOfferingSet><item><instanceType>t3.micro</instanceType><location>us-east-1a</location></item></instanceTypeOfferingSet></DescribeInstanceTypeOfferingsResponse>`,
				}
				mutationPath = "RunInstances"
				created = `<RunInstancesResponse><instancesSet><item><instanceId>i-12345678</instanceId></item></instancesSet></RunInstancesResponse>`
				observed = `<DescribeInstancesResponse><reservationSet><item><instancesSet><item><instanceId>i-12345678</instanceId><tagSet><item><key>providah-operation</key><value>` + op + `</value></item></tagSet><instanceState><name>running</name></instanceState></item></instancesSet></item></reservationSet></DescribeInstancesResponse>`
			}
			networkPath := ""
			if strings.HasSuffix(scenario, "-network") {
				if cloud == "digitalocean" {
					input.Network = "11111111-2222-3333-4444-555555555555"
					networkPath = "/v2/vpcs/" + input.Network
					fixtures[networkPath] = `{"vpc":{"id":"` + input.Network + `","region":"nyc3"}}`
					observed = strings.Replace(observed, `"status":"active"`, `"status":"active","vpc_uuid":"`+input.Network+`"`, 1)
				} else {
					input.Network = "44"
					networkPath = "/v1/networks/44"
					fixtures["/v1/locations"] = `{"locations":[{"id":1,"name":"fsn1","network_zone":"eu-central"}]}`
					fixtures[networkPath] = `{"network":{"id":44,"ip_range":"10.0.0.0/16","subnets":[{"type":"cloud","network_zone":"eu-central","ip_range":"10.0.1.0/24"}]}}`
					observed = strings.Replace(observed, `"status":"running"`, `"status":"running","private_net":[{"network":44,"ip":"10.0.1.2"}]`, 1)
				}
			}
			req := provider.Request{Version: provider.Protocol, OrganizationID: strings.Repeat("a", 64), ConnectionID: strings.Repeat("b", 64), Provider: cloud, Region: region, Credential: credential, Power: &provider.PowerRequest{OperationID: op, Phase: "submit", Action: "create", Create: input}}
			mutations := 0
			drop, failRead, missing := false, false, false
			client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				path := r.URL.Path
				content := "application/json"
				var body []byte
				if r.Body != nil {
					body, _ = io.ReadAll(r.Body)
					r.Body = io.NopCloser(strings.NewReader(string(body)))
				}
				if cloud == "aws" {
					if e := r.ParseForm(); e != nil {
						t.Fatal(e)
					}
					path = r.Form.Get("Action")
					if path == "DescribeImages" && (r.Form.Get("Owner.1") != "self" || r.Form.Get("Owner.2") != "amazon" || r.Form.Get("ImageId.1") != input.Image) {
						t.Fatal("image publisher boundary or reviewed AMI lost")
					}
					content = "text/xml"
				}
				mutate := path == mutationPath && r.Method == "POST"
				response := fixtures[path]
				if mutate {
					mutations++
					if req.Power.Phase != "submit" {
						t.Fatal("observation mutated provider")
					}
					if cloud == "aws" {
						for key, want := range map[string]string{"ClientToken": op, "MinCount": "1", "MaxCount": "1", "MetadataOptions.HttpTokens": "required", "NetworkInterface.1.AssociatePublicIpAddress": "false", "BlockDeviceMapping.1.Ebs.Encrypted": "true", "ImageId": input.Image, "KeyName": "test"} {
							if r.Form.Get(key) != want {
								t.Fatalf("%s=%s expected %s", key, r.Form.Get(key), want)
							}
						}
					} else {
						var values map[string]any
						if json.Unmarshal(body, &values) != nil {
							t.Fatal("invalid JSON")
						}
						if input.Network != "" {
							if cloud == "digitalocean" && values["vpc_uuid"] != input.Network {
								t.Fatal("VPC not sent")
							}
							if cloud == "hetzner" {
								nets, ok := values["networks"].([]any)
								if !ok || len(nets) != 1 || nets[0] != float64(44) {
									t.Fatal("network not sent", values)
								}
							}
						}
						if values["name"] != input.Name || !strings.Contains(string(body), "ssh_keys") || strings.Contains(string(body), "user_data") {
							t.Fatalf("wrong creation input %s", body)
						}
						if cloud == "digitalocean" && !strings.Contains(string(body), "providah-operation-"+op) {
							t.Fatal("missing operation tag")
						}
						if cloud == "hetzner" && !strings.Contains(string(body), op[:32]) {
							t.Fatal("missing operation labels")
						}
					}
					if drop {
						return nil, errors.New("lost response")
					}
					response = created
				} else if req.Power.Phase == "observe" {
					if cloud == "aws" && r.Form.Get("Filter.1.Value.1") != op {
						t.Fatal("missing marker filter")
					}
					if cloud == "digitalocean" && r.URL.Query().Get("tag_name") != "providah-operation-"+op {
						t.Fatal("missing tag filter")
					}
					if cloud == "hetzner" && !strings.Contains(r.URL.Query().Get("label_selector"), op[:32]) {
						t.Fatal("missing label filter")
					}
					response = observed
					if missing {
						if cloud == "aws" {
							response = `<DescribeInstancesResponse/>`
						} else {
							response = `{}`
						}
					}
				} else if failRead {
					return nil, errors.New("preflight denied")
				}
				if response == "" {
					t.Fatalf("unexpected SDK request %s", path)
				}
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{content}}, Body: io.NopCloser(strings.NewReader(response)), Request: r}, nil
			})}
			result := Power(context.Background(), req, client)
			if result.Validate() != nil || result.Power.Outcome != "accepted" || result.Power.NativeID != native || mutations != 1 {
				t.Fatalf("create failed: %+v mutations=%d", result.Power, mutations)
			}
			serialized, _ := json.Marshal(result)
			if strings.Contains(string(serialized), "never-return") {
				t.Fatal("creation leaked key/password")
			}
			req.Power.Phase = "observe"
			req.Power.NativeID = native
			result = Power(context.Background(), req, client)
			if result.Power.Outcome != "succeeded" || result.Power.Status != status || mutations != 1 {
				t.Fatalf("observe failed: %+v", result.Power)
			}
			req.Power.Phase = "submit"
			req.Power.NativeID = ""
			drop = true
			result = Power(context.Background(), req, client)
			if result.Power.Outcome != "uncertain" || mutations != 2 {
				t.Fatalf("lost response retried: %+v %d", result.Power, mutations)
			}
			req.Power.Phase = "observe"
			result = Power(context.Background(), req, client)
			if result.Power.Outcome != "succeeded" || result.Power.NativeID != native || mutations != 2 {
				t.Fatal("marker reconciliation failed")
			}
			missing = true
			result = Power(context.Background(), req, client)
			if result.Power.Outcome != "uncertain" || mutations != 2 {
				t.Fatal("absence proves neither creation nor failure")
			}
			req.Power.Phase = "submit"
			failRead = true
			result = Power(context.Background(), req, client)
			if result.Power.Outcome != "failed" || mutations != 2 {
				t.Fatal("failed preflight submitted mutation")
			}
			if input.Network != "" {
				failRead = false
				drop = false
				missing = false
				fixtures[networkPath] = strings.ReplaceAll(fixtures[networkPath], map[string]string{"digitalocean": "nyc3", "hetzner": "eu-central"}[cloud], "wrong-region")
				result = Power(context.Background(), req, client)
				if result.Power.Outcome != "failed" || mutations != 2 {
					t.Fatal("wrong-region network submitted")
				}
				req.Power.Phase = "observe"
				if cloud == "digitalocean" {
					observed = strings.ReplaceAll(observed, input.Network, "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee")
				} else {
					observed = strings.ReplaceAll(observed, `"network":44`, `"network":45`)
				}
				result = Power(context.Background(), req, client)
				if result.Power.Outcome == "succeeded" {
					t.Fatal("missing network attachment claimed success")
				}
			}
		})
	}
}
