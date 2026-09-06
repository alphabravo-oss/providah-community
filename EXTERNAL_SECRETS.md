# External cloud credentials

Connections can use either the existing age-encrypted credential or a pinned OpenBao / Vault-compatible KV v2 reference. The shared connection and replacement modals offer external references when the installation has a store configured. Only the source type is returned to the browser; reference paths and secret values are write-only.

## Deployment baseline

The integration uses the OpenBao Go API SDK v2.6.0. This first implementation supports KV v2 string fields and an operator-provided static access token. Configure the core application only:

| Setting | Meaning |
| --- | --- |
| `EXTERNAL_VAULT_ADDR` | Required HTTPS origin, optionally with a port such as 8200; no path, user information, query, or fragment. |
| `EXTERNAL_VAULT_MOUNT` | Required KV v2 mount, for example `secret`. |
| `EXTERNAL_VAULT_NAMESPACE` | Optional namespace, where supported by the server. |
| `EXTERNAL_VAULT_TOKEN_FILE` | Preferred mounted token file; readable by the app, never mounted into provider workers. |
| `EXTERNAL_VAULT_TOKEN` | Alternative direct token setting. Do not configure both token inputs. |
| `EXTERNAL_VAULT_CA_FILE` | Optional mounted CA certificate bundle for a private CA. Otherwise use system trust. |

The supplied Compose application reads its `.env`; mounted token/CA files require deployment volume overrides. Preserve existing installation encryption and session keys. No Vault settings are needed for built-in credentials or encryption maintenance commands.

Grant the deployment token read access only to the required KV v2 data paths below. Do not grant write, destroy, metadata-reset, or broad administrative rights. Token issuance, expiry, renewal, revocation, and file rotation are external deployment responsibilities; restart the application after replacing its token. Dynamic secret engines, AppRole/Kubernetes login, and automatic lease/token renewal are not implemented. Expired/revoked tokens fail closed.

The endpoint is selected by the installation operator, not by connection managers. Private network endpoints are supported. TLS verification is mandatory; ambient Vault/OpenBao configuration, proxies, redirects, and SDK retries are disabled. Reads have a 15-second deadline and 64 KiB response limit. Store initialization does not contact the server, so an outage does not prevent local startup.

## Reference and scope

Given organization ID `ORG_ID`, provider `hetzner`, mount `secret`, and relative path `production`, put a string field named `credential` at:

```text
secret / providah/ORG_ID/hetzner/production
```

The KV v2 API read is `/v1/secret/data/providah/ORG_ID/hetzner/production?version=2` when version 2 is selected. Use the actual organization ID shown by the API. AWS fields contain the same strict credential JSON documented in [AWS_AUTH.md](AWS_AUTH.md); DigitalOcean and Hetzner fields contain their API token. No credentials are supplied by this example.

The API accepts a credential string with this representation:

```text
vault-kv2:{"path":"production","key":"credential","version":2}
```

The core derives organization/provider prefixes, validates path segments, and adds a binding to the installation's endpoint, namespace, and mount before encrypting the reference. Connection managers cannot change these prefixes or select an arbitrary endpoint. A store configuration change requires replacing the reference; rotating only the deployment access token does not change this binding.

Versions must be explicit positive integers. There is no latest-version lookup or fallback. Missing, deleted, destroyed, mismatched, oversized, nested-reference, and non-string values fail. Changing the selected version uses the existing credential replacement flow, increments connection revision, and fences queued work from using an unreviewed credential revision.

KV version numbers rely on the external store's integrity. A Vault administrator capable of deleting/recreating metadata or restoring a different store at the same address can reuse a version number. This implementation does not provide a content digest or independent attestation of external secret contents. Restrict those privileges and treat store restore/replacement as a credential rotation event.

## Execution and outage behavior

All provider calls resolve through the core's existing credential boundary, including discovery, provisioning, power/deletion work, previews, and metrics. AWS references then pass through the existing STS/account verification broker. Workers receive the resolved provider credential only, never the Vault token or reference. Secrets exist transiently in process/request memory; no resolved value is persisted or cached between calls.

A failed Vault read prevents that provider call. Existing operation state handling and inventory preservation apply; unavailable secrets do not authorize retrying an uncertain cloud mutation. Read-only observation also requires a resolvable credential. Replace or repair the reference/store access to restore observation. External error bodies and secret values are not returned to the browser.

Existing backups and age-key rotation cover encrypted references, not the external secret contents. Back up and recover the external store separately. Do not retire a referenced version while work may need it for observation.

## Verification and limits

Automated checks use the actual OpenBao SDK against local HTTP fixtures to verify exact version/path/header scope, environment isolation, no cache/retry/redirect, reference validation, store binding, unavailable and deleted versions, and error redaction. PostgreSQL integration checks cover API metadata/redaction, encrypted reference storage, worker resolution, outage behavior, and credential revision changes. Browser checks cover shared conditional fields and serialized reference submission.

No live OpenBao or HashiCorp Vault server compatibility matrix, HA failover, real namespace deployment, or live cloud credential has been verified. Those release gates remain open; this is a bounded KV v2 integration, not a claim of complete Vault support.
