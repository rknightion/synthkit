---
id: SKT-0028
title: >-
  Three exemption rules exempt a likely defect or modelling gap, not capture
  narrowness
status: To Do
assignee: []
created_date: '2026-08-29 16:49'
labels: []
dependencies: []
priority: medium
type: bug
ordinal: 119000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Three of the thirteen exemption rules do not describe capture narrowness, which is what the other ten legitimately describe. They exempt something that may be a real synthkit defect or a real corpus modelling gap, and each reason is phrased so the capture carries the blame.

**1. `capture-coredns-build-server`** — "The k3d CoreDNS build-info readback does not expose the `server` label that synthkit models for CoreDNS build identity."

The captured `coredns_build_info` in `reality-corpus/k8s-addons/k3d-lab.json` carries `goversion` and no `server`, which matches real CoreDNS build identity. synthkit stamps `server` at `internal/construct/coredns/coredns.go:246`. CoreDNS release notes say the metrics plugin added a `server` label for *served* metrics from 1.1.3, and `coredns_build_info` is registered at startup rather than served per-server, so the one real observation available says synthkit invents this label. Under "correct the synth to reality, never reality to the synth" the observation wins: drop `server` from `coredns_build_info`, and delete the exemption.

**2. `capture-manifest-service-name`** — "The k3d Loki readback does not retain the `service_name` stream label that synthkit sets on the manifest stream."

Genuinely ambiguous and needs a decision rather than an assumption. Loki auto-derives `service_name` at ingest when the sender omits it, so a capture taken at collector egress would never carry it — which makes the exemption defensible. But it equally means synthkit's stream differs from what the real collector sends, and the same finding shows `instance` in reality and not in synth, so the stream is divergent in both directions. Decide whether synthkit should set `service_name` at all, and record the reason.

**3. `capture-k8s-build-info-source`** — "The folded EKS build-info family combines jobs with different source-stamping behavior, so this capture does not retain source on the combined family."

This one is honest about being a corpus limitation, and that is the point: the corpus folds several jobs into one `kubernetes_build_info` family and therefore cannot represent a label that only some of those jobs carry. The exemption hides a modelling gap rather than a synthkit defect. It is the same shape as SKT-0020.05's per-substrate problem, one level down — per job within a folded family.

None of this undermines the gate. The mechanism keeps all three visible with their reasons, which is exactly what the exemption surface exists for, and that visibility is how they were found. But an exemption is a review decision, and these three were not reviewed against the upstream behaviour they assert.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 coredns_build_info no longer carries an invented server label, or evidence is produced that real CoreDNS does carry it
- [ ] #2 Whether synthkit sets service_name on the Loki manifest stream is decided with its reason, and the instance divergence in the same finding is resolved
- [ ] #3 The folded build-info family's inability to represent per-job labels is recorded as a corpus modelling gap rather than left as an exemption
- [ ] #4 Every exemption rule that survives describes capture narrowness, and its reason does not assign to the capture a limitation that belongs to synthkit
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [ ] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [ ] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->
