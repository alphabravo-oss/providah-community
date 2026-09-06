package launcher

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/alphabravo-oss/providah-community/sdk/provider"
	"io"
	"os/exec"
	"slices"
	"time"
)

// LoadRuntimes admits only deployment-configured, locally present images. No
// request can supply a registry, image tag, Docker flags, or executable path.
func LoadRuntimes(ctx context.Context, raw, fallback string, trust *provider.RuntimeTrust) ([]provider.Runtime, error) {
	configured := map[string][]string{}
	if raw == "" {
		for _, cloud := range []string{"aws", "digitalocean", "hetzner"} {
			configured[cloud] = []string{fallback}
		}
	} else if json.Unmarshal([]byte(raw), &configured) != nil {
		return nil, errors.New("PROVIDER_RUNTIMES must map provider IDs to image-reference arrays")
	}
	if len(raw) > 65536 {
		return nil, errors.New("runtime configuration too large")
	}
	total := 0
	for cloud, images := range configured {
		if !slices.Contains([]string{"aws", "digitalocean", "hetzner"}, cloud) || len(images) == 0 {
			return nil, errors.New("invalid configured provider")
		}
		total += len(images)
	}
	if total == 0 || total > 32 {
		return nil, errors.New("configure 1–32 provider runtimes")
	}
	descriptions := map[string][]provider.Runtime{}
	out := []provider.Runtime{}
	for _, cloud := range []string{"aws", "digitalocean", "hetzner"} {
		for _, ref := range configured[cloud] {
			image, err := ResolveImage(ctx, ref)
			if err != nil {
				return nil, err
			}
			admittedAt := time.Now()
			if !trust.Allows(image, cloud, admittedAt) {
				return nil, errors.New("provider runtime lacks a current trusted publisher approval")
			}
			entries, ok := descriptions[image]
			if !ok {
				entries, err = describe(ctx, image)
				if err != nil {
					return nil, err
				}
				descriptions[image] = entries
			}
			found := false
			for _, entry := range entries {
				if entry.Provider == cloud {
					entry.PublisherKeyID, entry.ApprovalExpiresAt = trust.Approval(image, cloud, admittedAt)
					out = append(out, entry)
					found = true
					break
				}
			}
			if !found {
				return nil, errors.New("configured image does not describe the expected provider")
			}
		}
	}
	return out, provider.ValidateRuntimes(out)
}
func describe(ctx context.Context, image string) ([]provider.Runtime, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	suffix := make([]byte, 16)
	if _, err := rand.Read(suffix); err != nil {
		return nil, err
	}
	name := "providah-health-" + hex.EncodeToString(suffix)
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = exec.CommandContext(cleanup, "docker", "rm", "-f", name).Run()
	}()
	args := append([]string{"run"}, expiryLabels()...)
	args = append(args, "--label", "providah.worker=health", "--rm", "--pull=never", "--name", name, "--read-only", "--cap-drop=ALL", "--security-opt=no-new-privileges", "--pids-limit=128", "--memory=512m", "--cpus=1", "--user=65532:65532", "--network=none", image, "--describe")
	cmd := exec.CommandContext(ctx, "docker", args...)
	var out boundedBuffer
	cmd.Stdout = &out
	cmd.Stderr = io.Discard
	if err := cmd.Run(); err != nil || out.Len() > 65536 {
		return nil, errors.New("provider health/description failed")
	}
	var entries []provider.Runtime
	if json.Unmarshal(out.Bytes(), &entries) != nil {
		return nil, errors.New("invalid provider description")
	}
	for i := range entries {
		entries[i].Image = image
		entries[i].PublisherKeyID = ""
		entries[i].ApprovalExpiresAt = ""
	}
	return entries, provider.ValidateRuntimes(entries)
}
