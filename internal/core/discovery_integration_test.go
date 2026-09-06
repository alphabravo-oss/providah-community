//go:build integration

package core

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"

	"connectrpc.com/connect"
	pb "github.com/alphabravo-oss/providah-community/internal/gen/providah/v1"
	"github.com/alphabravo-oss/providah-community/internal/gen/providah/v1/providahv1connect"
	"github.com/alphabravo-oss/providah-community/sdk/provider"
)

func exerciseDiscovery(t *testing.T, s *Service, client providahv1connect.ConsoleServiceClient, ctx context.Context, org, other, id string) {
	t.Helper()
	check := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	result := provider.Response{Version: provider.Protocol, Complete: true, Resources: []provider.Resource{{Tags: &provider.Tags{Labels: map[string]string{"env": "production", "empty": ""}, Names: []string{"named:production"}}, NativeID: "123", Name: "web", Region: "fsn1", Status: "running", PublicIP: "192.0.2.1", Size: "cx23"}}}
	s.cfg.ProviderCall = func(_ context.Context, r provider.Request) (provider.Response, error) {
		if r.OrganizationID != org || r.ConnectionID != id || r.Credential != "hetzner-test-secret-never-return" {
			t.Fatal("wrong credential scope")
		}
		return result, nil
	}
	defer func() { s.cfg.ProviderCall = nil }()
	_, err := client.SetConnectionEnabled(ctx, connect.NewRequest(&pb.SetConnectionEnabledRequest{OrganizationId: org, Id: id, Enabled: true}))
	check(err)
	queue := func() string {
		r, err := client.RefreshConnection(ctx, connect.NewRequest(&pb.RefreshConnectionRequest{OrganizationId: org, Id: id}))
		check(err)
		return r.Msg.JobId
	}
	run := func() { job, err := s.q.ClaimScan(ctx); check(err); check(s.runScan(ctx, job)) }
	count := func(want int) {
		r, err := client.ListResources(ctx, connect.NewRequest(&pb.ListResourcesRequest{OrganizationId: org}))
		check(err)
		if len(r.Msg.Resources) != want {
			t.Fatalf("inventory count: got %d want %d", len(r.Msg.Resources), want)
		}
	}
	jobID := queue()
	if queue() != jobID {
		t.Fatal("duplicate refresh created another job")
	}
	run()
	count(1)
	resources, err := client.ListResources(ctx, connect.NewRequest(&pb.ListResourcesRequest{OrganizationId: org}))
	check(err)
	resourceID := resources.Msg.Resources[0].Id
	if resources.Msg.Resources[0].Tags == nil || resources.Msg.Resources[0].Tags.Labels["env"] != "production" {
		t.Fatal("tags not persisted")
	}

	scopes, e := client.ListResourceScopes(ctx, connect.NewRequest(&pb.ListResourceScopesRequest{OrganizationId: org}))
	check(e)
	choices := map[string]string{}
	for _, scope := range scopes.Msg.Scopes {
		choices[scope.Kind+":"+scope.Value] = scope.Label
	}
	if choices["connection:"+id] != "Production" || choices["region:fsn1"] != "fsn1" || choices["status:running"] != "running" {
		t.Fatal("scope choices missing observed metadata")
	}
	_, err = client.ListResourceScopes(ctx, connect.NewRequest(&pb.ListResourceScopesRequest{OrganizationId: other}))
	if connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatal("scope choices leaked across organizations")
	}
	for _, tc := range []struct {
		connection, region, status string
		want                       int
	}{{id, "fsn1", "running", 1}, {id, "other", "running", 0}, {id, "fsn1", "off", 0}, {randomID(), "fsn1", "running", 0}} {
		listed, e := client.ListResources(ctx, connect.NewRequest(&pb.ListResourcesRequest{OrganizationId: org, ConnectionId: tc.connection, Region: tc.region, Status: tc.status}))
		check(e)
		summary, e := client.GetResourceSummary(ctx, connect.NewRequest(&pb.GetResourceSummaryRequest{OrganizationId: org, GroupBy: "status", Filters: &pb.InventoryViewSpec{ConnectionId: tc.connection, Region: tc.region, Status: tc.status}}))
		check(e)
		if len(listed.Msg.Resources) != tc.want || summary.Msg.Total != int64(tc.want) {
			t.Fatal("scope list/count mismatch")
		}
	}
	for _, tc := range []struct {
		key, value, name string
		exists           bool
		count            int
	}{{"env", "production", "", false, 1}, {"env", "Production", "", false, 0}, {"empty", "", "", false, 1}, {"missing", "", "", true, 0}, {"env", "", "", true, 1}, {"", "", "named:production", false, 1}, {"", "", "production", false, 0}} {
		filtered, e := client.ListResources(ctx, connect.NewRequest(&pb.ListResourcesRequest{OrganizationId: org, TagKey: tc.key, TagValue: tc.value, TagName: tc.name, TagExists: tc.exists}))
		check(e)
		summary, e := client.GetResourceSummary(ctx, connect.NewRequest(&pb.GetResourceSummaryRequest{OrganizationId: org, GroupBy: "provider", Filters: &pb.InventoryViewSpec{TagKey: tc.key, TagValue: tc.value, TagName: tc.name, TagExists: tc.exists}}))
		check(e)
		if summary.Msg.Total != int64(tc.count) {
			t.Fatal("summary and inventory diverged", tc)
		}
		if len(filtered.Msg.Resources) != tc.count {
			t.Fatal("incorrect tag filter", tc)
		}
	}
	if _, e := client.ListResources(ctx, connect.NewRequest(&pb.ListResourcesRequest{OrganizationId: org, TagValue: "without key"})); connect.CodeOf(e) != connect.CodeInvalidArgument {
		t.Fatal("invalid tag filter accepted")
	}

	for _, tc := range []struct {
		conditions []*pb.TagCondition
		any        bool
		want       int
	}{
		{[]*pb.TagCondition{{Key: "env", Value: "production"}, {Key: "empty", Exists: true}}, false, 1},
		{[]*pb.TagCondition{{Key: "env", Value: "production"}, {Key: "missing", Exists: true}}, false, 0},
		{[]*pb.TagCondition{{Key: "env", Value: "production"}, {Key: "missing", Exists: true}}, true, 1},
		{[]*pb.TagCondition{{Name: "named:production"}, {Key: "env", Value: "production"}}, false, 1},
		{[]*pb.TagCondition{{Name: "missing"}, {Key: "missing", Exists: true}}, true, 0},
	} {
		listed, e := client.ListResources(ctx, connect.NewRequest(&pb.ListResourcesRequest{OrganizationId: org, TagConditions: tc.conditions, TagMatchAny: tc.any}))
		check(e)
		summary, e := client.GetResourceSummary(ctx, connect.NewRequest(&pb.GetResourceSummaryRequest{OrganizationId: org, GroupBy: "status", Filters: &pb.InventoryViewSpec{TagConditions: tc.conditions, TagMatchAny: tc.any}}))
		check(e)
		if len(listed.Msg.Resources) != tc.want || summary.Msg.Total != int64(tc.want) {
			t.Fatal("combined tag matching diverged", tc)
		}
	}
	_, err = s.pool.Exec(ctx, "UPDATE resources SET tag_metadata=NULL WHERE id=$1", resourceID)
	check(err)
	unknown, e := client.ListResources(ctx, connect.NewRequest(&pb.ListResourcesRequest{OrganizationId: org, TagConditions: []*pb.TagCondition{{Key: "env", Exists: true}}}))
	check(e)
	if len(unknown.Msg.Resources) != 0 {
		t.Fatal("unknown tags matched combined filter")
	}
	_, err = s.pool.Exec(ctx, `UPDATE resources SET tag_metadata='{"labels":{"env":"production","empty":""},"names":["named:production"]}'::jsonb WHERE id=$1`, resourceID)
	check(err)
	_, err = client.ListResources(ctx, connect.NewRequest(&pb.ListResourcesRequest{OrganizationId: org, TagKey: "env", TagConditions: []*pb.TagCondition{{Key: "env", Exists: true}}}))
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatal("ambiguous mixed filter accepted")
	}

	_, err = s.pool.Exec(ctx, `INSERT INTO resources(id,org_id,connection_id,native_id,name,provider,kind,region,status,tag_metadata) SELECT repeat(md5('summary'||n),2),org_id,connection_id,'summary-'||n,name,provider,kind,region,status,tag_metadata FROM resources CROSS JOIN generate_series(1,201)n WHERE id=$1`, resourceID)
	check(err)
	for _, group := range []string{"provider", "kind", "status"} {
		summary, e := client.GetResourceSummary(ctx, connect.NewRequest(&pb.GetResourceSummaryRequest{OrganizationId: org, GroupBy: group, Filters: &pb.InventoryViewSpec{TagKey: "env", TagValue: "production"}}))
		check(e)
		if summary.Msg.Total != 202 || len(summary.Msg.Groups) != 1 || summary.Msg.Groups[0].OldestObservation == "" {
			t.Fatal("summary truncated at a page or lost freshness")
		}
	}
	_, err = s.pool.Exec(ctx, "DELETE FROM resources WHERE org_id=$1 AND native_id LIKE 'summary-%'", org)
	check(err)
	_, err = client.GetResourceSummary(ctx, connect.NewRequest(&pb.GetResourceSummaryRequest{OrganizationId: other, GroupBy: "status", Filters: &pb.InventoryViewSpec{}}))
	if connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatal("summary leaked across organizations")
	}
	_, err = client.GetResourceSummary(ctx, connect.NewRequest(&pb.GetResourceSummaryRequest{OrganizationId: org, GroupBy: "sql", Filters: &pb.InventoryViewSpec{}}))
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatal("invalid summary grouping accepted")
	}

	detail, err := client.GetResource(ctx, connect.NewRequest(&pb.GetResourceRequest{OrganizationId: org, Id: resourceID}))
	check(err)
	if detail.Msg.Resource.PublicIp != "192.0.2.1" || detail.Msg.ConnectionName != "Production" {
		t.Fatal("resource detail mapping failed")
	}
	_, err = client.GetResource(ctx, connect.NewRequest(&pb.GetResourceRequest{OrganizationId: other, Id: resourceID}))
	if connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("cross-tenant detail: %v", err)
	}
	// A partial scan never retires previously observed resources.
	s.cfg.ProviderCall = func(context.Context, provider.Request) (provider.Response, error) {
		return result, errors.New("sensitive provider error")
	}
	queue()
	run()
	count(1)
	connections, err := client.ListConnections(ctx, connect.NewRequest(&pb.ListConnectionsRequest{OrganizationId: org}))
	check(err)
	if connections.Msg.Connections[0].ScanStatus != pb.ScanStatus_SCAN_STATUS_FAILED || connections.Msg.Connections[0].LastScanAt == "" {
		t.Fatal("scan freshness/failure not retained")
	}
	// Credential rotation while a worker runs invalidates that worker's result.
	s.cfg.ProviderCall = func(context.Context, provider.Request) (provider.Response, error) {
		_, err := client.RotateCredential(ctx, connect.NewRequest(&pb.RotateCredentialRequest{OrganizationId: org, Id: id, Credential: "new-test-credential"}))
		check(err)
		return provider.Response{Version: provider.Protocol, Complete: true}, nil
	}
	queue()
	run()
	count(1)
	// An expired lease cannot publish even if the worker later returns successfully.
	s.cfg.ProviderCall = func(context.Context, provider.Request) (provider.Response, error) {
		return provider.Response{Version: provider.Protocol, Complete: true}, nil
	}
	queue()
	job, err := s.q.ClaimScan(ctx)
	check(err)
	_, err = s.pool.Exec(ctx, "UPDATE scan_jobs SET lease_until=now()-interval '1 second' WHERE id=$1", job.ID)
	check(err)
	check(s.finishScan(ctx, job, provider.Response{Version: provider.Protocol, Complete: true}, ""))
	count(1)
	check(s.discoveryTick(ctx))
	count(1)
	// Only a complete successful empty scan retires missing servers.
	queue()
	run()
	count(0)
	// Kinds form part of identity. A server-only rollback must retain other kinds.
	result = provider.Response{Version: provider.Protocol, Complete: true, InventoryKinds: []string{"compute.server", "storage.volume"}, Resources: []provider.Resource{
		{NativeID: "123", Region: "fsn1", Status: "running"},
		{Kind: "storage.volume", NativeID: "123", Region: "fsn1", Status: "available"},
	}}
	s.cfg.ProviderCall = func(_ context.Context, r provider.Request) (provider.Response, error) {
		if len(r.InventoryKinds) != len(provider.InventoryKinds("hetzner")) {
			t.Fatal("core did not request full inventory")
		}
		return result, nil
	}
	queue()
	run()
	count(2)
	volumes, e := client.ListResources(ctx, connect.NewRequest(&pb.ListResourcesRequest{OrganizationId: org, Kind: "storage.volume"}))
	check(e)
	if len(volumes.Msg.Resources) != 1 || volumes.Msg.Resources[0].Kind != "storage.volume" {
		t.Fatal("kind filter failed")
	}
	_, e = client.RequestOperation(ctx, connect.NewRequest(&pb.RequestOperationRequest{OrganizationId: org, ResourceId: volumes.Msg.Resources[0].Id, Action: "start", ExpectedStatus: "available", Reason: "Reject non-server power", IdempotencyKey: randomID()}))
	if e == nil {
		t.Fatal("volume accepted a server operation")
	}
	result = provider.Response{Version: provider.Protocol, Complete: true}
	queue()
	run()
	count(1)
	result.InventoryKinds = []string{"storage.volume"}
	queue()
	run()
	count(0)

	// Equal native IDs across resource kinds remain distinct; legacy scans preserve them.
	kinds := []string{"storage.snapshot", "storage.backup", "network.primary_ip", "network.floating_ip", "compute.placement_group", "network.certificate", "dns.zone", "dns.record"}
	result = provider.Response{Version: provider.Protocol, Complete: true, InventoryKinds: kinds}
	for _, kind := range kinds {
		result.Resources = append(result.Resources, provider.Resource{Kind: kind, NativeID: "456", Name: kind + "\n\"Ω", Region: "global", Status: "available"})
	}
	queue()
	run()
	count(len(kinds))
	baseline, e := client.ListResources(ctx, connect.NewRequest(&pb.ListResourcesRequest{OrganizationId: org}))
	check(e)
	for _, sortBy := range []string{"id", "name", "provider", "kind", "region", "status"} {
		value := func(r *pb.Resource) string {
			switch sortBy {
			case "name":
				return r.Name
			case "provider":
				return r.Provider
			case "kind":
				return r.Kind
			case "region":
				return r.Region
			case "status":
				return r.Status
			default:
				return r.Id
			}
		}
		for _, descending := range []bool{false, true} {
			expected := append([]*pb.Resource{}, baseline.Msg.Resources...)
			slices.SortFunc(expected, func(a, b *pb.Resource) int {
				order := strings.Compare(value(a), value(b))
				if order == 0 {
					order = strings.Compare(a.Id, b.Id)
				}
				if descending {
					return -order
				}
				return order
			})
			var got []*pb.Resource
			token := ""
			for page := 0; page < 10; page++ {
				part, e := client.ListResources(ctx, connect.NewRequest(&pb.ListResourcesRequest{OrganizationId: org, SortBy: sortBy, Descending: descending, PageSize: 2, PageToken: token}))
				check(e)
				got = append(got, part.Msg.Resources...)
				token = part.Msg.NextPageToken
				if token == "" {
					break
				}
				if page == 0 {
					_, e = client.ListResources(ctx, connect.NewRequest(&pb.ListResourcesRequest{OrganizationId: org, SortBy: sortBy, Descending: !descending, PageToken: token}))
					if connect.CodeOf(e) != connect.CodeInvalidArgument {
						t.Fatal("cursor accepted different sort direction")
					}
				}
			}
			if len(got) != len(expected) {
				t.Fatal("sorted pagination lost or duplicated resources")
			}
			for i := range got {
				if got[i].Id != expected[i].Id {
					t.Fatalf("incorrect %s descending=%v order", sortBy, descending)
				}
			}
		}
	}
	_, e = client.ListResources(ctx, connect.NewRequest(&pb.ListResourcesRequest{OrganizationId: org, SortBy: "unsupported"}))
	if connect.CodeOf(e) != connect.CodeInvalidArgument {
		t.Fatal("unsupported sort accepted")
	}
	for _, kind := range kinds {
		listed, e := client.ListResources(ctx, connect.NewRequest(&pb.ListResourcesRequest{OrganizationId: org, Kind: kind}))
		check(e)
		if len(listed.Msg.Resources) != 1 || listed.Msg.Resources[0].Kind != kind {
			t.Fatal("resource kind filtering failed")
		}
		_, e = client.RequestOperation(ctx, connect.NewRequest(&pb.RequestOperationRequest{OrganizationId: org, ResourceId: listed.Msg.Resources[0].Id, Action: "start", ExpectedStatus: "available", Reason: "Reject non-server power", IdempotencyKey: randomID()}))
		if e == nil {
			t.Fatal("non-server resource accepted power")
		}
	}
	result = provider.Response{Version: provider.Protocol, Complete: true}
	queue()
	run()
	count(len(kinds))
	result.InventoryKinds = kinds
	queue()
	run()
	count(0)
	originalCatalog := s.cfg.ProviderRuntimes
	s.cfg.ProviderRuntimes = []provider.Runtime{{Provider: "hetzner", Image: "sha256:" + strings.Repeat("a", 64), Version: "legacy", SDKVersion: "1", Protocol: provider.Protocol}}
	s.cfg.ProviderCall = func(_ context.Context, r provider.Request) (provider.Response, error) {
		raw, e := json.Marshal(r)
		check(e)
		if strings.Contains(string(raw), "inventory_kinds") {
			t.Fatal("legacy worker received an unknown request field")
		}
		return provider.Response{Version: provider.Protocol, Complete: true}, nil
	}
	queue()
	run()
	count(0)
	s.cfg.ProviderRuntimes = originalCatalog
	_, err = client.SetConnectionEnabled(ctx, connect.NewRequest(&pb.SetConnectionEnabledRequest{OrganizationId: org, Id: id, Enabled: false}))
	check(err)
	_, err = client.RefreshConnection(ctx, connect.NewRequest(&pb.RefreshConnectionRequest{OrganizationId: org, Id: id}))
	if connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("disabled connection queued: %v", err)
	}
}

