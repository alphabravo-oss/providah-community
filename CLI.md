# Providah CLI

Build with `make cli`; the binary is `.bin/providahctl`. It uses the generated Go Connect client and the same API, organization checks and permissions as the UI. It has no direct database or provider connection, privileged endpoint, or persistent credential store.

## Interactive use

```sh
.bin/providahctl session --url https://console.example.com --email operator@example.com
.bin/providahctl resources --url https://console.example.com --email operator@example.com --org ORGANIZATION_ID --kind compute.server
.bin/providahctl resources --url https://console.example.com --email operator@example.com --org ORGANIZATION_ID --connection CONNECTION_ID --search web --sort name --json
```

The password is read without terminal echo using golang.org/x/term. An authenticator-code prompt appears only when the server returns an MFA challenge; accounts without enabled MFA do not need a code. They cannot be supplied as command-line flags. Each invocation logs in, performs one API command and attempts Logout with a separate five-second cleanup deadline, including after a failed/canceled read. Cookies remain in memory only. A failed cleanup prints a warning; abrupt process termination cannot guarantee server-side logout and remaining sessions expire under the normal server policy. This is not a new CLI token lifetime or a service identity.

The existing MFA replay and login rate limits apply. Obtain a fresh authenticator code for a subsequent invocation; do not reuse a code already consumed by the UI or another CLI command. Local-human login is the initial flow. Organization SSO requirements remain enforced and can deny reads from this local session. Browser OIDC/SAML handoff, service identities, token issuance/rotation and client credential persistence remain open; the CLI does not bypass those gates.

## Noninteractive use

`--auth-stdin` reads one bounded JSON object with required `email` and `password`, plus optional `code`. Supply a fresh six-digit code when the account requires MFA; a missing required code fails authentication without executing the command. Unknown fields, trailing objects and inputs larger than 4096 bytes are rejected. Example input shape:

```json
{"email":"operator@example.com","password":"YOUR_PASSWORD","code":"123456"}
```

Pipe a fresh object from your own trusted credential source, or redirect an existing protected input file. The CLI never creates that file or stores cloud credentials. Avoid putting real authentication JSON into shell history, shared files, logs or command arguments. Noninteractive local-human MFA does not make this suitable for unattended scheduled automation.

```sh
.bin/providahctl resources --url https://console.example.com --org ORGANIZATION_ID --json --auth-stdin < /secure/cli-login.json
```

## Commands and filters

| Command | Scope and optional filters |
|---|---|
| session | Current authorized organizations; no --org required |
| connections | --org required |
| resources | --org; optional --connection, --kind, --provider, --search, --sort, --descending, --page, --all; or --filters FILE with --page/--all |
| resource | --org and --id required |
| resource-summary | --org; optional --filters FILE, --group-by provider/kind/status (default provider) |
| views | --org; saved inventory views visible to the caller |
| dashboards | --org; dashboards visible to the caller |
| operation | --org and --id; optional --wait DURATION (up to 5m) |
| operations | --org; optional --filters FILE, --page, --all |
| audit | --org; optional --actor, --action, --target, --from, --before, --page, --all |
| schedules | --org required |
| schedule | --org and --id required |
| schedule-history | --org; optional --schedule, --outcome queued/skipped, --page, --all |
| modules | --org required |

Unsupported command/filter combinations fail before login. Organization/connection IDs are explicit; no hidden last-used context is persisted. Audit filters use the API's exact matching and RFC3339 inclusive-start/exclusive-end semantics. Resource pages request the server maximum of 200; other feeds retain their API limits. Pagination defaults to one page per invocation. Pass the opaque next-page token with the same scope/filters, or use --all to collect remaining pages within one login. Exact operation waiting is available with `operation --id ID --wait 5m`; general inventory/feed streaming remains open.

`--json` returns protobuf JSON, including its nextPageToken when present. Default tables show useful record fields and a next-page hint. Terminal output quotes untrusted names, embedded newlines and control characters; JSON preserves values through JSON escaping. Full nested metadata is available through JSON. Raw remote error bodies, passwords, TOTP values and session cookies are never printed.

## Transport and exit codes

