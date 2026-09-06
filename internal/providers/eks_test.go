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

func TestEKSInventory(t *testing.T) {
	for _, kind := range []string{"kubernetes.cluster", "kubernetes.node_group"} {
		for _, mode := range []string{"complete", "empty", "denied", "wrong-identity", "missing-status", "cycle", "node-cycle", "negative-capacity"} {
			t.Run(kind+"/"+mode, func(t *testing.T) {
				calls := 0
				client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
					calls++
					if calls > 20 {
						t.Fatal("unbounded EKS paging")
					}
					if r.Method != "GET" || r.URL.Host != "eks.us-east-1.amazonaws.com" || !strings.Contains(r.Header.Get("Authorization"), "/us-east-1/eks/aws4_request") {
						t.Fatal("wrong EKS endpoint or unsigned read")
					}
					parts := strings.Split(strings.Trim(r.URL.Path, "/"), "/")
					body := map[string]any{}
					status := 200
					switch len(parts) {
					case 1:
						if r.URL.Query().Get("maxResults") != "100" || r.URL.Query().Get("include") != "" {
							t.Fatal("unbounded/external cluster list")
						}
						name := "production"
						if r.URL.Query().Get("nextToken") == "next" {
							name = "staging"
						} else {
							body["nextToken"] = "next"
						}
						body["clusters"] = []string{name}
						if mode == "empty" {
							body = map[string]any{"clusters": []string{}}
						}
						if mode == "cycle" {
							body = map[string]any{"clusters": []string{}, "nextToken": "next"}
						}
					case 2:
						name := parts[1]
						if mode == "wrong-identity" {
							name = "other"
						}
						v := map[string]any{"name": name, "status": "ACTIVE", "version": "1.33", "platformVersion": "eks.1", "tags": map[string]string{"env": "production"}, "endpoint": "private-endpoint", "certificateAuthority": map[string]string{"data": "private-certificate"}}
						if mode == "missing-status" {
							delete(v, "status")
						}
						body["cluster"] = v
						if mode == "denied" && parts[1] == "staging" {
							status = 403
						}
					case 3:
						if r.URL.Query().Get("maxResults") != "100" {
							t.Fatal("unbounded node group list")
						}
						name := "workers"
						if r.URL.Query().Get("nextToken") == "next" {
							name = "batch"
						} else {
							body["nextToken"] = "next"
						}
						body["nodegroups"] = []string{name}
						if mode == "node-cycle" {
							body = map[string]any{"nodegroups": []string{}, "nextToken": "next"}
						}
					case 4:
						name := parts[3]
						if mode == "wrong-identity" {
							name = "other"
						}
						desired := 2
						if mode == "negative-capacity" {
							desired = -1
						}
						v := map[string]any{"nodegroupName": name, "clusterName": parts[1], "status": "ACTIVE", "version": "1.33", "capacityType": "ON_DEMAND", "scalingConfig": map[string]int{"desiredSize": desired, "minSize": 1, "maxSize": 4}, "tags": map[string]string{"env": "production"}, "nodeRole": "private-role", "remoteAccess": map[string]string{"ec2SshKey": "private-key"}}
						if mode == "missing-status" {
							delete(v, "status")
						}
						body["nodegroup"] = v
						if mode == "denied" && parts[1] == "staging" {
							status = 403
						}
					default:
						t.Fatal("unexpected EKS path", r.URL.Path)
					}
					if status != 200 {
						body = map[string]any{"message": "private-error"}
					}
					data, _ := json.Marshal(body)
					return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": []string{"application/json"}}, Body: io.NopCloser(strings.NewReader(string(data))), Request: r}, nil
				})}
				r := provider.Request{Version: provider.Protocol, OrganizationID: strings.Repeat("a", 64), ConnectionID: strings.Repeat("b", 64), Provider: "aws", Region: "us-east-1", Credential: `{"access_key_id":"test","secret_access_key":"test"}`, InventoryKinds: []string{kind}}
				result := Discover(context.Background(), r, client)
				valid := mode == "complete" || mode == "empty" || (kind == "kubernetes.cluster" && (mode == "node-cycle" || mode == "negative-capacity"))
				if !valid {
					if result.Complete || result.Error == "" || len(result.Resources) != 0 {
						t.Fatal("partial EKS inventory published", result)
					}
					return
				}
				want := 2
				if kind == "kubernetes.node_group" {
					want = 4
				}
				if mode == "empty" {
					want = 0
				}
				if !result.Complete || result.Validate() != nil || len(result.Resources) != want {
					t.Fatal("incomplete EKS result", result)
				}
				for _, row := range result.Resources {
					if row.Tags == nil || row.Tags.Labels["env"] != "production" || row.Status != "active" || row.Region != "us-east-1" || !strings.Contains(row.Size, "1.33") {
						t.Fatal("metadata lost", row)
					}
					if kind == "kubernetes.node_group" && (!strings.Contains(row.NativeID, ":") || !strings.Contains(row.Size, "desired=2")) {
						t.Fatal("node scope/capacity lost", row)
					}
				}
				encoded, _ := json.Marshal(result)
				if strings.Contains(string(encoded), "private-") {
					t.Fatal("private SDK metadata exposed")
				}
			})
		}
	}
}
