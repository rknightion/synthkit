---
id: SKT-0070
title: >-
  Close standalone Docker machine identity gaps from the independent cAdvisor
  capture
status: Parked
assignee:
  - '@codex'
created_date: '2026-09-08 21:15'
updated_date: '2026-09-10 22:07'
labels: []
dependencies: []
priority: medium
ordinal: 166000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
The standalone Docker cAdvisor capture promoted in reality-corpus/host/docker-standalone-cadvisor.json directly observes boot_id and machine_id on machine_memory_bytes. The existing host Docker emitter lacks both keys. This is an independently proven single-producer envelope gap, distinct from the Kubernetes P3 repair. Raw container-label annotations vary with deployed images and must not be made a global catalogue map. Preserve the capture and source the narrow machine identity model before implementation.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 Source the standalone cAdvisor machine descriptor and model boot_id and machine_id only where observed
- [x] #2 Retain failing-before and passing-after proof with negative controls for container siblings and non-Docker paths
- [ ] #3 Inventory and fidelity pass without rewriting evidence, weakening producer comparison, or adding exemptions
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [ ] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [x] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Wave 2026-09-17: L3 sources standalone cAdvisor machine descriptor, proves the observed machine-only identity gap failing first, implements narrowly, and proves sibling/non-Docker rejection. Root applies signal proposals and runs integrated inventory/fidelity/gates.

Wave 2026-09-18: L2 recaptures the pinned alloy-default permutation locally with producer attribution; L3 reapplies the archived machine-only identity patch with focused negative controls. Root applies attributed evidence and retires superseded documents before integrated fidelity, with comparator and exemptions frozen.

Wave 2026-09-19: L1 implements receiver RW1/RW2 transport/job attribution with consumed job and failing-first proof. L2 reapplies archived Docker machine-only identity with mutation controls. Root reviews and commits L1 before dispatching L3 local alloy-default recapture; promotes observed evidence, records retirement family/log delta, retires the authorized pair, then runs integrated gates and exact-SHA CI. Comparator, bound 203 and single exemption remain frozen.

Root repair A1: the fixed receiver cannot satisfy the old alloy-default acceptance predicate because it requires the consumed job label. Preserve the first partial capture/result. Commission only the alloy-default predicate to check direct nonempty promrw producer identity and absence of compared job keys while preserving other checks. Review the newly commissioned file separately, commit, then repeat the full 300-second capture. Comparator, bounds, exemptions and captured data remain unchanged.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Standalone cAdvisor descriptor and focused failing-before/passing-after controls support machine-only boot_id and machine_id. Integrated just check fails: legacy unpaired k3s machine_memory_bytes capture compares the global synth label union and rejects Docker machine_id. Source patch archived under codex/scratch/wave-2026-09-17/l3/parked.patch and parked-source; owned source restored. No comparator weakening, exemption or capture rewrite. CodeRabbit completed zero findings on the archived implementation.

Acceptance correction: AC1 was checked when the source model was archived; that was too broad because the model is not in current source. AC1 is now unchecked. AC2 records retained failing-before/passing-after and mutation evidence only. Reapply the archived patch only after the legacy unpaired-producer comparison boundary is authorized and resolved.

Wave 2026-09-18: pinned alloy-default capture succeeded for the full 300-second window, but all 107 candidate metric families lack producers. Root read e2e/lab/matrix/promote.go:102-115: promotion copies metrics and elides labels; it does not derive attribution. The claim that recent attributed documents prove the just-lab route automatically attributes captures was WRONG. No corpus replacement or retirement applied. The scratch-only custom projection is not accepted as an existing capture route. L3 focused proof and CodeRabbit zero-findings review passed; its four owned implementation/documentation files and full patch are retained at codex/scratch/wave-2026-09-18/l3/parked-source and parked.patch, with tracked source restored to HEAD. AC1 remains unchecked because implementation is archived.

Correction for wave 2026-09-19: the broad previous claim that no existing shipped attribution route uses promrw/job was WRONG; the synth inventory path already consumes job into promrw/job, and accepted k3d-lab-otel-collector-prom provenance records that projection (commit 1f759d8). Rejecting the scratch projection as the deliverable remains correct. The actual missing path is direct attribution in the lab receiver; this wave explicitly commissions RW1/RW2 implementation and a fresh capture through it. No comparator amendment is authorized.

Root judgement A1: source evidence at e2e/lab/permutations/alloy-default/acceptance.jq requires metric_label(job), conflicting with the frozen receiver contract. Confidence high; bounded acceptance representation repair under goal section 1.2. Retaining job violates the chosen contract; dropping identity verification weakens evidence; parking would leave an evidence-resolvable prerequisite unfixed. The sibling alloy-otlp-podlogs predicate has the same legacy requirement and is reported for a later run, not amended in this wave.

Source integrated: receiver commit 90a738b264cd9c5ee71022c2b6d26cfd757eda52 derives RW1/RW2 producers and preserves them in snapshots; Docker commit 19ec8a7de3f502fda1b81744f99db0a7eb3871b9 is byte-identical to archived patch with passing package and all five mutation controls. Receiver CodeRabbit completed with one invalid major request to retain job, rejected against the frozen consumed-job contract; host review completed with zero findings. First fresh 300-second capture is 107/107 attributed but remains partial because the old harness predicate requires job. A1 fixes acceptance representation, not the captured data. No corpus retirement has occurred.

