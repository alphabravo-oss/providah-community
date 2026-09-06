# Server deletion

Server deletion is an interactive operation for AWS EC2, DigitalOcean Droplets, and Hetzner Cloud servers. It is never a scheduled action. It uses the existing durable queue, maintenance windows, uncertain-result lock, and observation-only reconciliation.

## Review and authorization

1. A requester needs `resources.read`, `operations.request`, `operations.delete`, and recent MFA. Only administrators receive the new delete permission by default; custom roles can grant it explicitly.
2. **Read deletion impact** performs a read-only provider inspection with the connection's stored credentials and selected runtime. No deletion is submitted.
3. The modal shows the exact server, region, current state, and provider-specific impact. Type the native resource ID to acknowledge deletion and the listed data loss, and provide a reason.
4. On submission the core reads impact again. If its digest differs, no operation is created. The verified impact is stored with the operation and audit entry.
5. A different user needs `operations.approve`, `operations.delete`, `resources.read`, and recent MFA to approve. Approval expiry and revocation are checked before dispatch, including both parties' deletion authority.
6. The worker reads the server and dependencies again immediately before deletion. A changed impact or state fails without submitting. No protection setting is disabled to force deletion.

The API remains authoritative: a typed confirmation or hidden UI button is not authorization. An older provider runtime that does not implement preview/delete fails closed. The common TanStack form, modal, table, CSS, and SSE operation updates are reused.

## Provider calls and impact

| Provider | Submission | Reviewed side effects |
|---|---|---|
| AWS | EC2 `TerminateInstances`, one instance ID | Local instance-store data loss; EBS volume IDs and their DeleteOnTermination flags; network interface IDs and their deletion flags; connectivity loss and retained Elastic IP allocations |
| DigitalOcean | `Droplets.Delete`, one Droplet ID | Local disk and automatic-backup deletion; attached volume IDs and snapshot IDs retained; released server addresses. The separate destroy-with-associated-resources endpoint is never used. |
| Hetzner | `Server.DeleteWithResult`, one server ID | Local disk and automatic-backup deletion; attached volumes, snapshots, and floating IPs retained; each primary IP read separately to identify its auto-delete setting. Server deletion protection blocks inspection. |

