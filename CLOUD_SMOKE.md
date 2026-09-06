# Read-only cloud smoke test

Start the local Community console with `make up-fast`. Copy `.env.cloud.example` to `.env.cloud`, then run `chmod 600 .env.cloud`. Never put temporary provider keys into the application's deployment `.env`.

Fill in any or all of:

- Hetzner: `HCLOUD_TOKEN` with read-only project access.
- DigitalOcean: `DIGITALOCEAN_TOKEN` with read permissions for the inventory you want to discover.
- AWS: `AWS_ACCESS_KEY_ID`, `AWS_SECRET_ACCESS_KEY`, `AWS_SESSION_TOKEN` when using temporary credentials, and an explicit `AWS_REGION`. `AWS_EXPECTED_ACCOUNT_ID` enables account verification through the existing broker.

Set `PROVIDAH_ORGANIZATION_ID` if the administrator belongs to multiple organizations. The default login comes from the private `.local/seed-admin.json`; `PROVIDAH_LOGIN_FILE` can select another private JSON file with `email`, `password`, `mfa_enabled` and, when MFA is enabled, `totp_secret`.

```sh
python3 scripts/cloud_smoke.py --check
make test-cloud
```

For credentials stored outside the checkout, pass `--env /path/to/.env.cloud`. Use `--url`, `--login-file` and `--output` to target another local installation and keep its login and report separate.

The first command validates configuration without network calls. The second logs in through the normal API, creates a uniquely named connection per configured provider, waits for discovery through the credential broker and isolated worker, and verifies inventory is readable. Empty accounts can pass with zero resources. Provider permissions must cover the runtime's advertised inventory; partial or failed discovery is a failure, not a successful empty result.

No cloud create, power, resize, snapshot, tag or delete operation is requested. Each smoke connection is disabled in a cleanup step to stop recurring discovery. Its inventory and encrypted credential remain in the local database for inspection; revoke the temporary cloud keys when finished. If interrupted or cleanup fails, disable the `cloud-smoke-*` connections in the console before leaving the installation running.

Results contain provider names, counts and local connection IDs in `.local/cloud-smoke.json`. Credentials are not printed or placed in command-line arguments. `.env.cloud` and `.local` are excluded from Git and Docker build contexts. The test accepts only a loopback console URL and rejects redirects and broadly readable credential files.

This establishes the read-only connection/discovery path. Cloud lifecycle testing is a separate step using explicitly selected disposable resources.
