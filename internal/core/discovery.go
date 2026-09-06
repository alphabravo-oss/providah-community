package core

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"connectrpc.com/connect"
	"github.com/alphabravo-oss/providah-community/internal/database"
	pb "github.com/alphabravo-oss/providah-community/internal/gen/providah/v1"
	"github.com/alphabravo-oss/providah-community/sdk/provider"
	"github.com/jackc/pgx/v5"
)

func (s *Service) RefreshConnection(ctx context.Context, req *connect.Request[pb.RefreshConnectionRequest]) (*connect.Response[pb.RefreshConnectionResponse], error) {
	if s.cfg.ProviderCall == nil {
		return nil, conflict("Provider discovery is not configured. Start the provider launcher.")
	}
	id, err := s.queueScan(ctx, req.Msg.OrganizationId, req.Msg.Id, actor(ctx).UserID, actor(ctx).Email)
	return connect.NewResponse(&pb.RefreshConnectionResponse{JobId: id}), err
}
func (s *Service) queueScan(ctx context.Context, org, connectionID, requester, who string) (string, error) {
	var id string
	err := s.transaction(ctx, func(q *database.Queries) error {
		c, err := q.ConnectionForScan(ctx, database.ConnectionForScanParams{OrgID: org, ID: connectionID})
		if errors.Is(err, pgx.ErrNoRows) {
			return denied()
		}
		if err != nil {
			return err
		}
		if err = requireModule(ctx, q, org, c.Provider, 0); err != nil {
			return err
		}
		if !c.Enabled {
			return conflict("Enable the connection before refreshing.")
		}
		runtime, revision, err := s.runtimeFor(ctx, q, org, c.Provider, 0)
		if err != nil {
			return err
		}
		id, err = q.QueueScan(ctx, database.QueueScanParams{TraceParent: traceParent(ctx), RuntimeID: runtime, ModuleRevision: revision, ID: randomID(), OrgID: org, ConnectionID: connectionID, RequesterID: requester, Actor: who})
		if errors.Is(err, pgx.ErrNoRows) {
			id, err = q.ActiveScan(ctx, database.ActiveScanParams{OrgID: org, ConnectionID: connectionID})
			return err
		}
		if err != nil {
			return err
		}
		if err = q.MarkScanQueued(ctx, database.MarkScanQueuedParams{OrgID: org, ID: connectionID}); err != nil {
			return err
		}
		return audit(ctx, q, org, who, "connection.refresh_requested", connectionID, map[string]any{"job_id": id})
	})
	return id, err
}

