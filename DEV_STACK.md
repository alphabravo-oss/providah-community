# Local development stack

Start the built console and its local test services:

```sh
make up-fast
make dev-stack
make test-dev-stack
```

| Service | Open locally | Container address |
|---|---|---|
| Providah | http://localhost:8760 | app:8080 |
| RustFS console | http://localhost:19001 | rustfs:9001 |
| RustFS S3 API | http://localhost:19000 | rustfs:9000 |
| Mailpit inbox | http://localhost:18025 | mailpit:8025 |
| Mailpit SMTP | localhost:11025 | mailpit:1025 |
| Webhookie | http://localhost:18080 | webhookie:8080 |

All published ports bind to 127.0.0.1. RustFS, Mailpit and Webhookie have separate persistent named volumes. Images are pinned by digest in compose.dev.yaml. Webhookie's one-shot volume initializer grants its non-root process ownership of its own data directory. No application or database volume is modified by that initializer.

RustFS credentials are generated once in `.local/dev-stack.env` (mode 0600); use its `RUSTFS_ACCESS_KEY` and `RUSTFS_SECRET_KEY` to log in to the console. They are not the Providah administrator login. The SDK smoke test creates a uniquely named temporary bucket, checks object roundtrip and removes its fixtures. The overlay configures new source bundles and managed state versions to use encrypted, version-pinned RustFS objects. Existing database state remains readable. See [ARTIFACT_STORAGE.md](ARTIFACT_STORAGE.md) for the envelope, access and recovery boundaries.

Private `.local/artifact-store.json` and `.local/artifact-store.env` files are generated from the RustFS credentials. Compose reads the env file for the non-root app; both host files stay mode 0600. The development overlay enables `DEV_CAPTURE=true` and installation SMTP through Mailpit. Capture mode requires a local HTTP console origin and cannot run with secure production cookies. Only the exact `http://webhookie:8080/hooks/generic/default` webhook endpoint and unauthenticated `mailpit:1025` SMTP destination receive a local-network exception. Other destinations retain normal HTTPS/TLS, public-address and control-plane protections. Disabling capture mode closes those exceptions even for saved configurations. Webhook signing and recipient/sender verification remain active.

In **Notifications**, choose installation SMTP for Mailpit, or use the exact Webhookie endpoint above with a Standard Webhooks signing secret. Open the capture service to retrieve verification codes. The page shows links when local capture mode is enabled.

`make test-dev-stack` uses the optional seeded account from `.local/seed-admin.json`. It creates or reuses **Development testing**, adds dedicated Mailpit/Webhookie destinations, verifies them through the real captured challenges and checks queued delivery. It sends only to the local captures, retains the visible test messages and signs out. It does not create cloud connections or resources. An account that requires MFA must use the browser flow instead.

`make dev-stack` remembers the selection in `.local/dev-stack.enabled`, so subsequent `make up-fast` deployments keep capture mode. `make dev-stack-stop` stops the test services and restores normal outbound behavior without deleting their volumes. To edit a source-development server manually, supply the same exact Compose-side capture hosts or keep normal outbound validation enabled; arbitrary hostnames/ports are not a development exception.

Upstream configuration: [RustFS Docker](https://docs.rustfs.com/en/installation/container/docker), [Mailpit Docker](https://mailpit.axllent.org/docs/install/docker/), [Webhookie](https://github.com/alphabravo-oss/webhookie).
