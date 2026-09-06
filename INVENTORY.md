# Inventory coverage

Implementation baseline: 2026-09-05. These are read-only inventory workflows, not full resource lifecycle coverage. The existing paginated TanStack resource table supports provider, type, and text filters and a shared details modal. Only servers can receive power actions or become schedule targets.

| Provider | SDK baseline | Resource kinds and API operations | Region behavior |
|---|---|---|---|
| AWS | AWS SDK v2 EC2 v1.329.0 | EC2 instances (DescribeInstances); VPCs (DescribeVpcs); subnets (DescribeSubnets); Elastic IP allocations (DescribeAddresses); security groups (DescribeSecurityGroups); EBS volumes (DescribeVolumes); owned EBS snapshots (DescribeSnapshots); owned AMIs (DescribeImages); SSH key metadata (DescribeKeyPairs); instance types (DescribeInstanceTypes) | Connection region, default us-east-1 |
| DigitalOcean | godo v1.206.0 | Droplets; DNS zones/record metadata; managed database clusters; Kubernetes clusters; certificates; VPCs; reserved IPv4 addresses; firewalls; volumes; Droplet/volume snapshots; private backup images; load balancers; images; SSH key metadata; server sizes through their SDK List methods | Connection region or all regions; firewalls are account-wide; global load balancers use global |
| Hetzner Cloud | hcloud-go/v2 v2.47.0 | Servers; Cloud DNS zones/record-set metadata; certificates; networks; primary/floating IPs; placement groups; firewalls; volumes; load balancers; images; snapshot/backup images; SSH key metadata; server sizes through their SDK List methods | Connection location or all locations; networks and firewalls are project-wide |

