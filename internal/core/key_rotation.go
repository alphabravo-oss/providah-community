package core

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"slices"

	"github.com/alphabravo-oss/providah-community/internal/database"
	"github.com/alphabravo-oss/providah-community/internal/sourcebundle"
	"github.com/alphabravo-oss/providah-community/internal/tfstate"
	"github.com/jackc/pgx/v5"
)

// Keep this inventory in sync with all database-encrypted fields. Schema drift fails closed.
var encryptedColumns = []struct{ table, key, column string }{
	{"automation_projects", "id", "inputs_ciphertext"},
	{"automation_states", "id", "ciphertext"},
	{"automation_sources", "id", "ciphertext"},
	{"users", "id", "totp_ciphertext"},
	{"setup_pending", "id", "ciphertext"},
	{"connections", "id", "ciphertext"},
	{"invitations", "id", "totp_ciphertext"},
	{"notification_smtp", "org_id", "ciphertext"},
	{"notification_destinations", "id", "ciphertext"},
	{"notification_deliveries", "id", "verification_ciphertext"},
	{"audit_exports", "org_id", "ciphertext"},
}

type KeyRotationReport struct {
	KeyID       string         `json:"key_id"`
	Checked     map[string]int `json:"checked"`
	Rewrapped   map[string]int `json:"rewrapped"`
	NeedsRewrap map[string]int `json:"needs_rewrap"`
	CheckOnly   bool           `json:"check_only"`
}

// RewrapSecrets is an offline deployment-administrator operation, never a browser API.
// Stop every core replica and worker coordinator first; retain old keys for historical backups.
func (s *Service) RewrapSecrets(ctx context.Context, write bool) (KeyRotationReport, error) {
	report := KeyRotationReport{NeedsRewrap: map[string]int{}, KeyID: s.identity.Recipient().String(), Checked: map[string]int{}, Rewrapped: map[string]int{}, CheckOnly: !write}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return KeyRotationReport{}, err
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err = tx.Exec(ctx, "SET LOCAL lock_timeout='10s'"); err != nil {
		return KeyRotationReport{}, err
	}
	var locked bool
	if err = tx.QueryRow(ctx, "SELECT pg_try_advisory_xact_lock(724193806214)").Scan(&locked); err != nil {
		return KeyRotationReport{}, err
	}
	if !locked {
		return KeyRotationReport{}, errors.New("another key maintenance command is running")
	}
	expected := []string{}
	tables := []string{}
	for _, c := range encryptedColumns {
		expected = append(expected, c.table+"."+c.column)
		tables = append(tables, pgx.Identifier{c.table}.Sanitize())
	}
	var actual []string
	if err = tx.QueryRow(ctx, `SELECT array_agg(table_name||'.'||column_name ORDER BY table_name,column_name) FROM information_schema.columns WHERE table_schema=current_schema() AND column_name LIKE '%ciphertext'`).Scan(&actual); err != nil {
		return KeyRotationReport{}, err
	}
	slices.Sort(expected)
	if !slices.Equal(expected, actual) {
		return KeyRotationReport{}, errors.New("encrypted column inventory changed; update the rotation implementation before proceeding")
	}
	// ponytail: one transaction and table locks require an offline maintenance window; use staged per-key batches if downtime becomes unacceptable.
	for _, table := range tables {
		if _, err = tx.Exec(ctx, "LOCK TABLE "+table+" IN ACCESS EXCLUSIVE MODE"); err != nil {
			return KeyRotationReport{}, errors.New("could not lock secret tables; stop all core replicas and retry")
		}
	}
	for _, c := range encryptedColumns {
		limit := int64(65536)
		batchSize := 100
		if c.table == "automation_states" {
			limit = tfstate.MaxBytes
			batchSize = 1
		}
		if c.table == "automation_sources" {
			limit = sourcebundle.MaxArchive
		}
		table, key, column := pgx.Identifier{c.table}.Sanitize(), pgx.Identifier{c.key}.Sanitize(), pgx.Identifier{c.column}.Sanitize()
		cursor := ""
		for {
			rows, e := tx.Query(ctx, fmt.Sprintf("SELECT %s::text,%s FROM %s WHERE %s IS NOT NULL AND %s::text>$1 ORDER BY %s::text LIMIT %d", key, column, table, column, key, key, batchSize), cursor)
			if e != nil {
				return KeyRotationReport{}, e
			}
			batch, e := pgx.CollectRows(rows, func(row pgx.CollectableRow) (struct {
				id     string
				cipher []byte
			}, error) {
				var v struct {
					id     string
					cipher []byte
				}
				e := row.Scan(&v.id, &v.cipher)
				return v, e
			})
			if e != nil {
				return KeyRotationReport{}, e
			}
			if len(batch) == 0 {
				break
			}
			for _, v := range batch {
				plain, e := s.openBounded(v.cipher, limit)
				// Only this offline conversion command accepts the previous raw age format.
				if bytes.HasPrefix(v.cipher, []byte("age-encryption.org/v1\n")) {
					plain, e = decryptAge(v.cipher, limit, s.identities...)
				}
				if e != nil {
					return KeyRotationReport{}, fmt.Errorf("cannot decrypt %s record; no rotation committed", c.table)
				}
				report.Checked[c.table]++
				_, primaryErr := decryptBounded(v.cipher, limit, s.identity)
				if primaryErr != nil && !write {
					report.NeedsRewrap[c.table]++
				}
				if write && primaryErr != nil {
					cipher, e := s.seal(plain)
					if e != nil {
						return KeyRotationReport{}, errors.New("encryption failed; no rotation committed")
					}
					if _, e = decryptBounded(cipher, limit, s.identity); e != nil {
						return KeyRotationReport{}, errors.New("new ciphertext verification failed")
					}
					if _, e = tx.Exec(ctx, fmt.Sprintf("UPDATE %s SET %s=$1 WHERE %s::text=$2", table, column, key), cipher, v.id); e != nil {
						return KeyRotationReport{}, e
					}
					report.Rewrapped[c.table]++
				}
				if write && c.table == "connections" {
					if _, e = tx.Exec(ctx, "UPDATE connections SET key_id=$1 WHERE id=$2 AND key_id<>$1", report.KeyID, v.id); e != nil {
						return KeyRotationReport{}, e
					}
				}
				cursor = v.id
			}
		}
	}
	if write {
		rows, e := tx.Query(ctx, "SELECT id FROM organizations ORDER BY id")
		if e != nil {
			return KeyRotationReport{}, e
		}
		orgs, e := pgx.CollectRows(rows, pgx.RowTo[string])
		if e != nil {
			return KeyRotationReport{}, e
		}
		q := database.New(tx)
		for _, org := range orgs {
			if e = audit(ctx, q, org, "system:key-rotation", "security.encryption_key_rotated", report.KeyID, map[string]any{"key_id": report.KeyID}); e != nil {
				return KeyRotationReport{}, e
			}
		}
	}
	if err = tx.Commit(ctx); err != nil {
		return KeyRotationReport{}, err
	}
	return report, nil
}
