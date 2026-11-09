# Native OTLP HTTP dashboard

This template compares the reference blueprint's Kubernetes-enriched and SDK-only HTTP services.
It uses their shared service identity and the deployment environment on `target_info`, so the
SDK-only service does not need Kubernetes labels to appear.

The current stable image includes the generator and this template. Follow the
[Docker generation guide](../../../docs/tools.md#synthkit-dash-grafana-dashboard-generator), which
selects the canonical image reference from `.env.example` and writes resources through an output
mount. Older images may contain the `otlp-native` blueprint without this template. Grafana Explore in the
[Docker Quick Start](../../../docs/quickstart.md) verifies the emitted signals without requiring
a generated dashboard.

As an optional alternative, a source development checkout can generate the resources offline
with no tenant credential:

```bash
go run ./cmd/synthkit-dash \
  -blueprint blueprints/otlp-native.yaml \
  -datasource prometheus='<Prometheus datasource UID>' \
  -namespace demo \
  -out /tmp/otlp-native-dashboards
```

The native HTTP view is `demo-otlp-native-http.json`. The command also writes the folder, thin
index, existing remote-write metrics view and complete panel inventory. Omitting `-namespace`
keeps the default resource identities. The datasource value is the Prometheus datasource's
resource UID in the destination, not its display label, URL or credential. Copy the UID from
its Grafana settings or `/connections/datasources/edit/<uid>` URL; see
[datasource bindings](../../../docs/tools.md#find-the-datasource-uids).
Follow the shared
[generation and verification procedure](../README.md) to validate, publish and record observations;
generation itself does not contact Grafana or publish anything.

## Signal coverage

The query contract comes from the captured
[native HTTP gateway projection](../../../signals/otlp-metrics.md#expected-post-gateway-prometheus-shape-slug-otlp-gateway-prom).

| View | Meaning |
| --- | --- |
| Request rate | Rate of the projected duration histogram's cumulative `_count`, grouped by service. |
| Server error share | HTTP 5xx count rate divided by total request rate. The reference producer emits 200 and 500 status values. |
| Request duration | p50, p95 and p99 from the projected classic `_bucket` series, retaining `le` and displaying seconds. OTLP transport does not imply a Prometheus native histogram. |
| Active requests | Sum of the projected non-monotonic UpDownCounter, queried as a gauge. |

Generation checks that each declared web service actually emits both native HTTP instruments and
the required resource identity, including `service.namespace` and `service.instance.id`. A
disabled native lane refuses generation before writing output.

The August 31 same-source dual-ingest capture supersedes the broader June promotion list.
Ordinary native application series carry `service_name` and the measured five-attribute promoted
set, plus standard `job` and `instance`. Deployment environment remains on `target_info`. Every
HTTP query intersects its per-instance data with environment-scoped `target_info` using
`and on (job, instance)` before aggregation. Standard conversion makes `job` from
`service.namespace/service.name` and `instance` from `service.instance.id`. Matching resource
inventory must retain `service_name` and `deployment_environment_name`; deployments sharing
service names must have distinct instance identity. A receiver with a different projection needs
a matching template. Missing `target_info` produces no result, with no unscoped fallback.

Rates and quantiles use an explicit `[5m]` window to cover the reference blueprint's one-minute
metric cadence. They do not depend on a datasource's default scrape interval or substitute a
shorter Grafana rate macro.

Missing samples remain missing. The dashboard does not fill gaps with zero or claim successful
delivery from an empty result. Allow several metric collection intervals before evaluating rate
or quantile queries. An error share of zero requires observed counter data; a newly observed
counter's first value does not establish activity before its first observation.

Native addon metrics, logs, traces and backend-derived spanmetrics are outside this HTTP view's
coverage. `target_info` supplies identity filtering rather than a separate inventory panel. The
separately generated remote-write metrics view retains its own inventory and existing verification
contract.

## Offline query proof

The normal CLI tests cover datasource binding, deterministic output, independent namespaces,
inventory identity and refusal when the native lane is disabled. The opt-in query proof additionally
requires `promtool` on `PATH` and fails if it is absent:

```bash
go test -tags=integration ./cmd/synthkit-dash \
  -run '^TestOTLPNativeQueriesAgainstEmittedHTTPMetrics$' -count=1
```

That bounded fixture loads the actual blueprint and decodes emitted OTLP over a local HTTP
receiver. It projects the two HTTP instruments according to the retained gateway evidence, then
executes every literal HTTP panel query in the real Prometheus engine against those points.
Ordinary series follow the measured August promotion set; full resource identity becomes
`target_info` at the actual observed ticks. Numeric expectations come from emitted counts, bucket
bounds and concurrency. A foreign deployment with the same service names and different
environment/instance identity is excluded from every query. Removing `target_info` while retaining
ordinary samples yields empty results. Empty input and parser errors are checked by the same
engine; a local datasource-resolution response exercises the missing-datasource classification
through the public verifier.

This establishes sourced projection and query correctness for the new HTTP template. It does not
establish a live gateway's configuration, browser rendering or query coverage of the unrelated
remote-write inventory dashboard. Deployed verification still needs actual observations from the
selected datasource using the generated panel inventory.
