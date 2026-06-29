# Grafana Pyroscope continuous profiling — ScopeBlueprint + ScopeSubstrate

Pyroscope receives profiling data via four emitter shapes, discriminated by the **`source`** label
(absent on SDK push) and **`pyroscope_spy`** (absent on eBPF and most SDK paths). All profile types
use the `__profile_type__` selector format:

```
name:sampleType:sampleUnit:periodType:periodUnit
```

e.g. `process_cpu:cpu:nanoseconds:cpu:nanoseconds`, `memory:alloc_objects:count:space:bytes`.

Pyroscope derives synthetic meta-labels from the profile stream that are queryable but NOT emitted
by the client: `__name__`, `__profile_type__`, `__type__`, `__unit__`, `__period_type__`,
`__period_unit__`, `__service_name__`.

Global rules: see [`00-canon.md`](00-canon.md) — scoping `[slug: scoping]`, cardinality
`[slug: cardinality]`, shape rules `[slug: shape-rules]`.

*Provenance: live capture 2026-06-15 (a live reference EKS cluster, datasource
`grafanacloud-profiles`), incl. a multi-language deployment (Go SDK, Python SDK, Java
async-profiler, eBPF DaemonSet). Raw query results not retained in-repo.*

---

## Emitter shape discriminators [slug: profiles-discriminators]

| Shape | `source` label | `pyroscope_spy` label | `jfr_event` label |
|---|---|---|---|
| Alloy eBPF | `alloy/pyroscope.ebpf` | absent | absent |
| Alloy pprof scrape | `alloy/pyroscope.pprof` | absent | absent |
| Alloy Java async-profiler | `alloy/pyroscope.java` | `alloy.java` | `itimer` (+ others under alloc/lock activity) |
| SDK push (Go/Python/etc.) | **absent** | `gospy` (Go SDK) · absent (Python SDK) | absent |

⚠ **`source` absent = SDK push.** Do NOT synthesise a `source` label for SDK-push profiles; its
absence is the discriminator. Similarly, current Python SDK (pyroscope-io 1.0.11) does **not** set
`pyroscope_spy` — this is not a gap, it is reality.

---

## Runtime → profile type mapping [slug: profiles-runtime-map]

| Runtime | Profile types emitted |
|---|---|
| Any language (eBPF) | `process_cpu:cpu:nanoseconds:cpu:nanoseconds` only |
| Go (pprof scrape or SDK) | Full Go set — see [slug: profiles-pprof] / [slug: profiles-sdk-go] |
| JVM (async-profiler) | `process_cpu` + `alloc_in_new_tlab_*` + Java mutex (activity-gated) |
| Python (SDK) | `process_cpu:cpu:nanoseconds:cpu:nanoseconds` only |
| .NET (SDK) | `process_cpu:cpu:nanoseconds:cpu:nanoseconds` only (current SDKs) |

---

## Alloy eBPF scrape — `source="alloy/pyroscope.ebpf"` [slug: profiles-ebpf]

*Provenance: live capture 2026-06-15 (a live reference EKS cluster); service_name examples: checkout, karpenter,
spike-python.*

One profile type only, any language:

```
process_cpu:cpu:nanoseconds:cpu:nanoseconds
```

Labels are injected by the Alloy `pyroscope.ebpf` component from the k8s discovery context.
`language=python` was observed on Python-runtime pods (Alloy detects from proc maps); absent on
Go/Java pods in the same capture.

```yaml signals
family: profiles_ebpf
scope: substrate
sink: pyroscope
labels:
  service_name: <service-name>           # e.g. checkout, karpenter, spike-python
  service_namespace: <k8s-namespace>
  namespace: <k8s-namespace>
  pod: <pod-name>
  node: <node-hostname>                  # e.g. ip-10-1-30-42.eu-west-1.compute.internal
  container: <container-name>
  cluster: <cluster-name>
  k8s_cluster_name: <cluster-name>
  source: alloy/pyroscope.ebpf
  language: python                       # present on Python-runtime pods only; absent on Go/Java
profile_types:
  - {id: "process_cpu:cpu:nanoseconds:cpu:nanoseconds", v: ok}
```

---

## Alloy pprof scrape — `source="alloy/pyroscope.pprof"` [slug: profiles-pprof]

*Provenance: live capture 2026-06-15 (a live reference EKS cluster); example instance=10.1.59.164:8082;
helm_sh_chart=argo-cd-9.5.21; topology_kubernetes_io_region=eu-west-1.*

Full Go pprof profile type set (richest label envelope):

