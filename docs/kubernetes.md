---
title: Kubernetes
description: Deploy synthkit on Kubernetes with the bundled Helm chart, including the per-stack credential model, the control-plane exposure gate, state volumes, and measured resource sizing.
---

# Kubernetes

The Helm chart in `charts/synthkit/` is the Kubernetes equivalent of the
[Compose deployment](deployment.md). It runs the same published image with the same environment
surface, so everything in [Configuration](configuration.md) and [Credentials](credentials.md)
applies unchanged — the chart decides only how those values are supplied and what is reachable from
outside the pod.

Kubernetes is the mode most people reach for, because synthkit exists to model Kubernetes estates.
It is worth being explicit that the chart does **not** observe the cluster it runs in: synthkit
reads no Kubernetes API **in the default HA-off chart**, needs no RBAC, and its ServiceAccount
carries no projected token. The optional binary Lease mode described below is a coordination
exception; estates still come from blueprints, never from the surrounding cluster.

---

## Optional stateless HA chart

HA is opt-in and requires Kubernetes **1.31 or newer**. HA off retains Kubernetes 1.25 support,
one replica/Recreate and the existing persistent-volume defaults. The HA chart uses exactly two
replicas with lease election and Kubernetes state, RollingUpdate `maxSurge: 1` / `maxUnavailable: 0`,
no state volume or PVC, and a PDB with `minAvailable: 1` / `unhealthyPodEvictionPolicy: AlwaysAllow`.
Hostname topology spread uses `maxSkew: 1` and `ScheduleAnyway`, so a single-node cluster is not
made unschedulable. Resources and probes retain their existing settings.

```yaml
ha:
  enabled: true
  mode: lease
  stateBackend: kubernetes
  replicas: 2
  leaseName: sample-election
  controlConfigMap: sample-control
  bootConfigMap: sample-boot
  gitSourceConfigMaps:
    sample_source: sample-source
persistence:
  enabled: false
```

The slot map is the explicit inventory of predeclared source IDs and named ConfigMaps. Valid
unused slots are allowed. Chart rendering rejects malformed IDs/names, duplicate object names,
unknown values fields, HA file state, PVCs, automatic SM provisioning and `extraEnv` overrides of
any HA/state key. Mutable control-source configuration is not a chart inventory: the runtime
rejects a configured source with a missing or unknown slot before accepting its persistence.
Source IDs may contain single underscores. There is no invented configured-source-ID environment
variable or discovery/list API.

**The operator must precreate all named state outside Helm.** This includes the Lease, control and
boot ConfigMaps, and every declared source ConfigMap, even unused slots. The chart never looks up,
creates or manages these objects, either as hooks or managed release resources. There is no automatic
bootstrap mode; `ha.createResources` is rejected. Missing objects fail runtime startup, never trigger
workload creation. Helm's installing principal needs no access to the named mutable state; the
separate provisioning principal needs permission to create the initial objects.

The following initial-object examples match the values above. They are provisioning inputs for an
external operator, **not chart templates or hooks**. Supply the workload namespace in that operator's
provisioning context. Create only when absent and handle AlreadyExists by preserving the existing
object. Never apply an empty initial object over existing state, delete/recreate it, or add Helm
release ownership metadata. These examples do not provision anything by themselves.

```yaml
apiVersion: coordination.k8s.io/v1
kind: Lease
metadata:
  name: sample-election
spec: {}
---
apiVersion: v1
kind: ConfigMap
metadata:
  name: sample-control
binaryData: {}
---
apiVersion: v1
kind: ConfigMap
metadata:
  name: sample-boot
binaryData: {}
---
apiVersion: v1
kind: ConfigMap
metadata:
  name: sample-source
binaryData: {}
```

State is absent from both rendered hooks and stored release manifests, so chart install, upgrade,
rollback and uninstall have no state-object operations to replay. This is not a `keep`-annotation
guarantee: install hooks can delete an object that appeared after a NotFound lookup. Precreate new
slots before an upgrade and keep names stable. Retain old mappings and objects for intended rollback,
and only roll back to revisions that also leave mutable state outside Helm ownership. Backups,
retention and eventual cleanup are operator responsibilities, independent of the release lifecycle.

