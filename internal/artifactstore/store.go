// Package artifactstore stores immutable encrypted objects in deployment-owned S3 storage.
// Configuration is never accepted from tenant input.
package artifactstore

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
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	smithyhttp "github.com/aws/smithy-go/transport/http"
)

const MaxBytes = 33 << 20 // A bounded 32 MiB state plus age framing.
var objectKey = regexp.MustCompile(`^(states/[a-f0-9]{64}/[a-f0-9]{64}/[a-f0-9]{64}|sources/[a-f0-9]{64}/[a-f0-9]{64})$`)
var digest = regexp.MustCompile(`^[a-f0-9]{64}$`)

type Config struct{ Endpoint, Region, Bucket, AccessKey, SecretKey, SessionToken string }
type Store struct {
	client     *s3.Client
	bucket, id string
}
type Reference struct {
	Store, Key, Version, SHA256 string
	Bytes                       int64
}

func New(c Config, development bool) (*Store, error) {
	u, e := url.Parse(c.Endpoint)
	if e != nil || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") || (u.Scheme != "https" && (!development || c.Endpoint != "http://rustfs:9000" && c.Endpoint != "http://127.0.0.1:19000")) {
		return nil, errors.New("artifact endpoint must be an HTTPS service root (or the exact development RustFS endpoint)")
	}
	if !regexp.MustCompile(`^[a-z0-9][a-z0-9.-]{1,61}[a-z0-9]$`).MatchString(c.Bucket) || strings.Contains(c.Bucket, "..") || !regexp.MustCompile(`^[a-z0-9-]{1,64}$`).MatchString(c.Region) || len(c.AccessKey) < 3 || len(c.AccessKey) > 256 || len(c.SecretKey) < 3 || len(c.SecretKey) > 4096 || len(c.SessionToken) > 8192 {
		return nil, errors.New("invalid artifact storage configuration")
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	transport.ResponseHeaderTimeout = 10 * time.Second
	transport.MaxResponseHeaderBytes = 16 << 10
	client := &http.Client{Transport: transport, Timeout: 20 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("artifact redirects refused") }}
	sdk := s3.New(s3.Options{Region: c.Region, BaseEndpoint: aws.String(c.Endpoint), UsePathStyle: true, Credentials: credentials.NewStaticCredentialsProvider(c.AccessKey, c.SecretKey, c.SessionToken), HTTPClient: boundedClient{client}, RetryMaxAttempts: 1, RequestChecksumCalculation: aws.RequestChecksumCalculationWhenRequired})
	h := sha256.Sum256([]byte(c.Endpoint + "\n" + c.Region + "\n" + c.Bucket))
	return &Store{client: sdk, bucket: c.Bucket, id: hex.EncodeToString(h[:])}, nil
}

// Prepare requires versioning. Only explicit local development can provision a bucket.
func (s *Store) Prepare(ctx context.Context, provision bool) error {
	if provision {
		_, e := s.client.HeadBucket(ctx, &s3.HeadBucketInput{Bucket: &s.bucket})
		if e != nil {
			var response *smithyhttp.ResponseError
			if !errors.As(e, &response) || response.HTTPStatusCode() != 404 {
				return errors.New("artifact bucket unavailable")
			}
			if _, e = s.client.CreateBucket(ctx, &s3.CreateBucketInput{Bucket: &s.bucket}); e != nil {
				return errors.New("artifact bucket creation failed")
			}
		}
		if _, e = s.client.PutBucketVersioning(ctx, &s3.PutBucketVersioningInput{Bucket: &s.bucket, VersioningConfiguration: &types.VersioningConfiguration{Status: types.BucketVersioningStatusEnabled}}); e != nil {
			return errors.New("artifact versioning setup failed")
		}
	}
	versioning, e := s.client.GetBucketVersioning(ctx, &s3.GetBucketVersioningInput{Bucket: &s.bucket})
	if e != nil || versioning.Status != types.BucketVersioningStatusEnabled {
		return errors.New("artifact storage requires an accessible versioned bucket")
	}
	return nil
}
func (s *Store) Put(ctx context.Context, key string, payload []byte) (Reference, error) {
	return s.put(ctx, key, payload, false)
}

// Restore accepts an existing key only when its pinned bytes exactly match the backup.
func (s *Store) Restore(ctx context.Context, key string, payload []byte) (Reference, error) {
	return s.put(ctx, key, payload, true)
}
func (s *Store) put(ctx context.Context, key string, payload []byte, recovery bool) (Reference, error) {
	if !objectKey.MatchString(key) || len(payload) == 0 || len(payload) > MaxBytes {
		return Reference{}, errors.New("invalid artifact key or size")
	}
	h := sha256.Sum256(payload)
	ref := Reference{Store: s.id, Key: key, SHA256: hex.EncodeToString(h[:]), Bytes: int64(len(payload))}
	out, e := s.client.PutObject(ctx, &s3.PutObjectInput{Bucket: &s.bucket, Key: &key, Body: bytes.NewReader(payload), IfNoneMatch: aws.String("*"), ContentType: aws.String("application/octet-stream")})
	if e != nil {
		var response *smithyhttp.ResponseError
		if !recovery || !errors.As(e, &response) || response.HTTPStatusCode() != 412 {
			return Reference{}, errors.New("artifact write failed")
		}
		existing, err := s.client.HeadObject(ctx, &s3.HeadObjectInput{Bucket: &s.bucket, Key: &key})
		if err != nil {
			return Reference{}, errors.New("existing recovery object unavailable")
		}
		ref.Version = aws.ToString(existing.VersionId)
	} else {
		ref.Version = aws.ToString(out.VersionId)
	}
	if _, e = s.Read(ctx, ref); e != nil {
		return Reference{}, e
	}
	return ref, nil
}
func (s *Store) Read(ctx context.Context, ref Reference) ([]byte, error) {
	if ref.Store != s.id || !objectKey.MatchString(ref.Key) || ref.Version == "" || ref.Version == "null" || len(ref.Version) > 1024 || !digest.MatchString(ref.SHA256) || ref.Bytes < 1 || ref.Bytes > MaxBytes {
		return nil, errors.New("invalid artifact reference")
	}
	out, e := s.client.GetObject(ctx, &s3.GetObjectInput{Bucket: &s.bucket, Key: &ref.Key, VersionId: &ref.Version})
	if e != nil {
		return nil, errors.New("artifact read failed")
	}
	defer func() { _ = out.Body.Close() }()
	b, e := io.ReadAll(io.LimitReader(out.Body, ref.Bytes+1))
	h := sha256.Sum256(b)
	if e != nil || int64(len(b)) != ref.Bytes || hex.EncodeToString(h[:]) != ref.SHA256 || aws.ToString(out.VersionId) != ref.Version {
		return nil, errors.New("artifact integrity check failed")
	}
	return b, nil
}

// Bound SDK error bodies as well as object reads.
type boundedClient struct{ *http.Client }
type boundedBody struct {
	io.Reader
	io.Closer
}

func (c boundedClient) Do(r *http.Request) (*http.Response, error) {
	out, e := c.Client.Do(r) // #nosec G704 -- SDK requests use the validated installation storage endpoint; redirects are disabled.
	if e == nil {
		out.Body = boundedBody{io.LimitReader(out.Body, MaxBytes+1), out.Body}
	}
	return out, e
}
