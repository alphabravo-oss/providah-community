package launcher

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/alphabravo-oss/providah-community/internal/automation"
	"io"
	"net/http"
	"os/exec"
	"time"
)

func (l *Launcher) ConfigureAutomation(ctx context.Context, raw, proxyRef string) error {
	runtimes, e := automation.ParseRuntimes(raw)
	if e != nil {
		return e
	}
	for _, r := range runtimes {
		id, e := ResolveImage(ctx, r.Image)
		if e != nil || id != r.Image {
			return errors.New("automation image must be present locally at its configured immutable ID")
		}
	}
	for _, r := range runtimes {
		if len(r.DependencyHosts) > 0 {
			image, e := ResolveImage(ctx, proxyRef)
			if e != nil {
				return e
			}
			l.proxyImage = image
			break
		}
	}
	l.automationRuntimes = runtimes
	return nil
}
func (l *Launcher) validateAutomation(w http.ResponseWriter, r *http.Request) {
	var req automation.Request
	d := json.NewDecoder(http.MaxBytesReader(w, r.Body, 6<<20))
	d.DisallowUnknownFields()
	if d.Decode(&req) != nil || d.Decode(new(any)) != io.EOF {
		http.Error(w, "Invalid validation request", 400)
		return
	}
	allowed := false
	for _, runtime := range l.automationRuntimes {
		allowed = allowed || runtime.Matches(req.Runtime, req.Image, runtime.Version, req.RuntimePolicy)
	}
	if !allowed || req.Validate() != nil {
		http.Error(w, "Source or runtime is not approved", 400)
		return
	}
	select {
	case l.slots <- struct{}{}:
		defer func() { <-l.slots }()
	default:
		http.Error(w, "Worker capacity is busy", http.StatusServiceUnavailable)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 130*time.Second)
	defer cancel()
	result, e := l.runAutomation(ctx, req)
	if e != nil {
		http.Error(w, "Validation worker failed", http.StatusBadGateway)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(result)
}
func (l *Launcher) runAutomation(ctx context.Context, r automation.Request) (automation.Result, error) {
	var result automation.Result
	suffix := make([]byte, 16)
	if _, e := rand.Read(suffix); e != nil {
		return result, e
	}
	name := "providah-validation-" + hex.EncodeToString(suffix)
	var hosts []string
	for _, runtime := range l.automationRuntimes {
		if runtime.Matches(r.Runtime, r.Image, runtime.Version, r.RuntimePolicy) {
			hosts = runtime.DependencyHosts
		}
	}
	r.DependencyProxy = len(hosts) > 0
	volume := ""
	var e error
	if r.DependencyProxy {
		var cleanup func()
		volume, cleanup, e = l.startProxy(ctx, name, hosts)
		if e != nil {
			return result, e
		}
		defer cleanup()
	}
	payload, e := json.Marshal(r)
	if e != nil {
		return result, e
	}
	args := append([]string{"run"}, expiryLabels()...)
	args = append(args, "--rm", "--pull=never", "--name", name, "--label", "providah.worker=validation", "-i", "--read-only", "--cap-drop=ALL", "--security-opt=no-new-privileges", "--pids-limit=128", "--memory=512m", "--cpus=1", "--user=65532:65532", "--network=none", "--tmpfs", "/work:rw,exec,nosuid,nodev,size=128m,uid=65532,gid=65532,mode=0700", "--tmpfs", "/tmp:rw,nosuid,nodev,size=64m,uid=65532,gid=65532,mode=0700", "--entrypoint", "/providah-automation", r.Image)
	if volume != "" {
		args = append(args[:len(args)-1], "--mount", "type=volume,src="+volume+",dst=/run/egress,readonly,volume-nocopy", r.Image)
	}
	cmd := exec.CommandContext(ctx, "docker", args...)
	cmd.WaitDelay = time.Second
	cmd.Stdin = bytes.NewReader(payload)
	cmd.Stderr = io.Discard
	var out boundedBuffer
	cmd.Stdout = &out
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = exec.CommandContext(cleanup, "docker", "rm", "-f", name).Run()
	}()
	if e = cmd.Run(); e != nil {
		return result, errors.New("validation process failed")
	}
	if out.Len() > 1024 {
		return result, errors.New("invalid validation output")
	}
	d := json.NewDecoder(&out)
	d.DisallowUnknownFields()
	if d.Decode(&result) != nil || d.Decode(new(any)) != io.EOF || (result.Status != "succeeded" && result.Status != "failed") {
		return automation.Result{}, errors.New("invalid validation output")
	}
	return result, nil
}
