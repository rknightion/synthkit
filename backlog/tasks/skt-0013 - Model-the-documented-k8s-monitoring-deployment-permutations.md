---
id: SKT-0013
title: Model the documented k8s-monitoring deployment permutations
status: In Progress
assignee: []
created_date: '2026-08-27 08:29'
updated_date: '2026-08-27 08:35'
labels: []
dependencies: []
priority: high
type: feature
ordinal: 69000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Grafana Cloud documents several distinct ways to get Kubernetes telemetry into a stack, and they produce materially different observable shapes for the same cluster. synthkit models one of them well and one partially. A user deploying synthkit to stand in for their estate needs the permutation THEY run, not the one synthkit happens to default to.

The permutations, from the Grafana Cloud kubernetes-monitoring configuration docs:

1. **Alloy k8s-monitoring, Prometheus remote-write** — the default and what synthkit models today. Captured in the corpus.
2. **podLogsViaOpenTelemetry** — pod logs leave as OTLP rather than Loki-native. Captured, and SKT-0006.05 built the emission lane.
3. **OTel Collector with Prometheus exporters** (`config-other-methods/otel-collector/`) — a Deployment scrapes kube-state-metrics, node-exporter, kubelet and cAdvisor through the Prometheus receiver and ships metrics by remote-write, while logs and cluster events go OTLP. Metric names stay Prometheus-shaped with `job` values like `integrations/kubernetes/cadvisor`, so this is close to permutation 1 on the metrics side and different on the logs side.
4. **OTel Collector native receivers** (`config-other-methods/otel-collector-receivers/`) — a DaemonSet running `otlp`, `hostmetrics` and `filelog`, plus a Deployment running `otlp`, `k8s_cluster` and `k8sobjects`. This is a genuinely different metric NAMESPACE, not a transport change.
5. **Prometheus remote-write via the Helm operator** — tracked separately as its own subtask, because its difference is a label envelope rather than a namespace.

**Why permutation 4 is the priority, and it is not obvious from the list.** It is precisely the deployment SKT-0007.01 cited when it verdicted `k8s_cluster` and `host` OTEL-NATIVE — `k8sclusterreceiver`, `kubeletstatsreceiver` and `hostmetricsreceiver` are its receivers. So capturing it produces the ground truth SKT-0007.04 and SKT-0007.06 need, and it resolves cantfind SK-85, the unconfirmed kubeletstats default-enabled metric set that currently blocks SKT-0007.04 from emitting anything. Capturing it is cheaper than the emission work it unblocks.

The k3d lab is the right vehicle for all of these. They are chart CONFIGURATION, not infrastructure, and the lab is credential-free, disposable, and already wired into the fidelity corpus. Do not reach for a live cluster to answer a question a values file settles.

Corpus entries must be tagged by permutation, not merged into one k8s document — two permutations disagreeing about a metric is a real difference, not a contradiction to resolve.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 The k3d lab can capture each documented permutation, selected explicitly rather than by editing a values file in place
- [ ] #2 Corpus entries record which permutation produced them, so two permutations disagreeing is not read as drift
- [ ] #3 Permutation 4 is captured and cantfind SK-85 is resolved from it
- [ ] #4 Each permutation observable difference from the default is recorded in signals/ with provenance
- [ ] #5 Which permutations synthkit can emit, and which it cannot yet, is stated plainly somewhere a user choosing a deployment will find it
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 make gate (build vet test race rw-proto-check spdx-check forbidden-words)
- [ ] #2 make blueprint-schema (only if a blueprint field or construct/workload config struct changed)
- [ ] #3 DRY_RUN=true go run ./cmd/synthkit -once -dump — inventory diffed against signals/
<!-- DOD:END -->
