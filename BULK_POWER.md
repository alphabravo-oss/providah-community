# Bulk server power operations

Baseline: 2026-09-05. Inventory supports fixed-target start, graceful shutdown, and restart requests for up to fifty servers across the currently supported AWS, DigitalOcean, and Hetzner connections. Provider execution uses the existing Go SDK adapters and isolated workers; no bulk-specific cloud executor was added.

## Visual workflow

Open **Bulk power actions** in Inventory, choose exact targets from the displayed page, and select one action. The modal freezes the displayed selection; a filter change or new discovery cannot add targets. Preview lists names, provider identities, regions, observed state, action, and eligibility. Unavailable or inaccessible IDs reveal no resource metadata. Non-server kinds and unsupported runtimes are distinguished from currently ineligible targets.

Only eligible targets are submitted, with an explicit count and reason. Starting can incur charges; shutdown and restart require separate independent approval for each operation. Bulk deletion is not offered: existing single-server deletion retains its dependency preview and typed confirmation. Bulk maintenance overrides are not offered; use the existing reviewed single-operation exception path where authorized.

Each result contains either its durable operation or a per-target request error. The Operations page retains individual requests, approvals, state transitions, audit history, cancellation, and reconciliation after the bulk dialog closes. Successful targets are not rolled back when another target fails. There is no separate persistent batch dashboard in this implementation.

## Review and execution guarantees

`PreviewBulkPower` accepts explicit IDs, never a search expression. The response gives each eligible target a ten-minute signed review containing its organization, requesting user, resource, action, material status, connection revision, runtime/module revision, maintenance revision/window, and a unique request key. Tokens contain operational metadata only, no credentials. They use the existing JWT library with a separate required audience from access tokens and a fixed signing algorithm.

`RequestBulkPower` validates the complete envelope before writing intent: size limits, signature, expiry, issuer, audience, actor/organization binding, and duplicate targets. It then records each target through the same transactional function used by single-server operations. Current SSO, permissions, connection/module state, inventory freshness, material state, runtime support, and maintenance checks remain authoritative. A changed reviewed credential, runtime, or maintenance boundary requires a new preview. Existing dispatch checks and uncertain-outcome reconciliation are unchanged.

Transactions are independent and sequential, bounded to fifty. An interrupted response can leave some operations recorded. Retry the same review tokens and reason while valid: each stable key returns its original operation while the target remains accessible, including completed or uncertain work, rather than creating another action. Changed input under the same key is rejected. A successful bulk request response means intent was recorded, not that cloud work succeeded.

After review expiry, inspect Operations for recorded work before requesting a new preview. Pending/uncertain targets cannot acquire overlapping operations. Retrying a recorded operation does not redispatch it; failed eligible targets require fresh review and a new request. Uncertain targets require reconciliation first. There is no automatic bulk retry or blind cloud resubmission.

## API and UI

The versioned Connect API exposes `PreviewBulkPower` and `RequestBulkPower` using generated Go/TypeScript clients. Both require operation-request permission, resource-read permission, an authorized organization session, and recent MFA. There is no new bypass permission or schema migration.

The frontend reuses TanStack Query mutations, TanStack Table review/results, TanStack Form validation/selection, and the global modal/CSS components. It does not execute cloud SDK calls in the browser.

## Verification and scope

PostgreSQL tests exercise mixed eligibility, foreign-tenant/unknown-ID metadata isolation, duplicate/tampered review rejection before intent, changed-state partial results, stable-key retries, changed-reason rejection, normal queued execution, changed-credential fencing, approval/self-review checks, and invalid/expired review rejection. Browser tests exercise selection, unsupported-target display, exact token submission, and result tracking using mocked cloud-action responses. Existing provider SDK and worker tests continue to cover the underlying execution path. No live cloud actions are performed during these tests.

This implements bulk power for the existing server lifecycle. Other provider action groups, persistent batch grouping, bulk approval UI, and multi-resource provisioning remain separate requirements. See PROVIDER_COVERAGE.md for supported workflows.
