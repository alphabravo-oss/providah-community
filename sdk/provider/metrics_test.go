package provider

import (
	"math"
	"testing"
	"time"
)

func TestMetricsBoundaries(t *testing.T) {
	end := time.Now().Unix()
	request := MetricsRequest{NativeID: "123", Start: end - 3600, End: end}
	if request.Validate("hetzner") != nil {
		t.Fatal("valid request rejected")
	}
	request.Start = end - 26*3600
	if request.Validate("hetzner") == nil {
		t.Fatal("unbounded history accepted")
	}
	result := MetricsResult{Series: []MetricSeries{{ID: "cpu", Name: "CPU", Unit: "percent", Aggregation: "Average", PeriodSeconds: 300, Points: []MetricPoint{{At: end - 300, Value: 0}, {At: end, Value: 10}}}}}
	if result.Validate() != nil {
		t.Fatal("valid zero rejected")
	}
	for _, value := range []float64{math.NaN(), math.Inf(1), -1} {
		result.Series[0].Points[1].Value = value
		if result.Validate() == nil {
			t.Fatal("invalid numeric value accepted")
		}
	}
	result.Series[0].Points[1] = result.Series[0].Points[0]
	if result.Validate() == nil {
		t.Fatal("duplicate timestamp accepted")
	}
	legacy := Runtime{CapabilitiesVersion: 0}
	modern := Runtime{CapabilitiesVersion: 1, Metrics: true}
	if legacy.Accepts(Request{Metrics: &request}) || !modern.Accepts(Request{Metrics: &request}) {
		t.Fatal("metrics runtime capability not enforced")
	}
	if (Response{Version: Protocol, Complete: true, Metrics: &MetricsResult{}}).Validate() == nil {
		t.Fatal("metrics mixed with authoritative inventory")
	}
}
