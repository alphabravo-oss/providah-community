package providers

import (
	"context"
	"fmt"
	"github.com/alphabravo-oss/providah-community/sdk/provider"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestS3BucketInventory(t *testing.T) {
	for _, mode := range []string{"complete", "empty", "untagged", "later-denied", "tags-denied", "wrong-region", "duplicate", "cycle", "invalid-name"} {
		t.Run(mode, func(t *testing.T) {
			lists := 0
			client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
				if r.Method != "GET" || r.URL.Host != "s3.us-west-2.amazonaws.com" || !strings.Contains(r.Header.Get("Authorization"), "/us-west-2/s3/aws4_request") {
					t.Fatal("unexpected SDK request", r.URL)
				}
				body := ""
				status := 200
				if r.URL.Path == "/" {
					lists++
					if lists > 3 {
						t.Fatal("unbounded pages")
					}
					if r.URL.Query().Get("bucket-region") != "us-west-2" || r.URL.Query().Get("max-buckets") != "100" {
						t.Fatal("missing bounds")
					}
					name := fmt.Sprintf("test-bucket-%d", lists)
					if mode == "duplicate" {
						name = "test-bucket-1"
					}
					if mode == "invalid-name" {
						name = "../escape"
					}
					region := "us-west-2"
					if mode == "wrong-region" {
						region = "us-east-1"
					}
					entry := fmt.Sprintf("<Bucket><Name>%s</Name><BucketRegion>%s</BucketRegion><CreationDate>2026-01-01T00:00:00Z</CreationDate></Bucket>", name, region)
					if mode == "empty" || mode == "cycle" {
						entry = ""
					}
					next := ""
					if lists == 1 && mode != "empty" || mode == "cycle" {
						next = "<ContinuationToken>next</ContinuationToken>"
					}
					body = "<ListAllMyBucketsResult><Buckets>" + entry + "</Buckets>" + next + "</ListAllMyBucketsResult>"
					if lists == 2 && mode == "later-denied" {
						status = 403
						body = "<Error><Code>AccessDenied</Code><Message>private-error</Message></Error>"
					}
				} else {
					if !strings.HasPrefix(r.URL.Path, "/test-bucket-") || !r.URL.Query().Has("tagging") {
						t.Fatal("unexpected bucket endpoint", r.URL)
					}
					body = "<Tagging><TagSet><Tag><Key>environment</Key><Value>production</Value></Tag></TagSet></Tagging>"
					if mode == "untagged" {
						status = 404
						body = "<Error><Code>NoSuchTagSet</Code></Error>"
					}
					if mode == "tags-denied" {
						status = 403
						body = "<Error><Code>AccessDenied</Code><Message>private-error</Message></Error>"
					}
				}
				return &http.Response{StatusCode: status, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
			})}
			out := Discover(context.Background(), provider.Request{Version: provider.Protocol, OrganizationID: strings.Repeat("a", 64), ConnectionID: strings.Repeat("b", 64), Provider: "aws", Region: "us-west-2", Credential: `{"access_key_id":"fake","secret_access_key":"fake-secret"}`, InventoryKinds: []string{"storage.bucket"}}, client)
			if mode == "complete" || mode == "untagged" || mode == "empty" {
				want := 2
				if mode == "empty" {
					want = 0
				}
				if !out.Complete || out.Validate() != nil || len(out.Resources) != want {
					t.Fatal(out)
				}
				if mode == "complete" && (out.Resources[0].Tags.Labels["environment"] != "production" || !strings.Contains(out.Resources[0].Size, "2026-01-01")) {
					t.Fatal("lost metadata", out)
				}
			} else if out.Complete || len(out.Resources) != 0 || out.Error == "" || strings.Contains(out.Error, "private-error") {
				t.Fatal("unsafe partial inventory", out)
			}
		})
	}
}
