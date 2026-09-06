package auditstore

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

type fakeHTTP func(*http.Request) (*http.Response, error)

func (f fakeHTTP) Do(r *http.Request) (*http.Response, error) { return f(r) }

func TestConditionalObjectVerification(t *testing.T) {
	payload := []byte("immutable compressed audit payload")
	sum := sha256.Sum256(payload)
	request := Request{Target: Target{Bucket: "audit-bucket"}, Key: "providah-audit/org/batch.jsonl.gz", Payload: payload, Checksum: hex.EncodeToString(sum[:]), First: "1", Last: "9"}
	for _, tc := range []struct {
		name    string
		put     int
		stored  string
		wantErr bool
	}{
		{"new", 200, string(payload), false}, {"existing-identical", 412, string(payload), false},
		{"existing-different", 412, "wrong", true}, {"new-corrupt", 200, "wrong", true},
		{"put-failed-no-hidden-retry", 503, "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			puts, gets := 0, 0
			client := s3.NewFromConfig(aws.Config{Region: "us-east-1", Credentials: credentials.NewStaticCredentialsProvider("explicit-key", "explicit-secret", ""), RetryMaxAttempts: 1, RequestChecksumCalculation: aws.RequestChecksumCalculationWhenRequired, HTTPClient: fakeHTTP(func(r *http.Request) (*http.Response, error) {
				if !strings.Contains(r.Header.Get("Authorization"), "Credential=explicit-key/") {
					t.Fatal("static credentials not used")
				}
				if r.URL.Path != "/audit-bucket/"+request.Key {
					t.Fatal("wrong object", r.URL.Path)
				}
				code, body := 200, tc.stored
				switch r.Method {
				case "PUT":
					puts++
					if r.Header.Get("If-None-Match") != "*" {
						t.Fatal("overwrite allowed")
					}
					actual, _ := io.ReadAll(r.Body)
					if string(actual) != string(payload) {
						t.Fatal("payload changed")
					}
					code = tc.put
					body = ""
					if code >= 400 {
						body = fmt.Sprintf("<Error><Code>%s</Code></Error>", map[int]string{412: "PreconditionFailed", 503: "SlowDown"}[code])
					}
				case "GET":
					gets++
				default:
					t.Fatal("unexpected request")
				}
				return &http.Response{StatusCode: code, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body)), Request: r}, nil
			})}, func(o *s3.Options) { o.BaseEndpoint = aws.String("https://storage.example.com"); o.UsePathStyle = true })
			err := write(context.Background(), client, request)
			if (err != nil) != tc.wantErr {
				t.Fatalf("error=%v", err)
			}
			wantGets := 1
			if tc.put == 503 {
				wantGets = 0
			}
			if puts != 1 || gets != wantGets {
				t.Fatalf("requests: put=%d get=%d", puts, gets)
			}
		})
	}
}

func TestValidateStorage(t *testing.T) {
	base := Target{Endpoint: "https://storage.example.com", Region: "us-east-1", Bucket: "audit-bucket", AccessKey: "key", SecretKey: "secret"}
	if err := Validate(base); err != nil {
		t.Fatal(err)
	}
	for _, endpoint := range []string{"http://storage.example.com", "https://127.0.0.1", "https://169.254.169.254", "https://storage.example.com/path", "https://user:pass@storage.example.com", "https://storage.example.com:8443"} {
		bad := base
		bad.Endpoint = endpoint
		if Validate(bad) == nil {
			t.Fatal("unsafe endpoint allowed", endpoint)
		}
	}
}
