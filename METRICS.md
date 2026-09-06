# Provider-reported server metrics

Baseline: 2026-09-05. Resource details expose an on-demand history view through the existing provider worker boundary. This is provider telemetry, separate from Providah system observability. It does not install an agent, change monitoring settings, or enable paid detailed monitoring.

## Implemented measurements

| Provider | SDK and API | Series | Units and interpretation |
|---|---|---|---|
| AWS | AWS SDK for Go v2 CloudWatch `GetMetricStatistics` | CPUUtilization, NetworkIn, NetworkOut | CPU average in percent; network sum in bytes per five-minute period across all interfaces |
| Hetzner Cloud | hcloud-go/v2 `Server.GetMetrics` | CPU; public network receive/send; local disk read/write bandwidth and IOPS | Provider CPU percent, bytes/second, and IOPS; reported sample interval is retained |
| DigitalOcean | godo `Monitoring.GetDropletBandwidth` | Public/private inbound/outbound bandwidth | Provider-reported Mbps; sampling interval is provider-defined |

AWS requests use namespace AWS/EC2, the exact inventoried InstanceId, region, and a 300-second period. Credentials need `cloudwatch:GetMetricStatistics` in addition to the permissions for other enabled workflows. [AWS metric definitions](https://docs.aws.amazon.com/AWSEC2/latest/UserGuide/viewing_metrics_with_cloudwatch.html) describe the distinction between percentage averages and network sums. CloudWatch API usage may incur provider charges.

DigitalOcean calls require `monitoring:read`. Returned host/interface/direction labels must match the exact request; extra or mismatched series are rejected. Its bandwidth API returns Mbps, so these values are not relabeled as bytes/second. See [DigitalOcean Monitoring API](https://docs.digitalocean.com/reference/api/reference/monitoring/). CPU, memory, and filesystem measurements from agent-dependent APIs are not included in this pass.

Hetzner requests CPU, network, and disk together at a requested 300-second step and retains the returned step. Missing series remain empty. Public-network and local-disk series are not represented as metrics for every private interface or attached volume. See [Hetzner Cloud API](https://docs.hetzner.cloud/reference/cloud).

## Scope and boundaries

The UI offers one, six, or twenty-four hours. The server resolves the range, ending at the most recent five-minute boundary. Provider publication can lag that endpoint. The browser cannot submit arbitrary providers, credentials, native IDs, endpoints, or metric expressions; it supplies an authorized inventory resource ID and the bounded history choice.

An enabled server connection, enabled module, current resource-read permission, and current organization sign-in policy are required. Each read pins its credential revision and provider runtime/module revision. The core releases database locks before authenticating and calling the worker. It rechecks the session, resource, permission, sign-in policy, credential revision, and module revision before returning data. Revoked authority or changed credentials discard an in-flight result.

Metrics use an explicit optional runtime capability. Core and launcher refuse metrics for older or unsupported runtimes, while retaining their existing discovery and operation behavior. The worker receives a typed read-only metrics request through `/metrics`, with credentials in the existing stdin channel. Metrics, discovery, and mutation request/response shapes cannot be mixed.

The AWS integration reuses the existing brokered temporary credentials and SDK credential configuration. CloudWatch v1.71.0 is pinned alongside the existing EC2, godo, and hcloud SDKs. Discovery/metric/power clients continue to use provider SDK serialization and signing; there is no hand-written cloud API client.

## Data handling and UI

Responses are bounded to sixteen series, two thousand points per series, and twelve thousand points total. Each upstream HTTP response is limited to 2 MiB. Finite nonnegative values, ascending unique timestamps, labels, and requested time bounds are validated. Non-numeric missing samples represented by NaN are omitted where supported. Missing samples are never replaced by zeros; a reported zero is retained. Invalid/truncated/error responses produce a generic unavailable message without provider error bodies or credentials.

There is a shared limit of ten metric reads per minute per connection, in addition to launcher concurrency and provider quotas. No background metrics collection or permanent time-series store was added. Each read can make three CloudWatch calls, four DigitalOcean calls, or one Hetzner call. Query caches are scoped to organization/resource/history and display the fetch timestamp.

The shared modal uses TanStack Query, TanStack Form, and a paged TanStack Table. A shared SVG scatter plot shows only actual reported samples with a UTC axis; it does not interpolate across missing time. Exact timestamps, units, aggregation, sample period where known, and latest reported value accompany the plot. Only one series is rendered at a time, with at most one hundred table rows per page.

## Evidence and remaining scope

SDK transport tests exercise actual authenticated requests and decoding, including the current CloudWatch CBOR protocol, unsorted AWS points, DigitalOcean label scoping/Mbps, Hetzner mapping, empty histories, zero values, invalid targets, and redacted failures. Contract tests cover invalid numeric values, duplicate timestamps, request ranges, mixed response types, and legacy runtime refusal. PostgreSQL checks cover successful reads, empty data, in-flight permission revocation, credential changes, malformed provider ranges, and unsupported runtime behavior. Browser coverage includes plots, sample tables, history selection, and explicit missing-data messaging using local fixtures.

Live-account validation remains pending. DigitalOcean CPU/agent-backed measurements, additional AWS/Hetzner metrics, managed-service metric groups, metric alerting, and the console's Prometheus/OpenTelemetry system observability are not completed by this chunk. No full monitoring-platform or complete provider-coverage claim is made.
