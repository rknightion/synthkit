# Kubernetes substrate — k8s-monitoring (→ Mimir) — ScopeSubstrate

All families in this file are substrate-scoped: every series carries `cluster` and `k8s_cluster_name`
(both required, same value) and NEVER a `blueprint` label. Substrate identity (cluster name,
node hostnames, AZs) disambiguates between declared clusters; collisions are rejected at load.

Global rules: see [`00-canon.md`](00-canon.md) — scoping `[slug: scoping]`, cardinality `[slug: cardinality]`, shape rules `[slug: shape-rules]`.

*Provenance: live a live reference EKS cluster capture 2026-06-14 (raw audit inventories not retained in-repo) + `internal/construct/k8scluster/{ksm,nodeexporter,cadvisor,kubelet,conformance,helpers}.go`; predecessor SIGNALS §2.8 + `research/apm-spanmetrics-k8s.md` (topology/pod-naming heritage).*

This is the comprehensive metrics DB for everything the `k8s_cluster` construct emits, organised by
**source/job**. Cardinalities cited are the live a live reference EKS cluster counts (3 nodes, OTel-demo workload);
synth scales them with declared node/pod count. Where synth emits a **representative subset** of a
real high-cardinality dimension (network devices, fs mounts, disk devices) it is flagged inline.

**Topology.** Each declared cluster gets a complete k8s-monitoring set.

**Node sizing [slug: k8s-node-floor-env]** *(realism enhancement 2026-06-16):* Node count is env-weight-aware:
`NodesNeeded = max(EnvFloor(weight), ceil(totalPods / 8))` where `EnvFloor` maps env weight to a minimum:
- weight ≥ 1.0 (production): floor **6** nodes
- weight ≥ 0.5 (staging/test): floor **4** nodes
- weight < 0.5 (dev/non-prod, or no weight declared): floor **3** nodes

`totalPods` includes app workload replicas only (substrate pods do not contribute to the node-floor derivation
at resolve time). `fixture.EnvNodeFloor(weight)` is the canonical Go function; `fixture.DeriveNodesFloor`
and `fixture.LiveNodes` use it. `fixture.DeriveNodes` (floor=3) remains available for callers that don't need
env-aware sizing (tests, EC2 construct). At the replicas=2 baseline: a prod cluster has 6 nodes; a dev
cluster has 3 nodes. ⚠ A PRD cluster kept at a back-compat name (`<bp>-eks-1`, not `-prd`) is a known
dashboard-variable dependency.

**kube-system + addon workload inventory [slug: k8s-kube-system-baseline]** *(realism enhancement 2026-06-16):*
The KSM deployment/daemonset inventory now includes the EKS baseline and addon-derived workloads:

*Always present (EKS baseline, regardless of addons):*
- `coredns` — Deployment, ns `kube-system`, 2 replicas
- `metrics-server` — Deployment, ns `kube-system`, 2 replicas
- `aws-node` — DaemonSet, ns `kube-system`, 1 pod/node (VPC CNI agent)
- `kube-proxy` — DaemonSet, ns `kube-system`, 1 pod/node

*Addon-derived (gated by `cluster.addons:` in the blueprint):*
| addon name | workload | kind | replicas |
|---|---|---|---|
| `load_balancer_controller` | `aws-load-balancer-controller` | Deployment | 2 |
| `cluster_autoscaler` | `cluster-autoscaler` | Deployment | 1 |
| `external_dns` | `external-dns` | Deployment | 1 |
| `ebs_csi` | `ebs-csi-controller` | Deployment | 2 |
| `ebs_csi` | `ebs-csi-node` | DaemonSet | 1/node |
| `cert_manager` | `cert-manager`, `cert-manager-webhook`, `cert-manager-cainjector` | Deployment | 1 each |
| `core_dns` / `vpc_cni` | (covered by baseline `coredns`/`aws-node`) | — | — |

`helpers.go` `workloadDeployments(cl)` populates the Deployment map; `substrateDaemonSets(cl)` returns
the DaemonSet list; both read `cl.Addons []string` (populated from `blueprint.ClusterDecl.Addons` by the
resolver). A prod cluster with the full addon set has ≥9 kube-system Deployments + 3 kube-system
DaemonSets + node-exporter DaemonSet in monitoring = ~15 workloads in the KSM inventory.

**Node identity (join seam, I12):** `EKSNode{Hostname, InstanceID, AZ, InstanceType, VCPUs, MemBytes}`;
hostname `ip-10-<o2>-<o3>-<o4>.<region>.compute.internal`; `kube_node_info.provider_id =
aws:///<az>/<instanceID>` is the EC2 join key (§2.1.4).

**Pod naming (deterministic, single source):** `PodSuffix(workload, idx)=fmt("%05x-%d",
hash(workload)&0xfffff, idx)`; `PodName=workload+"-"+PodSuffix`. This single source is shared by
k8s-monitoring series AND OTLP resource metrics (`target_info`, `traces_target_info`) so the
entity/knowledge graph resolves `service→pod` (I16).

**Per-controller pod naming + ownership [slug: k8s-controller-kinds].** Source: live `kubectl`
live capture on a reference EKS cluster, 2026-06-16, plus a
throwaway `gt-job`/`gt-cron` Job/CronJob (created + deleted same session). Each long-lived workload is
owned by exactly ONE controller; the pod name form, `kube_pod_owner.owner_kind` /
`kube_pod_info.created_by_kind`, and `namespace_workload_pod.workload_type` all follow the controller:

