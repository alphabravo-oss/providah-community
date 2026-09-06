# Continuous audit export

Open **Audit export → Configure storage** as an organization administrator with recent MFA. Save a public HTTPS S3-compatible service root, region, bucket, and dedicated storage credentials. Run **Test storage**, wait for its verified result, then **Enable export**. Saving configuration pauses export and requires verification again. Credentials are encrypted with the deployment's age key and are never returned by the read API. Back up that key separately from the database.

The storage probe creates a small object under `providah-audit/<organization-id>/checks/`. It remains in the bucket. Continuous export writes under `providah-audit/<organization-id>/`. Restrict credentials to that prefix with object write/read permissions (`s3:PutObject` and `s3:GetObject` on AWS); the exporter does not list or delete bucket objects. Bucket policies, encryption settings, and customer-managed encryption keys may impose additional requirements. The bucket must already exist.

Storage must support path-style S3 access, conditional `PutObject` with `If-None-Match: *`, and reading the resulting object. The AWS Go SDK handles signing and transport serialization. Static access keys and optional session tokens are supplied explicitly; ambient machine credentials are not used. Expired credentials cause retries until an administrator replaces the configuration. AWS role assumption and external vaults remain planned.

Endpoints are restricted to public HTTPS on port 443, with no credentials, query, fragment, or non-root path. Connections use the same DNS/address checks as notification delivery, reject private and metadata addresses, and use no HTTP proxy or redirects. Private S3 endpoints need the future explicit private-destination approval workflow. Do not weaken these checks to reach a private endpoint.

## Persistence and retries

Each batch contains at most 100 events. Its gzip JSONL document begins with a versioned manifest containing organization, first/last event IDs, and count; subsequent lines hold events. IDs are strings to preserve full precision. Detail fields are allowlisted; unknown fields and nested objects are omitted. Allowed free-text fields remain user content, so users must not paste secrets into names or audit reasons. This is a documented export representation, not a byte-for-byte dump of database rows.

The payload, SHA-256 checksum, range, and object key are committed locally before any storage request. Raw and compressed documents are capped at 4 MiB. A batch that exceeds that limit is retained locally and preparation fails; it is never silently skipped. Administrative capacity management and adaptive splitting remain pending.

An object key contains the range and payload checksum. Writes never overwrite an existing object. Both successful writes and “already exists” responses must be followed by reading and hashing the complete stored bytes. Only a matching length and checksum permits cursor advancement. ETags and object metadata alone are insufficient. Storage implementations that acknowledge writes without reliably persisting data are outside this guarantee.

The worker uses a 60-second lease and a 40-second attempt deadline. One SDK attempt is permitted; durable retries wait 1, 2, 4, 8, 16, 32, then at most 60 minutes between attempts. A crash or lost response reuses the same persisted object key and bytes. **Retry now** advances a pending retry. Successful batches retain their metadata and history while releasing the duplicate local payload. Export completion does not generate another source audit event, allowing an idle backlog to drain.

Pausing cancels pending work. An upload already in flight may finish, but a canceled or superseded job cannot advance the cursor. Replacing configuration resets the cursor to zero and backfills retained history after verification and enabling. Existing bucket objects are not removed; backfills may create overlapping ranges. Consumers should deduplicate by organization and event ID.

Audit inserts serialize ID allocation behind the organization lock, so a concurrent uncommitted lower ID cannot be skipped by a committed cursor. Deploy this change by stopping older application writers before starting the new version. Mixed-version rolling upgrades have not been validated. This is application-level coordination; direct database writers must not bypass it.

## Operations and limits

`audit.read` permits status/history viewing. Configuration, probes, enable/pause, and retries require both `audit.read` and `audit.export.manage`, plus recent MFA. Built-in administrators receive these grants; custom roles must receive them explicitly. API authorization is independent of visible buttons.

The shared TanStack Query/Table/Form page shows verification, backlog count, oldest pending event, committed cursor, attempts, object keys, and checksums. Organization SSE updates refresh it. Failures and excessive backlog generate events through the existing verified email/webhook destinations, coalesced by target/type into 15-minute buckets. The backlog warning threshold is 10,000 events or 24 hours.

Export outages do not block otherwise valid cloud actions while local audit persistence works. Required local audit failures still reject mutations. There is currently **no local audit pruning**, so unexported events are retained. Storage is finite: operators must monitor PostgreSQL/disk capacity and provision space. Storage quotas, disk-capacity alerts, configurable retention, and automated offboarding are not yet implemented. Never manually delete unexported history to reduce backlog.

Checksums detect accidental corruption and object mismatch. They are not a signed audit chain or tamper evidence against database/root administrators. External retention, encryption, backups, and object-lock policies are controlled by the bucket owner.

Validation uses the actual AWS SDK against fake HTTP transport, disposable PostgreSQL workflow/concurrency tests, and a browser configuration flow. No real S3 account, cloud resources, email, or webhook destination was contacted for these checks. Production storage compatibility and recovery drills remain required.


## Searching local audit history

Audit log → Filter events opens the shared TanStack Form modal. Actor, action and target are exact, case-sensitive matches; empty fields disable that filter. Percent signs and underscores are literal characters, not wildcards. Time boundaries use RFC3339 with an explicit timezone (for example `2026-09-05T00:00:00Z`); From is inclusive and Before is exclusive. Clear filters returns to the most recent unfiltered page.

The same fields are available on the versioned `ListAudit` API as `actor`, `action`, `target`, `occurred_from` and `occurred_before`. All filters are combined with AND inside the authorized organization query. Results retain descending audit-ID order, 100-row pages and opaque cursors. A cursor is tied to its organization and exact filter strings; changing any filter requires starting over. Clients without filters retain their existing pagination format. No filter searches secret payloads or changes the export stream.

Shared TanStack Query keys include all filters; the common event table, SSE invalidation and permission checks are unchanged. Invalid timestamps, reversed/equal time ranges and oversized filters are rejected by the server. The existing organization/ID index bounds tenant scanning; dedicated filter indexes should be added only after measuring large audit-history workloads. Full-text search, CSV downloads and persistent audit views are not implemented by this change.
