# skcapture — Kubernetes environment inspector

`skcapture` inspects a customer Kubernetes cluster and writes one encrypted inventory file.
The inventory is processed offline by `skforge` (Grafana-side tooling) to produce a synthkit
blueprint. Nothing is sent to Grafana automatically — the encrypted file travels out-of-band.

---

## What skcapture reads

The following table lists every Kubernetes resource kind skcapture reads, the API group, and
the verbs granted by `rbac.yaml`. This is the complete list — nothing else is accessed.

| Resource | API Group | Verbs | Purpose |
|---|---|---|---|
| `nodes` | core/v1 | get, list | Node-group synthesis, provider/region detection |
| `namespaces` | core/v1 | get, list | Namespace inventory, addon fingerprinting |
| `services` | core/v1 | get, list | Call-graph hints, ExternalName DB/cache endpoints |
| `deployments` | apps/v1 | get, list | Workload inventory, addon/Helm detection |
| `statefulsets` | apps/v1 | get, list | Workload inventory |
| `daemonsets` | apps/v1 | get, list | Workload inventory (e.g. Alloy, node exporters) |
| `ingresses` | networking.k8s.io/v1 | get, list | North-south edge detection |

**The default install reads NO Secrets, NO ConfigMaps, and NO Pods.**
This is enforced by RBAC — not just flag discipline. The `rbac.yaml` ClusterRole does not
include secrets, configmaps, or pods. The binary cannot read them even if asked.

skcapture also calls `kubectl version` and `kubectl config current-context` for metadata
(server k8s version and cluster name). Neither requires extra RBAC.

---

## Data-access posture

`skcapture` never captures Secret or arbitrary ConfigMap data values. The former
`--include-secret-data` and `--include-configmap-data` flags were removed because they never
implemented that behavior; no shipped RBAC now promises it.

The optional `rbac-collector-identity.yaml` is deliberately narrower than the base role: it
grants `get`, never `list`, on one named collector release-info ConfigMap in the collector
namespace. That lets the capture read the `cluster` key that the collector itself stamps on real
telemetry. That one value becomes cluster identity rather than captured object data. The grant
permits no Secret access and must be customised to the observed release-info name
and namespace before use.

Addon detection does **not** need secret access. skcapture detects operators via the
`meta.helm.sh/release-name` annotation and `app.kubernetes.io/managed-by=Helm` label on
the workload objects it already fetches, plus well-known namespace and deployment names.

---

## Flags and defaults

| Flag | Default | Description |
|---|---|---|
| `--out` | `capture.age` | Output file path |
| `--passphrase-file` | _(none)_ | Path to a file containing the encryption passphrase. Required unless `--plain`. The file must not be empty. No interactive TTY prompt (skcapture runs in a k8s Job). |
| `--plain` | `false` | Write unencrypted JSON. Mutually exclusive with `--passphrase-file`. Use only for local debugging — never share a plain capture. |
| `--namespaces` | _(all)_ | Comma-separated namespace allow-list. Empty means all namespaces. |
| `--exclude-namespaces` | `kube-system,kube-node-lease,kube-public` | Comma-separated namespace deny-list applied after the allow-list. |
| `--collectors` | `k8s` | Comma-separated list of enabled collectors. Only `k8s` is registered in this release. |
| `--version` | `false` | Print tool version and inventory schema version, then exit. |

---

## Node-group snapshot caveat (karpenter clusters)

skcapture groups nodes by `(instance_type, provisioner, os)` and records the **current node
count** as the `desired` count in the skeleton blueprint. For karpenter-provisioned nodes this
is a **point-in-time snapshot**, not karpenter's configured range or the workload's steady-state
target.

The SE reviewing the generated blueprint should treat karpenter node-group counts as a starting
estimate and adjust `min`/`max`/`desired` to match the customer's actual fleet sizing policy.

---

## Running skcapture

### Option 1: In-cluster Job (recommended)

