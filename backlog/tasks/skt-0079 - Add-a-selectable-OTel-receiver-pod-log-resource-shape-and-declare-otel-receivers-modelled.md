---
id: SKT-0079
title: >-
  Add a selectable OTel-receiver pod-log resource shape and declare
  otel-receivers modelled
status: Parked
assignee:
  - '@codex'
created_date: '2026-09-11 19:04'
updated_date: '2026-09-11 20:23'
labels: []
dependencies: []
ordinal: 175000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Alloy and OTel receiver deployments have different observed pod-log resource shapes.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 The default and k8s_monitoring collector choices preserve existing output byte-identically; invalid choices fail blueprint load.
- [x] #2 The otel_collector shape retains nine shared keys, adds nine observed OTel keys and omits cluster and app_kubernetes_io_name.
- [ ] #3 Declaring otel-receivers modelled produces zero new unexempted contradictions, without changing the sole exemption or its expected_matches.
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [x] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [x] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Execute the frozen campaign lane contract; root integrates and measures before any claim adjustment, then validates the exact tree and reconciles acceptance.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Root integration: storage 296d83a91c267c9004ffa79e4cd41825a0b0d4e4 CI 34639635537 success; swap c6e9f9ff9926f7171b76952395dd5aacb2909f71 CI 34641719112 success; profile c500ec61618b13d83218d9b1a4292225e46aeac1 CI34642964976 success. Ordered gen-check, SPDX 736 and full just check passed. just gen regenerated the profile field and earlier storage doc index. Dump comparison proves 36 added host pairs; sample-time Kubernetes termination and topology-conflict changes are separately identified. No cloud, capture, cluster, lab run, second nightly dispatch, release retry or PR mutation occurred.
Empty/k8s_monitoring default canonical resource JSON before and after:2162 bytes SHA256 a032a03c2e8390000eb693a517b8388e7fd4505771b315d8d1ba836780ff7818. Fail-first tests covered absent field, wrong profile key set and active Loki combinations. New profile retains every record, uses18-key Deployment union, omits inapplicable ownership/node dimensions, reuses existing native identities and steady-state zero restart baseline. Invalid profile values and active Loki/native-Loki combinations fail load; disabled none/objects remain inert.
Scratch declaration produced exactly one new unexempted contradiction: {"source": "otel-receivers", "finding": {"kind": "unexpected_label_key", "disposition": "contradiction", "signal": "otlp_logs[family=k8s_pod_logs]", "field": "stream_labels", "synth_values": ["app_kubernetes_io_name", "cluster", "k8s.cluster.name", "k8s.container.name", "k8s.deployment.name", "k8s.namespace.name", "k8s.node.name", "k8s.pod.name", "service.instance.id", "service.name", "service.namespace"], "reality_values": ["container.image.name", "container.image.tag", "k8s.cluster.name", "k8s.cluster.uid", "k8s.container.name", "k8s.container.restart_count", "k8s.deployment.name", "k8s.namespace.name", "k8s.node.name", "k8s.pod.name", "k8s.pod.start_time", "k8s.pod.uid", "k8s.replicaset.name", "k8s.replicaset.uid", "service.instance.id", "service.name", "service.namespace", "service.version"]}}
The existing Alloy keys are correct for the preserved default. Main modelled list remains alloy-default and alloy-otlp-podlogs. The sole exemption remains one rule with expected_matches2, byte-unchanged. Resume with explicitly commissioned collector-profile-aware inventory/comparison design that preserves both real shapes; no exemption or deletion of default keys.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Selectable profile landed; AC1/2 proven. AC3 remains unchecked and declaration parked on exactly one source-scoped pod-log key contradiction. Shape alone does not establish whole-permutation acceptance.
<!-- SECTION:FINAL_SUMMARY:END -->
