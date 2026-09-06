// Package inputspec owns the shared, source-pinned input contract.
package inputspec

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

const MaxBytes = 16 << 10
const Filename = "providah.inputs.json"

type Field struct {
	Name    string   `json:"name"`
	Label   string   `json:"label"`
	Type    string   `json:"type"`
	Choices []string `json:"choices,omitempty"`
	Min     *int64   `json:"min,omitempty"`
	Max     *int64   `json:"max,omitempty"`
}

var identifier = regexp.MustCompile(`^[a-z][a-z0-9_]{0,63}$`)

func Parse(raw []byte) ([]Field, error) {
	bad := errors.New("providah.inputs.json must declare version 1 and at most 32 bounded string, integer or boolean inputs")
	if len(raw) > MaxBytes {
		return nil, bad
	}
	var doc struct {
		Version int     `json:"version"`
		Inputs  []Field `json:"inputs"`
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if d.Decode(&doc) != nil || d.Decode(new(any)) != io.EOF || doc.Version != 1 || len(doc.Inputs) > 32 {
		return nil, bad
	}
	names := map[string]bool{}
	for _, f := range doc.Inputs {
		if !identifier.MatchString(f.Name) || names[f.Name] || strings.HasPrefix(f.Name, "ansible_") || slices.Contains([]string{"hostvars", "groups", "group_names", "inventory_hostname", "omit", "vars", "environment"}, f.Name) || len(f.Label) < 1 || len(f.Label) > 80 {
			return nil, bad
		}
		names[f.Name] = true
		switch f.Type {
		case "string":
			if len(f.Choices) < 1 || len(f.Choices) > 32 || f.Min != nil || f.Max != nil {
				return nil, bad
			}
			seen := map[string]bool{}
			for _, choice := range f.Choices {
				if len(choice) == 0 || len(choice) > 256 || seen[choice] {
					return nil, bad
				}
				seen[choice] = true
			}
		case "integer":
			if f.Min == nil || f.Max == nil || *f.Min > *f.Max || *f.Min < -1000000000 || *f.Max > 1000000000 || len(f.Choices) > 0 {
				return nil, bad
			}
		case "boolean":
			if f.Min != nil || f.Max != nil || len(f.Choices) > 0 {
				return nil, bad
			}
		default:
			return nil, bad
		}
	}
	return doc.Inputs, nil
}

// Values rejects missing/unknown/duplicate fields and emits stable typed JSON.
// There are no secret literals, CLI flags, or unconstrained identifier strings.
func Values(fields []Field, raw []byte) ([]byte, error) {
	bad := errors.New("Choose every declared input with its allowed value and type; extra inputs are not accepted")
	if len(raw) == 0 {
		raw = []byte("{}")
	}
	if len(raw) > MaxBytes {
		return nil, bad
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	token, e := d.Token()
	if e != nil || token != json.Delim('{') {
		return nil, bad
	}
	values := map[string]json.RawMessage{}
	for d.More() {
		token, e = d.Token()
		if e != nil {
			return nil, bad
		}
		key, ok := token.(string)
		if !ok {
			return nil, bad
		}
		if _, ok = values[key]; ok {
			return nil, bad
		}
		var v json.RawMessage
		if d.Decode(&v) != nil {
			return nil, bad
		}
		values[key] = v
	}
	if _, e = d.Token(); e != nil || d.Decode(new(any)) != io.EOF || len(values) != len(fields) {
		return nil, bad
	}
	out := map[string]any{}
	for _, f := range fields {
		raw, ok := values[f.Name]
		if !ok || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			return nil, bad
		}
		switch f.Type {
		case "string":
			var value string
			if json.Unmarshal(raw, &value) != nil || !slices.Contains(f.Choices, value) {
				return nil, bad
			}
			out[f.Name] = value
		case "integer":
			value, e := strconv.ParseInt(string(raw), 10, 64)
			if e != nil || f.Min == nil || f.Max == nil || value < *f.Min || value > *f.Max {
				return nil, bad
			}
			out[f.Name] = value
		case "boolean":
			var value bool
			if json.Unmarshal(raw, &value) != nil {
				return nil, bad
			}
			out[f.Name] = value
		default:
			return nil, bad
		}
	}
	return json.Marshal(out)
}
