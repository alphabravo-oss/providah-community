package providers

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/alphabravo-oss/providah-community/sdk/provider"
	"github.com/aws/smithy-go/encoding/cbor"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestProviderMetrics(t *testing.T) {
	end := time.Now().UTC().Truncate(5 * time.Minute).Unix()
	start := end - 3600
	for _, cloud := range []string{"aws", "digitalocean", "hetzner"} {
		t.Run(cloud, func(t *testing.T) {
			calls := 0
			fail := false
			empty := false
			foreign := false
			client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				calls++
				if r.Header.Get("Authorization") == "" {
					t.Fatal("missing SDK authentication")
				}
				if fail {
					return &http.Response{StatusCode: 403, Header: http.Header{}, Body: io.NopCloser(strings.NewReader("never-expose-provider-secret")), Request: r}, nil
				}
				body := []byte{}
				ct := "application/json"
				switch cloud {
				case "aws":
					if r.URL.Host != "monitoring.us-east-1.amazonaws.com" || !strings.HasSuffix(r.URL.Path, "GetMetricStatistics") {
						t.Fatal("unexpected cloudwatch request", r.URL.Host, r.URL.Path)
					}
					raw, _ := io.ReadAll(r.Body)
					decoded, err := cbor.Decode(raw)
					if err != nil {
						t.Fatal(err)
					}
					args := decoded.(cbor.Map)
					dimensions := args["Dimensions"].(cbor.List)
					if args["Namespace"] != cbor.String("AWS/EC2") || dimensions[0].(cbor.Map)["Value"] != cbor.String("i-12345678") || args["Period"] != cbor.Uint(300) {
						t.Fatal("wrong cloudwatch scope")
					}
					points := cbor.List{}
					if !empty {
						points = cbor.List{cbor.Map{"Timestamp": &cbor.Tag{ID: 1, Value: cbor.Uint(end - 300)}, "Average": cbor.Float64(12), "Sum": cbor.Float64(60)}, cbor.Map{"Timestamp": &cbor.Tag{ID: 1, Value: cbor.Uint(start)}, "Average": cbor.Float64(0), "Sum": cbor.Float64(0)}}
					}
					body = cbor.Encode(cbor.Map{"Datapoints": points})
					ct = "application/cbor"
				case "digitalocean":
					q := r.URL.Query()
					if r.URL.Host != "api.digitalocean.com" || r.URL.Path != "/v2/monitoring/metrics/droplet/bandwidth" || q.Get("host_id") != "123" || q.Get("start") != fmt.Sprint(start) {
						t.Fatal("wrong droplet metric scope")
					}
					host := "123"
					if foreign {
						host = "999"
					}
					result := fmt.Sprintf(`[{"metric":{"host_id":%q,"interface":%q,"direction":%q},"values":[[%d,"0"],[%d,"1.25"]]}]`, host, q.Get("interface"), q.Get("direction"), start, end-300)
					if empty {
						result = "[]"
					}
					body = []byte(`{"status":"success","data":{"resultType":"matrix","result":` + result + `}}`)
				case "hetzner":
					if r.URL.Host != "api.hetzner.cloud" || r.URL.Path != "/v1/servers/123/metrics" || r.URL.Query().Get("step") != "300" {
						t.Fatal("wrong Hetzner metric scope")
					}
					series := fmt.Sprintf(`{"cpu":{"values":[[%d,"0"],[%d,"12"]]}}`, start, end-300)
					if empty {
						series = "{}"
					}
					body = []byte(fmt.Sprintf(`{"metrics":{"start":%q,"end":%q,"step":300,"time_series":%s}}`, time.Unix(start, 0).UTC().Format(time.RFC3339), time.Unix(end, 0).UTC().Format(time.RFC3339), series))
				}
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": []string{ct}, "Smithy-Protocol": []string{"rpc-v2-cbor"}}, Body: io.NopCloser(bytes.NewReader(body)), Request: r}, nil
			})}
			credential, region, native := "never-expose-provider-secret", "fsn1", "123"
			if cloud == "aws" {
				credential = `{"access_key_id":"test-key","secret_access_key":"never-expose-provider-secret"}`
				region = "us-east-1"
				native = "i-12345678"
			}
			request := provider.Request{Version: provider.Protocol, OrganizationID: strings.Repeat("a", 64), ConnectionID: strings.Repeat("b", 64), Provider: cloud, Region: region, Credential: credential, Metrics: &provider.MetricsRequest{NativeID: native, Start: start, End: end}}
			out := Metrics(context.Background(), request, client)
			if out.Validate() != nil || out.Metrics.Error != "" || len(out.Metrics.Series) == 0 || len(out.Metrics.Series[0].Points) != 2 || out.Metrics.Series[0].Points[0].Value != 0 {
				t.Fatalf("invalid metrics: %+v", out.Metrics)
			}
			if cloud == "digitalocean" && out.Metrics.Series[0].Unit != "Mbps" {
				t.Fatal("bandwidth units changed")
			}
			encoded, _ := json.Marshal(out)
			if strings.Contains(string(encoded), "never-expose") {
				t.Fatal("metrics leaked credential")
			}
			empty = true
			out = Metrics(context.Background(), request, client)
			if out.Validate() != nil || out.Metrics.Error != "" {
				t.Fatal("empty metrics became failure")
			}
			for _, series := range out.Metrics.Series {
				if len(series.Points) != 0 {
					t.Fatal("missing data filled with zero")
				}
			}
			empty = false
			foreign = true
			if cloud == "digitalocean" {
				out = Metrics(context.Background(), request, client)
				if out.Metrics.Error != "unavailable" {
					t.Fatal("foreign labels accepted")
				}
			}
			fail = true
			out = Metrics(context.Background(), request, client)
			encoded, _ = json.Marshal(out)
			if out.Validate() != nil || out.Metrics.Error != "unavailable" || strings.Contains(string(encoded), "never-expose") {
				t.Fatal("provider error leaked")
			}
			before := calls
			request.Metrics.NativeID = "invalid"
			out = Metrics(context.Background(), request, client)
			if out.Metrics.Error != "invalid_configuration" || calls != before {
				t.Fatal("invalid target reached provider")
			}
		})
	}
}
