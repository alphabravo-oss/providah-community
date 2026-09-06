package database

import (
	"context"
	"embed"
	"fmt"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
)

//go:embed migrations/*.sql
var migrations embed.FS

func Open(ctx context.Context, url string) (pool *pgxpool.Pool, err error) {
	return OpenEdition(ctx, url, "community")
}

func OpenEdition(ctx context.Context, url, edition string) (pool *pgxpool.Pool, err error) {
	if edition == "" {
		edition = "community"
	}
	if edition != "community" && edition != "enterprise" {
		return nil, fmt.Errorf("invalid installation edition")
	}
	config, err := pgxpool.ParseConfig(url)
	if err != nil {
		return nil, err
	}
	config.ConnConfig.Tracer = queryTracer{}
	pool, err = pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, err
	}
	opened := pool
	defer func() {
		if err != nil {
			opened.Close()
		}
	}()
	conn, err := pool.Acquire(ctx)
	if err != nil {
		return nil, err
	}
	defer conn.Release()
	if _, err = conn.Exec(ctx, "SELECT pg_advisory_lock(74193821)"); err != nil {
		return nil, err
	}
	defer func() {
		if _, e := conn.Exec(context.Background(), "SELECT pg_advisory_unlock(74193821)"); e != nil {
			_ = conn.Conn().Close(context.Background())
		}
	}()
	var marker *string
	if err = conn.QueryRow(ctx, "SELECT to_regclass('public.installation_edition')::text").Scan(&marker); err != nil {
		return nil, err
	}
	if marker != nil {
		var installed string
		if err = conn.QueryRow(ctx, "SELECT edition FROM installation_edition WHERE singleton").Scan(&installed); err != nil {
			return nil, err
		}
		if installed == "enterprise" && edition != "enterprise" {
			return nil, fmt.Errorf("enterprise state requires the matching Enterprise build or a pre-upgrade backup")
		}
	}
	db := stdlib.OpenDBFromPool(pool)
	defer func() { _ = db.Close() }()
	goose.SetBaseFS(migrations)
	if err = goose.SetDialect("postgres"); err != nil {
		return nil, err
	}
	if err = goose.UpContext(ctx, db, "migrations"); err != nil {
		return nil, fmt.Errorf("migrations: %w", err)
	}
	if edition == "enterprise" {
		if _, err = conn.Exec(ctx, "UPDATE installation_edition SET edition='enterprise' WHERE singleton"); err != nil {
			return nil, err
		}
	}
	return pool, nil
}
