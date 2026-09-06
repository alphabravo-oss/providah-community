package providers

import (
	"context"
	"errors"
	"github.com/alphabravo-oss/providah-community/sdk/provider"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatch"
	cwtypes "github.com/aws/aws-sdk-go-v2/service/cloudwatch/types"
	"github.com/digitalocean/godo"
	"github.com/hetznercloud/hcloud-go/v2/hcloud"
	"golang.org/x/oauth2"
	"io"
	"math"
	"net/http"
	"slices"
	"strconv"
	"time"
)

type metricsTransport struct{ http.RoundTripper }

func (t metricsTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	response, err := t.RoundTripper.RoundTrip(r)
	if err == nil && response.Body != nil {
		response.Body = struct {
			io.Reader
			io.Closer
		}{io.LimitReader(response.Body, 2<<20), response.Body}
	}
	return response, err
}
func Metrics(ctx context.Context, r provider.Request, client *http.Client) provider.Response {
	bounded := *client
	base := client.Transport
	if base == nil {
		base = http.DefaultTransport
	}
	bounded.Transport = metricsTransport{base}
	client = &bounded

	result := &provider.MetricsResult{Error: "invalid_configuration"}
	if r.Metrics != nil && r.Validate() == nil {
		var series []provider.MetricSeries
		var err error
		switch r.Provider {
		case "aws":
			series, err = awsMetrics(ctx, r, client)
		case "digitalocean":
			series, err = doMetrics(ctx, r, client)
		case "hetzner":
			series, err = hetznerMetrics(ctx, r, client)
		}
		result = &provider.MetricsResult{Series: series}
		for i := range series {
			slices.SortFunc(series[i].Points, func(a, b provider.MetricPoint) int {
				if a.At < b.At {
					return -1
				}
				if a.At > b.At {
					return 1
				}
				return 0
			})
		}
		if err != nil || result.Validate() != nil || !result.Within(*r.Metrics) {
			result = &provider.MetricsResult{Error: "unavailable"}
		}
	}
	return provider.Response{Version: provider.Protocol, Metrics: result}
}
func awsMetrics(ctx context.Context, r provider.Request, client *http.Client) ([]provider.MetricSeries, error) {
	ec, err := AWSClient(r, client, 1)
	if err != nil {
		return nil, err
	}
	svc := cloudwatch.New(cloudwatch.Options{Region: r.Region, Credentials: ec.Options().Credentials, HTTPClient: client, RetryMaxAttempts: 1})
	out := []provider.MetricSeries{}
	for _, metric := range []struct {
		id, name, unit string
		stat           cwtypes.Statistic
	}{{"CPUUtilization", "CPU utilization", "percent", cwtypes.StatisticAverage}, {"NetworkIn", "All-interface network received", "bytes / 5 minutes", cwtypes.StatisticSum}, {"NetworkOut", "All-interface network sent", "bytes / 5 minutes", cwtypes.StatisticSum}} {
		data, err := svc.GetMetricStatistics(ctx, &cloudwatch.GetMetricStatisticsInput{Namespace: aws.String("AWS/EC2"), MetricName: aws.String(metric.id), Dimensions: []cwtypes.Dimension{{Name: aws.String("InstanceId"), Value: aws.String(r.Metrics.NativeID)}}, StartTime: aws.Time(time.Unix(r.Metrics.Start, 0)), EndTime: aws.Time(time.Unix(r.Metrics.End, 0)), Period: aws.Int32(300), Statistics: []cwtypes.Statistic{metric.stat}})
		if err != nil {
			return nil, err
		}
		s := provider.MetricSeries{ID: metric.id, Name: metric.name, Unit: metric.unit, Aggregation: string(metric.stat), PeriodSeconds: 300}
		for _, point := range data.Datapoints {
			value := point.Sum
			if metric.stat == cwtypes.StatisticAverage {
				value = point.Average
			}
			if point.Timestamp == nil || value == nil {
				return nil, errors.New("incomplete cloudwatch datapoint")
			}
			s.Points = append(s.Points, provider.MetricPoint{At: point.Timestamp.Unix(), Value: *value})
		}
		out = append(out, s)
	}
	return out, nil
}
func doMetrics(ctx context.Context, r provider.Request, client *http.Client) ([]provider.MetricSeries, error) {
	svc := godo.NewClient(&http.Client{Transport: &oauth2.Transport{Source: oauth2.StaticTokenSource(&oauth2.Token{AccessToken: r.Credential}), Base: client.Transport}, Timeout: client.Timeout})
	out := []provider.MetricSeries{}
	for _, network := range []string{"public", "private"} {
		for _, direction := range []string{"inbound", "outbound"} {
			data, _, err := svc.Monitoring.GetDropletBandwidth(ctx, &godo.DropletBandwidthMetricsRequest{DropletMetricsRequest: godo.DropletMetricsRequest{HostID: r.Metrics.NativeID, Start: time.Unix(r.Metrics.Start, 0), End: time.Unix(r.Metrics.End, 0)}, Interface: network, Direction: direction})
			if err != nil {
				return nil, err
			}
			if data == nil || data.Status != "success" || data.Data.ResultType != "matrix" || len(data.Data.Result) > 1 {
				return nil, errors.New("invalid droplet metric result")
			}
			s := provider.MetricSeries{ID: network + "." + direction, Name: network + " network " + direction, Unit: "Mbps", Aggregation: "Provider-reported rate"}
			for _, stream := range data.Data.Result {
				if string(stream.Metric["host_id"]) != r.Metrics.NativeID || string(stream.Metric["interface"]) != network || string(stream.Metric["direction"]) != direction {
					return nil, errors.New("unexpected metric target")
				}
				for _, point := range stream.Values {
					value := float64(point.Value)
					if math.IsNaN(value) {
						continue
					}
					s.Points = append(s.Points, provider.MetricPoint{At: int64(point.Timestamp) / 1000, Value: value})
				}
			}
			out = append(out, s)
		}
	}
	return out, nil
}
func hetznerMetrics(ctx context.Context, r provider.Request, client *http.Client) ([]provider.MetricSeries, error) {
	id, _ := strconv.ParseInt(r.Metrics.NativeID, 10, 64)
	svc := hcloud.NewClient(hcloud.WithToken(r.Credential), hcloud.WithHTTPClient(client))
	data, _, err := svc.Server.GetMetrics(ctx, &hcloud.Server{ID: id}, hcloud.ServerGetMetricsOpts{Types: []hcloud.ServerMetricType{hcloud.ServerMetricCPU, hcloud.ServerMetricNetwork, hcloud.ServerMetricDisk}, Start: time.Unix(r.Metrics.Start, 0), End: time.Unix(r.Metrics.End, 0), Step: 300})
	if err != nil {
		return nil, err
	}
	if data == nil || data.Step < 1 || data.Step > 3600 {
		return nil, errors.New("invalid metric interval")
	}
	out := []provider.MetricSeries{}
	for _, metric := range []struct{ id, name, unit string }{{"cpu", "CPU utilization", "percent"}, {"network.0.bandwidth.in", "Public network received", "bytes / second"}, {"network.0.bandwidth.out", "Public network sent", "bytes / second"}, {"disk.0.bandwidth.read", "Local disk reads", "bytes / second"}, {"disk.0.bandwidth.write", "Local disk writes", "bytes / second"}, {"disk.0.iops.read", "Local disk read operations", "IOPS"}, {"disk.0.iops.write", "Local disk write operations", "IOPS"}} {
		s := provider.MetricSeries{ID: metric.id, Name: metric.name, Unit: metric.unit, Aggregation: "Provider-reported samples", PeriodSeconds: int(data.Step)}
		for _, point := range data.TimeSeries[metric.id] {
			value, err := strconv.ParseFloat(point.Value, 64)
			if err != nil {
				return nil, err
			}
			if math.IsNaN(value) {
				continue
			}
			if math.IsNaN(point.Timestamp) || math.IsInf(point.Timestamp, 0) {
				return nil, errors.New("invalid metric timestamp")
			}
			s.Points = append(s.Points, provider.MetricPoint{At: int64(point.Timestamp), Value: value})
		}
		out = append(out, s)
	}
	return out, nil
}
