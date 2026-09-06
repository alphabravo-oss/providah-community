package provider

import (
	"errors"
	"unicode/utf8"
)

// Nil metadata means not collected; an empty Tags value means observed untagged.
type Tags struct {
	Labels map[string]string `json:"labels,omitempty"`
	Names  []string          `json:"names,omitempty"`
}

func (t *Tags) Validate() error {
	if t == nil {
		return nil
	}
	if len(t.Labels)+len(t.Names) > 100 {
		return errors.New("too many tags")
	}
	size := 0
	valid := func(s string, max int) bool { return len(s) <= max && utf8.ValidString(s) }
	for k, v := range t.Labels {
		if k == "" || !valid(k, 256) || !valid(v, 2048) {
			return errors.New("invalid label")
		}
		size += len(k) + len(v)
	}
	seen := map[string]bool{}
	for _, name := range t.Names {
		if name == "" || !valid(name, 256) || seen[name] {
			return errors.New("invalid named tag")
		}
		seen[name] = true
		size += len(name)
	}
	if size > 16384 {
		return errors.New("tag metadata too large")
	}
	return nil
}
