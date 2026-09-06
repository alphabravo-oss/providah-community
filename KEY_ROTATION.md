# Encryption key rotation

Providah uses age X25519 encryption. New writes use one current key; reads may also use up to eight distinct previous keys. This supports a controlled deployment change and offline re-encryption without replacing cloud credentials, MFA seeds, webhook signing keys, or other secret values.

## Key injection

- `AGE_IDENTITY`: current private identity, or `AGE_IDENTITY_FILE`: mounted file containing that identity.
- `AGE_PREVIOUS_IDENTITIES`: whitespace-separated previous private identities, or `AGE_PREVIOUS_IDENTITIES_FILE`: mounted file with one identity per line.
- Use the value or its file setting, not both. Files must be regular files, with a maximum input size of 16 KiB. Duplicate and invalid identities fail startup. File contents are raw identities, without comments or shell assignments.
- Mount files read-only and restrict access through the deployment's secret mechanism. Compose users must add the relevant mounts and container paths to their local override. Keep these files out of source control.

The application does not retrieve private keys through a browser API. Current and historical keys must be backed up separately from database backups. A public age recipient is used as the connection key ID and in maintenance reports.

## Offline procedure

Run these steps as a deployment administrator using the application's existing database and configuration environment. The commands need the regular ORIGIN, SESSION_KEY, and BOOTSTRAP_TOKEN configuration but do not start HTTP serving, schedulers, notification delivery, or provider workers. Launcher availability is not required.

1. Back up the database and retain every old key needed for that backup. Drain active operations and stop **all** core replicas/coordinators before proceeding.
2. Generate a new encryption identity into a private file. This command creates only an encryption key; it does not replace the session or bootstrap secrets:

   ```sh
   umask 077
   providah encryption-keygen > new.agekey
   ```

3. Configure the new identity as current and the old identities as previous keys through environment injection or mounted files. Do not overwrite or discard the historical key files. If switching to `AGE_IDENTITY_FILE`, remove the old `AGE_IDENTITY` value to avoid a conflicting setting.
4. Check every encrypted record without rewriting it:

   ```sh
   providah check-encryption
   ```

   The JSON report contains only the public key ID and per-table counts. `needs_rewrap` identifies records that require a previous key. Any unreadable record stops the command.
5. Re-encrypt the live database:

   ```sh
   providah rotate-encryption
   ```

6. Remove previous keys from the **runtime configuration**, then run `check-encryption` again while the application remains stopped. It must succeed with the new current key alone. Keep the old keys offline for historical backups until their retention periods end.
7. Start all core replicas with the same new current key and verify login, connection access, and readiness. Never resume an old replica configured to write with an old key.

The repository development equivalent is `go run ./cmd/providah <command>` with the existing environment loaded. Do not regenerate the whole `.env` using the initial `keygen` command against an existing database.

## Transaction and coverage

Rotation takes a database advisory lock and exclusive locks on secret tables, with a ten-second lock-acquisition timeout. It decrypts and re-encrypts in bounded batches inside **one transaction**. A missing key, corrupt ciphertext, schema mismatch, cancellation, or update failure aborts the transaction; no partial rotation commits. Concurrent maintenance commands are rejected. This requires downtime and sufficient transaction/WAL capacity; online batched rotation remains future work.

Coverage includes user TOTP seeds, pending bootstrap enrollment, cloud connections (including disabled/deleted records), pending invitation TOTP seeds, notification destination credentials, notification verification payloads, and audit-export credentials. Nullable cleared fields are skipped. A schema inventory check refuses rotation if a new ciphertext column is not covered.

Secret plaintext remains unchanged. Connection key IDs are updated, but connection/notification/audit-export credential revisions are preserved, so wrapping changes do not impersonate cloud credential rotation or invalidate approved work. Records already readable by the current key are verified and left byte-for-byte intact. Each successful write command emits `security.encryption_key_rotated` into every organization's audit and the existing notification pipeline; organization events contain the public key ID, not deployment-wide record counts or private material.

Injected SMTP credentials, session keys, bootstrap tokens, external vaults, provider credentials already held by workers, and historical database backups are outside this database re-encryption operation. Session signing-key rotation and external-vault integration remain separate unfinished work.

## Verification

Real PostgreSQL tests cover all seven encrypted columns, read-only preflight, full rollback after a later corrupt record, current-key-only readability, old-key rejection of rewritten records, unchanged plaintext/credential revision, idempotent reruns, and rejection of an unrecognized ciphertext column. Key-file checks cover injection, conflicting sources, and size bounds. Tests use disposable keys/databases; the local installation's existing key has not been rotated.

External KV v2 references use the existing connection ciphertext column and participate in age-key rotation. Resolved external values are never persisted; their backup and rotation remain external-store responsibilities. See [EXTERNAL_SECRETS.md](EXTERNAL_SECRETS.md).

## Metadata envelope conversion

All encrypted database fields now contain an age envelope with `key_id`, `algorithm` and `ciphertext`. Normal runtime reads reject older raw-age rows. Stop application writers, keep the original keys configured, and run `providah rotate-encryption` with the new binary before restarting it. This command converts old rows and verifies every new ciphertext in one transaction; a failed conversion commits nothing. Run `providah check-encryption` afterward. Keep the pre-conversion database backup and its keys together outside source history. No new keys are required merely to convert formats.
