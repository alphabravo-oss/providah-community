package core

import (
	"fmt"
	"slices"
	"strings"
	"time"
	_ "time/tzdata"

	pb "github.com/alphabravo-oss/providah-community/internal/gen/providah/v1"
	"github.com/robfig/cron/v3"
)

var scheduleParser = cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)

type occurrence struct {
	Due                 time.Time
	Local, Action, Skip string
}

func validateScheduleSpec(spec *pb.ScheduleSpec) error {
	if spec == nil || !slices.Contains([]string{"start", "shutdown", "restart", "window"}, spec.Action) {
		return invalid("Choose a supported schedule action.")
	}
	if spec.Timezone == "" || spec.Timezone == "Local" || len(spec.Timezone) > 100 {
		return invalid("Choose an IANA timezone, such as America/New_York.")
	}
	if _, err := time.LoadLocation(spec.Timezone); err != nil {
		return invalid("Unknown timezone.")
	}
	if len(spec.Exceptions) > 100 {
		return invalid("Use at most 100 calendar exception dates.")
	}
	for _, day := range spec.Exceptions {
		if _, err := time.Parse("2006-01-02", day); err != nil {
			return invalid("Exception dates must use YYYY-MM-DD.")
		}
	}
	if spec.RunAt != "" {
		if spec.Cron != "" || spec.StopCron != "" || spec.Action == "window" {
			return invalid("One-time schedules cannot also contain recurrence rules.")
		}
		if _, err := time.Parse("2006-01-02T15:04", spec.RunAt); err != nil {
			return invalid("Choose a local date and time for the one-time action.")
		}
		return nil
	}
	rules := []string{spec.Cron}
	if spec.Action == "window" {
		rules = append(rules, spec.StopCron)
	} else if spec.StopCron != "" {
		return invalid("A shutdown rule is only used with a paired window.")
	}
	for _, rule := range rules {
		if len(rule) > 120 || strings.Contains(rule, "TZ=") {
			return invalid("Use a five-field cron rule and the separate timezone field.")
		}
		if _, err := scheduleParser.Parse(rule); err != nil {
			return invalid("Use a valid five-field cron rule: minute hour day month weekday.")
		}
	}
	return nil
}

// Cron parses nominal local calendar times in UTC. We resolve actual offsets separately
// so a DST gap is recorded and a repeated wall time selects its first occurrence.
func wallOffsets(wall time.Time, loc *time.Location) map[int]bool {
	offsets := map[int]bool{}
	anchor := time.Date(wall.Year(), wall.Month(), wall.Day(), wall.Hour(), wall.Minute(), 0, 0, loc)
	for _, h := range []int{-48, -24, 0, 24, 48} {
		_, offset := anchor.Add(time.Duration(h) * time.Hour).Zone()
		offsets[offset] = true
	}
	return offsets
}
func resolveWall(wall time.Time, loc *time.Location) (time.Time, bool) {
	offsets := wallOffsets(wall, loc)
	var first, fallback time.Time
	for offset := range offsets {
		candidate := wall.Add(-time.Duration(offset) * time.Second)
		if candidate.After(fallback) {
			fallback = candidate
		}
		local := candidate.In(loc)
		if local.Format("2006-01-02T15:04") == wall.Format("2006-01-02T15:04") && (first.IsZero() || candidate.Before(first)) {
			first = candidate
		}
	}
	if first.IsZero() {
		return fallback, true
	}
	return first, false
}
func nextSchedule(spec *pb.ScheduleSpec, after time.Time) (occurrence, error) {
	return nextScheduleAfter(spec, after, "", "")
}

// The local slot breaks UTC ties between missing and real wall times.
func nextScheduleAfter(spec *pb.ScheduleSpec, after time.Time, priorLocal, priorAction string) (occurrence, error) {
	if err := validateScheduleSpec(spec); err != nil {
		return occurrence{}, err
	}
	loc, _ := time.LoadLocation(spec.Timezone)
	makeOccurrence := func(wall time.Time, action string) occurrence {
		due, missing := resolveWall(wall, loc)
		skip := ""
		if missing {
			skip = "Local time does not exist at the DST transition."
		}
		if slices.Contains(spec.Exceptions, wall.Format("2006-01-02")) {
			skip = "Calendar exception."
		}
		return occurrence{Due: due, Local: wall.Format("2006-01-02 15:04") + " " + spec.Timezone + " (" + due.In(loc).Format("-07:00") + ")", Action: action, Skip: skip}
	}
	if spec.RunAt != "" {
		wall, _ := time.Parse("2006-01-02T15:04", spec.RunAt)
		o := makeOccurrence(wall, spec.Action)
		if !o.Due.After(after) {
			return occurrence{}, nil
		}
		return o, nil
	}
	rules := []string{spec.Cron}
	actions := []string{spec.Action}
	if spec.Action == "window" {
		rules = append(rules, spec.StopCron)
		actions = []string{"start", "shutdown"}
	}
	slot := func(o occurrence) string { return o.Local + "\x00" + o.Action }
	eligible := func(o occurrence) bool {
		return o.Due.After(after) || (o.Due.Equal(after) && priorLocal != "" && slot(o) > priorLocal+"\x00"+priorAction)
	}
	local := after.In(loc)
	wallAfter := time.Date(local.Year(), local.Month(), local.Day(), local.Hour(), local.Minute(), local.Second(), local.Nanosecond(), time.UTC)
	minimum, maximum := 24*60*60, -24*60*60
	for offset := range wallOffsets(wallAfter, loc) {
		minimum = min(minimum, offset)
		maximum = max(maximum, offset)
	}
	start := wallAfter.Add(-time.Duration(maximum-minimum) * time.Second)
	// Include the current minute for equal-instant slot continuation.
	if priorLocal != "" {
		start = start.Add(-time.Nanosecond)
	}
	var next occurrence
	var candidates []occurrence
	for i, rule := range rules {
		parsed, _ := scheduleParser.Parse(rule)
		wall := start
		for attempts := 0; attempts < 6000; attempts++ {
			wall = parsed.Next(wall)
			if wall.IsZero() {
				break
			}
			earliest := wall.Add(24 * time.Hour)
			for offset := range wallOffsets(wall, loc) {
				candidate := wall.Add(-time.Duration(offset) * time.Second)
				if candidate.Before(earliest) {
					earliest = candidate
				}
			}
			if !next.Due.IsZero() && earliest.After(next.Due) {
				break
			}
			o := makeOccurrence(wall, actions[i])
			if o.Due.Before(after) {
				continue
			}
			candidates = append(candidates, o)
			if eligible(o) && (next.Due.IsZero() || o.Due.Before(next.Due) || (o.Due.Equal(next.Due) && slot(o) < slot(next))) {
				next = o
			}
		}
	}
	// Two real paired actions at one instant are both blocked. A missing local
	// start is only a skipped slot and must not suppress a real paired action.
	if next.Skip == "" {
		for _, other := range candidates {
			if other.Skip == "" && other.Due.Equal(next.Due) && other.Action != next.Action {
				next.Skip = "Paired actions overlap at the same instant; neither will run."
				break
			}
		}
	}
	if next.Due.IsZero() {
		return next, fmt.Errorf("no upcoming occurrence in supported cron horizon")
	}
	return next, nil
}
