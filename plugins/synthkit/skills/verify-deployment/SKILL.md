---
name: verify-deployment
description: "Verify a synthkit deployment's control plane and declared signals; not generic Grafana queries."
---

# Verify a synthkit deployment

Verify deployment identity and control-plane health, then check only signals that the enabled
blueprints and configured lanes declare. This skill is read-only. Use the bundled instructions
and local checkout docs; a normal signed-in Grafana browser is sufficient for remote readback.
No Grafana query plugin or specialised documentation service is required. Stop before a remote
request when its destination or authorised browser/API access is absent; never solicit or print
a token merely to verify.

## Locate the synthkit checkout

```bash
SYNTHKIT_CHECKOUT="/absolute/path/to/synthkit"
SYNTHKIT_CHECKOUT="$(git -C "$SYNTHKIT_CHECKOUT" rev-parse --show-toplevel)" || exit 1
test -f "$SYNTHKIT_CHECKOUT/AGENTS.md" && \
  test -f "$SYNTHKIT_CHECKOUT/docker-compose.yml" && \
  test -f "$SYNTHKIT_CHECKOUT/.env.example" || exit 1
cd "$SYNTHKIT_CHECKOUT"
```

Resolve documentation and helpers under `$SYNTHKIT_CHECKOUT`, not a harness-specific plugin root
or cache. Ordinary Docker startup needs no host Go, Node.js, `just` or `gcx`. Full identity
verification below additionally needs Python 3.11+ and the existing checkout helper. If that
prerequisite is absent, basic Docker/control-plane/browser observations may still be collected,
but record identity as unverified and full verification as blocked. Do not mark this skill complete
or substitute those observations for its required identity proof.

## 1. Immutable deployment identity

For Compose, require the intended verified immutable reference, expected binary version and full
source revision from that exact artifact's public publication metadata or an approved deployment
record. The committed `.env.example` supplies the approved default reference; the image's
publication/provenance and `$SYNTHKIT_CHECKOUT/docs/deployment.md` supply its identity context.
Do not use the running binary's output as its own expected identity, infer identity from a mutable
tag, or assume the image matches the current checkout. Run the closed read-only cross-check:

```bash
python3 "$SYNTHKIT_CHECKOUT/scripts/synthkit-deploy.py" inspect-running \
  --container "$(docker compose ps -q synthkit)" \
  --expected-reference "ghcr.io/rknightion/synthkit@sha256:<index>" \
  --expected-version "<published-binary-version>" \
  --expected-revision "<40-hex-source-sha>"
```

Require the configured index reference, discovered registry index, selected platform manifest, OCI
config/running image identity, binary version and revision to agree. Retain the helper's closed
classic-Docker config-ID or containerd index-plus-platform-descriptor path; an index digest is not
generally a config digest. `inspect-running` binds the running identity and does not verify a
signature or provenance. Replacement artifacts still require the appropriate artifact verification
in `$SYNTHKIT_CHECKOUT/docs/deployment.md`, including the distinct release versus main-build rules.
Use that publication record's independent OCI label input; its spelling can differ from the Git
tag. Never fill an expected input from the candidate's own observed metadata.
Stop on missing expected inputs or contradictory identity evidence; do not change selectors,
snapshot/restore state or print private deployment records to get past the check.

Docker may retain both index and platform digest aliases for one local image. The helper selects
the explicitly expected index and verifies its registry, platform, config and running binary.
Aliases alone neither establish identity nor invalidate an otherwise matching image. Preserve
evidence on any refusal; do not remove shared cache entries or relax identity checks to pass.

## 2. Preflight context and credentials

1. Read `.env` by presence/shape only; never `cat` it or run `docker compose config`. Confirm
   `DRY_RUN=false` before expecting landing data. Record the declared `TICK_DEFAULT` value; if it
   is absent, use the documented default `5s`. Check whether `BLUEPRINT_NAMES` is empty, exact names,
   or `*` without printing any secret values. Empty means intentional setup mode and no synthetic data.
2. Use the browser's normal signed-in session for the intended Grafana stack. Confirm the stack
   identity before opening Explore, and keep synthetic-data and process-self-observability
   destinations separate. An ingest token is write access, not a query credential; browser
   readback needs no extra API token. A `gcx` context is optional, not a browser prerequisite.
