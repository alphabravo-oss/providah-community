package core

import (
	"context"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
	"go.opentelemetry.io/otel/trace/noop"
	"net/http"
	"strconv"
	"strings"
	"time"

	pb "github.com/alphabravo-oss/providah-community/internal/gen/providah/v1"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

type telemetry struct {
	storage          *prometheus.GaugeVec
	storageRefreshed prometheus.Gauge
	storageFailures  prometheus.Counter
	tracer           trace.Tracer
	registry         *prometheus.Registry
	requests         *prometheus.HistogramVec
	active           prometheus.Gauge
	backlog          *prometheus.GaugeVec
	oldest           *prometheus.GaugeVec
	refreshed        prometheus.Gauge
	failures         prometheus.Counter
}

func newTelemetry() *telemetry {
	t := &telemetry{tracer: noop.NewTracerProvider().Tracer("providah/core"), registry: prometheus.NewRegistry(),
		storage:          prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "providah_database_storage_bytes", Help: "Database allocation and operator-set budget in bytes; budget zero means unconfigured. Audit allocation is part of used bytes."}, []string{"kind"}),
		storageRefreshed: prometheus.NewGauge(prometheus.GaugeOpts{Name: "providah_database_storage_snapshot_timestamp_seconds", Help: "Last successful database size snapshot; zero until first success."}),
		storageFailures:  prometheus.NewCounter(prometheus.CounterOpts{Name: "providah_database_storage_snapshot_failures_total", Help: "Failed database size snapshots; last successful values are retained."}),
		requests:         prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: "providah_http_request_seconds", Help: "Completed HTTP request duration; SSE measures stream lifetime.", Buckets: []float64{.01, .05, .1, .25, .5, 1, 2.5, 5, 15, 30, 60}}, []string{"route", "method", "status"}),
		active:           prometheus.NewGauge(prometheus.GaugeOpts{Name: "providah_http_active_requests", Help: "Requests currently being served, including SSE streams."}),
		backlog:          prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "providah_work_items", Help: "Durable active or attention-required work in the last successful snapshot."}, []string{"kind", "status"}),
		oldest:           prometheus.NewGaugeVec(prometheus.GaugeOpts{Name: "providah_work_oldest_seconds", Help: "Age since creation of the oldest work item in each state, not time spent in state."}, []string{"kind", "status"}),
		refreshed:        prometheus.NewGauge(prometheus.GaugeOpts{Name: "providah_work_snapshot_timestamp_seconds", Help: "Last successful durable-work snapshot; zero until first success."}),
		failures:         prometheus.NewCounter(prometheus.CounterOpts{Name: "providah_work_snapshot_failures_total", Help: "Failed durable-work snapshots; previous values are retained."}),
	}
	t.registry.MustRegister(t.storage, t.storageRefreshed, t.storageFailures, t.requests, t.active, t.backlog, t.oldest, t.refreshed, t.failures, collectors.NewGoCollector(), collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))
	return t
}