Use the console's exact public origin without a subpath/query/fragment. HTTPS uses normal certificate verification. Plain HTTP is allowed only for literal loopback addresses or localhost, for example `http://localhost:8760`. Redirects are refused, including redirects of login requests. The transport does not load proxy environment variables or offer an insecure TLS option. Requests have a 30-second timeout and an 8 MiB response limit; there are no automatic login/read retries.

| Exit | Meaning |
|---|---|
| 0 | Command completed (or help displayed) |
| 1 | Transport/server/output failure |
| 2 | Invalid input, flags, filters or context |
| 3 | Authentication failure or expired session |
| 4 | Server denied access |
| 5 | Server policy/current-state precondition or conflict |
| 6 | Server rate/capacity limit |

Credential administration, account-recovery actions, SSO handoff, service tokens and unattended automation remain unimplemented. Operation commands are described below. Existing UI/API workflows remain available. Packaging/signing for supported operating systems and end-user terminal/platform testing remain release gates.


## Operation commands

These commands use the same versioned API and independent approval rules as the UI. Local CLI login supplies fresh TOTP verification when MFA is enabled. Server-side global/organization policy and step-up requirements still apply; password-only login grants no extra permissions and cannot permit self-approval. A successful request returns an operation ID and its durable status, not a claim that the cloud change completed.

| Command | Required inputs | Effect |
|---|---|---|
| preview-deletion | --org, --id RESOURCE_ID | Reads current provider impact/digest through the existing preview API |
| request-operation | --org, --input FILE | Requests server power/resize or supported resource deletion |
| request-server | --org, --input FILE | Requests single-server creation |
| request-ssh-key | --org, --input FILE | Requests an independently approved public-key import |
| review-operation | --org, --input FILE | Explicitly approves or rejects an immutable request |
| cancel-operation | --org, --id OPERATION_ID | Cancels eligible pending work under existing requester rules |
| reconcile-operation | --org, --id OPERATION_ID | Requests provider reads for eligible uncertain work; no mutation resubmission |
| resolve-operation | --org, --input FILE | Records an authorized independent manual resolution with evidence/reason |

Input files must be regular files of at most 64 KiB containing the command's protobuf JSON request. Unknown fields are rejected. `organizationId` may be omitted (the CLI uses --org), but a conflicting organization is rejected before login. `review-operation` requires an explicit JSON boolean `approve`, including false for rejection. Missing reasons, request keys and required target identifiers fail before login. Other validation and permissions remain authoritative on the server.

Example resize input shape (replace identifiers and the request key):

```json
{
  "resourceId": "RESOURCE_ID",
  "action": "resize",
  "expectedStatus": "off",
  "expectedSize": "cx23",
  "targetSize": "cx33",
  "reason": "Increase CPU and RAM while preserving disk size",
  "idempotencyKey": "YOUR_UNIQUE_REQUEST_KEY"
}
```

