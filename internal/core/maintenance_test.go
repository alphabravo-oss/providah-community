package core

import (
	"encoding/json"
	"github.com/alphabravo-oss/providah-community/internal/database"
	pb "github.com/alphabravo-oss/providah-community/internal/gen/providah/v1"
	"testing"
	"time"
)

func TestMaintenanceWindows(t *testing.T) {
	for _, tc := range []struct {
		name, zone, cron, at string
		minutes              int32
		exceptions           []string
		want                 bool
	}{
		{"inclusive start", "UTC", "0 9 * * *", "2026-09-05T09:00:00Z", 60, nil, true},
		{"exclusive end", "UTC", "0 9 * * *", "2026-09-05T10:00:00Z", 60, nil, false},
		{"overnight", "UTC", "0 22 * * *", "2026-09-06T00:30:00Z", 180, nil, true},
		{"exception closes carryover", "UTC", "0 22 * * *", "2026-09-06T00:30:00Z", 180, []string{"2026-09-06"}, false},
		{"missing start", "America/New_York", "30 2 * * *", "2026-03-08T07:45:00Z", 60, nil, false},
		{"valid start after missing start", "America/New_York", "0 2,3 * * *", "2026-03-08T07:05:00Z", 10, nil, true},
		{"first repeated start", "America/New_York", "30 1 * * *", "2026-11-01T05:45:00Z", 45, nil, true},
		{"second repeated start ignored", "America/New_York", "30 1 * * *", "2026-11-01T06:45:00Z", 45, nil, false},
		{"elapsed time across fold", "America/New_York", "30 1 * * *", "2026-11-01T06:45:00Z", 120, nil, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			at, _ := time.Parse(time.RFC3339, tc.at)
			got, err := maintenanceOpen(&pb.ScheduleSpec{Action: "start", Timezone: tc.zone, Cron: tc.cron, Exceptions: tc.exceptions}, tc.minutes, at)
			if err != nil || got != tc.want {
				t.Fatalf("open=%v err=%v want=%v", got, err, tc.want)
			}
		})
	}
	at, _ := time.Parse(time.RFC3339, "2026-09-05T09:30:00Z")
	open, _ := json.Marshal(&pb.ScheduleSpec{Action: "start", Timezone: "UTC", Cron: "0 9 * * *"})
	closed, _ := json.Marshal(&pb.ScheduleSpec{Action: "start", Timezone: "UTC", Cron: "0 18 * * *"})
	policies := []database.ListMaintenancePoliciesRow{{ID: "global", Name: "Organization", Spec: open, DurationMinutes: 60}, {ID: "scoped", Name: "Connection", Spec: closed, DurationMinutes: 60, ConnectionIds: []string{"one"}}}
	if reason, _, err := maintenanceRestriction(policies, "one", at); err != nil || reason == "" {
		t.Fatal("closed connection policy did not intersect global policy", reason, err)
	}
	if reason, _, err := maintenanceRestriction(policies, "two", at); err != nil || reason != "" {
		t.Fatal("unrelated connection policy restricted target", reason, err)
	}
}
