---
id: SKT-0054
title: >-
  Resolve the control-plane PENDING rows from observed scheduler,
  controller-manager and etcd evidence
status: In Progress
assignee:
  - '@codex'
created_date: '2026-09-07 08:10'
updated_date: '2026-09-07 09:08'
labels:
  - signals
  - control-plane
  - corpus
dependencies: []
priority: medium
type: feature
ordinal: 150000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
signals/k8s.md (kube-scheduler and kube-controller-manager sections) and signals/k8s-addons.md (etcd) are doc-sourced with every row v: PENDING under cantfind SK-53 and SK-49, on the belief that managed EKS never exposes those components. Two observations now change that. The promoted Rancher (RKE2) capture carries 104 direct-producer scheduler_ and etcd_ families, 14 of them already catalogued and emitted by synthkit and 90 uncatalogued. The lab EKS cluster's k8s-monitoring chart has scraped kube-scheduler and kube-controller-manager through the chart's eks-proxy discovery mode (the EKS control-plane metrics API, Kubernetes 1.28 and above) since 2026-08-11, and both jobs are fresh on the lab's telemetry tenant; only the EKS live read-back producer (cmd/reality-corpus-gcx) does not select those families, so the corpus has no EKS control-plane evidence and SK-53 wrongly says EKS is unreachable. Resolve both from evidence: add read-back selectors for the kube-scheduler and kube-controller-manager jobs, run the read-back once with the operator-selected context, merge the k8s document; compare synthkit's control-plane and etcd emission against the promoted Rancher document and the refreshed EKS document with signal-fidelity; correct synth toward the observed label sets and histogram bounds; flip the 14 catalogued rows from PENDING to observed with substrate provenance; triage the 90 uncatalogued families into the existing k8s and k8s-addons sections the way SKT-0010.04 and SKT-0021.03 did, with a recorded verdict for any family that is debug-only, chart-dropped or not modelled; correct the stale 'managed EKS does not expose' provenance lines and re-resolve SK-53 and SK-49 in cantfind.md. No new signals file: these are upstream Kubernetes control-plane and etcd families that belong to the existing sections; etcd remains self-managed-only evidence.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 cmd/reality-corpus-gcx selects the kube-scheduler and kube-controller-manager jobs, the read-back ran once and reality-corpus/k8s/eks-live-readback.json carries their families with elided identity values, pasted
- [ ] #2 Every previously PENDING kube-scheduler, kube-controller-manager and etcd row that the Rancher or EKS evidence observed is marked observed with substrate provenance, and synth's label sets and histogram bounds for those families match the observation, proven by signal-fidelity with zero exemptions added
- [ ] #3 Each of the 90 uncatalogued scheduler_ and etcd_ families has either a new catalogue row with provenance or a recorded verdict naming why it is not modelled, and the count of each is pasted
- [ ] #4 cantfind.md SK-49 and SK-53 are re-resolved from the observations, and the stale EKS-cannot-expose provenance text in signals/k8s.md and signals/k8s-addons.md is corrected
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [ ] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [ ] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Wave 2026-09-10: add exact scheduler/controller-manager read-back selectors test-first; perform the one authorized read-back; judge the 104 observed control-plane families; correct catalogue, cantfind, and synth only toward observed shapes; run the final gates.
<!-- SECTION:PLAN:END -->
