# synthkit Helm chart

Deploys [synthkit](https://github.com/rknightion/synthkit) — a composable synthetic-telemetry
generator — onto Kubernetes. The operator-facing guide is
[docs/kubernetes.md](../../docs/kubernetes.md); this file is the values reference and the record of
the resource measurement.

```bash
helm install synthkit ./charts/synthkit \
  --namespace synthkit --create-namespace \
  --set config.blueprintNames=otlp-native
```

That starts a dry run: every estate is built and rendered, nothing is pushed, and no credentials are
needed. Note that `config.blueprintNames` takes blueprint **identities**, which are the `name:`
field inside each YAML and are not always the filename — `blueprints/cw-infra-aws.yaml` declares
`aws-cloudwatch-infra`, and `blueprints/synthetic-monitoring.yaml` declares `synthetic-checks`. An
unknown name is a hard startup failure that lists the available ones.

---

## Two things this chart refuses to do

**It never takes a credential from a values file.** Every credential is projected from an existing
Secret, grouped by destination stack, and an environment variable may only be projected from the
group that owns it. `credentials.selfObs.existingSecret` may not be the same Secret object as any
synthetic-data group's, because sharing one is how `GC_TOKEN` ends up authenticating
self-observability — which the architecture forbids. Both rules fail the render, not the runtime.

**It never opens the control plane implicitly.** The default binds `127.0.0.1:8088`, renders no
Service or Ingress, and applies a default-deny-ingress NetworkPolicy; `kubectl port-forward` reaches
it because forwarding runs inside the pod's network namespace. Opening it needs an exact
acknowledgement string, a `CONTROL_TOKEN` Secret, and `service.enabled` — and the binary re-checks
the token and acknowledgement at startup, independently of the chart.

---

## Resource requests and limits, and how they were measured

`resources.requests.memory: 768Mi` / `resources.limits.memory: 1Gi` size a **small explicit
selection** — measured against four blueprints. They are not a ceiling for an arbitrary selection,
and the whole catalogue needs roughly five times as much.

### Method

The binary was run natively at the chart's default cadence (`TICK_DEFAULT=5s`) in `DRY_RUN=true`,
which builds and renders every estate on every tick and exercises the same construct, workload,
encode and queue paths a live run does. Two figures were sampled every 15 s for 10 minutes per case:

- **`heap_bytes`** from the control plane's `/control/health`. Runtime-reported and
  platform-independent, so it is the figure the sizing is derived from.
- **RSS** from `ps`, as a sanity check. Sampled on darwin/arm64, where Go's `MADV_FREE` pages stay
  resident until memory pressure, so RSS overstates what a Linux container needs.

The script is reproducible against any selection:

```bash
go build -o /tmp/synthkit-measure ./cmd/synthkit
DRY_RUN=true BLUEPRINT_NAMES='<your selection>' TICK_DEFAULT=5s \
  JSON_HTTP_ADDR=127.0.0.1:8099 /tmp/synthkit-measure &
while sleep 15; do
  curl -s http://127.0.0.1:8099/control/health | jq '.process.heap_bytes/1048576|floor'
done
```

Sample for at least ten minutes. The first three minutes are not steady state.

### What was measured

Measured 2026-08-27, synthkit at `main`, darwin/arm64, `DRY_RUN=true`, `TICK_DEFAULT=5s`. Runs were
10 minutes (15 for the catalogue). Heap is a sawtooth, so both ends of it matter, and the statistics
below cover the steady-state window only — the first three minutes are discarded because the heap
has not settled before then.

| Selection | Blueprints | Heap floor | Heap peak | Peak RSS | CPU |
|---|---|---|---|---|---|
| `otlp-native` | 1 | 253 MiB | 461 MiB | 534 MiB | 2.59 s / 600 s = **0.43 %** of one core |
| `k8s-full-stack,otlp-native,aws-cloudwatch-infra,hostfleet` | 4 | 289 MiB | 522 MiB | 603 MiB | 4.64 s / 600 s = **0.77 %** of one core |
| `*` (whole bundled catalogue) | 26 | 1716 MiB | 3135 MiB | 3615 MiB | 220.7 s / 900 s = **24.5 %** of one core |

### What the numbers say, and how the defaults follow

1. **The floor is flat, so there is no leak.** For the single-blueprint case the sawtooth floor sat
   at 253–260 MiB across ten minutes with only mild upward drift. That drift is cumulative
   label-combination state in `internal/state` as new combinations appear, which is the documented
   behaviour rather than unbounded growth.
2. **Estate cost is not linear in blueprint count.** Going from one blueprint to four moved the
   floor by 36 MiB and the peak by 61 MiB — most of the small-selection footprint is a fixed
   baseline. Going to all 26 moved it by a factor of six. A small selection and a large one are
   genuinely different sizing problems, which is why there is no single default that covers both.
3. **The peak is roughly twice the floor**, because Go's collector targets about twice the live
   set. A memory limit has to absorb the sawtooth peak, not the steady figure.
4. **Requests are set above the measured peak RSS, not at the floor.** A Burstable pod chronically
   above its memory request is the first candidate for eviction under node pressure. `768Mi` sits
   above the 603 MiB peak RSS of the four-blueprint case; `1Gi` is about twice its 522 MiB peak
   heap.
5. **CPU is negligible on average and bursty in shape.** 0.43 % of one core for a single blueprint
   independently reproduces the 0.53 % the Compose deployment recorded on 2026-08-21 — a useful
   cross-check that this method measures the same thing. The `100m` request is roughly 13–23× the
   measured average, as headroom for the burst at each tick boundary where the whole estate is
   rendered at once.

For `*`, budget `requests {cpu: 500m, memory: 3584Mi}` and `limits {memory: 5Gi}`. That is the
worked example in `ci/06-all-lanes-values.yaml`.

### What the measurement does not cover

- **Queue memory under backpressure.** `SEND_QUEUE_CAPACITY` is an item count **per sink**, not a
  byte budget, and item sizes differ substantially between a metric series, a Loki stream, an OTLP
  resource, a Faro beacon, a profile and a Sigil export. A dry run never fills those queues. Before
  raising `config.sendQueueCapacity`, use the pressure-test calculation in
  [docs/deployment.md](../../docs/deployment.md#capacity-queue-memory-and-container-limits).
- **Linux container behaviour.** Measured on darwin, so RSS is the pessimistic figure and heap is
  the portable one. Sizing is derived from heap.
- **Live delivery.** A live run adds TLS buffers and retry state on top.

### Capping the peak instead of raising the limit

Go's collector does not know about a container memory limit, which is what makes the peak roughly
double the live set. Setting `GOMEMLIMIT` a little below the container limit makes the collector
work harder instead of growing, trading a few percent of CPU for a much tighter peak. It is a Go
runtime variable rather than synthkit configuration, so it goes through `extraEnv`:

```yaml
resources:
  requests: { cpu: 100m, memory: 640Mi }
  limits:   { memory: 768Mi }
extraEnv:
  - name: GOMEMLIMIT
    value: "680MiB"
```

Set it below the limit, never above: `GOMEMLIMIT` is a soft target, and a value at or above the
limit lets the container be OOM-killed before the collector reacts.

---

## Values

### Image

| Key | Default | Notes |
|---|---|---|
| `image.repository` | `ghcr.io/rknightion/synthkit` | |
| `image.tag` | `""` | Falls back to `Chart.appVersion`. Bare `X.Y.Z`; Git tags carry the `v`. |
| `image.digest` | `""` | Preferred for standing deployments. Wins over `tag`. |
| `image.pullPolicy` | `IfNotPresent` | |
| `imagePullSecrets` | `[]` | |

### Runtime behaviour

| Key | Default | Environment variable |
|---|---|---|
| `config.dryRun` | `true` | `DRY_RUN` — live push is always an explicit opt-in |
| `config.blueprintNames` | `""` | `BLUEPRINT_NAMES` — empty is setup mode, emits nothing |
| `config.tickDefault` | `5s` | `TICK_DEFAULT` |
| `config.seriesCap` | `""` | `SERIES_CAP` |
| `config.tickTimeout` | `""` | `TICK_TIMEOUT` (seconds) |
| `config.gitPollInterval` | `"0"` | `GIT_POLL_INTERVAL` |
| `config.sendShards` | `"8"` | `SEND_SHARDS` |
| `config.sendBatchMax` | `"5000"` | `SEND_BATCH_MAX` |
| `config.sendBatchDeadline` | `5s` | `SEND_BATCH_DEADLINE` |
| `config.sendQueueCapacity` | `"500000"` | `SEND_QUEUE_CAPACITY` |
| `config.sendDrainDeadline` | `30s` | `SEND_DRAIN_DEADLINE` |

`BLUEPRINTS`, `CONFIG_SNAPSHOT_PATH`, `BLUEPRINT_DATA_DIR`, `JSON_HTTP_ADDR`, `SYNTHKIT_BIND` and
`CONTROL_EXPOSURE_ACK` are set by the chart on the container, where an explicit `env` entry outranks
the ConfigMap, so editing the ConfigMap cannot move the control plane off its bind or relocate the
state file.

### Self-observability

| Key | Default | Environment variable |
|---|---|---|
| `selfObs.enabled` | `false` | `SELFOBS_ENABLED` |
| `selfObs.tags` | `""` | `SELFOBS_TAGS` |
| `selfObs.metricInterval` | `15s` | `SELFOBS_METRIC_INTERVAL` |
| `selfObs.pyroscopeTags` | `""` | `PYROSCOPE_TAGS` |
| `selfObs.pyroscopeMutexFraction` | `"5"` | `PYROSCOPE_MUTEX_FRACTION` |
| `selfObs.pyroscopeBlockRate` | `"5"` | `PYROSCOPE_BLOCK_RATE` |

Enabling it requires `credentials.selfObs.existingSecret`, which must be a different Secret from
every synthetic-data group's.

### Credentials

Each group takes `existingSecret` (a Secret in the release namespace) and `keys` (an
`ENV_VAR: secretKey` map, defaulting to identity). Groups: `data`, `selfObs`, `rum`, `sm`, `fm`,
`sigil`, `control`, `git`. The variables each group owns are listed in
[docs/kubernetes.md](../../docs/kubernetes.md#credentials-come-from-secrets-grouped-by-destination-stack).

Helm deep-merges maps, so dropping one of the chart's default `keys` entries needs an explicit
`KEY: null`, not just omitting it.

### Control plane

| Key | Default | Notes |
|---|---|---|
| `controlPlane.exposure.ack` | `""` | Closed. `trusted-network` or `tls-proxy` to open |
| `controlPlane.service.enabled` | `false` | Requires a non-empty `ack` |
| `controlPlane.service.type` / `.port` | `ClusterIP` / `8088` | |
| `controlPlane.ingress.enabled` | `false` | Requires `service.enabled` and at least one host |
| `networkPolicy.enabled` | `true` | Ingress-only; default-deny while closed |
| `networkPolicy.ingressFrom` | `[]` | Peers allowed on 8088 once exposed |

### Persistence, scheduling, probes

| Key | Default | Notes |
|---|---|---|
| `persistence.enabled` | `true` | `false` swaps in an emptyDir — lost on reschedule |
| `persistence.existingClaim` | `""` | Bind an operator-managed claim |
| `persistence.size` | `1Gi` | |
| `persistence.retain` | `true` | `helm.sh/resource-policy: keep` |
| `probes.startup` / `.readiness` | enabled | `synthkit -healthcheck`, delivery-aware |
| `probes.liveness.enabled` | `false` | The same check as liveness crash-loops on a backend outage |
| `resources` | see above | Derived from measurement |
| `serviceAccount.automountServiceAccountToken` | `false` | synthkit calls no Kubernetes API |
| `revisionHistoryLimit` | `2` | Superseded ReplicaSets kept; the Kubernetes default of 10 is nine nobody rolls back to |
| `terminationGracePeriodSeconds` | `60` | Drain budget for the delivery queue |

Replica count is deliberately not a value: two emitters produce the same series identities against
the same backend. Setting `replicaCount` is rejected by the values schema rather than ignored,
because it is the key every other chart uses and silently dropping it would leave you believing you
had scaled out.

**There is deliberately no PodDisruptionBudget, and adding one would be a bug.** This workload runs
exactly one replica, so a PDB with `minAvailable: 1` can never be satisfied by evicting it and a
node drain blocks forever. A gap during a drain is a gap in synthetic data, not an outage.

### Synthetic Monitoring provisioner

| Key | Default | Notes |
|---|---|---|
| `smProvision.enabled` | `false` | Opt-in one-shot Job, not a Helm hook |
| `smProvision.apply` | `false` | Preview first; review the log, then apply |
| `smProvision.runId` | `"1"` | Bump to re-run on a later upgrade |

---

## Development

```bash
helm lint charts/synthkit
helm template charts/synthkit
bash charts/synthkit/tests/render_test.sh
REQUIRE_KUBECONFORM=1 bash charts/synthkit/tests/render_test.sh   # what CI runs
just helm-test                                                    # all of the above
```

`values.schema.json` is structural validation and closes a gap the template guards cannot see. The
`synthkit.validate` helper enforces semantics — which credential groups a mode requires, which
environment variables a group may own — but it can only inspect keys that were actually set. A
misspelled key is merged silently by Helm and then simply is not there, so the group renders
unconfigured and the failure surfaces later as an unauthenticated lane. `additionalProperties:
false` turns that into a values error at install time. The suite reports which guard refused each
negative permutation, `(template guard)` or `(values schema)`.

kubeconform validates the rendered manifests against the Kubernetes API schemas at Chart.yaml's
`kubeVersion` floor, so the compatibility claim is checked rather than asserted. Override with
`KUBE_VERSION`. It is skipped when the binary is absent; CI sets `REQUIRE_KUBECONFORM=1` so a
missing binary fails instead.

`tests/render_test.sh` asserts the credential and exposure permutations rather than eyeballing one
render. `ci/*-values.yaml` are permutations that must render correctly;
`tests/invalid/*-values.yaml` are permutations the chart must **refuse** — a self-obs credential
sourced from the synthetic-data Secret, a Service with no acknowledgement, an `extraEnv` shadowing
an owned credential. Both directories are excluded from the packaged chart by `.helmignore`.
