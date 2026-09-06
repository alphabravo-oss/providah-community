package provider

import (
	"strings"
	"testing"
)

func TestTagsBoundaries(t *testing.T) {
	for _, v := range []*Tags{nil, {}, {Labels: map[string]string{"env": "production", "empty": ""}}, {Names: []string{"env:production"}}} {
		if v.Validate() != nil {
			t.Fatal("valid metadata rejected")
		}
	}
	for _, v := range []*Tags{{Labels: map[string]string{"": "bad"}}, {Names: []string{"same", "same"}}, {Names: []string{""}}, {Labels: map[string]string{"key": strings.Repeat("x", 2049)}}} {
		if v.Validate() == nil {
			t.Fatal("invalid metadata accepted")
		}
	}
}