HA mounts a ServiceAccount token and injects `HA_NAMESPACE` from `metadata.namespace` and `POD_UID`
from `metadata.uid`; process identity also includes a random nonce. Runtime Role access is limited
to `get/update/patch` over exact `resourceNames` for the Lease and control/boot/source ConfigMaps.
No create/list/watch/delete/Event permission is granted. The chart's ingress-only NetworkPolicy
does not restrict API-server egress. If another policy restricts egress, the operator must supply
working API connectivity; this chart does not invent API-server CIDRs or endpoint discovery.

HA emits the binary's default budgets: Lease 30s, renewal 15s, retry 2s, Kubernetes request 2s,
HTTP 5s, retry-series 3s, whole-flush 8s, fence allowance 2s and release 2s. State document size is
786432 bytes and CAS attempts are 5. Empty `config.sendDrainDeadline` selects 10s for HA and 30s
for HA off. HA overrides use positive integral seconds, with termination grace strictly greater
than drain plus the 6s final renewal/release/fence allowance; the existing 60s grace stays unchanged.
Chart-managed environment variables cannot be shadowed through `extraEnv`.

The binary separately accepts `HA_MODE=lease` with existing local file state. That staging mode is
**not** stateless failover or shared-state continuity and is not a supported two-replica chart
combination. Never attach one ReadWriteOnce claim to two replicas.

An operator configuring the binary directly must pre-create the exact Lease and supply
`HA_LEASE_NAME`, `HA_NAMESPACE` and downward-API `POD_UID`, with a mounted ServiceAccount
token and working API-server egress. Runtime RBAC needs only `get`/`update` for that named
`coordination.k8s.io/leases` resource (`resourceNames`); no create/list/watch/delete or
Event permission is used. Missing/forbidden/malformed Lease data fails startup. File mode
requires no ConfigMaps and rereads its existing control and fetched-source files on
acquisition. No remote git fetch happens during bootstrap. Configuration lists the HA
budget defaults and strict validation inequalities.

Lease loss irreversibly closes local admission and exits immediately without drain or
release. Earlier admitted network requests can still be in flight: this is not an
API-server fencing token and does not promise distributed exclusivity. Planned SIGTERM
stops and joins production, then positively joins a globally capped best-effort drain
while renewal continues. Only then is renewal sealed and the current named Lease holder
cleared with a resourceVersion CAS. A different holder is never cleared, and any missing
join crashes without release. No synthetic, Fleet, state or SM operation follows release.

Defaults require termination grace **greater than 16s**: 10s global drain + 2s final
renewal join + 2s release + 2s allowance; the chart's existing 60s remains unchanged.
Handoff is restart semantics: counters/histograms and in-memory shape/queue state reset,
unfinished traces/RUM sessions and queued data may truncate, and fixture identities stay
stable. Crash takeover may take the 30s Lease duration plus acquisition/preparation. Do
not promise zero gaps, duplication or data loss. HA uploads and automatic SM provisioning
remain unavailable; the binary suppresses unsupported SM emission without artifact I/O.
Process telemetry/profiles carry immutable `ha.role` tags, including standby exports.

## Optional named ConfigMap state

The binary also accepts `STATE_BACKEND=kubernetes` with either lease mode or HA off. Supply
`HA_NAMESPACE`, `STATE_CONTROL_CONFIGMAP`, `STATE_BOOT_CONFIGMAP` and the JSON
`STATE_GIT_SOURCE_CONFIGMAPS` mapping. Pre-create every named ConfigMap. Each document lives in
`binaryData["document"]`; an empty object is a valid initial document. Runtime uses only named
`get` and resourceVersion-conditional `update` over core `configmaps`. A namespace Role may grant
`get/update/patch` restricted by `resourceNames` to those exact names. No create/list/watch/delete
or Event permissions are needed. A separate provisioning operator, not Helm or the workload,
creates resources and preserves populated objects. The HA chart wires these exact names and
runtime permissions using the operator-owned lifecycle described above.

The projected ServiceAccount credentials and API-server egress must work. Missing, forbidden,
corrupt or oversize documents fail startup; no fallback to local defaults is permitted. The
encoded source cap defaults to 786432 bytes and cannot be widened; aggregate ConfigMap data must
also fit 1 MiB. Fetch status and git files are one source document, joined to current source
configuration by stable ID and fingerprint. Removed/reconfigured sources ignore old blobs.

