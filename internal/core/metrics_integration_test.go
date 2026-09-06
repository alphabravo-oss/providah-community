//go:build integration

package core

import (
	"connectrpc.com/connect"
	"context"
	"github.com/alphabravo-oss/providah-community/internal/database"
	pb "github.com/alphabravo-oss/providah-community/internal/gen/providah/v1"
	"github.com/alphabravo-oss/providah-community/internal/gen/providah/v1/providahv1connect"
	"github.com/alphabravo-oss/providah-community/sdk/provider"
	"strings"
	"testing"
)

func exerciseMetrics(t *testing.T, s *Service, client providahv1connect.ConsoleServiceClient, ctx context.Context, org string) {
	check := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	var connection string
	check(s.pool.QueryRow(ctx, "SELECT id FROM connections WHERE org_id=$1 AND provider='hetzner' AND enabled AND deleted_at IS NULL LIMIT 1", org).Scan(&connection))
	id := randomID()
	check(s.q.UpsertResource(ctx, database.UpsertResourceParams{ID: id, OrgID: org, ConnectionID: connection, Provider: "hetzner", Kind: "compute.server", NativeID: "9988", Name: "Metric server", Region: "fsn1", Status: "running"}))
	previous, catalog := s.cfg.ProviderCall, s.cfg.ProviderRuntimes
	defer func() { s.cfg.ProviderCall = previous; s.cfg.ProviderRuntimes = catalog }()
	calls := 0
	mode := "valid"
	s.cfg.ProviderCall = func(_ context.Context, r provider.Request) (provider.Response, error) {
		calls++
		if r.Validate() != nil || r.Metrics == nil || r.Metrics.NativeID != "9988" || r.Metrics.End-r.Metrics.Start != 3600 || r.ConnectionID != connection || r.OrganizationID != org || r.Power != nil {
			t.Fatal("wrong metric scope")
		}
		result := &provider.MetricsResult{Series: []provider.MetricSeries{{ID: "cpu", Name: "CPU utilization", Unit: "percent", Aggregation: "Average", PeriodSeconds: 300, Points: []provider.MetricPoint{{At: r.Metrics.Start, Value: 0}, {At: r.Metrics.End - 300, Value: 12}}}}}
		switch mode {
		case "empty":
			result.Series[0].Points = nil
		case "revoke":
			_, e := s.pool.Exec(ctx, "UPDATE memberships SET role_id=NULL,permissions='{}' WHERE org_id=$1 AND user_id=(SELECT id FROM users WHERE email='owner@example.com')", org)
			check(e)
		case "rotate":
			_, e := s.pool.Exec(ctx, "UPDATE connections SET revision=revision+1 WHERE id=$1", connection)
			check(e)
		case "outside":
			result.Series[0].Points[0].At = r.Metrics.Start - 300
		}
		return provider.Response{Version: provider.Protocol, Metrics: result}, nil
	}
	request := &pb.GetResourceMetricsRequest{OrganizationId: org, ResourceId: id, Hours: 1}
	result, err := client.GetResourceMetrics(ctx, connect.NewRequest(request))
	check(err)
	if len(result.Msg.Series) != 1 || len(result.Msg.Series[0].Points) != 2 || result.Msg.Series[0].Points[0].Value != 0 || result.Msg.FetchedAt == "" {
		t.Fatal("metrics incorrectly mapped")
	}
	mode = "empty"
	result, err = client.GetResourceMetrics(ctx, connect.NewRequest(request))
	check(err)
	if len(result.Msg.Series[0].Points) != 0 {
		t.Fatal("empty samples filled")
	}
	mode = "revoke"
	_, err = client.GetResourceMetrics(ctx, connect.NewRequest(request))
	if connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatal("in-flight permission revocation leaked metrics", err)
	}
	_, err = s.pool.Exec(ctx, "UPDATE memberships SET role_id='administrator' WHERE org_id=$1 AND user_id=(SELECT id FROM users WHERE email='owner@example.com')", org)
	check(err)
	mode = "rotate"
	_, err = client.GetResourceMetrics(ctx, connect.NewRequest(request))
	if connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatal("in-flight credential rotation retained metrics", err)
	}
	mode = "outside"
	_, err = client.GetResourceMetrics(ctx, connect.NewRequest(request))
	if connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatal("out-of-window data accepted", err)
	}
	before := calls
	request.ResourceId = randomID()
	_, err = client.GetResourceMetrics(ctx, connect.NewRequest(request))
	if connect.CodeOf(err) != connect.CodePermissionDenied || calls != before {
		t.Fatal("unknown target reached provider")
	}
	request.ResourceId = id
	request.Hours = 48
	_, err = client.GetResourceMetrics(ctx, connect.NewRequest(request))
	if connect.CodeOf(err) != connect.CodeInvalidArgument || calls != before {
		t.Fatal("unbounded metrics accepted")
	}
	request.Hours = 1
	s.cfg.ProviderRuntimes = []provider.Runtime{{Provider: "hetzner", Image: "sha256:" + strings.Repeat("a", 64), Protocol: provider.Protocol, Version: "legacy", SDKVersion: "legacy"}}
	_, err = client.GetResourceMetrics(ctx, connect.NewRequest(request))
	if connect.CodeOf(err) != connect.CodeFailedPrecondition || calls != before {
		t.Fatal("legacy runtime received metrics")
	}
	detail, err := client.GetResource(ctx, connect.NewRequest(&pb.GetResourceRequest{OrganizationId: org, Id: id}))
	check(err)
	if detail.Msg.MetricsSupported {
		t.Fatal("legacy metric capability advertised")
	}
}
