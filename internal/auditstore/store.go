package auditstore

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/alphabravo-oss/providah-community/internal/notification"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	smithyhttp "github.com/aws/smithy-go/transport/http"
)

const MaxBatchBytes = 4 << 20

type Target struct{ Endpoint, Region, Bucket, AccessKey, SecretKey, SessionToken string }
type Request struct {
	Target                Target
	Origin, Key, Checksum string
	Payload               []byte
	First, Last           string
}

func Validate(t Target) error {
	if err := notification.ValidateHTTPS(t.Endpoint); err != nil {
		return err
	}
	u, _ := url.Parse(t.Endpoint)
	if u.Path != "" && u.Path != "/" {
		return errors.New("Use the storage service root endpoint.")
	}
	if !regexp.MustCompile("^[a-z0-9][a-z0-9.-]{1,61}[a-z0-9]$").MatchString(t.Bucket) || strings.Contains(t.Bucket, "..") {
		return errors.New("Use a valid 3–63 character bucket name.")
	}
	if !regexp.MustCompile("^[a-z0-9-]{1,64}$").MatchString(t.Region) || len(t.AccessKey) < 3 || len(t.AccessKey) > 256 || len(t.SecretKey) < 3 || len(t.SecretKey) > 4096 || len(t.SessionToken) > 8192 {
		return errors.New("Provide a region and bounded storage credentials.")
	}
	return nil
}
func Write(ctx context.Context, r Request) error {
	if err := Validate(r.Target); err != nil {
		return err
	}
	client, err := notification.PublicHTTPSClient(r.Origin)
	if err != nil {
		return err
	}
	defer client.CloseIdleConnections()
	sdk := s3.NewFromConfig(aws.Config{Region: r.Target.Region, Credentials: credentials.NewStaticCredentialsProvider(r.Target.AccessKey, r.Target.SecretKey, r.Target.SessionToken), HTTPClient: boundedClient{client}, RetryMaxAttempts: 1, RequestChecksumCalculation: aws.RequestChecksumCalculationWhenRequired}, func(o *s3.Options) { o.BaseEndpoint = aws.String(r.Target.Endpoint); o.UsePathStyle = true })
	return write(ctx, sdk, r)
}
func write(ctx context.Context, client *s3.Client, r Request) error {
	digest := sha256.Sum256(r.Payload)
	if len(r.Payload) == 0 || len(r.Payload) > MaxBatchBytes || hex.EncodeToString(digest[:]) != r.Checksum {
		return errors.New("Invalid audit batch checksum or size.")
	}
	_, err := client.PutObject(ctx, &s3.PutObjectInput{Bucket: aws.String(r.Target.Bucket), Key: aws.String(r.Key), Body: bytes.NewReader(r.Payload), IfNoneMatch: aws.String("*"), ContentType: aws.String("application/gzip"), Metadata: map[string]string{"sha256": r.Checksum, "first-event-id": r.First, "last-event-id": r.Last}})
	if err != nil {
		var response *smithyhttp.ResponseError
		if !errors.As(err, &response) || response.HTTPStatusCode() != 412 {
			return err
		}
	}
	// A lost PUT response can be retried safely. A pre-existing object is accepted
	// only after reading and hashing its bytes; metadata or ETag alone is insufficient.
	stored, err := client.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(r.Target.Bucket), Key: aws.String(r.Key)})
	if err != nil {
		return err
	}
	defer func() { _ = stored.Body.Close() }()
	h := sha256.New()
	n, err := io.Copy(h, io.LimitReader(stored.Body, int64(len(r.Payload))+1))
	if err != nil {
		return err
	}
	if n != int64(len(r.Payload)) || hex.EncodeToString(h.Sum(nil)) != r.Checksum {
		return errors.New("Stored audit object failed verification.")
	}
	return nil
}

// Bound SDK error decoding as well as successful object reads.
type boundedClient struct{ *http.Client }
type boundedBody struct {
	io.Reader
	io.Closer
}

func (c boundedClient) Do(r *http.Request) (*http.Response, error) {
	response, err := c.Client.Do(r) // #nosec G704 -- SDK requests use the validated installation storage endpoint; redirects are disabled.
	if err == nil {
		response.Body = boundedBody{io.LimitReader(response.Body, MaxBatchBytes+1), response.Body}
	}
	return response, err
}
