# Provider coverage and release evidence

Baseline reviewed: 2026-09-06, local schema 66. This records implemented native workflows; it is not certification of the providers' entire public APIs. A capability is not proof of successful live cloud execution.

## Status and evidence rules

- **Implemented / offline-tested**: SDK fixture and application paths exist. Real cloud lifecycle verification remains required.
- **Read-only**: discovery exists; lifecycle mutations remain planned.
- **Planned**: no usable native workflow has been established here.
- No cloud workflow in this matrix is marked live-verified. Local PostgreSQL, browser and deployment checks do not qualify as provider lifecycle tests.

Runtime selection is authoritative: older runtime images may expose fewer actions. `ListProviderModules` lists the selected runtime's capabilities; disabled modules/connections and RBAC can further restrict them. `sdk/provider/discovery.go`, `sdk/provider/runtime.go`, `internal/providers`, and `internal/core/modules.go` are the corresponding implementation sources.

## Pinned SDK baseline

| Provider/service | Installed Go module | Version | Official reference |
|---|---|---|---|
| AWS EC2 | aws-sdk-go-v2/service/ec2 | v1.329.0 | [EC2 API](https://docs.aws.amazon.com/AWSEC2/latest/APIReference/Welcome.html) |
| AWS ELB v2 | aws-sdk-go-v2/service/elasticloadbalancingv2 | v1.62.0 | [ELB v2 API](https://docs.aws.amazon.com/elasticloadbalancing/latest/APIReference/Welcome.html) |
| AWS Classic ELB | aws-sdk-go-v2/service/elasticloadbalancing | v1.40.0 | [Classic ELB API](https://docs.aws.amazon.com/elasticloadbalancing/2012-06-01/APIReference/Welcome.html) |
| AWS EKS | aws-sdk-go-v2/service/eks | v1.98.0 | [EKS API](https://docs.aws.amazon.com/eks/latest/APIReference/Welcome.html) |
| AWS RDS | aws-sdk-go-v2/service/rds | v1.128.0 | [RDS API](https://docs.aws.amazon.com/AmazonRDS/latest/APIReference/Welcome.html) |
| AWS Route 53 | aws-sdk-go-v2/service/route53 | v1.69.0 | [Route 53 API](https://docs.aws.amazon.com/Route53/latest/APIReference/Welcome.html) |
| AWS CloudWatch | aws-sdk-go-v2/service/cloudwatch | v1.71.0 | [CloudWatch API](https://docs.aws.amazon.com/AmazonCloudWatch/latest/APIReference/Welcome.html) |
| AWS STS | aws-sdk-go-v2/service/sts | v1.49.0 | [STS API](https://docs.aws.amazon.com/STS/latest/APIReference/welcome.html) |
| S3 client | aws-sdk-go-v2/service/s3 | v1.111.0 | [S3 API](https://docs.aws.amazon.com/AmazonS3/latest/API/Welcome.html) |
| DigitalOcean | digitalocean/godo | v1.206.0 | [Provider API](https://docs.digitalocean.com/reference/api/digitalocean/) |
| Hetzner Cloud | hetznercloud/hcloud-go/v2 | v2.47.0 | [Cloud API](https://docs.hetzner.cloud/) |

Versions come from go.mod. S3 supports internal artifact/state storage and AWS bucket inventory; it does not yet establish a cloud object browser. No GCP or Azure provider runtime is implemented.

## Shared module integration

All existing AWS, DigitalOcean and Hetzner discovery now calls `inventory.Collect` from the public Apache-2.0 module [`ab-provider-modules`](https://github.com/alphabravo-oss/ab-provider-modules), pinned at `v0.2.0`. No sibling checkout or private credentials are needed to build.

The shared module owns cloud inventory API calls, paging, region filtering and display metadata extraction. This includes existing RDS/EKS/load-balancer/S3/Route 53 collection and DO/Hetzner infrastructure. Providah's thin `internal/providers/discover.go` adapter retains request validation, AWS broker-output restrictions, wire mapping, error redaction and response validation. Worker isolation and inventory retirement remain unchanged.

A failed page or kind returns no inventory. Hetzner server collection retains its full-inventory safety bound before region filtering. The worker resource bounds are operational safety limits, not edition entitlements.

Shared-module tests and Providah provider/SDK/launcher checks pass offline. This extraction preserves existing resource coverage; it does not establish live cloud verification. Azure/GCP/OCI/Linode remain unintegrated in Providah. Sierra remains unchanged.

Update with `go get github.com/alphabravo-oss/ab-provider-modules@<version>`, then `go mod tidy` and `go test ./internal/providers ./sdk/provider ./internal/launcher`. Shared-repository Dependabot updates upstream SDK families weekly; product dependency pins move explicitly after checks.

## Native resource/action matrix

Every row has discovery through `ListResources` / `GetResource` and the shared `/app/resources` table and detail modal unless marked Planned. “Delete” means the reviewed operation path, not an immediate provider mutation. Conditions are further detailed in the linked workflow documents below.

| Resource kind | AWS | DigitalOcean | Hetzner Cloud |
|---|---|---|---|
| compute.server | Create, start/shutdown/restart, resize, snapshot, tags, delete | Same action set | Same action set |
| compute.image | Read; available owned AMI deregistration, snapshots retained | Read | Read |
| compute.type | Read catalog | Read catalog | Read catalog |
| storage.bucket | Read general-purpose buckets and tags | Planned Spaces workflow | No native bucket workflow |
| storage.volume | Read; detached volume delete | Read; detached volume delete | Read; detached volume delete |
| storage.snapshot | Read; standalone snapshot delete | Read; snapshot delete | Read; snapshot delete |
| storage.backup | Not a separate native kind | Read | Read |
| network.network | Read VPCs | Read; reviewed delete | Read; reviewed delete |
| network.subnet | Read | Not a separate native kind | Not a separate native kind |
| network.route_table | Read summary | Planned comparable workflows | Planned comparable workflows |
| network.internet_gateway | Read | Not this kind | Not this kind |
| network.nat_gateway | Read | Planned comparable workflows | Not this kind |
| network.firewall | Read; reviewed security-group delete | Read; reviewed delete | Read; reviewed delete |
| network.load_balancer | Read | Read; reviewed delete | Read; reviewed delete |
| access.ssh_key | Read; approved public-key import; reviewed key-pair delete | Read; approved public-key import; reviewed delete | Read; approved public-key import; reviewed delete |
| network.ip | Read Elastic IPs | Read reserved IPv4 | Separate primary/floating kinds |
| network.reserved_ipv6 | Not this kind | Read | Not this kind |
| network.primary_ip | Not this kind | Not this kind | Read |
| network.floating_ip | Not this kind | Not this kind | Read |
| compute.placement_group | Read; empty group reviewed delete | Not this kind | Read; empty group reviewed delete |
| database.instance | Read | Cluster kind | No native managed-database workflow |
| database.cluster | Read | Read | No native managed-database workflow |
| database.snapshot | Read | Not this kind | Not this kind |
| database.cluster_snapshot | Read | Not this kind | Not this kind |
| kubernetes.cluster | Read EKS | Read | No managed Kubernetes workflow |
| kubernetes.node_group | Read EKS managed node groups | Read node pools | No managed Kubernetes workflow |
| network.certificate | Planned | Read | Read |
| dns.zone | Read hosted zones | Read | Read |
| dns.record | Read record sets | Read records | Read record sets |
| organization.project | Planned account/org workflows | Read; empty non-default project delete | Not this kind |
| application.app | ECS workflows planned separately | Read deployment phase/component counts | Not this kind |

“Not this kind” is a contract distinction, not a claim that the provider has no comparable API. DNS values, application specs, provider account administration and arbitrary infrastructure editing are not implied by a resource being listed.

## Workflow contract and evidence

| Workflow / API action | UI and console permissions | Native behavior / recovery boundary | Offline evidence |
|---|---|---|---|
| Discovery: RefreshConnection, ListResources, GetResource | Connections refresh; resource table/detail; connection-management and resources.read gates | Bounded SDK pagination; a failed page does not publish a partial replacement; region/provider scope preserved | discover_test.go, infrastructure_test.go, rds_test.go, route53_test.go, do_apps_test.go, aws_load_balancers_test.go, eks_test.go, do_node_pools_test.go, aws_placement_test.go, s3_test.go; discovery_integration_test.go |
| Server creation: RequestServerCreation | Create server modal; operations.request/create and resources.read; connection picker also uses connections.read | Existing image, size, SSH key and network inputs; provider submission/completion rules in PROVISIONING.md | create_test.go; creation integration and browser flows |
| Public-key import: RequestSSHKeyCreation | Import SSH key modal and request-ssh-key CLI; operations.request/create; independent operations.approve/create review | Single public key; exact persisted review; read-only confirmation and lost-response reconciliation; see SSH_KEY_IMPORT.md | key_create_test.go; key_creation_integration_test.go; CLI and three-provider browser flows |
| Power: RequestOperation(start/shutdown/restart) | Resource action modal; operations.request plus resource access; independent operations.approve review | Exact resource state and authority rechecked; asynchronous observation; no retry of ambiguous writes | power_test.go; operations_integration_test.go |
| Resize: RequestOperation(resize) | Shared resize modal; operations.request; independent review | Stopped servers; provider compatibility checks; DO/HZ avoid automatic disk growth; no automatic rollback | resize_test.go; RESIZING.md |
| Server image: RequestOperation(snapshot) | Shared action modal; operations.create at request/review/dispatch | Stopped source; creates disk image; storage charges apply; no power-state change; creation identity observed | server_snapshot_test.go, server_snapshot_integration_test.go |
| Deletion: PreviewDeletion, RequestOperation(delete) | Impact review, typed target confirmation; operations.delete; independent review | Final impact comparison; dependency/default/attached-resource checks by kind; observe absence; uncertainty preserved | *_delete_test.go, deletion_test.go, deletion_integration_test.go; shared browser modals |
| Metrics: GetResourceMetrics | Resource metrics panel; resource read authority | Read-only provider metrics; provider retention, delay and gaps apply | metrics_test.go; METRICS.md |
| Schedule power | /app/schedules; schedules.manage/read and independent approval | Explicit targets, standing revision approval, maintenance and current authority; no scheduled deletion/resize/snapshot | schedules_integration_test.go, schedule_time_test.go, schedule_slots_integration_test.go |
| IaC ownership guard | Resource notice and referencing-project links | 47 allowlisted state resource types; durable claims; block direct edits/delete; power and image-copy exceptions | resources_test.go, managed_state_integration_test.go; DELETION.md |

Test filenames refer to internal/providers, internal/core or internal/tfstate as appropriate. Exact console gates remain enforced by core access checks; this table does not grant authority. The `PrivateNetworkCreate` runtime flag means server creation with an existing private network, not creation of a network itself.

All operation paths store reviewed inputs, requester, scoped target, approval, runtime/credential revisions and durable outcome. API/UI/CLI requests use the same core checks. Pending cancellation is available under existing status/authority rules; once submitted, cancellation does not imply reversal at the provider. Uncertain work requires observation/reconciliation, not blind resubmission. See DELETION.md, PROVISIONING.md, RESIZING.md and SNAPSHOTS.md for action-specific details.

## Cloud permissions, limits and secret boundaries

The required cloud permissions are the corresponding SDK read/write actions in each adapter. Exact tested least-privilege policies for every row are **not yet available** and remain a release gate. Known AWS creation and deletion dependencies are documented in PROVISIONING.md / DELETION.md; encrypted images can additionally require KMS authority. DO scoped-token permissions and HZ read/write tokens must be verified against each workflow before release. Console permission does not imply cloud permission.

No fixture proves current account quota, region availability, subscription tier or capacity. Validation is not a capacity reservation. No universal provider dry-run is promised. Native retry/idempotency differences are described in the workflow documents; cancellation and recovery must be verified per operation, not inferred from HTTP success.

Cloud credentials are passed privately to isolated adapters and are not ordinary API responses. Inventory is an allowlisted projection. Application environment/specification values, private keys, database credentials and raw provider errors are excluded. Audit records capture bounded operation evidence rather than full provider responses. State/artifact access has separate authority and encryption; see ARTIFACT_STORAGE.md and AUTOMATION_SOURCES.md.

## Required breadth still missing

| Provider | Planned workflows not established by the matrix above |
|---|---|
| AWS | EBS create/attach/detach/modify/restore; VPC/subnet/routes/gateways lifecycle; broad security-group/rule edits; IP allocation/association; key generation/rotation; S3 bucket/object workflows; Route 53 edits/change tracking; RDS create/configure/restore/replicas/deletion; ECS and EKS lifecycle; broader account/partition discovery |
| DigitalOcean | Network/firewall/load-balancer creation and edits; volume lifecycle beyond delete; image restore/transfer; IP lifecycle; DNS edits; key generation/rotation; project creation/edit/resource membership; App Platform lifecycle; managed DB and Kubernetes lifecycle; registry and Spaces workflows; other public API groups require a frozen endpoint inventory |
| Hetzner Cloud | Network/subnet/firewall/routing creation and edits; volume attach/detach/resize/create; IP allocation/reassignment; placement-group lifecycle; load-balancer configuration; image restores; key generation/rotation; DNS/certificate lifecycle; remaining Cloud API groups require a frozen endpoint inventory |
| Future providers | GCP, Azure and arbitrary third-party runtime/UI extension lifecycle |

External Terraform plan/apply, Ansible target execution, full provider/account scope attestation and resource-level authorization remain incomplete; native coverage cannot substitute for those requirements. Dedicated-server Robot, Kubernetes workload management and database data browsing remain outside their respective agreed initial boundaries.

## Release gate

Before a provider can be described as fully covered, freeze a dated upstream endpoint inventory and annotate every endpoint with its workflow or explicit exclusion. Expand this artifact with tested minimum cloud permissions, account-tier/region constraints, quota behavior, cancellation/recovery evidence, secret fields and live lifecycle results for every operation. Use bounded dedicated test accounts, cleanup/leak checks and explicit authority for irreversible actions. No existing offline test count or runtime capability count satisfies this gate.

Server tag edits use shared operation approval with immutable expected/target metadata. The resource modal supports AWS/Hetzner label rows and DigitalOcean named tags, including removal. Known IaC resources cannot be edited. Complete sets are visible to reviewers; SDK writes may partially apply and race external changes. See TAG_EDITING.md.
