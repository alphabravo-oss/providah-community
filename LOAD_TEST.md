# Inventory read capacity check

Run `make test-load` on an otherwise idle development installation. It uses the local PostgreSQL connection from the Makefile to create a uniquely named disposable database, applies real migrations, loads synthetic records, runs the real authenticated HTTP API, then drops that database. It never starts workers or calls cloud accounts. The database user needs create/drop database permission.

For another PostgreSQL host:

```sh
TEST_DATABASE_URL='postgres://USER:PASSWORD@HOST/DATABASE?sslmode=require' go test -tags load ./internal/core -run '^TestInventoryLoad$' -count=1 -v
```

Use deployment-appropriate database TLS settings. The test does not print the database URL, access cookies or temporary signing/encryption keys. Credentials and sessions in the disposable fixture are synthetic; this measures authenticated requests, not enrollment or password hashing.

## Workload

- 100 organizations, 1,000 connections and 100,000 resources.
- One organization holds 50,000 resources; the remaining 50,000 are distributed among 99 organizations.
- 100 distinct authenticated users with current database membership checks.
- 100 concurrent closed-loop clients, with 12 requests per client per phase.
- Four equally weighted operations: first page sorted by name, next page sorted by name, provider/type/search-filtered page sorted by status, and resource details. Lists request 50 rows.
- Distributed-organization and single-hot-organization scenarios, each with first-pass and warm phases: 4,800 measured requests total.
- Each phase reports p50/p95/p99/max and throughput; any request failure or p95 at/above one second fails the check. A three-minute deadline bounds the run.

## Measured local baseline

Results below are a local development baseline, not a production capacity guarantee. HTTP client and Go server run in one host process; PostgreSQL runs in the local container VM. Requests include HTTP, session validation, current membership/identity-policy checks, SQL and response serialization.

Environment on 2026-09-05: Go 1.26.2, macOS ARM64, 16 host CPUs, 128 GiB host RAM; Docker VM configured with 6 CPUs and approximately 23.4 GiB RAM; pgx pool maximum 16 connections. No race detector or application tracing was enabled. Existing development app services were idle.

| Scenario | Phase | p50 | p95 | p99 | Maximum | Requests/sec |
|---|---|---:|---:|---:|---:|---:|
| Distributed | First-pass | 32.9 ms | 52.8 ms | 61.8 ms | 81.2 ms | 2,610 |
| Distributed | Warm | 30.7 ms | 36.0 ms | 38.2 ms | 63.7 ms | 2,879 |
| Hot organization | First-pass | 342.1 ms | 435.9 ms | 467.5 ms | 561.9 ms | 321 |
| Hot organization | Warm | 344.9 ms | 455.8 ms | 485.3 ms | 619.3 ms | 320 |

All 4,800 requests succeeded. The complete target took about 14 seconds including fixture setup and cleanup. Vet passed for the load-tagged code; cleanup left no load-test databases, and the existing local console remained ready.

## Limits and outstanding release gates

The first-pass phase is **not a cold-cache measurement**: seeding, ANALYZE and fixture lookups warm database/OS caches. This short, closed-loop workload has no browser rendering, remote network latency, think time or long soak. It measures the combined request mix rather than separate per-route p95 gates.

Production release still requires a documented cold-cache method, production-equivalent container/Kubernetes hardware, representative RBAC/MSP/workspace grants, discovery and bulk-write interference, transfers/automation running concurrently, sustained soak, failure/recovery and per-endpoint results. OIDC/JWKS and external credential-provider latency are not involved in stored inventory reads here. Availability, RPO and RTO are separate unverified gates.

No new database index was added: this measured profile must demonstrate a bottleneck before adding indexes solely for these sort paths.
