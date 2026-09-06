package provider

import (
	"errors"
	"math"
	"regexp"
	"time"
)

type MetricsRequest struct {
	NativeID string `json:"native_id"`
	Start    int64  `json:"start"`
	End      int64  `json:"end"`
}
type MetricPoint struct {
	At    int64   `json:"at"`
	Value float64 `json:"value"`
}
type MetricSeries struct {
	ID            string        `json:"id"`
	Name          string        `json:"name"`
	Unit          string        `json:"unit"`
	Aggregation   string        `json:"aggregation"`
	PeriodSeconds int           `json:"period_seconds"`
	Points        []MetricPoint `json:"points"`
}
type MetricsResult struct {
	Series []MetricSeries `json:"series"`
	Error  string         `json:"error,omitempty"`
}

func (r MetricsRequest) Validate(cloud string) error {
	pattern := `^[1-9][0-9]{0,17}$`
	if cloud == "aws" {
		pattern = `^i-[a-f0-9]{8,17}$`
	}
	now := time.Now().Unix()
	if !regexp.MustCompile(pattern).MatchString(r.NativeID) || r.Start < now-25*3600 || r.End > now+60 || r.End <= r.Start || r.End-r.Start > 24*3600 {
		return errors.New("invalid metric target or interval")
	}
	return nil
}
func (r MetricsResult) Validate() error {
	if (r.Error != "" && r.Error != "unavailable" && r.Error != "invalid_configuration") || len(r.Series) > 16 || (r.Error != "" && len(r.Series) > 0) {
		return errors.New("invalid metrics response")
	}
	seen := map[string]bool{}
	total := 0
	for _, s := range r.Series {
		if s.ID == "" || s.Name == "" || s.Unit == "" || len(s.ID) > 80 || len(s.Name) > 120 || len(s.Unit) > 40 || s.Aggregation == "" || len(s.Aggregation) > 100 || s.PeriodSeconds < 0 || s.PeriodSeconds > 3600 || len(s.Points) > 2000 || seen[s.ID] {
			return errors.New("invalid metric series")
		}
		seen[s.ID] = true
		total += len(s.Points)
		var last int64
		for _, p := range s.Points {
			if p.At <= last || math.IsNaN(p.Value) || math.IsInf(p.Value, 0) || p.Value < 0 {
				return errors.New("invalid metric point")
			}
			last = p.At
		}
	}
	if total > 12000 {
		return errors.New("too many metric points")
	}
	return nil
}
func (r MetricsResult) Within(request MetricsRequest) bool {
	for _, s := range r.Series {
		for _, p := range s.Points {
			if p.At < request.Start || p.At > request.End {
				return false
			}
		}
	}
	return true
}