The in-cluster Job uses the `skcapture` ServiceAccount and never requires kubeconfig credentials
to leave the cluster.

```sh
# 1. Apply base RBAC (always required)
kubectl apply -f deploy/skcapture/rbac.yaml

# 2. Optional but recommended: customise the named release-info target, then
# apply its narrow identity-read grant for an authoritative cluster name.
# The checked-in manifest is intentionally a non-functional template until both
# the Role namespace and resourceNames value match your collector.
kubectl apply -f deploy/skcapture/rbac-collector-identity.yaml

# 3. Create the passphrase Secret (keep the passphrase for decryption)
PASSPHRASE=$(openssl rand -base64 32)
echo "Passphrase (share this with Grafana SE separately): $PASSPHRASE"
kubectl -n skcapture create secret generic skcapture-pass \
  --from-literal=passphrase="$PASSPHRASE"

# 4. Run the Job. The output-hold helper retains the emptyDir for ten minutes;
# do not wait for Job completion before copying, because completion ends both containers.
kubectl apply -f deploy/skcapture/job.yaml

# 5. Retrieve the encrypted capture file
POD=$(kubectl -n skcapture get pods -l job-name=skcapture \
      -o jsonpath='{.items[0].metadata.name}')
kubectl -n skcapture wait --for=jsonpath='{.status.containerStatuses[?(@.name=="skcapture")].state.terminated.reason}'=Completed pod/$POD --timeout=120s
kubectl -n skcapture cp -c output-hold $POD:/out/capture.age ./capture.age

# 6. Clean up
kubectl delete -f deploy/skcapture/job.yaml
kubectl -n skcapture delete secret skcapture-pass
# Optionally remove the namespace and RBAC entirely after the engagement:
# kubectl delete -f deploy/skcapture/rbac.yaml
# kubectl delete -f deploy/skcapture/rbac-collector-identity.yaml
```

### Option 2: Local docker run (uses your kubeconfig)

Use this when you have `kubectl` access from your laptop and prefer not to deploy anything to
the cluster.

```sh
# Build the image locally first
docker build -f Dockerfile.skcapture -t skcapture:dev .

# Run against your current kubectl context (encrypted output)
echo "my-secret-passphrase" > /tmp/pass.txt
docker run --rm \
  -v ~/.kube:/root/.kube:ro \
  -v /tmp/out:/out \
  -v /tmp/pass.txt:/secrets/pass:ro \
  skcapture:dev \
  --out /out/capture.age \
  --passphrase-file /secrets/pass

# Or plain (local inspection only — do not share)
docker run --rm \
  -v ~/.kube:/root/.kube:ro \
  -v /tmp/out:/out \
  skcapture:dev \
  --plain --out /out/capture.json
```

Note: the docker run path uses your local kubectl credentials and inherits the RBAC of your
kubeconfig user. The in-cluster Job path is preferred for auditable, minimal-permission captures.

---

## Handing off the capture to Grafana

1. Send `capture.age` to your Grafana SE via a secure file transfer (do not email).
2. Share the passphrase via a **separate channel** (e.g. encrypted message, password manager
   shared vault). Never put the passphrase in the same message or commit as the capture file.
3. The SE decrypts with `skforge inspect capture.age --key passphrase.txt`, generates a prompt
   with `skforge prompt`, and produces a validated blueprint with `skforge validate`.

---

## Landing the generated blueprint

`skforge` is Grafana-side tooling. It does **not** have filesystem access to your synthkit
deployment and has no `upload` subcommand.

Once the SE has a validated `capture-blueprint.yaml`, they upload it via the synthkit admin UI:

1. Open the synthkit admin UI (typically `http://<host>:9090/control`).
2. Use the **Upload blueprint** form.
3. The namespace is `capture/<customer>` (e.g. `capture/acme`).
4. After upload, restart synthkit to apply (`restart-to-apply`).

The blueprint lives under `capture/<customer>` in the upload namespace — it is **never** written
to the `blueprints/` directory or committed to the synthkit repo.
