# AWS connection authentication

AWS connection credentials remain encrypted and write-only. The connection and rotation dialogs use shared TanStack forms with masked key/token fields, optional role ARN, external ID, and expected account ID. Rotation starts empty because stored credentials cannot be read back; re-enter the complete desired configuration. API clients may supply the equivalent JSON:

```json
{
  "access_key_id": "SOURCE_ACCESS_KEY",
  "secret_access_key": "SOURCE_SECRET_KEY",
  "session_token": "OPTIONAL_SOURCE_SESSION_TOKEN",
  "role_arn": "arn:aws:iam::123456789012:role/Providah",
  "external_id": "OPTIONAL_TRUST_POLICY_EXTERNAL_ID",
  "account_id": "123456789012"
}
```

Omit optional fields instead of filling placeholders. Without `role_arn`, the supplied keys are used directly. `account_id` optionally pins direct credentials to an AWS account. With a role, its ARN determines the required account; an explicit account ID must agree.

The core uses the official AWS SDK v2 STS credential provider to assume the role for 15 minutes, then calls GetCallerIdentity with those temporary credentials to verify the account. Only normalized access key, secret key, and session token are sent to the isolated provider worker. Source keys and external ID are not sent to the worker for role connections. An exchange failure never falls back to source permissions. The source identity needs permission to assume the role, and the role trust policy must allow it and match the external ID when required.

Each job obtains credentials afresh. Brokering has a 30-second timeout, no automatic retries, and the existing public HTTPS destination guard. Discovery and power operations both use the broker. A pre-dispatch authentication failure fails the operation without calling the provider; an observation authentication failure leaves its external outcome uncertain.

Commercial, China, and GovCloud role partitions must match the selected region. There is no ambient profile, environment credential chain, instance metadata identity, custom STS endpoint, interactive AWS MFA, or automatic source session renewal. Temporary source credentials must be rotated before expiry.

Verification uses the real SDK with local HTTP transport fixtures: source/role signing, external ID, duration, account mismatch, denied exchange, partition mismatch, strict field validation, and no-network direct credentials. No live AWS account has been tested. Deployment-identity credential sources remain pending. Browser coverage verifies guided creation, empty rotation fields, and replacement without exposing credentials in the connection list.
