# Automation source imports

In **Templates → Imported sources**, publishers can upload an immutable ZIP for Terraform, OpenTofu, or Ansible. Choose the runtime and an exact `.tf` / `.tf.json` or `.yml` / `.yaml` entrypoint within the ZIP. This records source only; it does not publish a runnable template, execute code, or adopt cloud resources.

With artifact storage configured, the original archive is age-encrypted with a per-object identity and stored as a pinned S3 version; PostgreSQL holds its encrypted identity and reference. Otherwise it remains encrypted in PostgreSQL. SHA-256 binds the exact original bytes. Key rotation includes both storage forms. Object-backed sources require the referenced object versions alongside database backups. Catalog APIs return metadata and file paths, never source contents or ciphertext. Keep secrets out of source and use protected credential references. Filename checks are not a secret scanner.

Limits: 4 MiB ZIP, 16 MiB total expanded content, 256 archive entries, and 50 immutable bundles per organization. Exact repeats with the same runtime and entrypoint return the original record, including its original name. New bytes produce a new immutable ID. No extraction or runtime process occurs during import. Validation rejects invalid checksums, unsafe relative paths, duplicates (including case variants), file/directory collisions, links, special files, state files, common environment/private-key files, `.git`, and `.terraform` contents.

`templates.read` permits catalog access; `templates.publish` plus the existing sensitive-action proof permits upload. The write rechecks current organization and identity policy under the existing access lock. Imports are audited without source contents. Only the import RPC has a larger request ceiling; ordinary RPC limits are unchanged.

Remaining required work: pinned Git imports, credentialed plan/apply and Ansible target execution, protected plans/logs, artifact retention/recovery, execution approvals, resource ownership, and scheduled execution. An imported source is not approved to execute.

## Reviewed publication

**Published automation** records immutable, numbered source/runtime/connection bindings. A publisher selects an imported source, a deployment-allowed image, a connection and region, and a source/dependency review note. The server pins the current connection revision. Publishing under the same name creates the next version; optimistic version checks prevent concurrent overwrites and exact retries preserve the original version. Retirement and revocation are one-way lifecycle changes. Source publication is not execution approval.

