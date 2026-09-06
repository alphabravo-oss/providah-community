package core

import (
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime/debug"
	"strings"
	"time"

	"connectrpc.com/connect"
	"github.com/alphabravo-oss/providah-community/internal/gen/providah/v1/providahv1connect"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

func (s *Service) Handler(staticDir string) http.Handler {
	r := chi.NewRouter()
	r.Use(s.observeHTTP, middleware.Recoverer)
	r.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("X-Content-Type-Options", "nosniff")
			w.Header().Set("Referrer-Policy", "no-referrer")
			w.Header().Set("X-Frame-Options", "DENY")
			if strings.HasPrefix(r.URL.Path, "/api/") {
				w.Header().Set("Cache-Control", "no-store")
				if strings.HasPrefix(r.URL.Path, statePath) {
					s.automationState(w, r)
					return
				}
				if r.URL.Path == "/api/oidc/callback" && r.Method == "GET" {
					host, _, _ := net.SplitHostPort(r.RemoteAddr)
					if err := s.limit(r.Context(), "auth-peer:"+host); err != nil {
						http.Error(w, "Authentication temporarily rate limited", http.StatusTooManyRequests)
						return
					}
					next.ServeHTTP(w, r)
					return
				}
				if r.URL.Path == "/api/events" {
					if r.Method != "GET" || (r.Header.Get("Origin") != s.cfg.Origin && (r.Header.Get("Origin") != "" || r.Header.Get("Sec-Fetch-Site") != "same-origin")) {
						http.Error(w, "Origin or method not allowed", http.StatusForbidden)
						return
					}
					next.ServeHTTP(w, r)
					return
				}
				if r.Method != "POST" {
					http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
					return
				}
				if r.Header.Get("Origin") != s.cfg.Origin {
					http.Error(w, "Origin not allowed", http.StatusForbidden)
					return
				}
				limit := int64(64 << 10)
				if r.URL.Path == "/api"+providahv1connect.ConsoleServiceImportAutomationSourceProcedure {
					limit = 6 << 20
				}
				r.Body = http.MaxBytesReader(w, r.Body, limit)
				if strings.HasSuffix(r.URL.Path, "/Login") || strings.HasSuffix(r.URL.Path, "/BeginOIDCLogin") || strings.HasSuffix(r.URL.Path, "/CompleteOIDCLogin") || strings.HasSuffix(r.URL.Path, "/BeginSetup") || strings.HasSuffix(r.URL.Path, "/FinishSetup") || strings.HasSuffix(r.URL.Path, "/BeginInvitation") || strings.HasSuffix(r.URL.Path, "/AcceptInvitation") {
					host, _, _ := net.SplitHostPort(r.RemoteAddr)
					if err := s.limit(r.Context(), "auth-peer:"+host); err != nil {
						http.Error(w, "Authentication temporarily rate limited", http.StatusTooManyRequests)
						return
					}
				}
			}
			next.ServeHTTP(w, r)
		})
	})
	r.Get("/api/events", s.events)
	r.Get("/api/oidc/callback", s.oidcCallback)
	r.Get("/version", func(w http.ResponseWriter, r *http.Request) {
		revision := "development"
		if info, ok := debug.ReadBuildInfo(); ok {
			for _, setting := range info.Settings {
				if setting.Key == "vcs.revision" {
					revision = setting.Value
				}
			}
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"edition": s.cfg.Edition, "revision": revision})
	})
	r.Get("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte("ok\n"))
	})
	r.Get("/readyz", func(w http.ResponseWriter, r *http.Request) {
		if err := s.pool.Ping(r.Context()); err != nil {
			http.Error(w, "Database unavailable", http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte("ready\n"))
	})
	path, handler := providahv1connect.NewConsoleServiceHandler(s, connect.WithInterceptors(s.Interceptor()), connect.WithConditionalHandlerOptions(func(spec connect.Spec) []connect.HandlerOption {
		limit := 64 << 10
		if spec.Procedure == providahv1connect.ConsoleServiceImportAutomationSourceProcedure {
			limit = 6 << 20
		}
		return []connect.HandlerOption{connect.WithReadMaxBytes(limit)}
	}))
	r.Mount("/api"+path, http.StripPrefix("/api", http.TimeoutHandler(handler, 30*time.Second, "Request timed out")))
	if staticDir != "" {
		r.Get("/*", func(w http.ResponseWriter, r *http.Request) {
			clean := filepath.Clean("/" + r.URL.Path)
			target := filepath.Join(staticDir, clean)
			if info, err := os.Stat(target); err == nil && !info.IsDir() { // #nosec G703 -- Path is cleaned from a rooted URL and joined to the immutable deployment static directory.
				http.ServeFile(w, r, target) // #nosec G703 -- Target is confined by the rooted clean path above; static deployment assets are trusted.
				return
			}
			w.Header().Set("Cache-Control", "no-cache")
			http.ServeFile(w, r, filepath.Join(staticDir, "index.html"))
		})
	}
	return r
}