Kubernetes control/manifest/git state does not need a PVC or disk materialization. HA-off custom
uploads still use the existing local upload directory and require retained disk for continuity;
Kubernetes mode does not make them shared. HA upload and automatic SM exclusions are unchanged.
With HA off, Kubernetes state is still one emitter, not permission to scale replicas. This remains
a standalone binary capability; the chart selects Kubernetes state only for its explicit HA mode.

## Install

```bash
git clone https://github.com/rknightion/synthkit.git
cd synthkit

# Dry run first: builds and renders every estate, pushes nothing, needs no credentials.
helm install synthkit ./charts/synthkit \
  --namespace synthkit --create-namespace \
  --set config.blueprintNames=otlp-native

kubectl -n synthkit rollout status deploy/synthkit
```

Nothing is emitted until `config.blueprintNames` names a blueprint. Empty is setup mode — the same
safe default the binary and Compose use. `*` loads the whole bundled catalogue and does not fit the
chart's default memory limit; see [Resources](#resources-and-the-measurement-behind-them).

To push live, create the credential Secret and flip `config.dryRun`:

```bash
kubectl -n synthkit create secret generic synthkit-data \
  --from-literal=GC_TOKEN=... \
  --from-literal=GC_PROM_RW=... --from-literal=GC_PROM_USER=... \
  --from-literal=GC_OTLP_ENDPOINT=... --from-literal=GC_OTLP_USER=... \
  --from-literal=GC_LOKI=... --from-literal=GC_LOKI_USER=...

helm upgrade synthkit ./charts/synthkit --namespace synthkit --reuse-values \
  --set config.dryRun=false \
  --set credentials.data.existingSecret=synthkit-data
```

---

## Credentials come from Secrets, grouped by destination stack

!!! danger "There is no credential value in this chart"
    No values file field accepts a token, and adding one has no effect. Every credential is
    projected from a Secret you create in the release namespace. A values file is routinely
    committed to a Git repository; a chart that accepted credentials there would make that a
    credential leak.

Credentials are grouped by **where the telemetry lands**, not by convenience, and the chart holds a
fixed table of which environment variable belongs to which group:

| Group | Destination | Variables |
|---|---|---|
| `data` | The synthetic-data stack | `GC_TOKEN`, `GC_PROM_RW`, `GC_PROM_USER`, `GC_OTLP_ENDPOINT`, `GC_OTLP_USER`, `GC_LOKI`, `GC_LOKI_USER`, `GC_PROFILES_URL`, `GC_PROFILES_USER` |
| `selfObs` | A **separate** self-observability stack | `GC_SELF_OTLP_ENDPOINT`, `GC_SELF_OTLP_USER`, `GC_SELF_OTLP_PASSWORD`, `GC_SELF_GRAFANA_URL`, `GC_PYROSCOPE_URL`, `GC_PYROSCOPE_USER`, `GC_PYROSCOPE_PASSWORD` |
| `rum` | Faro collector | `GC_FARO_COLLECTOR`, `GC_FARO_APP_KEY` |
| `sm` | Synthetic Monitoring API | `GC_SM_URL`, `GC_SM_TOKEN` |
| `fm` | Fleet Management API | `GC_FM_URL`, `GC_FM_STACK_ID`, `GC_FM_TOKEN` |
| `sigil` | Sigil AI-observability ingest | `GC_SIGIL_ENDPOINT`, `GC_SIGIL_TENANT_ID`, `GC_SIGIL_TOKEN` |
| `control` | The control plane itself | `CONTROL_TOKEN` |
| `git` | Private git blueprint sources | `GIT_TOKEN` |

Two rules are enforced when the chart renders, so a mistake fails `helm install` rather than
producing a running deployment that quietly does the wrong thing:

1. **A variable can only be projected from the group that owns it.** Listing
   `GC_SELF_OTLP_PASSWORD` under `credentials.data` fails the render.
