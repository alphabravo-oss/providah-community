# Providah Community

Self-hosted cloud operations console. Go/chi + Connect-RPC, PostgreSQL, React/Vite/Tailwind.

Apache-2.0 cloud operations console with Hetzner, DigitalOcean and AWS support. Inventory uses the shared `ab-provider-modules` library.

For temporary read-only cloud testing, see [CLOUD_SMOKE.md](CLOUD_SMOKE.md). Engineering choices and explicit guide deviations are recorded in [TECHNOLOGY_DECISIONS.md](TECHNOLOGY_DECISIONS.md).

## Run locally

Requirements: Go matching `go.mod`, Node 24+, Docker Compose.

```sh
cd web && npm ci && cd ..
make dev
```

In a second terminal:

```sh
make web
```

Open http://localhost:5173. The first command creates a private `.env` with persistent encryption/session keys and a setup token, then starts PostgreSQL and the API. Copy `BOOTSTRAP_TOKEN` from `.env` into the setup screen. Enroll the displayed key in an authenticator app and create your administrator and organization. Codes use 30-second periods; a used code cannot be reused to log in.

Keep `.env` private and backed up separately from the database. Never regenerate it against an existing database: doing so loses access to encrypted credentials and MFA seeds. Generate single-use backup sign-in codes from your profile while the authenticator is available; see [RECOVERY.md](RECOVERY.md). Lost-authenticator replacement and administrator disaster recovery remain pending. Do not use a production cloud credential for development.

## Run the container build

```sh
make up
```

For faster repeat builds using host Go/npm caches, run `make up-fast` on a host matching the Docker engine architecture.

Open http://localhost:8760. This builds the UI and Go binary and serves them from a non-root container. The Compose file is a **local development installation**, with a loopback-bound database and development password. TLS, production secrets, HA, upgrade/recovery proof, and production deployment hardening remain required before deployment beyond a trusted development host.

The container uses host port 8760; development uses API port 8080 and UI port 5173. Both use the local database. Stop the app and discovery launcher with `docker compose stop app launcher` when it is no longer needed.

## What works

- One-time bootstrap, local password + TOTP MFA, short-lived JWT cookies, rotating refresh cookies, logout, and database-backed next-request revocation.
- Organization-scoped permission checks at the Connect interceptor; no secret-bearing read API.
- AWS, DigitalOcean, and Hetzner credential storage, enable/disable, and rotation with recent-MFA checks.
- Transactional audit records, protected against updates/deletes.
- Tenant-scoped inventory query and pagination, connection management, overview, and audit UI.
- Team directory, built-in and custom roles, member enable/disable, immediate next-request permission checks, and last-administrator protection.
- Single-use 48-hour invitation links, new-user password/TOTP enrollment, authenticated existing-user acceptance, revocation, and role-version checks. Links are shown only when created and are shared manually; email delivery remains pending.
- In-place MFA verification for sensitive actions, with replay protection and an audit event.
- Organization-scoped SSE updates invalidate TanStack Query caches. Reconnect refreshes current state; access is rechecked every two seconds, and revocation clears the displayed organization data.
- TanStack Table for every grid, TanStack Form with Zod for all forms, shared page/modal components, and global CSS tokens. React Hook Form was removed.
- Responsive layout, keyboard-accessible dialogs, light/dark appearance.

Enabled connections refresh inventory every five minutes through AWS SDK for Go v2, DigitalOcean godo, and Hetzner hcloud-go/v2. **Refresh** requests an immediate scan; duplicate requests share the active job. Inventory includes servers, networks/VPCs, firewalls/security groups, volumes, and DigitalOcean/Hetzner load balancers. The shared table filters by provider and resource type and opens resource details in a modal. Failed or incomplete scans preserve inventory; a successful scan retires missing resources only in the types it covered. Disabling a connection retains inventory and pauses scans. See [INVENTORY.md](INVENTORY.md) for exact scope, permissions, regions, runtime compatibility, and remaining lifecycle work.

AWS has guided write-only fields for source keys, optional session token, role ARN, external ID, and account verification. The core exchanges source keys for temporary role credentials before invoking workers; see [AWS_AUTH.md](AWS_AUTH.md). DigitalOcean and Hetzner use masked API token fields. AWS defaults to `us-east-1`; DigitalOcean/Hetzner scan all regions unless filtered. Successful discovery confirms read access only, not permission for other actions. The UI never returns stored credentials.

## Provider modules

Use **Provider modules** to enable or disable each bundled provider for an organization. Changes block new execution and invalidate queued work without deleting resources, credentials, or inventory. Re-enabling does not replay old work. See [MODULES.md](MODULES.md) for schedule review and ongoing observation. [RUNTIMES.md](RUNTIMES.md) covers deployment-approved per-provider image selection/rollback and signed installation features still required.