// Only descriptor-backed method names enter labels or logs. Paths, queries, headers and bodies never do.
func telemetryRoute(path string) string {
	switch path {
	case "/healthz", "/readyz", "/api/events", "/api/oidc/callback":
		return path
	}
	const prefix = "/api/providah.v1.ConsoleService/"
	if name, ok := strings.CutPrefix(path, prefix); ok {
		methods := pb.File_providah_v1_console_proto.Services().ByName("ConsoleService").Methods()
		for i := 0; i < methods.Len(); i++ {
			if string(methods.Get(i).Name()) == name {
				return prefix + name
			}
		}
	}
	return "other"
}
func (s *Service) observeHTTP(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := randomID()
		r = r.WithContext(context.WithValue(r.Context(), middleware.RequestIDKey, id))
		w.Header().Set("X-Request-ID", id)
		wrapped := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
		start := time.Now()
		route := telemetryRoute(r.URL.Path)
		method := r.Method
		switch method {
		case "GET", "POST", "HEAD", "OPTIONS", "PUT", "PATCH", "DELETE":
		default:
			method = "other"
		}
		spanCtx, span := s.telemetry.tracer.Start(propagation.TraceContext{}.Extract(r.Context(), propagation.HeaderCarrier(r.Header)), method+" "+route, trace.WithSpanKind(trace.SpanKindServer), trace.WithAttributes(attribute.String("http.route", route), attribute.String("http.request.method", method), attribute.String("providah.request_id", id)))
		r = r.WithContext(spanCtx)
		defer span.End()
		s.telemetry.active.Inc()
		defer func() {
			s.telemetry.active.Dec()
			status := wrapped.Status()
			if status == 0 {
				status = 200
			}
			span.SetAttributes(attribute.Int("http.response.status_code", status))
			if status >= 500 {
				span.SetStatus(codes.Error, "server error")
			}
			duration := time.Since(start)
			s.telemetry.requests.WithLabelValues(route, method, strconv.Itoa(status)).Observe(duration.Seconds())
			if route != "/healthz" && route != "/readyz" {
				s.log.Info().Str("trace_id", span.SpanContext().TraceID().String()).Str("request_id", id).Str("route", route).Str("method", method).Int("status", status).Dur("duration_ms", duration).Msg("http request")
			}
		}()
		next.ServeHTTP(wrapped, r)
	})
}
func (s *Service) registerPoolMetrics() {
	if s.pool == nil {
		return
	}
	for name, value := range map[string]func() float64{
		"acquired": func() float64 { return float64(s.pool.Stat().AcquiredConns()) },
		"idle":     func() float64 { return float64(s.pool.Stat().IdleConns()) },
		"max":      func() float64 { return float64(s.pool.Stat().MaxConns()) },
		"total":    func() float64 { return float64(s.pool.Stat().TotalConns()) },
	} {
		s.telemetry.registry.MustRegister(prometheus.NewGaugeFunc(prometheus.GaugeOpts{Name: "providah_database_connections", Help: "Application PostgreSQL pool connections by state.", ConstLabels: prometheus.Labels{"state": name}}, value))
	}
}
func (s *Service) MetricsHandler() http.Handler {
	return promhttp.HandlerFor(s.telemetry.registry, promhttp.HandlerOpts{MaxRequestsInFlight: 2, Timeout: 5 * time.Second})
}
func (s *Service) StartTelemetry(ctx context.Context) {
	for {
		s.refreshTelemetry(ctx)
		select {
		case <-ctx.Done():
			return
		case <-time.After(15 * time.Second):
		}
	}
}

type workSample struct {
	kind, status string
	count        int64
	age          float64
}

func (s *Service) workSnapshot(ctx context.Context) ([]workSample, error) {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	// ponytail: one bounded aggregate snapshot per replica; use incremental counters if queue volume makes it expensive.
	rows, err := s.pool.Query(ctx, `SELECT kind,status,count(*),greatest(0,extract(epoch FROM now()-min(created_at)))::float8 FROM (
 SELECT 'operation' AS kind,status,created_at FROM operations WHERE status IN ('awaiting_approval','queued','dispatching','observing','uncertain')
 UNION ALL SELECT 'scan',status,created_at FROM scan_jobs WHERE status IN ('queued','running')
 UNION ALL SELECT 'delivery',status,created_at FROM notification_deliveries WHERE status IN ('pending','sending','dead')
 UNION ALL SELECT 'validation',status,created_at FROM automation_validations WHERE status IN ('queued','running')
 UNION ALL SELECT 'audit_export',status,created_at FROM audit_export_batches WHERE status IN ('pending','running')
 ) work GROUP BY kind,status ORDER BY kind,status`)

	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var samples []workSample
	for rows.Next() {
		var v workSample
		if err = rows.Scan(&v.kind, &v.status, &v.count, &v.age); err != nil {
			return nil, err
		}
		samples = append(samples, v)
	}
	return samples, rows.Err()
}
func (s *Service) refreshTelemetry(ctx context.Context) {
	storage, storageErr := s.databaseStorage(ctx)
	if storageErr != nil {
		s.telemetry.storageFailures.Inc()
	} else {
		for kind, value := range map[string]int64{"used": storage.UsedBytes, "audit": storage.AuditBytes, "budget": storage.BudgetBytes} {
			s.telemetry.storage.WithLabelValues(kind).Set(float64(value))
		}
		s.telemetry.storageRefreshed.SetToCurrentTime()
	}

	samples, err := s.workSnapshot(ctx)
	if err != nil {
		s.telemetry.failures.Inc()
		return
	}
	// Keep a fixed set of zero-valued series when queues drain.
	for kind, states := range map[string][]string{"operation": {"awaiting_approval", "queued", "dispatching", "observing", "uncertain"}, "scan": {"queued", "running"}, "delivery": {"pending", "sending", "dead"}, "validation": {"queued", "running"}, "audit_export": {"pending", "running"}} {
		for _, status := range states {
			s.telemetry.backlog.WithLabelValues(kind, status).Set(0)
			s.telemetry.oldest.WithLabelValues(kind, status).Set(0)
		}
	}
	for _, v := range samples {
		s.telemetry.backlog.WithLabelValues(v.kind, v.status).Set(float64(v.count))
		s.telemetry.oldest.WithLabelValues(v.kind, v.status).Set(v.age)
	}
	s.telemetry.refreshed.SetToCurrentTime()
}
