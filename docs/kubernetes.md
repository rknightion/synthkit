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
reads no Kubernetes API, needs no RBAC, and its ServiceAccount carries no projected token. The
estates it emits come from blueprints, not from the surrounding cluster.

---

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
    The binary picks which address to validate from a container check that keys on `/.dockerenv`, a
    Docker-specific file a CRI runtime does not create. The chart sets `SYNTHKIT_BIND` to mirror the
    host portion of `JSON_HTTP_ADDR`, so the exposure gate reaches the same verdict on either
    branch. That is why the chart never presents a loopback host bind in front of an all-interfaces
    listener, and why it does not set `SYNTHKIT_IN_CONTAINER` at all.

The NetworkPolicy is ingress-only, so egress to the telemetry backends is untouched. It is belt and
braces rather than the primary control: enforcement needs a policy-capable CNI, whereas the loopback
bind holds everywhere. Once exposure is acknowledged, `networkPolicy.ingressFrom` narrows who may
reach port 8088; leaving it empty with a `ClusterIP` Service means any pod in the cluster, which is
what a bare ClusterIP implies anyway.

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
- [Configuration](configuration.md) — every environment variable
- `charts/synthkit/README.md` — the full values reference and the resource measurement
