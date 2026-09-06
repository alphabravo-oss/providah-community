# Recovery codes

While your authenticator is available, open **Recovery codes** beneath your profile. Enter your current password and a fresh authenticator code, then save the ten displayed codes in a password manager or offline. Each code has 128 bits of random entropy. The server stores only a SHA-256 verifier bound to your account; the displayed codes cannot be retrieved later.

Generating another set atomically invalidates all previous codes. Generation requires a newly consumed TOTP code, even when the session recently completed MFA. No administrator can retrieve another user's codes. The browser displays them only in the open dialog, clears them when dismissed, and does not put them in Query data or persistent browser storage. A lost generation response can mean the old set has already been invalidated; generate a fresh set using your authenticator and save that result.

On the sign-in page, select **Use a recovery code**, and enter your password plus one unused code. Spaces at the ends, letter case, and optional hyphens are normalized. You still need the account password. Login rate limits apply to recovery attempts, including incorrect passwords and used codes.

A successful recovery login atomically consumes the code, revokes that account's existing access/refresh sessions, creates a new session, and records the recovery event. Concurrent submissions of one code permit only one success. If required audit persistence fails, the whole transaction rolls back: the code remains usable and older sessions remain valid.

Recovery login restores ordinary account access under existing RBAC. It does **not** establish recent authenticator verification for sensitive actions. Refreshing the session does not elevate it. An authenticator code remains necessary for credential/access changes, approvals requiring recent MFA, and other sensitive workflows. Recovery codes cannot be used in the **Verify MFA** dialog and never replace the independent approver.

Own-account history shows the 25 most recent account-security events. Events are append-only, persist even without an active organization membership, and are also mirrored into currently accessible organizations' audit logs. Verified organization notification destinations receive corresponding security events through the existing delivery pipeline, with its normal 15-minute deduplication. No codes, passwords, or TOTP seeds appear in these events or messages.

## Scope still required

These are backup sign-in codes, not the complete account-recovery system. TOTP replacement after losing the authenticator, password reset, recovery with neither codes nor authenticator, independent identity verification, SSO recovery policy, and the approved offline administrator procedure remain open production gates. Do not deploy relying on these codes as a way to reset a lost authenticator. Initial setup/invitation enrollment does not yet require users to save codes; users generate them through their profile afterward.

Do not edit TOTP seeds, passwords, or roles directly in the production database as an improvised recovery flow. Preserve the database and deployment encryption key together in your documented backup procedure. Losing that key prevents decryption of credentials and TOTP seeds; recovery codes do not recreate it.

The implemented flow is covered by disposable PostgreSQL tests for concurrent consumption, old-code invalidation, old-session revocation, MFA restrictions across refresh, transactional audit failure, and append-only account history, plus browser generation/hide/sign-in tests. Integration clock adjustments apply only to disposable test users. Production identity-recovery and restore drills are still required.

## Authenticated password changes

The account menu provides a shared TanStack form for the current password, a different new password (12–72 bytes), and a fresh TOTP. A recent session or recovery-code login does not substitute for the TOTP. Password verification, replay prevention, active-session revalidation, password update, all-session revocation, and the `account.password_changed` audit event share the account-change transaction. The password uses the existing bcrypt policy. Existing recovery codes remain valid only with the new password; this flow does not reset the authenticator.

On success both browser cookies are expired and all sessions for the account are deleted, including the caller. Existing SSE authorization checks observe that revocation. The UI clears its query cache and returns to sign-in. The security history and organization audit show the change without password contents; existing notification subscriptions may include `account.password_changed`.

Forgotten-password recovery, authenticator replacement, and offline administrator recovery remain separate unfinished flows.

## Active sessions

Open **Active sessions** beneath your profile to list your own unexpired sessions across the installation. The shared TanStack table shows a short session reference, the current-session marker, local or organization sign-in, last MFA verification and expiry. Device names, IP addresses and creation times are not recorded. Access tokens, refresh verifiers and credentials are never returned. Pagination is bounded to 100 records per page with an account-bound cursor; organization membership does not grant access to another user's sessions.

Revoking a selected session requires your current password and a fresh, single-use authenticator code in the shared modal form. The existing account-change transaction revalidates the caller, consumes the TOTP, deletes only an owned session and records `account.session_revoked`. Audit failure rolls back both deletion and code consumption. Missing and foreign session references return the same unavailable response.

Deletion blocks subsequent authenticated requests and refresh for that session; SSE observes revocation through its existing authorization checks. Already-submitted cloud operations continue under their existing execution rules. Revoking the current session also expires both cookies, clears the browser query cache and returns to sign-in. Other sessions remain valid. The account security history and organization audit record the action without session tokens or proof contents.

Disposable PostgreSQL tests cover pagination, ownership isolation, wrong proof, TOTP replay, audit rollback, immediate request rejection and current-session logout. The browser test revokes a second fixture session through the modal and verifies that the current session remains visible and the security history records the event. Administrator-wide session control, device attribution and authenticator replacement remain unfinished scope.


## Optional MFA and global administrators

MFA is now an account setting. Existing accounts remain MFA-enabled by default; the explicitly seeded development global administrator starts with MFA disabled. Password-only sessions record no authenticator verification. When MFA is disabled, login and account changes accept an empty authenticator field, and sensitive-action checks honor that account setting. Password checks, sessions, current permissions, independent review and audit transactions still apply.

The profile's Enable/Disable MFA modal verifies the password and enrolled authenticator before changing the setting. Enabling requires proof of the existing enrollment; disabling an enabled account also requires its current code. Changes revoke all sessions atomically and record `account.mfa_enabled` or `account.mfa_disabled`. A user without the enrolled authenticator still needs the unfinished authenticator-recovery flow. The seed file preserves the initial test account's enrollment details.

This explicit account-setting change supersedes earlier unconditional MFA requirements in this document. Global administrators have installation-wide authorization; ordinary organization administrators cannot grant or revoke that global authority through membership editing. See the optional local seed instructions in README.md.

Login shows email and password first. After those are accepted, accounts with MFA enabled see a separate authenticator/recovery-code screen. Accounts with MFA disabled proceed directly to the workspace. The MFA prompt itself grants no session; the final submission rechecks the password and current account policy.

## Administrator MFA policies

Global administrators manage **Global MFA policy** from the top-right account menu. Organization administrators with identity.manage use **Sign-in policy → Organization MFA policy**. Both policies default to optional; a global requirement cannot be weakened by an optional organization setting. Optional means users may retain their own enabled authenticator.

A requirement gates affected organization access and future dispatch, including API and SSE authorization. A password-only account may still sign in to manage its own security, but its session lists affected organizations as requiring MFA and cannot use their cloud APIs. Existing submitted cloud actions continue read-only observation. MFA-enrolled recovery-code sign-in remains supported; sensitive actions still require fresh verification. Organization policy is not a claim that an external identity provider supplied an MFA assertion.

The administrator enabling a requirement must already have MFA enabled. Current password and fresh code (when enrolled) are required to change policy. Stale policy edits are rejected. Users cannot disable MFA while a global or currently accessible organization requires it.

MFA-disabled users can choose **Enable MFA** in the account menu, confirm their password, enroll the newly shown authenticator key, and verify its code. This replaces the unused disabled seed; it cannot reset an already-enabled authenticator. Enabling signs out all existing sessions. The key is held only in the enrollment dialog, not browser storage. Lost-enabled-authenticator recovery remains separate work.
