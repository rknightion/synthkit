---
id: SKT-0042
title: Recapture mixed-substrate corpus input with unambiguous provenance
status: To Do
assignee: []
created_date: '2026-08-30 08:40'
labels:
  - needs-triage
dependencies: []
priority: high
type: bug
ordinal: 133000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
A captured corpus document declares one substrate in provenance while containing series from two substrates. The comparator cannot assign per-substrate semantics without corrupting ground truth. Produce separate source captures or an explicit multi-substrate schema before ingestion.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 Each captured document has provenance that unambiguously identifies every substrate represented by its signal families
- [ ] #2 Capture duration and load/soak conditions are retained for each substrate
- [ ] #3 The synthkit corpus ingests the replacement without relabelling or splitting the source artefact by inference
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [ ] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [ ] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->
