package provider

import (
	"errors"
	"maps"
	"regexp"
	"slices"
	"strings"
)

var writableNamedTag = regexp.MustCompile(`^[A-Za-z0-9:_-]{1,255}$`)

func TagsEqual(a, b *Tags) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	left, right := slices.Clone(a.Names), slices.Clone(b.Names)
	slices.Sort(left)
	slices.Sort(right)
	return maps.Equal(a.Labels, b.Labels) && slices.Equal(left, right)
}

func ValidateTagChange(cloud string, before, after *Tags) error {
	if before == nil || after == nil || before.Validate() != nil || after.Validate() != nil {
		return errors.New("invalid tag sets")
	}
	if cloud == "digitalocean" {
		for _, set := range []*Tags{before, after} {
			seen := map[string]bool{}
			for _, name := range set.Names {
				lower := strings.ToLower(name)
				if seen[lower] {
					return errors.New("duplicate named tag")
				}
				seen[lower] = true
				if !writableNamedTag.MatchString(name) {
					return errors.New("invalid named tag")
				}
			}
		}
		if len(before.Labels) > 0 || len(after.Labels) > 0 {
			return errors.New("named tags required")
		}
		for _, old := range before.Names {
			for _, name := range after.Names {
				if old != name && strings.EqualFold(old, name) {
					return errors.New("case-only tag changes are unsupported")
				}
			}
		}
		return nil
	}
	if cloud != "aws" && cloud != "hetzner" || len(before.Names) > 0 || len(after.Names) > 0 {
		return errors.New("labels required")
	}
	limit := 50
	if cloud == "hetzner" {
		limit = 64
	}
	editable := 0
	for k, v := range after.Labels {
		if cloud == "aws" && strings.HasPrefix(strings.ToLower(k), "aws:") {
			if old, ok := before.Labels[k]; !ok || old != v {
				return errors.New("reserved AWS tag")
			}
			continue
		}
		editable++
		if len(k) > 128 || len(v) > 256 {
			return errors.New("label too long")
		}
	}
	if editable > limit {
		return errors.New("too many labels")
	}
	if cloud == "aws" {
		for k, v := range before.Labels {
			if strings.HasPrefix(strings.ToLower(k), "aws:") {
				if value, ok := after.Labels[k]; !ok || value != v {
					return errors.New("reserved AWS tag")
				}
			}
		}
	}
	return nil
}
