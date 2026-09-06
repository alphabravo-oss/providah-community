# Email and webhook delivery

Configure destinations in **Notifications**. Configuration requires notifications.manage and recent MFA. Delivery history requires the separate notifications.read permission.

Saving a destination does not contact it. **Send verification** queues a real message. Copy the received code into the console within 30 minutes. Email requires two independent codes: one delivered to the recipient and one delivered to the sender mailbox. Operational events only reach enabled, verified destinations.

Webhook signing keys and SMTP credentials are encrypted with the installation age identity and never returned by read APIs. To rotate, replace the destination configuration and verify again. Existing deliveries retain the old revision and are canceled rather than sent with changed credentials or a changed address. In-flight requests cannot be recalled. Coordinate any old-key acceptance window with your receiver.

## SMTP

Use custom SMTP per destination, or configure the installation default with a private SMTP_CONFIG environment variable:

    {"host":"smtp.example.com","port":587,"username":"notification-user","password":"REPLACE_ME","sender":"notifications@example.com"}

The existing Compose .env is passed to the app. Keep this configuration private. Port 587 requires STARTTLS; port 465 starts with TLS. Certificate and hostname verification remain enabled, and authentication never falls back to plaintext. Username and password must either both be present or both be absent.

Selecting installation SMTP copies its current settings into that destination's encrypted configuration. Changing the installation default affects future saves; rotate and reverify existing destinations explicitly. Organizations can also save a shared SMTP profile as described below.

## Webhook receiver contract

