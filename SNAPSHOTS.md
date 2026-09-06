# Server images and snapshots

Stopped AWS, DigitalOcean and Hetzner servers support **Create server image** through the existing resource action modal and durable operation queue. Requesters need resources.read, operations.request and operations.create. A different reviewer needs resources.read, operations.approve and operations.create. Current MFA, connection/runtime, maintenance and identity policy checks apply at request/review/dispatch. Snapshots are not yet a scheduled or bulk action.

| Provider | Native API | Result and coverage |
|---|---|---|
| AWS | EC2 CreateImage | EBS-backed AMI, including EBS block-device mappings. Instance-store disks are excluded. NoReboot is true; the source must be stopped at preflight. |
| DigitalOcean | DropletActions.Snapshot | Droplet disk image; attached volumes and scratch disks are excluded. |
| Hetzner | Server.CreateImage, type snapshot | Server disk snapshot; attached volumes are excluded. |

The image name/description is `providah-` followed by the operation ID. Storage charges apply. Shutdown is a separate reviewed operation; this action never starts or reboots the server. A stopped-server check is not an application-consistency guarantee, and external actors can change state after preflight.

AWS observation checks the returned AMI ID, source instance, generated name and available status. Hetzner checks image ID, source server, snapshot type, description and availability. DigitalOcean checks the returned action ID, source Droplet, action type and completed status. Core preserves the first returned identifier and rejects inconsistent completion evidence. An accepted response remains observing; a lost submission response remains uncertain and is never automatically resubmitted. Missing evidence must be reconciled or reviewed manually. Observation has the existing fifteen-minute deadline; large images may need later read-only reconciliation.

The original provider runtime remains pinned. Older runtimes do not advertise the snapshot action and cannot receive it. Schema 61 adds the action; rollback refuses while snapshot history exists. Connections refresh after outcomes so new images/snapshots are discovered through the existing inventory paths. AWS AMI cleanup/deregistration is not yet supported; this action does not add automatic retention or deletion.

Creating a recovery copy does not edit the IaC-managed source, so the shared state-ownership classification permits this action. New images are not automatically attributed to the source's Terraform project. No ownership-release bypass is added.

Provider behavior references: [AWS CreateImage](https://docs.aws.amazon.com/AWSEC2/latest/APIReference/API_CreateImage.html), [DigitalOcean Droplet snapshots](https://docs.digitalocean.com/products/snapshots/how-to/snapshot-droplets/), [Hetzner Cloud server actions](https://docs.hetzner.cloud/reference/cloud#server-actions-create-image-from-a-server).

Verification uses real SDK HTTP fixtures and the isolated PostgreSQL operation path. No real cloud snapshot was created during development verification.

AWS AMIs can now be deregistered through reviewed resource deletion once available. Associated EBS snapshots are explicitly retained and can be reviewed separately for deletion. See DELETION.md for protections, permissions and dependency limitations.