A1 integrated as 1ab4df086582ed7f4f2424be56aba8ebe13349d9 after a separate review of the newly commissioned predicate (complete, zero findings). The fresh C1 raw passes all seven updated checks; controls without producers, with empty promrw suffixes, and with retained job keys fail exactly their intended checks. C1 outcome remains partial in its original result. L3 cycle 2 is commissioned to earn a new captured result through this committed acceptance predicate.

Pre-retirement coverage record for wave 2026-09-19, based on the fresh full-window C2 capture through receiver 90a738b and acceptance predicate 1ab4df0.

Metrics: reality-corpus/k8s/k3d-lab.json contains 75 families and reality-corpus/k8s-addons/k3d-lab.json contains 25. Every one is present in the fresh candidate and in the proposed 100-family promotion. Missing metric families: none in either document. The seven candidate-only exclusions are existing lab/scrape exclusions: scrape_duration_seconds, scrape_samples_post_metric_relabeling, scrape_samples_scraped, scrape_series_added, synthkit_lab_requests_total, synthkit_lab_up, up.

Log delta from reality-corpus/k8s/k3d-lab.json, enumerated by source, transport, stream/resource keys and structured metadata keys. The addons document has no log shapes.

1. Anonymous source, transport loki: keys action, cluster, instance, job, k8s_cluster_name, k8s_kind, k8s_namespace_name; metadata k8s_daemonset_name, k8s_deployment_name, k8s_pod_name. This exact empty source identifier is absent from fresh capture, but its full key/metadata shape is present under k8s_manifests. This is observed reclassification, not a structural coverage loss.
2. Source k8s_pod_logs, transport otlp_logs, first envelope: keys app_kubernetes_io_name, cluster, k8s.cluster.name, k8s.container.name, k8s.deployment.name, k8s.namespace.name, k8s.node.name, k8s.pod.name, service.instance.id, service.name, service.namespace; metadata log.iostream, logtag. Absent from the fresh Alloy-default capture; retained as an explicit coverage gap for a future OTLP capture.
3. Source k8s_pod_logs, transport otlp_logs, second envelope: keys cluster, k8s.cluster.name, k8s.container.name, k8s.namespace.name, k8s.pod.name, service.instance.id, service.name, service.namespace; metadata log.iostream, logtag. Absent from the fresh Alloy-default capture; retained as an explicit coverage gap for a future OTLP capture.

The Loki k8s_pod_logs and kubernetes-events shapes remain present. Fresh C2 has 107/107 attributed candidate families; the promotion retains 100/100, with no compared metric job key. machine_memory_bytes carries promrw/integrations/kubernetes/cadvisor. C2 and the previous wave's retained candidate have identical 107 family names and identical derived producer sets; no additions, removals or producer differences. Captured job values are consumed by the receiver before value elision; metric job-key removal is intentional and symmetric with synth.

This record is written to task notes before either authorized legacy document is retired. The terminal report reproduces the delta after all verification; the captured documents and results are not rewritten.

Wave 2026-09-19 integrated source/corpus a6a6a323eaf0ba261655b9b6210581064130c6fc: just gen-check and just spdx-check passed. Full just check ran and failed at signal-fidelity: contradiction exemption "capture-manifest-service-name" expected_matches=1 but matched 0 findings. The report has no contradictions; manifest stream labels are now a coverage gap with only-in-synth service_name and only-in-reality instance. The gate exits before emitting or checking the producer-coverage ratchet, so no final ratchet pass is claimed. Frozen expected_count remains 203, exactly one exemption remains, and comparator/policy hashes are unchanged. just dump passed: all metric family names unchanged (3063), intended machine_memory_bytes machine_id label addition only; OTLP metrics 678, both log inventories and profiles unchanged. Two trace model-name subsets vary in the sampled dump; trace code unchanged. just gen was not applicable: no blueprint field, config struct or skill changed. AC1 and AC2 now supported by integrated source and focused evidence. AC3 and full-check DoD remain open. No exemption, comparator, bound or unrelated manifest-emitter change is authorized to manufacture green.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
AC3 remains open. Resume by authorizing a producer-scoped treatment of the legacy unpaired Kubernetes capture, then reapply the archived narrow Docker patch and pass integrated fidelity. Focused proof does not constitute integrated acceptance.

Current acceptance is AC2 only. AC1 model integration and AC3 fidelity remain open; no Docker identity implementation landed.

Resume at the capture producer boundary: authorize and implement direct producer provenance in the standard e2e receiver/lab path, with no inference from family names and no capture rewriting; re-capture alloy-default at chart 4.5.0, verify every family and machine_memory_bytes attribution, then retire superseded documents and apply the archived Docker patch before unchanged fidelity. Existing comparator, exemptions and producer bound remain frozen. AC2 only; integrated Docker acceptance was not run against the known unattributed replacement.

Current wave disposition supersedes older archive-only summaries: Docker implementation and direct RW1/RW2 attribution are integrated and pushed, fresh 107/107 attributed capture promoted as 100/100, and both authorized legacy documents retired after the full delta note. SKT-0070 is Parked with AC1 and AC2 checked, AC3 unchecked. Resume at the frozen manifest exemption cardinality boundary: authorize the handling of capture-manifest-service-name now matching zero contradictions after the fresh capture; keep authentic capture data intact, then rerun unchanged fidelity and exact-head CI. This wave does not authorize changing the exemption, its expected_matches, the comparator or the producer bound. The former machine identity contradiction is absent, but no complete integrated pass is claimed.
<!-- SECTION:FINAL_SUMMARY:END -->
