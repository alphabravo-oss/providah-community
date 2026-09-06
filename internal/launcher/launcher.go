// Package launcher owns the narrow container-launch boundary. The public API never gets a Docker socket.
package launcher

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/alphabravo-oss/providah-community/internal/automation"
	"github.com/alphabravo-oss/providah-community/sdk/provider"
	"io"
	"net/http"
	"os/exec"
	"strings"
	"time"
)

type Launcher struct {
	automationRuntimes []automation.Runtime
	proxyImage         string
	runtimes           []provider.Runtime
	slots              chan struct{}
}

func NewRuntimes(runtimes []provider.Runtime) (*Launcher, error) {
	if err := provider.ValidateRuntimes(runtimes); err != nil {
		return nil, err
	}
	copy := append([]provider.Runtime(nil), runtimes...)
	for i := range copy {
		copy[i].InventoryKinds = append([]string(nil), copy[i].InventoryKinds...)
		copy[i].Actions = append([]string(nil), copy[i].Actions...)
	}
	return &Launcher{runtimes: copy, slots: make(chan struct{}, 4)}, nil
}
func (l *Launcher) runtime(r provider.Request) (string, error) {
	for _, allowed := range l.runtimes {
		if allowed.Provider == r.Provider && allowed.Image == r.RuntimeID && allowed.Accepts(r) {
			return allowed.Image, nil
		}
	}
	return "", errors.New("runtime not approved for provider")
}
func (l *Launcher) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method == "POST" && r.URL.Path == "/automation/validate" {
		l.validateAutomation(w, r)
		return
	}
	if r.Method == "GET" && r.URL.Path == "/runtimes" {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(l.runtimes)
		return
	}
	if r.Method != "POST" || (r.URL.Path != "/discover" && r.URL.Path != "/execute" && r.URL.Path != "/metrics") {
		http.NotFound(w, r)
		return
	}
	var req provider.Request
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 64<<10))
	d.DisallowUnknownFields()
	if d.Decode(&req) != nil || req.Validate() != nil || (r.URL.Path == "/execute") != (req.Power != nil) || (r.URL.Path == "/metrics") != (req.Metrics != nil) || d.Decode(&struct{}{}) != io.EOF {
		http.Error(w, "Invalid discovery request", 400)
		return
	}
	if _, err := l.runtime(req); err != nil {
		http.Error(w, "Provider runtime is not approved", 400)
		return
	}
	select {
	case l.slots <- struct{}{}:
		defer func() { <-l.slots }()
	default:
		http.Error(w, "Provider capacity is busy", http.StatusServiceUnavailable)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 130*time.Second)
	defer cancel()
	result, err := l.run(ctx, req)
	if err != nil {
		http.Error(w, "Provider worker failed", http.StatusBadGateway)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(result)
}

type boundedBuffer struct{ bytes.Buffer }

func (b *boundedBuffer) Write(p []byte) (int, error) {
	if b.Len()+len(p) > provider.MaxResponseBytes {
		return 0, errors.New("worker response limit")
	}
	return b.Buffer.Write(p)
}
func (l *Launcher) run(ctx context.Context, r provider.Request) (provider.Response, error) {
	var result provider.Response
	image, err := l.runtime(r)
	if err != nil {
		return result, err
	}
	suffix := make([]byte, 16)
	if _, err := rand.Read(suffix); err != nil {
		return result, err
	}
	name := "providah-scan-" + hex.EncodeToString(suffix)
	payload, err := json.Marshal(r)
	if err != nil {
		return result, err
	}
	args := append([]string{"run"}, expiryLabels()...)
	args = append(args, "--rm", "--pull=never", "--name", name, "--label", "providah.worker=discovery", "--label", "providah.organization="+r.OrganizationID, "-i", "--read-only", "--cap-drop=ALL", "--security-opt=no-new-privileges", "--pids-limit=128", "--memory=512m", "--cpus=1", "--user=65532:65532", "--network=bridge", image)
	cmd := exec.CommandContext(ctx, "docker", args...)
	cmd.Stdin = bytes.NewReader(payload)
	cmd.Stderr = io.Discard
	var out boundedBuffer
	cmd.Stdout = &out
	// Killing the attached CLI does not guarantee daemon-side container termination.
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = exec.CommandContext(cleanup, "docker", "rm", "-f", name).Run()
	}()
	if err = cmd.Run(); err != nil {
		return result, errors.New("worker execution failed")
	}
	d := json.NewDecoder(&out)
	d.DisallowUnknownFields()
	if d.Decode(&result) != nil || d.Decode(&struct{}{}) != io.EOF {
		return result, errors.New("invalid worker output")
	}
	if result.Error != "" {
		switch result.Error {
		case "invalid_configuration", "discovery_failed", "invalid_provider_response":
		default:
			return result, errors.New("invalid worker error")
		}
		return result, nil
	}
	return result, result.Validate()
}
func ResolveImage(ctx context.Context, reference string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if reference == "" || len(reference) > 1024 || strings.HasPrefix(reference, "-") {
		return "", errors.New("PROVIDER_IMAGE is required")
	}
	out, err := exec.CommandContext(ctx, "docker", "image", "inspect", "--format", "{{.Id}}", reference).Output()
	if err != nil {
		return "", errors.New("build or load the configured worker image before starting the launcher")
	}
	return strings.TrimSpace(string(out)), nil
}
