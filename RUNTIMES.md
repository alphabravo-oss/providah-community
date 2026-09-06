# Deployment-approved provider runtimes

The launcher can route AWS, DigitalOcean, and Hetzner to different immutable local images. An installation operator first builds/pulls and approves images outside the console. The console can then select an approved image for each organization/provider, including selecting a previous image for rollback. It cannot pull arbitrary images, alter Docker flags, or install code from a UI request.

Set `PROVIDER_RUNTIMES` for the launcher to a JSON object mapping provider IDs to ordered image-reference arrays. For example, using images that your deployment process has already built or pulled:

```json
{
  "aws": ["your-registry/providah-aws:1.1.0", "your-registry/providah-aws:1.0.0"],
  "digitalocean": ["your-registry/providah-do:1.0.0"],
  "hetzner": ["your-registry/providah-hetzner:1.2.0", "your-registry/providah-hetzner:1.1.0"]
}
```

This example is configuration syntax, not an available registry recommendation. The current bundled worker implements all three adapters; separate approved images may use different builds of that worker. With no `PROVIDER_RUNTIMES`, `PROVIDER_IMAGE` supplies one bundled image for all three providers, preserving the development setup. The first configured image for a provider is its default for new work when the organization has no explicit selection. Providers absent from a nonempty catalog cannot dispatch new work.

Startup resolves each local reference to a Docker image ID (`sha256:…`) and probes each unique image once. The probe uses `--describe`, no network, no credentials, non-root execution, read-only root, dropped capabilities, no-new-privileges, CPU/memory/PID limits, bounded output, and a deadline. The response must declare the supported provider, matching protocol major, worker version, and SDK version. The bundled worker derives SDK versions from Go build information; its version defaults to `development` and can be set at build with `-ldflags='-X main.version=1.2.0'`.

A failed/incompatible probe prevents launcher readiness. Compose waits for the launcher's catalog health check before starting the core. The core loads a validated immutable catalog at startup. Catalog changes require restarting launcher and core; they are not accepted from tenant requests. Up to 32 provider/image entries are supported. Tag changes after startup cannot change the admitted immutable image IDs.

## Selection and rollback

In **Provider modules**, open a provider and choose **Change runtime / rollback**. Review the current image, active-work count, candidate version/SDK/image identity, and confirmation. Selection requires `modules.manage`, recent MFA, the exact provider confirmation, and the module revision that was reviewed. A stale revision fails instead of applying over another administrator's change. Disabled modules remain disabled after a runtime change.

Only the deployment-approved provider/image pair is accepted. An image approved for Hetzner cannot be selected for AWS unless it was separately configured and passed the AWS description check. An explicitly selected image takes precedence over a later default change. Pinning the current default explicitly also records a module revision, so the selection remains stable on restart.

Scans and operations persist the exact runtime image and authorization revision before execution. Updates invalidate queued work through the module revision; old scheduled targets need a new save/review. Already-submitted operations continue read-only observation using the image stored on their operation, including after rollback. They never silently switch to the new default. Keep old images in the approved catalog and local image store until submitted work resolves.

Removing an old image does not remove its history. If the core no longer admits the pinned runtime, the operation becomes uncertain without invoking a replacement. Restore that approved image/catalog, wait for the worker lease to expire, then use **Check provider state** to resume reads. A missing daemon-side image similarly cannot fall back to another image; observation retries are bounded by the existing operation deadline.

Rollback changes future runtime selection. It does not undo cloud resources, replay commands, or migrate stored provider payloads. The current protocol supports declared inventory discovery and server creation/power/deletion operations. No plugin may run a core database migration.

## Upgrading from schema 13 or earlier

Older jobs do not contain a proven runtime binding. Drain submitted operations before upgrading whenever possible. The migration leaves their runtime identity empty rather than inventing one. With a real launcher catalog, old unbound queued operations fail closed and old unbound submitted work becomes uncertain. Use independent provider verification/manual resolution for those legacy outcomes; do not assign a guessed image ID. Old scans can be refreshed as new bound jobs. New jobs are pinned automatically.

## Trust and remaining work

This is a deployment-admin allowlist, not signed OCI package verification. The operator's image admission process is trusted, and the health probe does not prove that third-party code is harmless. Signed artifact/publisher verification, registry management from the UI, install/uninstall workflows, sandboxed custom UI assets, namespaced arbitrary providers, richer versioned schemas, and production egress isolation are not currently supported.

The launcher still owns the only Docker socket. Its shared job socket is mode 0660, owned by application UID 65532 and launcher group 0 so the health process can connect without adding privilege capabilities. Provider workers never mount that socket. The core receives only the catalog and the narrow job interface over its configured Unix socket. Worker requests cannot choose an executable, host mount, URL, or unapproved image. Secrets remain on stdin and are absent from probes and image metadata.

Verification covers provider/image admission, incompatible catalogs, immutable routing, stale update rejection, queued-work cancellation, old-runtime observation through rollback, and recovery after restoring an admitted image. PostgreSQL tests use fake provider calls; the browser runtime chooser uses a catalog fixture while other console flows use the real backend. The opt-in container smoke test uses invalid AWS configuration and makes no cloud API calls. These checks do not replace signed-publisher validation or live provider lifecycle tests.

## Runtime capabilities

Description metadata now includes capabilities_version=1, inventory_kinds, and actions. The core requests only declared inventory kinds and rejects undeclared response coverage. The launcher independently admits only declared requests. API actions, schedule preview/save/dispatch, and resource action buttons respect the selected runtime. Empty actions permit read-only runtimes.

