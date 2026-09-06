//go:build integration

package database

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"github.com/jackc/pgx/v5/pgxpool"
	"net/url"
	"os"
	"testing"
)

func TestEditionUpgrade(t *testing.T) {
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("set TEST_DATABASE_URL")
	}
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	var random [8]byte
	if _, err := rand.Read(random[:]); err != nil {
		t.Fatal(err)
	}
	name := "test_edition_" + hex.EncodeToString(random[:])
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = admin.Exec(ctx, "DROP DATABASE "+name+" WITH (FORCE)") }()
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatal(err)
	}
	u.Path = "/" + name
	community, err := Open(ctx, u.String())
	if err != nil {
		t.Fatal(err)
	}
	community.Close()
	enterprise, err := OpenEdition(ctx, u.String(), "enterprise")
	if err != nil {
		t.Fatal(err)
	}
	enterprise.Close()
	if downgraded, err := Open(ctx, u.String()); err == nil {
		downgraded.Close()
		t.Fatal("Community accepted Enterprise state")
	}
}
