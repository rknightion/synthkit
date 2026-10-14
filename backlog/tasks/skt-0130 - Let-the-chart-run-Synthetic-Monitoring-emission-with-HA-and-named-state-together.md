---
id: SKT-0130
title: >-
  Let the chart run Synthetic Monitoring emission with HA and named state
  together
status: In Progress
assignee:
  - '@codex'
created_date: '2026-10-12 19:10'
updated_date: '2026-10-14 20:56'
labels: []
dependencies:
  - SKT-0099.01
type: feature
ordinal: 253000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
SKT-0099.01 (diskless SM provisioning Job in the named-state chart) shipped a standalone provisioning Job only. A chart install that enables HA together with Synthetic Monitoring still cannot run SM emission out of the box. The HA emitter Role in charts/synthkit/templates/ha.yaml grants ConfigMap access only to the control, boot and source objects, not the SM aggregate state slot. The emitter does not receive the named SM state environment either, so the combined HA+SM permutation stays excluded and the docs (docs/kubernetes.md, docs/synthetic-monitoring.md) describe the emitter environment and RBAC as separately owned. Deployments that want both features must currently hand-wire that mapping. Activation also needs a published image whose engine matches the chart's SM state contract.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 With HA and Synthetic Monitoring both enabled, the rendered chart gives the emitter the named SM aggregate state slot through its environment, and grants named get/update on that slot only. It grants no create, list or watch, and adds no access to the provisioner Lease or to unrelated ConfigMaps or Secrets.
- [ ] #2 The provisioner Job and the emitters keep distinct Leases and identities. Fail-first rendered RBAC tests prove that neither can mutate the other's Lease or the declarative source input.
- [ ] #3 A local runtime proof (fake SM plus fake Kubernetes, real rendered config) shows the provisioned check state reaching an HA emitter without a shared volume, and preserves operator-owned resources byte-identical. File mode and the standalone Job behaviour are unchanged.
- [ ] #4 Chart docs and the values schema stop describing the HA+SM combination as excluded, and name the minimum image version that carries the matching engine contract.
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [ ] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [ ] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
First reconcile hosted consumer HA/named-state contract read-only. Add generic HA emitter named SM state environment and a separate named get/update RBAC rule, retaining distinct emitter/provisioner Leases and identities and operator-owned resources. Prove rendered permissions fail-first and real rendered-config runtime flow using fake APIs; preserve file and standalone modes. Update docs and establish existing minimum published engine contract. Independent security review, CodeRabbit and full gate before landing.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Generic chart contract reconciled against consuming service read-only; no consumer repository changes. Separate emitter named SM ConfigMap get/update rule, distinct principals and Leases, source-input isolation, explicit preview/apply/restart. Real rendered config -> real HA publication -> provision CLI against fake APIs -> restarted HA RW2 receipt proved configuration version; operator-owned resources byte-identical. CodeRabbit complete0, full focused chart/runtime proofs green. Engine source floor rc.176 and published multiarchitecture index currently verified; no live HA qualification claimed. Final composed gate and exact CI pending.

Final local composed gate did not pass: unchanged control-dashboard race proof reached the 600-second timeout under host load255.01. Ordinary tests, focused chart/runtime proof and final static/chart checks passed. Awaiting owner decision on CI-first landing; local full gate remains failed, not waived green.
<!-- SECTION:NOTES:END -->