3. If neither an authorised browser session nor an explicitly supplied query context is
   available, collect only the local observations and report remote data landing as unverified.
   An unavailable access prerequisite is not a remote-readback pass. Do not make remote requests
   or ask for another token when the existing signed-in browser is sufficient.

## 3. Control plane health

Resolve `SYNTHKIT_BIND` from `.env` (default `127.0.0.1`) and detect only whether
`CONTROL_TOKEN` is non-empty. Never print its value. Use Basic auth for protected routes when set,
and call the public readiness probe without credentials. On an exact image verified to support
`-startupcheck`, `GET /control/startup` (or that CLI command inside the container) separately proves
boot completion only. Read the startup-versus-delivery-readiness section in
`$SYNTHKIT_CHECKOUT/docs/control-plane.md`.
A selected dry-run or sparse lane can pass startup while delivery readiness remains 503; do not
replace readiness with startup, inflate traffic, or claim source admission, HA authority or live
landing from startup success. Chart startup defaults retain `-healthcheck` for older pinned images;
opt into the new command only after image compatibility is verified. For the browser-first path,
open the deployment's `/control/ui`, `/control/readiness` and authenticated `/control/status` on
the host's loopback port 8088. If Basic auth is enabled, use username `control` and enter its
configured token directly in the browser's local prompt, not in chat or a URL. The following CLI
commands are optional alternatives when `curl` is already available:

```bash
curl -fsS "http://<bind>:8088/control/readiness"
curl -fsS -u control "http://<bind>:8088/control/status"
curl -fsS -u control -o /dev/null -w '%{http_code}\n' "http://<bind>:8088/control/ui"
docker compose ps
docker compose logs --tail=50 synthkit
```

`-u control` prompts for the token without exposing it in process arguments. For unattended
verification use an operator-owned mode-0600 netrc file; never expand a token into argv.
Omit `-u` only when `CONTROL_TOKEN` is absent on a loopback-only deployment. Expect UI status
`200`, a running container, and ready sinks in the authenticated status result. Treat a
connection refusal from another machine as a likely loopback bind; query on the host or use an SSH
tunnel, rather than opening the bind casually.

Read `setup_required`, `ready`, and `live_ready` from the readiness/status response. When
`setup_required=true`, require HTTP `200`, `ready=true`, `live_ready=false`, and zero loaded/active
blueprints. Search the retained service log, not only the 50-line sample above, for the actionable
warning in `docker compose logs --no-color synthkit`; do not require a separate search tool.
Report that the control plane is healthy but no synthetic telemetry is intended, and stop before
remote landing queries. When `setup_required=false`, continue with the selected blueprint checks below.

On a selected image that implements the current optional-lane diagnostics, inspect authenticated
`optional_lanes` immediately after health. Require exactly the nine documented lanes and report
each state/reason/verification. An older image without that surface has unverified optional-lane
capabilities; current source documentation does not supply its missing evidence. Stop before
claiming an optional lane when it is `unsupported` or unresolved `partial`; missing fields are
names only and must never be expanded to
values. An SM partial state is a successful diagnostic boundary: report that snapshot provisioning
and/or the required emitter restart remains. This read-only skill does not run the provisioner.

Inspect each sink-history `last_acknowledgement` in `/control/status`. `accepted` is
protocol-level receiver acceptance; `partial`/`rejected` indicates explicit loss, while
`unverified`/`deferred` is not complete acceptance evidence. Counts use the declared unit,
which differs between logical series and RW2 wire elements. Continue with backend readback;
no receipt or readiness field proves checkpoint takeover/backfill. See
`$SYNTHKIT_CHECKOUT/docs/checkpoint-foundations.md`. If the selected older image lacks an
acknowledgement field, report it as unavailable rather than accepted and continue to actual
backend readback for the supported signals.

## 4. Wait for declared cadence, then query

The master cadence is not a promise that every series appears every master tick: metric-producing
constructs/workloads have their own intervals and the runner clamps metric lanes to a 60-second
DPM floor with deterministic phase offsets. After a new deployment or configuration change, wait
**two declared emission intervals plus the configured delivery deadline** for every declared lane.
Use the per-construct/workload interval from the schema/catalogue, or 60 seconds when it is a
metric lane below that floor; add `SEND_BATCH_DEADLINE` (default `5s`). A master tick is not an
emission proof.

