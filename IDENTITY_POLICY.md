# Organization sign-in policy

Baseline: 2026-09-05, schema 18. An organization can require the installation's configured OIDC issuer while preserving explicitly designated local recovery administrators. Existing membership and RBAC checks still apply; identity verification does not grant permissions.

## Setup and recovery

Configure and verify OIDC as described in [OIDC.md](OIDC.md). Link an existing account, then complete a fresh **Sign in with your organization** flow, including local TOTP. Linking alone does not convert a local session into an OIDC session.

An administrator with `identity.manage` and recent MFA opens **Sign-in policy**, selects required organization sign-in, designates at least one active built-in administrator for local recovery, and records a reason. The server requires proof from the currently configured issuer before enabling enforcement. Updates use an expected revision, so stale editors cannot overwrite changes silently. At most twenty recovery administrators can be designated.

Recovery administrators can sign in locally with the existing password/MFA flow and disable enforcement during an IdP outage. Membership changes cannot demote or deactivate the last designated active administrator while enforcement remains enabled. This check shares the organization lock with policy changes. Recovery designation applies only while that user remains an active administrator; it grants no additional permissions. Direct database administration remains outside these application safeguards.

Before changing the installation issuer, use recovery access to disable enforcement, configure and verify the new issuer, then enable enforcement again. Policies retain their exact issuer; changing deployment settings does not silently switch trust.

## Enforcement boundaries

- Organization APIs require either an OIDC-authenticated session linked to the required issuer or a current recovery exception. Local sessions can still use account settings and other organizations where authorized.
- Session responses identify inaccessible organizations and omit their permissions. The UI presents the shared sign-in panel instead of rendering their pages.
- SSE checks identity policy when opening and at its existing two-second authorization interval. Revocation closes the stream and clears cached UI data.
- Session refresh preserves the OIDC link identity. Local password and recovery-code sign-in do not acquire OIDC authority.
- Manual operations record the requester's and reviewer's sign-in identities. Scheduled work records its publisher and standing approver. Dispatch checks the current policy and recorded identities alongside existing RBAC, runtime, credential, inventory, and maintenance checks.
- Enabling enforcement can cancel queued local requests or skip locally published/reviewed schedules. Recreate requests or edit and review schedules through authorized sign-in where required. Designated recovery exceptions remain valid for these checks.
- Work already submitted to a provider retains read-only observation; a policy change cannot undo an API call already sent. Policy updates do not cancel provider-side work.
- Identity references use a unique link instance. Unlinking and relinking the same issuer/subject cannot revive an old queued request's authority. Unlinking removes session identity references; the account unlink API also revokes all sessions.

Policy changes emit the existing organization audit and notification events, with issuer, recovery account IDs, and reason. They store no IdP tokens or secrets. The shared TanStack Query, Table, and Form components and existing Radix modal implement the policy UI.

## Evidence and remaining scope

PostgreSQL integration tests cover verified-sign-in requirements, recovery requirements, stale updates, local API denial, hidden permissions, live-stream revocation, last-recovery protection, queued and scheduled dispatch denial, valid OIDC dispatch, unlink/relink fencing, and recovery disablement. Browser checks cover the policy modal and restricted organization screen with mocked identity responses. No real identity provider or cloud mutations are involved.

This is one installation issuer with per-organization enforcement. Multiple IdPs, federation-only enrollment, SAML, SCIM, group mapping, upstream deprovisioning/back-channel logout, and assertion-based MFA remain separate work. An upstream IdP disabling a user does not automatically revoke an existing console session; console account/membership revocation remains authoritative. A recorded OIDC identity for standing automation is not a fresh IdP login at every scheduled occurrence.
