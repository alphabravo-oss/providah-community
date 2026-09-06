package core

import (
	"fmt"
	"github.com/alphabravo-oss/providah-community/internal/database"
	"net/http"
	"time"
)

// Event IDs are durable organization revisions. Reconnect sends a snapshot invalidation,
// so clients recover changes missed during disconnection without an in-memory event bus.
func (s *Service) events(w http.ResponseWriter, r *http.Request) {
	p, err := s.authenticate(r.Context(), r.Header)
	if err != nil {
		http.Error(w, "Sign in", http.StatusUnauthorized)
		return
	}
	org := r.URL.Query().Get("organization_id")
	revision, err := s.q.EventRevision(r.Context(), database.EventRevisionParams{ID: org, UserID: p.UserID})
	if err != nil || !identityAllowed(r.Context(), s.q, org, p.UserID, p.OIDC) {
		http.Error(w, "Access denied", http.StatusForbidden)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache, no-store")
	w.Header().Set("X-Accel-Buffering", "no")
	control := http.NewResponseController(w)
	send := func(event string, revision int64) bool {
		if control.SetWriteDeadline(time.Now().Add(5*time.Second)) != nil {
			return false
		}
		if _, err := fmt.Fprintf(w, "id: %d\nevent: %s\ndata: {}\n\n", revision, event); err != nil {
			return false
		}
		return control.Flush() == nil
	}
	if !send("change", revision) {
		return
	}
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	// ponytail: one cheap revision lookup per open stream every two seconds; add a shared
	// LISTEN/NOTIFY wakeup when measured concurrent sessions justify it.
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			if _, err = s.authenticate(r.Context(), r.Header); err != nil {
				send("reauth", revision)
				return
			}
			next, err := s.q.EventRevision(r.Context(), database.EventRevisionParams{ID: org, UserID: p.UserID})
			if err != nil || !identityAllowed(r.Context(), s.q, org, p.UserID, p.OIDC) {
				send("revoked", revision)
				return
			}
			event := "heartbeat"
			if next != revision {
				event = "change"
				revision = next
			}
			if !send(event, revision) {
				return
			}
		}
	}
}
