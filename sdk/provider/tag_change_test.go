package provider

import "testing"

func TestTagChangeBoundaries(t *testing.T) {
	reserved := &Tags{Labels: map[string]string{"aws:managed": "yes"}}
	for _, tc := range []struct {
		cloud         string
		before, after *Tags
	}{
		{"aws", reserved, &Tags{}},
		{"aws", &Tags{}, reserved},
		{"digitalocean", &Tags{}, &Tags{Names: []string{"../escape"}}},
		{"digitalocean", &Tags{Names: []string{"Production"}}, &Tags{Names: []string{"production"}}},
		{"digitalocean", &Tags{}, &Tags{Names: []string{"prod", "PROD"}}},
		{"hetzner", &Tags{}, &Tags{Names: []string{"prod"}}},
	} {
		if ValidateTagChange(tc.cloud, tc.before, tc.after) == nil {
			t.Fatal("unsafe change accepted", tc.cloud)
		}
	}
	if ValidateTagChange("aws", reserved, reserved) != nil {
		t.Fatal("unchanged reserved tag rejected")
	}
	if !TagsEqual(&Tags{Names: []string{"a", "b"}}, &Tags{Names: []string{"b", "a"}}) {
		t.Fatal("ordering changed equality")
	}
}
