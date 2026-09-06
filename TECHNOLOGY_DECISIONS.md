# Technology decisions

Reviewed against the [technology selection guide](https://technology-selection-guide.aws.ablabs.io/) on 2026-09-06. This records the implemented foundation and explicit deviations; it is not a claim of full guide or production compliance.

## Shared foundation

- Go, chi and Connect RPC. Protobuf definitions are in `proto/`; generated Go handlers/messages are in `internal/gen/`, and TypeScript messages/clients in `web/src/gen/`.
- PostgreSQL 17, pgx and sqlc. Annotated queries live in `internal/database/queries/`; generated Go is checked in. Embedded goose migrations run under an advisory lock before serving.
- React, strict TypeScript, Vite, Tailwind, Radix, TanStack Query and generated Connect clients.
- Structured zerolog logs, Prometheus and OpenTelemetry. HTTP trace context is propagated without baggage, queued scans/operations retain trace parents, and provider execution plus PostgreSQL queries produce bounded spans without request bodies, credentials or SQL parameters.
- Age encryption uses a common envelope with key ID, algorithm and ciphertext across encrypted fields. Normal reads select the matching key. Only offline rotation accepts the previous raw-age format; the live application has no trial-decrypt compatibility fallback.
- The application filters worker capabilities before use; worker capability claims cannot enable unsupported operations.
- CI checks builds, existing race-enabled Go tests, generated-code drift, linting, dependency vulnerabilities and secret history. Native container provenance is used by BuildKit; runtime images carry source/revision/version/edition labels. Do not describe an unpublished image as a signed release.

## Scoped decisions

| Guide prescription | Current decision and evidence | Revisit trigger |
|---|---|---|
| React Hook Form | Retain TanStack Form with Zod. Existing forms use it consistently; replacing it adds migration work without a demonstrated benefit. | A concrete validation, accessibility or performance limitation. |
| OTLP gRPC | Retain bounded OTLP HTTP export, with endpoint validation and redacted exporter errors. Existing export checks exercise it. | Collector compatibility or a measured transport limitation. |
| Four local observability containers | Retain the optional combined LGTM development image and existing provisioning. Production topology is a separate deployment decision. | Independent retention, scaling or operational ownership is required. |
| Testcontainers/pgtestdb, gotestsum and broader browser harness | Retain existing Compose-backed disposable PostgreSQL tests, Go tests and Playwright. Build/type checks and focused enforcement regressions remain required. | CI isolation or reporting requirements cannot be met with the existing commands. |
| Broad default lint suite | Start with golangci-lint's standard suite, including errcheck, govet, ineffassign, staticcheck and unused. Integration build tags include test helpers. ST1005 is excluded only in listed files that deliberately expose complete user-facing sentences. | Expand the suite when it identifies actionable defects without forcing unrelated stylistic rewrites. |
| Security scan gating | Gosec gates high-severity findings; govulncheck gates reachable vulnerabilities, npm audit gates high/critical findings, and Gitleaks gates secret history. Generated outputs are regenerated/drift-checked rather than treated as handwritten scanner findings. Specific gosec trust-boundary/bounds exceptions and four synthetic Gitleaks fixtures are annotated at their exact lines. Lower-severity scanner findings require separate review before production qualification. | Production release review or a changed trust boundary. |
| Development host binding and `/ws` proxy | Keep loopback-bound development services. The application uses SSE for events, so no unused WebSocket proxy is added. | A real remote-development or WebSocket requirement. |
| Distroless/nonroot for every executable | Application, provider and egress images use nonroot distroless. The launcher uses a pinned Docker CLI image and the Docker socket; it is a privileged deployment component, not an untrusted tenant extension. Existing worker isolation and approval checks remain authoritative. | A deployment needs a separate execution host or a runtime without Docker socket access. |
| Full domain package decomposition | Keep startup and worker construction small and explicit. Shared implementation remains internal; no exported service/database object or plugin framework is introduced. | A concrete application domain needs a narrow composition boundary. |
| Uniform encryption columns | Store the common metadata envelope inside existing ciphertext columns, reusing one encryption path. Connection metadata remains populated for its existing schema. Avoid duplicating metadata columns across eleven tables. | SQL-based key reporting or resumable online rotation becomes necessary. |
| Air-gap and FIPS profiles | Cloud API connectivity remains required for cloud operations. No air-gap, HA or FIPS qualification is claimed. | A defined deployment profile has testable requirements and operational evidence. |

Stable operation and scan statuses already use protobuf enums; remaining fixed API string categories need targeted conversion when those APIs change. Extensible cloud catalogs must not be frozen into arbitrary closed enums.