```
process_cpu:cpu:nanoseconds:cpu:nanoseconds
process_cpu:samples:count:cpu:nanoseconds
memory:alloc_objects:count:space:bytes
memory:alloc_space:bytes:space:bytes
memory:inuse_objects:count:space:bytes
memory:inuse_space:bytes:space:bytes
goroutine:goroutine:count:goroutine:count
```

⚠ **`goroutine` SINGULAR** — the pprof-scrape path emits `goroutine:goroutine:count:goroutine:count`
(not `goroutines`). The SDK push path differs — see [slug: profiles-sdk-go].

The pprof scrape path carries the **richest label set**: the core k8s labels from eBPF plus
app.kubernetes.io well-known labels, helm labels, StatefulSet labels, and topology labels, all
injected by Alloy relabelling from pod metadata.

```yaml signals
family: profiles_pprof
scope: substrate
sink: pyroscope
labels:
  # Core k8s envelope (same as eBPF):
  service_name: <service-name>
  service_namespace: <k8s-namespace>
  namespace: <k8s-namespace>
  pod: <pod-name>
  node: <node-hostname>
  container: <container-name>
  cluster: <cluster-name>
  k8s_cluster_name: <cluster-name>
  source: alloy/pyroscope.pprof
  # app.kubernetes.io labels (from pod metadata via Alloy relabelling):
  app_kubernetes_io_name: <app-name>
  app_kubernetes_io_component: <component>
  app_kubernetes_io_instance: <instance>
  app_kubernetes_io_part_of: <part-of>
  app_kubernetes_io_managed_by: <managed-by>
  app_kubernetes_io_version: <version>
  # Helm / controller labels:
  helm_sh_chart: <chart-name-version>    # e.g. argo-cd-9.5.21
  controller_revision_hash: <hash>
  apps_kubernetes_io_pod_index: <index>
  statefulset_kubernetes_io_pod_name: <pod-name>
  # Topology labels:
  topology_kubernetes_io_region: <region>  # e.g. eu-west-1
  topology_kubernetes_io_zone: <zone>
  # Scrape identity:
  instance: <ip:port>                    # e.g. 10.1.59.164:8082
  profiles_grafana_com_scrape: "true"
profile_types:
  - {id: "process_cpu:cpu:nanoseconds:cpu:nanoseconds", v: ok}
  - {id: "process_cpu:samples:count:cpu:nanoseconds", v: ok}
  - {id: "memory:alloc_objects:count:space:bytes", v: ok}
  - {id: "memory:alloc_space:bytes:space:bytes", v: ok}
  - {id: "memory:inuse_objects:count:space:bytes", v: ok}
  - {id: "memory:inuse_space:bytes:space:bytes", v: ok}
  - {id: "goroutine:goroutine:count:goroutine:count", v: ok,
     note: "SINGULAR goroutine — pprof scrape path; SDK push uses goroutines (PLURAL) — see profiles-sdk-go"}
```

---

## Alloy Java async-profiler — `source="alloy/pyroscope.java"` [slug: profiles-java]

*Provenance: live capture 2026-06-15 (a live reference EKS cluster);
example service_instance_id=synthkit-profiling-spike.spike-java-69fff7fdb9-h8c5r.spike-java.*

Java profiles are scraped by Alloy's `pyroscope.java` component using the async-profiler JVM agent.
`jfr_event="itimer"` is the CPU sampling event. Alloc and mutex types appear **only under real
alloc/lock activity** — a CPU-only JVM (no alloc/lock pressure) emits `process_cpu` only.

⚠ **Java mutex period unit is `count`, NOT `nanoseconds`** — the mutex family for Java is
`mutex:contentions:count:mutex:count` and `mutex:delay:nanoseconds:mutex:count`. This coexists
with the Go mutex family `mutex:contentions:count:contentions:count` — the `mutex`/`contentions`
sample type ID is shared, but the PERIOD TYPE differs: Java uses period type `mutex` (`mutex:count`),
Go uses period type `contentions` (`contentions:count`). So the two families are distinct series.