// StartDiscovery runs bounded, durable read-only jobs. Expired jobs never publish late results.
// ponytail: one scan per core replica; add bounded concurrency after measuring provider quotas.
func (s *Service) StartDiscovery(ctx context.Context) {
	if s.cfg.ProviderCall == nil {
		return
	}
	timer := time.NewTicker(time.Second)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			if err := s.discoveryTick(ctx); err != nil && !errors.Is(err, context.Canceled) {
				s.log.Error().Msg("discovery coordinator failed; pending work will be retried")
			}
		}
	}
}
func (s *Service) discoveryTick(ctx context.Context) error {
	rows, err := s.q.ExpiredScans(ctx)
	if err != nil {
		return err
	}
	for _, r := range rows {
		if err = s.transaction(ctx, func(q *database.Queries) error {
			if _, err := q.ConnectionForScan(ctx, database.ConnectionForScanParams{OrgID: r.OrgID, ID: r.ConnectionID}); err != nil && !errors.Is(err, pgx.ErrNoRows) {
				return err
			}
			n, err := q.ExpireScan(ctx, r.ID)
			if err != nil || n == 0 {
				return err
			}
			return q.RecordScanFailure(ctx, database.RecordScanFailureParams{OrgID: r.OrgID, ID: r.ConnectionID, Revision: r.Revision, ScanError: "Refresh was interrupted; retrying after five minutes."})
		}); err != nil {
			return err
		}
	}
	due, err := s.q.DueConnections(ctx)
	if err != nil {
		return err
	}
	for _, c := range due {
		if _, err = s.queueScan(ctx, c.OrgID, c.ID, "", "system:inventory"); err != nil {
			var ce *connect.Error
			if !errors.As(err, &ce) {
				return err
			}
		}
	}
	job, err := s.q.ClaimScan(ctx)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	return s.runScan(ctx, job)
}
func (s *Service) runScan(ctx context.Context, job database.ScanJob) error {
	ctx, span := s.telemetry.tracer.Start(workContext(ctx, job.TraceParent), "inventory.scan")
	defer span.End()
	var req provider.Request
	err := s.transaction(ctx, func(q *database.Queries) error {
		c, err := q.ConnectionForScan(ctx, database.ConnectionForScanParams{OrgID: job.OrgID, ID: job.ConnectionID})
		if err != nil {
			return err
		}
		if err = requireModule(ctx, q, job.OrgID, c.Provider, job.ModuleRevision); err != nil {
			return err
		}
		if !s.runtimeAvailable(c.Provider, job.RuntimeID) {
			return conflict("The pinned provider runtime is unavailable.")
		}
		if !c.Enabled || c.Revision != job.Revision {
			return conflict("Connection changed.")
		}
		if job.RequesterID != "" {
			perms, err := q.Permissions(ctx, database.PermissionsParams{OrgID: job.OrgID, UserID: job.RequesterID})
			if err != nil {
				return denied()
			}
			allowed := false
			for _, p := range perms {
				if p == "connections.manage" {
					allowed = true
				}
			}
			if !allowed {
				return denied()
			}
		}
		credential, err := s.open(c.Ciphertext)
		if err != nil {
			return err
		}
		region := c.Region
		if c.Provider == "aws" && region == "" {
			region = "us-east-1"
		}
		credential, err = s.providerCredential(ctx, c.Provider, credential, region, c.OrgID, c.ID)
		if err != nil {
			return err
		}
		capabilities := s.runtimeCapabilities(c.Provider, job.RuntimeID)
		if len(capabilities.DiscoveryKinds()) == 0 {
			return conflict("Selected runtime does not support discovery.")
		}
		var kinds []string
		if capabilities.CapabilitiesVersion == 1 {
			kinds = capabilities.DiscoveryKinds()
		}
		req = provider.Request{InventoryKinds: kinds, RuntimeID: job.RuntimeID, Version: provider.Protocol, OrganizationID: c.OrgID, ConnectionID: c.ID, Provider: c.Provider, Region: region, Credential: credential}
		return q.MarkScanRunning(ctx, database.MarkScanRunningParams{OrgID: c.OrgID, ID: c.ID, Revision: c.Revision})
	})
	var result provider.Response
	if err == nil {
		scanCtx, cancel := context.WithTimeout(ctx, 140*time.Second)
		result, err = s.callProvider(scanCtx, req)
		cancel()
	}
	if err == nil {
		for _, kind := range result.CoveredKinds() {
			if !slices.Contains(s.runtimeCapabilities(req.Provider, job.RuntimeID).DiscoveryKinds(), kind) {
				err = errors.New("unexpected inventory scope")
				break
			}
		}
	}
	message := ""
	if err != nil || result.Power != nil || result.Validate() != nil {
		message = "Could not complete server discovery. Check credentials, read permissions, region, and launcher availability."
	}
	return s.finishScan(ctx, job, result, message)
}
func (s *Service) finishScan(ctx context.Context, job database.ScanJob, result provider.Response, message string) error {
	return s.transaction(ctx, func(q *database.Queries) error {
		// All paths lock the connection before its job to avoid queue/completion deadlocks.
		c, err := q.ConnectionForScan(ctx, database.ConnectionForScanParams{OrgID: job.OrgID, ID: job.ConnectionID})
		if _, lockErr := q.LockRunningScan(ctx, job.ID); errors.Is(lockErr, pgx.ErrNoRows) {
			return nil
		} else if lockErr != nil {
			return lockErr
		}
		if errors.Is(err, pgx.ErrNoRows) || err == nil && (!c.Enabled || c.Revision != job.Revision) {
			return q.FinishScanJob(ctx, database.FinishScanJobParams{ID: job.ID, Status: "canceled", Error: "connection_changed"})
		}
		if err != nil {
			return err
		}
		allowed, err := moduleAllowed(ctx, q, job.OrgID, c.Provider, job.ModuleRevision)
		if err != nil {
			return err
		}
		if !allowed {
			if err = q.RecordModuleScanCanceled(ctx, database.RecordModuleScanCanceledParams{OrgID: job.OrgID, ID: job.ConnectionID, Revision: job.Revision}); err != nil {
				return err
			}
			return q.FinishScanJob(ctx, database.FinishScanJobParams{ID: job.ID, Status: "canceled", Error: "provider_module_changed"})
		}
		if message != "" {
			if err = q.RecordScanFailure(ctx, database.RecordScanFailureParams{OrgID: job.OrgID, ID: job.ConnectionID, Revision: job.Revision, ScanError: message}); err != nil {
				return err
			}
			if err = q.FinishScanJob(ctx, database.FinishScanJobParams{ID: job.ID, Status: "failed", Error: "discovery_failed"}); err != nil {
				return err
			}
			return audit(ctx, q, job.OrgID, job.Actor, "connection.refresh_failed", job.ConnectionID, map[string]any{"job_id": job.ID})
		}
		if result.Power != nil {
			return invalid("Unexpected action response.")
		}
		if err = result.Validate(); err != nil {
			return err
		}
		// ponytail: discard the whole connection snapshot when operations overtake it; use per-resource versions if churn makes this expensive.
		overtaken, err := q.ScanOvertakenByOperation(ctx, database.ScanOvertakenByOperationParams{OrgID: job.OrgID, ConnectionID: job.ConnectionID, ScanStarted: job.CreatedAt})
		if err != nil {
			return err
		}
		if overtaken {
			if err = q.RescheduleOvertakenScan(ctx, database.RescheduleOvertakenScanParams{OrgID: job.OrgID, ID: job.ConnectionID}); err != nil {
				return err
			}
			return q.FinishScanJob(ctx, database.FinishScanJobParams{ID: job.ID, Status: "canceled", Error: "operation_overtook_scan"})
		}
		ids := make([]string, 0, len(result.Resources))
		for _, r := range result.Resources {
			id := hex.EncodeToString(hash(fmt.Sprintf("%s\n%s\n%s\n%s\n%s", job.OrgID, job.ConnectionID, r.ResourceKind(), r.Region, r.NativeID)))
			ids = append(ids, id)
			var tags []byte
			if r.Tags != nil {
				tags, err = json.Marshal(r.Tags)
				if err != nil {
					return err
				}
			}
			if err = q.UpsertResource(ctx, database.UpsertResourceParams{TagMetadata: tags, ProviderIdentity: r.ProviderIdentity, ID: id, OrgID: job.OrgID, ConnectionID: job.ConnectionID, Kind: r.ResourceKind(), NativeID: r.NativeID, Name: r.Name, Provider: c.Provider, Region: r.Region, Status: r.Status, PublicIp: r.PublicIP, PrivateIp: r.PrivateIP, Size: r.Size}); err != nil {
				return err
			}
		}
		if err = q.RetireMissingResources(ctx, database.RetireMissingResourcesParams{OrgID: job.OrgID, ConnectionID: job.ConnectionID, PresentIds: ids, CoveredKinds: result.CoveredKinds()}); err != nil {
			return err
		}
		if err = q.RecordScanSuccess(ctx, database.RecordScanSuccessParams{OrgID: job.OrgID, ID: job.ConnectionID, Revision: job.Revision}); err != nil {
			return err
		}
		if err = q.FinishScanJob(ctx, database.FinishScanJobParams{ID: job.ID, Status: "succeeded"}); err != nil {
			return err
		}
		return audit(ctx, q, job.OrgID, job.Actor, "connection.refreshed", job.ConnectionID, map[string]any{"job_id": job.ID, "resources": len(ids), "kinds": result.CoveredKinds()})
	})
}
func (s *Service) GetResource(ctx context.Context, req *connect.Request[pb.GetResourceRequest]) (*connect.Response[pb.GetResourceResponse], error) {
	r, err := s.q.GetResource(ctx, database.GetResourceParams{OrgID: req.Msg.OrganizationId, ID: req.Msg.Id})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, denied()
	}
	if err != nil {
		return nil, err
	}
	enabled, err := moduleAllowed(ctx, s.q, req.Msg.OrganizationId, r.Provider, 0)
	if err != nil {
		return nil, err
	}
	state, e := s.q.GetProviderModule(ctx, database.GetProviderModuleParams{OrgID: req.Msg.OrganizationId, Provider: r.Provider})
	if e != nil {
		return nil, e
	}
	capabilities := s.runtimeCapabilities(r.Provider, s.selectedRuntime(r.Provider, state.RuntimeID))
	owners, err := s.q.ResourceOwnership(ctx, database.ResourceOwnershipParams{OrgID: req.Msg.OrganizationId, ConnectionID: r.ConnectionID, Kind: r.Kind, NativeID: r.NativeID, Region: r.Region})
	if err != nil {
		return nil, err
	}
	actions := []string{}
	for _, action := range capabilities.OperationActions() {
		if (len(owners) == 0 || ownershipActionAllowed(action)) && capabilities.SupportsResourceAction(r.Kind, action) && (!provider.DatabaseKind(r.Kind) || r.ProviderIdentity != "") {
			actions = append(actions, action)
		}
	}
	return connect.NewResponse(&pb.GetResourceResponse{OwnershipProjectIds: owners, MetricsSupported: capabilities.Metrics && r.Kind == "compute.server", AvailableActions: actions, ProviderEnabled: enabled, ConnectionName: r.ConnectionName, ConnectionEnabled: r.ConnectionEnabled, Resource: &pb.Resource{Tags: resourceTags(r.TagMetadata), Id: r.ID, ConnectionId: r.ConnectionID, NativeId: r.NativeID, Name: r.Name, Provider: r.Provider, Kind: r.Kind, Region: r.Region, Status: r.Status, ObservedAt: stamp(r.ObservedAt), PublicIp: r.PublicIp, PrivateIp: r.PrivateIp, Size: r.Size}}), nil
}

func resourceTags(raw []byte) *pb.ResourceTags {
	if len(raw) == 0 {
		return nil
	}
	var tags pb.ResourceTags
	if json.Unmarshal(raw, &tags) != nil {
		return nil
	}
	return &tags
}
