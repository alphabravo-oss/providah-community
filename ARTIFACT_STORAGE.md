# Protected automation objects

When `ARTIFACT_STORE` or `ARTIFACT_STORE_FILE` is configured, new imported source bundles and managed state versions are stored in a versioned S3-compatible bucket using the existing AWS Go SDK. The development Compose stack configures RustFS automatically. The configuration is deployment-owned, never a tenant-provided destination.

The JSON configuration has `Endpoint`, `Region`, `Bucket`, `AccessKey`, `SecretKey` and optional `SessionToken`. Use a private file and the `_FILE` setting. HTTPS service-root endpoints are required outside local capture mode. Only the exact development RustFS endpoints permit HTTP. Requests do not use proxy environment variables, follow redirects or log raw storage errors. Production buckets must already exist with versioning enabled; local development provisions `providah-artifacts` and enables versioning at startup.

Each source bundle or state file is encrypted with a fresh age identity before upload. PostgreSQL stores an age-encrypted envelope containing that identity, the storage identity, generated object key, exact S3 version, ciphertext checksum and size. The installation encryption key protects the envelope. A master-key rotation rewraps these envelopes through the existing offline rotation command; object files need no rewrite. Object names contain generated organization/source/project/state IDs, never credentials, labels or source text.

Writes use create-only object keys and verify the uploaded version's bytes before committing the state version, project pointer and audit event in one database transaction. Reads fetch that exact version, verify its size and checksum, decrypt it, and verify the original state lineage, serial and checksum. A later overwrite cannot substitute a different version. Missing storage, deleted versions, changed endpoints/buckets, altered envelopes and corrupt bytes fail closed. A failed upload never advances the database state. The existing capability, RBAC, tenant, session, lock ownership and concurrency checks still apply.

Existing database-backed state remains readable; new writes use the configured store. There is no automatic history migration or deletion. Without storage configuration, new writes retain the encrypted database implementation. Do not disable the development stack while testing object-backed reads: its RustFS service must be running. No browser API returns raw state, envelopes, S3 credentials, object keys or download URLs.

Imported ZIP bundles use the same encrypted envelopes and exact-version checks. Archive validation happens before storage. Source metadata and audit commit only after upload verification, and repeated imports return the existing immutable source without another upload. Validation loads the pinned object and revalidates the source hash, archive contract and inputs before calling the runner. Existing database-backed bundles remain usable. The 4 MiB ZIP/16 MiB expanded limits and 50-source catalog bound remain in force; raw source is never returned by catalog APIs.

## Operations and recovery

Give the production runtime only bucket-versioning inspection, object creation and version-specific reads. Do not grant object/version deletion, lifecycle changes, bucket-policy changes or versioning suspension. Provision and protect the bucket using separate administrative credentials. The local stack uses its generated RustFS development administrator credentials; it is not a production IAM configuration. Versioning prevents silent substitution; it is not object-lock retention or protection from a storage administrator.

Preserve the database, its installation keyring and **all referenced object versions** together. Database-only backups cannot recover object-backed state. Restoring current object bytes under a new version ID does not satisfy an existing reference. Storage identity includes endpoint, region and bucket; transparent rebinding to a different store is intentionally refused. The offline restore command explicitly rebinds and verifies those references before cutover.

A process failure after upload but before database commit can leave an unreferenced encrypted object. It cannot advance state, and its per-object key was not committed. There is deliberately no automatic garbage collection: retention, reference-aware cleanup and online migration remain outstanding. Plans and run logs are not stored by this implementation.

## Checks

`make test-dev-stack` checks real RustFS immutable writes, pinned-version reads, corruption/missing-version rejection and notification capture. To run the full database state lifecycle against RustFS with the trusted provider-free CLI fixtures:

```sh
TEST_RUSTFS=1 TEST_STATE_CLIS=/opt/homebrew/bin/tofu:/opt/homebrew/bin/terraform TEST_DATABASE_URL='postgres://providah:providah@127.0.0.1:55432/providah?sslmode=disable' go test -race -tags integration ./internal/core -count=1
```

