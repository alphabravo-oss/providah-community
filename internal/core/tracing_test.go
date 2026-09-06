package core

import (
	"context"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/rs/zerolog"
	collector "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	"google.golang.org/protobuf/proto"
)

func TestOTLPExport(t *testing.T) {
	var mu sync.Mutex
	var spans int
	collectorServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
		if err != nil {
			t.Error(err)
		}
		var request collector.ExportTraceServiceRequest
		if r.URL.Path != "/v1/traces" || proto.Unmarshal(body, &request) != nil {
			t.Error("invalid OTLP request")
		}
		if strings.Contains(request.String(), "secret-test-value") {
			t.Error("sensitive data in trace")
		}
		mu.Lock()
		defer mu.Unlock()
		for _, resource := range request.ResourceSpans {
			for _, scope := range resource.ScopeSpans {
				for _, span := range scope.Spans {
					spans++
					if span.Name != "POST other" || hex.EncodeToString(span.ParentSpanId) != "2222222222222222" || hex.EncodeToString(span.TraceId) != "11111111111111111111111111111111" || len(span.Attributes) != 4 {
						t.Error("unbounded span shape or lost parent")
					}
				}
			}
		}
		w.Header().Set("Content-Type", "application/x-protobuf")
	}))
	defer collectorServer.Close()
	s := &Service{telemetry: newTelemetry(), log: zerolog.Nop()}
	shutdown, err := s.ConfigureTracing(context.Background(), collectorServer.URL+"/v1/traces", 1)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest("POST", "/secret-test-value?code=secret-test-value", strings.NewReader("secret-test-value"))
	request.Header.Set("traceparent", "00-11111111111111111111111111111111-2222222222222222-01")
	s.observeHTTP(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(503) })).ServeHTTP(httptest.NewRecorder(), request)
	if err = shutdown(context.Background()); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if spans != 1 {
		t.Fatalf("got %d spans", spans)
	}
	for _, endpoint := range []string{"http://remote.invalid/v1/traces", "https://user:pass@remote.invalid", "https://remote.invalid/?secret=x"} {
		if _, err = s.ConfigureTracing(context.Background(), endpoint, 1); err == nil {
			t.Fatal("unsafe endpoint accepted")
		}
	}
}
