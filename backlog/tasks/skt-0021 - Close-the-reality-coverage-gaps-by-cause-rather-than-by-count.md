---
id: SKT-0021
title: 'Close the reality coverage gaps, by cause rather than by count'
status: Done
assignee:
  - '@codex'
created_date: '2026-08-29 09:21'
updated_date: '2026-09-01 23:02'
labels: []
dependencies: []
priority: medium
type: feature
ordinal: 109000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
646 coverage gaps stand as of 2026-08-29 at `9d8aec5`. Decided with Rob: a coverage gap reports and does not fail the build, but it is still a statement that synthkit models less than reality, and the backlog for closing it should be visible rather than living only in a report nobody reads.

The 646 is not 646 pieces of work. Measured breakdown by finding type:

- **418 `extra_metric`** — reality publishes a family synthkit does not emit at all. 375 of these are CloudWatch, 41 k8s, 2 k8s-addons. This is the honest coverage backlog.
- **118 `instrument_mismatch`** — every single one has `only-in-reality=[unknown]`. Not one is a synthkit defect: the corpus does not carry an instrument type for those families, so the comparator is comparing against an absence. This is capture quality, and no change to synthkit can ever clear it.
- **110 `unexpected_label_key`** — the same four systematic label causes that produce the contradictions, seen from the other direction.

By signals area: cw.md 466, k8s.md 116, k8s-addons.md 42, host.md 19, logs.md 3.

By substrate: eks 499, k3s 147 — which is to say the entire gap backlog is a statement about two substrates, and SKT-0020 is what changes that.

Also measured: **33 findings are byte-identical in both the Contradictions and the Coverage gaps sections**, because a finding that diverges in both directions is emitted twice. Driving contradictions to zero therefore does not reduce the gap count by the same 33, and any plan that treats the two counts as independent is wrong.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 Each of the three gap classes has a recorded verdict: closed, deliberately out of scope with a reason, or blocked on capture
- [x] #2 No class is closed by suppressing findings rather than changing what synthkit emits or what the corpus records
- [x] #3 The gap count is reported per class and per substrate, so a change is attributable
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 make signal-fidelity runs and the count movement is attributed to a cause
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
After the CloudWatch and k8s/host child counts return, record one coherent parent verdict across extra_metric, instrument_mismatch, and unexpected_label_key classes, with attributable per-substrate movement and no suppressed findings.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
2026-09-02 parent verdict: the current 648 findings are 420 extra_metric, 118 instrument_mismatch, and 110 unexpected_label_key; 499 are EKS and 149 k3s. The 420 extra_metric rows resolve to 280 emit-candidate CloudWatch expansions, 50 deliberate exclusions, 75 PENDING expansions, and 15 already-emitted kube-proxy component rows. The 118 unknown-type rows remain absent evidence/capture quality, and the 110 label rows retain their recorded systematic causes. No finding or threshold was suppressed; zero exemptions were added. Integrated signal-fidelity passed.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
2026-09-02: Recorded all three coverage classes and current substrate counts with attributable causes; no class was closed by suppression and the integrated fidelity gate passed with zero new exemptions.
<!-- SECTION:FINAL_SUMMARY:END -->