The checks create isolated databases and uniquely named temporary buckets, remove only their own fixture objects and buckets, and never use cloud credentials or imported user automation on the host.

## Read-only recovery verification

Run `/providah check-artifacts` with the installation configuration, matching encryption keyring, database connection and artifact store configuration. In the local stack:

```sh
docker compose -f compose.yaml -f compose.dev.yaml exec -T app /providah check-artifacts
```

For a restored database, run a separate instance of that command with `DATABASE_URL` pointing to the recovery target; do not start the server or workers against it. This command skips migrations and runner admission, never provisions storage, and accepts read-only database defaults. It reads one source/state record at a time in a repeatable-read snapshot. Success returns only source/state counts. Failures identify a record ID and type where available, never source/state contents, keys or backend error bodies.

The check decrypts every retained source/state, verifies source manifests and input declarations, state lineage/serial/hash/size, and each current project pointer. It fails on missing storage or pinned versions, corruption, wrong keys and metadata mismatches. Master-key rotation and a restored database can use the same check without rewriting objects.

The integration recovery drill restores an encrypted database backup after key rotation, compares all table digests, and verifies all restored source/state references against the **retained original RustFS object versions**. This proves database/key/reference recovery while objects survive. The additional object-loss drill below verifies restore into new object versions. Offsite replication, retention and production RPO/RTO remain unproven.

## Backup and restore after object-store loss

`/providah backup-artifacts DIRECTORY` creates a **new** mode-0700 directory containing only encrypted object bytes, named by ciphertext checksum, and a completion marker written last. Files are mode 0600 and synced before success. Existing directories/files are never overwritten. A failed backup may leave an incomplete directory; choose a new destination after addressing the failure. The database holds the separately encrypted per-object keys and references, so this directory alone cannot decrypt the artifacts.

Use a matching backup set:

1. Stop/fence all core replicas and worker coordinators. Keep PostgreSQL and artifact storage available. Writers must stay stopped while making both backups.
2. Run `backup-artifacts` and the [encrypted database backup](DATABASE_BACKUP.md) against that frozen installation. Securely escrow the installation keyring and configuration separately. Copy the backup set off-host using your protected backup process.
3. Restore the trusted database backup into the fresh `providah_restore_...` database created by the existing restore utility. Keep its read-only defaults and application fencing.
4. Provision a destination bucket with versioning enabled. Configure `ARTIFACT_STORE_FILE` for that destination and `DATABASE_URL` for the restored database, then run `/providah restore-artifacts DIRECTORY`.
5. Run `/providah check-artifacts` with the restored configuration, review pending operations/schedules and fence the original installation before any cutover. Restore does not start servers, workers, cloud actions or schedules, or change database read-only defaults.

The default app container has a read-only filesystem. For containerized backup, use a separate maintenance invocation with a writable backup mount, for example `docker compose -f compose.yaml -f compose.dev.yaml run --rm --no-deps -v /secure/recovery:/recovery app backup-artifacts /recovery/artifacts`. Prepare that host directory for the non-root container user (65532). Supply the restored database/destination-store settings to the separate restore invocation; never run restore against the active installation.

Restore is restricted to database names matching `providah_restore_...` with read-only defaults. Its explicit maintenance transaction permits only its own writes, serializes with key maintenance and locks artifact metadata. Every backup file is bounded, checksum-verified and confined to the backup directory; nonregular files and escaping symlinks are rejected. It creates destination objects without overwriting existing keys. A retry accepts an existing destination version only after reading and verifying identical bytes. After all copies, it rechecks all decrypted contents and pointers, then commits the updated encrypted references and per-organization recovery audit together. Failure rolls back all database changes; already copied encrypted objects may remain and are safely reused on retry. Nothing is automatically deleted.

The integration drill restores all 40 database tables after key rotation, deletes the original disposable RustFS bucket, rejects a deliberately damaged backup without partial database updates, restores all source/state objects into a second bucket with new version IDs, verifies their decrypted contents and confirms idempotent retry. This establishes the implemented artifact recovery path; it does not establish scheduled/offsite backups, retention or production recovery-time targets.
