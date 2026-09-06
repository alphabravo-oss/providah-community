package core

import (
	"context"
	pb "github.com/alphabravo-oss/providah-community/internal/gen/providah/v1"
	"time"
)

// This is database allocation, not host free space or a quota enforcement mechanism.
func (s *Service) databaseStorage(ctx context.Context) (*pb.DatabaseStorage, error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	out := &pb.DatabaseStorage{BudgetBytes: s.cfg.DatabaseBudgetBytes}
	// ponytail: PostgreSQL file-size accounting per sample; use a slower sampling cadence if relation count makes it expensive.
	err := s.pool.QueryRow(ctx, `SELECT pg_database_size(current_database()), pg_total_relation_size('audit_events')+pg_total_relation_size('installation_events')`).Scan(&out.UsedBytes, &out.AuditBytes)
	if err != nil {
		return nil, err
	}
	out.Status = databaseStorageStatus(out.UsedBytes, out.BudgetBytes)
	return out, nil
}
func databaseStorageStatus(used, budget int64) string {
	if budget <= 0 {
		return "unconfigured"
	}
	ratio := float64(used) / float64(budget)
	if ratio >= 0.9 {
		return "critical"
	}
	if ratio >= 0.8 {
		return "warning"
	}
	return "within_budget"
}
