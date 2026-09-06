//go:build integration

package core

import (
	"context"
	"encoding/json"
	"github.com/alphabravo-oss/providah-community/internal/artifactstore"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"os"
	"testing"
	"time"
)

func testArtifacts(t *testing.T) (*artifactstore.Store, func()) {
	t.Helper()
	ctx := context.Background()

	raw, e := os.ReadFile("../../.local/artifact-store.json")
	if e != nil {
		t.Fatal(e)
	}
	var config artifactstore.Config
	if json.Unmarshal(raw, &config) != nil {
		t.Fatal("invalid artifact fixture")
	}
	config.Endpoint = "http://127.0.0.1:19000"
	config.Bucket = "providah-state-test-" + randomID()[:32]
	store, e := artifactstore.New(config, true)
	if e != nil {
		t.Fatal(e)
	}
	if e = store.Prepare(ctx, true); e != nil {
		t.Fatal(e)
	}
	destroyed := false
	destroy := func() {
		if destroyed {
			return
		}
		cleanup, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		client := s3.New(s3.Options{Region: config.Region, BaseEndpoint: aws.String(config.Endpoint), UsePathStyle: true, Credentials: credentials.NewStaticCredentialsProvider(config.AccessKey, config.SecretKey, "")})
		versions, e := client.ListObjectVersions(cleanup, &s3.ListObjectVersionsInput{Bucket: &config.Bucket})
		if e != nil {
			t.Error("fixture cleanup list failed")
			return
		}
		for _, v := range versions.Versions {
			if _, e = client.DeleteObject(cleanup, &s3.DeleteObjectInput{Bucket: &config.Bucket, Key: v.Key, VersionId: v.VersionId}); e != nil {
				t.Error("fixture object cleanup failed")
				return
			}
		}
		if _, e = client.DeleteBucket(cleanup, &s3.DeleteBucketInput{Bucket: &config.Bucket}); e != nil {
			t.Error("fixture cleanup bucket failed")
			return
		}
		destroyed = true
	}
	t.Cleanup(destroy)
	return store, destroy
}

func exerciseArtifactCheck(t *testing.T, s *Service, ctx context.Context) {
	t.Helper()
	report, e := s.CheckArtifacts(ctx)
	if e != nil {
		t.Fatal(e)
	}
	var sources, states int
	if e = s.pool.QueryRow(ctx, "SELECT count(*) FROM automation_sources").Scan(&sources); e != nil {
		t.Fatal(e)
	}
	if e = s.pool.QueryRow(ctx, "SELECT count(*) FROM automation_states").Scan(&states); e != nil {
		t.Fatal(e)
	}
	if report.Sources != sources || report.States != states || sources == 0 || states == 0 {
		t.Fatal("artifact verification skipped records")
	}
	var id string
	var original []byte
	if e = s.pool.QueryRow(ctx, "SELECT id,ciphertext FROM automation_sources ORDER BY id LIMIT 1").Scan(&id, &original); e != nil {
		t.Fatal(e)
	}
	if _, e = s.pool.Exec(ctx, "UPDATE automation_sources SET ciphertext=$2 WHERE id=$1", id, []byte("corrupt")); e != nil {
		t.Fatal(e)
	}
	_, checkErr := s.CheckArtifacts(ctx)
	if _, e = s.pool.Exec(ctx, "UPDATE automation_sources SET ciphertext=$2 WHERE id=$1", id, original); e != nil {
		t.Fatal(e)
	}
	if checkErr == nil {
		t.Fatal("artifact check accepted corruption")
	}
	var checksum string
	if e = s.pool.QueryRow(ctx, "SELECT id,sha256 FROM automation_projects WHERE state_id<>'' LIMIT 1").Scan(&id, &checksum); e != nil {
		t.Fatal(e)
	}
	if _, e = s.pool.Exec(ctx, "UPDATE automation_projects SET sha256=repeat('0',64) WHERE id=$1", id); e != nil {
		t.Fatal(e)
	}
	_, checkErr = s.CheckArtifacts(ctx)
	if _, e = s.pool.Exec(ctx, "UPDATE automation_projects SET sha256=$2 WHERE id=$1", id, checksum); e != nil {
		t.Fatal(e)
	}
	if checkErr == nil {
		t.Fatal("artifact check accepted invalid current state pointer")
	}
	if s.cfg.Artifacts != nil {
		cfg := s.cfg
		cfg.Artifacts = nil
		missing, e := New(s.pool, cfg, s.log)
		if e != nil {
			t.Fatal(e)
		}
		if _, e = missing.CheckArtifacts(ctx); e == nil {
			t.Fatal("artifact check accepted missing store")
		}
	}
}