Legacy descriptions without capabilities metadata conservatively support server discovery and start/shutdown/restart. Their scan request omits the newer inventory_kinds field entirely. Creation and deletion require explicit capabilities; rollback therefore cannot accidentally send a newer action to an older worker. Metadata is frozen when the launcher admits its catalog.

An optional `metrics: true` runtime capability now permits bounded server metric reads. Missing/false capabilities keep metrics unavailable; old discovery/power requests retain their wire shape. See [METRICS.md](METRICS.md).

## Publisher-signed admission

Set both `PROVIDER_TRUST_KEYS_FILE` and `PROVIDER_RELEASES_FILE` on the launcher to require publisher approvals for every configured provider/image pair, including the fallback image. Mount both files read-only with a deployment Compose override. A missing, partial, malformed, expired or invalid trust configuration prevents launcher startup. Without both settings, the existing deployment-approved local-image mode remains unchanged; it does not claim publisher verification.

Trust roots are a deployment-owned JSON object mapping key IDs to Ed25519 public keys in PKIX PEM form. Keep this root file separate from publisher-supplied releases. An approval binds version 1, the exact Docker image ID, supported provider IDs and an expiry. It is verified before any `--describe` execution. Mutable tag resolution cannot substitute a different image ID. Keys are limited to 16, release envelopes to 32, documents to 64 KiB and signed payloads to 4 KiB. Unknown publisher keys, fields, providers and protocol versions fail closed.

The offline publisher command uses standard Go Ed25519 and accepts a PKCS8 PEM private key:

```sh
umask 077
openssl genpkey -algorithm ED25519 -out publisher-key.pem
openssl pkey -in publisher-key.pem -pubout -out publisher-public.pem
# Use the immutable image ID printed by docker image inspect --format '{{.Id}}' IMAGE.
go run ./cmd/runtime-sign --private-key publisher-key.pem --key-id example-publisher \
  --image-id sha256:REPLACE_WITH_64_HEX_IMAGE_ID --providers aws,digitalocean,hetzner \
  --expires REPLACE_WITH_FUTURE_RFC3339_TIMESTAMP > releases.json
```

Do not mount the private key into the launcher, app or provider. Build `keys.json` from the public PEM, for example with Python's json encoder: `json.dump({"example-publisher": pathlib.Path("publisher-public.pem").read_text()}, output)`. Combine publisher outputs as one JSON array when admitting multiple images. Multiple trusted public keys allow deliberate publisher key rotation.

Envelope fields are `key_id`, `payload` and `signature`. Payload and signature use standard base64; the signed bytes are the UTF-8 prefix `providah-runtime-approval-v1\n` followed by the decoded JSON payload. Payload fields are `version`, `image_id`, `providers` and `expires_at`. Base64 preserves exact signed bytes through JSON formatting. Use the SDK signer instead of inventing a different canonicalization.

This is a Providah runtime admission attestation over a local Docker image ID, **not** a Cosign signature over an OCI manifest digest, a registry downloader, or a published general plugin contract. It assumes the deployment host/Docker daemon and trust-root mounts are controlled by the installation operator. Publisher trust does not prove code safety; existing isolation, permission and egress checks still apply. The current supported provider IDs remain AWS, DigitalOcean and Hetzner.

Expiry is checked at startup admission and before each startup probe. An admitted catalog is immutable until restart; expiry or root-file changes do not kill in-flight work. Removing a trust root/release requires restarting launcher and core to rebuild admission, with the existing missing-runtime/observation implications. Registry UI installation, trust administration audit, ongoing revocation policy, OCI interoperability and arbitrary extension IDs remain open PLAN work.

Verification: `TEST_PROVIDER_IMAGE=providah-provider:dev go test -race ./internal/launcher -run 'TestSignedProviderContainer|TestRuntimeTrustBeforeProbe' -count=1` exercises a real no-network probe using ephemeral test keys and proves unapproved images are rejected before execution.

## Visible admission provenance

Provider modules show the selected image's admission mode, verified publisher key ID and approval expiry. Runtime choices carry the same provenance. A provider's --describe output cannot assert these fields: the launcher discards any such claims and attaches only metadata from its verified approval. Deployment-approved images are labeled separately, and a missing image is unavailable. The expiry display describes the startup approval; it does not claim that an expired approval terminates admitted jobs. Tenant APIs expose no signing keys or trust-root modification controls.

### Installation admission history

Before serving requests, core compares its admitted runtime catalog with the last `runtime.catalog_admitted` installation event and atomically appends a system event when it differs. Image IDs, versions, SDK versions, capabilities and verified publisher/expiry metadata are retained. Ordering is preserved because the first runtime for each provider is its default. Identical concurrent starts deduplicate under a database transaction lock. An empty catalog also records removal; maintenance commands do not record admission. Failure to persist the audit stops server startup.

Events appear in the existing global installation audit with actor `system:runtime-admission` and inherit append-only protections. This records catalogs observed by core at startup, not trust-file edits or the identity of the deployment operator. Replicas configured with different catalogs can record alternating changes. Registry installation and UI trust administration remain unfinished.

Runtimes may advertise `private_network_create` for DigitalOcean/Hetzner server creation. Core requires it before queuing a nonempty reviewed private-network ID. The field is validated only with capability version 1 and the create action; AWS continues to use subnet/security-group selection.

Public SSH-key imports require explicit `ssh_key_create: true`, capability version 1, `access.ssh_key` inventory, and `create` action support. Legacy runtimes remain ineligible. Modules advertise this as `access.ssh_key.create`; the core rechecks support at request and dispatch. See [SSH_KEY_IMPORT.md](SSH_KEY_IMPORT.md).
