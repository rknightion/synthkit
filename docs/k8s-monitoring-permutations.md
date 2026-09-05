---
title: Kubernetes monitoring deployment permutations
description: Choose the documented Kubernetes telemetry path that synthkit can model, and find the signal contract for each path.
---

# Kubernetes monitoring deployment permutations

The Grafana Kubernetes monitoring documentation describes five collector paths. They do not all
produce the same metric names, labels, or log attributes. Choose the path that matches the estate
you want synthkit to represent, then set the corresponding switch in the cluster declaration.

These switches select what synthkit emits; they do not install Alloy, an OTel Collector, or a
Prometheus Operator. **Emitted** below means that synthkit has a lane for the documented observable
surface. Prometheus-shaped metric lanes use Remote-Write v2; native OTLP metric lanes use OTLP, and
the two lanes remain distinct.

## The five documented paths

| # | Documented path | synthkit status | Blueprint selection |
|---|---|---|---|
| 1 | Alloy `k8s-monitoring`, Prometheus remote-write, and Loki-native pod logs | **Emitted** | Use `cluster.k8s_monitoring.enabled: true`, `alloy: true`, and `features.cluster_metrics: true`; add `features.pod_logs: true` and `pod_logs_method: loki` for pod logs |
| 2 | Alloy `k8s-monitoring` with `podLogsViaOpenTelemetry` | **Emitted** | Keep the Alloy baseline and set `cluster.k8s_monitoring.features.pod_logs: true` plus `cluster.k8s_monitoring.pod_logs_method: opentelemetry` |
| 3 | OTel Collector with Prometheus exporters | **Not yet emitted** | No supported switch; see [Permutation 3](#3-otel-collector-with-prometheus-exporters) |
| 4 | OTel Collector native receivers | **Emitted for the native metric surface** | Set `cluster.otel.metrics: true`; the reference `k8s-otel-native` blueprint also keeps `k8s_monitoring.features.cluster_metrics: true` |
| 5 | Prometheus remote-write through the Helm Operator path | **Emitted for the captured family subset** | Set `cluster.prometheus_operator_remote_write` with both `prometheus` and `prometheus_replica` |

### 1. Alloy k8s-monitoring with Loki-native pod logs

This is the documented default: the Alloy deployment scrapes the Prometheus-shaped Kubernetes
families and sends pod logs by the Loki-native path. Enable the Alloy baseline with
`cluster.k8s_monitoring.enabled: true`, `cluster.k8s_monitoring.alloy: true`, and
`cluster.k8s_monitoring.features.cluster_metrics: true`. If pod logs are part of the estate, also
enable `cluster.k8s_monitoring.features.pod_logs: true` and select
`cluster.k8s_monitoring.pod_logs_method: loki`.

The authoritative contracts are [signals/k8s.md — slugs `k8s-label-types`, `k8s-ksm`,
`k8s-node-exporter`, `k8s-cadvisor`, `k8s-kubelet`, `k8s-conformance`, `k8s-events`, and
`k8s-pod-logs`](https://github.com/rknightion/synthkit/blob/main/signals/k8s.md). The
`k8s-pod-logs` contract covers the Loki-native transport and its distinction from the OTLP log
transport in permutation 2.

### 2. Alloy k8s-monitoring with podLogsViaOpenTelemetry

This keeps the Alloy Prometheus-shaped metrics lane from permutation 1 and changes the pod-log
transport to OTLP. Select it with `cluster.k8s_monitoring.features.pod_logs: true` and
`cluster.k8s_monitoring.pod_logs_method: opentelemetry`. The log content is the same, but the
observable resource, record, stream-label, and structured-metadata shape is transport-specific.

Use [signals/k8s.md — slug `k8s-pod-logs`](https://github.com/rknightion/synthkit/blob/main/signals/k8s.md)
for the two pod-log transports. The Prometheus-shaped metric families remain under the slugs
listed for permutation 1.

### 3. OTel Collector with Prometheus exporters

This documented OTel Collector configuration uses a Prometheus receiver for kube-state-metrics,
node-exporter, kubelet, and cAdvisor, then sends those Prometheus-shaped metrics by remote-write.
Its logs and cluster events use OTLP. That combination is close to permutation 1 for metric names,
but it is a separate deployment path because its log shape differs.

**Synthkit does not emit this permutation yet.** The repository has a P3 capture definition, but
its metadata is explicitly still `unproven`, and no authoritative P3 signal slug has been admitted.
No switch should therefore be presented as selecting it.
The existing [signals/k8s.md slugs `k8s-ksm`, `k8s-node-exporter`, `k8s-cadvisor`, and
`k8s-kubelet`](https://github.com/rknightion/synthkit/blob/main/signals/k8s.md) are the source for
the shared Prometheus-shaped names only; they do not establish the P3 collector envelope or its OTLP
log shape. The L8 capture must establish that contract before a later emitter can follow it.

### 4. OTel Collector native receivers

This path uses native `kubeletstatsreceiver` and `k8sclusterreceiver` metric sources, so its metric
namespace is different from the `kube_*`, `container_*`, and `node_*` scrape families. Select the
native metric lane with `cluster.otel.metrics: true`. The shipped `k8s-otel-native` reference
blueprint demonstrates that switch.

The switch is additive: it emits the captured native OTLP metric surface while preserving the
existing Prometheus-shaped Kubernetes lane. It therefore gives an operator the native receiver
contract without claiming that synthkit installs or reproduces every collector component in the
documented deployment.

The native metric contract is [signals/otlp-metrics.md — slug
`k8s-native-otlp-metrics`](https://github.com/rknightion/synthkit/blob/main/signals/otlp-metrics.md).
Its capture provenance and the separate native receiver observations are recorded in [signals/k8s.md
— slug `k8s-otel-native-permutation`](https://github.com/rknightion/synthkit/blob/main/signals/k8s.md),
[signals/host.md — slug `host-otel-native-permutation`](https://github.com/rknightion/synthkit/blob/main/signals/host.md),
and [signals/logs.md — slug `logs-otel-native-permutation`](https://github.com/rknightion/synthkit/blob/main/signals/logs.md).

### 5. Prometheus remote-write through the Helm Operator path

This path changes the collector envelope around a reviewed subset of the existing Kubernetes
families. It is not a second Kubernetes catalogue. Select it with the cluster-level
`prometheus_operator_remote_write` block and provide both required identity fields:

```yaml
cluster:
  prometheus_operator_remote_write:
    prometheus: <operator-defined-prometheus-identity>
    prometheus_replica: <operator-defined-replica-identity>
```

The values are collector identity chosen by the operator. They are not inferred from the Alloy
job labels, and the switch does not authorize unreviewed families. The shipped
`k8s-prometheus-operator` reference leaves the Alloy monitoring flags off so the example focuses
on this envelope. The authoritative contract is
[signals/k8s.md — slug `k8s-prometheus-operator-rw`](https://github.com/rknightion/synthkit/blob/main/signals/k8s.md).

## See also

- [Kubernetes](kubernetes.md) — deploy synthkit with the bundled Helm chart
- [Emission Switches](emission-switches.md) — how blueprint switches gate construct lanes
- [Reading the Catalogue](signals.md) — how signal slugs and provenance work