For an ordinary 60-second metric lane with the default five-second delivery deadline, this is a
125-second warmup. Use a bounded recent Explore range that includes this run, such as the last
15 minutes, and require observations newer than deployment startup. A slower declared lane needs
its own calculated window. At the end of the window, inspect diagnostics and the selected query;
do not poll indefinitely, increase traffic to force data, or count old results as this run's proof.

Before building a query, read authenticated `GET /control/inventory`. It is the mapping source:
each construct lists the metric names it has emitted and, when available, a low-cardinality
`identity` object with its explicit `scope` plus query labels. Use the exact metric name with those
labels. A blueprint-scoped identity includes `blueprint`; a substrate identity deliberately does
not, and is queried with its declared cluster/account/database identity instead. This avoids a
hand-built family-to-blueprint table and never promotes high-cardinality values into selectors.

For generated views, read `$SYNTHKIT_CHECKOUT/docs/tools.md`. A current source checkout provides
the `otlp-native` template and separate process operational exporter, but a pinned older published
image need not contain those commands. Check the exact image's capabilities before choosing that
optional path. Browser Explore does not depend on a generated dashboard. Each export's query
inventory and omissions describe its coverage; offline generation does not establish delivery,
and deployment filtering is not datasource authorization.

In the same two-interval window, read `/control/status`, `/control/health`, and
`/control/diagnostics`: status identifies a blocked delivery lane, health identifies a failing
blueprint/construct tick, and diagnostics identifies a rejected load. A mutation returning `200`
only proves control-state acceptance. Reset and scaling require an explicit before/after emitted-data
assertion from the inventory-backed query before they can be called successful.

For the customer metric lane, choose a real expected metric family from the active blueprint's
relevant `signals/` page. Substitute only the placeholders shown here:

```promql
{__name__=~"<real_metric_name_or_family_regex>", blueprint="<blueprint_label>"}
```

For a substrate-scoped construct, omit `blueprint` because synthkit deliberately does not stamp it:

```promql
{__name__=~"<real_substrate_metric_name_or_family_regex>", cluster="<declared_cluster>"}
```

For staff self-observability, query the known family selector after its own cadence:

```promql
{__name__=~"synthkit_.*"}
```

### Browser Explore (default)

Open the selected stack's Grafana Explore in its normal signed-in browser session. Choose each
signal's datasource deliberately and use the same bounded recent range. For the ordinary
`otlp-native` first run, use the sourced selectors in
`$SYNTHKIT_CHECKOUT/docs/quickstart.md`:

```promql
http_server_request_duration_seconds_count{service_name="otlp-api-enriched"}
```

```logql
{source="app",service_name="otlp-api-enriched",cluster="otlp-native-prod-euw1"}
```

```traceql
{ resource.service.name = "otlp-api-enriched" }
```

Require non-empty results with fresh sample/log/trace timestamps after this deployment started.
Open a returned trace and inspect its HTTP root span. The native OTLP duration histogram is
receiver-projected into classic `_bucket`, `_sum` and `_count` series on the documented path;
do not infer a Prometheus native histogram solely from OTLP input. Use the actual inventory and
local signal contract for another selected blueprint rather than substituting fabricated names.
An empty successful query, green sink strip or successful preflight is not proof that all signals
arrived. If the agent cannot inspect the browser result, report the exact query and verification
gap without requiring an extra plugin or claiming success.

### Native application readback

Keep ingestion evidence separate from native-app discovery and browser state. After the bounded
warmup, confirm fresh post-start data in Explore for the same datasource and declared identity.
If Kubernetes Home was cold-opened before data arrived and still shows onboarding, hard reload
the same Cloud URL after selecting a recent relative range; a time-picker change alone may retain
the cached negative discovery result. After live scaling, navigate afresh to the workload and
load its pod table before comparing counts. Follow
`$SYNTHKIT_CHECKOUT/docs/troubleshooting.md#native-app-view-stays-empty-after-data-arrives`;
do not alter Grafana app code or promise an upstream fix as part of deployment verification.

Application Observability Logs / New Format is conditional on the exact deployed image's fresh
received logs having the matching `service_name` and `service_namespace`, plus successful readback
in that view. Until that is proved, retain Loki Explore and the exact log `trace_id` to Tempo HTTP
root check. App logs use the Loki sink; OTLP application metrics/traces do not establish an OTLP
app-log exporter. See `$SYNTHKIT_CHECKOUT/docs/troubleshooting.md#application-logs-new-format-is-empty`.
Do not infer a working latency control from an error-control demonstration; verify the actual
emitted metric and trace response separately on the deployed image.

