# Application telemetry

This monitors Providah itself. Provider resource histories remain a separate feature documented in [METRICS.md](METRICS.md).

## Collection

Set `METRICS_ADDR` on the core process to enable a separate HTTP listener, for example `127.0.0.1:9090`. Scrape `GET /metrics`. The main console listener never registers the metrics handler. The optional listener has no application authentication: bind it to loopback or an isolated monitoring network and restrict access with deployment network policy. It is not intended for public ingress. Compose does not publish a metrics port by default; a container monitoring deployment must explicitly configure its listener/network.

The endpoint uses the existing Prometheus Go client with a private registry per core process, bounded concurrent scrapes, and Go/process collectors. It does not require a hosted monitoring service. Each replica has its own request/process metrics; work snapshots reflect the shared database, so do not sum backlog gauges across replicas.

| Metric | Meaning |
| --- | --- |
| `providah_http_request_seconds` | Duration histogram, with bounded route, method and HTTP status labels. Includes `_count`, `_sum` and `_bucket` series. SSE records stream lifetime on disconnect. |
| `providah_http_active_requests` | Current requests, including long-lived SSE streams. |
| `providah_database_connections` | Application pool acquired, idle, maximum and total connections. |
| `providah_work_items` | Current operations awaiting approval/queued/dispatching/observing/uncertain; queued/running scans; pending/sending/dead notification deliveries. |
| `providah_work_oldest_seconds` | Oldest creation age within those groups, not time spent in a status. |
| `providah_work_snapshot_timestamp_seconds` | Last successful durable-work snapshot, zero before first success. |
| `providah_work_snapshot_failures_total` | Failed snapshots; old values remain until collection succeeds. |

When enabled, a three-second-bounded database aggregation runs every fifteen seconds. Empty groups report zero. The snapshot timestamp distinguishes a genuinely empty queue from stale data. Gauges update individually; a scrape concurrent with refresh can see adjacent snapshot values. Database aggregates may need incremental rollups at high job volume; no scale claim is made from functional tests.

Useful alerts include uncertain operations above zero, old queued work, dead notifications, a snapshot older than a minute, sustained pool saturation, and elevated HTTP error ratio. Choose thresholds for the installation. An old awaiting-approval item is not the same as a stuck dispatcher; filter by status. Exclude `/api/events` from ordinary HTTP latency objectives because its duration is the live-stream lifetime.

## Logs and correlation

Core HTTP completion logs use the existing zerolog JSON logger and contain a generated request ID, bounded route/method, HTTP status and duration. The same ID is returned as `X-Request-ID`; supplied IDs are ignored. Health/readiness requests still count in metrics but omit completion logs.

Only generated protobuf service method names and a small explicit set of infrastructure paths are retained; other URLs use `other`. Queries, request/response bodies, cookies, authorization, caller-supplied IDs, raw paths and database arguments are excluded from this instrumentation. There are no organization, email, resource or credential labels. These HTTP logs supplement the durable audit trail, not replace it.

## Verification and remaining scope

Unit checks verify input redaction, locally generated IDs, HTTP status metrics, bounded route names, active-request cleanup and preservation of the flushing interface used by SSE. PostgreSQL checks verify successful collection, pool gauges, retained snapshots after failure and absent credential/user values. Existing browser/SSE flows run through the instrumented handler.

The initial metrics chunk did not implement tracing. HTTP OTLP export is now available as described below. Cross-process traces and operation/run/delivery correlation, provider throttling counters, and scheduler/audit/transfer-specific telemetry remain requirements in the supported feature documentation; HTTP and queue instrumentation alone does not close the production observability gate.

## HTTP OTLP tracing

Set `TRACE_ENDPOINT` to the full collector trace URL, such as `https://collector.example.com/v1/traces`. HTTP is accepted only for a literal loopback IP for local collectors. Set `TRACE_SAMPLE_RATIO` between 0 and 1; the default is 0.1. Leave the endpoint unset to disable export. This configuration is separate from the metrics listener.

The OpenTelemetry Go SDK/exporter is pinned at v1.46.0 and uses OTLP/HTTP protobuf. It batches up to 128 spans, keeps at most 1,024 queued spans, and exports every five seconds or when a batch fills. Export attempts have a three-second timeout and no retries; a full queue drops traces rather than block application requests. Shutdown has a five-second drain deadline. Collector outages must not prevent serving requests.

Spans are local server roots with fixed service name `providah`, bounded route/method, HTTP response status, and the generated request ID. HTTP 5xx spans are marked as errors with a generic description. Completion logs include the trace ID for correlation. No remote parent, baggage, URL/query, header, body, error text, database arguments, host/user resource detector, or cloud credentials are exported. Exporter failures are reduced to a generic message before SDK diagnostics.

The endpoint and sampling configuration are operator-controlled. System TLS trust is used; proxy use and redirects are disabled, and collector responses are limited to 1 MiB. Collector authentication and custom CA injection are not yet supported directly; use a locally managed collector to forward securely to the customer backend. Do not put credentials in the endpoint URL. This does not enable tracing inside provider workers or connect a later durable job to its initiating request.

