# Server templates

The Templates catalog publishes immutable versions of one server configuration tied to one organization, cloud connection and region. Publication uses the same image, size and public SSH-key catalogs as Create server. AWS configurations also pin subnet and security group. Credentials stay on the connection; no password, token, private key, boot script or arbitrary command is accepted by this template schema.

Publishing the same template name creates the next numbered version. Existing versions are never edited. The catalog currently holds at most 200 versions per organization, including retired/revoked history. Publish requires templates.publish; browse/deploy requires templates.read. Built-in roles receive read access and administrators receive publication access; custom roles can grant these explicitly. Existing MFA, SSO and current-role checks apply.

Deployment changes only the server name and records a reason. The server checks every other submitted value against the immutable version, pins its ID on the durable operation, and uses the existing independent creation approval, connection revision, runtime, maintenance, provider preflight, and observation paths. Publishing a template is not approval to create billable resources. Operators still require operations.request and operations.create. A retry with the same request key returns the existing request, including after retirement.

Retirement blocks new deployment bindings and preserves existing approved requests. Security revocation also cancels pinned work at its next dispatch check. Revocation cannot undo an API call already sent to the provider; observation continues to establish its result. A retired version can subsequently be revoked. Neither operation deletes cloud resources or template/operation history.

This is the first native-template execution path. Multi-resource dependency graphs, workspace drafts, typed variable constraints beyond the server name, template-bound schedules, imported Terraform/OpenTofu/Ansible projects and isolated automation runtimes are not currently supported. They are not represented as supported by this catalog.

Validation uses fake provider execution with real PostgreSQL transactions and a browser fixture. No live cloud creation is performed by these checks.
