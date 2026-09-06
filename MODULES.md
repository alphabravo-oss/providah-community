# Provider module controls

**Provider modules** controls the bundled AWS, DigitalOcean, and Hetzner integrations independently within each organization. The page shows current enablement, connection count, submitted/unresolved operation count, worker contract major, and implemented server capabilities. It uses the shared TanStack Query/Table/Form components and modal shell, refreshed by organization SSE.

Viewing requires `modules.read`; changing state requires `modules.manage`, recent MFA, and typing the provider ID. Built-in roles receive module visibility, while only the built-in administrator receives module management. Custom roles need explicit grants. These are organization grants, not installation-administrator authority to install arbitrary code.

## Disable and re-enable

Disabling a module does not stop or delete cloud resources, reveal/remove credentials, or alter individual connection enablement. Cached inventory remains visible, with the module restriction shown in resource details. Configuration can remain stored while execution is disabled.

New discovery and mutation requests are rejected. Scheduled discovery excludes disabled providers. Scans, operations, and schedule targets carry an organization/provider revision captured when queued or saved. Every actual module state change increments that revision. The worker checks it before new execution; discovery checks it again before publishing results.

Queued work from an older revision is canceled or skipped when its worker processes it, including after the module is re-enabled. No missed occurrence or canceled mutation is replayed. A stale scan cannot replace inventory. Affected schedules must be edited/saved with current targets and independently approved again. Preview identifies disabled provider targets; the schedule list no longer presents a stale module target as approved.

Work already released to a worker may finish. Minimal observation of submitted operations remains allowed through a disabled module while the existing credential/authority checks permit it. The controls do not revoke cloud-side credentials or terminate already-submitted effects. The worker remains pinned to the installation's configured image.

A repeated request for the current enablement state is a no-op. Each actual change is audited and reaches the existing verified notification destinations. Other providers and organizations are unaffected.

## What this does not yet implement

The displayed revision is an authorization revision, **not a provider release version**. Deployment-approved runtime selection and rollback are now available; [RUNTIMES.md](RUNTIMES.md) defines configuration, immutable job binding, and old-runtime observation. Contract v1 describes the current bounded worker protocol, not a stable published third-party SDK promise. The bundled runtime still packages the three adapters together, uses maintained provider Go SDKs, and covers server discovery/power actions only.

Signed OCI provider releases, trusted registry administration, UI-triggered installation/uninstall, complete Describe/metrics/schema contracts, arbitrary provider IDs, and isolated third-party UI assets are not currently supported. This page does not simulate those capabilities or accept arbitrary images/code. Installation state and organization enablement remain separate concepts.

Validation uses disposable PostgreSQL workflows and fake provider calls. It covers permission denial, queued work across disable/enable, canceled scan publication, stale schedule review/dispatch, preserved observation, retained inventory, and unaffected provider state. The browser checks confirmation, disabled resource actions, re-enabling, and shared table/modal presentation. No cloud accounts are contacted.
