// Package tfstate validates state identity without interpreting or exposing values.
package tfstate

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"regexp"
	"unicode/utf8"
)

const MaxBytes = 32 << 20

var UUID = regexp.MustCompile(`^[a-f0-9]{8}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{4}-[a-f0-9]{12}$`)

type Metadata struct {
	Lineage, SHA256 string
	Serial          int64
}

func Parse(raw []byte) (Metadata, error) {
	invalid := errors.New("state must be format 4 JSON with a lineage UUID and nonnegative serial")
	if len(raw) == 0 || len(raw) > MaxBytes || !utf8.Valid(raw) {
		return Metadata{}, invalid
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	token, e := d.Token()
	if e != nil || token != json.Delim('{') {
		return Metadata{}, invalid
	}
	fields := map[string]json.RawMessage{}
	for d.More() {
		token, e = d.Token()
		if e != nil {
			return Metadata{}, invalid
		}
		key, ok := token.(string)
		if !ok {
			return Metadata{}, invalid
		}
		if _, exists := fields[key]; exists {
			return Metadata{}, invalid
		}
		var value json.RawMessage
		if d.Decode(&value) != nil {
			return Metadata{}, invalid
		}
		fields[key] = value
	}
	if _, e = d.Token(); e != nil || d.Decode(new(any)) != io.EOF {
		return Metadata{}, invalid
	}
	var version int
	var m Metadata
	if json.Unmarshal(fields["version"], &version) != nil || version != 4 || json.Unmarshal(fields["lineage"], &m.Lineage) != nil || !UUID.MatchString(m.Lineage) || bytes.Equal(bytes.TrimSpace(fields["serial"]), []byte("null")) || json.Unmarshal(fields["serial"], &m.Serial) != nil || m.Serial < 0 {
		return Metadata{}, invalid
	}
	sum := sha256.Sum256(raw)
	m.SHA256 = hex.EncodeToString(sum[:])
	return m, nil
}