```yaml signals
family: profiles_java
scope: blueprint
sink: pyroscope
labels:
  service_name: <service-name>
  service_namespace: <k8s-namespace>
  namespace: <k8s-namespace>
  pod: <pod-name>
  node: <node-hostname>
  container: <container-name>
  cluster: <cluster-name>
  k8s_cluster_name: <cluster-name>
  source: alloy/pyroscope.java
  pyroscope_spy: alloy.java
  jfr_event: itimer                      # CPU sampling; other events possible under alloc/lock
  service_instance_id: <instance-id>     # e.g. synthkit-profiling-spike.spike-java-69fff7fdb9-h8c5r.spike-java
profile_types:
  - {id: "process_cpu:cpu:nanoseconds:cpu:nanoseconds", v: ok}
  - {id: "memory:alloc_in_new_tlab_bytes:bytes:space:bytes", v: ok,
     note: "activity-gated: emitted only under real TLAB alloc activity"}
  - {id: "memory:alloc_in_new_tlab_objects:count:space:bytes", v: ok,
     note: "activity-gated: emitted only under real TLAB alloc activity"}
  - {id: "mutex:contentions:count:mutex:count", v: ok,
     note: "activity-gated; period unit is count (Java) — same ID as Go mutex:contentions, different semantic"}
  - {id: "mutex:delay:nanoseconds:mutex:count", v: ok,
     note: "activity-gated; period unit is count (Java) — distinct from Go mutex:delay (period: nanoseconds)"}
```

---

## SDK push (no `source` label) [slug: profiles-sdk]

SDK-push profiles do NOT carry a `source` label. The presence/absence of `source` is the sole
discriminator between Alloy-collected and SDK-pushed profiles.

### Go SDK (`pyroscope_spy="gospy"`) [slug: profiles-sdk-go]

*Provenance: live capture 2026-06-15 (a live reference EKS cluster, Go SDK push);
example env=<stack>/<env>; version=dev.*

Full Go SDK profile type set. ⚠ **`goroutines` PLURAL** distinguishes the SDK push path from the
pprof scrape path (`goroutine` singular — see [slug: profiles-pprof]).

```
process_cpu:cpu:nanoseconds:cpu:nanoseconds
process_cpu:samples:count:cpu:nanoseconds
memory:alloc_objects:count:space:bytes
memory:alloc_space:bytes:space:bytes
memory:inuse_objects:count:space:bytes
memory:inuse_space:bytes:space:bytes
goroutines:goroutine:count:goroutine:count       ← PLURAL (SDK push)
mutex:contentions:count:contentions:count
mutex:delay:nanoseconds:contentions:count
block:contentions:count:contentions:count
block:delay:nanoseconds:contentions:count
```

⚠ Go mutex/block period TYPE is `contentions` (period unit `count`) — contrast Java async-profiler
where the mutex period type is `mutex` (`mutex:count`) (see [slug: profiles-java]).

```yaml signals
family: profiles_sdk_go
scope: blueprint
sink: pyroscope
labels:
  service_name: <service-name>
  env: <env-name>                        # e.g. <stack>/<env>
  version: <version>                     # e.g. dev
  pyroscope_spy: gospy
  # NOTE: source label is ABSENT — SDK push discriminator
profile_types:
  - {id: "process_cpu:cpu:nanoseconds:cpu:nanoseconds", v: ok}
  - {id: "process_cpu:samples:count:cpu:nanoseconds", v: ok}
  - {id: "memory:alloc_objects:count:space:bytes", v: ok}
  - {id: "memory:alloc_space:bytes:space:bytes", v: ok}
  - {id: "memory:inuse_objects:count:space:bytes", v: ok}
  - {id: "memory:inuse_space:bytes:space:bytes", v: ok}
  - {id: "goroutines:goroutine:count:goroutine:count", v: ok,
     note: "PLURAL goroutines — SDK push path; pprof scrape emits goroutine (SINGULAR) — see profiles-pprof"}
  - {id: "mutex:contentions:count:contentions:count", v: ok}
  - {id: "mutex:delay:nanoseconds:contentions:count", v: ok}
  - {id: "block:contentions:count:contentions:count", v: ok}
  - {id: "block:delay:nanoseconds:contentions:count", v: ok}
```

### Python SDK (pyroscope-io 1.0.11) [slug: profiles-sdk-python]

*Provenance: live capture 2026-06-15 (a live reference EKS cluster, Python SDK push);
example env=<stack>/<env>.*

Current Python SDK (pyroscope-io 1.0.11) emits CPU profiling only. `pyroscope_spy` and `source`
are both **absent** on Python SDK push — the Python SDK dropped the `*spy` label in current
versions. `language=python` is set by the SDK itself (not Alloy).

⚠ **Heap/lock profile types are NOT emitted by current Python SDK versions** — this is a signals gap.
See PENDING note below.

