---
id: SKT-0130
title: >-
  Let the chart run Synthetic Monitoring emission with HA and named state
  together
status: To Do
assignee:
  - '@loop-root'
created_date: '2026-10-12 19:10'
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