The implementation uses the official [Standard Webhooks Go library](https://github.com/standard-webhooks/standard-webhooks/tree/main/libraries/go). Supply a secret consisting of 32–64 random bytes encoded as base64, optionally prefixed with whsec_. Configure the same key at the receiver.

Requests carry:

| Header | Meaning |
| --- | --- |
| Webhook-Id | Stable event ID, retained across retries and manual redelivery |
| Webhook-Timestamp | This attempt's Unix timestamp |
| Webhook-Signature | Standard Webhooks signature of ID, timestamp, and exact body bytes |
| Providah-Attempt-Id | Unique ID of this delivery attempt |

Payload version 1:

    {
      "version": 1,
      "id": "opaque-event-id",
      "organization_id": "opaque-organization-id",
      "type": "operation.failed",
      "occurred_at": "2026-09-05T20:00:00Z",
      "console_path": "/"
    }

Verification messages additionally include verification_code. They do not include SMTP sender codes. Normal messages exclude names, native resource IDs, credentials, audit details, and raw provider errors. Email adds a link to the configured console origin; webhooks provide the relative console path. Full context requires signing into the console.

Verify the exact bytes before parsing JSON. The reference library checks the signature and a five-minute timestamp tolerance:

    wh, err := standardwebhooks.NewWebhook(secret)
    if err != nil {
        return err
    }
    if err := wh.Verify(rawBody, request.Header); err != nil {
        return err
    }

Import standardwebhooks from github.com/standard-webhooks/standard-webhooks/libraries/go. Bound body reads (64 KiB is ample for this payload), require version 1, and durably enqueue accepted events before returning 2xx. Deduplicate by Webhook-Id, not attempt ID. Delivery is at least once and unordered; receipt does not prove downstream work completed.

## Delivery behavior

- Audit changes and notification fan-out commit together. Rolled-back source changes produce no delivery.
- Actionable defaults cover failed/uncertain/blocked/expired operations, approval requests and rejection, skipped schedules, schedule changes needing approval, connection failures/credential rotation, maintenance policy changes, and role/member/invitation security changes. Successful operations are opt-in.
- Identical organization/type/target alerts coalesce within fixed 15-minute buckets. This avoids sending a new notice for every repeated polling failure. A bucket boundary can produce a second notice soon after the first.
- Each delivery gets eight automatic attempts with exponential retry intervals of 1, 2, 4, 8, 16, 32, and 64 minutes. Network failures, 408, 429, and 5xx retry. Other 3xx/4xx responses become terminal. Redirects are never followed.
- Attempts have 60-second leases and a 40-second overall send deadline. Expired leases are reclaimable; abandoned attempts are marked uncertain because the destination may already have received them.
- Authorized manual redelivery of a completed operational event preserves its event ID, payload, and attempt history and starts a new bounded retry cycle. It requires the same enabled, verified destination revision. Verification requires a fresh challenge instead.
- Delivery history stores response codes and fixed diagnostic messages. Remote response bodies and raw network/SMTP errors are not retained.
- Pending/sending/terminal transitions update the existing organization SSE stream.

## Current deployment limits

Public HTTPS destinations use port 443 without credentials, query strings, or fragments. SMTP uses public hosts on 465/587. Addresses are resolved at connection time and checked before dialing the exact IP; mixed public/private DNS answers are rejected. Proxies and redirects are disabled. Loopback, private, link-local, reserved/transition ranges, cloud metadata/control addresses, and the console's resolved addresses are blocked.

Installation-admin approvals for specific private hosts are **not implemented yet**; internal SMTP relays and webhooks remain blocked. Further pending work includes delivery-history retention, fleet-scale worker tuning, and event families belonging to unimplemented modules.

The current directory supports 100 destinations per organization. One delivery executes at a time per app process; database leases allow future workers to share the queue. SMTP and receiver ownership checks are tested with local protocol fixtures and a stub delivery adapter; no external email or webhook was sent during development validation.

## Event subscriptions

Open a destination and choose **Event subscriptions**. Defaults retain all supported actionable events and the destination's existing success preference. **Selected events** replaces that set with the exact checked events, including success notices only when explicitly selected. At least one event must be selected; use Disable destination to stop delivery. Future event types automatically join defaults, while custom selections remain explicit.

The server advertises the currently supported event catalog and rejects unknown types. Subscription edits require notifications.manage, the normal fresh-authentication policy, and the displayed destination revision. They preserve encrypted secrets and verification, advance the destination revision, and invalidate pending deliveries from the old revision. Already sent or in-flight messages cannot be recalled; earlier revision deliveries cannot be manually replayed. Verification and explicitly requested test messages bypass event selection. Duplicate grouping remains fixed at 15-minute buckets.

## Investigation links

Destination and delivery rows are links. Copy the current URL to reopen the exact record independently of the current directory/history page; refresh and browser history preserve selection. Breadcrumbs return to Notifications. Links require the current organization's normal management or delivery-history permission. Unavailable records show no cached details or send/redelivery actions.

## Remove a destination

Use **Remove destination** in its detail view and confirm the exact name. Removal requires notification management, the normal fresh-authentication policy and the displayed revision. It is permanent for that destination ID; create and verify a new destination to start again.

Removal erases its address, encrypted credentials and verification challenge material from the live database. It marks the destination removed and disabled, cancels pending deliveries and prevents future fan-out or redelivery. An already executing send may finish; removal cannot recall a message or copies held by receivers. Delivery/attempt and audit history retain the destination name. Removed IDs no longer open destination controls and do not count against the active directory limit. Backups and exports have their own retention; this is not a historical backup purge.

### Context links

New event payloads include organization-scoped `console_path` links. Operation/approval events open the exact operation; schedule events open the schedule; destination tests and verification open the destination. Connection, module and audit-export events open the corresponding management page. Other event families open the organization's overview. Only validated internal record IDs enter record selectors; resource names, credentials and audit details remain excluded.

Email renders the same relative path against the configured console origin, including destination and sender verification messages. A fixed route allowlist rejects off-site or malformed paths. Existing queued payloads retain their original links and are never rewritten; retries and manual redelivery preserve event identity and payload. Opening a link requires normal authentication and current RBAC. A link does not grant access.

Connection alerts now include `connection=<internal ID>` and open the exact metadata-only connection detail, even when absent from the bounded connection directory. Normal connection-read authorization applies.

### Organization alert grouping

Administrators with notifications.manage can configure **Alert grouping** on Notifications: every event, or fixed windows of 1, 5, 15 (default), or 60 minutes. Grouping keys are organization, event type and internal target; the first event is retained and further matches in that window are suppressed before destination fan-out. This is fixed-window suppression, not a summary/digest: adjacent windows may deliver alerts close together.

PostgreSQL computes windows from its clock. Every-event mode uses distinct event rows without a grouping bucket. Saving requires fresh authentication and the displayed policy revision, is audited, and starts a fresh grouping revision. Already queued events, retries, subscriptions, verification and direct test messages are unchanged. Audit history still records the underlying actions. The policy applies across all organization destinations, not per recipient.


## Automation alerts

Default subscriptions include `automation.validation_failed`, `automation.validation_canceled`, `automation.version_retired`, and `automation.version_revoked`. Successful checks (`automation.validation_succeeded`) require the destination's success preference or an explicit selected-event subscription. Existing custom selections do not expand automatically. These events reuse the transactional outbox, configured grouping, revision checks and bounded delivery retries. No historical events are replayed.

Validation alerts link to `/app/templates?org=…&validation=…`; retirement/revocation alerts link to the published version. Validation history also offers **View validation**. The shared result modal shows safe status/result metadata, links to its version/project, and authorized cancellation for queued/running jobs. Exact reads require templates.read and organization membership, independently of the recent-history list. Payloads contain no source, inputs, credentials or raw tool output. Successful validation remains advisory and does not authorize apply.


## Shared organization SMTP

Administrators with notifications.manage can open **Organization SMTP** in Notifications. Saving/replacing/removing a profile requires the normal fresh-authentication policy and the displayed profile revision. Settings use the existing SMTP host/port/TLS rules and installation age encryption; reads expose only configured status and revision. The offline encryption-key rotation inventory includes profiles. Saving a profile makes no network request and does not verify mailbox ownership.

Choose **Email with organization SMTP** when adding or replacing an email destination. The server checks the selected profile revision and copies its settings into the destination's encrypted configuration while holding the organization access lock. The browser holds the revision captured when its form opened. A changed or removed profile requires reloading the form. The destination still requires independent sender and recipient verification before operational delivery. Mixed profile/custom/platform requests are rejected when organization SMTP is selected. Audit metadata records only the source and revision, never credentials.

Profile replacement/removal affects future saves only. Existing destination configurations and queued deliveries retain their existing settings. To rotate a destination, replace its configuration using the new profile and verify again; normal revision fencing cancels work from the previous destination configuration. Removing the shared profile clears its live ciphertext but leaves a revision marker. Existing destination copies and backups follow their separate lifecycle. No automatic bulk rotation or fallback to installation SMTP occurs.
