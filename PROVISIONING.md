# Reviewed server creation

Baseline: 2026-09-05. One-server creation is implemented for AWS EC2, DigitalOcean Droplets, and Hetzner Cloud using the installed provider Go SDKs. This is a starting lifecycle workflow, not full provisioning coverage or live-account certification.

## Workflow and authority

Inventory → Create server opens the shared modal. Choose an enabled connection, then a server name, explicit location, image, size, and existing SSH key. Catalog pickers use TanStack Query pagination, connection-scoped API filtering, and the shared TanStack Form. AWS additionally requires an explicit subnet and security group. Review the exact configuration and billable-resource notice, confirm the name, and provide a reason.

Creation requires resources.read, operations.request, operations.create, and recent MFA. The UI also requires connections.read for its connection picker. An independent reviewer needs operations.approve plus operations.create and resources.read. Built-in administrators receive creation authority; other roles need an explicit grant. Approval lasts up to one hour and remains subject to the originally reviewed maintenance window. Creation does not bypass maintenance policy.

The operation stores the exact non-secret inputs, connection revision, provider image, module revision, and requester. At dispatch, the coordinator rechecks authority, approval, credentials, runtime, and maintenance. The isolated worker performs live read validation before one creation API call. No boot scripts or credentials enter creation input or approval history.

## Provider behavior

| Provider | Live checks | Submission |
|---|---|---|
| AWS SDK v2 EC2 | Account-owned or Amazon-published available EBS-root AMI; instance architecture; existing key pair; available subnet with addresses; security group in the same VPC; instance-type offering in the subnet availability zone | RunInstances with count exactly one, original operation ID as client token, Name/request tags, no public IPv4, required IMDSv2, encrypted root disk and root/primary-interface delete-on-termination |
| DigitalOcean godo | Available image in the location; existing SSH key; available size in the location with sufficient root disk | Droplets.Create with one image/size/location, SSH key, request tag, and IPv6; provider default VPC/public networking; backups are not enabled |
| Hetzner hcloud-go/v2 | Available non-deleted image; compatible size architecture and disk; available size/location combination; existing SSH key | Server.Create with location, image, size, SSH key, startup enabled, and two request labels; provider default public networking |

AWS requires DescribeImages, DescribeInstanceTypes, DescribeInstanceTypeOfferings, DescribeKeyPairs, DescribeSubnets, DescribeSecurityGroups, RunInstances, CreateTags and observation DescribeInstances. Encrypted AMIs/volumes may require appropriate KMS grants. DigitalOcean and Hetzner credentials need the corresponding read/create/tag or label permissions. Quotas, current capacity, billing entitlement, and all provider-specific policies remain authoritative at submission; preflight is not a capacity reservation or dry-run guarantee.

Catalog choices expose IDs and descriptions, not private keys. The worker discards any Hetzner root password returned by the SDK. AWS source credentials follow the existing role broker when configured. Resource prices, networks, storage, and other charges are not estimated; the review explicitly discloses that the operation creates billable resources without a price quote.

## Outcomes and recovery

Submission records the provider-native ID and binds a real inventory resource when the response supplies one. There is no invented cloud resource before submission. Accepted remains observing until a read sees the marked server running/active. The normal discovery workflow fills additional resource metadata.

All mutation SDK retries remain disabled. AWS receives its native client token as an additional guard; the coordinator does not automatically replay the request even there. Lost responses become uncertain. Check provider state performs only read queries using the exact request tag/label pair, allowing recovery when the native ID was lost. An absent, ambiguous, wrong-region, or conflicting identity cannot prove completion. Missing/changed credentials or runtime keep the existing uncertainty semantics. Manual resolution still needs independent authority and a verification reason.

Pending and uncertain creation blocks another request for the same connection, location, and server name, even with a new request key. Changing the name describes a different request; this is not a cloud-wide duplicate-name detector. Preserve request markers until reconciliation completes. Failed provisioning does not automatically delete resources or retry creation. Inspect the provider before deciding on recovery.

A discovery snapshot overtaken by an operation is discarded and rescheduled. It cannot overwrite or retire a newer creation observation. This currently checks the whole connection; per-resource versions can replace it if concurrent activity makes refresh starvation measurable.

## Verification and limits

Offline tests exercise the actual three SDK serializers, exact-one AWS input/security settings, public-key/password exclusion, preflight failure, lost-response uncertainty, and read-only marker recovery. PostgreSQL tests cover approval when required by organization policy, immutable input/idempotency, absent resource before submission, real resource binding, duplicate uncertainty fencing, revoked authority, and the stale-snapshot race. Playwright covers catalog selection and the shared review modal; its final creation submission is mocked because that browser backend has no provider launcher. Real worker behavior is tested independently through SDK transports.

No live cloud creation was performed. Custom scripts/cloud-init, multiple keys, additional disks, private network selection for DigitalOcean/Hetzner, firewalls at creation for those providers, AWS IAM profiles, public/Marketplace AMIs, pricing estimates, quota preflight, templates, bulk creation, and IaC ownership remain open PLAN requirements. Provider default network behavior and external changes can affect reachability; full resource policy enforcement is not claimed.

### Optional private network placement

DigitalOcean and Hetzner creation forms can select an existing network from discovered inventory. The reviewed ID is stored with the operation and with published server templates. Only the server name can change when deploying a template; changing the network requires a new template version.

DigitalOcean's official Go SDK re-reads the VPC and checks its region before passing VPCUUID to droplet creation. Hetzner's SDK re-reads the network and checks that a cloud subnet exists in the selected location's network zone before passing Networks to server creation. Observation waits for the requested attachment before reporting successful creation. Capacity and network configuration may still change between preflight and provider acceptance; failed/lost submissions follow the existing uncertain-outcome contract and are not retried automatically.

A missing selection preserves provider-default VPC behavior on DigitalOcean and no explicit private network on Hetzner. Public networking remains enabled. AWS still uses its explicit subnet/security-group configuration. The selected runtime must advertise private_network_create; older images cannot accept new network-bound requests. This does not create networks or edit attachments on existing servers.

AWS creation catalogs include the newest Amazon Linux 2023 default-kernel images for x86_64 and arm64, selected from Amazon-owned EC2 images. Public images remain catalog-only. Creation rechecks the reviewed AMI ID with EC2 owner filters (`self`, `amazon`); arbitrary third-party publishers are excluded.