func exerciseRDSInventory(t *testing.T, s *Service, client providahv1connect.ConsoleServiceClient, ctx context.Context, org, other string) {
	exerciseServiceInventory(t, s, client, ctx, org, other, "aws", []string{"storage.bucket", "compute.placement_group", "database.instance", "database.cluster", "database.snapshot", "database.cluster_snapshot", "network.route_table", "network.internet_gateway", "network.nat_gateway", "network.load_balancer", "kubernetes.cluster", "kubernetes.node_group"})
	exerciseServiceInventory(t, s, client, ctx, org, other, "digitalocean", []string{"organization.project", "application.app", "kubernetes.node_group"})
}

func exerciseServiceInventory(t *testing.T, s *Service, client providahv1connect.ConsoleServiceClient, ctx context.Context, org, other, cloud string, kinds []string) {
	t.Helper()
	check := func(e error) {
		t.Helper()
		if e != nil {
			t.Fatal(e)
		}
	}
	old := s.cfg.ProviderCall
	defer func() { s.cfg.ProviderCall = old }()
	credential := "fixture-token"
	region := "nyc"
	if cloud == "aws" {
		credential = `{"access_key_id":"test","secret_access_key":"fixture-secret"}`
		region = "us-east-1"
	}
	connection, e := client.CreateConnection(ctx, connect.NewRequest(&pb.CreateConnectionRequest{OrganizationId: org, Name: cloud + " service fixture", Provider: cloud, Region: region, Credential: credential}))
	check(e)
	id := connection.Msg.Connection.Id
	result := provider.Response{Version: provider.Protocol, Complete: true, InventoryKinds: kinds}
	for _, kind := range kinds {
		result.Resources = append(result.Resources, provider.Resource{Kind: kind, NativeID: "same-id", Name: kind, Region: region, Status: "available", Size: "postgres 17.4"})
	}
	s.cfg.ProviderCall = func(_ context.Context, r provider.Request) (provider.Response, error) {
		if r.Provider != cloud || r.ConnectionID != id || r.Validate() != nil {
			t.Fatal("wrong AWS database scan scope")
		}
		for _, kind := range kinds {
			if !slices.Contains(r.InventoryKinds, kind) {
				t.Fatal("runtime omitted database capability")
			}
		}
		return result, nil
	}
	scan := func() {
		_, e := client.RefreshConnection(ctx, connect.NewRequest(&pb.RefreshConnectionRequest{OrganizationId: org, Id: id}))
		check(e)
		job, e := s.q.ClaimScan(ctx)
		check(e)
		if job.ConnectionID != id {
			t.Fatal("unexpected scan claimed")
		}
		check(s.runScan(ctx, job))
	}
	scan()
	for _, kind := range kinds {
		rows, e := client.ListResources(ctx, connect.NewRequest(&pb.ListResourcesRequest{OrganizationId: org, ConnectionId: id, Kind: kind}))
		check(e)
		if len(rows.Msg.Resources) != 1 || rows.Msg.Resources[0].Provider != cloud || rows.Msg.Resources[0].Kind != kind {
			t.Fatal("database kind identity/filter lost")
		}
		resource := rows.Msg.Resources[0]
		_, e = client.GetResource(ctx, connect.NewRequest(&pb.GetResourceRequest{OrganizationId: other, Id: resource.Id}))
		if connect.CodeOf(e) != connect.CodePermissionDenied {
			t.Fatal("cross-organization database detail allowed")
		}
		_, e = client.RequestOperation(ctx, connect.NewRequest(&pb.RequestOperationRequest{OrganizationId: org, ResourceId: resource.Id, Action: "restart", ExpectedStatus: "available", Reason: "Must reject database restart", IdempotencyKey: randomID()}))
		if e == nil {
			t.Fatal("database accepted server action")
		}
	}
	count := func(want int) {
		rows, e := client.ListResources(ctx, connect.NewRequest(&pb.ListResourcesRequest{OrganizationId: org, ConnectionId: id}))
		check(e)
		if len(rows.Msg.Resources) != want {
			t.Fatalf("database publication count %d want %d", len(rows.Msg.Resources), want)
		}
	}
	result = provider.Response{Version: provider.Protocol, Error: "discovery_failed"}
	scan()
	count(len(kinds))
	result = provider.Response{Version: provider.Protocol, Complete: true}
	scan()
	count(len(kinds))
	result.InventoryKinds = kinds
	scan()
	count(0)
	_, e = client.SetConnectionEnabled(ctx, connect.NewRequest(&pb.SetConnectionEnabledRequest{OrganizationId: org, Id: id, Enabled: false}))
	check(e)
}
