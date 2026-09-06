package launcher

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/alphabravo-oss/providah-community/sdk/provider"
)

func newTestLauncher(image string) (*Launcher, error) {
	return NewRuntimes([]provider.Runtime{{Provider: "aws", Image: image, Version: "test", SDKVersion: "test", Protocol: provider.Protocol}, {Provider: "digitalocean", Image: image, Version: "test", SDKVersion: "test", Protocol: provider.Protocol}, {Provider: "hetzner", Image: image, Version: "test", SDKVersion: "test", Protocol: provider.Protocol}})
}

func TestLaunchBoundary(t *testing.T) {
	if _, err := newTestLauncher("untrusted:latest"); err == nil {
		t.Fatal("mutable image reference accepted")
	}
	l, err := newTestLauncher("sha256:" + strings.Repeat("a", 64))
	if err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{`{}`, `{"provider":"aws","credential":"secret"}`, `{"image":"evil"}`, strings.Repeat("x", 65537)} {
		w := httptest.NewRecorder()
		l.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/discover", strings.NewReader(body)))
		if w.Code != 400 {
			t.Fatalf("malformed launch request: %d", w.Code)
		}
	}
}

// Opt-in smoke check exercises the actual container flags, stdin protocol, and cleanup.
func TestProviderContainer(t *testing.T) {
	image := os.Getenv("TEST_PROVIDER_IMAGE")
	if image == "" {
		t.Skip("set TEST_PROVIDER_IMAGE to a locally built provider image")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	id, err := ResolveImage(ctx, image)
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := LoadRuntimes(ctx, "", image, nil)
	if err != nil || len(catalog) != 3 {
		t.Fatalf("runtime health catalog: %v", err)
	}
	for _, r := range catalog {
		if r.Image != id || r.SDKVersion == "" {
			t.Fatal("runtime metadata not bound to image")
		}
	}
	l, err := NewRuntimes(catalog)
	if err != nil {
		t.Fatal(err)
	}
	r, err := l.run(ctx, provider.Request{RuntimeID: id, Version: provider.Protocol, OrganizationID: strings.Repeat("a", 64), ConnectionID: strings.Repeat("b", 64), Provider: "aws", Region: "us-east-1", Credential: "invalid-json-no-cloud-call"})
	if err != nil || r.Complete || len(r.Resources) != 0 || r.Error != "discovery_failed" {
		t.Fatalf("container protocol: %+v %v", r, err)
	}
	power := provider.Request{RuntimeID: id, Version: provider.Protocol, OrganizationID: strings.Repeat("a", 64), ConnectionID: strings.Repeat("b", 64), Provider: "aws", Region: "us-east-1", Credential: "invalid-json-no-cloud-call", Power: &provider.PowerRequest{OperationID: strings.Repeat("c", 64), Phase: "submit", Action: "shutdown", NativeID: "i-12345678", ExpectedStatus: "running"}}
	result, err := l.run(ctx, power)
	if err != nil || result.Validate() != nil || result.Power == nil || result.Power.Outcome != "failed" || result.Power.Error != "invalid_configuration" {
		t.Fatalf("power container boundary: %+v %v", result.Power, err)
	}
	power.Power = nil
	power.Metrics = &provider.MetricsRequest{NativeID: "i-12345678", Start: time.Now().Add(-time.Hour).Unix(), End: time.Now().Unix()}
	result, err = l.run(ctx, power)
	if err != nil || result.Validate() != nil || result.Metrics == nil || result.Metrics.Error != "unavailable" {
		t.Fatalf("metrics container boundary: %+v %v", result.Metrics, err)
	}

}

func TestRuntimeAdmission(t *testing.T) {
	a, b := "sha256:"+strings.Repeat("a", 64), "sha256:"+strings.Repeat("b", 64)
	approved := []provider.Runtime{{Provider: "aws", Image: a, Version: "1", SDKVersion: "1", Protocol: provider.Protocol}, {Provider: "hetzner", Image: b, Version: "2", SDKVersion: "2", Protocol: provider.Protocol}}
	l, err := NewRuntimes(approved)
	if err != nil {
		t.Fatal(err)
	}
	approved[0].Image = b // Caller mutation cannot change admission policy.
	for _, tc := range []struct {
		cloud, image string
		allowed      bool
	}{{"aws", a, true}, {"hetzner", b, true}, {"aws", b, false}, {"hetzner", a, false}, {"aws", "", false}, {"aws", "evil:latest", false}} {
		got, e := l.runtime(provider.Request{Provider: tc.cloud, RuntimeID: tc.image})
		if (e == nil) != tc.allowed || (e == nil && got != tc.image) {
			t.Fatal("runtime scope bypass", tc.cloud)
		}
	}
	bad := append([]provider.Runtime(nil), l.runtimes...)
	bad[0].Protocol++
	if _, err = NewRuntimes(bad); err == nil {
		t.Fatal("incompatible runtime admitted")
	}
	response := httptest.NewRecorder()
	l.ServeHTTP(response, httptest.NewRequest("GET", "/runtimes", nil))
	if response.Code != 200 || !strings.Contains(response.Body.String(), a) {
		t.Fatal("approved catalog missing")
	}
}

func TestRuntimeCapabilitySnapshot(t *testing.T) {
	r := provider.Runtime{Provider: "hetzner", Image: "sha256:" + strings.Repeat("a", 64), Version: "1", SDKVersion: "1", Protocol: provider.Protocol, CapabilitiesVersion: 1, InventoryKinds: []string{"compute.server"}, Actions: []string{"start"}}
	l, err := NewRuntimes([]provider.Runtime{r})
	if err != nil {
		t.Fatal(err)
	}
	r.Actions[0] = "delete"
	if _, err = l.runtime(provider.Request{Provider: r.Provider, RuntimeID: r.Image, Power: &provider.PowerRequest{Action: "delete"}}); err == nil {
		t.Fatal("mutable capability input changed launcher authority")
	}
	if _, err = l.runtime(provider.Request{Provider: r.Provider, RuntimeID: r.Image, InventoryKinds: []string{"compute.image"}}); err == nil {
		t.Fatal("unadvertised inventory reached runtime")
	}
}
