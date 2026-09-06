package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/alphabravo-oss/providah-community/internal/database"
	"github.com/alphabravo-oss/providah-community/internal/inputspec"
	"github.com/alphabravo-oss/providah-community/internal/sourcebundle"
	"github.com/alphabravo-oss/providah-community/internal/tfstate"
	"github.com/jackc/pgx/v5"
	"reflect"
	"slices"
)

type ArtifactCheckReport struct {
	Sources int `json:"sources"`
	States  int `json:"states"`
}

// CheckArtifacts reads one record at a time in a consistent, read-only snapshot.
// No runtime, cloud operation, secret output, object mutation or migration is involved.
func (s *Service) CheckArtifacts(ctx context.Context) (ArtifactCheckReport, error) {
	report := ArtifactCheckReport{}
	tx, e := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if e != nil {
		return report, errors.New("artifact snapshot unavailable")
	}
	defer func() { _ = tx.Rollback(ctx) }()
	report, e = s.checkArtifacts(ctx, database.New(tx))
	if e != nil {
		return report, e
	}
	if e = tx.Commit(ctx); e != nil {
		return report, errors.New("artifact snapshot could not complete")
	}
	return report, nil
}
func (s *Service) checkArtifacts(ctx context.Context, q *database.Queries) (ArtifactCheckReport, error) {
	report := ArtifactCheckReport{}
	for cursor := ""; ; {
		row, e := q.VerifyAutomationSource(ctx, cursor)
		if errors.Is(e, pgx.ErrNoRows) {
			break
		}
		if e != nil {
			return report, errors.New("source metadata unavailable")
		}
		raw, e := s.openArtifact(ctx, "sources/"+row.OrgID+"/"+row.ID, row.Storage, row.Ciphertext, sourcebundle.MaxArchive)
		if e != nil {
			return report, fmt.Errorf("source %s artifact unreadable", row.ID)
		}
		manifest, e := sourcebundle.Validate([]byte(raw), row.Runtime, row.Entrypoint)
		var inputs []inputspec.Field
		schemaErr := json.Unmarshal(row.InputSchema, &inputs)
		if len(inputs) == 0 {
			inputs = nil
		}
		if e != nil || schemaErr != nil || !reflect.DeepEqual(manifest.Inputs, inputs) || manifest.SHA256 != row.Sha256 || !slices.Equal(manifest.Files, row.Files) || manifest.ExpandedBytes != row.ExpandedBytes || len(raw) != int(row.ArchiveBytes) {
			return report, fmt.Errorf("source %s artifact metadata mismatch", row.ID)
		}
		report.Sources++
		cursor = row.ID
	}
	for cursor := ""; ; {
		row, e := q.VerifyAutomationState(ctx, cursor)
		if errors.Is(e, pgx.ErrNoRows) {
			break
		}
		if e != nil {
			return report, errors.New("state metadata unavailable")
		}
		raw, e := s.openArtifact(ctx, "states/"+row.OrgID+"/"+row.ProjectID+"/"+row.ID, row.Storage, row.Ciphertext, tfstate.MaxBytes)
		if e != nil {
			return report, fmt.Errorf("state %s artifact unreadable", row.ID)
		}
		meta, e := tfstate.Parse([]byte(raw))
		if e != nil || meta.SHA256 != row.Sha256 || meta.Serial != row.Serial || meta.Lineage != row.Lineage || len(raw) != int(row.StateBytes) {
			return report, fmt.Errorf("state %s artifact metadata mismatch", row.ID)
		}
		report.States++
		cursor = row.ID
	}
	invalid, e := q.InvalidAutomationStatePointer(ctx)
	if e != nil || invalid {
		return report, errors.New("state project pointer mismatch")
	}
	return report, nil
}
