package artifactstore

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"os"
	"strings"
	"testing"
	"time"
)

func TestConfiguration(t *testing.T) {
	c := Config{Endpoint: "https://storage.example.test", Region: "us-east-1", Bucket: "providah-artifacts", AccessKey: "key", SecretKey: "secret"}
	if _, e := New(c, false); e != nil {
		t.Fatal(e)
	}
	for _, endpoint := range []string{"http://rustfs:9000", "https://user:password@storage.example.test", "https://storage.example.test/path", "https://storage.example.test?x=y", "http://169.254.169.254", "http://localhost:9000"} {
		c.Endpoint = endpoint
		if _, e := New(c, false); e == nil {
			t.Fatal("unsafe endpoint accepted", endpoint)
		}
	}
	c.Endpoint = "http://rustfs:9000"
	if _, e := New(c, true); e != nil {
		t.Fatal(e)
	}
}
func TestRustFSObjects(t *testing.T) {
	if os.Getenv("TEST_RUSTFS") != "1" {
		t.Skip("requires local RustFS")
	}
	raw, e := os.ReadFile("../../.local/artifact-store.json")
	if e != nil {
		t.Fatal(e)
	}
	var c Config
	if json.Unmarshal(raw, &c) != nil {
		t.Fatal("invalid fixture config")
	}
	c.Endpoint = "http://127.0.0.1:19000"
	c.Bucket = "providah-artifact-test-" + time.Now().Format("20060102150405.000000000")
	store, e := New(c, true)
	if e != nil {
		t.Fatal(e)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if e = store.Prepare(ctx, true); e != nil {
		t.Fatal(e)
	}
	defer func() {
		versions, e := store.client.ListObjectVersions(ctx, &s3.ListObjectVersionsInput{Bucket: &c.Bucket})
		if e != nil {
			t.Error("cleanup list failed")
			return
		}
		for _, v := range versions.Versions {
			_, _ = store.client.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: &c.Bucket, Key: v.Key, VersionId: v.VersionId})
		}
		if _, e = store.client.DeleteBucket(ctx, &s3.DeleteBucketInput{Bucket: &c.Bucket}); e != nil {
			t.Error("cleanup failed")
		}
	}()
	key := "states/" + strings.Repeat("a", 64) + "/" + strings.Repeat("b", 64) + "/" + strings.Repeat("c", 64)
	payload := []byte("encrypted bytes fixture")
	ref, e := store.Put(ctx, key, payload)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = store.Put(ctx, key, []byte("conflicting bytes")); e == nil {
		t.Fatal("immutable key overwritten")
	}
	recovered, e := store.Restore(ctx, key, payload)
	if e != nil || recovered != ref {
		t.Fatal("exact recovery retry failed", e)
	}
	if _, e = store.Restore(ctx, key, []byte("conflicting recovery bytes")); e == nil {
		t.Fatal("recovery accepted conflicting object")
	}
	// An operator overwrite must not redirect reads away from the pinned version.
	if _, e = store.client.PutObject(ctx, &s3.PutObjectInput{Bucket: &c.Bucket, Key: &key, Body: bytes.NewReader([]byte("new version"))}); e != nil {
		t.Fatal("overwrite fixture failed")
	}
	got, e := store.Read(ctx, ref)
	if e != nil || !bytes.Equal(got, payload) {
		t.Fatal("pinned read changed", e)
	}
	bad := ref
	bad.SHA256 = strings.Repeat("0", 64)
	if _, e = store.Read(ctx, bad); e == nil {
		t.Fatal("hash mismatch accepted")
	}
	bad = ref
	bad.Bytes--
	if _, e = store.Read(ctx, bad); e == nil {
		t.Fatal("size mismatch accepted")
	}
	bad = ref
	bad.Store = strings.Repeat("0", 64)
	if _, e = store.Read(ctx, bad); e == nil {
		t.Fatal("foreign store accepted")
	}
	if _, e = store.client.DeleteObject(ctx, &s3.DeleteObjectInput{Bucket: &c.Bucket, Key: &key, VersionId: aws.String(ref.Version)}); e != nil {
		t.Fatal("delete fixture failed")
	}
	if _, e = store.Read(ctx, ref); e == nil {
		t.Fatal("missing pinned version silently replaced")
	}
}
