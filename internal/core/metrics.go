package core

import (
	"connectrpc.com/connect"
	"context"
	"github.com/alphabravo-oss/providah-community/internal/database"
	pb "github.com/alphabravo-oss/providah-community/internal/gen/providah/v1"
	"github.com/alphabravo-oss/providah-community/sdk/provider"
	"slices"
	"time"
)

func (s *Service) GetResourceMetrics(ctx context.Context, req *connect.Request[pb.GetResourceMetricsRequest]) (*connect.Response[pb.GetResourceMetricsResponse], error) {
	r := req.Msg
	if !slices.Contains([]int32{1, 6, 24}, r.Hours) {
		return nil, invalid("Choose one, six, or twenty-four hours of metrics.")
	}
	if s.cfg.ProviderCall == nil {
		return nil, conflict("Provider execution is not configured.")
	}
	var request provider.Request
	var connectionRevision, moduleRevision int64
	// Snapshot authority without holding database locks during the provider network call.
	err := s.transaction(ctx, func(q *database.Queries) error {
		resource, err := q.GetResource(ctx, database.GetResourceParams{OrgID: r.OrganizationId, ID: r.ResourceId})
		if err != nil {
			return denied()
		}
		c, err := q.LockOperationConnection(ctx, database.LockOperationConnectionParams{OrgID: r.OrganizationId, ID: resource.ConnectionID})
		if err != nil {
			return err
		}
		if !hasPermission(ctx, q, c.OrgID, actor(ctx).UserID, "resources.read") || !identityAllowed(ctx, q, c.OrgID, actor(ctx).UserID, actor(ctx).OIDC) {
			return denied()
		}
		if resource.Kind != "compute.server" || !c.Enabled || c.DeletedAt.Valid {
			return conflict("Metrics require an enabled server connection.")
		}
		if err = requireModule(ctx, q, c.OrgID, c.Provider, 0); err != nil {
			return err
		}
		runtime, revision, err := s.runtimeFor(ctx, q, c.OrgID, c.Provider, 0)
		if err != nil {
			return err
		}
		if !s.runtimeCapabilities(c.Provider, runtime).Metrics {
			return conflict("Selected provider runtime does not support metrics.")
		}
		raw, err := s.open(c.Ciphertext)
		if err != nil {
			return err
		}
		connectionRevision, moduleRevision = c.Revision, revision
		end := time.Now().UTC().Truncate(5 * time.Minute)
		request = provider.Request{Version: provider.Protocol, RuntimeID: runtime, OrganizationID: c.OrgID, ConnectionID: c.ID, Provider: c.Provider, Region: resource.Region, Credential: raw, Metrics: &provider.MetricsRequest{NativeID: resource.NativeID, Start: end.Add(-time.Duration(r.Hours) * time.Hour).Unix(), End: end.Unix()}}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if err = s.limit(ctx, "metrics:"+request.OrganizationID+":"+request.ConnectionID); err != nil {
		return nil, err
	}
	callCtx, cancel := context.WithTimeout(ctx, 140*time.Second)
	defer cancel()
	raw, err := s.providerCredential(callCtx, request.Provider, request.Credential, request.Region, request.OrganizationID, request.ConnectionID)
	if err != nil {
		return nil, conflict("Could not authenticate to read provider metrics.")
	}
	request.Credential = raw
	result, err := s.callProvider(callCtx, request)
	if err != nil || result.Validate() != nil || result.Metrics == nil || result.Metrics.Error != "" || !result.Metrics.Within(*request.Metrics) {
		return nil, conflict("Metrics are unavailable. Check provider permissions, metric availability, or try again later.")
	}
	// Revocation or credential/runtime changes while the read was in flight discard its result.
	current, err := s.authenticate(ctx, req.Header())
	if err != nil {
		return nil, unauthenticated()
	}
	err = s.transaction(ctx, func(q *database.Queries) error {
		c, err := q.LockOperationConnection(ctx, database.LockOperationConnectionParams{OrgID: r.OrganizationId, ID: request.ConnectionID})
		if err != nil {
			return err
		}
		if !hasPermission(ctx, q, c.OrgID, current.UserID, "resources.read") || !identityAllowed(ctx, q, c.OrgID, current.UserID, current.OIDC) {
			return denied()
		}
		resource, err := q.GetResource(ctx, database.GetResourceParams{OrgID: c.OrgID, ID: r.ResourceId})
		if err != nil {
			return denied()
		}
		if !c.Enabled || c.DeletedAt.Valid || c.Revision != connectionRevision || resource.NativeID != request.Metrics.NativeID || resource.Region != request.Region {
			return conflict("Metric target or credential changed. Read metrics again.")
		}
		return requireModule(ctx, q, c.OrgID, c.Provider, moduleRevision)
	})
	if err != nil {
		return nil, err
	}
	out := &pb.GetResourceMetricsResponse{Start: time.Unix(request.Metrics.Start, 0).UTC().Format(time.RFC3339), End: time.Unix(request.Metrics.End, 0).UTC().Format(time.RFC3339), FetchedAt: time.Now().UTC().Format(time.RFC3339)}
	for _, series := range result.Metrics.Series {
		entry := &pb.MetricSeries{Id: series.ID, Name: series.Name, Unit: series.Unit, Aggregation: series.Aggregation, PeriodSeconds: int32(series.PeriodSeconds)} // #nosec G115 -- Metrics result validation bounds the period before serialization.
		for _, point := range series.Points {
			entry.Points = append(entry.Points, &pb.MetricPoint{Timestamp: time.Unix(point.At, 0).UTC().Format(time.RFC3339), Value: point.Value})
		}
		out.Series = append(out.Series, entry)
	}
	return connect.NewResponse(out), nil
}
