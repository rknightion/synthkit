---
id: SKT-0023
title: k8s pollers stamp source on kube-proxy families where reality has none
status: To Do
assignee: []
created_date: '2026-08-29 09:22'
labels: []
dependencies: []
priority: high
type: bug
ordinal: 114000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
13 of the 67 contradictions, in `signals/k8s.md` on substrate `eks` from `gcx_live_readback`, are one defect: synthkit stamps `source` on kube-proxy families that the real collector does not.

Verified 2026-08-29 across `kubeproxy_iptables_ct_state_invalid_dropped_packets_total`, `kubeproxy_iptables_localhost_nodeports_accepted_packets_total`, `kubeproxy_sync_proxy_rules_endpoint_changes_total` and ten more, each reporting `only-in-synth=[source]` against `reality=[cluster, instance, job, k8s_cluster_name]`.

This is the exact mirror of the k8s-addons defect, where synthkit omits `source` and reality has it — so the two must be fixed together or they will trade places. What the capture actually shows is that `source` is not a universal stamp: real k8s-monitoring applies it per job, and synthkit applies it by a rule that does not match. Establish the real rule from the capture and apply that, rather than adding an exception for kube-proxy.

`kubeproxy_conntrack_reconciler_deleted_entries_total` is in this set with a second, unrelated divergence: reality carries an `ip_family` label synthkit does not. Handle that as a separate finding rather than folding it into the source fix.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 The rule by which real k8s-monitoring stamps source is established from the capture and stated
- [ ] #2 synthkit applies that rule rather than a per-family exception
- [ ] #3 The fix is consistent with the sibling k8s-addons correction, so the two do not trade places
- [ ] #4 The ip_family divergence on kubeproxy_conntrack_reconciler_deleted_entries_total is handled as its own finding
- [ ] #5 signals/k8s.md records the correction with provenance
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 make gate (build vet test race rw-proto-check spdx-check forbidden-words)
- [ ] #2 make blueprint-schema (only if a blueprint field or construct/workload config struct changed)
- [ ] #3 DRY_RUN=true go run ./cmd/synthkit -once -dump — inventory diffed against signals/
<!-- DOD:END -->
