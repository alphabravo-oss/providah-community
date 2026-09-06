package auditstore

import (
	"bytes"
	"context"
	"fmt"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"io"
	"os"
	"strings"
	"testing"
	"time"
)

func TestDevelopmentRustFS(t *testing.T) {
	if os.Getenv("TEST_RUSTFS") != "1" {
		t.Skip("start make dev-stack, then TEST_RUSTFS=1 go test ./internal/auditstore -run DevelopmentRustFS")
	}
	raw, e := os.ReadFile("../../.local/dev-stack.env")
	if e != nil {
		t.Fatal(e)
	}
	values := map[string]string{}
	for _, line := range strings.Split(string(raw), "\n") {
		k, v, ok := strings.Cut(line, "=")
		if ok {
			values[k] = v
		}
	}
	client := s3.New(s3.Options{Region: "us-east-1", BaseEndpoint: aws.String("http://127.0.0.1:19000"), UsePathStyle: true, Credentials: credentials.NewStaticCredentialsProvider(values["RUSTFS_ACCESS_KEY"], values["RUSTFS_SECRET_KEY"], "")})
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	bucket := fmt.Sprintf("providah-smoke-%d", time.Now().UnixNano())
	key := "roundtrip"
	if _, e = client.CreateBucket(ctx, &s3.CreateBucketInput{Bucket: &bucket}); e != nil {
		t.Fatal(e)
	}
	defer func() {
		_, _ = client.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: &bucket, Key: &key})
		_, _ = client.DeleteBucket(ctx, &s3.DeleteBucketInput{Bucket: &bucket})
	}()
	payload := []byte("providah local object roundtrip")
	if _, e = client.PutObject(ctx, &s3.PutObjectInput{Bucket: &bucket, Key: &key, Body: bytes.NewReader(payload)}); e != nil {
		t.Fatal(e)
	}
	object, e := client.GetObject(ctx, &s3.GetObjectInput{Bucket: &bucket, Key: &key})
	if e != nil {
		t.Fatal(e)
	}
	defer func() { _ = object.Body.Close() }()
	got, e := io.ReadAll(object.Body)
	if e != nil || !bytes.Equal(got, payload) {
		t.Fatal("S3 payload mismatch", e)
	}
}