References: [AWS EC2 SDK](https://pkg.go.dev/github.com/aws/aws-sdk-go-v2/service/ec2), [DigitalOcean SDK](https://github.com/digitalocean/godo), [Hetzner SDK](https://pkg.go.dev/github.com/hetznercloud/hcloud-go/v2/hcloud).

Connections need read permissions for each listed group. An AWS policy requires the Describe operations listed above; DigitalOcean tokens need the corresponding resource read scopes; Hetzner uses its project read permissions. This change does not grant cloud permissions. Existing narrowly scoped server-only credentials may need additional read permissions before the expanded scan succeeds.

## Snapshot rules

The core requests only the inventory kinds advertised by the pinned runtime. Legacy runtimes receive no inventory_kinds field, avoiding rejection by older strict decoders. New workers only discover those requested kinds. When no kinds are requested, workers retain the legacy server-only behavior. A response without coverage metadata is also interpreted as server-only, preserving compatibility with older approved runtimes.

Identity includes organization, connection, kind, region, and provider-native ID. Servers and volumes with the same numeric ID therefore remain distinct. A successful response retires missing resources only in its declared kinds. Switching back to a server-only runtime leaves other inventory retained with its older observation timestamp.

All requested groups must complete before a snapshot publishes. A failed or malformed page leaves the previous inventory unchanged and marks the connection refresh failed. Each SDK fetches bounded pages; the worker deadline and resource/response limits still apply. The scope list is bounded and rejects duplicates or unknown kinds. Power responses cannot contain discovery scope metadata.

The `global` region means project/account-wide rather than a location. Such resources remain included when a connection selects a region. `present` means the API returned the object; it is not a health or reachability assertion. Network CIDR ranges appear in the current size/configuration field. Volumes expose size; load balancers expose provider status when available, type and public IP. Detailed rules, attachments, topology, health metrics, and editing remain pending.

## Verification and remaining work

Offline fixtures exercise the actual SDK serializers for all twenty added provider/resource combinations, failed reads, authentication, region mapping, pagination rules, and output validation. PostgreSQL checks cover kind-specific identities, filtering, rejection of non-server power requests, and retention across server-only rollback. Browser checks cover type filtering and volume details without server actions.

No live accounts were contacted. Non-server creation, resource modification, non-server deletion, rich properties/dependencies, remaining AWS groups, full DigitalOcean/Hetzner coverage, and per-resource lifecycle verification are outside the currently documented coverage.

## Provisioning catalogs

Images, SSH-key fingerprints, and server sizes appear in the shared resource table and details modal. Public/private key material is never copied into inventory. AWS images are restricted to owned AMIs; DigitalOcean images include API-visible public/private choices; deleted Hetzner images are excluded. Account/project-wide entries use global scope, while regional availability is preserved where supplied by the SDK.

Catalog entries are discovery metadata, not a capacity or eligibility guarantee. Server creation must still validate image, architecture, location, quotas, size, and current provider availability immediately before execution. AWS instance type listings do not establish regional offerings or capacity. Single-server provisioning is now implemented with live preflight checks; see [PROVISIONING.md](PROVISIONING.md).

Server details now include explicit on-demand provider metric history where the selected runtime declares support. Scope and units are documented in [METRICS.md](METRICS.md).

## Snapshot and backup inventory

`storage.snapshot` now covers owned AWS EBS snapshots (`DescribeSnapshots` with `OwnerIds=self`), DigitalOcean Droplet and volume snapshots (`Snapshots.List`), and Hetzner snapshot images (`Image.List` with the snapshot type filter). `storage.backup` covers DigitalOcean private images of type backup (`Images.ListUser`, also filtered client-side) and Hetzner backup images. AWS EBS snapshots are not classified as automatic backups; AWS Backup remains separate; RDS snapshot inventory is described below.

DigitalOcean snapshot region availability produces one inventory entry per reported region; missing regions/source type/source ID fail the scan rather than invent location or source semantics. Hetzner recovery images use global scope. Snapshot/backup records may also appear in the existing image catalog where the provider exposes them as provisioning images; kind is part of identity, and this does not represent two separate cloud objects.

The shared UI labels and filters both kinds. Existing resource scope, incomplete-scan preservation, stale-runtime coverage, and server-only operation gates apply. No restoration, snapshot creation, backup policy management, or verified recoverability is implied. Standalone snapshot deletion is now available through the reviewed workflow in [DELETION.md](DELETION.md). Displayed size/source metadata is provider-reported; it does not prove that a source still exists or that restoration can succeed.

New worker descriptions advertise the additional kinds. Deploy the updated core and worker together; rolling back to an older declared runtime preserves these records with their previous observation time. Newly expanded connection scans require corresponding snapshot/image read permissions. No new database schema or provider SDK dependency was required.

Local SDK fixtures verify actual API paths, AWS ownership, private/type filters, mappings, and failure rejection. PostgreSQL checks verify kind isolation/filtering, retirement boundaries, and rejection of power operations. Browser checks verify the shared snapshot detail flow. Live provider-account verification remains pending.

Detached-volume deletion is now available through the reviewed workflow in [DELETION.md](DELETION.md). Hetzner attached volumes are explicitly marked attached; volumes remain ineligible for server power actions.


## IP, subnet and placement inventory

AWS adds `network.subnet` through paginated `DescribeSubnets` (CIDR, VPC and availability zone) and `network.ip` through `DescribeAddresses` (Elastic IP allocation ID, public/private address and association status). DigitalOcean adds `network.ip` through paginated `ReservedIPs.List`, using the provider's IPv4 address as native identity and reported region. DigitalOcean reserved IPv6 addresses are discovered separately as `network.reserved_ipv6`, described below.

Hetzner adds paginated `PrimaryIP.List`, `FloatingIP.List` and `PlacementGroup.List`. Primary and floating IPs use separate kinds so equal numeric IDs cannot collide; primary IP location and floating IP home location determine inventory region. IPv6 allocations retain the network in size metadata and the SDK address in the public address field. Assignment, blocked/locked status and placement group type/server count are provider-reported observations. Placement groups are project-wide (`global`).

These six API groups use the existing pinned SDKs, runtime inventory capabilities, bounded pagination, shared TanStack table/filter/detail modal and scan publication rules. Invalid IP addresses or missing location/allocation metadata fail the scan. Deploy the core and worker together; older runtime coverage preserves previously discovered kinds. No schema migration is needed.

This is read-only coverage: allocation, reassignment, release, subnet creation and placement membership changes remain pending. These resource kinds cannot receive server power or deletion operations. Local SDK fixtures cover actual endpoints, assignment states, region filtering, malformed addresses and API failures; database and browser coverage exercises kind identity, legacy scan preservation, filtering and shared details. Live cloud verification remains pending.


## Server-side sorting

Inventory headers sort by resource name, provider, type, region or status; a third click restores the default resource-ID order. TanStack Table owns the selected sort, TanStack Query includes it in the cache key, and changing sort starts at the first page. The API additionally accepts explicit `id` sorting. Unsupported fields are rejected.

PostgreSQL applies authorization scope and filters before ordering. Each cursor carries the last sort value plus resource ID, and is bound to organization, filters, sort field and direction. Equal values use the resource ID as a deterministic tie-breaker; text ordering uses the C collation. Legacy requests without a sort retain their previous cursor format. Sorted cursors allow bounded quoted/Unicode names without treating names as SQL.

Pagination is over live inventory, not a frozen snapshot: discovery or resource edits can move rows between pages. Whole-result export consistency, observed-time sorting, multi-column sorting, persistent saved views and performance verification at 100,000 resources remain pending. Local PostgreSQL tests cover both directions for every supported field, ties, Unicode/newline names, complete pagination and mismatched direction tokens.


## Managed services and certificates

DigitalOcean `database.cluster` uses `Databases.List` to inventory identity, name, region, state, engine/version, size slug and node count. `kubernetes.cluster` uses `Kubernetes.List` for identity, region, state, control-plane IPv4 address, version, node-pool count and HA flag. This does not fetch kubeconfigs, query a Kubernetes API, connect to databases or expose database users/passwords/connection strings. SDK results can contain sensitive connection structures; the adapter copies only the listed fields into the worker response.

DigitalOcean and Hetzner `network.certificate` use their certificate list APIs. Both are account/project-wide (`global`) and show certificate name, provider state, type, domain count and UTC expiry in the shared Specification field. Hetzner managed issuance/renewal failures map to failed; other displayed states are provider observations, not an independent TLS validation. Missing expiry is explicitly “not reported.” Certificate PEM bodies and keys are not copied into inventory.

These four integrations use existing SDKs, pagination, runtime coverage, shared TanStack filtering/sorting/column controls and resource details. Narrowly scoped DigitalOcean credentials need the corresponding additional read permissions. Malformed required database/cluster metadata or expiry causes the connection scan to fail and preserve its previous inventory. Old runtimes preserve groups they do not cover.

Local SDK fixtures check actual API paths, mappings, region filtering, malformed responses and secret omission; PostgreSQL checks cover certificate kind isolation/retirement and server-action rejection, and the browser checks certificate details. Live provider verification, database/cluster creation and lifecycle, backup/restore, Kubernetes workload management, detailed domain lists, certificate upload/renewal/deletion, and expiry notifications remain pending.


## Personal saved views

The Saved views control stores up to 50 named inventory views per user and organization. Save current view captures search, provider/type filters, sort field/direction and hidden columns. Selecting a view reapplies those settings and starts on page one. Replace with current settings updates/renames a view after review; Delete view removes the preference only. Views survive reloads and later sessions but are applied explicitly, not automatically.

The API exposes ListInventoryViews, SaveInventoryView and DeleteInventoryView under resources.read. Ownership always comes from the authenticated session, never a client-supplied user ID. Reads and writes are organization/user scoped; writes recheck membership and sign-in policy under the organization lock. Revision checks reject stale replacement/deletion, and the same unavailable response covers foreign and missing IDs. The server validates filters/columns, prevents hiding every column, enforces the 50-view limit and rejects duplicate names within the same personal scope.

Definitions contain settings only, never cached resource rows or grants. Applying a view makes normal authorized inventory requests, so saved settings cannot retain access after revocation. Audit records contain the view ID/revision, not its private name/search/filter contents. Schema 22 stores these preferences in PostgreSQL; no browser storage or new dependency is used.

Workspace-shared views, delegation/sharing policy, column ordering, multi-column sorting and automatic last-view restoration remain pending. Personal views do not implement workspace isolation or shared-view authorization.


## DNS zone and record metadata

DigitalOcean uses `Domains.List` and per-domain `Domains.Records`. Hetzner uses the current Cloud API `Zone.List` and `Zone.ListRRSets` through hcloud-go; this does not integrate the legacy standalone Hetzner DNS API or Robot.

`dns.zone` reports identity, zone name, default TTL and available provider status/mode/count metadata. `dns.record` reports record name with its zone, type and TTL; Hetzner additionally reports the number of values in each record set and inherits the zone TTL when the set has no override. All records are global to their connection.

DigitalOcean record identities combine zone name and numeric record ID; Hetzner identities combine zone ID, record name and type. Equal record names/IDs across zones remain separate. Native inventory identifiers allow up to 512 bytes to accommodate valid DNS names and these composite identities. Server action validation is unchanged.

Zone-file contents, DNS record values, TSIG transfer keys and raw SDK objects are not persisted in this metadata inventory. Detailed value inspection, record create/update/delete, zone management and delegation verification remain pending. AWS Route 53 metadata discovery is described below. This does not constitute complete DNS management.

Both zone and nested record pages use the existing bounded scan path; any page failure or resource limit discards the entire scan. Only validated ASCII/punycode DNS zone names enter DigitalOcean's domain path. A complete record scan requires permission to read every listed zone's records; it can exceed the worker deadline for very large DNS accounts and then preserves the previous snapshot. Incremental per-zone jobs remain a future scaling step.

SDK fixtures verify zone paths, nested pagination, TTL inheritance, zone-qualified identities, sensitive-field omission and later-page failure preservation. PostgreSQL/browser checks cover kind isolation, old-runtime coverage, shared detail rendering and rejection of server actions. Updated runtimes advertise both kinds; deploy the core and worker together.


## AWS Route 53 hosted zones and record sets

AWS now advertises `dns.zone` and `dns.record`, using the official Go SDK v2 Route 53 module v1.69.0. `ListHostedZones` discovers public and private account-owned hosted zones; `ListResourceRecordSets` discovers each zone's record sets. Both use explicit broker-supplied credentials and the SDK's endpoint/signing resolution. They do not load ambient profiles or caller-selected service endpoints. See [ListHostedZones](https://docs.aws.amazon.com/Route53/latest/APIReference/API_ListHostedZones.html) and the [Route 53 Go SDK](https://pkg.go.dev/github.com/aws/aws-sdk-go-v2/service/route53@v1.69.0).

Required AWS actions are `route53:ListHostedZones` (account listing) and `route53:ListResourceRecordSets` on all discovered hosted zones. Console access remains `resources.read`; discovery requests use the existing connection refresh permission. Additional cloud permissions are not granted automatically. A missing permission fails the complete connection scan and preserves its previous inventory.

Zones show name, public/private visibility and record-set count. Records show FQDN, routing-policy set identifier when present, type, TTL and value count. Aliases report that TTL is managed by the target; they do not invent a zero TTL. Zone comments/caller references, record values, alias target names and other unselected SDK fields are excluded. This is metadata discovery, not record-value inspection or DNS management.

DNS resources are connection-global, even when that AWS connection has a regional EC2 scope. Zone IDs omit the API's `/hostedzone/` prefix. Record identities combine the zone ID with a SHA-256 digest of the JSON tuple of zone ID, record name, type and routing-policy identifier. This bounded identity keeps same-name records in different zones or policies distinct. It is an inventory identifier, not a Route 53 API record ID. Future mutations must resolve the full record key and perform their own reviewed preflight.

Zone markers and the three-part record cursor (name/type/identifier) are preserved across pages. Missing/repeated pagination markers fail the scan. Nested scans retain the shared resource/page limits and worker deadline; per-zone scheduling remains a future scaling step. Newly advertised kinds use the existing TanStack DNS table filters/details and runtime coverage negotiation. Older runtimes preserve records outside their declared coverage.

Local SDK fixtures verify real paths and signed requests, public/private metadata, both pagination levels, alias TTL handling, record identity, selected-field isolation and whole-scan failure on API errors/malformed metadata/cursors. Existing PostgreSQL kind isolation/coverage tests pass. Live AWS account verification, partitions beyond the tested commercial endpoint, delegation/DNSSEC/health checks, record-value reads, create/update/delete, and change tracking remain open. No live DNS account was contacted.


## AWS database inventory

The pinned AWS SDK for Go v2 RDS module (`service/rds` v1.128.0) adds four regional inventory kinds:

| Kind | SDK operation | Display metadata |
|---|---|---|
| `database.instance` | `DescribeDBInstances` | Identifier, status, engine/version, instance class, allocated storage when reported |
| `database.cluster` | `DescribeDBClusters` | Identifier, status, engine/version, engine mode, allocated storage when reported |
| `database.snapshot` | `DescribeDBSnapshots` | Identifier, status, engine/version, snapshot type, allocated storage when reported |
| `database.cluster_snapshot` | `DescribeDBClusterSnapshots` | Identifier, status, engine/version, snapshot type, allocated storage when reported |

The connection's regional identity and existing credential broker apply. No ambient AWS profile or metadata identity is loaded. The SDK performs signing and response parsing. Credentials need `rds:DescribeDBInstances`, `rds:DescribeDBClusters`, `rds:DescribeDBSnapshots` and `rds:DescribeDBClusterSnapshots` for the selected account/region. A denied read fails the complete connection refresh and preserves the prior inventory.

Snapshot reads explicitly exclude shared and public snapshots and leave snapshot type unset, selecting the API's default owned manual/automated set. AWS Backup recovery points, retained automated-backup inventories and point-in-time recovery windows are separate unfinished coverage. These semantics follow the [instance snapshot API](https://docs.aws.amazon.com/AmazonRDS/latest/APIReference/API_DescribeDBSnapshots.html) and [cluster snapshot API](https://docs.aws.amazon.com/AmazonRDS/latest/APIReference/API_DescribeDBClusterSnapshots.html).

The [instance API](https://docs.aws.amazon.com/AmazonRDS/latest/APIReference/API_DescribeDBInstances.html) can also return Neptune and DocumentDB metadata. Returned engine names remain unchanged; seeing these records does not imply complete integration with those services. Cluster and instance records represent distinct API resources, including Aurora members. Native identifiers may overlap between kinds without colliding in the inventory.

Only the display fields above are copied. Database endpoints, usernames, master-secret references, KMS key references, tags and arbitrary SDK payloads are not retained. Missing allocated storage remains absent rather than being displayed as zero. Reported storage is allocation metadata, not measured usage; a snapshot's presence does not prove recoverability.

Every API call requests at most 100 records. The shared page/resource/deadline limits apply, repeated markers fail explicitly, and failure in any of the four families discards the entire scan. Existing SSE invalidation, TanStack table sorting/filtering, saved views and the resource-detail modal handle these kinds without a database-specific page. Older runtime coverage retains newer kinds at their previous observation time. Deploy the core and updated worker together.

Database resources cannot use server resize/delete or EBS snapshot-delete actions. AWS instance/cluster start and stop are described below. Creation, modification, deletion, snapshot creation/restoration, richer topology and backup policy controls remain required work. No SQL/data access is added.

SDK fixtures cover all four real signed API request paths, two-page results, ownership flags, empty results, repeated cursors, invalid metadata, duplicate identities, unknown allocation and a last-family failure after earlier successful reads. Real PostgreSQL checks cover runtime capabilities, publication, cross-organization denial, kind identity/filtering, failure preservation, legacy coverage preservation and retirement. Browser fixtures exercise all four types through the shared table/detail modal. Live cloud verification remains pending.


## DigitalOcean reserved IPv6

The existing godo SDK lists `/v2/reserved_ipv6` with `ReservedIPV6s.List` and 200-item pagination. The resource kind `network.reserved_ipv6` is advertised separately so older runtime scans cannot retire records for coverage they do not claim. The shared inventory includes the canonical IPv6 address, region and assigned/unassigned state. IPv4, malformed addresses, missing regions and invalid attached Droplet IDs fail the scan. Failed later pages publish no partial inventory; existing records remain authoritative until a successful covering scan.

This is metadata discovery, not address allocation, reassignment or release. It uses the existing credential boundary and requires `reserved_ip:read`. No new dependency or UI page is needed. API contract: [DigitalOcean reserved IPv6](https://docs.digitalocean.com/products/networking/ipv6/reference/api/reserved-ipv6/). Local SDK tests cover pagination, region filtering, empty results, canonicalization, assignments, malformed metadata, duplicate addresses and late/cyclic failure. Live-account verification remains open.

## Reviewed network deletion

Unused DigitalOcean/Hetzner networks and firewalls expose Delete resource in the shared detail modal when the installed runtime advertises the capability. Read/review checks reject applicable assignments, selectors, peerings, default/protected networks and vSwitch connectivity. [DELETION.md](DELETION.md) records exact scope and the provider-side race boundary. AWS network resources and other network kinds remain read-only; creation and editing are not implemented by this change.

## Reviewed SSH-key deletion

AWS, DigitalOcean and Hetzner SSH-key records expose Delete SSH key in the shared resource modal when the runtime explicitly advertises it. The review explains that existing server access remains, and identifies the fingerprint and immutable native ID. See [DELETION.md](DELETION.md). Key creation/import and guest authorized-key management remain separate unfinished work.

## Team dashboards

In Dashboards, choose Edit dashboard → Dashboard visibility → Selected teams, then choose up to 20 teams. Sharing requires dashboards.manage; readers cannot change or delete shared dashboards. Private dashboards remain owner-only. Team audiences include current members of any enabled selected team, the owner, and organization dashboard managers. All widgets still require their ordinary resource/operation permissions; sharing does not grant cloud access.

The audience list exposes only organization team IDs, names and enabled state to dashboard managers, without member identities or role assignments. Membership changes take effect on subsequent server reads and normal live refresh. Deleted team references remain recorded: a dashboard with no surviving selected team stays restricted to its owner/managers. The editor shows unavailable team references for explicit removal; it never silently falls back to organization visibility. Select Private or Shared with organization explicitly to change that audience.

Schema 51 stores bounded team IDs alongside the existing organization-sharing flag. Team references are validated under the organization write lock. Rollback refuses to discard nonempty team audiences. Shared dashboards retain the existing 50-per-organization cap and unique shared name across organization/team audiences.

## DigitalOcean projects and App Platform

The official godo SDK now discovers `organization.project` through Projects.List and `application.app` through Apps.List. Cloud projects use global scope and expose name, environment and default-project status. Apps expose name, App Platform region slug, deployment phase and counts of services/workers/jobs/static sites/functions/databases. In-progress deployment phase takes precedence over pending and active deployments; this is deployment state, not a live application health probe. Apps without a deployment show `not_deployed`.

App specifications, environment variable values, deployment specs/logs, repository configuration, owner metadata and project descriptions are not persisted or returned. App Platform region slugs (for example `nyc`) are used as returned; they are not translated into Droplet datacenter slugs such as `nyc3`. Use an account-wide connection to include both regional naming schemes. Project membership and app/project relationships are not yet exposed.

Both kinds use existing bounded SDK pagination, atomic scan publication, organization filtering, resource deep links, saved views and dashboards. Failed/invalid later pages discard the new snapshot. Creation, updates, deployments, application deletion and project resource assignment remain unimplemented. Empty non-default cloud projects now support reviewed deletion; see DELETION.md. Scanner version 4 recognizes stored `digitalocean_app` and `digitalocean_project` state references under the existing connection-scope limitations. API permissions must allow listing the selected services. [DigitalOcean API reference](https://docs.digitalocean.com/reference/api/digitalocean/).

## AWS route tables and gateways

EC2 discovery includes `network.route_table`, `network.internet_gateway` and `network.nat_gateway` using DescribeRouteTables, DescribeInternetGateways and DescribeNatGateways, with 100-item pages. Cloud credentials require those corresponding `ec2:Describe*` permissions. Names and labels use provider tags; organization, connection and region boundaries follow the shared inventory path.

Route tables show VPC ID, route/blackhole counts, explicit association count and main-table status. A blackhole yields a `degraded` summary; absence of blackholes does not prove network reachability. Implicit subnet associations are not enumerated. Internet gateways show attachment presence and VPC attachment state. NAT gateways show state, VPC/subnet identity, connectivity/availability mode and address count. Regional NAT gateways need not have a subnet ID. Deleted NAT gateways are omitted from the new snapshot. Scanner version 5 recognizes stored Terraform/OpenTofu ownership for these three kinds, using the existing resource protection and project links. Route entries, address-level detail, topology traversal and create/edit/delete workflows remain unfinished.

The shared EC2 inventory pagination loop now rejects repeated tokens, including empty-page loops. Failure discards the complete new scan and preserves prior inventory. References: [route tables](https://docs.aws.amazon.com/AWSEC2/latest/APIReference/API_DescribeRouteTables.html), [internet gateways](https://docs.aws.amazon.com/AWSEC2/latest/APIReference/API_DescribeInternetGateways.html), [NAT gateways](https://docs.aws.amazon.com/AWSEC2/latest/APIReference/API_DescribeNatGateways.html).

## AWS load balancers

`network.load_balancer` now includes Application, Network, Gateway and Classic Load Balancers via the official ELB v2 and Classic ELB Go SDKs. Discovery reads both API generations in the connection region using 20-item pages and a matching DescribeTags call per nonempty page. Cloud permissions require `elasticloadbalancing:DescribeLoadBalancers` and `elasticloadbalancing:DescribeTags`. Names and tags feed the existing resource table, saved scopes and dashboards.

Modern balancers use their ARN as native identity and display provider state, type, scheme, address type and availability-zone count. Classic balancers use their regional name as native identity and display `present`, scheme and zone/registered-instance counts. Neither status is an application-health result. Listeners, rules, certificates, endpoints, target-group membership and health probes are not yet projected. Reviewed AWS load-balancer deletion is available through the shared approval workflow (see DELETION.md); scanner version 7 recognizes stored aws_lb/aws_alb/aws_elb ownership references, including the modern ARN region.

Missing/foreign/duplicate tag records, failed pages, repeated markers and duplicate inventory identities fail the complete scan and preserve prior inventory. A connection needs access to both API generations; partial permissions do not silently publish partial coverage. References: [DescribeLoadBalancers](https://docs.aws.amazon.com/elasticloadbalancing/latest/APIReference/API_DescribeLoadBalancers.html), [modern tags](https://docs.aws.amazon.com/elasticloadbalancing/latest/APIReference/API_DescribeTags.html), [Classic tags](https://docs.aws.amazon.com/elasticloadbalancing/2012-06-01/APIReference/API_DescribeTags.html).

## AWS EKS clusters and managed node groups

The official EKS SDK discovers clusters and managed node groups through ListClusters/DescribeCluster and ListNodegroups/DescribeNodegroup. The two inventory kinds share existing filtering, tag scopes, dashboards and resource links. Cluster names identify clusters; `cluster:nodegroup` identifies node groups, so equally named groups in different clusters remain distinct. Display names include the parent cluster.

Clusters show provider status and Kubernetes/platform versions. Managed node groups show status, Kubernetes version, capacity type and reported desired/minimum/maximum sizes. Desired capacity is a configuration value, not a live healthy-node count. Missing capacity fields are omitted. AWS tags are projected per resource; node groups do not inherit cluster tags in the console. Cluster endpoints, certificate-authority data, IAM roles, SSH configuration and Kubernetes credentials are not projected or used. Discovery performs no Kubernetes API calls.

Permissions require `eks:ListClusters` and `eks:DescribeCluster` for clusters; node-group discovery requires ListClusters, ListNodegroups and DescribeNodegroup. Both lists use bounded 100-item pages, reject duplicate names/repeated tokens, and require exact described identities. Any missing, denied or invalid later response preserves previous inventory rather than publishing a partial scan.

External connected clusters, self-managed node groups, Fargate profiles, Auto Mode node pools, add-ons and workloads are not enumerated. Cluster/node-group create, upgrade, scale and removal remain unfinished. Scanner version 8 recognizes aws_eks_cluster and aws_eks_node_group references using recorded/project regions. References: [cluster listing](https://docs.aws.amazon.com/eks/latest/APIReference/API_ListClusters.html), [managed node groups](https://docs.aws.amazon.com/eks/latest/APIReference/API_ListNodegroups.html).

## DigitalOcean Kubernetes node pools

DigitalOcean now discovers each managed node pool as `kubernetes.node_group` through the existing cluster listing and node-pool endpoint. Inventory uses the pool ID, a cluster/pool display name, region, size, node count, autoscaling bounds, tags and labels. Status `present` means the pool was returned, not that its nodes are healthy. Node details and cluster endpoint/credentials are not copied.

Both cluster and pool listings use bounded pagination; failed later pages invalidate the whole scan. The installed godo v1.206.0 node-pool convenience method drops pagination links, so this endpoint uses godo's request/response transport with its typed node-pool models and retained links. No new HTTP client or dependency is introduced. Node-pool creation and scaling/deletion remain unfinished. Scanner version 9 maps supported stored Terraform cluster/default-pool and standalone-pool identities; see DELETION.md.

## AWS placement groups

AWS now exposes placement groups as `compute.placement_group`, alongside the existing Hetzner kind. The EC2 SDK DescribePlacementGroups response supplies the regional group ID/name, state, placement strategy, optional partition count/spread level and tags. Deleted groups are omitted. Missing identity/state/strategy, invalid counts or duplicate identities invalidate publication.

This EC2 endpoint has no pagination fields in the installed SDK; the shared resource/result bounds still apply. Listing does not enumerate group member instances, prove capacity, or enable placement-group creation, editing or removal. Scanner version 10 maps supported AWS and Hetzner placement-group state identities; see DELETION.md.

## AWS S3 buckets

AWS storage.bucket discovery lists general-purpose buckets in the configured connection region through the installed S3 SDK, with bounded regional pagination. It reads bucket tags for shared filters and dashboards. NoSuchTagSet means an empty tag set; denied tag reads or later page failures invalidate the full scan. Bucket name and reported region must be valid, and duplicate resource identities are rejected. The reported creation timestamp is display metadata, not an immutable identity guarantee.

Required reads are s3:ListAllMyBuckets and s3:GetBucketTagging for discovered buckets. Status present does not establish public/private access, encryption, versioning, retention or health. Directory buckets, access points, object listing/download/upload, bucket configuration and native lifecycle are not implemented. This provider inventory is separate from Providah's internal RustFS artifact storage. Scanner version 11 recognizes supported bucket and policy/versioning/lifecycle state references; see DELETION.md.

Resources and summaries show account-owned infrastructure. Public provider images and machine-size catalogs are excluded; creation and resize selectors request catalogs explicitly. Owned images, snapshots and backups remain inventory.

Organization administrators can choose **Manage existing only** or **Manage and create** under **Administration → Resource management**. Manage existing blocks new servers, snapshots and SSH-key imports at request, approval and dispatch; already-submitted work continues observation. Policy changes are audited and checked against the current revision. The default is manage and create, subject to member permissions and approval.
