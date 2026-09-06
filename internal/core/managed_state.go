package core

import (
	"connectrpc.com/connect"
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"github.com/alphabravo-oss/providah-community/internal/database"
	"github.com/alphabravo-oss/providah-community/internal/tfstate"
	"github.com/jackc/pgx/v5"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"
)

const statePath = "/api/automation-state/"

var stateID = regexp.MustCompile(`^[a-f0-9]{64}$`)

// Internal only: the approved execution dispatcher must issue this capability.
// No human RPC returns raw-state access. Recovery requires a separate workflow.
func (s *Service) newStateSession(ctx context.Context, org, project string, p principal, writable bool) (string, string, error) {
	id, secret := randomID(), randomID()
	e := s.transaction(ctx, func(q *database.Queries) error {
		permission := "templates.read"
		if writable {
			permission = "templates.publish"
		}
		if _, e := q.LockAccess(ctx, org); e != nil {
			return e
		}
		if !hasPermission(ctx, q, org, p.UserID, permission) || !identityAllowed(ctx, q, org, p.UserID, p.OIDC) {
			return denied()
		}
		if _, e := q.LockAutomationProject(ctx, database.LockAutomationProjectParams{OrgID: org, ID: project}); e != nil {
			return e
		}
		return q.CreateAutomationStateSession(ctx, database.CreateAutomationStateSessionParams{ID: id, OrgID: org, ProjectID: project, RequesterID: p.UserID, RequesterOidcID: p.OIDC, SecretHash: hash(secret), Writable: writable})
	})
	if e != nil {
		return "", "", e
	}
	return id, secret, nil
}