| Controller | Pod name | `owner_kind` / `created_by_kind` | `created_by_name` / `owner_name` | `workload_type` | desired count |
|---|---|---|---|---|---|
| **Deployment** | `<name>-<rshash10>-<podhash5>` (e.g. `coredns-6db8d9dc49-6twzr`) | `ReplicaSet` | `<name>-<rshash>` (the ReplicaSet) | `deployment` | `spec.replicas` |
| **StatefulSet** | `<name>-<ordinal>` (e.g. `argocd-application-controller-0`, `alloy-metrics-0/1/2`) | `StatefulSet` | `<name>` | `statefulset` | `spec.replicas` (ordinals `0..N-1`) |
| **DaemonSet** | `<name>-<5char>` (e.g. `aws-node-2g5t7`, `kube-proxy-czzxd`) | `DaemonSet` | `<name>` | `daemonset` | **count of SCHEDULABLE nodes** (NOT spec; selector-matched) |
| **Job** (pod) | `<job>-<5char>` (e.g. `gt-job-r2czp`) | `Job` | `<job>` | — | `spec.completions` |
| **CronJob** → Job | `<cronjob>-<scheduleIndex>` (e.g. `gt-cron-29693797`; index = unix-minutes for `*/1`) | `CronJob` (Job's owner) | `<cronjob>` | — | per `successful_job_history_limit` |

`namespace_workload_pod.workload_type ∈ {deployment, statefulset, daemonset}` (Job/CronJob pods are NOT
in this series — they are not long-lived workloads).

- **DaemonSet desired = count of schedulable nodes** (verified: `aws-node`/`kube-proxy`/`ebs-csi-node`
  desired=3 on a 3-node cluster; node-selector-only DaemonSets `ebs-csi-node-windows`/`dcgm-server`/
  `windows-exporter` desired=**0** — selector matches no nodes). A DaemonSet's replicas scale WITH the
  nodes; they do NOT drive node demand. synth zeroes a daemonset workload's `Replicas` at resolve time
  (so it never inflates the node cascade) and mints one pod per node, named by node hostname.
- **HPA is OPT-IN** (verified): only some Deployments carry one (`scaleTargetRef: Deployment/<name>`);
  infra/operator deployments (cert-manager, argocd, karpenter, coredns) have NONE. synth gates the
  `kube_horizontalpodautoscaler_*` family behind a per-workload opt-in (Deployment/StatefulSet only —
  never DaemonSet/Job).
- **StatefulSet PVC naming = `<volumeClaimTemplate>-<statefulset>-<ordinal>`** (verified:
  `datadir-mysql-app2-mysql-0`, `demo-postgres-1`), one PVC per (template, ordinal). Stateless
  Deployments carry NO PVC by default; a Deployment that does mount a claim uses a single shared
  `<template>-<name>` PVC (not per-ordinal). PVCs are opt-in via declared volume-claim templates.
- **Job pod batch labels** (verified on `gt-job`/`gt-cron` pods): `batch.kubernetes.io/job-name=<job>`,
  `batch.kubernetes.io/controller-uid=<uuid>` (plus the legacy unprefixed `job-name` / `controller-uid`).
  The Job's `spec.selector.matchLabels` = `{batch.kubernetes.io/controller-uid: <uuid>}`. In KSM these
  pod metadata labels surface on **`kube_pod_labels`** (`label_batch_kubernetes_io_*`), NOT on
  `kube_pod_info`. synthkit does not emit `kube_pod_labels` today, so it does NOT emit these labels at
  all — a Job pod's identity rides `kube_pod_info.created_by_kind=Job` + `kube_pod_owner.owner_kind=Job`.
  Adding the `kube_pod_labels` family (with batch labels) is a documented future addition.

---

## The label-pair TYPES (read first) [slug: k8s-label-types]

Every series is a `metric{labelset} value` pair. Five DISTINCT label patterns recur across the five
sources — recognise them and the rest of §2.2 reads cleanly:

| Type | What it is | Where it lives | Example pair |
|---|---|---|---|
| **Ambient target labels** | added by Alloy/scrape relabelling on EVERY series in a job — NOT emitted by the exporter | all jobs | `…{cluster="<cluster>", k8s_cluster_name="<cluster>", job="integrations/kubernetes/kube-state-metrics", instance="10.1.30.7:8080", source="kubernetes"}` |
| **metric-specific dimension labels** | enum/numeric fan-out keys that define one series per value | per metric | `kube_node_status_capacity{resource="pods", unit="integer"} 35` · `kube_node_status_condition{condition="Ready", status="true"} 1` · `node_cpu_seconds_total{cpu="0", mode="idle"}` |
| **histogram `le` buckets** | one cumulative bucket series per upper bound + the `_sum`/`_count` siblings | `*_bucket` | `kubelet_pod_worker_duration_seconds_bucket{operation_type="sync", le="0.5"}` |
| **info-metric identity labels** | high-key-count carrier series, value always `1`, all identity in the labels | `*_info`, `kube_node_labels`, `*build_info` | `kube_node_info{provider_id="aws:///eu-west-1a/i-0f10bea2eb75b5660", kernel_version="6.12.88", …} 1` |
| **`label_*` bags** | one label key per k8s object label — unbounded; provisioner-variant keys | `kube_node_labels`, `*_match_labels`, `*_selector_labels` | `kube_node_labels{label_node_kubernetes_io_instance_type="m6g.large", label_kubernetes_io_arch="arm64", …}` |

- **Ambient set, exact:** `cluster` AND `k8s_cluster_name` (dual, same value — both required), `job`,
  `instance`, `source="kubernetes"`. node-exporter additionally injects the DaemonSet pod labels
  `app="node-exporter"`, `component="metrics"`, `container="node-exporter"`, `pod`,
  `namespace="monitoring"`, `workload="DaemonSet/<name>"`. (Real also carries `asserts_env` /
  `asserts_site` from the Asserts pipeline — synth does NOT emit Asserts labels.)
- **The uid / provider_id correlation keys.** `uid` (k8s object UUID) appears on KSM pod/container/PVC
  series and on OpenCost node/container/pv series — synth derives it deterministically (`podUID(ns,name)`)
  so the SAME pod's `uid` matches across KSM ↔ OpenCost. `provider_id` (`aws:///<az>/<instance-id>`) is
  the EC2 join key shared by `kube_node_info`, the OpenCost node-cost series and §2.1.4 EC2.
- **`label_*` provisioner variant.** Karpenter nodes carry `label_karpenter_sh_nodepool`; EKS
  managed-nodegroup nodes carry `label_eks_amazonaws_com_nodegroup` instead. ⚠ synth currently emits
  `label_eks_amazonaws_com_nodegroup` on `kube_node_labels` (managed-nodegroup variant); the reference cluster is
  Karpenter (`label_karpenter_sh_nodepool`) — known divergence.

**Scrape families & job labels (load-bearing for dashboard variables):**

| Family | `job` | `instance` | Defining convention |
|---|---|---|---|
| kube-state-metrics | `integrations/kubernetes/kube-state-metrics` | `<pod-ip>:8080` (live-verified port 8080) | object-scoped; pod/container/PVC series carry `uid` (genuine UUID v4) |
| cAdvisor | `integrations/kubernetes/cadvisor` | node hostname | container-scoped adds `id,image,name,node,pod,namespace,container`; CPU adds `cpu="total"`; `container_network_*` is pod-scoped (NO `container`, adds `interface`) |
| kubelet | `integrations/kubernetes/kubelet` | node hostname | + `node`; histograms carry `le`; probes sub-family `job=integrations/kubernetes/probes` (`prober_*`); resource sub-family `job=integrations/kubernetes/resources` (`node_cpu_usage_seconds_total`, `node_memory_working_set_bytes`) |
| node-exporter | `integrations/node_exporter` ⚠ **NO `kubernetes/` segment** | node hostname | carries DaemonSet pod labels (see ambient set above) |

> ⚠ **High-card guard.** cAdvisor's `id` (cgroup path) and `name` (full containerd hash) are present on
> every container-scoped series; `prober_probe_total` carries `pod_uid`. These are real but high-card —
> the chart's relabelling is expected to drop `id`/`name` pre-remote-write. synth emits them
> (representative single values) for shape fidelity.

---

## kube-state-metrics — `job=integrations/kubernetes/kube-state-metrics` [slug: k8s-ksm]

Gauges unless marked **(C)** counter. Ambient labels omitted from the per-metric label lists below.

**Node objects** (3 nodes):
| metric | type | metric-specific labels | example pair |
|---|---|---|---|
| `kube_node_info` | info | `node, container_runtime_version, internal_ip, kernel_version, kubelet_version, kubeproxy_version, os_image, provider_id, system_uuid` | `kube_node_info{kernel_version="6.12.88", os_image="Bottlerocket OS 1.62.0 (aws-k8s-1.35)", kubelet_version="v1.35.2-eks-f69f56f", container_runtime_version="containerd://2.1.7+bottlerocket", provider_id="aws:///eu-west-1a/i-0f10bea2eb75b5660"} 1` — plugin-discovery gate |
| `kube_node_created` | gauge | `node` | unix create ts |
| `kube_node_labels` | label-bag | `node` + `label_*` bag | `kube_node_labels{label_node_kubernetes_io_instance_type="m6g.large", label_kubernetes_io_arch="arm64", label_topology_kubernetes_io_zone="eu-west-1a", label_topology_kubernetes_io_region="eu-west-1", label_kubernetes_io_os="linux"} 1` — arch ∈ {amd64,arm64} derived per-node from instance type |
| `kube_node_spec_unschedulable` | gauge | `node` | `0` |
| `kube_node_status_capacity` | gauge | `node, resource, unit` | `kube_node_status_capacity{resource="pods", unit="integer"} 35`; `{resource="cpu", unit="core"}`, `{resource="memory", unit="byte"}`, `{resource="ephemeral_storage", unit="byte"}`, `{resource="hugepages_1Gi"/"hugepages_2Mi"/"hugepages_32Mi"/"hugepages_64Ki", unit="byte"} 0` |
| `kube_node_status_allocatable` | gauge | `node, resource, unit` | same resource/unit set as capacity |
| `kube_node_status_condition` | gauge | `node, condition, status` | `kube_node_status_condition{condition="Ready", status="true"} 1`. condition ∈ {Ready, MemoryPressure, DiskPressure, PIDPressure} + Bottlerocket extras {ContainerRuntimeReady, KernelReady, NetworkingReady, StorageReady} when OSID=bottlerocket; status ∈ {true,false,unknown} (unknown=0 at baseline, matching real KSM's 3-way fan-out) |

`kube_node_status_addresses` | gauge | `node, type, address` | type ∈ {Hostname, InternalDNS, InternalIP}; 3 series/node, value 1.

**Pods** (`uid` on all): `kube_pod_info` (info: `node, uid, created_by_kind="ReplicaSet", created_by_name,
host_ip, host_network, pod_ip, priority_class` ∈ {normal, system-node-critical}),
`kube_pod_owner` (`owner_kind, owner_name, owner_is_controller="true"`), `kube_pod_start_time`,
`kube_pod_restart_policy{type="Always"}`, `kube_pod_status_phase` (⚠ **ALL 5 phases emitted per pod** —
`{phase="Running"} 1` plus `Pending/Failed/Succeeded/Unknown` at 0 — matching real KSM's 93×5 fan-out;
at steady-state ~1% of pods are mid-startup → Pending=1 via `startingUp` churn model),
`kube_pod_status_reason` (5-reason fan-out: `reason` ∈ {Evicted, NodeAffinity, NodeLost, Shutdown,
UnexpectedAdmissionError}, baseline 0).
`kube_pod_status_ready` (3-condition fan-out per pod: `condition` ∈ {true,false,unknown}; ready pod:
`{condition="true"} 1, {false} 0, {unknown} 0`; starting-up or incident-down: `{true} 0, {false} 1,
{unknown} 0`. Source: kube-state-metrics standard; normal-transient reasons confirmed real on the reference cluster
Karpenter capture 2026-06-16: ContainerCreating, PodInitializing).
Container: `kube_pod_container_info` (info:
`container, container_id, image, image_id, image_spec`), `kube_pod_container_resource_requests` /
`_limits` (`resource` ∈ {cpu,memory}, `unit` ∈ {core,byte}, `node`),
`kube_pod_container_status_restarts_total` **(C)**.
`kube_pod_container_status_waiting` (gauge 0/1 per container; 1 when pod is mid-startup).
`kube_pod_container_status_waiting_reason` (gauge; `reason` label; only normal-transient reasons emitted:
`reason` ∈ {ContainerCreating, PodInitializing}; error reasons ImagePullBackOff/CrashLoopBackOff/ErrImagePull
are incident-only and NOT emitted at baseline. Source: kube-state-metrics standard; both reason values
confirmed real on the reference cluster Karpenter capture 2026-06-16).
**Incident-only** (failure-mode gated, not at
baseline): `kube_pod_container_status_last_terminated_reason{reason="OOMKilled"}`.
`namespace_workload_pod{workload="…"} 1` + recording-rule alias
`namespace_workload_pod:kube_pod_owner:relabel` (⚠ the reference cluster does NOT scrape these from the KSM job —
they are recording rules; synth emits them directly).

**Init containers:** `kube_pod_init_container_info`, `_status_ready`, `_status_running`,
`_status_terminated`, `_status_waiting`, `_status_restarts_total`, `_resource_requests`, `_resource_limits`,
`_status_terminated_reason{reason="Completed"}`.

**Workload controllers:**
- **Deployment:** `_spec_replicas`, `_status_replicas`, `_status_replicas_available`, `_status_replicas_updated`,
  `_metadata_generation`, `_status_observed_generation`, `_status_condition` (`condition` ∈ {Available,Progressing},
  `status` ∈ {true,false} + `reason` label e.g. MinimumReplicasAvailable/NewReplicaSetAvailable).
- **ReplicaSet:** `_owner` (`owner_kind="Deployment"`), `_spec_replicas`, `_status_replicas`,
  `_status_ready_replicas`, `_created`, `_metadata_generation`, `_status_observed_generation`,
  `_status_fully_labeled_replicas`, `_status_terminating_replicas` (full real set).
- **StatefulSet:** `_replicas`, `_status_replicas{,_available,_current,_ready,_updated}`, `_metadata_generation`,
  `_status_observed_generation`, `_status_current_revision`, `_status_update_revision`, `_created`,
  `_persistentvolumeclaim_retention_policy` (`when_deleted="Retain", when_scaled="Retain"`).
- **DaemonSet:** `_status_desired_number_scheduled`, `_current_number_scheduled`, `_number_ready`,
  `_number_available`, `_number_misscheduled`, `_number_unavailable`, `_updated_number_scheduled`,
  `_metadata_generation`, `_status_observed_generation`, `_created`.
- **HPA:** `_spec_min_replicas`, `_spec_max_replicas`, `_status_current_replicas`, `_status_desired_replicas`
  (label `horizontalpodautoscaler`).
- **Job / CronJob:** `kube_cronjob_info` (`cronjob, schedule="0 2 * * *", concurrency_policy="Forbid", timezone`),
  `_created`, `_status_active`, `_spec_suspend`, `_spec_successful_job_history_limit`,
  `_spec_failed_job_history_limit`, `_next_schedule_time`, `_status_last_schedule_time`,
  `_metadata_resource_version`; `kube_job_info`, `_owner` (`owner_kind="CronJob", job_name`), `_created`,
  `_spec_completions`, `_spec_parallelism`, `_status_active`, `_status_ready`, `_status_succeeded`,
  `_status_failed`, `_status_start_time`, `_status_completion_time`, `kube_job_complete{condition="true"}`.
  ✅ **Label set + family VERIFIED against a live 2026-06-14 capture** (Jobs/CronJobs created on the
  a live reference EKS cluster lab cluster, captured via gcx, then torn down) — `kube_cronjob_info.timezone` and
  `kube_job_status_ready` were confirmed real and added.
  CronJob-spawned Job NAME = `<cronjob>-<scheduleIndex>` (verified the reference clusternight 2026-06-16 — see
  [slug: k8s-controller-kinds]). The scheduleIndex is real-world unix-minutes, but synth derives it
  from `clusterCreatedUnix` (a STABLE per-cluster value, NOT wall-clock) so the job-series NAME does
  not churn every tick. Each Job also gets pod-level series: `kube_pod_info`/`kube_pod_owner` with
  `owner_kind="Job", owner_name=<job>`, pod name `<job>-<5char>`. The `batch.kubernetes.io/*` pod
  labels are NOT emitted (they belong on `kube_pod_labels`, which this construct does not emit — see
  [slug: k8s-controller-kinds]).

**Namespace / quota / config:** `kube_namespace_status_phase` (`phase` ∈ {Active,Terminating}),
`kube_resourcequota` (`resource, resourcequota, type` ∈ {hard,used}), `kube_configmap_info` +
`_metadata_resource_version`, `kube_secret_metadata_resource_version`.

**Ingress** (synth emits; documented from chart allowlist shape): `kube_ingress_info` (`ingress, ingressclass`),
`kube_ingress_path` (`host, path, path_type, service_name, service_port`), `kube_ingress_labels`,
`kube_ingress_annotations`, `kube_ingress_created`, `kube_ingress_metadata_resource_version`.

**PV / PVC family:** `kube_persistentvolumeclaim_info` (`persistentvolumeclaim, storageclass="gp3",
volumemode="Filesystem", volumename`), `_labels`, `_access_mode{access_mode="ReadWriteOnce"}`,
`_resource_requests_storage_bytes`, `_status_phase` (`phase` fan-out ∈ {Bound,Pending,Lost}, value 1 on Bound),
`kube_persistentvolume_status_phase` (`phase` ∈ {Available,Bound,Released,Failed,Pending}, 1 on Bound),
`kube_pod_spec_volumes_persistentvolumeclaims_info` (`pod, persistentvolumeclaim, volume`). PVC/PV names join
to kubelet `kubelet_volume_stats_*` and OpenCost `pv_hourly_cost`/`pod_pvc_allocation`.

```yaml signals
family: kube_state_metrics
scope: substrate
sink: promrw
labels:
  cluster: <cluster-name>
  k8s_cluster_name: <cluster-name>
  job: integrations/kubernetes/kube-state-metrics
  instance: <pod-ip>:8080
  source: kubernetes
metrics:
  # Node objects
  - {root: kube_node_info, type: gauge, unit: bool, v: ok, note: info-metric; value always 1; identity in labels (provider_id, kernel_version, os_image, kubelet_version, etc.)}
  - {root: kube_node_created, type: gauge, unit: seconds, v: ok}
  - {root: kube_node_labels, type: gauge, unit: bool, v: ok, note: label-bag; label_* keys per node label}
  - {root: kube_node_spec_unschedulable, type: gauge, unit: bool, v: ok}
  - {root: kube_node_status_capacity, type: gauge, unit: varies, v: ok, note: "resource∈{pods,cpu,memory,ephemeral_storage,hugepages_*}; unit∈{integer,core,byte}"}
  - {root: kube_node_status_allocatable, type: gauge, unit: varies, v: ok, note: same resource/unit set as capacity}
  - {root: kube_node_status_condition, type: gauge, unit: bool, v: ok, note: "condition∈{Ready,MemoryPressure,DiskPressure,PIDPressure}+Bottlerocket extras; status∈{true,false,unknown}; 3-way fan-out"}
  - {root: kube_node_status_addresses, type: gauge, unit: bool, v: ok, note: "type∈{Hostname,InternalDNS,InternalIP}; 3 series/node; value 1"}
  # Pod objects
  - {root: kube_pod_info, type: gauge, unit: bool, v: ok, note: "info-metric; uid+node+created_by_kind/name+host_ip+pod_ip+priority_class"}
  - {root: kube_pod_owner, type: gauge, unit: bool, v: ok, note: "owner_kind,owner_name,owner_is_controller"}
  - {root: kube_pod_start_time, type: gauge, unit: seconds, v: ok}
  - {root: kube_pod_restart_policy, type: gauge, unit: bool, v: ok, note: "type=Always"}
  - {root: kube_pod_status_phase, type: gauge, unit: bool, v: ok, note: "ALL 5 phases emitted per pod; Running=1, rest=0"}
  - {root: kube_pod_status_reason, type: gauge, unit: bool, v: ok, note: "5-reason fan-out; baseline 0"}
  - {root: kube_pod_container_info, type: gauge, unit: bool, v: ok, note: "info; container,container_id,image,image_id,image_spec"}
  - {root: kube_pod_container_resource_requests, type: gauge, unit: varies, v: ok, note: "resource∈{cpu,memory}; unit∈{core,byte}"}
  - {root: kube_pod_container_resource_limits, type: gauge, unit: varies, v: ok}
  - {root: kube_pod_container_status_restarts_total, type: counter, unit: count, v: ok}
  - {root: kube_pod_container_status_last_terminated_reason, type: gauge, unit: bool, v: ok, note: "incident-only (failure-mode gated); reason=OOMKilled"}
  - {root: kube_pod_status_ready, type: gauge, unit: bool, v: ok, note: "condition∈{true,false,unknown}; 3-way fan-out per pod; ready=true→1, starting-up/incident-down=false→1; kube-state-metrics standard; normal-transient reasons confirmed the reference cluster Karpenter 2026-06-16"}
  - {root: kube_pod_container_status_waiting, type: gauge, unit: bool, v: ok, note: "0/1 per container; 1 when pod is in startup transient (ContainerCreating/PodInitializing)"}
  - {root: kube_pod_container_status_waiting_reason, type: gauge, unit: bool, v: ok, note: "reason∈{ContainerCreating,PodInitializing} — normal-transient only; confirmed real on the reference cluster Karpenter 2026-06-16; error reasons (ImagePullBackOff/CrashLoopBackOff/ErrImagePull) are incident-only, not emitted at baseline"}
  - {root: namespace_workload_pod, type: gauge, unit: bool, v: ok, note: "raw KSM-style input series; synth emits directly. The k8s-monitoring recording rule consumes it to produce namespace_workload_pod:kube_pod_owner:relabel"}
  - {root: "namespace_workload_pod:kube_pod_owner:relabel", type: gauge, unit: bool, v: ok, note: "RECORDING-RULE output (k8s-monitoring app), NOT synth-emitted — synth pushing it directly created a duplicate-cardinality series that broke vector matching; the deployed rule is the sole producer"}
  # Init containers
  - {root: kube_pod_init_container_info, type: gauge, unit: bool, v: ok}
  - {root: kube_pod_init_container_status_ready, type: gauge, unit: bool, v: ok}
  - {root: kube_pod_init_container_status_running, type: gauge, unit: bool, v: ok}
  - {root: kube_pod_init_container_status_terminated, type: gauge, unit: bool, v: ok}
  - {root: kube_pod_init_container_status_waiting, type: gauge, unit: bool, v: ok}
  - {root: kube_pod_init_container_status_restarts_total, type: counter, unit: count, v: ok}
  - {root: kube_pod_init_container_resource_requests, type: gauge, unit: varies, v: ok}
  - {root: kube_pod_init_container_resource_limits, type: gauge, unit: varies, v: ok}
  - {root: kube_pod_init_container_status_terminated_reason, type: gauge, unit: bool, v: ok, note: "reason=Completed"}
  # Deployment
  - {root: kube_deployment_spec_replicas, type: gauge, unit: count, v: ok}
  - {root: kube_deployment_status_replicas, type: gauge, unit: count, v: ok}
  - {root: kube_deployment_status_replicas_available, type: gauge, unit: count, v: ok}
  - {root: kube_deployment_status_replicas_updated, type: gauge, unit: count, v: ok}
  - {root: kube_deployment_metadata_generation, type: gauge, unit: count, v: ok}
  - {root: kube_deployment_status_observed_generation, type: gauge, unit: count, v: ok}
  - {root: kube_deployment_status_condition, type: gauge, unit: bool, v: ok, note: "condition∈{Available,Progressing}; status∈{true,false}; reason label"}
  # ReplicaSet
  - {root: kube_replicaset_owner, type: gauge, unit: bool, v: ok, note: "owner_kind=Deployment"}
  - {root: kube_replicaset_spec_replicas, type: gauge, unit: count, v: ok}
  - {root: kube_replicaset_status_replicas, type: gauge, unit: count, v: ok}
  - {root: kube_replicaset_status_ready_replicas, type: gauge, unit: count, v: ok}
  - {root: kube_replicaset_created, type: gauge, unit: seconds, v: ok}
  - {root: kube_replicaset_metadata_generation, type: gauge, unit: count, v: ok}
  - {root: kube_replicaset_status_observed_generation, type: gauge, unit: count, v: ok}
  - {root: kube_replicaset_status_fully_labeled_replicas, type: gauge, unit: count, v: ok}
  - {root: kube_replicaset_status_terminating_replicas, type: gauge, unit: count, v: ok}
  # StatefulSet
  - {root: kube_statefulset_replicas, type: gauge, unit: count, v: ok}
  - {root: kube_statefulset_status_replicas, type: gauge, unit: count, v: ok}
  - {root: kube_statefulset_status_replicas_available, type: gauge, unit: count, v: ok}
  - {root: kube_statefulset_status_replicas_current, type: gauge, unit: count, v: ok}
  - {root: kube_statefulset_status_replicas_ready, type: gauge, unit: count, v: ok}
  - {root: kube_statefulset_status_replicas_updated, type: gauge, unit: count, v: ok}
  - {root: kube_statefulset_metadata_generation, type: gauge, unit: count, v: ok}
  - {root: kube_statefulset_status_observed_generation, type: gauge, unit: count, v: ok}
  - {root: kube_statefulset_status_current_revision, type: gauge, unit: bool, v: ok}
  - {root: kube_statefulset_status_update_revision, type: gauge, unit: bool, v: ok}
  - {root: kube_statefulset_created, type: gauge, unit: seconds, v: ok}
  - {root: kube_statefulset_persistentvolumeclaim_retention_policy, type: gauge, unit: bool, v: ok, note: "when_deleted=Retain,when_scaled=Retain"}
  # DaemonSet
  - {root: kube_daemonset_status_desired_number_scheduled, type: gauge, unit: count, v: ok}
  - {root: kube_daemonset_status_current_number_scheduled, type: gauge, unit: count, v: ok}
  - {root: kube_daemonset_status_number_ready, type: gauge, unit: count, v: ok}
  - {root: kube_daemonset_status_number_available, type: gauge, unit: count, v: ok}
  - {root: kube_daemonset_status_number_misscheduled, type: gauge, unit: count, v: ok}
  - {root: kube_daemonset_status_number_unavailable, type: gauge, unit: count, v: ok}
  - {root: kube_daemonset_status_updated_number_scheduled, type: gauge, unit: count, v: ok}
  - {root: kube_daemonset_metadata_generation, type: gauge, unit: count, v: ok}
  - {root: kube_daemonset_status_observed_generation, type: gauge, unit: count, v: ok}
  - {root: kube_daemonset_created, type: gauge, unit: seconds, v: ok}
  # HPA
  - {root: kube_horizontalpodautoscaler_spec_min_replicas, type: gauge, unit: count, v: ok}
  - {root: kube_horizontalpodautoscaler_spec_max_replicas, type: gauge, unit: count, v: ok}
  - {root: kube_horizontalpodautoscaler_status_current_replicas, type: gauge, unit: count, v: ok}
  - {root: kube_horizontalpodautoscaler_status_desired_replicas, type: gauge, unit: count, v: ok}
  # Job / CronJob (✅ live-verified 2026-06-14)
  - {root: kube_cronjob_info, type: gauge, unit: bool, v: ok, note: "cronjob,schedule,concurrency_policy,timezone — timezone live-confirmed"}
  - {root: kube_cronjob_created, type: gauge, unit: seconds, v: ok}
  - {root: kube_cronjob_status_active, type: gauge, unit: count, v: ok}
  - {root: kube_cronjob_spec_suspend, type: gauge, unit: bool, v: ok}
  - {root: kube_cronjob_spec_successful_job_history_limit, type: gauge, unit: count, v: ok}
  - {root: kube_cronjob_spec_failed_job_history_limit, type: gauge, unit: count, v: ok}
  - {root: kube_cronjob_next_schedule_time, type: gauge, unit: seconds, v: ok}
  - {root: kube_cronjob_status_last_schedule_time, type: gauge, unit: seconds, v: ok}
  - {root: kube_cronjob_metadata_resource_version, type: gauge, unit: count, v: ok}
  - {root: kube_job_info, type: gauge, unit: bool, v: ok}
  - {root: kube_job_owner, type: gauge, unit: bool, v: ok, note: "owner_kind=CronJob,job_name"}
  - {root: kube_job_created, type: gauge, unit: seconds, v: ok}
  - {root: kube_job_spec_completions, type: gauge, unit: count, v: ok}
  - {root: kube_job_spec_parallelism, type: gauge, unit: count, v: ok}
  - {root: kube_job_status_active, type: gauge, unit: count, v: ok}
  - {root: kube_job_status_ready, type: gauge, unit: count, v: ok, note: live-confirmed 2026-06-14}
  - {root: kube_job_status_succeeded, type: gauge, unit: count, v: ok}
  - {root: kube_job_status_failed, type: gauge, unit: count, v: ok}
  - {root: kube_job_status_start_time, type: gauge, unit: seconds, v: ok}
  - {root: kube_job_status_completion_time, type: gauge, unit: seconds, v: ok}
  - {root: kube_job_complete, type: gauge, unit: bool, v: ok, note: "condition=true"}
  # Namespace / quota / config
  - {root: kube_namespace_status_phase, type: gauge, unit: bool, v: ok, note: "phase∈{Active,Terminating}"}
  - {root: kube_resourcequota, type: gauge, unit: varies, v: ok, note: "resource,resourcequota,type∈{hard,used}"}
  - {root: kube_configmap_info, type: gauge, unit: bool, v: ok}
  - {root: kube_configmap_metadata_resource_version, type: gauge, unit: count, v: ok}
  - {root: kube_secret_metadata_resource_version, type: gauge, unit: count, v: ok}
  # Ingress
  - {root: kube_ingress_info, type: gauge, unit: bool, v: ok, note: "ingress,ingressclass"}
  - {root: kube_ingress_path, type: gauge, unit: bool, v: ok, note: "host,path,path_type,service_name,service_port"}
  - {root: kube_ingress_labels, type: gauge, unit: bool, v: ok}
  - {root: kube_ingress_annotations, type: gauge, unit: bool, v: ok}
  - {root: kube_ingress_created, type: gauge, unit: seconds, v: ok}
  - {root: kube_ingress_metadata_resource_version, type: gauge, unit: count, v: ok}
  # PV / PVC
  - {root: kube_persistentvolumeclaim_info, type: gauge, unit: bool, v: ok, note: "persistentvolumeclaim,storageclass=gp3,volumemode=Filesystem,volumename"}
  - {root: kube_persistentvolumeclaim_labels, type: gauge, unit: bool, v: ok}
  - {root: kube_persistentvolumeclaim_access_mode, type: gauge, unit: bool, v: ok, note: "access_mode=ReadWriteOnce"}
  - {root: kube_persistentvolumeclaim_resource_requests_storage_bytes, type: gauge, unit: bytes, v: ok}
  - {root: kube_persistentvolumeclaim_status_phase, type: gauge, unit: bool, v: ok, note: "phase∈{Bound,Pending,Lost}; 1 on Bound"}
  - {root: kube_persistentvolume_status_phase, type: gauge, unit: bool, v: ok, note: "phase∈{Available,Bound,Released,Failed,Pending}; 1 on Bound"}
  - {root: kube_pod_spec_volumes_persistentvolumeclaims_info, type: gauge, unit: bool, v: ok, note: "pod,persistentvolumeclaim,volume"}
```

---

## node-exporter — `job=integrations/node_exporter` (NO `kubernetes/` segment) [slug: k8s-node-exporter]

> The `node_*` metric vocabulary + physics is now SHARED via `internal/nodeexp` (this is the k8s
> profile, carrying the DaemonSet pod ambient labels). The standalone (non-k8s) host fleet uses
> the SAME lib — see `signals/host.md [slug: host-node-linux]`.

node-exporter 1.11.1; all series carry the DaemonSet pod ambient labels. ~160 distinct native metrics.

- **Identity (info):** `node_uname_info` (`machine="aarch64", sysname="Linux", release="6.12.88",
  nodename`(=instance)`, domainname="(none)", version="#1 SMP …"`), `node_os_info` (`id="bottlerocket",
  name, pretty_name, version, version_id, variant_id, build_id` — ⚠ synth omits `variant_id`/`build_id`
  for Amazon-Linux platforms, present for Bottlerocket), `node_exporter_build_info`
  (`version, revision, branch, goversion, goos, goarch, tags`).
- **CPU:** `node_cpu_seconds_total` **(C)** `cpu` 0..N-1 × `mode` ∈ {idle,iowait,irq,nice,softirq,steal,
  system,user}; `node_cpu_guest_seconds_total` **(C)** `cpu` × `mode` ∈ {nice,user}; `node_cpu_online` `cpu`.
- **Memory (MemInfo set, 43 flat gauges, no metric-specific labels):** `node_memory_MemTotal_bytes`,
  `_MemFree_bytes`, `_MemAvailable_bytes`, `_Buffers_bytes`, `_Cached_bytes`, `_Active_bytes`,
  `_Active_anon_bytes`, `_Active_file_bytes`, `_Inactive_bytes`/`_anon`/`_file`, `_AnonPages_bytes`,
  `_AnonHugePages_bytes`, `_Mapped_bytes`, `_Shmem_bytes`/`_ShmemHugePages`/`_ShmemPmdMapped`, `_Slab_bytes`,
  `_SReclaimable_bytes`, `_SUnreclaim_bytes`, `_KernelStack_bytes`, `_PageTables_bytes`, `_Percpu_bytes`,
  `_Dirty_bytes`, `_Writeback_bytes`/`_WritebackTmp`, `_CommitLimit_bytes`, `_Committed_AS_bytes`,
  `_Mlocked_bytes`, `_Unevictable_bytes`, `_Bounce_bytes`, `_NFS_Unstable_bytes`, `_HardwareCorrupted_bytes`,
  `_Vmalloc{Total,Used,Chunk}_bytes`, `_Cma{Total,Free}_bytes`, `_HugePages_{Total,Free,Rsvd,Surp}`,
  `_Hugepagesize_bytes`, `_Swap{Total,Free,Cached}_bytes` (=0 — no swap on EKS), `_Zswap_bytes`, `_Zswapped_bytes`.
- **Disk I/O (C, `device`):** `node_disk_{read_bytes,written_bytes,reads_completed,writes_completed,
  read_time_seconds,write_time_seconds,io_time_seconds,io_time_weighted_seconds}_total`. ⚠ synth devices =
  **representative {nvme0n1, nvme1n1, dm-0}**; real adds nvme2n1 (EBS CSI) on one node.
- **Filesystem (`device, fstype, mountpoint`):** `node_filesystem_{avail,size,free}_bytes`, `_files`,
  `_files_free`, `_device_error`, `_readonly`. ⚠ synth emits a **single representative mount**
  `{device="/dev/nvme0n1p1", fstype="ext4", mountpoint="/"}`; real has 7 mounts (ext4 `/boot` `/.bottlerocket`
  + xfs `/var` `/opt` `/local` `/mnt`) + CSI volume mounts. Also emits `node_filesystem_mount_info`
  (`device, major, minor, mountpoint` — no fstype) + `node_filesystem_purgeable_bytes` (`device, fstype, mountpoint`).
- **Network — traffic (C, `device`):** `node_network_{receive,transmit}_bytes_total`, `_packets_total`,
  `_{drop,errs,fifo,compressed}_total`, receive-only `_multicast_total`, transmit-only `_queue_length`.
  Info/state (`device`): `node_network_carrier`, `node_network_up`, `node_network_mtu_bytes`,
  `node_network_speed_bytes` (only speed-bearing devices), `node_network_info`
  (`device, address, adminstate, broadcast, operstate` — e.g. lo: `adminstate="up", operstate="unknown"`).
  ⚠ synth devices = **representative {lo, eth0, eth1, eth2}** (lo lacks speed); real has ~30 (lo + eth0-2 +
  pod-id-link0 + ~25 `eni*` VPC ENIs). synth does NOT emit `node_arp_entries` per-eni spray (one `eth0` series).
- **Netstat (29 flat gauges):** `node_netstat_{Tcp,Udp,Udp6,UdpLite,Icmp,Icmp6,IpExt,TcpExt}_*` — full set
  per audit (e.g. `_Tcp_RetransSegs`, `_TcpExt_TCPSynRetrans`, `_Udp_RcvbufErrors`).
- **Sockstat (18 flat gauges):** `node_sockstat_{TCP,TCP6,UDP,UDP6,RAW,RAW6,FRAG,FRAG6,UDPLITE,UDPLITE6}_*`
  + `_sockets_used` (`_TCP_alloc`, `_TCP_tw`, `_TCP_orphan`, `_TCP_mem`/`_mem_bytes`).
- **Vmstat (7 C):** `node_vmstat_{oom_kill,pgfault,pgmajfault,pgpgin,pgpgout,pswpin,pswpout}`.
- **Timex/time:** `node_timex_{offset,maxerror,estimated_error}_seconds`, `_sync_status`,
  `node_time_zone_offset_seconds{time_zone="UTC"}`.
- **Softnet (per-CPU, `cpu`):** `node_softnet_{processed,dropped,times_squeezed}_total` (C).
- **Conntrack:** `node_nf_conntrack_entries`, `_entries_limit`. **ARP:** `node_arp_entries` (`device`).
- **Load:** `node_load1`, `node_load5`, `node_load15`. **Filefd:** `node_filefd_allocated`, `_maximum`.
- **Misc scalar:** `node_boot_time_seconds`, `node_context_switches_total` (C), `node_intr_total` (C),
  `node_procs_running`, `node_textfile_scrape_error`.
- **process_* self-metrics:** `process_cpu_seconds_total` (C), `process_resident_memory_bytes`,
  `process_open_fds`, `process_max_fds` (full real set).

```yaml signals
family: node_exporter
scope: substrate
sink: promrw
labels:
  cluster: <cluster-name>
  k8s_cluster_name: <cluster-name>
  job: integrations/node_exporter
  instance: <node-hostname>
  source: kubernetes
  app: node-exporter
  component: metrics
  container: node-exporter
  pod: <daemonset-pod-name>
  namespace: monitoring
  workload: DaemonSet/<name>
metrics:
  # Identity / info
  - {root: node_uname_info, type: gauge, unit: bool, v: ok, note: "machine,sysname,release,nodename,domainname,version"}
  - {root: node_os_info, type: gauge, unit: bool, v: ok, note: "id,name,pretty_name,version,version_id; variant_id/build_id omitted for non-Bottlerocket"}
  - {root: node_exporter_build_info, type: gauge, unit: bool, v: ok, note: "version,revision,branch,goversion,goos,goarch,tags"}
  # CPU
  - {root: node_cpu_seconds_total, type: counter, unit: seconds, v: ok, note: "cpu=0..N-1 × mode∈{idle,iowait,irq,nice,softirq,steal,system,user}"}
  - {root: node_cpu_guest_seconds_total, type: counter, unit: seconds, v: ok, note: "cpu × mode∈{nice,user}"}
  - {root: node_cpu_online, type: gauge, unit: bool, v: ok, note: "cpu label"}
  # Memory (43 flat gauges — representative list)
  - {root: node_memory_MemTotal_bytes, type: gauge, unit: bytes, v: ok}
  - {root: node_memory_MemFree_bytes, type: gauge, unit: bytes, v: ok}
  - {root: node_memory_MemAvailable_bytes, type: gauge, unit: bytes, v: ok}
  - {root: node_memory_Buffers_bytes, type: gauge, unit: bytes, v: ok}
  - {root: node_memory_Cached_bytes, type: gauge, unit: bytes, v: ok}
  - {root: node_memory_Active_bytes, type: gauge, unit: bytes, v: ok}
  - {root: node_memory_SwapTotal_bytes, type: gauge, unit: bytes, v: ok, note: "=0 on EKS (no swap)"}
  - {root: node_memory_SwapFree_bytes, type: gauge, unit: bytes, v: ok}
  - {root: node_memory_SwapCached_bytes, type: gauge, unit: bytes, v: ok}
  - {root: node_memory_Zswap_bytes, type: gauge, unit: bytes, v: ok}
  - {root: node_memory_Zswapped_bytes, type: gauge, unit: bytes, v: ok}
  # Disk I/O (representative — full pattern node_disk_*_total, device label)
  - {root: node_disk_read_bytes_total, type: counter, unit: bytes, v: ok, note: "device label; synth devices={nvme0n1,nvme1n1,dm-0}"}
  - {root: node_disk_written_bytes_total, type: counter, unit: bytes, v: ok}
  - {root: node_disk_reads_completed_total, type: counter, unit: count, v: ok}
  - {root: node_disk_writes_completed_total, type: counter, unit: count, v: ok}
  - {root: node_disk_read_time_seconds_total, type: counter, unit: seconds, v: ok}
  - {root: node_disk_write_time_seconds_total, type: counter, unit: seconds, v: ok}
  - {root: node_disk_io_time_seconds_total, type: counter, unit: seconds, v: ok}
  - {root: node_disk_io_time_weighted_seconds_total, type: counter, unit: seconds, v: ok}
  # Filesystem (device,fstype,mountpoint labels)
  - {root: node_filesystem_avail_bytes, type: gauge, unit: bytes, v: ok, note: "synth: single representative mount /dev/nvme0n1p1 ext4 /"}
  - {root: node_filesystem_size_bytes, type: gauge, unit: bytes, v: ok}
  - {root: node_filesystem_free_bytes, type: gauge, unit: bytes, v: ok}
  - {root: node_filesystem_files, type: gauge, unit: count, v: ok}
  - {root: node_filesystem_files_free, type: gauge, unit: count, v: ok}
  - {root: node_filesystem_device_error, type: gauge, unit: bool, v: ok}
  - {root: node_filesystem_readonly, type: gauge, unit: bool, v: ok}
  - {root: node_filesystem_mount_info, type: gauge, unit: bool, v: ok, note: "device,major,minor,mountpoint; no fstype"}
  - {root: node_filesystem_purgeable_bytes, type: gauge, unit: bytes, v: ok}
  # Network traffic (C, device label; synth devices={lo,eth0,eth1,eth2})
  - {root: node_network_receive_bytes_total, type: counter, unit: bytes, v: ok}
  - {root: node_network_transmit_bytes_total, type: counter, unit: bytes, v: ok}
  - {root: node_network_receive_packets_total, type: counter, unit: count, v: ok}
  - {root: node_network_transmit_packets_total, type: counter, unit: count, v: ok}
  - {root: node_network_receive_drop_total, type: counter, unit: count, v: ok}
  - {root: node_network_transmit_drop_total, type: counter, unit: count, v: ok}
  - {root: node_network_receive_errs_total, type: counter, unit: count, v: ok}
  - {root: node_network_transmit_errs_total, type: counter, unit: count, v: ok}
  - {root: node_network_receive_fifo_total, type: counter, unit: count, v: ok}
  - {root: node_network_transmit_fifo_total, type: counter, unit: count, v: ok}
  - {root: node_network_receive_compressed_total, type: counter, unit: count, v: ok}
  - {root: node_network_transmit_compressed_total, type: counter, unit: count, v: ok}
  - {root: node_network_receive_multicast_total, type: counter, unit: count, v: ok}
  - {root: node_network_transmit_queue_length, type: gauge, unit: count, v: ok}
  # Network info/state (device label)
  - {root: node_network_carrier, type: gauge, unit: bool, v: ok}
  - {root: node_network_up, type: gauge, unit: bool, v: ok}
  - {root: node_network_mtu_bytes, type: gauge, unit: bytes, v: ok}
  - {root: node_network_speed_bytes, type: gauge, unit: bytes_per_second, v: ok, note: "only speed-bearing devices"}
  - {root: node_network_info, type: gauge, unit: bool, v: ok, note: "device,address,adminstate,broadcast,operstate"}
  # Netstat (29 flat gauges; pattern node_netstat_<Proto>_*)
  - {root: node_netstat_Tcp_RetransSegs, type: gauge, unit: count, v: ok, note: "representative; full set per audit"}
  - {root: node_netstat_TcpExt_TCPSynRetrans, type: gauge, unit: count, v: ok}
  - {root: node_netstat_Udp_RcvbufErrors, type: gauge, unit: count, v: ok}
  # Sockstat (18 flat gauges; pattern node_sockstat_*)
  - {root: node_sockstat_sockets_used, type: gauge, unit: count, v: ok}
  - {root: node_sockstat_TCP_alloc, type: gauge, unit: count, v: ok}
  - {root: node_sockstat_TCP_tw, type: gauge, unit: count, v: ok}
  # Vmstat (7 counters)
  - {root: node_vmstat_oom_kill, type: counter, unit: count, v: ok}
  - {root: node_vmstat_pgfault, type: counter, unit: count, v: ok}
  - {root: node_vmstat_pgmajfault, type: counter, unit: count, v: ok}
  - {root: node_vmstat_pgpgin, type: counter, unit: count, v: ok}
  - {root: node_vmstat_pgpgout, type: counter, unit: count, v: ok}
  - {root: node_vmstat_pswpin, type: counter, unit: count, v: ok}
  - {root: node_vmstat_pswpout, type: counter, unit: count, v: ok}
  # Timex / time
  - {root: node_timex_offset_seconds, type: gauge, unit: seconds, v: ok}
  - {root: node_timex_maxerror_seconds, type: gauge, unit: seconds, v: ok}
  - {root: node_timex_estimated_error_seconds, type: gauge, unit: seconds, v: ok}
  - {root: node_timex_sync_status, type: gauge, unit: bool, v: ok}
  - {root: node_time_zone_offset_seconds, type: gauge, unit: seconds, v: ok, note: "time_zone=UTC"}
  # Softnet (per-CPU, cpu label)
  - {root: node_softnet_processed_total, type: counter, unit: count, v: ok}
  - {root: node_softnet_dropped_total, type: counter, unit: count, v: ok}
  - {root: node_softnet_times_squeezed_total, type: counter, unit: count, v: ok}
  # Conntrack / ARP
  - {root: node_nf_conntrack_entries, type: gauge, unit: count, v: ok}
  - {root: node_nf_conntrack_entries_limit, type: gauge, unit: count, v: ok}
  - {root: node_arp_entries, type: gauge, unit: count, v: ok, note: "device label; synth=one eth0 series (not per-ENI spray)"}
  # Load
  - {root: node_load1, type: gauge, unit: ratio, v: ok}
  - {root: node_load5, type: gauge, unit: ratio, v: ok}
  - {root: node_load15, type: gauge, unit: ratio, v: ok}
  # Filefd
  - {root: node_filefd_allocated, type: gauge, unit: count, v: ok}
  - {root: node_filefd_maximum, type: gauge, unit: count, v: ok}
  # Misc scalar
  - {root: node_boot_time_seconds, type: gauge, unit: seconds, v: ok}
  - {root: node_context_switches_total, type: counter, unit: count, v: ok}
  - {root: node_intr_total, type: counter, unit: count, v: ok}
  - {root: node_procs_running, type: gauge, unit: count, v: ok}
  - {root: node_textfile_scrape_error, type: gauge, unit: bool, v: ok}
  # process_* self-metrics
  - {root: process_cpu_seconds_total, type: counter, unit: seconds, v: ok}
  - {root: process_resident_memory_bytes, type: gauge, unit: bytes, v: ok}
  - {root: process_open_fds, type: gauge, unit: count, v: ok}
  - {root: process_max_fds, type: gauge, unit: count, v: ok}
note: "Integration-allowlist extras (node_md_disks*, node_systemd_*, node_memory_DirectMap*, node_network_transmit_multicast_total) deferred — EKS/Bottlerocket lack them; see upstream node-exporter integration allowlist for non-EKS scope."
```

---

## cAdvisor — `job=integrations/kubernetes/cadvisor`, instance=node hostname [slug: k8s-cadvisor]

> The `container_*`/`machine_*` cAdvisor vocabulary + physics is now SHARED via `internal/nodeexp`
> (this is the k8s profile, which adds `node`/`pod`/`namespace`/`container`). The standalone
> Docker lane uses the SAME lib with the native `name`/`image`/`id` scope only (no k8s labels) —
> see `signals/host.md [slug: host-docker]`.

Container-scoped series add `id` (cgroup path), `image`, `name` (containerd hash), `node`, `pod`,
`namespace`, `container` (high-card `id`/`name` flagged above).

| metric | type | extra labels | note |
|---|---|---|---|
| `container_cpu_usage_seconds_total` | C | `cpu="total"` | ⚠ only `cpu="total"` — no per-core breakdown (normal for containerd/EKS) |
| `container_cpu_cfs_periods_total`, `_throttled_periods_total` | C | — | |
| `container_memory_working_set_bytes`, `_cache`, `_rss`, `_swap`(=0), `_usage_bytes` | gauge | — | |
| `container_fs_reads_bytes_total`, `_writes_bytes_total`, `_reads_total`, `_writes_total` | C | `device` | ⚠ synth device = representative `/dev/nvme0n1p1`; real {nvme0n1, nvme1n1, nvme2n1} |
| `container_network_{receive,transmit}_bytes_total`, `_packets_total`, `_packets_dropped_total` | C | `interface` (NO `container`) | **pod-scoped**; ⚠ synth `interface="eth0"` only; real eth0/eth1/eth2 |
| `machine_memory_bytes` | gauge | `node` (=instance) | 1/node |

⚠ `machine_cpu_cores` is in the chart allowlist but **absent from live data** on this EKS/containerd cluster — synth does NOT emit it (correct).

```yaml signals
family: cadvisor
scope: substrate
sink: promrw
labels:
  cluster: <cluster-name>
  k8s_cluster_name: <cluster-name>
  job: integrations/kubernetes/cadvisor
  instance: <node-hostname>
  source: kubernetes
  # container-scoped series additionally carry: id, image, name, node, pod, namespace, container
  # pod-scoped network series carry: interface (NOT container)
metrics:
  - {root: container_cpu_usage_seconds_total, type: counter, unit: seconds, v: ok, note: "cpu=total only — no per-core breakdown (containerd/EKS)"}
  - {root: container_cpu_cfs_periods_total, type: counter, unit: count, v: ok}
  - {root: container_cpu_cfs_throttled_periods_total, type: counter, unit: count, v: ok}
  - {root: container_memory_working_set_bytes, type: gauge, unit: bytes, v: ok}
  - {root: container_memory_cache, type: gauge, unit: bytes, v: ok}
  - {root: container_memory_rss, type: gauge, unit: bytes, v: ok}
  - {root: container_memory_swap, type: gauge, unit: bytes, v: ok, note: "=0 on EKS"}
  - {root: container_memory_usage_bytes, type: gauge, unit: bytes, v: ok}
  - {root: container_fs_reads_bytes_total, type: counter, unit: bytes, v: ok, note: "device label; synth=/dev/nvme0n1p1"}
  - {root: container_fs_writes_bytes_total, type: counter, unit: bytes, v: ok}
  - {root: container_fs_reads_total, type: counter, unit: count, v: ok}
  - {root: container_fs_writes_total, type: counter, unit: count, v: ok}
  - {root: container_network_receive_bytes_total, type: counter, unit: bytes, v: ok, note: "pod-scoped; interface label (no container); synth=eth0 only"}
  - {root: container_network_transmit_bytes_total, type: counter, unit: bytes, v: ok}
  - {root: container_network_receive_packets_total, type: counter, unit: count, v: ok}
  - {root: container_network_transmit_packets_total, type: counter, unit: count, v: ok}
  - {root: container_network_receive_packets_dropped_total, type: counter, unit: count, v: ok}
  - {root: container_network_transmit_packets_dropped_total, type: counter, unit: count, v: ok}
  - {root: machine_memory_bytes, type: gauge, unit: bytes, v: ok, note: "node label (=instance); 1/node"}
```

---

## kubelet — `job=integrations/kubernetes/kubelet` (+ `/probes`, `/resources`) [slug: k8s-kubelet]

All carry `node`. Histograms emit `_bucket{le}` + `_sum` + `_count`.

- **Counts:** `kubelet_running_pods`, `kubelet_running_containers` (⚠ real adds `container_state` label —
  synth omits), `kubelet_node_name`.
- **Runtime ops (C, `operation_type`):** `kubelet_runtime_operations_total`, `_errors_total`(=0);
  `operation_type` ∈ {container_start, container_stop, create_container, pull_image, remove_container}.
  ⚠ `kubelet_runtime_operations_duration_seconds` histogram does NOT exist (counters only).
- **Histograms (H):** `kubelet_cgroup_manager_duration_seconds` (`operation_type`),
  `kubelet_pleg_relist_duration_seconds`, `kubelet_pleg_relist_interval_seconds`,
  `kubelet_pod_start_duration_seconds`, `kubelet_pod_worker_duration_seconds` (`operation_type`). ✅ SK-7:
  cgroup_manager/pleg_relist/pod_worker use the **Prometheus client default seconds buckets**
  `[0.005,0.01,0.025,0.05,0.1,0.25,0.5,1,2.5,5,10]`; real `kubelet_pod_start_duration_seconds` uses a
  **custom 25-boundary set** `[0.5,1,2,3,4,5,6,8,10,20,30,45,60,120,180,240,300,360,480,600,900,1200,1800,2700,3600]`
  (⚠ synth currently uses the default buckets for pod_start too — known divergence).
- **Volume stats (`namespace, persistentvolumeclaim`):** `kubelet_volume_stats_{capacity,used,available}_bytes`,
  `_inodes`, `_inodes_used`, `_inodes_free` — capacity joins the shared `volEntry` so it agrees with
  `kube_persistentvolumeclaim_resource_requests_storage_bytes`.
- **Certs:** `kubelet_certificate_manager_server_ttl_seconds`, `kubelet_server_expiration_renew_errors`(=0).
  ⚠ real omits `kubelet_certificate_manager_client_ttl_seconds`/`_client_expiration_renew_errors`/
  `kubelet_node_config_error` (synth also omits — matches).
- **Build/storage/volume-manager (live-confirmed additions):** `kubernetes_build_info` (info: `build_date,
  compiler="gc", git_commit, git_tree_state="clean", git_version, go_version, major, minor,
  platform="linux/<arch>"`; 1/node), `volume_manager_total_volumes` (`plugin_name="kubernetes.io/csi",
  state` ∈ {actual_state_of_world, desired_state_of_world}), `storage_operation_duration_seconds_count` (C;
  `operation_name` ∈ {volume_mount, volume_unmount, unmount_device, verify_controller_attached_volume,
  volume_apply_access_control} (a reference cluster EBS-CSI recon 2026-06-16 — legacy in-tree `volume_attach` absent
  from the CSI path), `status="success"`, `volume_plugin="kubernetes.io/csi"`,
  `migrated="false"`). ⚠ real `storage_operation_errors_total` not present (synth omits — matches).
- **Probes sub-family** (`job=integrations/kubernetes/probes`; **gated by `control_plane.kubelet_probes`, chart default OFF**):
  `prober_probe_total` **(C)** (`container, namespace, pod, pod_uid, probe_type, result="successful"`;
  `probe_type` ∈ {readiness, liveness, startup}; ⚠ `pod_uid` high-card), `prober_probe_duration_seconds`
  (H, `container, namespace, pod, probe_type`; default seconds buckets).
- **Resource sub-family** (`job=integrations/kubernetes/resources`): `node_cpu_usage_seconds_total` (C),
  `node_memory_working_set_bytes` (gauge) — node-level only.
- ⚠ `rest_client_requests_total` (`code, method, host`) appears on the REAL kubelet job (card 23) but in
  synth is emitted by the **cluster-autoscaler add-on** (§2.3), not the k8scluster construct.
  `go_goroutines` / `process_*` self-metrics on the real kubelet job are NOT emitted by synth here.

> **Recording rules / scrape meta (NOT construct output).** Under these jobs the live stack also returns
> `up`, `scrape_samples_scraped`, the `node_namespace_pod_container:*` and `cluster:namespace:pod_*` recording
> rules, and `asserts:*` series. These are Prometheus/Asserts pipeline artefacts, not exporter scrape output,
> and synth does not (and should not) emit them.

```yaml signals
family: kubelet
scope: substrate
sink: promrw
labels:
  cluster: <cluster-name>
  k8s_cluster_name: <cluster-name>
  job: integrations/kubernetes/kubelet
  instance: <node-hostname>
  node: <node-hostname>
  source: kubernetes
metrics:
  - {root: kubelet_running_pods, type: gauge, unit: count, v: ok}
  - {root: kubelet_running_containers, type: gauge, unit: count, v: ok, note: "⚠ real adds container_state label; synth omits"}
  - {root: kubelet_node_name, type: gauge, unit: bool, v: ok}
  - {root: kubelet_runtime_operations_total, type: counter, unit: count, v: ok, note: "operation_type∈{container_start,container_stop,create_container,pull_image,remove_container}"}
  - {root: kubelet_runtime_operations_errors_total, type: counter, unit: count, v: ok, note: "=0 at baseline"}
  # ⚠ kubelet_runtime_operations_duration_seconds does NOT exist (runtime ops are counters only)
  - {root: kubelet_cgroup_manager_duration_seconds, type: histogram, unit: seconds, v: ok, buckets: [0.005,0.01,0.025,0.05,0.1,0.25,0.5,1,2.5,5,10], note: "operation_type label; Prometheus default buckets"}
  - {root: kubelet_pleg_relist_duration_seconds, type: histogram, unit: seconds, v: ok, buckets: [0.005,0.01,0.025,0.05,0.1,0.25,0.5,1,2.5,5,10]}
  - {root: kubelet_pleg_relist_interval_seconds, type: histogram, unit: seconds, v: assumed, buckets: [0.005,0.01,0.025,0.05,0.1,0.25,0.5,1,2.5,5,10], note: "buckets assumed (SK-73); SK-7 confirmed defaults for cgroup_manager/pleg_relist(_duration)/pod_worker only, not the _interval variant"}
  - {root: kubelet_pod_start_duration_seconds, type: histogram, unit: seconds, v: assumed, buckets: [0.005,0.01,0.025,0.05,0.1,0.25,0.5,1,2.5,5,10], note: "⚠ synth uses default buckets; real uses custom 25-boundary set [0.5..3600] — KNOWN DIVERGENCE (SK-73)"}
  - {root: kubelet_pod_worker_duration_seconds, type: histogram, unit: seconds, v: ok, buckets: [0.005,0.01,0.025,0.05,0.1,0.25,0.5,1,2.5,5,10], note: "operation_type label"}
  - {root: kubelet_volume_stats_capacity_bytes, type: gauge, unit: bytes, v: ok, note: "namespace,persistentvolumeclaim labels"}
  - {root: kubelet_volume_stats_used_bytes, type: gauge, unit: bytes, v: ok}
  - {root: kubelet_volume_stats_available_bytes, type: gauge, unit: bytes, v: ok}
  - {root: kubelet_volume_stats_inodes, type: gauge, unit: count, v: ok}
  - {root: kubelet_volume_stats_inodes_used, type: gauge, unit: count, v: ok}
  - {root: kubelet_volume_stats_inodes_free, type: gauge, unit: count, v: ok}
  - {root: kubelet_certificate_manager_server_ttl_seconds, type: gauge, unit: seconds, v: ok}
  - {root: kubelet_server_expiration_renew_errors, type: gauge, unit: count, v: ok, note: "=0 at baseline"}
  - {root: kubernetes_build_info, type: gauge, unit: bool, v: ok, note: "info; build_date,compiler=gc,git_commit,git_tree_state=clean,git_version,go_version,major,minor,platform=linux/<arch>; 1/node"}
  - {root: volume_manager_total_volumes, type: gauge, unit: count, v: ok, note: "plugin_name=kubernetes.io/csi; state∈{actual_state_of_world,desired_state_of_world}"}
  - {root: storage_operation_duration_seconds_count, type: counter, unit: count, v: ok, note: "operation_name∈{volume_mount,volume_unmount,unmount_device,verify_controller_attached_volume,volume_apply_access_control} (a reference cluster EBS-CSI recon 2026-06-16; legacy volume_attach absent); status=success; volume_plugin=kubernetes.io/csi; migrated=false"}

# Probes sub-family (job=integrations/kubernetes/probes)
# prober_probe_total: counter; container,namespace,pod,pod_uid(⚠ high-card),probe_type,result=successful; probe_type∈{readiness,liveness,startup}
# prober_probe_duration_seconds: histogram; container,namespace,pod,probe_type; default seconds buckets

# Resource sub-family (job=integrations/kubernetes/resources)
# node_cpu_usage_seconds_total: counter; node-level only
# node_memory_working_set_bytes: gauge; node-level only
```

---

## k8s-monitoring conformance / discovery series (I22) ✅ [slug: k8s-conformance]

The vendor Kubernetes Monitoring app lights up only with these exact discovery series. Constructs own
version canonicalization:

- `grafana_kubernetes_monitoring_build_info{cluster, k8s_cluster_name,
  job="integrations/kubernetes/kubernetes_monitoring_telemetry", instance="grafana-k8s-monitoring",
  namespace="monitoring", version}` = 1 (✅ observed real version=`4.1.5` — the Helm chart version,
  deployment-specific). Plugin status: `topk(1, group by(version)(…) OR vector(0))`>0.
- `alloy_build_info{cluster, k8s_cluster_name, job="integrations/alloy", instance="<pod-ip>:12345",
  namespace="infra", version, revision, goversion}` = 1. ⚠ `version` **MUST start with `"v"`** (status
  check regex `version=~"v.+"`). Emitted for EVERY enabled cluster. ✅ observed real values (staff stack):
  version=`v1.16.3`, revision=`1e2007e`, goversion=`go1.26.3` (real k8s-monitoring Alloy pods live in
  `namespace="monitoring"`; synthkit's fake-collector Alloy in §6 uses `namespace="infra"`).
- **OpenCost** (`job="integrations/opencost"`, instance = first node hostname). Gauges unless **(C)**.
  Metric-specific labels in `{}`; `uid` (k8s UUID) and `provider_id` join to KSM / EC2:
  - `opencost_build_info{version, revision}` (✅ observed real version=`1.120.3`/`85c87a3`; ⚠ synth emits
    constants `1.120.2`/`cb9a0cb` — known divergence).
  - `kubecost_cluster_info{provider="AWS", provisioner="EKS", region, id, name, clusterprofile="development"}`,
    `kubecost_cluster_management_cost{provisioner_name="EKS"}`, `kubecost_cluster_memory_working_set_bytes`.
  - `kubecost_http_requests_total` **(C)** `{code="200", method="GET", handler}` (handler ∈ {/metrics,/healthz})
    — the ONLY HTTP metric. ✅ SK-30: live OpenCost 1.120.3 does NOT emit `kubecost_http_response_size_bytes`/
    `_http_response_time_seconds` (DROPPED — synth omits, matching).
  - `kubecost_load_balancer_cost{namespace="envoy-gateway-system", service_name, ingress_ip, uid}` (synth IP in
    RFC 6598 100.64/10 space, matching real `100.124.x.y`), `kubecost_network_internet_egress_cost`,
    `_network_region_egress_cost`, `_network_zone_egress_cost` (no metric-specific labels).
  - Node cost (per node, shared label set `{arch, instance_type, node, provider_id, region, uid}`):
    `node_cpu_hourly_cost`, `node_ram_hourly_cost`, `node_total_hourly_cost`, `node_gpu_count`(=0),
    `node_gpu_hourly_cost`(=0), `kubecost_node_is_spot`(=0).
  - Container alloc (per pod `{container, namespace, node, pod, uid}`): `container_cpu_allocation`,
    `container_memory_allocation_bytes`, `container_gpu_allocation`(=0).
  - PV cost: `pv_hourly_cost{persistentvolume, provider_id(=EBS vol-id), volumename, uid}`,
    `pod_pvc_allocation{namespace, persistentvolume, persistentvolumeclaim, pod, uid}` (joins KSM PVC + kubelet volume_stats).
  - `label_*` bags (one series per workload): `deployment_match_labels{deployment, namespace, uid}`,
    `service_selector_labels{service, namespace, uid}`, `statefulSet_match_labels{statefulSet, namespace, uid}` —
    each plus a representative `label_app_kubernetes_io_name`/`label_app_kubernetes_io_component` pair (⚠ real
    sprays one `label_*` key per actual workload label; synth emits the structural shape, not the full spray).
- **Kepler** (`job="integrations/kepler"`): synth emits `kepler_exporter_build_info{version="release-0.7.12",
  revision}` (1/node), per-node **(C)** `kepler_node_{package,core,dram,platform}_joules_total`, and
  per-container **(C)** `kepler_container_joules_total` + `kepler_container_core_joules_total`
  (`{namespace, pod, container, node}`).
  > ✅ **Live reference (staff stack Kepler v0.8.0).** Real Kepler additionally emits `kepler_node_info`,
  > a `kepler_node_uncore_joules_total`, the full `kepler_container_{dram,package,platform,other,uncore}` set and
  > bpf counters (`kepler_container_{bpf_cpu_time_ms,bpf_*_irq,cpu_cycles,cpu_instructions,…}_total`), with
  > container labels `{container_id, container_name, container_namespace, pod_name, mode∈{dynamic,idle},
  > source="trained_power_model"}` (Graviton/arm64 = estimated, no RAPL). ⚠ synth currently emits the
  > **reduced set above (4 node + 2 container families)** and an older `release-0.7.12` version — known
  > divergence from the v0.8.0 reference. k8s-monitoring v4 `telemetryServices.kepler.deploy=true` deploys the
  > DaemonSet but does NOT auto-wire a scrape (real installs need a podMonitor/annotation scrape on `:9102`).

```yaml signals
family: k8s_conformance
scope: substrate
sink: promrw
labels:
  cluster: <cluster-name>
  k8s_cluster_name: <cluster-name>
  source: kubernetes
metrics:
  # k8s-monitoring telemetry (job=integrations/kubernetes/kubernetes_monitoring_telemetry)
  - {root: grafana_kubernetes_monitoring_build_info, type: gauge, unit: bool, v: ok, note: "instance=grafana-k8s-monitoring; namespace=monitoring; version=Helm chart version e.g. 4.1.5"}
  # Alloy (job=integrations/alloy)
  - {root: alloy_build_info, type: gauge, unit: bool, v: ok, note: "instance=<pod-ip>:12345; namespace=infra; version MUST start with v; revision,goversion"}
  # OpenCost (job=integrations/opencost)
  - {root: opencost_build_info, type: gauge, unit: bool, v: assumed, note: "version,revision; synth=1.120.2/cb9a0cb vs real 1.120.3/85c87a3"}
  - {root: kubecost_cluster_info, type: gauge, unit: bool, v: ok, note: "provider=AWS,provisioner=EKS,region,id,name,clusterprofile=development"}
  - {root: kubecost_cluster_management_cost, type: gauge, unit: dollars_per_hour, v: ok, note: "provisioner_name=EKS"}
  - {root: kubecost_cluster_memory_working_set_bytes, type: gauge, unit: bytes, v: ok}
  - {root: kubecost_http_requests_total, type: counter, unit: count, v: ok, note: "code=200,method=GET,handler∈{/metrics,/healthz}; ONLY HTTP metric (SK-30)"}
  - {root: kubecost_load_balancer_cost, type: gauge, unit: dollars_per_hour, v: ok, note: "namespace,service_name,ingress_ip,uid"}
  - {root: kubecost_network_internet_egress_cost, type: gauge, unit: dollars_per_hour, v: ok}
  - {root: kubecost_network_region_egress_cost, type: gauge, unit: dollars_per_hour, v: ok}
  - {root: kubecost_network_zone_egress_cost, type: gauge, unit: dollars_per_hour, v: ok}
  - {root: node_cpu_hourly_cost, type: gauge, unit: dollars_per_hour, v: ok, note: "arch,instance_type,node,provider_id,region,uid"}
  - {root: node_ram_hourly_cost, type: gauge, unit: dollars_per_hour, v: ok}
  - {root: node_total_hourly_cost, type: gauge, unit: dollars_per_hour, v: ok}
  - {root: node_gpu_count, type: gauge, unit: count, v: ok, note: "=0"}
  - {root: node_gpu_hourly_cost, type: gauge, unit: dollars_per_hour, v: ok, note: "=0"}
  - {root: kubecost_node_is_spot, type: gauge, unit: bool, v: ok, note: "=0"}
  - {root: container_cpu_allocation, type: gauge, unit: cores, v: ok, note: "container,namespace,node,pod,uid"}
  - {root: container_memory_allocation_bytes, type: gauge, unit: bytes, v: ok}
  - {root: container_gpu_allocation, type: gauge, unit: count, v: ok, note: "=0"}
  - {root: pv_hourly_cost, type: gauge, unit: dollars_per_hour, v: ok, note: "persistentvolume,provider_id(=EBS vol-id),volumename,uid"}
  - {root: pod_pvc_allocation, type: gauge, unit: bytes, v: ok, note: "namespace,persistentvolume,persistentvolumeclaim,pod,uid"}
  - {root: deployment_match_labels, type: gauge, unit: bool, v: ok, note: "label_* bag; deployment,namespace,uid"}
  - {root: service_selector_labels, type: gauge, unit: bool, v: ok, note: "label_* bag; service,namespace,uid"}
  - {root: statefulSet_match_labels, type: gauge, unit: bool, v: ok, note: "label_* bag; statefulSet,namespace,uid"}
  # Kepler (job=integrations/kepler)
  - {root: kepler_exporter_build_info, type: gauge, unit: bool, v: assumed, note: "version=release-0.7.12; 1/node; ⚠ real v0.8.0 — known divergence"}
  - {root: kepler_node_package_joules_total, type: counter, unit: joules, v: ok}
  - {root: kepler_node_core_joules_total, type: counter, unit: joules, v: ok}
  - {root: kepler_node_dram_joules_total, type: counter, unit: joules, v: ok}
  - {root: kepler_node_platform_joules_total, type: counter, unit: joules, v: ok}
  - {root: kepler_container_joules_total, type: counter, unit: joules, v: ok, note: "namespace,pod,container,node"}
  - {root: kepler_container_core_joules_total, type: counter, unit: joules, v: ok}
```

---

## k8s events + manifests (→ Loki) — ScopeSubstrate [slug: k8s-events]

*Provenance: live the reference cluster eventhandler 2026-06-15.*

Two stream families per cluster (substrate — no blueprint label):

**Cluster events** (`job="integrations/kubernetes/eventhandler"`): logfmt body carrying
`kind/objectAPIversion/objectRV/eventRV/reportingcontroller/reportinginstance/sourcecomponent/sourcehost/reason/type/count/msg`.
Reason vocab (from live capture): `Scheduled`/`Pulling`/`Pulled`/`Created`/`Started`/`Killing` (Info);
`BackOff`/`FailedScheduling` (Warning — sparse, first pod only); `ScalingReplicaSet` (Deployment, Info).
Kubelet events have `kind=Pod` ONLY (never `kind=Deployment+kubelet`). `level ∈ {Info, Warning}`.
`name` and (for kubelet events) `node` are **STRUCTURED METADATA** on the Loki line (`loki.Line.Meta`),
NOT stream labels. `namespace` is omitted for cluster-scoped (Node) objects.

**Manifests** (`job="integrations/kubernetes/manifests"`): JSON body `{"apiVersion","kind","metadata":{"name","namespace"}}`.
Stream labels include `action` (`manifest`|`created`|`deleted`|`modified` — ⚠ NOT `sync`), `k8s_kind`
(`Pod`|`Deployment`|`StatefulSet`|`DaemonSet`), `k8s_namespace_name`. Structured metadata: `k8s_<kind>_name`
(`k8s_deployment_name` or `k8s_pod_name`).

```yaml signals
family: k8s_events
scope: substrate
sink: loki

# --- Cluster events stream ---
stream_labels:
  cluster: <cluster-name>
  k8s_cluster_name: <cluster-name>
  job: integrations/kubernetes/eventhandler
  service_name: integrations/kubernetes/eventhandler
  source: kubernetes-events
  namespace: <namespace>      # omitted for cluster-scoped objects
  reason: Scheduled|Pulling|Pulled|Created|Started|Killing|BackOff|FailedScheduling|ScalingReplicaSet
  level: Info|Warning         # Warning is sparse (first pod only, idle baseline)
structured_metadata:
  name: <object-name>         # pod name (kubelet events) or deployment (controller events)
  node: <node-hostname>       # kubelet events only; omitted for non-kubelet events
body_fields:
  - logfmt: "kind objectAPIversion objectRV eventRV reportingcontroller reportinginstance sourcecomponent sourcehost reason type count msg"
enums:
  reason_Info: [Scheduled, Pulling, Pulled, Created, Started, Killing, ScalingReplicaSet]
  reason_Warning: [BackOff, FailedScheduling]

# --- Manifests stream ---
stream_labels:
  cluster: <cluster-name>
  k8s_cluster_name: <cluster-name>
  job: integrations/kubernetes/manifests
  service_name: integrations/kubernetes/manifests
  action: manifest|created|deleted|modified   # ⚠ NOT "sync"
  k8s_kind: Pod|Deployment|StatefulSet|DaemonSet
  k8s_namespace_name: <namespace>
structured_metadata:
  k8s_deployment_name: <deploy-name>   # Deployment manifests
  k8s_pod_name: <pod-name>             # Pod manifests
body_fields:
  - JSON: '{"apiVersion":"...","kind":"...","metadata":{"name":"...","namespace":"..."}}'
```

---

## k8s pod logs (→ Loki) — ScopeSubstrate [slug: k8s-pod-logs]

*Provenance: live the reference cluster otel shape (k8s-monitoring opentelemetry method) 2026-06-15. Classic shape doc-sourced.*

Gated by `Features["pod_logs"]` + `PodLogsMethod`. One stream per pod×container, one line each tick.
Two shapes (selected by `PodLogsMethod`):

**otel** (method `"opentelemetry"` or `""`; default; the reference cluster uses this path):
Stream labels carry the OTel k8s semantic convention form. `job` is NOT a stream label (k8s-monitoring
opentelemetry method does not set `job` — collector injects via relabeling if at all).

**classic** (method `"kubernetes_api"` or `"loki"`):
Stream labels carry the classic Alloy `kubernetes_api` shape with `job=<ns>/<container>`;
structured metadata carries `k8s_pod_name`/`pod`/`service_instance_id`.

`objects` method: deferred (not implemented — returns nil).

```yaml signals
family: k8s_pod_logs
scope: substrate
sink: loki

# --- OTel shape (method=opentelemetry; default; the reference cluster live-confirmed) ---
stream_labels_otel:
  cluster: <cluster-name>
  k8s_cluster_name: <cluster-name>
  k8s_namespace_name: <namespace>
  k8s_pod_name: <pod-name>
  k8s_container_name: <container-name>
  k8s_node_name: <node-hostname>
  k8s_deployment_name: <deploy-name>
  app_kubernetes_io_name: <deploy-name>
  service_name: <deploy-name>
  service_namespace: <namespace>
  service_instance_id: "<ns>.<pod-name>.<container>"
  log_iostream: stdout
  logtag: F
  detected_level: info
# NO job label on otel shape

# --- Classic shape (method=kubernetes_api or loki) ---
stream_labels_classic:
  cluster: <cluster-name>
  k8s_cluster_name: <cluster-name>
  namespace: <namespace>
  pod: <pod-name>
  container: <container-name>
  job: "<namespace>/<container>"
  app_kubernetes_io_name: <deploy-name>
  service_name: <deploy-name>
  service_namespace: <namespace>
  service_instance_id: "<ns>.<pod-name>.<container>"
  stream: stdout
  detected_level: info
structured_metadata_classic:
  k8s_pod_name: <pod-name>
  pod: <pod-name>
  service_instance_id: "<ns>.<pod-name>.<container>"
note: "objects method deferred (not implemented)"
```

---

## k8s node logs (→ Loki) — ScopeSubstrate [slug: k8s-node-logs]

*Provenance: live the reference cluster journal (Bottlerocket OS) 2026-06-15. Non-Bottlerocket units doc-sourced (v: PENDING).*

Gated by `Features["node_logs"]`. One stream per node×unit. Journal-unit content varies by node OS.

**Bottlerocket** (live-confirmed): units `host-containers@control.service` (level=`INFO`) and `init.scope`
(level=`UNKNOWN`). **Other OS** (non-Bottlerocket, doc-sourced): units `kubelet.service` + `containerd.service`
(level=`INFO`) — `v: PENDING` (no live Linux EKS node journal capture yet; see cantfind.md SK-50).

Note: `level` values are UPPERCASE (not lowercase). `service_name` mirrors the `unit` label.

```yaml signals
family: k8s_node_logs
scope: substrate
sink: loki
stream_labels:
  cluster: <cluster-name>
  k8s_cluster_name: <cluster-name>
  job: integrations/kubernetes/journal
  instance: <node-hostname>
  source: journal
  unit: <systemd-unit>
  service_name: <systemd-unit>   # mirrors unit
  level: INFO|UNKNOWN            # UPPERCASE; INFO for most units; UNKNOWN for init.scope
  detected_level: info          # Loki lowercase auto-detected level; OMITTED when level=UNKNOWN (live the reference cluster 2026-06-15)
enums:
  unit_bottlerocket: ["host-containers@control.service", "init.scope"]
  level_bottlerocket: {host-containers@control.service: INFO, init.scope: UNKNOWN}
  unit_other: ["kubelet.service", "containerd.service"]   # v: PENDING — non-Bottlerocket
  level_other: INFO    # v: PENDING
note: "Bottlerocket units live-confirmed 2026-06-15; non-Bottlerocket units PENDING (see SK-50)"
```

---

## kube-proxy (→ Mimir) — ScopeSubstrate [slug: k8s-kube-proxy]

*Provenance: live-evidenced the reference cluster 2026-06-15 (EXCEPT histogram bucket boundaries — see cantfind.md SK-51).
Gated by `control_plane.kube_proxy` (default depends on cluster config). Per node,
`instance=<node.PrivateIP>:10249`, `job="integrations/kubernetes/kube-proxy"`.*

```yaml signals
family: kube_proxy
scope: substrate
sink: promrw
labels:
  cluster: <cluster-name>
  k8s_cluster_name: <cluster-name>
  job: integrations/kubernetes/kube-proxy
  instance: <nodePrivateIP>:10249    # one per node
metrics:
  # Histograms — ip_family ∈ {IPv4,IPv6} × histogram; conntrack has no ip_family
  - {root: kubeproxy_sync_proxy_rules_duration_seconds, type: histogram, unit: seconds, v: ok, note: "ip_family ∈ {IPv4,IPv6}; buckets v: PENDING (see SK-51)"}
  - {root: kubeproxy_sync_full_proxy_rules_duration_seconds, type: histogram, unit: seconds, v: ok, note: "ip_family; buckets PENDING"}
  - {root: kubeproxy_sync_partial_proxy_rules_duration_seconds, type: histogram, unit: seconds, v: ok, note: "ip_family; buckets PENDING"}
  - {root: kubeproxy_network_programming_duration_seconds, type: histogram, unit: seconds, v: ok, note: "ip_family; buckets PENDING"}
  - {root: kubeproxy_conntrack_reconciler_sync_duration_seconds, type: histogram, unit: seconds, v: ok, note: "no ip_family; buckets PENDING"}
  # Gauges — ip_family × table ∈ {filter,nat} where applicable
  - {root: kubeproxy_sync_proxy_rules_last_timestamp_seconds, type: gauge, unit: seconds, v: ok, note: "ip_family"}
  - {root: kubeproxy_sync_proxy_rules_last_queued_timestamp_seconds, type: gauge, unit: seconds, v: ok, note: "ip_family"}
  - {root: kubeproxy_sync_proxy_rules_iptables_last, type: gauge, unit: count, v: ok, note: "ip_family × table ∈ {filter,nat}"}
  - {root: kubeproxy_sync_proxy_rules_endpoint_changes_pending, type: gauge, unit: count, v: ok}
  - {root: kubeproxy_sync_proxy_rules_service_changes_pending, type: gauge, unit: count, v: ok}
  # Counters
  - {root: kubeproxy_sync_proxy_rules_iptables_total, type: counter, unit: count, v: ok, note: "ip_family × table"}
  - {root: kubeproxy_sync_proxy_rules_no_local_endpoints_total, type: counter, unit: count, v: ok, note: "ip_family × traffic_policy ∈ {external,internal}"}
  - {root: kubeproxy_sync_proxy_rules_endpoint_changes_total, type: counter, unit: count, v: ok}
  - {root: kubeproxy_sync_proxy_rules_service_changes_total, type: counter, unit: count, v: ok}
  - {root: kubeproxy_conntrack_reconciler_deleted_entries_total, type: counter, unit: count, v: ok, note: "=0 at baseline"}
  - {root: kubeproxy_iptables_ct_state_invalid_dropped_packets_total, type: counter, unit: count, v: ok, note: "=0 at baseline"}
  - {root: kubeproxy_iptables_localhost_nodeports_accepted_packets_total, type: counter, unit: count, v: ok, note: "=0 at baseline"}
  - {root: rest_client_requests_total, type: counter, unit: requests, v: ok, note: "code=200,method=GET,host=<apiserver>:443"}
  # Build info
  - {root: kubernetes_build_info, type: gauge, unit: info, v: ok, note: "same label shape as kubelet; build_date,compiler,git_commit,git_tree_state,git_version,go_version,major,minor,platform"}
enums:
  ip_family: [IPv4, IPv6]
  table: [filter, nat]
  traffic_policy: [external, internal]
note: "histogram buckets v: PENDING — see SK-51"
```

---

## kube-apiserver (→ Mimir) — ScopeSubstrate [slug: k8s-apiserver]

*Provenance: ✅ LIVE-CAPTURED on a live reference EKS cluster EKS 2026-06-15 (apiServer scrape enabled — EKS DOES expose
the apiserver `/metrics` endpoint; 430 distinct families seen, synth emits a representative subset). Label
KEYS are live-verified (v: ok); histogram bucket boundaries still PENDING (SK-52). Opt-in via
`control_plane.api_server` (default OFF). `job="integrations/kubernetes/kube-apiserver"`. Real series also
carry ambient `namespace="default"`, `service="kubernetes"`, `instance=<apiserverIP>:443`, `source` — synth
emits the representative job/instance form.*

```yaml signals
family: kube_apiserver
scope: substrate
sink: promrw
labels:
  cluster: <cluster-name>
  k8s_cluster_name: <cluster-name>
  job: integrations/kubernetes/kube-apiserver
  instance: kubernetes.default.svc:443
metrics:
  - {root: apiserver_request_total, type: counter, unit: requests, v: ok, note: "live labels: verb,code,component=apiserver,group(empty→omitted for core),version,resource,scope∈{cluster,namespace,resource}"}
  - {root: apiserver_request_duration_seconds, type: histogram, unit: seconds, v: ok, note: "verb,resource,scope; bucket boundaries PENDING (SK-52)"}
  - {root: apiserver_current_inflight_requests, type: gauge, unit: count, v: ok, note: "request_kind ∈ {mutating,readOnly} (live-confirmed)"}
  - {root: workqueue_adds_total, type: counter, unit: count, v: ok, note: "name=<controller>"}
  - {root: workqueue_depth, type: gauge, unit: count, v: ok}
  - {root: workqueue_queue_duration_seconds, type: histogram, unit: seconds, v: ok, note: "buckets PENDING"}
  - {root: workqueue_work_duration_seconds, type: histogram, unit: seconds, v: ok, note: "buckets PENDING"}
  - {root: rest_client_requests_total, type: counter, unit: requests, v: ok, note: "live: code∈{200,201,403,404,409,429,500,<error>}, method∈{GET,POST,PUT,PATCH,DELETE}, host=<EKS endpoint>"}
  - {root: etcd_request_duration_seconds, type: histogram, unit: seconds, v: ok, note: "live labels: operation,resource (NOT `type`),group(empty→omitted); buckets PENDING"}
note: "label keys live-verified on EKS 2026-06-15 (SK-52); only histogram buckets remain PENDING"
```

---

## kube-scheduler (→ Mimir) — ScopeSubstrate [slug: k8s-scheduler]

*Provenance: doc-sourced (kube-prometheus-stack mixin; managed EKS does not expose scheduler).
All `v: PENDING`. Opt-in via `control_plane.kube_scheduler` (default OFF).
`job="kube-scheduler"`, `instance="kube-scheduler:10259"`.*

```yaml signals
family: kube_scheduler
scope: substrate
sink: promrw
labels:
  cluster: <cluster-name>
  k8s_cluster_name: <cluster-name>
  job: kube-scheduler
  instance: kube-scheduler:10259
metrics:
  - {root: scheduler_scheduling_attempt_duration_seconds, type: histogram, unit: seconds, v: PENDING, note: "profile=default-scheduler,result ∈ {scheduled,unschedulable,error}; buckets PENDING (SK-53)"}
  - {root: scheduler_pending_pods, type: gauge, unit: count, v: PENDING, note: "queue ∈ {active,backoff,unschedulable,gated}"}
  - {root: scheduler_schedule_attempts_total, type: counter, unit: count, v: PENDING, note: "profile,result"}
  - {root: workqueue_depth, type: gauge, unit: count, v: PENDING, note: "name=DynamicConfigMap"}
  - {root: workqueue_adds_total, type: counter, unit: count, v: PENDING}
  - {root: rest_client_requests_total, type: counter, unit: requests, v: PENDING, note: "code,method,host"}
note: "v: PENDING — managed EKS does not expose kube-scheduler; doc-sourced. See SK-53"
```

---

## kube-controller-manager (→ Mimir) — ScopeSubstrate [slug: k8s-controller-manager]

*Provenance: doc-sourced (kube-prometheus-stack mixin; managed EKS does not expose controller-manager).
All `v: PENDING`. Opt-in via `control_plane.kube_controller_manager` (default OFF).
`job="kube-controller-manager"`, `instance="kube-controller-manager:10257"`.*

```yaml signals
family: kube_controller_manager
scope: substrate
sink: promrw
labels:
  cluster: <cluster-name>
  k8s_cluster_name: <cluster-name>
  job: kube-controller-manager
  instance: kube-controller-manager:10257
metrics:
  - {root: workqueue_adds_total, type: counter, unit: count, v: PENDING, note: "name ∈ {node,replicaset,daemonset,deployment,disruption}"}
  - {root: workqueue_depth, type: gauge, unit: count, v: PENDING}
  - {root: workqueue_queue_duration_seconds, type: histogram, unit: seconds, v: PENDING}
  - {root: workqueue_work_duration_seconds, type: histogram, unit: seconds, v: PENDING}
  - {root: workqueue_retries_total, type: counter, unit: count, v: PENDING, note: "=0 at baseline"}
  - {root: rest_client_requests_total, type: counter, unit: requests, v: PENDING, note: "code,method,host"}
enums:
  name: [node, replicaset, daemonset, deployment, disruption]
note: "v: PENDING — managed EKS does not expose kube-controller-manager; doc-sourced. See SK-53"
```

---

## windows_exporter (→ Mimir) — ScopeSubstrate [slug: k8s-windows-exporter]

> The `windows_*` metric vocabulary + physics is now SHARED via `internal/nodeexp` (this is the
> k8s profile, `job="integrations/windows-exporter"` — **hyphen**). The standalone host fleet uses
> the SAME lib with `job="integrations/windows_exporter"` (**underscore**) — see
> `signals/host.md [slug: host-windows]`.

*Provenance: ✅ LIVE-CAPTURED 2026-06-15 from a real k8s-monitoring windows-exporter on a Windows
Server 2022 EKS node (live reference). The chart's DEFAULT enabled collectors are cpu,container,logical_disk,
memory,net,os — so windows_cs_* (computer-system) and windows_system_* collectors are NOT enabled and
their families do NOT exist; windows_os_* exposes only windows_os_info / windows_os_hostname (not
visible_memory). Names + label shapes below are live-verified (v: ok); values are representative.
OS=windows nodes only; `instance=<node-hostname>`; ambient labels only. `job="integrations/windows-exporter"`
(hyphenated). NOTE: getting these to flow end-to-end to Grafana Cloud also needs the Windows host
firewall opened for the exporter port :9182 (the scrape times out otherwise — up=0).*

Note: `kube_node_labels{label_kubernetes_io_os="windows"}` identifies Windows nodes via KSM.

```yaml signals
family: windows_exporter
scope: substrate
sink: promrw
labels:
  cluster: <cluster-name>
  k8s_cluster_name: <cluster-name>
  source: kubernetes
  job: integrations/windows-exporter    # ⚠ hyphenated (load-bearing)
  instance: <node-hostname>             # OS=windows nodes only; NO pod/namespace/container labels
metrics:
  # CPU (cpu collector)
  - {root: windows_cpu_time_total, type: counter, unit: seconds, v: ok, note: "core=\"<group>,<core>\" e.g. \"0,0\" × mode ∈ {idle,user,privileged,interrupt,dpc}"}
  - {root: windows_cpu_logical_processor, type: gauge, unit: count, v: ok, note: "no labels (live; replaces cs-collector windows_cs_logical_processors)"}
  # Memory (memory collector)
  - {root: windows_memory_physical_total_bytes, type: gauge, unit: bytes, v: ok, note: "replaces cs windows_cs_physical_memory_bytes"}
  - {root: windows_memory_available_bytes, type: gauge, unit: bytes, v: ok}
  - {root: windows_memory_physical_free_bytes, type: gauge, unit: bytes, v: ok}
  - {root: windows_memory_committed_bytes, type: gauge, unit: bytes, v: ok}
  # OS (os collector — info only; NO windows_os_visible_memory_bytes)
  - {root: windows_os_info, type: gauge, unit: info, v: ok, note: "=1; labels product,version,major_version,minor_version,build_number,revision"}
  - {root: windows_os_hostname, type: gauge, unit: info, v: ok, note: "=1; label hostname"}
  # Disk (logical_disk collector, per volume)
  - {root: windows_logical_disk_size_bytes, type: gauge, unit: bytes, v: ok, note: "volume label e.g. C:"}
  - {root: windows_logical_disk_free_bytes, type: gauge, unit: bytes, v: ok}
  - {root: windows_logical_disk_read_bytes_total, type: counter, unit: bytes, v: ok, note: "volume label"}
  - {root: windows_logical_disk_write_bytes_total, type: counter, unit: bytes, v: ok}
  # Network (net collector, per NIC — friendly adapter name)
  - {root: windows_net_bytes_received_total, type: counter, unit: bytes, v: ok, note: "nic label = adapter friendly name"}
  - {root: windows_net_bytes_sent_total, type: counter, unit: bytes, v: ok}
  - {root: windows_net_packets_received_total, type: counter, unit: count, v: ok}
  # NOT emitted (collector disabled in default k8s-monitoring): windows_cs_* (cs), windows_system_* (system)
  # NOT modelled: windows_container_* (container collector — pod-scoped, carries high-card container_id)
enums:
  cpu_mode: [idle, user, privileged, interrupt, dpc]
  volume: ["C:"]
  nic: ["Amazon Elastic Network Adapter"]
note: "live-verified 2026-06-15 (SK-54 resolved); default collectors cpu/container/logical_disk/memory/net/os — cs+system NOT enabled"
```

---

## Addon pod correlation — SubstrateWorkloads join model `[slug: k8s-addon-pod-correlation]`

*Provenance: k8saddon helpers (`internal/construct/k8saddon/`), live a live reference EKS cluster capture 2026-06-16. Applies to ALL k8s-addon constructs (certmanager, coredns, extdns, lbc, karpenter, argocd, envoygateway).*

### How addon metrics join to KSM series

Addon constructs emit metrics stamped with the per-pod identity labels below. These labels make the addon metric series JOIN-COMPATIBLE with `kube_pod_*` (KSM) and `kube_pod_container_info` series emitted by the `k8s_cluster` construct:

| Label | Value form | Join target |
|---|---|---|
| `pod` | `<workload>-<hash>` (or `<statefulset>-<ordinal>` for StatefulSets) | `kube_pod_info.pod` |
| `namespace` | ns the addon runs in | `kube_pod_info.namespace` |
| `container` | the scraped container name (see table below) | `kube_pod_container_info.container` |
| `instance` | `<podIP>:<scrapePort>` | `kube_pod_info.pod_ip` (join fragment) |
| `node` | node hostname | `kube_pod_info.node` |

**`kube_pod_info` carries `pod_ip` and `host_ip` labels (live-confirmed 2026-06-16)** — these are the IP fields that enable the `instance=<podIP>:<port>` → `pod_ip` join. `kube_pod_info.pod_ip` is the raw IP (no port); synthkit's `instance` label adds the `:port` suffix per Prometheus scrape convention.

### Per-addon container names (`kube_pod_container_info.container`)

| Addon construct | Deployment/Workload | Container name (scraped) | Notes |
|---|---|---|---|
| `coredns` | `coredns` | `coredns` | One container per pod |
| `lbc` | `aws-load-balancer-controller` | `aws-load-balancer-controller` | |
| `external_dns` | `external-dns` | `external-dns` | |
| `cert_manager` | `cert-manager` | `cert-manager-controller` | ⚠ container ≠ deployment name |
| `cert_manager` | `cert-manager-cainjector` | `cert-manager-cainjector` | |
| `cert_manager` | `cert-manager-webhook` | `cert-manager-webhook` | |
| `karpenter` | `karpenter` | `controller` | ⚠ container ≠ deployment name |
| `argocd` | `argocd-application-controller` | `application-controller` | StatefulSet |
| `argocd` | `argocd-applicationset-controller` | `applicationset-controller` | |
| `argocd` | `argocd-server` | `server` | |
| `argocd` | `argocd-repo-server` | `repo-server` | |
| `argocd` | `argocd-redis` | `redis` (process) + `metrics` (redis_exporter sidecar) | `StampPodsContainer(container="metrics")` for the exporter |
| `envoy_gateway` | `envoy-gateway` | `envoy-gateway` | Control plane |
| `envoy_gateway` | `envoy-<gw-ns>-<gw-name>-<hash>` | `envoy` | Data plane proxy |
| `envoy_gateway` | (same proxy pod) | `shutdown-manager` | Sidecar on data-plane pods |

### `k8saddon` helper stamp functions

- `k8saddon.StampPods(workload)` — stamps `pod`, `namespace`, `instance`, `node` on ALL pods of a workload.
- `k8saddon.StampLeader(workload)` — stamps the leader pod only (single-replica or leader-elected metrics).
- `k8saddon.StampPodsContainer(workload, container)` — stamps ALL pods with an EXPLICIT container label override (use when the scraped container name differs from the pod's primary container, e.g. the argocd redis `metrics` sidecar).

All three return `nil` when `SubstrateWorkloads` is absent in the blueprint (graceful degradation → cluster-scoped series only, no per-pod breakout).

### `kube_pod_info` labels relevant to addon correlation (live-confirmed 2026-06-16)

`kube_pod_info` carries these labels used for addon join:

```yaml signals
family: kube_pod_info_addon_labels
scope: substrate
sink: promrw
note: "Subset of kube_pod_info labels relevant for addon pod correlation"
labels:
  cluster: <cluster>
  k8s_cluster_name: <cluster>
  job: integrations/kubernetes/kube-state-metrics
relevant_fields:
  - pod            # pod name — primary join key
  - namespace      # namespace — combined with pod for uniqueness
  - node           # node hostname — joins to kube_node_info
  - pod_ip         # raw pod IP (no port) — matches the IP part of addon instance=<IP>:<port>
  - host_ip        # node's host IP
  - uid            # pod UUID — joins to kube_pod_container_info
  - created_by_kind  # ReplicaSet | StatefulSet | DaemonSet | Job
  - created_by_name  # the owning controller name
```
