// Package sourcebundle validates uploaded source without extracting or executing it.
package sourcebundle

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"github.com/alphabravo-oss/providah-community/internal/inputspec"
	"io"
	"path"
	"strings"
	"unicode"
)

const MaxArchive = 4 << 20
const MaxExpanded = 16 << 20
const MaxFiles = 256

type Manifest struct {
	Inputs        []inputspec.Field
	SHA256        string
	Files         []string
	ExpandedBytes int64
}

func Validate(raw []byte, runtime, entrypoint string) (Manifest, error) {
	bad := func() (Manifest, error) {
		return Manifest{}, errors.New("Use a ZIP with at most 256 regular files, 4 MiB compressed and 16 MiB expanded, safe relative paths, and an existing runtime entrypoint. Links, state, environment and private-key files are not accepted.")
	}
	if len(raw) == 0 || len(raw) > MaxArchive {
		return bad()
	}
	if runtime != "terraform" && runtime != "opentofu" && runtime != "ansible" {
		return bad()
	}
	if !safePath(entrypoint) {
		return bad()
	}
	if runtime == "ansible" {
		if !strings.HasSuffix(entrypoint, ".yml") && !strings.HasSuffix(entrypoint, ".yaml") {
			return bad()
		}
	} else if !strings.HasSuffix(entrypoint, ".tf") && !strings.HasSuffix(entrypoint, ".tf.json") {
		return bad()
	}
	z, e := zip.NewReader(bytes.NewReader(raw), int64(len(raw)))
	if e != nil || len(z.File) == 0 || len(z.File) > MaxFiles {
		return bad()
	}
	m := Manifest{}
	seen := map[string]bool{}
	found := false
	for _, f := range z.File {
		name := strings.TrimSuffix(f.Name, "/")
		if !safePath(name) || seen[strings.ToLower(name)] {
			return bad()
		}
		seen[strings.ToLower(name)] = true
		if f.Mode().IsDir() {
			if f.UncompressedSize64 != 0 {
				return bad()
			}
			continue
		}
		if !f.Mode().IsRegular() || f.UncompressedSize64 > MaxExpanded {
			return bad()
		}
		lower := strings.ToLower(name)
		base := path.Base(lower)
		if strings.Contains(lower, ".tfstate") || base == ".env" || strings.HasPrefix(base, ".env.") || strings.HasSuffix(base, ".pem") || strings.HasSuffix(base, ".key") || base == "id_rsa" || base == "id_ed25519" {
			return bad()
		}
		for _, part := range strings.Split(lower, "/") {
			if part == ".git" || part == ".terraform" {
				return bad()
			}
		}
		r, e := f.Open()
		if e != nil {
			return bad()
		}
		var n int64
		if name == inputspec.Filename {
			raw, err := io.ReadAll(io.LimitReader(r, inputspec.MaxBytes+1))
			n = int64(len(raw))
			e = err
			if e == nil {
				m.Inputs, e = inputspec.Parse(raw)
			}
		} else {
			n, e = io.Copy(io.Discard, io.LimitReader(r, MaxExpanded-m.ExpandedBytes+1))
		}
		closeErr := r.Close()
		if e != nil || closeErr != nil || n != int64(f.UncompressedSize64) || n > MaxExpanded-m.ExpandedBytes {
			return bad()
		}
		m.ExpandedBytes += n
		m.Files = append(m.Files, name)
		found = found || name == entrypoint
	}
	// A file cannot also be an ancestor directory of another archive entry.
	for _, f := range m.Files {
		for name := range seen {
			if strings.HasPrefix(name, strings.ToLower(f)+"/") {
				return bad()
			}
		}
	}
	if !found {
		return bad()
	}
	digest := sha256.Sum256(raw)
	m.SHA256 = hex.EncodeToString(digest[:])
	return m, nil
}
func safePath(p string) bool {
	return p != "" && len(p) <= 240 && path.Clean(p) == p && p != "." && p != ".." && !strings.HasPrefix(p, "../") && !strings.HasPrefix(p, "/") && !strings.ContainsAny(p, "\\:") && !strings.ContainsFunc(p, unicode.IsControl)
}