func (s *Service) automationState(w http.ResponseWriter, r *http.Request) {
	// Browser cookies, cached Basic credentials and cross-origin requests cannot
	// substitute for a job capability. Bodies and lock info never enter logs.
	if r.Header.Get("Origin") != "" || r.Header.Get("Sec-Fetch-Site") != "" {
		http.Error(w, "Runner authentication required", http.StatusForbidden)
		return
	}
	project := strings.TrimPrefix(r.URL.Path, statePath)
	user, password, ok := r.BasicAuth()
	if !ok || !stateID.MatchString(project) || !stateID.MatchString(user) || !stateID.MatchString(password) {
		http.Error(w, "Runner authentication required", http.StatusUnauthorized)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	session, e := s.q.AutomationStateSession(ctx, database.AutomationStateSessionParams{ID: user, ProjectID: project})
	if e != nil || subtle.ConstantTimeCompare(session.SecretHash, hash(password)) != 1 {
		http.Error(w, "Runner authentication required", http.StatusUnauthorized)
		return
	}
	switch r.Method {
	case "GET", "LOCK", "UNLOCK", "POST":
	default:
		w.Header().Set("Allow", "GET, POST, LOCK, UNLOCK")
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if r.Method != "GET" && !session.Writable {
		http.Error(w, "State write capability required", http.StatusForbidden)
		return
	}
	// Bound concurrent secret-bearing buffers before reading a potentially large body.
	select {
	case s.stateSlots <- struct{}{}:
		defer func() { <-s.stateSlots }()
	case <-ctx.Done():
		http.Error(w, "State capacity is busy", http.StatusServiceUnavailable)
		return
	}
	var raw []byte
	var meta tfstate.Metadata
	var lock struct{ ID string }
	if r.Method != "GET" {
		limit := int64(16 << 10)
		if r.Method == "POST" {
			limit = tfstate.MaxBytes
		}
		raw, e = io.ReadAll(http.MaxBytesReader(w, r.Body, limit))
		if e != nil {
			http.Error(w, "State request too large", http.StatusRequestEntityTooLarge)
			return
		}
		if r.Method == "POST" {
			meta, e = tfstate.Parse(raw)
			if e != nil {
				http.Error(w, "Invalid state identity or format", 400)
				return
			}
		} else if json.Unmarshal(raw, &lock) != nil || !tfstate.UUID.MatchString(lock.ID) {
			http.Error(w, "Invalid lock identity", 400)
			return
		}
	}
	status := 200
	var body []byte
	lockConflict := ""
	e = s.transaction(ctx, func(q *database.Queries) error {
		if _, e := q.LockAccess(ctx, session.OrgID); e != nil {
			return e
		}
		// Recheck inside the same organization lock used for permission changes.
		current, e := q.AutomationStateSession(ctx, database.AutomationStateSessionParams{ID: user, ProjectID: project})
		if e != nil {
			return denied()
		}
		permission := "templates.read"
		if r.Method != "GET" {
			permission = "templates.publish"
		}
		if r.Method != "GET" && !current.Writable {
			return denied()
		}
		if !hasPermission(ctx, q, current.OrgID, current.RequesterID, permission) || !identityAllowed(ctx, q, current.OrgID, current.RequesterID, current.RequesterOidcID) {
			return denied()
		}
		p, e := q.LockAutomationProject(ctx, database.LockAutomationProjectParams{OrgID: current.OrgID, ID: project})
		if e != nil {
			return e
		}
		switch r.Method {
		case "GET":
			if p.StateID == "" {
				status = 404
				return nil
			}
			state, e := q.CurrentAutomationState(ctx, database.CurrentAutomationStateParams{OrgID: p.OrgID, ID: p.ID})
			if e != nil {
				return e
			}
			plain, e := s.openArtifact(ctx, "states/"+p.OrgID+"/"+p.ID+"/"+state.ID, state.Storage, state.Ciphertext, tfstate.MaxBytes)
			if e != nil {
				return e
			}
			m, e := tfstate.Parse([]byte(plain))
			if e != nil || m.SHA256 != p.Sha256 || m.Lineage != p.Lineage || m.Serial != p.Serial || len(plain) != int(p.StateBytes) {
				return errors.New("stored state integrity mismatch")
			}
			body = []byte(plain)
			return nil
		case "LOCK":
			if p.LockID != "" {
				if p.LockID == lock.ID && p.LockSessionID == user {
					return nil
				}
				status = 423
				lockConflict = p.LockID
				return nil
			}
			if e = q.SetAutomationStateLock(ctx, database.SetAutomationStateLockParams{OrgID: p.OrgID, ID: p.ID, LockID: lock.ID, LockSessionID: user}); e != nil {
				return e
			}
			return audit(ctx, q, p.OrgID, "runner:"+current.RequesterID, "automation.state_locked", p.ID, nil)
		case "UNLOCK":
			if p.LockID == "" {
				return nil
			}
			if p.LockID != lock.ID || p.LockSessionID != user {
				status = 409
				lockConflict = p.LockID
				return nil
			}
			if e = q.SetAutomationStateLock(ctx, database.SetAutomationStateLockParams{OrgID: p.OrgID, ID: p.ID}); e != nil {
				return e
			}
			return audit(ctx, q, p.OrgID, "runner:"+current.RequesterID, "automation.state_unlocked", p.ID, nil)
		case "POST":
			ids := r.URL.Query()["ID"]
			if p.LockID == "" || p.LockSessionID != user || len(ids) != 1 || ids[0] != p.LockID {
				status = 409
				lockConflict = p.LockID
				return nil
			}
			if p.Serial >= 0 && (p.Lineage != meta.Lineage || meta.Serial < p.Serial || meta.Serial == p.Serial && meta.SHA256 != p.Sha256) {
				status = 409
				return nil
			}
			if meta.Serial == p.Serial {
				return nil
			} // Byte-identical retry, including after a lost response.
			id := randomID()
			ciphertext, storage, e := s.sealArtifact(ctx, "states/"+p.OrgID+"/"+p.ID+"/"+id, raw)
			if e != nil {
				return e
			}
			if e = q.InsertAutomationState(ctx, database.InsertAutomationStateParams{ID: id, OrgID: p.OrgID, ProjectID: p.ID, Lineage: meta.Lineage, Serial: meta.Serial, Sha256: meta.SHA256, StateBytes: int32(len(raw)), Ciphertext: ciphertext, Storage: storage}); e != nil { // #nosec G115 -- The state request body is limited to tfstate.MaxBytes before parsing.
				return e
			}
			if e = q.AdvanceAutomationState(ctx, database.AdvanceAutomationStateParams{OrgID: p.OrgID, ID: p.ID, StateID: id, Lineage: meta.Lineage, Serial: meta.Serial, Sha256: meta.SHA256, StateBytes: int32(len(raw))}); e != nil { // #nosec G115 -- The state request body is limited to tfstate.MaxBytes before parsing.
				return e
			}
			if e = s.indexOwnership(ctx, q, database.AutomationState{ID: id, OrgID: p.OrgID, ProjectID: p.ID}, raw); e != nil {
				return e
			}
			return audit(ctx, q, p.OrgID, "runner:"+current.RequesterID, "automation.state_written", p.ID, map[string]any{"state_id": id, "serial": meta.Serial, "sha256": meta.SHA256})
		}
		return nil
	})
	if e != nil {
		if errors.Is(e, pgx.ErrNoRows) || connect.CodeOf(e) == connect.CodePermissionDenied {
			http.Error(w, "State unavailable", http.StatusForbidden)
		} else {
			s.log.Error().Msg("managed state request failed")
			http.Error(w, "State request could not be completed", 500)
		}
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if lockConflict != "" {
		_ = json.NewEncoder(w).Encode(struct{ ID string }{lockConflict})
	} else if body != nil {
		_, _ = w.Write(body)
	}
}