## Account recovery codes

Open **Recovery codes** in your profile to generate ten single-use backup sign-in codes using your password and a fresh authenticator code. Store them safely; they are shown once. Recovery login revokes older sessions and does not bypass recent-MFA or approval requirements. See [RECOVERY.md](RECOVERY.md) for the implemented scope and remaining recovery gates.

## Audit export

Configure and verify S3-compatible storage in **Audit export**, then enable continuous delivery. The worker retains failed batches and verifies stored bytes before advancing its cursor. See [AUDIT_EXPORT.md](AUDIT_EXPORT.md) for credentials, endpoint restrictions, retry behavior, and capacity limits.

## Server actions

Open a discovered server in Inventory to request **Start**, **Graceful shutdown**, or **Restart**. The target and observed before-state are fixed when the review modal opens. Starting may incur provider charges. Shutdown uses the provider's graceful shutdown API and never falls back to force power-off.

In **Admin console → Resource management**, choose **Confirmation only** for solo development, **Approval for selected actions**, or **Approval for every action**. Confirmation only queues a permitted operation after confirmation; no second user is needed. Existing organizations retain the default of approving all actions except startup. When approval is required, another user with approval permission opens the operation, selects Approve, enters a reason, and submits the review. Approval lasts one hour; unreviewed requests expire after 24 hours. Requests and reviews still require recent MFA where MFA is enabled.

Policy changes require organization administration permission and an audited reason. Existing pending requests remain pending: cancel and submit again to use a new confirmation-only policy. Dispatch rechecks current approval requirements, so stricter policy can cancel queued work without approval. Permissions, typed deletion confirmation, dependency checks, maintenance rules and IaC protection remain enforced. Scheduled automation retains its separate schedule approval.

Operations are durable and visible in the Operations table. The system saves dispatch intent before calling a provider, allows one active/uncertain action per resource, rechecks authority and credential revisions, and disables SDK mutation retries. Acceptance becomes **Observing**, not success. Provider completion or the requested power state must be observed. Failed reads retry for up to fifteen minutes without resubmitting the mutation.

A lost submission response or expired dispatch lease becomes **Uncertain** and continues to block conflicting actions. After the worker lease expires, **Check provider state** performs reads only. A different authorized approver can record a manual resolution after independently verifying the provider state. Manual resolution is labeled separately from automatic success. AWS reboot does not expose a completion action through this adapter, so its completion remains uncertain pending independent verification.

These actions have SDK transport tests, database workflow tests, and browser review tests. No live cloud account or real server mutation has been used for validation. Configurable policies, scoped workspace grants, bulk actions, schedules, notifications, and other resource action families remain pending.

## Provider execution

`make up` and `make up-fast` package the provider worker and its launcher along with the console. Each discovery runs in a fresh non-root, read-only container with CPU/memory/process limits and the connection credential supplied only through stdin. The launcher resolves its configured local worker image to an immutable image ID on startup. The Make targets recreate the launcher when deploying so it picks up the rebuilt worker.

Only the launcher mounts the Docker socket. The console connects through a restricted Unix socket and cannot choose an executable, image, mount, command, or destination. Docker socket access is a privileged host capability: this launcher is trusted infrastructure. Third-party plugin signature validation, separate provider releases, outbound network policy, and stronger runner isolation remain future work; do not load arbitrary plugin images into this first-party launcher.

`make dev` alone does not start the launcher. Use the container installation for integrated discovery, or run `cmd/launcher` on the trusted development host with `PROVIDER_IMAGE` and `LAUNCHER_SOCKET` configured and set the same socket on the API. No cloud account has been used in automated tests.

## Checks

```sh
make test                 # security, SDK discovery, and launcher boundary checks
go vet ./...
make test-integration     # isolated real-PostgreSQL database; destroyed afterward
cd web && npx playwright install chromium && cd ..
make test-browser         # resets only the dedicated providah_browser database
make build
TEST_PROVIDER_IMAGE=providah-provider:dev go test ./internal/launcher -count=1 # optional built-worker smoke check
```

Browser tests use generated installation keys with disposable test accounts and credentials. Test traces and screenshots are ignored by Git and must not be published as production evidence.

## Generated contracts and queries

```sh
make tools
make proto
```

Protobuf/Connect clients and sqlc query code are checked in. No hosted code generator is required. Dependency versions are captured in `go.mod`, `go.sum`, and `web/package-lock.json`.