2. **`credentials.selfObs.existingSecret` may not be the same Secret object as any synthetic-data
   group's.** Sharing one Secret is exactly how `GC_TOKEN` ends up authenticating
   self-observability, which the [architecture](architecture.md) forbids: the generator's own
   telemetry ships to a different stack with a different token so it never intermingles with the
   synthetic data.

```yaml
selfObs:
  enabled: true
credentials:
  data:
    existingSecret: synthkit-data       # the synthetic-data stack
  selfObs:
    existingSecret: synthkit-selfobs    # a DIFFERENT stack, a DIFFERENT token
    keys:
      GC_SELF_OTLP_ENDPOINT: GC_SELF_OTLP_ENDPOINT
      GC_SELF_OTLP_USER: GC_SELF_OTLP_USER
      GC_SELF_OTLP_PASSWORD: GC_SELF_OTLP_PASSWORD
```

The `keys` map is `ENV_VAR: secretKey`, defaulting to identity, so a Secret whose keys are named
after the environment variables needs no `keys` block. A projected key the Secret does not carry
leaves the pod in `CreateContainerConfigError` — deliberate, so a typo cannot become a silently
unauthenticated lane. Helm deep-merges maps, so removing one of the chart's default entries needs an
explicit `KEY: null` rather than just omitting it.

`extraEnv` exists for names synthkit does not own, such as the operator-defined `token_env_var` a
private [git blueprint source](custom-blueprints.md) names. Using it for a name synthkit's own
configuration owns fails the render, so it cannot route a credential around the table above.

---

## The control plane is closed by default

Outside Kubernetes, synthkit refuses to serve the control plane on a non-loopback address without
both `CONTROL_TOKEN` and `CONTROL_EXPOSURE_ACK`. The chart keeps that friction rather than
dissolving it into a Service.

**Default: closed.** The container binds `127.0.0.1:8088`, no Service or Ingress is rendered, and a
default-deny-ingress NetworkPolicy is applied. Reach the operator UI without opening anything:

```bash
kubectl -n synthkit port-forward deploy/synthkit 8088:8088
open http://127.0.0.1:8088/control/ui
```

Port-forwarding runs inside the pod's own network namespace, so it reaches the loopback listener
without making the pod reachable from any other pod.

**Opening it takes three deliberate steps**, and any subset fails the render:

```yaml
credentials:
  control:
    existingSecret: synthkit-control     # 1. a Secret carrying CONTROL_TOKEN
    keys:
      CONTROL_TOKEN: CONTROL_TOKEN
controlPlane:
  exposure:
    ack: tls-proxy                       # 2. exactly "trusted-network" or "tls-proxy"
  service:
    enabled: true                        # 3. the Service itself
```

The acknowledgement means the same thing it means everywhere else: `trusted-network` asserts that
plaintext HTTP stays on an isolated trusted path, `tls-proxy` that a trusted proxy terminates TLS in
front of it. No other value is accepted. Setting `ack` also switches the container to bind all
interfaces, so the binary's own startup check re-validates the token and the acknowledgement — the
chart's guard and the binary's guard are independent, and the pod fails closed if either is missing.

!!! note "Why `SYNTHKIT_BIND` is set in a Kubernetes deployment"
    The binary recognises Kubernetes Pods through `KUBERNETES_SERVICE_HOST`, including those running
    under containerd or CRI-O, so the container exposure branch is selected without an explicit
    hint. The chart still sets `SYNTHKIT_BIND` to mirror the host portion of `JSON_HTTP_ADDR` as a
    deliberate belt-and-braces check: both the in-container listener and the effective exposure
    must agree. It never presents a loopback host bind in front of an all-interfaces listener.

The NetworkPolicy is ingress-only, so egress to the telemetry backends is untouched. It is belt and
braces rather than the primary control: enforcement needs a policy-capable CNI, whereas the loopback
bind holds everywhere. Once exposure is acknowledged, `networkPolicy.ingressFrom` narrows who may
reach port 8088; leaving it empty with a `ClusterIP` Service means any pod in the cluster, which is
what a bare ClusterIP implies anyway.

### Control from Grafana without cluster access

