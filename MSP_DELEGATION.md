# MSP delegation implementation boundary

The accepted plan requires customer-controlled role grants to an MSP team, separate from membership of that team. Both sides must remain active. Current organization teams are local grants, not MSP delegation.

## Required authorization change

Do not add foreign team permissions to `effective_memberships` while leaving identity checks as a single organization-level boolean. That would combine grants from several MSP organizations before checking which MSP sign-in policies the current session satisfies. A permissive grant could then admit permissions from another grant whose source identity policy failed.

Resolve permission contributions separately. Each delegation must carry customer organization, source organization/team, customer role, active status and revision. A contribution requires an active customer, active source organization/team, active direct source membership and account, plus source and customer identity/MFA policy satisfaction for the requesting identity. Union only authorized contributions. Local grants must remain usable independently of an unrelated failing delegation.

Apply the same resolver to HTTP, SSE and execution-time authorization. Workers must retain the requesting/approving identity context already used for OIDC checks. Management grant ceilings need the actor's authorized contributions; inspecting a target user's potential grants is a distinct operation and must not accidentally use the manager's sign-in context.

## Delivery requirements

- Customer administrators explicitly grant a customer role to an identified MSP team. MSP administrators cannot create or enlarge that customer grant.
- Use direct source memberships for delegation eligibility; do not create recursive delegation chains.
- Keep customer grant and source-team changes separately revisioned and audited.
- Revoking either side removes future authorization, including pending work. Existing provider submissions cannot be recalled.
- Preserve independent approval, connection/module gates, maintenance rules and customer MFA/SSO restrictions.
- Do not expose foreign organization/team directories to someone who merely knows an ID. Offer/acceptance UI must reveal only the relationship the parties authorize.
- Verify multiple simultaneous delegations with different source identity policies, local-plus-delegated grants, source/customer revocation, foreign IDs, stale edits and queued execution.

Connection/workspace/resource constraints remain separate required work; an initial organization role grant must not be described as resource-scoped delegation.
