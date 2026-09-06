# Administration and user consoles

The operational console is at /app. Administration is at /admin, with its own navigation and an organization directory. Both reuse the same session, CSS, TanStack tables/forms/query cache and page components. Old page URLs redirect to their new location, retaining query strings and fragments.

Global administrators can see every active organization returned by the existing server-authorized session directory, create organizations, change global MFA policy and drill into organization management. Delegated administrators see only their organizations with admin.access. Organization selection closes stale page forms and removes organization query caches. Existing SSE authorization/revocation and identity-policy checks remain active.

Grant admin.access through Team & access alongside specific permissions. It permits entering the administration UI; it grants no underlying API authority. Each page still requires its existing permission, and every backend request still enforces its organization scope and operation permission. A read-only membership administrator can hold admin.access and members.read; role editing additionally requires roles.manage, and membership changes require members.manage. Role grants cannot exceed the caller's own permissions.

Schema 38 adds admin.access to existing roles/legacy grants with administrative read/manage capabilities. Newly created built-in Administrator, Connection manager and Auditor roles receive it; Viewer, Operator and Approver do not. New custom roles must explicitly select it. Removing admin.access removes administrative navigation; to revoke API privileges, remove those specific permissions or disable the membership as well. This distinction keeps API clients and operational workflows governed by their actual capabilities.

Global administrators can search the installation directory for users and organizations, including disabled records, with status filters and pagination. This metadata-only API rechecks the active global grant and installation MFA requirements on every request. Delegated admins retain their scoped session organization list and cannot call the installation directory. Global admins can enable/disable other users and grant/revoke their global-admin authority through a password/MFA-protected modal. Every successful edit revokes all target sessions, checks the displayed revision and records actor, target and resulting state in installation_events. Self-edits are rejected so the acting active global admin remains available. Organization roles remain unchanged. Global audit at /admin/installation-audit shows installation user-access events and cross-organization audit records with shared filters and pagination. Only global administrators can use this route/API. Global admins can enable/disable organizations from the directory. Fresh proof and current revision are required; state changes are audited. Disabled scopes lose console/cloud authorization and queued notifications pause. Data and cloud resources remain, already-dispatched calls may finish, and existing schedule rules do not replay missed occurrences. Global health at /admin/health shows durable workload counts/ages, database pool usage and provider-execution configuration; it does not probe external service health. Installation event export, external health probes and additional admin presets remain unfinished.

## Deep links and breadcrumbs

Authenticated pages have shared breadcrumbs. URLs include org for the selected organization; inventory detail URLs also include resource. A copied inventory URL reopens the resource modal after local sign-in or refresh. Back/forward navigation follows URL selection, closing the modal removes its resource parameter, and changing organizations drops stale resource/query state. Unknown or unauthorized organization IDs do not fall back to another tenant's data. The server still authorizes the requested organization and resource.

Resource detail modals include their own breadcrumb back to inventory. Template detail modals and OIDC round-trip return destinations still need equivalent shareable selection links; filters and drafts are not yet all URL-backed.

Operation details also use a stable operation URL parameter under /app/operations with org. The exact record is fetched independently of the current table page through GetOperation. Reload/back/forward preserve selection; closing returns to the operations page. The modal includes an operation breadcrumb. The server requires operations.read and the requested organization scope; nonexistent and foreign IDs do not reveal record existence. Approval/cancellation/reconciliation remain explicit actions, never side effects of opening a link.


Schedule details use schedule under /app/schedules with org. GetSchedule reads the exact non-deleted record independently of the list and requires schedules.read in the requested organization. The modal includes a breadcrumb, loading/error handling and the existing explicit standing-approval, pause, identity and deletion controls. Edit drafts remain local form state; credentials and approval proofs are never URL parameters.


Managed-project details use project under /app/templates with org. GetAutomationProject requires templates.read and returns only the existing project metadata serializer. Exact reads do not depend on the catalog page; the state-history query waits for an authorized project read. The detail modal has a breadcrumb and loading/error states. Raw state, encrypted inputs, keys and run credentials are never returned by this endpoint or placed in URLs.


Published automation details use version under /app/templates with org. GetAutomationVersion returns the existing metadata and current runtime availability under templates.read, independently of catalog rows. The modal has a breadcrumb and error/loading states. Status changes and validation are explicit actions. Selecting a project clears version selection and vice versa; a manually combined URL gives publication selection precedence.


Teams on Team & access add existing roles to organization members. A membership manager may only assign or remove team roles within their own permissions. Disabled teams grant nothing; inactive accounts/memberships cannot use retained links. Team edits are revision-checked and audited. Individual roles remain additive, and SSO recovery still requires designated direct administrators. Team links preserve organization scope. Cross-organization MSP delegation is not implemented.


Deleting a team requires its current revision, authority over its role and exact-name UI confirmation. Only that team's additional grants are removed; direct memberships and other teams remain. The deletion is audited.


Installation audit includes user access and global MFA-policy changes. Actor email is snapshotted at insertion. Installation/organization audit reject normal database updates/deletes/truncation as applicable; database owners can bypass such guards and remain outside the application guarantee.