Deployment administrators configure `AUTOMATION_RUNTIMES` as a JSON array of objects with `runtime` (`terraform`, `opentofu`, or `ansible`), `image` (an immutable `sha256:` image ID followed by 64 lowercase hexadecimal characters), and `version` (the operator's runtime version label). The default catalog is empty. Maximum 32 entries; mutable tags, unknown fields, unsupported runtimes, and duplicate runtime/image pairs are rejected at startup. No images are pulled or executed by this configuration. Operators must verify images and bundled dependencies before admitting them; the catalog does not attest that an image exists locally or contains a particular binary. The launcher verifies local availability at startup and enforces its allowlist before validation.

Removing an image from deployment configuration marks existing versions unavailable without rewriting history. The catalog is bounded to 200 versions per organization. Existing `templates.read` and `templates.publish` permissions apply. Tests use explicitly fake runtime IDs and never execute their contents.

This release does not yet include constrained inputs, Ansible target/SSH bindings, dependency provenance enforcement, state management, or a runnable plan/apply path. Do not treat publication as proof that source is safe or runnable.

## Offline validation

Published version details now offer **Validate offline** when a runner is configured. The durable check runs the real CLI in an isolated container: Terraform/OpenTofu `init -backend=false -input=false -lockfile=readonly` followed by `validate`, or Ansible `--syntax-check` with a fixed localhost inventory. This is advisory syntax/configuration validation, not a cloud plan, check-mode run, or apply approval. It cannot verify remote resources or target connectivity.

The launcher admits only locally present, configured immutable image IDs. It shares its four-container limit with provider workers. Containers have no network, credentials, host mounts, Docker socket, or added capabilities; root is read-only, execution is non-root, memory is 512 MiB, CPU is one core, and process count is 128. `/work` and `/tmp` are bounded 128 MiB and 64 MiB temporary filesystems. Commands have a 120-second timeout, the launcher has a 130-second deadline, and cleanup forcibly removes the container. Subprocess output pipes have a bounded wait as well. Raw CLI output is discarded because diagnostics can expose source secrets; only predefined results reach the API/audit/UI.

Dependencies must be bundled in the allowed image or source. Terraform/OpenTofu use `/etc/providah/terraform.rc` for a reviewed offline provider mirror; lockfile checksums remain enforced. Ansible collections/roles must already exist. Missing dependencies fail the check rather than enabling network access. Existing source archive checks run again before extraction into a fresh temporary directory. The source hash must match the preserved archive.

Only current publishers can request validation. The coordinator rechecks requester identity policy, version publication status, runtime allowlist and bound connection revision before dispatch and before recording success. Changed authority cancels the result. Each version has at most one active check; queued work expires after 30 minutes and interrupted leases fail after three minutes without automatic re-execution. The UI shows the latest 201 checks. Audit revisions refresh the TanStack queries through existing SSE.

To build an example validator (no real cloud dependencies), choose `opentofu`, `terraform`, or `ansible`:

```sh
mkdir -p .bin/automation-image
CGO_ENABLED=0 GOOS=linux go build -o .bin/automation-image/automation-runner ./cmd/automation-runner
docker build -f Dockerfile.automation-examples --target opentofu -t providah-automation:dev .bin/automation-image
docker image inspect providah-automation:dev --format '{{.Id}}'
```

Configure the resulting immutable ID in `AUTOMATION_RUNTIMES` for both core and launcher and recreate them. The supplied Compose file forwards the same setting to both. Keep the live catalog empty until images and their dependencies have been reviewed. Example binaries are OpenTofu 1.11.6, Terraform 1.14.5, and Ansible Core 2.19.7; base images are pinned, but the Ansible example's transitive Python packages are resolved at build time. Capture/review the resulting image and dependency inventory before use. `make automation-image RUNTIME_IMAGE=...` wraps a custom reviewed runtime with the runner binary; its CLI must be under `/usr/local/bin`.

References: [OpenTofu container packaging](https://opentofu.org/docs/intro/install/docker/), [OpenTofu initialization](https://opentofu.org/docs/v1.8/cli/commands/init/), [Ansible verification options](https://docs.ansible.com/projects/ansible-core/devel/playbook_guide/playbooks_intro.html). Actual pinned CLI behavior is exercised by the container smoke tests.

Still required: protected diagnostics, dependency provenance enforcement, constrained inputs, credentialed plan/apply/check execution, independent execution approval, state authority, reviewed Ansible inventory/SSH access, and scheduling. Offline validation does not complete those workflows.

## Restricted dependency downloads

Runtime entries may now declare `dependency_hosts`: at most 16 exact, lowercase DNS hostnames. Wildcards, URLs, IP literals, duplicate hosts and non-HTTPS ports are rejected. Empty lists preserve fully offline validation. The published version stores the host list and a hash of the complete runtime policy; changes to image, version label or network authority require republication. Old publications with no policy hash remain valid only for offline runtimes.

When hosts are declared, the launcher starts a separate resource-limited proxy and a private per-job Docker volume containing only its Unix socket. The source container **still uses `--network=none`**. A loopback relay makes that socket available through HTTPS_PROXY. The source cannot reach a host bridge, metadata endpoint or private service by bypassing the proxy. It does not receive the Docker socket, a host directory, or any cloud credential.

The proxy accepts only CONNECT to configured hosts on port 443. It reuses the notification system's public-address resolver: every new connection rejects private, local, metadata, reserved and mixed public/private DNS answers, then dials the checked literal IP without a second lookup. TLS remains end-to-end and is verified by the CLI. Redirected download hosts must also be listed. Each proxy has 16 tunnel slots, two-minute tunnel deadlines and 64 MiB directional transfer limits; it exits after three minutes. The launcher normally removes the proxy and socket volume after the job. Launcher startup and one-minute sweeps remove explicitly labeled scratch containers and socket volumes after a ten-minute expiry. The expiry exceeds current execution deadlines; mounted volumes are preserved for a later sweep. Only known ephemeral names and worker kinds are eligible. Persistent data and older resources without expiry labels are never inferred to be disposable. Cleanup does not change durable operation outcomes or retry cloud actions.

The work tmpfs explicitly permits execution because Terraform providers are executable programs. Root remains read-only and all previously documented process/memory/CPU/time limits remain. Dependency artifacts must match the source lockfile; the proxy is a network boundary, not a replacement for provenance review. Runtime publishers see the exact host list in the publication form and immutable version details.

For OpenTofu's public registry and its GitHub-hosted provider assets, the tested host list was `registry.opentofu.org`, `github.com`, `objects.githubusercontent.com`, and `release-assets.githubusercontent.com`. Review hosts for your actual dependencies. The launcher also needs `AUTOMATION_PROXY_IMAGE`; supplied Compose defaults to the locally built `providah-egress:dev`, resolved to an immutable image ID at startup. Both normal and fast builds include this image. Validators built before proxy support must be rebuilt and republished with their new image ID.

## Managed projects and state

Templates now includes a managed-project catalog. A publisher binds a project to one published Terraform/OpenTofu version, with the existing connection/runtime review checks. Project creation is idempotent by organization/name/binding and creates no cloud resources. The catalog is bounded to 200 projects per organization. State history uses serial-based pagination and returns metadata only.

The internal state endpoint implements the [OpenTofu HTTP backend protocol](https://opentofu.org/docs/language/settings/backends/http/): GET, POST, LOCK and UNLOCK. DELETE is disabled. It requires a project-scoped, 15-minute capability; cookie authentication is ignored and browser-origin requests are rejected. Capability issuance exists only inside the backend for future approved execution dispatch, with no human issuance RPC or UI. Current user/organization authority and identity policy are checked again on each request. This does not enable cloud plan/apply yet.

State format 4, lineage UUID and nonnegative serial are validated before storage. Original bytes are age-encrypted; hashes, lineage and serial are checked when reading. Writes require the owning capability and lock ID. Wrong lineage, regressing serial and changed bytes at the same serial are rejected. Exact retries do not create another version. Losing or expiring a capability never releases its lock automatically. No forced-unlock, raw download, restore, delete or project-purge bypass is exposed. Human recovery still needs its separately approved workflow.

State is bounded to 32 MiB and four concurrent requests per core replica. Configured deployments store new states and imported source bundles as encrypted, version-pinned S3 objects; legacy database records remain readable. Key rotation rewraps encrypted object identities in PostgreSQL. Object-backed records require both database and object-version backups. See [ARTIFACT_STORAGE.md](ARTIFACT_STORAGE.md). Retention, recovery approval, remote backend adoption and the execution dispatcher remain unfinished. All history is currently retained; nothing is purged to satisfy a storage quota.

The opt-in integration check `TEST_STATE_CLIS=/absolute/path/to/tofu:/absolute/path/to/terraform make test-integration` runs trusted provider-free fixtures through the real HTTP handler and PostgreSQL backend. It exercises init, saved plan, apply, lock release, lineage preservation and serial advancement. It supplies no cloud credentials and does not run imported user code on the host.

## Source-pinned project inputs

A bundle may include `providah.inputs.json` at its archive root:

```json
{"version":1,"inputs":[
  {"name":"size","label":"Server size","type":"string","choices":["small","large"]},
  {"name":"count","label":"Node count","type":"integer","min":1,"max":5},
  {"name":"enabled","label":"Enabled","type":"boolean"}
]}
```

The file is part of the immutable source hash and publisher review. Limits: 16 KiB schema, 32 fields, identifier names up to 64 characters, labels up to 80 characters, 1–32 nonempty string choices of at most 256 bytes, and integer bounds within ±1 billion. Every field is required. Unknown, missing, duplicate, wrongly typed or out-of-range values are rejected. Reserved Ansible connection/magic variable names cannot be declared. Free-form strings, compound values, defaults and secret-reference inputs are not implemented yet; never put secrets in choices or labels.

Project creation renders the declared fields through the shared form and stores canonical values encrypted, with an immutable hash. Repeating equivalent values returns the same project; changing the binding under an existing name is rejected. Metadata returns the hash, not values. Key rotation includes encrypted inputs. Projects created before input support retain the empty-object binding.

Project validation is deduplicated per version/project and rechecks its binding, input hash and constraints before dispatch. The launcher and runner validate values against the actual pinned archive again. Ansible receives a private JSON extra-vars file. Terraform/OpenTofu syntax validation does not evaluate variable/provider-specific semantics; those require the future plan phase. A source-only syntax check does not require project values and cannot approve execution.

Rebuild validator images before requesting project checks: the new runner protocol carries constrained inputs. Existing source-only checks omit that field and remain compatible. Cloud planning, input secret references, Ansible target execution and approved apply remain unfinished.

### Cancel validation

Publishers can cancel queued or running checks from Validation history. Cancellation is organization-scoped and audited; terminal checks cannot be rewritten. Queued checks are excluded from dispatch. Running coordinators poll durable status once per second and cancel the isolated launcher request when cancellation or lease loss is observed. Database-check failure also stops the request. The launcher uses request cancellation to terminate its process and force-remove its named worker container; cleanup may take several seconds. A late runner response cannot overwrite canceled history. Existing launcher orphan recovery remains the fallback after process/host interruption.

This controls credential-free source validation only. It does not add Terraform/OpenTofu plan/apply or Ansible execution, and cannot undo external activity.

## One-command local validators

After the normal dev stack is running, `make dev-automation` builds the three pinned examples, verifies their CLI versions, runs actual valid/invalid-source container checks, and admits their immutable image IDs to both core and launcher. It preserves unrelated runtime entries and private environment settings. Images have no dependency hosts or cloud credentials; these examples validate dependency-free source. Real provider projects need a reviewed provider mirror or the existing restricted dependency-download configuration.

The local image/version report and Ansible package inventory are saved in `.local/automation-image-inventory.json`. Ansible's transitive packages are still resolved at build time; this development setup is not a production dependency-provenance attestation. Existing published versions remain immutable: rebuilding with a different image requires publication of a new version.

`make dev-automation-stop` removes only entries tracked by this dev setup and recreates the app/launcher. It does not delete images, sources, projects or state. Complete or cancel active validations before restarting the launcher.

For an isolated end-to-end check using those actual images:

```sh
TEST_AUTOMATION_CATALOG="$PWD/.local/automation-runtimes.json" \
TEST_DATABASE_URL='postgres://providah:providah@127.0.0.1:55432/providah?sslmode=disable' \
go test -race -tags integration ./internal/core -count=1
```

This imports, publishes, queues and records successful/failed validation for all configured engines in an isolated test database. It neither deploys cloud resources nor runs Ansible tasks. Do not rebuild runtime tags concurrently with this check.


## Validation history

Templates → Validation history now supports all/queued/running/succeeded/failed/canceled filters and pages of up to 100 results. **Older validations** advances by creation time plus unique record ID; **Most recent validations** returns to the latest page. The URL preserves the selected filter and page across reloads. Changing status clears the previous cursor. Each row retains its exact validation detail link.

The API's page token is bound to organization and status. Tied creation timestamps are ordered by ID so stable records are not duplicated or skipped. This is live history, not an immutable snapshot: changing job statuses can move records into or out of a filtered view, and new records appear on the latest page. Authorization is checked on every request. Source output, credentials and input values remain excluded.