Connect API requests are currently browser-cookie based and require the configured exact `Origin`. The read-only `/api/events` SSE endpoint also accepts same-origin browser fetch metadata; it uses the same session and membership checks and never puts bearer tokens in the URL. Supported service-account/CLI authentication will be added explicitly; do not circumvent the origin check to expose an unauthenticated automation API.

## Scheduled server actions

Use **Schedules** to select up to 50 servers, choose a one-time action, recurring cron rule, or paired startup/shutdown rules, and preview local run times before saving. Another authorized user approves the exact schedule revision. Material edits require approval again. You can pause the schedule or disable its scoped automation identity independently.

Occurrences enter the normal operation pipeline. Missed runs and overlapping actions are skipped; shutdown remains graceful. The history shows actual operation status separately from queuing. Email and webhook delivery is available through verified destinations in **Notifications**. See [NOTIFICATIONS.md](NOTIFICATIONS.md) for SMTP setup, receiver verification, retries, and network restrictions.

## Maintenance policies

Use **Maintenance** to define allowed action windows for the organization or selected connections. Every applicable policy must be open; closed dates override overnight windows. Policy edits require fresh approval of existing work, and queued work cannot wait for a later window.

Out-of-window manual actions require an explicit exception reason and maintenance.override authority. Unless the organization uses confirmation only, another user with approval and maintenance.override permissions must approve. Scheduled work never inherits a manual exception. Current policies cover server power actions; workspace and automation-runner enforcement will follow their respective modules.

Server deletion is available through a provider impact preview, exact-ID confirmation, explicit delete permission, and approval when required by organization policy. It rechecks the reviewed impact before submission and confirms termination/absence afterward. See [DELETION.md](DELETION.md) for cascades, permissions, and remaining verification limits.

Encryption keys support mounted files, previous-key reads, and offline transactional re-encryption through `check-encryption` and `rotate-encryption`. Follow [KEY_ROTATION.md](KEY_ROTATION.md); retain old keys for historical backups and stop all core replicas during maintenance.

Single-server creation and recovery: [PROVISIONING.md](PROVISIONING.md).

Approved public SSH-key import through Inventory or CLI: [SSH_KEY_IMPORT.md](SSH_KEY_IMPORT.md).

Organization identity sign-in: [OIDC.md](OIDC.md). Per-organization SSO enforcement and local recovery policy: [IDENTITY_POLICY.md](IDENTITY_POLICY.md).

Fixed-target bulk server start, shutdown, and restart: [BULK_POWER.md](BULK_POWER.md).

Provider-backed server history and units: [METRICS.md](METRICS.md).

External cloud credentials: see [EXTERNAL_SECRETS.md](EXTERNAL_SECRETS.md) for pinned OpenBao / Vault-compatible KV v2 references and deployment settings.

Application metrics and request correlation: [TELEMETRY.md](TELEMETRY.md).

Authenticated read and operation CLI: build with `make cli`; see [CLI.md](CLI.md) for commands, MFA, JSON output and authentication limits.


## Optional local global administrator

For local testing, set `DEV_SEED_ADMIN=true` and optionally `DEV_SEED_ADMIN_EMAIL=admin@alphabravo.io` in `.env`, then run `make up-fast` (or `make seed-admin` after starting the updated app). This option is off unless explicitly enabled. On an empty installation it uses the normal bootstrap API, generates a unique password, and writes login details to `.local/seed-admin.json` with owner-only permissions. No fixed password is shipped in source or logs.

The explicit local maintenance command applies the seed grant once: the account becomes a global administrator with MFA disabled. Existing passwords are preserved. Subsequent runs do not restore revoked global access or overwrite the user's MFA setting. Disabling the seed flag stops seeding; it does not delete or demote the existing account. Keep the private seed file out of version control; it also contains the original authenticator enrollment secret.

Open <http://localhost:8760>, enter the generated email/password, and leave the authenticator field blank when MFA is disabled. The profile displays **Global administrator** and the current MFA setting. **Enable MFA** / **Disable MFA** require the password and enrolled authenticator code; changing the setting signs out all sessions. The local seed is intended for development and testing.

Global administrators receive all built-in administrator permissions across active organizations, including organizations where they have no membership and organizations created later. The same scope applies to API calls, SSE and operation execution. Local global-admin access is independent of organization SSO policy. Disabling the account or revoking the global flag takes effect on subsequent authorization checks. Independent approvals still require another person; global administration does not expose stored cloud secrets or bypass provider/maintenance checks. Global grants currently use the explicit deployment-maintenance path, not an organization-role editor.

Provider release coverage: [current native workflows, SDK versions and verification gaps](PROVIDER_COVERAGE.md).

## Enterprise

For additional capabilities and support, see [Providah Enterprise](ENTERPRISE.md) and contact AlphaBravo.
