package core

import (
	pb "github.com/alphabravo-oss/providah-community/internal/gen/providah/v1"
	"testing"
	"time"
)

func TestScheduleClock(t *testing.T) {
	for _, tc := range []struct {
		name, rule, zone, after, want string
		skipped                       bool
	}{
		{"normal", "0 9 * * 1-5", "America/New_York", "2026-09-04T14:00:00Z", "2026-09-07T13:00:00Z", false},
		{"spring gap", "30 2 * * *", "America/New_York", "2026-03-08T05:00:00Z", "2026-03-08T07:30:00Z", true},
		{"first fold", "30 1 * * *", "America/New_York", "2026-11-01T04:00:00Z", "2026-11-01T05:30:00Z", false},
		{"no duplicate fold", "30 1 * * *", "America/New_York", "2026-11-01T06:10:00Z", "2026-11-02T06:30:00Z", false},
		{"half-hour gap", "15 2 * * *", "Australia/Lord_Howe", "2026-10-03T13:00:00Z", "2026-10-03T15:45:00Z", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			after, _ := time.Parse(time.RFC3339, tc.after)
			o, err := nextSchedule(&pb.ScheduleSpec{Action: "start", Cron: tc.rule, Timezone: tc.zone}, after)
			if err != nil || o.Due.Format(time.RFC3339) != tc.want || (o.Skip != "") != tc.skipped {
				t.Fatalf("next=%+v err=%v want=%s", o, err, tc.want)
			}
		})
	}
	spec := &pb.ScheduleSpec{Action: "window", Cron: "0 22 * * *", StopCron: "0 6 * * *", Timezone: "UTC", Exceptions: []string{"2026-09-06"}}
	after, _ := time.Parse(time.RFC3339, "2026-09-05T23:00:00Z")
	o, err := nextSchedule(spec, after)
	if err != nil || o.Action != "shutdown" || o.Skip != "Calendar exception." {
		t.Fatalf("overnight window: %+v %v", o, err)
	}
	spec.Action = "start"
	spec.Cron = ""
	spec.StopCron = ""
	spec.RunAt = "2026-09-06T09:00"
	o, err = nextSchedule(spec, after)
	if err != nil || o.Due.Hour() != 9 {
		t.Fatalf("one-time: %+v %v", o, err)
	}
	o, err = nextSchedule(spec, o.Due)
	if err != nil || !o.Due.IsZero() {
		t.Fatal("one-time schedule repeated")
	}
}

func TestScheduleDSTSlots(t *testing.T) {
	for _, tc := range []struct {
		name, zone, cron, stop, after string
		due                           []string
		skipped                       []bool
	}{
		{"hour gap", "America/New_York", "0,30 2,3 * * *", "", "2026-03-08T06:59:00Z", []string{"2026-03-08T07:00:00Z", "2026-03-08T07:00:00Z", "2026-03-08T07:30:00Z", "2026-03-08T07:30:00Z"}, []bool{true, false, true, false}},
		{"half-hour reorder", "Australia/Lord_Howe", "15,30,45 2 * * *", "", "2026-10-03T13:00:00Z", []string{"2026-10-03T15:30:00Z", "2026-10-03T15:45:00Z", "2026-10-03T15:45:00Z"}, []bool{false, true, false}},
		{"skipped paired start", "America/New_York", "0 2 * * *", "0 3 * * *", "2026-03-08T06:59:00Z", []string{"2026-03-08T07:00:00Z", "2026-03-08T07:00:00Z"}, []bool{true, false}},
		{"real paired overlap", "America/New_York", "0 3 * * *", "0 3 * * *", "2026-03-08T06:59:00Z", []string{"2026-03-08T07:00:00Z", "2026-03-08T07:00:00Z"}, []bool{true, true}},
		{"skipped calendar day", "Pacific/Apia", "0 9 * * *", "", "2011-12-30T08:00:00Z", []string{"2011-12-30T19:00:00Z", "2011-12-30T19:00:00Z"}, []bool{true, false}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			spec := &pb.ScheduleSpec{Action: "start", Timezone: tc.zone, Cron: tc.cron, StopCron: tc.stop}
			if tc.stop != "" {
				spec.Action = "window"
			}
			after, _ := time.Parse(time.RFC3339, tc.after)
			local, action := "", ""
			seen := map[string]bool{}
			for i, want := range tc.due {
				o, err := nextScheduleAfter(spec, after, local, action)
				if err != nil || o.Due.Format(time.RFC3339) != want || (o.Skip != "") != tc.skipped[i] {
					t.Fatalf("slot %d got %+v err=%v want %s skip=%v", i, o, err, want, tc.skipped[i])
				}
				key := o.Local + o.Action
				if seen[key] {
					t.Fatal("local slot repeated", key)
				}
				seen[key] = true
				after, local, action = o.Due, o.Local, o.Action
			}
		})
	}
}