Provider references: [AWS termination effects](https://docs.aws.amazon.com/us_en/AWSEC2/latest/UserGuide/how-ec2-instance-termination-works.html), [DigitalOcean destruction](https://docs.digitalocean.com/products/droplets/how-to/destroy/), [Hetzner backups and snapshots](https://docs.hetzner.com/cloud/servers/backups-snapshots/faq/), [Hetzner server/IP behavior](https://docs.hetzner.com/cloud/servers/faq/).

Connections need the corresponding read and deletion permissions, including primary-IP reads for Hetzner. There is no implicit backup creation and no universal undo. External application dependencies and autoscaling/controllers may exist beyond this inspection; the review explicitly calls that out. Known supported Terraform/OpenTofu state references block direct deletion as described below. Rich dependency graphs and comprehensive ownership detection remain unfinished.

## Outcomes and limits

A successful submission becomes **Observing**, not **Succeeded**. The worker reads the same server ID until AWS reports terminated/not found, or DigitalOcean/Hetzner return not found. A forbidden response is not deletion proof. Confirmed deletion retires the inventory server and schedules a connection refresh for related resources.

A lost submission response stays uncertain and is never automatically resubmitted. Existing lease fencing, fifteen-minute observation deadline, and manual verification controls apply. Manual resolution also requires deletion authority. Read-only reconciliation may establish that the requested state now exists, but does not prove which actor caused it.

The providers do not offer atomic comparison of all reviewed dependencies with deletion. Changes between the final read and the mutation remain a documented provider-side race. No cascade outside the explicitly displayed provider behavior is requested. Full external dependency discovery, broader protected-resource policy, comprehensive IaC ownership detection, and live-account lifecycle verification remain required release work.

Migration 15 adds the operation action, impact snapshot, and delete permission. Downgrade refuses while deletion history exists instead of deleting that history.

## Verification

Actual SDK transport fixtures exercise read-only preview, dependency changes, exact delete APIs, a lost response without retries, forbidden reads, and confirmed absence for all three providers. PostgreSQL tests cover exact confirmation, changed preview digest, separate approval, missing/revoked deletion authority, one submission followed by observation, and retired inventory. Browser tests exercise the typed review against a mocked provider preview/request; no real cloud deletion was performed.

## Standalone snapshot deletion

Snapshot deletion now shares the existing review/approval/dispatch/observation workflow for AWS EBS snapshots, DigitalOcean Droplet and volume snapshots, and Hetzner snapshot images. Automatic backup deletion and restores remain unimplemented.

The request pins `storage.snapshot` as an operation resource kind in schema 20 and sends it explicitly to a runtime advertising `snapshot_delete`. Server requests continue omitting the new worker field. Older runtimes cannot receive snapshot deletes; new runtimes still use the existing `delete` action permission and independent approval requirements. Runtime, credentials, maintenance, SSO and authority are rechecked before dispatch.

Provider preflight and execution:

- AWS checks the exact snapshot, its ownership, shared volume-creation permissions, and referencing owned AMIs (including disabled/deprecated images). AMI references block the operation; it does not deregister AMIs or cascade. Review includes snapshot identity/description, source volume, size, encryption and sharing impact. Permissions additionally require `DescribeSnapshots`, `DescribeImages`, `DescribeSnapshotAttribute` and `DeleteSnapshot`. External AWS Backup/retention/lock policies are not enumerated; provider rejection never becomes reported success.
- DigitalOcean reads the exact snapshot and validates source type/ID and regional availability. Review explicitly states that deletion removes the snapshot across every listed region, retaining the source. The SDK's snapshot delete endpoint is used for both Droplet and volume snapshots.
- Hetzner reads the exact image, requires type `snapshot`, rejects deletion protection and server-bound images, and uses the image delete endpoint. It never changes protection or deletes automatic backups as part of this action.

Reviewed material is read again immediately before the single SDK mutation. A changed review or unsupported state fails before submission. Mutation retries are disabled. Provider acceptance alone remains observing; only a subsequent not-found/deleted observation confirms success. Ambiguous submission stays uncertain and is never automatically reissued. As with server deletion, no provider API offers an atomic compare-and-delete: changes after the final read remain a documented race.

An active-work uniqueness constraint spans all regional entries for the same snapshot in one connection. Confirmed deletion retires those entries and any matching image-catalog aliases. Different connections to the same underlying cloud account are not globally deduplicated. This does not claim coordination with external cloud consoles or automation.

Upgrade core and worker together. Do not downgrade the database/core while snapshot operations exist without a reviewed migration plan; old server-only runtimes are not a substitute for the pinned snapshot-capable runtime needed to observe work already submitted.

Validation uses actual SDK request fixtures for preview, changed/protected/incomplete state, one submission, ambiguous errors without retries, and read-only not-found confirmation. PostgreSQL exercises both server and snapshot review flows, permission revocation, distinct approval, persisted kind, regional duplicate exclusion and retirement. Browser coverage uses the shared typed-confirmation/impact modal with a mocked provider response. No real cloud snapshot has been deleted in verification.

## Detached volume deletion

Reviewed deletion now supports detached AWS EBS, DigitalOcean and Hetzner Cloud volumes. Schema 21 admits `storage.volume` operations; runtimes must explicitly advertise `volume_delete`. Older runtimes do not receive these requests. Existing delete/request permissions, typed confirmation, independent approval, MFA, current SSO/maintenance rules and credential/runtime revision checks apply.

Preflight reads the exact volume and region. AWS requires an available volume with no attachments; DigitalOcean requires an empty Droplet attachment list; Hetzner requires no server attachment and deletion protection disabled. Hetzner inventory now marks attached volumes as attached so the UI does not advertise them as eligible. No automatic detach, protection change, snapshot, or cascading deletion occurs.

Review identifies the volume, reported size/location and provider metadata and states that all volume data is lost while existing standalone snapshots are retained. Changes to reviewed metadata block submission. AWS uses `DescribeVolumes`/`DeleteVolume`, DigitalOcean uses `Storage.GetVolume`/`DeleteVolume`, and Hetzner uses `Volume.GetByID`/`Delete`, with mutation retries disabled.

Acceptance is not completion. Read-only observation must report not found before inventory is retired. Authentication/read failures retain observation uncertainty; they are not interpreted as deletion. Active operations are unique per volume native ID within a connection. Existing external controllers may attach or change the volume after preflight; provider APIs do not support an atomic compare-and-delete, and this workflow does not coordinate external writers.

The same SDK fixture and PostgreSQL approval tests cover servers, snapshots and volumes. Volume-specific checks cover attached/protected states, changed impact, one submission, ambiguous failure without retry, and not-found confirmation. The browser uses the shared review/confirmation form. No real cloud volume was deleted during verification. Creation, attach/detach, resize, snapshot creation and restores remain part of the wider plan.

## Unused networks and firewalls

DigitalOcean and Hetzner runtimes now advertise `network_delete` for `network.network` and `network.firewall`. The existing resource detail modal, typed confirmation, immutable impact review, independent reviewer, delete permission, credential/module revision, identity policy, maintenance checks and uncertain-outcome handling apply. AWS security groups use the separate capability described below; AWS VPC deletion remains unavailable.

DigitalOcean VPC preflight requires the exact region and CIDR, a non-default VPC, no VPC members and no peerings. Even an empty first dependency page with a next-page marker blocks deletion. Firewall preflight requires `succeeded`, no Droplet assignments, no tag selectors and no pending changes. The impact includes the VPC identity/range or firewall identity and complete inbound/outbound rules.

Hetzner network preflight requires no servers or load balancers, deletion protection disabled, no vSwitch subnet connection and no route exposure to vSwitch. Review includes the network range and subnet/route configuration that deletion removes. Firewalls require no applied server or label-selector assignments; review includes their rules. These resources use the global inventory region.

Both providers use their existing official Go SDKs. The worker rereads dependencies and compares impact immediately before submission. A mutation is never retried automatically. Successful deletion is confirmed only by a subsequent read reporting absence. An existing resource remains under observation; a lost mutation response remains uncertain. Active operations fence regional aliases by resource kind/native identity, and confirmed deletion retires matching inventory aliases without affecting another kind with the same native ID.

Provider APIs do not expose atomic compare-and-delete across these dependency reads. An external attachment or rule change between the last read and deletion remains a provider-side race; coordinate external writers. No resource is automatically detached, no cascade request is issued, and no rollback or implicit backup is provided. Network/firewall creation, editing and broader AWS networking remain required work.

## SSH-key records

AWS, DigitalOcean and Hetzner advertise `ssh_key_delete` for `access.ssh_key`. Deletion uses the shared impact review, typed native-ID confirmation, independent approval and durable observation flow. The worker reads the exact immutable ID and compares the name, fingerprint and scope again before submission. Public/private key material is not included in the review. SDK mutation retries are disabled; only a subsequent successful absence observation retires inventory. Permission errors do not prove absence.

AWS uses EC2 DescribeKeyPairs filtered by key-pair-id, followed by DeleteKeyPair with KeyPairId. DigitalOcean uses Keys.GetByID/DeleteByID; Hetzner uses SSHKey.GetByID/Delete. Credentials need those read/delete operations. AWS identities and retirement are region-scoped; DigitalOcean and Hetzner keys are global, with aliases fenced across regions.

Deleting the provider record does **not** remove authorized keys from existing servers or delete a user's private key. Existing guest access must be revoked separately. Launch templates, schedules and external automation referencing the key may fail afterward; this workflow does not discover or rewrite every external reference. See [AWS key deletion](https://docs.aws.amazon.com/AWSEC2/latest/UserGuide/delete-key-pair.html), [DigitalOcean key deletion](https://docs.digitalocean.com/reference/doctl/reference/compute/ssh-key/delete/) and [Hetzner key changes](https://docs.hetzner.com/cloud/servers/how-to-rescue/change-ssh-key/). There is no provider-side atomic compare-and-delete; external changes after the last read remain possible.

## AWS security groups

AWS runtimes advertise `security_group_delete` for `network.firewall`; older runtimes and AWS VPC deletion stay unavailable. The existing detail modal, typed confirmation, immutable review, independent approval, current permission checks and durable observation apply. Schema 50 fences AWS network operations and inventory retirement by region while preserving global aliases for DigitalOcean/Hetzner.

Preview and submission read the exact group ID and reject default groups, attached network interfaces, inbound/outbound references from other groups, cross-VPC references and VPC associations. Self-referencing rules are allowed. Dependency queries must succeed and return a complete result; continuation tokens block rather than imply absence. Review includes group name/ID, owner, VPC, region and inbound/outbound rules. Changed review blocks the write. External launch templates and automation are not rewritten or exhaustively discovered.

Credentials require ec2:DescribeSecurityGroups, ec2:DescribeNetworkInterfaces, ec2:DescribeSecurityGroupReferences, ec2:DescribeSecurityGroupVpcAssociations and ec2:DeleteSecurityGroup. Writes use GroupId with SDK retries disabled. A missing/false success flag or error is uncertain, never retried; only a later InvalidGroup.NotFound confirms deletion. Access errors cannot retire inventory. No automatic detach/cascade/backup is performed. AWS has no atomic comparison against the reviewed rules; external changes after preflight remain possible.

References: [AWS deletion dependencies](https://docs.aws.amazon.com/AWSEC2/latest/APIReference/API_DeleteSecurityGroup.html), [cross-VPC references](https://docs.aws.amazon.com/AWSEC2/latest/APIReference/API_DescribeSecurityGroupReferences.html), [VPC associations](https://docs.aws.amazon.com/AWSEC2/latest/APIReference/API_DescribeSecurityGroupVpcAssociations.html).

## DigitalOcean and Hetzner load balancers

Load balancers use the same typed confirmation, separate delete permission and independent approval flow. Explicit load_balancer_delete runtime capability keeps older modules unavailable. The SDK preflight pins creation metadata, addresses, backend IDs/selectors, forwarding services and certificate references. DigitalOcean global routing adds child load balancers and domain references. Hetzner label-selector expansions are bounded and included; transient health values and certificate contents are excluded. Deletion protection blocks Hetzner deletion.

Deleting routing stops traffic through the load balancer. Backend servers, target load balancers and certificates are not explicitly deleted. Operators must account for DNS, clients and external controllers. Submission re-reads and compares impact, makes one delete attempt and requires a 204 acknowledgement; errors or missing acknowledgements remain uncertain. Only an explicit 404 confirms absence. Provider-side changes after the final read remain a race, as with other delete operations.

Schema 56 fences duplicate load balancer identities across regional inventory aliases and retires those aliases after confirmed deletion. AWS load balancer deletion and creation/editing remain unavailable. SDK HTTP fixtures cover regional/global DigitalOcean and Hetzner reads/deletes; no real cloud deletion was performed.

References: [DigitalOcean destruction](https://docs.digitalocean.com/products/networking/load-balancers/how-to/destroy/) and [Hetzner Cloud API](https://docs.hetzner.cloud/reference/cloud).


## Managed IaC state references

Schema 59 blocks deletion and resize when a stored Terraform/OpenTofu state references a supported server identity in the project’s configured connection (and AWS region). The API, deletion preview and pre-dispatch check enforce this; resource details show protection and referencing-project links. Historical references remain protective after a later state drops the resource. There is no ordinary admin bypass or release workflow. Permitted power changes carry an apply-drift warning.

This initial index covers aws_instance, digitalocean_droplet and hcloud_server only. It is conservative state evidence, not cloud-account attestation. Provider aliases, equivalent connections to one cloud account, external state and other resource types are not reliably covered. An absent badge does not establish that a resource is unmanaged. Already dispatched operations cannot be recalled. Full Terraform/OpenTofu deployment and comprehensive ownership protection remain unfinished.


### Expanded state mappings (schema 60)

The shared protection now recognizes these explicit Terraform resource types:

| Provider | Resource types |
|---|---|
| AWS | `aws_eks_cluster`, `aws_eks_node_group`, `aws_lb`, `aws_alb`, `aws_elb`, `aws_route_table`, `aws_internet_gateway`, `aws_nat_gateway`, `aws_ami`, `aws_ami_copy`, `aws_ami_from_instance`, `aws_db_instance`, `aws_rds_cluster_instance`, `aws_rds_cluster`, `aws_instance`, `aws_ebs_volume`, `aws_ebs_snapshot`, `aws_security_group`, `aws_key_pair`, `aws_db_snapshot`, `aws_db_cluster_snapshot` |
| DigitalOcean | `digitalocean_app`, `digitalocean_project`, `digitalocean_droplet`, `digitalocean_volume`, `digitalocean_volume_snapshot`, `digitalocean_droplet_snapshot`, `digitalocean_firewall`, `digitalocean_vpc`, `digitalocean_loadbalancer`, `digitalocean_ssh_key` |
| Hetzner | `hcloud_server`, `hcloud_network`, `hcloud_firewall`, `hcloud_volume`, `hcloud_load_balancer`, `hcloud_snapshot`, `hcloud_ssh_key` |

AWS key pairs use the `key_pair_id` attribute because Terraform's `id` is the key name; AWS database snapshots use their explicit snapshot identifier fields. These mappings were checked against the [AWS provider documentation](https://github.com/hashicorp/terraform-provider-aws/tree/main/website/docs/r), [DigitalOcean provider documentation](https://github.com/digitalocean/terraform-provider-digitalocean/tree/main/docs/resources), and [Hetzner provider documentation](https://github.com/hetznercloud/terraform-provider-hcloud/tree/main/docs/resources).

A new scanner version triggers historical rescanning before serving mutations. Missing/invalid individual identities are counted as uncovered while valid references in the same state are retained; duplicate JSON keys and malformed state structure still invalidate the scan. Aliases, inline child resources (such as inline EC2 block devices), association-only resources, external state and alternate Terraform resource implementations remain outside this explicit allowlist. Existing claims are never removed by rescanning.

### Inspecting project coverage

Open a managed project under Templates to inspect its latest scan and retained protections. “All identities recognized” describes parsing coverage only; it does not verify aliases or cloud account ownership. Incomplete historical scans remain visible even if the latest state scans completely. Users with both template-read and resource-read access can page through reference identities, open a matching inventory record and see whether another project also references the same target. Missing inventory records stay protected. This page provides no release/bypass action.

## AWS AMI deregistration

Available AWS `compute.image` resources use the shared deletion preview, typed confirmation, separate deletion permission, independent approval, ownership guard and queued authority recheck. Runtime catalogs must advertise `image_delete`; older runtimes do not gain the action automatically. The regional image operation index prevents duplicate pending deletions, and confirmed completion retires only that connection/region/image identity.

The AWS SDK reads the exact owned image, requires explicitly disabled deregistration protection, and includes its identity, creation date, backing snapshots and launch grants in the immutable review. Submission repeats the read and rejects changed impact. `DeregisterImage` explicitly sets `DeleteAssociatedSnapshots=false`, requires a positive acknowledgement and is never retried after an ambiguous response. Observation waits for deregistration or absence; denied reads do not indicate success.

Existing instances, EBS snapshots and instance-store S3 files remain. Future launches using the AMI fail. Launch-template/scaling-group and external dependencies are disclosed for review, not exhaustively discovered. Recycle Bin rules may retain an image; Providah does not promise recovery or automatically change protection. Available images only; no snapshot cascade, protection toggle or bulk cleanup. Known IaC claims block this action.

References: [AWS DeregisterImage](https://docs.aws.amazon.com/AWSEC2/latest/APIReference/API_DeregisterImage.html), [AMI protection](https://docs.aws.amazon.com/AWSEC2/latest/UserGuide/ami-deregistration-protection.html).

Scanner version 4 adds `digitalocean_app` and `digitalocean_project` using UUID `id`, plus `aws_ami_copy` and `aws_ami_from_instance` using the created AMI `id`. It does not claim the source AMI/instance as owned. Stored states indexed by earlier scanner versions are rescanned before mutations are served. These remain conservative references in the configured connection (and configured project region for AWS); provider aliases and cloud-account attestation remain unresolved; scanner version 6 adds recorded resource-region support below. DigitalOcean app/project editing remains unavailable, while their ownership references are visible through the shared resource/project UI.

## DigitalOcean empty projects

`organization.project` deletion uses the shared typed confirmation, deletion permission, independent approval, maintenance and IaC ownership checks. The godo SDK reads the exact project, rejects default projects (including an independent default-project read), and requires an empty resource listing with no remaining page or nonzero total. The reviewed project identity/name/environment/timestamps are compared again before submission. No contained resource is moved, reassigned or deleted. DigitalOcean also requires an empty project for deletion. See [DigitalOcean project deletion](https://docs.digitalocean.com/reference/doctl/reference/projects/delete/).

Only a 204 response acknowledges submission; ambiguous writes are not retried. A later exact-project 404 confirms removal, while access errors remain observation failures. Schema 66 fences concurrent project removals within a connection and retires matching project inventory after confirmation. New runtimes must explicitly advertise `project_delete`; older runtimes retain read-only project support. Project creation, editing and membership changes remain unfinished. No live project deletion has been exercised.

### AWS networking ownership (scanner version 5)

Stored `aws_route_table`, `aws_internet_gateway` and `aws_nat_gateway` resources now map their `id` to the matching inventory kinds. Historical states indexed by earlier scanners are rescanned before mutations are served, retaining existing claims. Resource and project views expose the same ownership links and shared guard. These inventory kinds currently have no native mutation actions. Standalone routes, associations, aliases and external state remain outside this mapping. Scanner version 6 adds recorded resource-region support below.

Identity fields follow the AWS Terraform provider documentation for [route tables](https://github.com/hashicorp/terraform-provider-aws/blob/main/website/docs/r/route_table.html.markdown), [internet gateways](https://github.com/hashicorp/terraform-provider-aws/blob/main/website/docs/r/internet_gateway.html.markdown), and [NAT gateways](https://github.com/hashicorp/terraform-provider-aws/blob/main/website/docs/r/nat_gateway.html.markdown).

### Recorded AWS resource regions (scanner version 6)

AWS ownership claims now use a supported resource's stored `attributes.region` when it is a valid nonempty AWS region string. Missing, null or empty values retain the configured project-region fallback for older states. Malformed explicit regions are counted as uncovered rather than guessed. Other providers retain their existing connection-wide identity matching. Same native IDs in different regions remain separate references.

Historical states indexed before version 6 are rescanned at startup. Existing claims are never removed, including conservative project-region claims written by older scanners: rescanning adds the correct recorded-region claim without silently unlocking anything. Provider aliases and equivalent connections/accounts are still not attested, and regions inferred only from ARNs, provider configuration or availability zones remain unresolved when no explicit region is stored. The [AWS instance provider documentation](https://github.com/hashicorp/terraform-provider-aws/blob/main/website/docs/r/instance.html.markdown) describes the per-resource region override.

### AWS load-balancer ownership (scanner version 7)

The state scanner recognizes `aws_lb` and its `aws_alb` alias using their ARN `id`, plus Classic `aws_elb` using its name `id`. Modern load-balancer claims derive region from the validated ARN even when older state lacks `attributes.region`; a conflicting explicit region is counted as uncovered. Listener/target-group ARNs are not accepted as load-balancer identities. Classic names use recorded region or the existing project fallback.

Version-seven backfill adds these claims from retained states without releasing previous protections. The allowlist now covers 36 Terraform resource types. No AWS load-balancer mutation is enabled. ARN account IDs remain state evidence rather than verified binding to the connection; account aliases and equivalent connections remain unresolved. References: [modern resource](https://github.com/hashicorp/terraform-provider-aws/blob/main/website/docs/r/lb.html.markdown), [Classic resource](https://github.com/hashicorp/terraform-provider-aws/blob/main/website/docs/r/elb.html.markdown).

### EKS ownership (scanner version 8)

Stored `aws_eks_cluster` names and `aws_eks_node_group` colon-separated cluster/group IDs now map to their inventory kinds. Recorded AWS regions and project fallback apply; retained historical states are rescanned without releasing previous claims. The allowlist covers 38 types. These kinds currently have no native mutation actions. Account/provider-alias attestation and ownership of related EC2 instances are not inferred. References: [cluster resource](https://github.com/hashicorp/terraform-provider-aws/blob/main/website/docs/r/eks_cluster.html.markdown), [node-group resource](https://github.com/hashicorp/terraform-provider-aws/blob/main/website/docs/r/eks_node_group.html.markdown).

### DigitalOcean Kubernetes ownership (scanner version 9)

Stored `digitalocean_kubernetes_cluster` and `digitalocean_kubernetes_node_pool` UUID identities now map to cluster and node-group inventory. Cluster state also protects the explicitly recorded default `node_pool[].id`; missing, malformed or empty default-pool metadata counts as uncovered while retaining the cluster claim. Individual Droplet identities, workloads and linked volumes are not inferred.

The retained-state backfill preserves previous claims. Shared resource/project notices and backend mutation guards apply within the configured connection; these Kubernetes kinds currently have no native mutation actions. The allowlist covers 40 resource types. Provider aliases, equivalent connections and external state remain limitations. Identity fields follow the [DigitalOcean Terraform cluster reference](https://raw.githubusercontent.com/digitalocean/terraform-provider-digitalocean/main/docs/resources/kubernetes_cluster.md), including its default-pool ID and separate-pool import example.

### Placement-group and DigitalOcean database ownership (scanner version 10)

The allowlist now includes 43 Terraform resource types. AWS placement groups use `placement_group_id`, not Terraform's `id` (which is the name); valid recorded AWS region or the project fallback supplies regional scope. Hetzner placement groups use their decimal `id`, and DigitalOcean database clusters use their UUID `id`. Only these resource identities are claimed, not member servers, database users or related services.

Historical states are rescanned and earlier claims remain protective. Existing project/resource ownership notices and shared edit/delete guards apply; these additions do not enable native mutations. Field definitions follow the official provider references for [AWS placement groups](https://raw.githubusercontent.com/hashicorp/terraform-provider-aws/main/website/docs/r/placement_group.html.markdown), [Hetzner placement groups](https://raw.githubusercontent.com/hetznercloud/terraform-provider-hcloud/main/docs/resources/placement_group.md), and [DigitalOcean database clusters](https://raw.githubusercontent.com/digitalocean/terraform-provider-digitalocean/main/docs/resources/database_cluster.md). Missing/invalid fields remain uncovered; account aliases and external state remain limitations.

### S3 ownership (scanner version 11)

S3 bucket references now map to storage.bucket: aws_s3_bucket uses id; aws_s3_bucket_policy, aws_s3_bucket_versioning and aws_s3_bucket_lifecycle_configuration use bucket. The explicit bucket field avoids composite import IDs containing an expected owner. Configuration-only references protect the target bucket without claiming ownership of its objects. Recorded AWS region and the project fallback apply.

The allowlist contains 47 types. Historical states are rescanned while retaining previous claims; shared ownership notices and mutation guards apply. Bucket native mutations remain unavailable. Other S3 resource types, aliases/equivalent connections, directory buckets and external state remain outside this coverage. References: [bucket](https://raw.githubusercontent.com/hashicorp/terraform-provider-aws/main/website/docs/r/s3_bucket.html.markdown), [policy](https://raw.githubusercontent.com/hashicorp/terraform-provider-aws/main/website/docs/r/s3_bucket_policy.html.markdown), [versioning](https://raw.githubusercontent.com/hashicorp/terraform-provider-aws/main/website/docs/r/s3_bucket_versioning.html.markdown), [lifecycle](https://raw.githubusercontent.com/hashicorp/terraform-provider-aws/main/website/docs/r/s3_bucket_lifecycle_configuration.html.markdown).

## Placement groups

AWS and Hetzner SDK adapters now inspect exact placement-group identity and reject nonempty groups before preview/submit. Hetzner uses the recorded server list and requires global scope. AWS reads by group ID, checks available state, then queries instance membership with that exact ID; nonempty or paginated membership results fail closed. The approved impact includes identifying configuration, and submit must match it. Reads denied or changed after review block mutation. Deletion is not retried; lost/unexpected acknowledgements remain uncertain, and completion requires read-only absence observation.

AWS's delete API takes the inspected group name rather than its immutable ID. Concurrent external replacement can race that call, and external launch-template/automation references are not exhaustively discovered. The review states these limits. No servers are detached/deleted, no backup is created, and no automatic undo is offered.

Compatible AWS/Hetzner runtimes explicitly advertise this action. Schema 70 persists reviewed placement-group operations and prevents overlapping work; shared independent approval, RBAC, maintenance and managed-IaC checks apply. The resource modal provides preview and exact-identity confirmation. Observed completion marks matching inventory deleted. Older runtimes do not gain the action implicitly. No live placement group has been modified.