A local HTTP collector test decodes real OTLP protobuf, verifies exported span/status shape, no inbound parent adoption, redaction, and shutdown flushing. No customer collector has been contacted. See the [official Go exporter documentation](https://opentelemetry.io/docs/languages/go/exporters/).

## Optional local monitoring stack

After `make up-fast`, run `make observe`. Open [local Grafana](http://localhost:8761) and sign in with the development-only `admin` / `admin` account. The **Providah operations** dashboard is provisioned automatically; Explore has Prometheus, Tempo, and Loki data sources. Run `python3 observability/verify.py` to check a real metrics scrape, an HTTP trace, its matching request log, and dashboard provisioning. Initial trace search can take around a minute.

`compose.observability.yaml` adds the pinned Grafana LGTM development image, which packages the required backends. Its configuration and dashboard live under `observability/`; no manual datasource setup is needed. The sidecar shares the app's network namespace so OTLP and scrape endpoints stay on loopback. Only Grafana's UI port is additionally published, bound to `127.0.0.1`. Traces use full sampling in this development profile. No cloud telemetry backend is used.

The development app writes JSON to stderr and to `DEV_LOG_FILE`. The maintained Lumberjack writer rotates the optional file at 10 MiB, with two backups and one-day age retention. The collector tails that file once; it does not also scrape container stderr. The log volume is read-only in the monitoring container. No Docker socket or installation secrets are mounted there. Standard deployments keep their existing stderr shipper and should leave `DEV_LOG_FILE` unset.

`make observe-stop` stops monitoring and restores the normal app configuration. Use it before `make up-fast` or other app recreation, then run `make observe` again afterward: sharing a network namespace requires recreating the sidecar when the app container changes. The stack is optional and intended for local development, with default Grafana credentials and root inside the monitoring container. It is not a production monitoring deployment. Do not expose port 8761 beyond localhost.

Backend storage is disposable on container replacement; this profile does not claim durable monitoring history. Prometheus retains at most 24 hours. The development log volume is retained when stopping. Avoid reading secrets into logs; request instrumentation explicitly omits bodies, headers and raw URLs, but this does not replace review of future log statements.

The profile follows the guide's Grafana/Prometheus/Tempo/Loki development-stack requirement using the [prebuilt Grafana development image](https://github.com/grafana/docker-otel-lgtm/tree/v0.32.1). Adapted upstream collector/datasource configuration retains its license in `observability/LICENSE.grafana-lgtm`. The existing metrics implementation uses the Prometheus client directly; migration to the guide's OTel meter/exporter integration remains open, as do production tracing coverage and deployment hardening.


## Database storage budget

Global administrators can view database allocation and the included audit-history allocation at `/admin/health`. PostgreSQL's [size functions](https://www.postgresql.org/docs/17/functions-admin.html#FUNCTIONS-ADMIN-DBSIZE) supply these values; audit allocation includes both organization and installation audit tables, their indexes and TOAST data. The API requires global administration and returns an error if the current measurement fails.

Set `DATABASE_BUDGET_BYTES` to a nonnegative integer in deployment configuration (for example `10737418240` for 10 GiB). Zero or unset means no budget; invalid/negative values fail startup. Compose already passes the private `.env` to the app. Restart the app after changing it. This setting is an advisory allocation budget, not a database quota, free-space measurement or automatic deletion policy. Choose it below available capacity, allowing for PostgreSQL WAL, other databases, backups, growth and operational headroom. External RustFS/S3 allocation and host disk free space require their own monitoring.

The shared telemetry loop measures storage every 15 seconds with a three-second query deadline. Metrics have no tenant/resource labels:

- `providah_database_storage_bytes{kind="used|audit|budget"}` (audit is included in used).
- `providah_database_storage_snapshot_timestamp_seconds`.
- `providah_database_storage_snapshot_failures_total`.

Failed measurements retain the last successful gauges and do not advance the timestamp. Work-queue measurements remain independent. File-size accounting can become expensive on very large databases; adjust the sampling cadence if measurement timeouts appear.

The optional observability overlay loads `observability/alerts.yaml`: warning at 80–90% for five minutes, critical at 90% or above for two minutes, and stale measurement after two minutes of staleness persisting for two minutes. Budget alerts require a positive budget and a sample newer than two minutes. Unconfigured budgets never imply sufficient capacity. Missing/down scrape targets still need the deployment's normal availability alert. Rules appear in Prometheus; outbound routing requires the operator's Alertmanager configuration and is not connected automatically to organization destinations.

Offline rule check using the pinned monitoring image already used by the overlay:

```sh
docker run --rm --network none -v "$PWD/observability:/rules:ro" -w /rules --entrypoint /otel-lgtm/prometheus/promtool grafana/otel-lgtm:0.32.1@sha256:7fd8eaad6bb64897ad5f644c8e15ee67c3204c97168f4bdba122adbf8f60e3c4 test rules alerts.test.yaml
```

Do not remove unexported audit history to clear a capacity warning. Export backlog remains visible independently; retention, object-store capacity and offboarding policies are still separate unfinished work.
