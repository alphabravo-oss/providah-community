package launcher

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestExpiryBoundary(t *testing.T) {
	now := time.Unix(1000, 0)
	name := "providah-validation-" + strings.Repeat("a", 32)
	for _, tc := range []struct {
		name, kind, deadline string
		volume, want         bool
	}{
		{name, "validation", "1000", false, true},
		{name, "validation", "1001", false, false},
		{name, "validation", "0", false, false},
		{name, "validation", "-1", false, false},
		{name, "validation", "", false, false},
		{name, "validation", "999999999999999999999", false, false},
		{name, "unknown", "999", false, false},
		{name + "-proxy", "validation-egress", "999", false, true},
		{name + "-egress", "validation", "999", true, true},
		{name + "-egress", "validation", "999", false, false},
		{"providah_postgres", "validation", "999", true, false},
		{"--force", "validation", "999", true, false},
		{"providah-scan-" + strings.Repeat("b", 32), "discovery", "999", false, true},
		{"providah-health-" + strings.Repeat("c", 32), "health", "999", false, true},
	} {
		if got := expiredResource(tc.name, tc.kind, tc.deadline, tc.volume, now); got != tc.want {
			t.Fatalf("unexpected eligibility: %+v", tc)
		}
	}
	labels := expiryLabels()
	expiry, e := strconv.ParseInt(strings.TrimPrefix(labels[3], "providah.expires="), 10, 64)
	if e != nil || expiry < time.Now().Add(9*time.Minute).Unix() || expiry > time.Now().Add(11*time.Minute).Unix() {
		t.Fatal("worker cleanup deadline is not outside normal execution")
	}
}

// Exercises daemon-owned resources surviving the process that launched them.
func TestExpiredContainers(t *testing.T) {
	image := os.Getenv("TEST_CLEANUP_IMAGE")
	if image == "" {
		t.Skip("set TEST_CLEANUP_IMAGE to a local image containing /bin/sleep")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	docker := func(args ...string) string {
		t.Helper()
		out, e := exec.CommandContext(ctx, "docker", args...).CombinedOutput()
		if e != nil {
			t.Fatalf("docker fixture: %v: %s", e, out)
		}
		return strings.TrimSpace(string(out))
	}
	suffix := make([]byte, 16)
	if _, e := rand.Read(suffix); e != nil {
		t.Fatal(e)
	}
	prefix := "providah-validation-" + hex.EncodeToString(suffix)
	expired := strconv.FormatInt(time.Now().Add(-time.Minute).Unix(), 10)
	future := strconv.FormatInt(time.Now().Add(time.Hour).Unix(), 10)
	var containers, volumes []string
	t.Cleanup(func() {
		cleanup, c := context.WithTimeout(context.Background(), 15*time.Second)
		defer c()
		for _, n := range containers {
			_ = exec.CommandContext(cleanup, "docker", "rm", "-f", n).Run()
		}
		for _, n := range volumes {
			_ = exec.CommandContext(cleanup, "docker", "volume", "rm", n).Run()
		}
	})
	volume := func(name, deadline string) {
		volumes = append(volumes, name)
		docker("volume", "create", "--label", "providah.ephemeral=true", "--label", "providah.worker=validation", "--label", "providah.expires="+deadline, name)
	}
	orphan := prefix + "-egress"
	volume(orphan, expired)
	// A second, well-formed expired volume is still mounted by an unexpired job.
	second := "providah-validation-" + hex.EncodeToString(append([]byte{suffix[0] ^ 255}, suffix[1:]...))
	mounted := second + "-egress"
	volume(mounted, expired)
	volume("providah-cleanup-test-"+hex.EncodeToString(suffix), expired) // Unrelated persistent name must survive.
	cases := []struct {
		name, deadline          string
		opted, running, removed bool
		mount                   string
	}{
		{prefix, expired, true, true, true, ""},
		{prefix + "-proxy", expired, true, false, true, ""},
		{second, future, true, true, false, mounted},
		{"providah-scan-" + hex.EncodeToString(suffix), expired, false, false, false, ""},
		{"providah-health-" + hex.EncodeToString(suffix), "bad", true, false, false, ""},
	}
	for _, tc := range cases {
		containers = append(containers, tc.name)
		kind := "validation"
		if strings.HasSuffix(tc.name, "-proxy") {
			kind = "validation-egress"
		}
		if strings.HasPrefix(tc.name, "providah-scan-") {
			kind = "discovery"
		}
		if strings.HasPrefix(tc.name, "providah-health-") {
			kind = "health"
		}
		args := []string{"create", "--pull=never", "--name", tc.name, "--network=none", "--read-only", "--cap-drop=ALL", "--security-opt=no-new-privileges", "--label", "providah.worker=" + kind, "--label", "providah.expires=" + tc.deadline}
		if tc.opted {
			args = append(args, "--label", "providah.ephemeral=true")
		}
		if tc.mount != "" {
			args = append(args, "--mount", "type=volume,src="+tc.mount+",dst=/scratch")
		}
		args = append(args, "--entrypoint", "/bin/sleep", image, "600")
		docker(args...)
		if tc.running {
			docker("start", tc.name)
		}
	}
	count, err := ReapExpired(ctx)
	if count < 3 || err == nil {
		t.Fatalf("expected 3 removals and refusal for mounted volume: %d %v", count, err)
	}
	for _, tc := range cases {
		e := exec.CommandContext(ctx, "docker", "container", "inspect", tc.name).Run()
		if (e != nil) != tc.removed {
			t.Fatalf("wrong cleanup outcome for %s: %v", tc.name, e)
		}
	}
	for _, n := range volumes {
		e := exec.CommandContext(ctx, "docker", "volume", "inspect", n).Run()
		if (e != nil) != (n == orphan) {
			t.Fatalf("wrong volume cleanup outcome: %s %v", n, e)
		}
	}
	docker("rm", "-f", second)
	count, err = ReapExpired(ctx)
	if count < 1 || err != nil {
		t.Fatalf("unmounted orphan not removed on next pass: %d %v", count, err)
	}
}
