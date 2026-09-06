# OIDC sign-in and explicit account linking

Baseline: 2026-09-05. The console supports one installation-configured OIDC issuer as an additional sign-in method for existing accounts. Users still enroll and use local TOTP. Organization invitations, memberships, custom roles, and permission checks remain authoritative. Per-organization enforcement and designated local recovery access are documented in [IDENTITY_POLICY.md](IDENTITY_POLICY.md). Federation-only enrollment, SAML, and SCIM remain pending.

## Configuration

Register a confidential authorization-code client at your identity provider. Its exact redirect URI is `ORIGIN/api/oidc/callback`; no browser-supplied return address is accepted. Enable authorization-code flow, PKCE S256, and the openid scope. Supported token signatures are RS256, ES256, and PS256.

Set these deployment environment values:

- OIDC_ISSUER: the exact HTTPS issuer, including any realm/tenant path.
- OIDC_CLIENT_ID: the registered client ID.
- OIDC_CLIENT_SECRET or OIDC_CLIENT_SECRET_FILE: the client secret, or an already-mounted file containing it. Never set both. The file reader accepts a regular file up to 16 KiB.
- ORIGIN: the console's exact public origin, without a path or trailing slash.
- COOKIE_SECURE: keep enabled in production and serve the console over HTTPS. Disabling secure cookies is for local development only.

The supplied development Compose file fixes ORIGIN to localhost:8760; production deployments must override that setting as well as provide TLS. Mount a secret file through deployment configuration if using the _FILE option. No identity-provider secret is entered in the browser or returned by an API. Existing installation keys are not replaced.

Issuer, discovery endpoints, and signing-key endpoints must use HTTPS on port 443 without userinfo, query parameters, or fragments. The existing outbound client rejects private/reserved network destinations, redirects, and the console origin. Publicly reachable self-hosted identity providers work under that policy; private-network IdPs and custom CA injection need an explicit deployment policy before support is added.

Discovery is lazy: unavailable IdP discovery cannot prevent application startup or local sign-in. Successful metadata is cached for the process lifetime; restart after changing registered endpoints. The maintained library refreshes signing keys when needed. Network calls have deadlines and responses are capped at 1 MiB.

## Linking and sign-in

An existing user opens **Organization sign-in**, verifies their local password and a fresh authenticator code, then authenticates with the configured IdP. The callback links that user's stable issuer/subject pair. The initiating local session must still exist. A subject cannot be linked to two users, and one user cannot silently replace an existing link for that issuer.

Email, email_verified, display name, groups, acr, and amr claims do not establish account ownership, organization membership, permissions, or MFA freshness. There is no matching by email and no automatic account or role creation. An administrator invitation and local enrollment remain required before a new user can link an identity.

**Sign in with your organization** starts a fresh OIDC flow. A valid signed ID token identifies an already-linked active account. The browser must then submit that account's local TOTP before the server issues its normal short-lived access cookie and rotating refresh cookie. An IdP proof alone creates no console session. No IdP access token, refresh token, or ID token is stored or sent to the frontend.

**Unlink and sign out** requires the local password and a fresh TOTP. It removes the identity link and revokes every local session. Pending login proofs recheck the link and active user before use, so retaining an old browser cookie does not bypass unlinking. Unlinking does not terminate the user's IdP session. Local password changes/recovery retain their existing session-revocation behavior.

## Flow boundaries

Flows expire after five minutes; expired database rows are removed when another flow begins. They use an unpredictable state plus a separate HttpOnly browser-binding cookie. The temporary OIDC cookie uses SameSite=Lax for the top-level callback; ordinary session cookies remain SameSite=Strict. State and browser binding must both match before a one-time database claim allows token exchange. Replayed callbacks cannot exchange again.

Nonce and PKCE verifier are separately derived with domain-separated HMAC from the installation session key and random state. Only state/browser hashes and flow metadata are stored. No temporary authentication secret needs an additional database encryption column. Changing the session key invalidates pending flows. A successful callback remains single-use across replicas and restarts using the shared database and session key.

The verifier checks signature, issuer, audience, expiry, recent issuance, nonce, authorized party when present/required, and access-token hash when supplied. Callback errors are generic and redirect only to the configured origin. Callback responses are not cacheable and send no-referrer. Avoid recording query strings for the callback at a reverse proxy, since authorization codes arrive there before the redirect strips them.

Start/complete operations retain the common same-origin POST checks and authentication rate limits. The narrowly scoped GET callback uses state/cookie correlation and peer rate limits instead of an Origin check. Linking, sign-in, and unlinking produce account audit history and existing organization audit/notification events. No external notifications were sent during implementation testing.

## Libraries and evidence

The implementation uses [coreos/go-oidc v3](https://github.com/coreos/go-oidc) for discovery and signed ID-token verification and [golang.org/x/oauth2](https://pkg.go.dev/golang.org/x/oauth2) for authorization-code exchange and PKCE. Lockfiles pin go-oidc v3.21.0 and oauth2 v0.36.0; provider SDK tests cover the shared oauth2 update too.

Tests use real RSA signatures and JWKS over an injected local transport. PostgreSQL integration checks explicit linking, no email/group auto-grants, PKCE, state/browser/nonce binding, issuer/audience/azp/expiry/signature rejection, callback replay, local TOTP, disabled users, unlinking with an old retained proof, and revocation of the linking session. Browser tests verify shared linking and MFA forms with mocked IdP redirects. No real identity-provider credentials or accounts were used.

Open requirements include multiple IdPs, invitation/federation-only enrollment policy, reviewed IdP MFA assertion mapping, trusted group-to-role mappings, SAML, SCIM, back-channel logout, and external IdP deprovisioning. Disabling a user in an external IdP does not automatically revoke an existing console session in this implementation; console membership/user revocation remains authoritative. Additional identity integrations are not represented as supported.
