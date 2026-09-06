package automation

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"github.com/alphabravo-oss/providah-community/internal/egress"
	"io"
	"regexp"
	"slices"
	"strings"
)

type Runtime struct {
	DependencyHosts []string `json:"dependency_hosts,omitempty"`
	Runtime         string   `json:"runtime"`
	Image           string   `json:"image"`
	Version         string   `json:"version"`
}

func ParseRuntimes(raw string) ([]Runtime, error) {
	if raw == "" {
		return nil, nil
	}
	bad := errors.New("AUTOMATION_RUNTIMES must contain at most 32 unique runtime, immutable sha256 image, and version entries")
	if len(raw) > 16384 {
		return nil, bad
	}
	var out []Runtime
	d := json.NewDecoder(strings.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(&out) != nil || d.Decode(new(any)) != io.EOF || len(out) > 32 {
		return nil, bad
	}
	seen := map[string]bool{}
	for i := range out {
		r := &out[i]
		if len(r.DependencyHosts) > 16 {
			return nil, bad
		}
		slices.Sort(r.DependencyHosts)
		for j, h := range r.DependencyHosts {
			if !egress.ValidHost(h) || (j > 0 && h == r.DependencyHosts[j-1]) {
				return nil, bad
			}
		}
		key := r.Runtime + ":" + r.Image
		if (r.Runtime != "terraform" && r.Runtime != "opentofu" && r.Runtime != "ansible") || !regexp.MustCompile(`^sha256:[a-f0-9]{64}$`).MatchString(r.Image) || !regexp.MustCompile(`^[A-Za-z0-9.+-]{1,80}$`).MatchString(r.Version) || seen[key] {
			return nil, bad
		}
		seen[key] = true
	}
	return out, nil
}

// Policy pins network authority as well as the image and version label.
func (r Runtime) Policy() string {
	raw, _ := json.Marshal(r)
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:])
}
func (r Runtime) Matches(engine, image, version, policy string) bool {
	return r.Runtime == engine && r.Image == image && r.Version == version && (r.Policy() == policy || policy == "" && len(r.DependencyHosts) == 0)
}
