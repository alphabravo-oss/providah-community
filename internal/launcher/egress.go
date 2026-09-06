package launcher

import (
	"context"
	"encoding/json"
	"errors"
	"os/exec"
	"strings"
	"time"
)

func (l *Launcher) startProxy(ctx context.Context, name string, hosts []string) (string, func(), error) {
	volume, proxy := name+"-egress", name+"-proxy"
	cleanup := func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = exec.CommandContext(ctx, "docker", "rm", "-f", proxy).Run()
		_ = exec.CommandContext(ctx, "docker", "volume", "rm", "-f", volume).Run()
	}
	if l.proxyImage == "" {
		return "", nil, errors.New("proxy image unavailable")
	}
	volumeArgs := append([]string{"volume", "create"}, expiryLabels()...)
	volumeArgs = append(volumeArgs, "--label", "providah.worker=validation", volume)
	if e := exec.CommandContext(ctx, "docker", volumeArgs...).Run(); e != nil {
		return "", nil, e
	}
	encoded, _ := json.Marshal(hosts)
	args := append([]string{"run"}, expiryLabels()...)
	args = append(args, "-d", "--rm", "--pull=never", "--name", proxy, "--label", "providah.worker=validation-egress", "--read-only", "--cap-drop=ALL", "--security-opt=no-new-privileges", "--pids-limit=32", "--memory=128m", "--cpus=0.25", "--user=65532:65532", "--network=bridge", "--mount", "type=volume,src="+volume+",dst=/run/egress", "--env", "DEPENDENCY_HOSTS="+string(encoded), "--entrypoint", "/egress-proxy", l.proxyImage)
	if e := exec.CommandContext(ctx, "docker", args...).Run(); e != nil {
		cleanup()
		return "", nil, e
	}
	ready, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	timer := time.NewTicker(250 * time.Millisecond)
	defer timer.Stop()
	for {
		select {
		case <-ready.Done():
			cleanup()
			return "", nil, errors.New("dependency proxy did not become ready")
		case <-timer.C:
			out, e := exec.CommandContext(ready, "docker", "inspect", "--format", "{{if .State.Health}}{{.State.Health.Status}}{{end}}", proxy).Output()
			if e == nil && strings.TrimSpace(string(out)) == "healthy" {
				return volume, cleanup, nil
			}
		}
	}
}
