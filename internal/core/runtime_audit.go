package core

import (
	"context"
	"encoding/json"
	"github.com/alphabravo-oss/providah-community/sdk/provider"
)

// AuditRuntimeCatalog records the admitted catalog before serving requests.
// Order is significant: the first image for each provider is its default.
func (s *Service) AuditRuntimeCatalog(ctx context.Context) error {
	catalog := s.cfg.ProviderRuntimes
	if catalog == nil {
		catalog = []provider.Runtime{}
	}
	if len(catalog) > 0 {
		if err := provider.ValidateRuntimes(catalog); err != nil {
			return err
		}
	}
	raw, err := json.Marshal(catalog)
	if err != nil {
		return err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	// Serialize startup comparisons across replicas; unchanged restarts add no event.
	if _, err = tx.Exec(ctx, `SELECT pg_advisory_xact_lock(574211003)`); err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO installation_events(actor_id,target_id,action,details)
 SELECT NULL,NULL,'runtime.catalog_admitted',jsonb_build_object('runtimes',$1::jsonb)
 WHERE $1::jsonb IS DISTINCT FROM (
 SELECT details->'runtimes' FROM installation_events WHERE action='runtime.catalog_admitted' ORDER BY id DESC LIMIT 1
 )`, raw)
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}
