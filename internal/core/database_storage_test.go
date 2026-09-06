package core

import "testing"

func TestDatabaseStorageStatus(t *testing.T) {
	for _, tc := range []struct {
		used, budget int64
		want         string
	}{{90, 0, "unconfigured"}, {0, 100, "within_budget"}, {79, 100, "within_budget"}, {80, 100, "warning"}, {89, 100, "warning"}, {90, 100, "critical"}, {120, 100, "critical"}} {
		if got := databaseStorageStatus(tc.used, tc.budget); got != tc.want {
			t.Fatalf("%d/%d: %s", tc.used, tc.budget, got)
		}
	}
}
