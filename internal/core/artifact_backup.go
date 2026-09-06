package core

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/alphabravo-oss/providah-community/internal/artifactstore"
	"github.com/alphabravo-oss/providah-community/internal/database"
	"github.com/jackc/pgx/v5"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"syscall"
)

type ArtifactBackupReport struct {
	Objects int `json:"objects"`
	Rebound int `json:"rebound"`
}

var artifactDigest = regexp.MustCompile(`^[a-f0-9]{64}$`)

// BackupArtifacts writes ciphertext only into a newly created private directory.
// Freeze writers while pairing this bundle with the separately encrypted database backup.
func (s *Service) BackupArtifacts(ctx context.Context, directory string) (ArtifactBackupReport, error) {
	report := ArtifactBackupReport{}
	if e := os.Mkdir(directory, 0700); e != nil {
		return report, errors.New("artifact backup requires a new directory")
	}
	root, e := os.OpenRoot(directory)
	if e != nil {
		return report, errors.New("artifact backup directory unavailable")
	}
	defer func() { _ = root.Close() }()
	tx, e := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if e != nil {
		return report, e
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := database.New(tx)
	if _, e = s.checkArtifacts(ctx, q); e != nil {
		return report, e
	}
	cursor := database.RecoveryArtifactParams{}
	for {
		row, e := q.RecoveryArtifact(ctx, cursor)
		if errors.Is(e, pgx.ErrNoRows) {
			break
		}
		if e != nil {
			return report, errors.New("artifact inventory unavailable")
		}
		envelope, e := s.artifactEnvelope(row.Ciphertext, row.ObjectKey)
		if e != nil {
			return report, e
		}
		data, e := s.cfg.Artifacts.Read(ctx, envelope.Reference)
		if e != nil {
			return report, e
		}
		if e = writeBackupFile(root, envelope.Reference.SHA256+".age", data); e != nil {
			return report, e
		}
		report.Objects++
		cursor = database.RecoveryArtifactParams{AfterKind: row.Kind, AfterID: row.ID}
	}
	if e = tx.Commit(ctx); e != nil {
		return report, errors.New("backup snapshot failed")
	}
	if e = writeBackupFile(root, "complete.json", []byte(`{"format":1}`)); e != nil {
		return report, e
	}
	folder, e := os.Open(directory)
	if e != nil {
		return report, e
	}
	defer func() { _ = folder.Close() }()
	if e = folder.Sync(); e != nil {
		return report, e
	}
	parent, e := os.Open(filepath.Dir(directory))
	if e != nil {
		return report, e
	}
	defer func() { _ = parent.Close() }()
	if e = parent.Sync(); e != nil {
		return report, e
	}
	return report, nil
}
func writeBackupFile(root *os.Root, name string, data []byte) error {
	f, e := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if e != nil {
		return errors.New("backup file already exists or cannot be created")
	}
	_, e = f.Write(data)
	if e == nil {
		e = f.Sync()
	}
	closeErr := f.Close()
	if e == nil {
		e = closeErr
	}
	if e != nil {
		return errors.New("backup file could not be persisted")
	}
	return nil
}
func (s *Service) artifactEnvelope(cipher []byte, key string) (artifactEnvelope, error) {
	var envelope artifactEnvelope
	raw, e := s.open(cipher)
	if e != nil || json.Unmarshal([]byte(raw), &envelope) != nil || envelope.Reference.Key != key || !artifactDigest.MatchString(envelope.Reference.SHA256) || envelope.Reference.Bytes < 1 || envelope.Reference.Bytes > artifactstore.MaxBytes {
		return envelope, errors.New("invalid recovery envelope")
	}
	return envelope, nil
}

// RestoreArtifacts is restricted to disconnected restore databases. It never deletes
// or overwrites an object and atomically rebinds all envelopes after verification.
func (s *Service) RestoreArtifacts(ctx context.Context, directory string) (ArtifactBackupReport, error) {
	report := ArtifactBackupReport{}
	var db, readonly string
	if e := s.pool.QueryRow(ctx, "SELECT current_database(),current_setting('default_transaction_read_only')").Scan(&db, &readonly); e != nil || !regexp.MustCompile(`^providah_restore_[a-z0-9_]{1,40}$`).MatchString(db) || readonly != "on" {
		return report, errors.New("artifact restore requires a providah_restore_ database with read-only defaults; fence all application replicas")
	}
	if s.cfg.Artifacts == nil {
		return report, errors.New("destination artifact store required")
	}
	root, e := os.OpenRoot(directory)
	if e != nil {
		return report, errors.New("artifact backup directory unavailable")
	}
	defer func() { _ = root.Close() }()
	raw, e := readBackupFile(root, "complete.json", 64)
	if e != nil || string(raw) != `{"format":1}` {
		return report, errors.New("artifact backup format invalid")
	}
	tx, e := s.pool.BeginTx(ctx, pgx.TxOptions{AccessMode: pgx.ReadWrite})
	if e != nil {
		return report, e
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, e = tx.Exec(ctx, "SET LOCAL lock_timeout='10s'"); e != nil {
		return report, e
	}
	var locked bool
	if e = tx.QueryRow(ctx, "SELECT pg_try_advisory_xact_lock(724193806214)").Scan(&locked); e != nil || !locked {
		return report, errors.New("another maintenance operation is running")
	}
	if _, e = tx.Exec(ctx, "LOCK TABLE automation_projects,automation_states,automation_sources IN ACCESS EXCLUSIVE MODE"); e != nil {
		return report, errors.New("could not lock artifact metadata; stop application replicas")
	}
	q := database.New(tx)
	cursor := database.RecoveryArtifactParams{}
	orgs := map[string]bool{}
	for {
		row, e := q.RecoveryArtifact(ctx, cursor)
		if errors.Is(e, pgx.ErrNoRows) {
			break
		}
		if e != nil {
			return report, errors.New("artifact inventory unavailable")
		}
		envelope, e := s.artifactEnvelope(row.Ciphertext, row.ObjectKey)
		if e != nil {
			return report, e
		}
		data, e := readBackupFile(root, envelope.Reference.SHA256+".age", envelope.Reference.Bytes)
		h := sha256.Sum256(data)
		if e != nil || int64(len(data)) != envelope.Reference.Bytes || hex.EncodeToString(h[:]) != envelope.Reference.SHA256 {
			return report, errors.New("backup object integrity mismatch")
		}
		ref, e := s.cfg.Artifacts.Restore(ctx, row.ObjectKey, data)
		if e != nil {
			return report, e
		}
		report.Objects++
		if ref != envelope.Reference {
			envelope.Reference = ref
			raw, e := json.Marshal(envelope)
			if e != nil {
				return report, e
			}
			cipher, e := s.seal(string(raw))
			if e != nil {
				return report, e
			}
			if row.Kind == "sources" {
				e = q.RebindSourceArtifact(ctx, database.RebindSourceArtifactParams{ID: row.ID, Ciphertext: cipher})
			} else {
				e = q.RebindStateArtifact(ctx, database.RebindStateArtifactParams{ID: row.ID, Ciphertext: cipher})
			}
			if e != nil {
				return report, errors.New("artifact rebind failed")
			}
			report.Rebound++
			orgs[row.OrgID] = true
		}
		cursor = database.RecoveryArtifactParams{AfterKind: row.Kind, AfterID: row.ID}
	}
	if _, e = s.checkArtifacts(ctx, q); e != nil {
		return report, e
	}
	for org := range orgs {
		if e = audit(ctx, q, org, "system:artifact-recovery", "automation.artifacts_restored", "installation", nil); e != nil {
			return report, errors.New("recovery audit failed")
		}
	}
	if e = tx.Commit(ctx); e != nil {
		return report, errors.New("artifact recovery commit failed")
	}
	return report, nil
}

func readBackupFile(root *os.Root, name string, limit int64) ([]byte, error) {
	f, e := root.OpenFile(name, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if e != nil {
		return nil, errors.New("backup object unavailable")
	}
	defer func() { _ = f.Close() }()
	info, e := f.Stat()
	if e != nil || !info.Mode().IsRegular() || info.Size() > limit {
		return nil, errors.New("backup object must be a bounded regular file")
	}
	return io.ReadAll(io.LimitReader(f, limit+1))
}
