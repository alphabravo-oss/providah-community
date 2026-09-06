package launcher

import (
	"context"
	"errors"
	"log"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Only ephemeral resources opt in. Persistent state must never carry these labels.
// Ten minutes exceeds every current worker deadline (at most 130 seconds).
func expiryLabels() []string {
	return []string{"--label", "providah.ephemeral=true", "--label", "providah.expires=" + strconv.FormatInt(time.Now().Add(10*time.Minute).Unix(), 10)}
}

var ephemeralName = regexp.MustCompile(`^providah-(scan|health|validation)-[a-f0-9]{32}(-proxy|-egress)?$`)

func expiredResource(name, kind, expires string, volume bool, now time.Time) bool {
	if !ephemeralName.MatchString(name) {
		return false
	}
	deadline, err := strconv.ParseInt(expires, 10, 64)
	if err != nil || deadline <= 0 || deadline > now.Unix() {
		return false
	}
	if volume {
		return kind == "validation" && strings.HasPrefix(name, "providah-validation-") && strings.HasSuffix(name, "-egress")
	}
	switch kind {
	case "discovery":
		return strings.HasPrefix(name, "providah-scan-") && len(name) == len("providah-scan-")+32
	case "health":
		return strings.HasPrefix(name, "providah-health-") && len(name) == len("providah-health-")+32
	case "validation":
		return strings.HasPrefix(name, "providah-validation-") && len(name) == len("providah-validation-")+32
	case "validation-egress":
		return strings.HasPrefix(name, "providah-validation-") && strings.HasSuffix(name, "-proxy")
	}
	return false
}

// ReapExpired removes only explicitly expired Providah scratch resources. It can
// run on multiple launchers: IDs/names are unique, and Docker refuses mounted
// volume removal. It never retries an operation or changes its durable outcome.
func ReapExpired(ctx context.Context) (int, error) {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	removed, attempts := 0, 0
	var failures []error
	for _, volume := range []bool{false, true} {
		format := `{{.ID}}\t{{.Names}}\t{{.Label "providah.worker"}}\t{{.Label "providah.expires"}}`
		args := []string{"ps", "-a", "--no-trunc", "--filter", "label=providah.ephemeral=true", "--format", format}
		if volume {
			args = []string{"volume", "ls", "--filter", "label=providah.ephemeral=true", "--format", `{{.Name}}\t{{.Name}}\t{{.Label "providah.worker"}}\t{{.Label "providah.expires"}}`}
		}
		var out boundedBuffer
		cmd := exec.CommandContext(ctx, "docker", args...)
		cmd.WaitDelay = time.Second
		cmd.Stdout = &out
		if err := cmd.Run(); err != nil {
			failures = append(failures, errors.New("ephemeral resource listing failed"))
			continue
		}
		for _, line := range strings.Split(out.String(), "\n") {
			fields := strings.Split(line, "\t")
			if len(fields) != 4 || !expiredResource(fields[1], fields[2], fields[3], volume, time.Now()) {
				continue
			}
			if attempts >= 64 {
				break
			} // Bound each pass; remaining expired resources wait for the next tick.
			attempts++
			args = []string{"rm", "-f", fields[0]}
			if volume {
				args = []string{"volume", "rm", fields[1]}
			}
			cmd = exec.CommandContext(ctx, "docker", args...)
			cmd.WaitDelay = time.Second
			if err := cmd.Run(); err != nil {
				failures = append(failures, errors.New("ephemeral resource removal failed"))
			} else {
				removed++
			}
		}
	}
	return removed, errors.Join(failures...)
}

func StartCleanup(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(time.Minute)
		defer ticker.Stop()
		for {
			count, err := ReapExpired(ctx)
			if ctx.Err() != nil {
				return
			}
			if count > 0 || err != nil {
				log.Printf("Expired runner cleanup: removed=%d failed=%t", count, err != nil)
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
}