```yaml signals
family: profiles_sdk_python
scope: blueprint
sink: pyroscope
labels:
  service_name: <service-name>
  app_name: <app-name>
  language: python
  env: <env-name>                        # e.g. <stack>/<env>
  cluster: <cluster-name>
  k8s_cluster_name: <cluster-name>
  # NOTE: source label ABSENT (SDK push)
  # NOTE: pyroscope_spy ABSENT (dropped in current SDK versions)
profile_types:
  - {id: "process_cpu:cpu:nanoseconds:cpu:nanoseconds", v: ok}
```

---

## synthkit SDK-push lane fidelity note [slug: profiles-sdk-synth-fidelity]

synthkit's SDK-push profiling lane (web_service + app workloads) emits the **captured Go and
Python shapes faithfully** and restricts uncaptured runtimes conservatively:

- **Go SDK**: full 11-type Go set with `pyroscope_spy=gospy`, `env`, `version` — matches
  [slug: profiles-sdk-go] exactly.
- **Python SDK**: `process_cpu` only with `language=python`, `env` — NO `version`, NO
  `pyroscope_spy` — matches [slug: profiles-sdk-python] (pyroscope-io 1.0.11) exactly.
- **JVM/node/.NET SDK**: `process_cpu` only, minimal labels — UNCAPTURED shapes. The rich JVM
  profile types (`alloc_in_new_tlab_*`, Java `mutex:*:mutex:count`) are produced EXCLUSIVELY by
  the Alloy `pyroscope.java` collector ([slug: profiles-java]) — they carry `source=alloy/pyroscope.java`
  + `pyroscope_spy=alloy.java` + `jfr_event=itimer`. Emitting those types on the SDK-push lane
  (which has no `source` label) would fabricate a shape no real Pyroscope estate produces.
  See cantfind.md SK-70 for the PENDING item tracking richer JVM SDK push shapes.

---

## Span profiles (trace → profile correlation) [slug: profiles-span]

*Provenance: Grafana Pyroscope source (github.com/grafana/pyroscope), 2026-06-15.
Ingest split: `pkg/model/pprofsplit/pprof_split.go`; reserved label keys: `pkg/pprof/pprof.go`;
Go span-profiles doc: `docs/sources/configure-client/trace-span-profiles/go-span-profiles.md`.*

Span profiles let a Tempo span deep-link to the flamegraph captured while that span ran. The
correlation key is a **pprof `Sample.Label`** with key **`span_id`** (value = the hex trace-span id),
attached to the SUBSET of samples collected during the span. Pyroscope also reserves `trace_id` and
`profile_id` (the latter renamed to `span_id` on ingest — `pprof.go` `SpanIDLabelName`).

⚠ **`span_id` is a SAMPLE label, NEVER a series label or part of the profile-type.** On ingest,
Pyroscope's `pprofsplit` groups samples *without* `trace_id`/`span_id`
(`GroupSamplesWithoutLabels`), so these stay at the sample level and do **not** enter the
`__profile_type__` / series identity. A correct span profile therefore adds NO new profile-types —
it remains queryable via the span selector against the normal type (e.g.
`process_cpu:cpu:nanoseconds:cpu:nanoseconds`).

⚠ **Go span profiles ride ONLY on `process_cpu`.** The Go SDK propagates span context through
`runtime/pprof.SetGoroutineLabels`, which is captured solely by the CPU profile — the Go
span-profiles doc states *"Only CPU profiling is supported."* So `span_id` sample labels appear on
`process_cpu` and never on `memory`/`block`/`mutex`/`goroutines`. (Java async-profiler additionally
supports `wall`; other languages are CPU-only. synthkit's SDK-push lane models the Go SDK, so it
restricts span labels to `process_cpu`.)

synthkit fidelity: the web_service + app SDK-push lanes attach `span_id` Sample.Labels (golden
thread: every value is a real emitted trace span id from the per-blueprint ledger) to `process_cpu`
profiles only. The pprof `SampleType`/`PeriodType` ValueTypes are unaffected — no per-span
profile-types are minted.

---

## PENDING — open gaps [slug: profiles-pending]

Unconfirmed/uncaptured gaps live in the consolidated register [`cantfind.md`](../cantfind.md), NOT here
(this file asserts only captured fact). Open items for profiling: **SK-65** (Python SDK richer types),
**SK-66** (.NET SDK richer types), **SK-67** (standalone/non-k8s label shapes), **SK-68** (`jfr_event`
values beyond `itimer`), **SK-69** (pprof-scrape rich discovery labels — synth emits the core subset;
needs a `fixture.Workload` extension), **SK-70** (JVM SDK-push shape, distinct from the Alloy
async-profiler shape). Each has a structurally-correct representative emitted today; confirm via
live-capture, then fold into this file and remove the `cantfind.md` row.
