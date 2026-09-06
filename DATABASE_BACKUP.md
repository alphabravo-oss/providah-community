# Encrypted database backup and restore

`scripts/database_backup.py` wraps native PostgreSQL tools and age using Python's standard library. It makes encrypted logical database archives; it does not switch the running application to a restored database.

## Requirements and connection

Use Python 3, age (including age-keygen for tests), and PostgreSQL client tools compatible with the server. This installation uses PostgreSQL 17; on this development Mac prepend `/opt/homebrew/opt/postgresql@17/bin` to PATH. The default PostgreSQL 14 tools cannot dump this server.

Set libpq connection settings (`PGHOST`, `PGPORT`, `PGUSER`, `PGDATABASE`) and use a protected `PGPASSFILE` for the password. `PGDATABASE` must be a simple source database name, not a connection URL. Configure certificate-verified TLS for remote servers. Native tools inherit these settings; the script never places passwords in command arguments or reports raw SQL errors.

## Back up and verify

Use an existing public age recipient whose private identity is securely escrowed separately from the host. Replace the example paths and recipient below. The destination parent directory must exist.

```sh
python3 scripts/database_backup.py backup /secure/backups/console-2026-09-05.age --recipient age1YOUR_PUBLIC_RECIPIENT
python3 scripts/database_backup.py verify /secure/backups/console-2026-09-05.age --identity /secure/offline/backup-identity
```

The backup streams custom-format pg_dump output directly into age. It publishes a mode-0600 encrypted file only after both tools succeed, using an atomic hard link on the same local POSIX filesystem and file/directory fsync. Existing paths, including symlinks, are never replaced. Choose unique archive names and arrange retention and offsite copies separately.

Verification authenticates and decrypts the entire file into a private temporary directory, then checks the PostgreSQL archive directory. It does **not** prove that every table can be restored. Temporary plaintext is removed on normal completion or handled failure; abrupt host/process termination can leave temporary files. Use a protected encrypted temporary filesystem with sufficient capacity.

## Restore drill

Restore only backups from a trusted source: recipient encryption alone does not authenticate the sender, and pg_restore executes SQL from the archive. See the [PostgreSQL restore documentation](https://www.postgresql.org/docs/17/app-pgrestore.html) and [age documentation](https://github.com/FiloSottile/age).

Keep the application disconnected from the target. Set the connection variables to the recovery server and `PGDATABASE` to an existing maintenance database there. The role needs database-creation privileges.

```sh
python3 scripts/database_backup.py restore /secure/backups/console-2026-09-05.age providah_restore_drill --identity /secure/offline/backup-identity
```

The full encrypted file must authenticate before database creation. The target name must begin with `providah_restore_` and must not already exist. Restore runs in a single transaction with ownership and ACL restoration disabled. Failures remove only a target created by this invocation; failed cleanup is reported for manual inspection. Successful targets default to read-only transactions. This default is an operational guard that privileged users can override, not a security or network isolation boundary.

Before considering cutover, validate schema and row data, recover matching encryption keys, verify credential decryption, and review pending operations, schedules, notification deliveries and audit exports. Fence the original workers and prevent old queued work or restored credentials from unexpectedly executing. Reconcile provider state and credential revisions. This utility neither performs that review nor enables writes or starts the application against the restored database.

## What requires separate recovery

Object-backed managed state requires its exact versioned RustFS/S3 objects as well as the database and installation encryption keys. The logical database backup contains the encrypted object references and keys, not the objects. See [ARTIFACT_STORAGE.md](ARTIFACT_STORAGE.md); the artifact backup/restore drill also verifies recovery into a different bucket after deleting the original object store. Pair the backups while application writers are stopped.


The archive contains database schema/data, including encrypted credential columns. It excludes PostgreSQL roles/grants, server configuration, external artifacts, provider runtime image catalogs/images, external Vault/OpenBao data and installation key files. Preserve the installation encryption keyring (including required previous keys), session signing material and deployment configuration securely. **Do not regenerate installation keys during recovery.** The archive's age identity is separate from the application's encryption keyring; both may be necessary.

Native commands have a ten-minute timeout, except best-effort failed-target cleanup. This is a bounded operator utility, not scheduled backup infrastructure. Immutable/offsite storage, retention automation, WAL/PITR, end-to-end key/artifact recovery and production fencing remain to be implemented and tested. The planned 15-minute RPO and one-hour RTO are not established by this logical restore drill.

## Reproducible checks

```sh
PATH="/opt/homebrew/opt/postgresql@17/bin:$PATH" make test-backup
```

Tests create and remove their own disposable databases and ephemeral test identities. They check encrypted round-trip data, permissions, overwrite refusal, wrong keys, truncated ciphertext, invalid recipients, fresh-target requirements and read-only behavior. The full application integration suite also restores its database and compares row counts and deterministic row digests for all 40 public tables at schema 35, including encrypted columns. The current integration drill also verifies decrypted sources and state through `check-artifacts` logic after restoring the database, using retained original object versions. No production database was restored, and no live cloud resources were changed.
