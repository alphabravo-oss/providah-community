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

func TestRDSDiscovery(t *testing.T) {
	cases := []struct{ kind, plural, singular, id, status, extra string }{
		{"database.instance", "DBInstances", "DBInstance", "DBInstanceIdentifier", "DBInstanceStatus", "<DBInstanceClass>db.t4g.small</DBInstanceClass>"},
		{"database.cluster", "DBClusters", "DBCluster", "DBClusterIdentifier", "Status", "<EngineMode>provisioned</EngineMode>"},
		{"database.snapshot", "DBSnapshots", "DBSnapshot", "DBSnapshotIdentifier", "Status", "<SnapshotType>manual</SnapshotType>"},
		{"database.cluster_snapshot", "DBClusterSnapshots", "DBClusterSnapshot", "DBClusterSnapshotIdentifier", "Status", "<SnapshotType>automated</SnapshotType>"},
	}
	kinds := []string{}
	for _, tc := range cases {
		kinds = append(kinds, tc.kind)
	}
	for _, mode := range []string{"complete", "empty", "unknown storage", "later failure", "repeated marker", "missing id", "missing status", "missing engine", "duplicate identity", "negative storage"} {
		t.Run(mode, func(t *testing.T) {
			calls := map[string]int{}
			client := &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				if req.Method != "POST" || req.URL.Host != "rds.us-east-1.amazonaws.com" || !strings.Contains(req.Header.Get("Authorization"), "/us-east-1/rds/aws4_request") {
					t.Fatal("unexpected unsigned or non-RDS request")
				}
				if e := req.ParseForm(); e != nil {
					t.Fatal(e)
				}
				action := req.Form.Get("Action")
				if req.Form.Get("MaxRecords") != "100" || req.Form.Get("Version") != "2014-10-31" {
					t.Fatal("unbounded or wrong API request")
				}
				for _, tc := range cases {
					if action != "Describe"+tc.plural {
						continue
					}
					calls[action]++
					if calls[action] > 3 {
						t.Fatal("page cycle was not bounded")
					}
					if strings.Contains(tc.kind, "snapshot") && (req.Form.Get("IncludePublic") != "false" || req.Form.Get("IncludeShared") != "false" || req.Form.Get("SnapshotType") != "") {
						t.Fatal("snapshot ownership/default types changed")
					}
					id, tail := "database-one", "<Marker>next-page</Marker>"
					if calls[action] > 1 {
						if req.Form.Get("Marker") != "next-page" {
							t.Fatal("lost page marker")
						}
						id, tail = "database-two", ""
						if mode == "duplicate identity" {
							id = "database-one"
						}
						if mode == "repeated marker" {
							tail = "<Marker>next-page</Marker>"
						}
						if mode == "later failure" && tc.kind == "database.cluster_snapshot" {
							return &http.Response{StatusCode: 403, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(`<ErrorResponse><Error><Code>AccessDenied</Code><Message>omitted-sensitive-error</Message></Error></ErrorResponse>`)), Request: req}, nil
						}
					} else if req.Form.Get("Marker") != "" {
						t.Fatal("first page received a marker")
					}
					fields := fmt.Sprintf("<%s>%s</%s><%s>available</%s><Engine>postgres</Engine><EngineVersion>17.4</EngineVersion><AllocatedStorage>40</AllocatedStorage>", tc.id, id, tc.id, tc.status, tc.status) + tc.extra
					if mode == "missing id" {
						fields = strings.ReplaceAll(fields, fmt.Sprintf("<%s>%s</%s>", tc.id, id, tc.id), "")
					}
					if mode == "missing status" {
						fields = strings.ReplaceAll(fields, fmt.Sprintf("<%s>available</%s>", tc.status, tc.status), "")
					}
					if mode == "missing engine" {
						fields = strings.ReplaceAll(fields, "<Engine>postgres</Engine>", "")
					}
					if mode == "unknown storage" {
						fields = strings.ReplaceAll(fields, "<AllocatedStorage>40</AllocatedStorage>", "")
					}
					if mode == "negative storage" {
						fields = strings.ReplaceAll(fields, "<AllocatedStorage>40</AllocatedStorage>", "<AllocatedStorage>-1</AllocatedStorage>")
					}
					fields += `<DbiResourceId>db-IMMUTABLE</DbiResourceId><DbClusterResourceId>cluster-IMMUTABLE</DbClusterResourceId><MasterUsername>omitted-sensitive-user</MasterUsername><MasterUserSecret><SecretArn>omitted-sensitive-secret</SecretArn></MasterUserSecret><KmsKeyId>omitted-sensitive-key</KmsKeyId><TagList><Tag><Key>env</Key><Value>production</Value></Tag></TagList>`
					item := fmt.Sprintf("<%s>%s</%s>", tc.singular, fields, tc.singular)
					if mode == "empty" {
						item, tail = "", ""
					}
					body := fmt.Sprintf(`<%sResponse xmlns="http://rds.amazonaws.com/doc/2014-10-31/"><%sResult><%s>%s</%s>%s</%sResult></%sResponse>`, action, action, tc.plural, item, tc.plural, tail, action, action)
					return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/xml"}}, Body: io.NopCloser(strings.NewReader(body)), Request: req}, nil
				}
				t.Fatalf("unexpected action %s", action)
				return nil, nil
			})}
			request := provider.Request{Version: provider.Protocol, OrganizationID: strings.Repeat("a", 64), ConnectionID: strings.Repeat("b", 64), Provider: "aws", Credential: `{"access_key_id":"test","secret_access_key":"test"}`, Region: "us-east-1", InventoryKinds: kinds}
			response := Discover(context.Background(), request, client)
			success := mode == "complete" || mode == "empty" || mode == "unknown storage"
			if !success {
				if response.Complete || response.Error == "" || len(response.Resources) != 0 {
					t.Fatalf("partial or invalid snapshot published: %+v", response)
				}
				return
			}
			want := 8
			if mode == "empty" {
				want = 0
			}
			if response.Validate() != nil || !response.Complete || len(response.Resources) != want || len(calls) != 4 {
				t.Fatalf("incomplete database discovery: %+v calls=%v", response, calls)
			}
			for _, row := range response.Resources {
				if row.Tags == nil || row.Tags.Labels["env"] != "production" {
					t.Fatal("database tags missing")
				}
				if row.Region != "us-east-1" || row.Status != "available" || !strings.Contains(row.Size, "postgres 17.4") {
					t.Fatalf("lost database metadata: %+v", row)
				}
				if (mode == "unknown storage") == strings.Contains(row.Size, "GiB") {
					t.Fatal("unknown storage was invented or known storage lost")
				}
				for _, action := range []string{"restart", "resize", "delete"} {
					if provider.ResourceActionAllowed(row.Kind, action, row.Status) && (!provider.DatabaseSnapshotKind(row.Kind) || action != "delete") {
						t.Fatal("database accepted an unrelated resource action")
					}
				}
			}
			encoded, _ := json.Marshal(response)
			if strings.Contains(string(encoded), "omitted-sensitive") {
				t.Fatal("SDK payload leaked into inventory")
			}
		})
	}
}