Use a fresh UUID (or the API's valid 16–80-character lowercase hex/dash key) for each new request. The CLI never invents or rotates that key. Preserve the file and key to recover from ambiguous outcomes. AWS stopped instances use expectedStatus `stopped`; provider state, type and runtime capabilities must match the actual target.

```sh
.bin/providahctl request-operation --url https://console.example.com --org ORGANIZATION_ID --email requester@example.com --input resize.json --json
```

A different authorized person first inspects the operation through `operations --json` or the UI, including creation configuration, source/target size or deletion impact, then submits an explicit review:

```json
{"id":"OPERATION_ID","approve":true,"reason":"Reviewed the exact target, configuration and impact"}
```

```sh
.bin/providahctl review-operation --url https://console.example.com --org ORGANIZATION_ID --email reviewer@example.com --input review.json --json
```

Deletion requests require the API's exact native-ID confirmation and current digest from `preview-deletion`. Human preview output includes both impact and digest; JSON is preferable for structured processing. The digest does not replace server preflight or independent approval. Creation uses RequestServerCreationRequest (`connectionId`, `region`, `creation`, `reason`, `idempotencyKey`) and preserves all existing catalog, scope and approval boundaries. See [PROVISIONING.md](PROVISIONING.md), [DELETION.md](DELETION.md) and [RESIZING.md](RESIZING.md).

Mutation commands send one request with no automatic retry. A timeout, connection failure or unavailable response may occur after the server recorded the request: exit 1 includes that warning. Inspect durable operations before retrying. For request-operation/request-server/request-ssh-key, any intentional retry must retain the identical input and original key. For review/cancel/reconcile/resolve, inspect current state before deciding whether another command is appropriate. Reconciliation and manual resolution enforce worker-lease rules and never erase uncertainty by silently resubmitting work.

Local SDK-client tests cover all operation command routes, scoped cookies, exact request-key preservation, strict input validation, explicit rejection, previews and ambiguous-response handling. The real PostgreSQL flow proves idempotent request creation, self-approval rejection, independent approval, cancellation and session cleanup. No cloud mutation was executed during these tests.


## Complete paginated reads

Use `--all` with resources, operations or audit to collect all remaining pages in one authenticated session:

```sh
.bin/providahctl resources --url https://console.example.com --org ORGANIZATION_ID --connection CONNECTION_ID --all --json --email operator@example.com
.bin/providahctl audit --url https://console.example.com --org ORGANIZATION_ID --action operation.requested --all --json --email operator@example.com
```

Each next request retains the same organization, connection and filters and uses the returned opaque cursor. Empty pages with a next cursor are followed. An optional --page starts at that cursor. Successful JSON output is one normal API response containing the combined records and no remaining cursor; table output similarly includes all collected rows.

Collection stops with exit 6 rather than publishing partial output if it exceeds 100,000 rows, 64 MiB of cumulative protobuf-encoded response data, 1,000 pages, or encounters a repeated cursor. Reads share a five-minute deadline after login; request timeouts still apply. Later-page errors (including revoked permissions) discard the collected output and retain their normal exit code. Session cleanup still runs. Buffer and rendered JSON memory can exceed the protobuf byte count; this is bounded collection, not streaming.

The server authorizes every page. Pagination reads live data, not a transactionally frozen inventory/export: concurrent discovery or edits can move rows between pages. The CLI does not claim snapshot consistency or silently deduplicate those observations. Narrow filters for large feeds. Mutations do not accept --all and are never looped or retried by this feature.

Tests cover a complete traversal, an empty intermediate page, one login/logout, repeated cursors, row limits and authorization failure without partial output. The real PostgreSQL check collects 401 scoped resources over three pages with unique IDs and no remaining cursor.

## Inspecting a requested operation

```sh
.bin/providahctl operation --url https://console.example.com --email operator@example.com --org ORGANIZATION_ID --id OPERATION_ID --json
.bin/providahctl operation --url https://console.example.com --email operator@example.com --org ORGANIZATION_ID --id OPERATION_ID --wait 5m --json
```

Without `--wait`, return the current exact operation. Waiting reads every two seconds using the same invocation session until success, failure, cancellation, rejection, expiry, resolution or uncertainty. Uncertainty stops the wait because it requires attention; it is not reported as cloud success. Approval-pending and queued operations may exceed the wait deadline. The server checks access on every read. A failed read ends the command without retries or stale output; timeout/cancellation also ends the wait, then attempts session logout. No operation is submitted, approved or reconciled by this command.

Exit 0 means the authorized lookup completed, including when the operation itself failed or is uncertain. Automation must inspect `operation.status` in JSON. Timeout is exit 1; other API failures use the documented exit codes. The maximum wait is five minutes and a later invocation can inspect the same ID again.

## Schedule inspection

```sh
.bin/providahctl schedule --url https://console.example.com --email operator@example.com --org ORGANIZATION_ID --id SCHEDULE_ID --json
.bin/providahctl schedule-history --url https://console.example.com --email operator@example.com --org ORGANIZATION_ID --schedule SCHEDULE_ID --outcome skipped --all --json
```

History uses the same filter-bound cursors and organization authorization as the UI, and remains available by schedule ID after removal. Exact lookup requires an existing schedule. `--outcome` selects dispatch outcome (`queued` or `skipped`); a queued action can subsequently succeed or fail. Inspect `operationStatus`, or use the returned `operationId` with the operation command. Without `--all`, history returns one page and its next-page token. All-page reads retain the existing row/byte/page limits and emit no partial history after a failed read. These commands do not create, approve or change schedules.

## Shared resource scopes

`resources`, `operations`, and `resource-summary` accept `--filters FILE` containing the same `InventoryViewSpec` JSON used by saved views and dashboard widgets. For example:

```json
{
  "provider": "aws",
  "kind": "compute.server",
  "region": "us-east-1",
  "tagConditions": [
    {"key": "environment", "value": "production"},
    {"key": "team", "value": "platform"}
  ],
  "tagMatchAny": false
}
```

```sh
.bin/providahctl resources --url https://console.example.com --org ORGANIZATION_ID --email operator@example.com --filters production.json --all --json
.bin/providahctl resource-summary --url https://console.example.com --org ORGANIZATION_ID --email operator@example.com --filters production.json --group-by status
.bin/providahctl operations --url https://console.example.com --org ORGANIZATION_ID --email operator@example.com --filters production.json --all --json
.bin/providahctl views --url https://console.example.com --org ORGANIZATION_ID --email operator@example.com --json
.bin/providahctl dashboards --url https://console.example.com --org ORGANIZATION_ID --email operator@example.com --json
```

Use `views --json` to obtain a view's `spec`, or `dashboards --json` for a widget's `filters`; save that object as the filter file. Filters also support connection, status, search, simple tags, and resource sorting. Server validation defines supported combinations; do not combine simple and compound tag filters. A filter file cannot be combined with individual resource filter/sort flags. Page tokens and `--all` remain available and preserve the same filters on every request. Scope narrows authorized results and never grants access.

Files must be regular files of at most 64 KiB. Unknown or duplicate JSON fields are rejected before login. Summary groups support provider, kind and status; counts reflect discovered inventory rather than a fresh provider scan. Operation filtering uses the existing API's resource association semantics. Views and dashboards are read-only through these commands; their full nested configuration is returned with `--json`.

## Schedule management

The CLI exposes the same schedule workflow as the UI. Each command requires `--org` and `--input FILE` containing its public API request JSON:

| Command | Required JSON fields | Behavior |
|---|---|---|
| preview-schedule | spec; optional resourceIds | Shows upcoming occurrences and applicable restrictions without saving |
| save-schedule | name, spec, resourceIds; id and expectedRevision for edits | Creates or edits a schedule under existing approval rules |
| approve-schedule | id, expectedRevision | Independently approves the reviewed revision |
| set-schedule-enabled | id, expectedRevision, enabled, identityEnabled | Explicitly sets both schedule and execution-identity switches |
| delete-schedule | id, expectedRevision, confirmation | Removes the schedule after exact-name confirmation |

Read `schedule --id ID --json` before changing an existing schedule. Copy its `revision` into `expectedRevision`; stale revisions fail, and the CLI does not automatically reload and retry. Both enable switches must be explicit JSON booleans, including false. Creation has no existing revision. Standard bounded-file, organization-mismatch, unknown-field and login checks apply. Schedule timing, target limits, permissions, independent approval and enabled-MFA policy remain authoritative on the server.

Example creation input:

```json
{
  "name": "Weeknight shutdown",
  "spec": {"timezone": "America/New_York", "action": "shutdown", "cron": "0 20 * * 1-5"},
  "resourceIds": ["RESOURCE_ID"]
}
```

Preview uses only `spec` and `resourceIds` from that object. It does not accept the creation-only `name` field. Run each command with its corresponding file:

```sh
.bin/providahctl preview-schedule --url https://console.example.com --org ORGANIZATION_ID --email operator@example.com --input preview.json
.bin/providahctl save-schedule --url https://console.example.com --org ORGANIZATION_ID --email operator@example.com --input schedule.json --json
```

Example pause input (replace the revision with the current one):

```json
{"id":"SCHEDULE_ID","expectedRevision":"3","enabled":false,"identityEnabled":true}
```

Pause stops future dispatches; removing a schedule does not recall previously dispatched operations. Saving or enabling a schedule is not proof of approval or execution: inspect the returned approval/state fields and schedule history. The CLI never retries writes. A lost response may hide a successful change; inspect schedules before retrying, especially creation, which could produce duplicates. No new scheduled actions are added by these commands.

## Saved views and dashboard management

Use `save-view`, `delete-view`, `save-dashboard`, and `delete-dashboard` with `--input FILE`. These commands use the same scoped APIs as the UI.

For a new view:

```json
{"name":"Production","spec":{"tagKey":"env","tagValue":"production"}}
```

For a new private dashboard:

```json
{"name":"Production","shared":false,"teamIds":[],"spec":{"widgets":[{"title":"Production servers","filters":{"kind":"compute.server","tagKey":"env","tagValue":"production"}}]}}
```

```sh
.bin/providahctl save-view --url https://console.example.com --org ORGANIZATION_ID --email operator@example.com --input view.json --json
.bin/providahctl save-dashboard --url https://console.example.com --org ORGANIZATION_ID --email operator@example.com --input dashboard.json --json
```

List `views --json` or `dashboards --json` first when editing. Save the complete replacement configuration with its `id` and current `revision` copied into `expectedRevision`. Dashboard saves require explicit `shared` and `teamIds`, even when false/empty; sharing to the organization or selected teams requires the existing server permission. Saved filters never grant resource access.

Deletion input is `{"id":"RECORD_ID","expectedRevision":"CURRENT_REVISION"}`. This deletes the saved view/dashboard, not its cloud resources. Unknown fields, missing specifications/revisions and conflicting organization IDs fail before login. The server validates filters, widgets, ownership and permissions. Writes are not retried; after a lost response, inspect the listing before another attempt, especially creation, which could create duplicates.

## Teams and access

`teams` lists team IDs, roles, active state, members and revisions. `access` returns the organization's roles, members, pending invitations and permission catalog in labeled sections; `--json` preserves the full typed API response. Existing API permissions determine access.

`save-team --input FILE` creates a team or replaces its configuration. Specify `name`, `roleId`, an explicit `active` boolean and the complete `userIds` array (including an empty array when intentionally clearing membership). An edit also requires `id` and the current revision as `expectedRevision`; obtain these with `teams --json`. List `access --json` to find role and member IDs. Server checks prevent granting permissions the caller cannot assign.

```json
{"name":"Operations","roleId":"ROLE_ID","active":true,"userIds":["USER_ID"]}
```

`delete-team --input FILE` requires `{"id":"TEAM_ID","expectedRevision":"CURRENT_REVISION"}`. This removes the team, not user accounts. Saves and removals use the existing server behavior and audit trail. The mutation response contains no new team ID; list teams afterward to retrieve it. A lost response may conceal success, so inspect teams before retrying; no automatic retry is performed. These commands send no invitations or external messages.

## Roles and organization members

`save-role --input FILE` creates a custom role, or replaces a custom role when `id` is supplied. Provide `name` and the complete nonempty `permissions` array. Use `access --json` to inspect the permission catalog and existing roles first. Built-in roles cannot be edited, and the server prevents granting or removing authority beyond the caller's permissions.

```json
{"name":"Inventory reader","permissions":["resources.read","connections.read"]}
```

`update-member --input FILE` changes an existing organization membership. Provide `userId`, `roleId`, and an explicit `active` boolean; false disables membership rather than deleting the account.

```json
{"userId":"USER_ID","roleId":"ROLE_ID","active":false,"expectedRevision":"CURRENT_REVISION"}
```

The backend retains administrator/recovery safeguards for member updates and audits both commands. Role edits require expectedRevision copied from access.roles[].revision; stale edits fail without overwriting permissions. Creation omits the revision. Member updates require expectedRevision copied from access.members[].revision; stale saves fail without changing membership. Read access immediately before editing and verify afterward; the CLI never retries writes. Role creation returns no ID, so list access afterward. Unknown fields, organization mismatches, missing permission lists and missing/null active flags fail before login. No invitation is created or sent by these commands.

### Server tag requests

The typed `request-operation` input supports `action: "tags"`. The selected provider runtime must explicitly advertise tag editing. Read the resource first and copy its complete observed tags into `expectedTags`; an absent observation is not an empty set. Supply the complete desired set in `targetTags`. AWS/Hetzner use `labels`; DigitalOcean uses `names`.

```json
{
  "resourceId": "RESOURCE_ID",
  "action": "tags",
  "expectedStatus": "off",
  "expectedTags": {"labels": {"environment": "dev"}},
  "targetTags": {"labels": {"environment": "test"}},
  "reason": "Change environment assignment",
  "idempotencyKey": "UNIQUE_REQUEST_KEY"
}
```

An explicit `"targetTags": {}` requests removal of all editable tags; AWS reserved tags must remain unchanged in the target. Missing/null sets are rejected before login, as are tag inputs on other actions. Inspect the complete operation with `--json` before independent approval. Backend provider validation, stale-state checks and managed-IaC protection apply. Writes can partially succeed or race external edits; do not automatically retry or assume rollback.

## Notification diagnostics

These commands read the same organization-scoped records as the notification UI:

| Command | Additional flags | Output |
|---|---|---|
| destinations | None | Configured destinations, verification, enabled state and subscribed events |
| destination | --id DESTINATION_ID | Exact destination metadata and revision |
| deliveries | --page CURSOR or --all | Delivery status, attempt count and failure detail |
| delivery | --id DELIVERY_ID | Exact delivery record |
| delivery-attempts | --id DELIVERY_ID | Individual attempts, response codes and timestamps |

All require `--url`, `--org` and the usual authentication. Use `--json` for complete public API fields. The destination table omits response-level SMTP availability metadata; JSON includes it. Secrets are not returned by these public read endpoints. Reads do not verify, enable, test or redeliver a destination.

```sh
.bin/providahctl deliveries --url https://console.example.com --org ORGANIZATION_ID --email operator@example.com --all
.bin/providahctl delivery-attempts --url https://console.example.com --org ORGANIZATION_ID --email operator@example.com --id DELIVERY_ID --json
```

`--all` uses the existing bounded pagination collector. Failed later pages or repeated cursors produce an error without printing a partial result. Separate commands are separate reads; they do not constitute a transactionally frozen snapshot. Destination setup and delivery commands are documented below.


### Notification subscription and grouping changes

Read `destination --id ID` or `notification-grouping` first to obtain the current revision. Configuration commands accept `--input FILE` with the public request JSON and the usual organization/authentication flags:

| Command | Required input |
|---|---|
| set-notification-subscriptions | id, expectedRevision, explicit eventTypes array |
| set-notification-grouping | expectedRevision, explicit seconds (0, 60, 300, 900 or 3600) |
| delete-destination | id, expectedRevision, confirmation equal to the exact destination name |

```json
{"id":"DESTINATION_ID","expectedRevision":"2","eventTypes":["operation.failed"]}
```

The event list replaces the full subscription set. An empty array explicitly clears it. Obtain supported event types from `destinations --json`; the backend validates them. Grouping zero means every event, while other values specify a window in seconds. Missing/null event lists or grouping values are rejected before login, as are absent revisions. Destination removal uses the existing soft-delete/cancellation workflow and does not recall messages already in flight.

These commands require the API's notification-management permission. Each sends once without automatic retry. After an ambiguous transport failure, inspect the destination/grouping record and reload its revision before deciding whether another request is needed. A stale revision fails with the existing conflict exit code. The CLI does not send test messages or redeliver notifications as a side effect of these commands.

API compatibility: direct callers of SaveNotificationDestination must now supply expectedRevision when replacing an existing destination. Creation requires zero/omission. The save-destination command uses this contract.

### Enable or disable a destination

`set-destination-enabled --input FILE` accepts an exact destination ID, its reviewed revision, and an explicit enabled boolean:

```json
{"id":"DESTINATION_ID","expectedRevision":"2","enabled":false}
```

Read `destination --id ID --json` first. Missing/null booleans and absent revisions fail before login. The API checks notification-management authority and rejects stale revisions; enabling does not bypass verification. This command does not itself send a test or verification message, though an enabled verified destination can receive subsequent operational events. On ambiguous failure, inspect the destination before retrying. Direct API callers must now supply expectedRevision for this operation too.


### Destination setup and delivery actions

All commands below use the public API and the existing authentication/organization flags. Mutations accept `--input FILE`; organization-smtp is a metadata-only read. Request files for save-destination, set-organization-smtp and verify-destination must be private regular files (for example, mode 0600). No secrets are accepted as command-line flags or echoed back. The CLI does not create or retain those files; manage them through your trusted credential workflow.

| Command | Input and behavior |
|---|---|
| save-destination | name, kind, endpoint and complete delivery credentials; id/expectedRevision for replacement |
| organization-smtp | Reads configured/revision metadata without secrets |
| set-organization-smtp | expectedRevision and explicit remove; provide complete smtp when remove is false |
| send-notification-test | id and explicit verification boolean; queues a verification message when true, otherwise a test |
| verify-destination | id, code, and senderCode when required for email verification |
| redeliver-notification | id of the existing delivery to requeue |

A webhook save requires signingSecret. An email save requires exactly one source: smtp, usePlatformSmtp, or useOrganizationSmtp with the reviewed organizationSmtpRevision. Backend validation checks endpoint, credential and transport requirements. Replacement resets verification and retains the existing cancellation rules. Saving SMTP/destination configuration does not itself send a verification message.

Example replacement using the already configured organization SMTP profile:

```json
{"id":"DESTINATION_ID","expectedRevision":"2","name":"Operations","kind":"email","endpoint":"ops@example.com","useOrganizationSmtp":true,"organizationSmtpRevision":"3"}
```

Example explicit verification request:

```json
{"id":"DESTINATION_ID","verification":true}
```

Test and redelivery commands can cause outbound messages. They dispatch once, without automatic retries; the server's durable delivery worker may subsequently retry transient failures under its configured policy. After a timeout, inspect deliveries/attempts before issuing another command because a duplicate request can create additional delivery work. Verification codes use the server's existing challenge lifetime and sender/recipient requirements. These commands retain public-destination checks and do not enable private-network delivery.

## Automation projects and ownership diagnostics

| Command | Flags beyond authentication/organization | Result |
|---|---|---|
| automation-projects | None | Project metadata and lock state |
| automation-project | --id PROJECT_ID | Exact project metadata and ownership coverage summary |
| project-ownership | --id PROJECT_ID, --page CURSOR, --all | Protected resource identities and first referencing state |
| project-states | --id PROJECT_ID, --page BEFORE_SERIAL, --all | Retained state metadata, newest first |
| validations | --status STATUS, --page CURSOR, --all | Validation history, optionally filtered by status |
| validation | --id VALIDATION_ID | Exact validation status and detail |

These commands read the existing public API under its organization permissions. State output contains lineage, serial, digest, size and timestamp metadata, never the raw Terraform/OpenTofu state. Project details include ownership coverage status and unsupported/incomplete counts. Ownership references include native/resource IDs, region, first state ID and conflicts. Missing ownership evidence does not establish that a resource is unmanaged. Use `--json` for full list-response metadata and nested project summaries.

`--all` uses the bounded collector and prints no partial output if a later page fails or repeats its cursor. State pagination passes the serial cursor returned by the API; use the displayed `--page` value to continue manually. Other commands use opaque cursors. Reads do not run automation, import state, release ownership claims or unlock projects.

```sh
.bin/providahctl automation-project --url https://console.example.com --org ORGANIZATION_ID --id PROJECT_ID --email operator@example.com
.bin/providahctl project-ownership --url https://console.example.com --org ORGANIZATION_ID --id PROJECT_ID --all --email operator@example.com
```

### Automation version selection and validation control

`automation-versions` lists published/version metadata; `automation-version --id VERSION_ID` inspects one exact version. Use `--json` for runtime inventory and the complete version binding, including connection revision and runtime policy. Unavailable or unpublished versions remain subject to the backend's checks.

| Command | Request file fields |
|---|---|
| create-automation-project | name, versionId, inputsJson containing an explicit JSON object |
| request-validation | versionId; optional projectId for project-bound inputs |
| cancel-validation | id of the validation job |

Example project input (the public API represents inputsJson as a JSON string):

```json
{"name":"Staging","versionId":"VERSION_ID","inputsJson":"{}"}
```

Project request files must have private permissions, like credential input files. Use an explicit empty object for versions with no inputs. Null, arrays and omitted inputs are rejected before login; the backend enforces the published input schema and immutable version/input binding.

These commands accept `--input FILE` and the usual endpoint, organization and authentication flags. They require the existing templates.publish authority. Validation uses the configured isolated runner and existing admission, identity, connection and runtime checks. Successful validation establishes syntax/input validity only; it does not perform Terraform/OpenTofu plan/apply or authorize execution. Cancellation follows existing job/worker rules and cannot cancel an unrelated provider operation.

Each command dispatches once. After an ambiguous failure, inspect projects or validation history before retrying. Retain the exact project name, version and inputs. The CLI does not generate a new project binding automatically or bypass version availability requirements.

### Import sources and publish versions

| Command | Arguments |
|---|---|
| automation-sources | Standard endpoint, organization and authentication flags |
| automation-source | --id SOURCE_ID |
| import-automation-source | --input METADATA.json --archive SOURCE.zip |
| publish-automation-version | --input VERSION.json |
| set-automation-version-status | --input STATUS.json |

Import metadata contains name, runtime (`terraform`, `opentofu` or `ansible`) and entrypoint. Do not include the archive in JSON:

```json
{"name":"Example infrastructure","runtime":"opentofu","entrypoint":"main.tf"}
```

The archive must be a private regular file, nonempty and at most 4 MiB. The CLI reads it once and uploads it through ImportAutomationSource. It does not extract or execute it locally. Existing backend checks enforce the ZIP format, safe paths, supported files, entrypoint, expanded-size/file-count limits and rejection of state/environment/private-key files. Source detail includes filenames and declared input metadata; it does not download the archive.

Publication input binds an imported source, configured runtime image, connection, region and review note. expectedVersion is the latest version number for the chosen name, or zero for the first publication:

```json
{"name":"Example infrastructure","expectedVersion":0,"sourceId":"SOURCE_ID","runtimeImage":"RUNTIME_IMAGE_DIGEST","connectionId":"CONNECTION_ID","region":"REGION","reviewNote":"Reviewed source and runtime binding"}
```

Obtain valid runtime choices from `automation-versions --json`. Publishing does not run or deploy the source. The backend enforces publication authority, reviewed version concurrency, runtime admission and connection scope.

Status changes accept an exact version ID and `retired` or `revoked`; republishing a status is not supported:

```json
{"id":"VERSION_ID","status":"retired"}
```

The API's retirement/revocation rules apply to existing projects and validations. Each command dispatches once. Inspect source/version records after an ambiguous response before retrying; imports/publications may already have created records. No automatic cloud apply follows import or publication.

## Native server templates

These templates describe a single server through the provider SDK workflow, separately from Terraform/OpenTofu/Ansible sources.

| Command | Flags/input |
|---|---|
| server-templates | Standard organization/authentication flags |
| server-template | --id TEMPLATE_ID |
| publish-server-template | --input FILE with name, connectionId, region, creation |
| set-server-template-status | --input FILE with id and status (`retired` or `revoked`) |

```json
{"name":"Web server","connectionId":"CONNECTION_ID","region":"fsn1","creation":{"image":"IMAGE_ID","size":"SERVER_TYPE","sshKey":"SSH_KEY_ID"}}
```

Provider-specific network/subnet/security-group settings belong in creation as supported by the existing API. The backend validates the configuration against its provider and connection; CLI inspection includes the full nested launch configuration. Publishing creates an immutable version and does not create a server. Unlike automation-version publication, this endpoint assigns the next version without an expectedVersion field. Inspect templates after an ambiguous response before repeating publication.

Retirement/revocation require the existing templates.publish permission and enforce the API's lifecycle restrictions. None of these commands automatically submits a server-creation operation. Subsequent creation continues through the independently reviewed operation workflow.


## Public SSH-key import

`request-ssh-key` accepts `RequestSSHKeyCreationRequest` JSON:

```json
{"connectionId":"CONNECTION_ID","region":"global","creation":{"name":"operator-key","publicKey":"YOUR_OPENSSH_PUBLIC_KEY"},"reason":"Import reviewed operator public key","idempotencyKey":"YOUR_ORIGINAL_UUID"}
```

Supply an EC2 region for AWS; use `global` for DigitalOcean/Hetzner. Replace the key placeholder with one Ed25519 or RSA (at least 2048-bit) public-key line. Missing configuration, private material, multiple lines and unsupported key types fail before login. The server rechecks input, scope, permissions and runtime capability. A request returns the durable operation. It queues directly under confirmation-only policy, or waits for approval when required by organization policy. Read `operation --id ID` to inspect its exact public material and provider/region before review. Deliberate retries must retain the original request key and exact input. See [SSH_KEY_IMPORT.md](SSH_KEY_IMPORT.md) for provider naming, read-only recovery and inventory completion.
