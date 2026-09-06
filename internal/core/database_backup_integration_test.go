//go:build integration

package core

import (
	"bytes"
	"context"
	"github.com/alphabravo-oss/providah-community/internal/database"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"

	"filippo.io/age"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func exerciseDatabaseBackup(t *testing.T, s *Service, ctx context.Context, destroyArtifacts func()) {
	t.Helper()
	if os.Getenv("TEST_BACKUP") != "1" {
		return
	}
	for _, tool := range []string{"python3", "pg_dump", "pg_restore", "age", "createdb", "dropdb", "psql"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Fatalf("backup tool missing: %s", tool)
		}
	}
	check := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	directory := t.TempDir()
	identity, err := age.GenerateX25519Identity()
	check(err)
	key := filepath.Join(directory, "backup-key")
	check(os.WriteFile(key, []byte(identity.String()+"\n"), 0600))
	archive := filepath.Join(directory, "console.age")
	artifactDirectory := filepath.Join(directory, "artifacts")
	backedUp, e := s.BackupArtifacts(ctx, artifactDirectory)
	check(e)
	if _, e = s.BackupArtifacts(ctx, artifactDirectory); e == nil {
		t.Fatal("artifact backup overwritten")
	}
	if _, e = s.RestoreArtifacts(ctx, artifactDirectory); e == nil {
		t.Fatal("artifact restore accepted non-recovery database")
	}

	cfg := s.pool.Config().ConnConfig
	environment := append(os.Environ(), "PGHOST="+cfg.Host, "PGPORT="+strconv.Itoa(int(cfg.Port)), "PGUSER="+cfg.User, "PGPASSWORD="+cfg.Password, "PGDATABASE="+cfg.Database, "PGCONNECT_TIMEOUT=5")
	if cfg.TLSConfig == nil {
		environment = append(environment, "PGSSLMODE=disable")
	}
	run := func(args ...string) {
		t.Helper()
		command := exec.CommandContext(ctx, "python3", append([]string{"../../scripts/database_backup.py"}, args...)...)
		command.Env = environment
		if err := command.Run(); err != nil {
			t.Fatalf("database backup %s failed; check matching PostgreSQL CLI versions", args[0])
		}
	}
	run("backup", archive, "--recipient", identity.Recipient().String())
	run("verify", archive, "--identity", key)
	target := "providah_restore_" + randomID()[:16]
	run("restore", archive, target, "--identity", key)
	defer func() {
		_, err := s.pool.Exec(context.Background(), "DROP DATABASE "+pgx.Identifier{target}.Sanitize()+" WITH (FORCE)")
		if err != nil {
			t.Error(err)
		}
	}()
	restoredConfig := s.pool.Config()
	restoredConfig.ConnConfig.Database = target
	restored, err := pgxpool.NewWithConfig(ctx, restoredConfig)
	check(err)
	defer restored.Close()
	var readonly string
	check(restored.QueryRow(ctx, "SHOW default_transaction_read_only").Scan(&readonly))
	if readonly != "on" {
		t.Fatal("restored database is writable by default")
	}
	rows, err := s.pool.Query(ctx, "SELECT tablename FROM pg_tables WHERE schemaname='public' ORDER BY tablename")
	check(err)
	tables := []string{}
	for rows.Next() {
		var table string
		check(rows.Scan(&table))
		tables = append(tables, table)
	}
	check(rows.Err())
	rows.Close()
	for _, table := range tables {
		query := "SELECT count(*)::text || ':' || coalesce(md5(string_agg(to_jsonb(t)::text, E'\\n' ORDER BY to_jsonb(t)::text COLLATE \"C\")),'empty') FROM " + pgx.Identifier{"public", table}.Sanitize() + " t"
		var before, after string
		check(s.pool.QueryRow(ctx, query).Scan(&before))
		check(restored.QueryRow(ctx, query).Scan(&after))
		if before != after {
			t.Fatalf("restore changed table %s", table)
		}
	}
	if len(tables) < 20 {
		t.Fatal("application schema fixture incomplete")
	}
	recovered, e := New(restored, s.cfg, s.log)
	check(e)
	before, e := s.CheckArtifacts(ctx)
	check(e)
	after, e := recovered.CheckArtifacts(ctx)
	check(e)
	if before != after || after.Sources == 0 || after.States == 0 {
		t.Fatal("restored artifact coverage differs")
	}
	if destroyArtifacts != nil {
		if backedUp.Objects != before.Sources+before.States {
			t.Fatal("backup object coverage incomplete")
		}
		next, _ := testArtifacts(t)
		cfg := s.cfg
		cfg.Artifacts = next
		destination, e := New(restored, cfg, s.log)
		check(e)
		cursor := database.RecoveryArtifactParams{}
		var last database.RecoveryArtifactRow
		for {
			row, e := s.q.RecoveryArtifact(ctx, cursor)
			if e == pgx.ErrNoRows {
				break
			}
			check(e)
			last = row
			cursor = database.RecoveryArtifactParams{AfterKind: row.Kind, AfterID: row.ID}
		}
		envelope, e := s.artifactEnvelope(last.Ciphertext, last.ObjectKey)
		check(e)
		damagedPath := filepath.Join(artifactDirectory, envelope.Reference.SHA256+".age")
		intact, e := os.ReadFile(damagedPath)
		check(e)
		check(os.WriteFile(damagedPath, []byte("damaged"), 0600))
		first, e := s.q.RecoveryArtifact(ctx, database.RecoveryArtifactParams{})
		check(e)
		destroyArtifacts()
		if _, e = recovered.CheckArtifacts(ctx); e == nil {
			t.Fatal("original object loss was not detected")
		}
		if _, e = destination.RestoreArtifacts(ctx, artifactDirectory); e == nil {
			t.Fatal("damaged backup accepted")
		}
		unchanged, e := database.New(restored).RecoveryArtifact(ctx, database.RecoveryArtifactParams{})
		check(e)
		if !bytes.Equal(first.Ciphertext, unchanged.Ciphertext) {
			t.Fatal("failed restore committed partial references")
		}
		check(os.WriteFile(damagedPath, intact, 0600))
		restoredObjects, e := destination.RestoreArtifacts(ctx, artifactDirectory)
		check(e)
		if restoredObjects.Objects != backedUp.Objects || restoredObjects.Rebound != backedUp.Objects {
			t.Fatal("restore did not rebind all objects")
		}
		verified, e := destination.CheckArtifacts(ctx)
		check(e)
		if verified != before {
			t.Fatal("recovered object coverage changed")
		}
		retry, e := destination.RestoreArtifacts(ctx, artifactDirectory)
		check(e)
		if retry.Rebound != 0 || retry.Objects != backedUp.Objects {
			t.Fatal("restore retry rewrote references")
		}
		var audits int
		check(restored.QueryRow(ctx, "SELECT count(*) FROM audit_events WHERE action='automation.artifacts_restored'").Scan(&audits))
		if audits < 1 {
			t.Fatal("recovery audit missing")
		}
		check(restored.QueryRow(ctx, "SHOW default_transaction_read_only").Scan(&readonly))
		if readonly != "on" {
			t.Fatal("recovery changed database read-only default")
		}
		t.Logf("Original RustFS bucket deleted; %d encrypted objects recovered into a different versioned bucket, with rollback and retry verified.", restoredObjects.Objects)
	}
	t.Logf("Restored database decrypted and verified %d source bundles and %d state versions.", after.Sources, after.States)
	t.Logf("Encrypted backup restored %d application tables with identical row digests and read-only defaults.", len(tables))
}