An Infinity action can send the control request server-side through Grafana and Private Data Source
Connect (PDC), so the control-plane Service can remain `ClusterIP`. Configure the Infinity datasource
to reach the in-cluster service through PDC and allow only the PDC agent through
`networkPolicy.ingressFrom`. Store the control-plane credential in the Infinity datasource and
enable Grafana's `vizActionsAuth` feature toggle; without the toggle, the actions are unavailable.

Generate the dashboard with `-action-mode infinity` and the datasource's name and UID. In this mode
`-write-base-url` is an absolute HTTP or HTTPS URL without credentials, a query or a fragment. Plain
HTTP is acceptable only for the hop from Grafana through PDC to this `ClusterIP` Service, or from
inside the cluster. The URL must not end in `/control` because generated actions append `/control/...`.
Never expose the control plane over plain HTTP across the public internet; use HTTPS elsewhere.
Anyone who can query the Infinity datasource can issue authenticated control POSTs
and change load or scenarios, even without viewing the dashboard or using its action buttons. Restrict
datasource query access and dashboard visibility to operators. Grafana's Viewer restriction on
dashboard actions does not protect the datasource from direct queries; see [Grafana's feature
note](https://grafana.com/whats-new/2025-09-03-actions-authentication-with-the-infinity-data-source/).

---

## State that has to survive a restart

`/data` is a PersistentVolumeClaim by default, mounted exactly as the Compose bind mount is:

| Path | Contents | Cost of losing it |
|---|---|---|
| `/data/control-state.json` | Control-plane selections: volume multiplier, active scenarios, scaling overrides | Re-select them in the UI |
| `/data/blueprints/` | Custom uploads and git-sourced blueprints (`custom/`, `git/<id>/`, `.boot-manifest.json`) | Re-upload or re-fetch |
| `/data/runtime/` | Synthetic Monitoring ownership ledger, registration, adoption marker, lock, pending journal | **Not recreatable.** Already-provisioned remote resources become foreign until explicitly previewed and adopted |

Decisions the chart makes, and why:

- **`fsGroup: 65532`.** The image is distroless nonroot. Without it, control-state saves fail with
  `permission denied` and every selection is silently lost on restart — the Kubernetes form of the
  ownership trap the Compose deployment documents. If a UI change does not survive a restart, check
  `persist.last_error` in `/control/status`.
- **`Recreate`, not `RollingUpdate`.** A ReadWriteOnce claim cannot be mounted by the incoming pod
  while the outgoing one still holds it, so a rolling upgrade deadlocks.
- **One replica, not configurable.** Two emitters generate the *same* series identities against the
  same backend — duplicate samples and out-of-order writes, not more throughput — and each keeps its
  own cumulative counter state.
- **`helm.sh/resource-policy: keep`.** The claim survives `helm uninstall`. Delete it deliberately.
- **`persistence.enabled: false`** swaps in an emptyDir. It survives a container restart but not a
  reschedule, so it is for throwaway labs only, and the chart refuses to combine it with the
  Synthetic Monitoring provisioner.

A container restart resets counters, which is a clean `rate()` window rather than a fault. No
counter state volume exists or should.

---

## Probes

Both probes run `synthkit -healthcheck`, which is **delivery-aware**: it succeeds only once every
intended lane has completed a current successful push and the state volume has passed an atomic
write probe.

- `startupProbe` allows 120s, covering the 60s construct interval plus a queue flush.
- `readinessProbe` uses the same check, so the pod reports NotReady while a lane has no current push.
- `livenessProbe` is **off by default**. The same delivery-aware check used as liveness turns a
  Grafana Cloud outage into a crash-loop, because a backend that stops accepting writes would then
  look like a broken process. Enable it only if you accept that.

They are `exec` probes rather than `httpGet` because the default bind is loopback and the kubelet
dials the pod IP.

---

## Resources, and the measurement behind them

Estate size drives memory, so the defaults are derived from a measurement rather than from habit.
The method, the samples, and how to re-run it against your own blueprint selection are in
`charts/synthkit/README.md`. In short:

Measured 2026-08-27 at `TICK_DEFAULT=5s` over 10-minute steady-state windows, with heap read from
`/control/health`:

| Selection | Blueprints | Heap floor | Heap peak | Peak RSS | CPU |
|---|---|---|---|---|---|
| `otlp-native` | 1 | 253 MiB | 461 MiB | 534 MiB | 0.43 % of one core |
| `k8s-full-stack,otlp-native,aws-cloudwatch-infra,hostfleet` | 4 | 289 MiB | 522 MiB | 603 MiB | 0.77 % of one core |
| `*` | 26 | 1716 MiB | 3135 MiB | 3615 MiB | 24.5 % of one core |

The shipped defaults — `requests {cpu: 100m, memory: 768Mi}`, `limits {memory: 1Gi}` — cover the
four-blueprint case: the request sits above its measured peak RSS so the pod is not chronically over
request and first in line for eviction, and the limit is about twice its peak heap. For `*`, budget
`requests {cpu: 500m, memory: 3584Mi}` and `limits {memory: 5Gi}`.

Estate cost is not linear in blueprint count. One blueprint to four moved the floor by 36 MiB; all
26 moved it by a factor of six. Measure your own selection rather than interpolating.

Two things make the peak roughly double the live set, and both are worth knowing before you pick a
limit:

- Go's collector targets about twice the live heap by default, so the sawtooth peak is what the
  limit has to accommodate, not the steady figure.
- The delivery queue is sized in **items per sink**, not bytes, and item sizes differ substantially
  between a metric series, a Loki stream, an OTLP resource, a Faro beacon, a profile, and a Sigil
  export. Do not estimate queue memory as capacity times one universal struct size; use the
  pressure-test calculation in [Deployment](deployment.md#capacity-queue-memory-and-container-limits).

There is deliberately **no CPU limit**. synthkit is a fixed-cadence tick loop, so throttling it
lengthens ticks and applies queue backpressure rather than saving anything.

---

## Synthetic Monitoring provisioning

The chart ships the same opt-in, one-shot provisioner the Compose deployment does, with the same
preview-then-apply sequence and the same least privilege — it receives `GC_SM_URL` and `GC_SM_TOKEN`
and no other credential.

```bash
# 1. Preview. No remote writes.
helm upgrade synthkit ./charts/synthkit --namespace synthkit --reuse-values \
  --set smProvision.enabled=true --set credentials.sm.existingSecret=synthkit-sm
kubectl -n synthkit logs job/synthkit-sm-provision-1

# 2. Apply, after reviewing that preview. Bump runId to make it a new Job.
helm upgrade synthkit ./charts/synthkit --namespace synthkit --reuse-values \
  --set smProvision.apply=true --set smProvision.runId=2
kubectl -n synthkit logs job/synthkit-sm-provision-2

# 3. Restart the emitter so the matching registration activates the lane.
kubectl -n synthkit rollout restart deploy/synthkit
```

It is not a Helm hook. Hooks run automatically on install, which would destroy the preview-then-apply
sequence the provisioner is built around. Because it writes to the emitter's ReadWriteOnce claim it
is co-scheduled onto the emitter's node with a pod affinity.

See [Synthetic Monitoring](synthetic-monitoring.md) for what the two phases actually do, and treat
collisions or a pending journal as a hard stop for operator review.

---

## Upgrades

The chart's `appVersion` tracks a synthkit release, and `image.tag` follows it. For a standing
deployment, prefer pinning the verified index digest instead:

```yaml
image:
  digest: sha256:...
```

`helm upgrade` recreates the pod, which resets counters and produces a clean `rate()` window. The
identity-verification workflow in [Deployment](deployment.md#reproducible-upgrade) — verifying the
signature, provenance, reported version and source revision of a candidate image before deploying
it — applies to the digest you pin here just as it does to the Compose selector.

---

## See also

- [Deployment](deployment.md) — the Compose deployment, queue-memory sizing, and image verification
- [Credentials](credentials.md) — what each credential is and where to get it
- [Control Plane](control-plane.md) — the operator UI and HTTP API
- [Instructor control dashboard](tools.md#synthkit-control-dash-control-dashboard-generator) — optional `-layout control-plane` JSON generator; use an operator-only folder and datasource access.
- [Configuration](configuration.md) — every environment variable
- [Kubernetes monitoring deployment permutations](k8s-monitoring-permutations.md) — choose the collector path your blueprint should represent
- `charts/synthkit/README.md` — the full values reference and the resource measurement