### Optional query CLI and API paths

Use `gcx` only when it is already available and explicitly configured for the intended stack.
Confirm its context without exposing credentials:

```bash
command -v gcx
gcx config list-contexts
gcx config current-context
gcx config check
```

Then replace the bracketed values in the following commands. `--context` avoids silently using
the wrong default context, and `--since` bounds the observation window:

```bash
gcx --context <customer-context> metrics query \
  '{__name__=~"<real_metric_name_or_family_regex>", blueprint="<blueprint_label>"}' \
  --since '<emission-interval-plus-delivery-deadline>'
```

For substrate-scoped output, use the cluster selector instead:

```bash
gcx --context <customer-context> metrics query \
  '{__name__=~"<real_substrate_metric_name_or_family_regex>", cluster="<declared_cluster>"}' \
  --since '<emission-interval-plus-delivery-deadline>'
```

For staff self-observability, change the context and selector:

```bash
gcx --context <staff-context> metrics query '{__name__=~"synthkit_.*"}' \
  --since '<selfobs-emission-interval-plus-delivery-deadline>'
```

Do not invent a metric name or hard-code a stack URL. For an already authorised Prometheus API
client, make only a GET to:

```text
https://<prometheus-api-host>/api/v1/query?query=<url-encoded-promql>
```

Use the stack's documented endpoint and credentials from the operator's terminal; do not place a
token in a command, chat, or URL. A successful response must have `status: "success"` and at least
one result. The absence of a result before the calculated wait window is inconclusive, not a
failure.

## 5. Optional lanes

- Self-observability is expected only with `SELFOBS_ENABLED=true` and the complete self-OTLP
  triplet, independently of synthetic `DRY_RUN`; query the staff context, never the customer context.
- In lease mode, inspect operational logs from both standby and leader after the five-minute
  heartbeat plus delivery deadline. Use their actual process identity and `ha.role` resource;
  metrics/traces alone do not prove the standard-log bridge. A source fix is not live evidence
  until its exact image is deployed and receiver logs are observed.
- Process profiling is expected only when self-obs is on and the complete
  `GC_PYROSCOPE_*` triplet is present. There is no standalone profiling switch.
- Fleet metrics require the declaration and normal metrics landing; they do not require FM API
  credentials. Registration separately requires the complete `GC_FM_*` triplet and fresh
  registration/heartbeat evidence. Never call the FM API for metrics-only mode.
- Synthetic Monitoring requires a selected declaration, a matching complete registration with no
  pending journal, and the post-provision restart/acquisition before status can be enabled/verified.
  Query only after its declared interval plus delivery deadline. First v2 format activation requires
  explicit apply/restart; subsequent release-only bumps preserve activation when specs/target/schema
  match. The write tool still must equal the current snapshot version. Kubernetes standby never
  produces data. HA chart deployments can use named Kubernetes state with separate emitter and
  provisioner Lease authority; verify the matching image and configuration against
  `$SYNTHKIT_CHECKOUT/docs/synthetic-monitoring.md`. The default file-mode Job still requires shared storage.
- An SM credential or endpoint change requires an explicit `SM_PROVISION_MIGRATE_TARGET=true`
  preview and matching apply within 15 minutes before the restart; never treat a new snapshot alone
  as migrated ownership.
- RUM, Sigil, Synthetic Monitoring, and synthetic profiles are expected only when both their
  documented credential/configuration gate and an emitting blueprint lane are present.

## 6. Triage

| Symptom | Likely cause | Safe next step |
|---|---|---|
| Status healthy, no data | `DRY_RUN=true` | After the dry-run passes and live delivery is authorised, set it false and recreate; do not ask again when that authorisation already exists. |
| Empty query before its wait window | Interval/phase/delivery delay | Wait the calculated interval; do not retest every master tick. |
| 401/403 | Wrong token scope or user/stack ID | Compare presence and documented scopes; never print values. |
| Container restart loop | Control-state directory is not writable | Check ownership; container uid is 65532. |
| Staff stack empty | Customer/staff endpoints or tokens crossed | Verify the separate `GC_SELF_OTLP_*` triplet. |
| Data in wrong stack | Endpoint/user mismatch | Compare declared non-secret endpoints and identifiers with the intended stack. |
