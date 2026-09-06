//go:build integration

package core

import (
	"bytes"
	"context"
	"encoding/json"
	"filippo.io/age"
	"fmt"
	"github.com/jackc/pgx/v5"
	"strings"
	"testing"
)

func exerciseEncryptionRotation(t *testing.T, s *Service, ctx context.Context) *Service {
	t.Helper()
	check := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	fixture, err := s.seal("rotation-only-fixture-secret")
	check(err)
	_, err = s.pool.Exec(ctx, "INSERT INTO setup_pending(id,ciphertext,expires_at) VALUES(true,$1,now()) ON CONFLICT(id) DO UPDATE SET ciphertext=$1", fixture)
	check(err)
	_, err = s.pool.Exec(ctx, "UPDATE invitations SET totp_ciphertext=$1 WHERE id=(SELECT id FROM invitations LIMIT 1)", fixture)
	check(err)
	_, err = s.pool.Exec(ctx, "UPDATE notification_deliveries SET verification_ciphertext=$1 WHERE id=(SELECT id FROM notification_deliveries LIMIT 1)", fixture)
	check(err)
	_, err = s.pool.Exec(ctx, "INSERT INTO notification_smtp(org_id,ciphertext) SELECT id,$1 FROM organizations LIMIT 1 ON CONFLICT(org_id) DO UPDATE SET ciphertext=$1", fixture)
	check(err)
	before := map[string][]byte{}
	plains := map[string]string{}
	ids := map[string]string{}
	for _, c := range encryptedColumns {
		limit := int64(65536)
		if c.table == "automation_states" {
			limit = 32 << 20
		}
		if c.table == "automation_sources" {
			limit = 4 << 20
		}
		var value []byte
		var id string
		err = s.pool.QueryRow(ctx, fmt.Sprintf("SELECT %s::text,%s FROM %s WHERE %s IS NOT NULL ORDER BY %s::text LIMIT 1", pgx.Identifier{c.key}.Sanitize(), pgx.Identifier{c.column}.Sanitize(), pgx.Identifier{c.table}.Sanitize(), pgx.Identifier{c.column}.Sanitize(), pgx.Identifier{c.key}.Sanitize())).Scan(&id, &value)
		check(err)
		plain, e := s.openBounded(value, limit)
		check(e)
		before[c.table] = value
		plains[c.table] = plain
		ids[c.table] = id
	}
	// An existing raw-age row is accepted only by offline conversion, never normal login.
	var old secretEnvelope
	check(json.Unmarshal(before["users"], &old))
	before["users"] = old.Ciphertext
	_, err = s.pool.Exec(ctx, "UPDATE users SET totp_ciphertext=$1 WHERE id=$2", old.Ciphertext, ids["users"])
	check(err)
	if _, err = s.open(old.Ciphertext); err == nil {
		t.Fatal("runtime accepted unmigrated ciphertext")
	}
	next, err := age.GenerateX25519Identity()
	check(err)
	cfg := s.cfg
	cfg.AgeIdentity = next.String()
	cfg.AgePreviousIdentities = []string{s.identity.String()}
	rotated, err := New(s.pool, cfg, s.log)
	check(err)
	report, err := rotated.RewrapSecrets(ctx, false)
	check(err)
	if len(report.NeedsRewrap) != len(encryptedColumns) || len(report.Rewrapped) != 0 {
		t.Fatal("check did not find all historical keys")
	}
	// A later corrupt record must roll back earlier rewrites in the same transaction.
	_, err = s.pool.Exec(ctx, "UPDATE connections SET ciphertext=$1 WHERE id=$2", []byte("corrupt"), ids["connections"])
	check(err)
	if _, err = rotated.RewrapSecrets(ctx, true); err == nil {
		t.Fatal("corrupt ciphertext accepted")
	}
	var value []byte
	check(s.pool.QueryRow(ctx, "SELECT totp_ciphertext FROM users WHERE id=$1", ids["users"]).Scan(&value))
	if !bytes.Equal(value, before["users"]) {
		t.Fatal("partial rotation committed")
	}
	_, err = s.pool.Exec(ctx, "UPDATE connections SET ciphertext=$1 WHERE id=$2", before["connections"], ids["connections"])
	check(err)
	var revision int64
	check(s.pool.QueryRow(ctx, "SELECT revision FROM connections WHERE id=$1", ids["connections"]).Scan(&revision))
	report, err = rotated.RewrapSecrets(ctx, true)
	check(err)
	if len(report.Rewrapped) != len(encryptedColumns) || len(report.NeedsRewrap) != 0 {
		t.Fatal("not all secrets rewrapped")
	}
	cfg.AgePreviousIdentities = nil
	primaryOnly, err := New(s.pool, cfg, s.log)
	check(err)
	_, err = primaryOnly.RewrapSecrets(ctx, false)
	check(err)
	for _, c := range encryptedColumns {
		limit := int64(65536)
		if c.table == "automation_states" {
			limit = 32 << 20
		}
		if c.table == "automation_sources" {
			limit = 4 << 20
		}
		check(s.pool.QueryRow(ctx, fmt.Sprintf("SELECT %s FROM %s WHERE %s::text=$1", pgx.Identifier{c.column}.Sanitize(), pgx.Identifier{c.table}.Sanitize(), pgx.Identifier{c.key}.Sanitize()), ids[c.table]).Scan(&value))
		plain, e := primaryOnly.openBounded(value, limit)
		check(e)
		if plain != plains[c.table] {
			t.Fatalf("secret changed in %s", c.table)
		}
		if _, e = s.openBounded(value, limit); e == nil {
			t.Fatalf("old key still decrypts %s", c.table)
		}
	}
	var keyID string
	var afterRevision int64
	check(s.pool.QueryRow(ctx, "SELECT key_id,revision FROM connections WHERE id=$1", ids["connections"]).Scan(&keyID, &afterRevision))
	if keyID != next.Recipient().String() || revision != afterRevision {
		t.Fatal("metadata or credential revision changed incorrectly")
	}
	encoded, err := json.Marshal(report)
	check(err)
	if strings.Contains(string(encoded), next.String()) || strings.Contains(string(encoded), "rotation-only-fixture-secret") {
		t.Fatal("report exposed secrets")
	}
	again, err := primaryOnly.RewrapSecrets(ctx, true)
	check(err)
	if len(again.Rewrapped) != 0 {
		t.Fatal("idempotent rotation rewrote current-key ciphertext")
	}
	_, err = s.pool.Exec(ctx, "ALTER TABLE setup_pending ADD COLUMN surprise_ciphertext bytea")
	check(err)
	if _, err = primaryOnly.RewrapSecrets(ctx, true); err == nil {
		t.Fatal("new encrypted column silently omitted")
	}
	_, err = s.pool.Exec(ctx, "ALTER TABLE setup_pending DROP COLUMN surprise_ciphertext")
	check(err)
	return primaryOnly
}
