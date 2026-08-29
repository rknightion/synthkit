---
id: SKT-0021
title: 'Close the reality coverage gaps, by cause rather than by count'
status: To Do
assignee: []
created_date: '2026-08-29 09:21'
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
- [ ] #1 Each of the three gap classes has a recorded verdict: closed, deliberately out of scope with a reason, or blocked on capture
- [ ] #2 No class is closed by suppressing findings rather than changing what synthkit emits or what the corpus records
- [ ] #3 The gap count is reported per class and per substrate, so a change is attributable
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 make signal-fidelity runs and the count movement is attributed to a cause
<!-- DOD:END -->
