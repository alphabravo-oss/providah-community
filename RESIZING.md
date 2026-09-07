# Reviewed server resizing

Select a stopped server in Inventory, choose Resize server, select an alternative discovered type, and provide a reason. The common form shows the current type and disk-preservation behavior. The Operations review shows the immutable source → target type for approval when required by organization policy. Available types come from the connection's existing paginated compute catalog; availability in inventory is not a guarantee of compatibility, current capacity or price.

The versioned RequestOperation API accepts `action=resize`, `expected_size` and `target_size`. Both are bounded provider type names/slugs and must differ. Other actions reject these fields. Schema 23 persists them on the operation, binds them to its idempotency key and enforces the shape with a database constraint. Changing the input requires a new request and approval.

The existing operations.request/resources.read permissions authorize requests; a different operations.approve holder approves. Existing MFA, SSO policy, maintenance, credential/module/runtime revision, request expiry, target concurrency and next-request revocation rules still apply. Resizing is not permitted by legacy runtimes unless their capability description explicitly advertises it. It is not added to schedules or bulk power actions.

The source state and size must match fresh inventory at request time and again before dispatch. The SDK reads the actual server immediately before submission and refuses a mismatched source type or non-stopped state. The platform sends no shutdown, force power-off, snapshot or separate startup as part of resizing. Providers may change power state during their resize workflow; the approval notice makes that explicit. Review backups, workload compatibility and capacity before approving a real operation.

## Provider behavior

| Provider | SDK operation | Disk behavior | Required mutation permission |
|---|---|---|---|
| AWS | EC2 ModifyInstanceAttribute with InstanceType | Only the instance-type attribute changes; requires an EBS-backed stopped instance | ec2:ModifyInstanceAttribute plus existing ec2:DescribeInstances |
| DigitalOcean | godo DropletActions.Resize | disk=false | droplet:update plus read permissions for server/action inspection |
| Hetzner | hcloud Server.ChangeType | upgrade_disk=false | Project token with write authority plus server/action reads |

AWS configuration compatibility, DigitalOcean size/disk restrictions and Hetzner type/storage compatibility remain provider-enforced. The UI lists discovered choices rather than implementing a speculative local compatibility engine. Existing disks are not deliberately expanded or shrunk. Native provider rejection is reported conservatively if the submission outcome cannot be established; it is never followed by an automatic mutation retry.

References: [AWS instance-type changes](https://docs.aws.amazon.com/AWSEC2/latest/UserGuide/change-instance-type-of-ebs-backed-instance.html), [DigitalOcean resize](https://docs.digitalocean.com/products/droplets/how-to/resize/), [Hetzner ChangeType SDK](https://pkg.go.dev/github.com/hetznercloud/hcloud-go/v2/hcloud#ServerClient.ChangeType).

## Observation and recovery

Dispatch sends the mutation once with SDK mutation retries disabled. A lost response becomes uncertain. Observation reads the current size; DigitalOcean/Hetzner also inspect the recorded action when its ID is available. Success requires the exact requested size, and an available recorded action must be complete. A failed recorded action produces failure. Matching size without an action ID proves the resulting configuration, not which external actor caused it.

The core independently rejects a worker success that omits or mismatches the target size. After uncertainty, wait for the worker lease to expire before using Check provider state. That operation performs reads only. A successful resize requests inventory refresh; no optimistic size update is shown before discovery. Existing manual resolution remains available for outcomes that cannot be proven. The platform never rolls back the size automatically.

## Verification and limits

Local SDK tests check actual mutation fields, disk-preserving flags, stopped/source-type preflight, lost-response no-retry behavior, read-only reconciliation, observed target size and failed actions across all three providers. PostgreSQL integration checks approval when required by organization policy, immutable input/idempotency, stale source cancellation and rejection of unsupported success claims. Browser coverage uses the real shared catalog/form with a mocked final mutation; it changes no cloud resources.

Live provider lifecycle/compatibility tests, automated cost estimates, disk-expanding resize, automatic stop/resize/start orchestration and scheduled/bulk resize remain open. Rollback of schema 23 refuses to discard existing resize history; preserve history and restore a compatible core/runtime instead.
