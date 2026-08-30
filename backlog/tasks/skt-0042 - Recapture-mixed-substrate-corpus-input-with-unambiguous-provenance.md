---
id: SKT-0042
title: Recapture mixed-substrate corpus input with unambiguous provenance
status: To Do
assignee: []
created_date: '2026-08-30 08:40'
updated_date: '2026-08-30 19:02'
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

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
RESOLVED ON THE PRODUCING SIDE, 2026-08-30. The replacement captures exist. Leaving the acceptance
criteria unticked because AC#3 is about synthkit INGESTING them, which has not happened.

The defect this task describes — a document declaring one substrate while containing series from
two — is fixed at source. Three captures, one per substrate, each a complete and unambiguous
statement about exactly one cluster:

  captures/rksy-20260830T182641Z.capture.json   rksy-aws         eks   1066 families  508,276 spans
  captures/rksy-20260830T182838Z.capture.json   rksy-azure-aks   aks   1066 families  605,430 spans
  captures/rksy-20260830T190021Z.capture.json   rksy-gcp-gke     gke   1057 families  542,419 spans

AC#1, unambiguous substrate provenance: met at source. scope.not_applied is EMPTY on all three, so
no signal is stack-wide while the others are narrowed — which is precisely the mixed-substrate
condition that got the previous corpus input rejected. Earlier per-cloud captures could not manage
this: traces and profiles stayed stack-wide, so all three carried the same 603,000 spans.

AC#2, capture duration and load conditions retained: met. Every document carries window, duration,
soak_duration and load_driven, with load_driven and soak_duration marked explicitly as
OPERATOR-DECLARED because nothing in the stack records them. unknown there means "cannot rule out
an idle estate" rather than a default.

AC#3 is the remaining work and belongs to synthkit: ingest these three without relabelling or
splitting them. The design intent is that no inference is needed — each file is already one
substrate.

PROOF THE SEPARATION IS REAL. 508,276 + 605,430 + 542,419 = 1,656,125, and an unscoped capture over
the identical window returns 1,656,125. Exact partition, every span in exactly one document.

ONE THING THE INGESTION MUST NOT MISREAD. These are cluster-scoped, so they contain ZERO
cloud-provider families — no aws_, azure_ or stackdriver_. Those are ingested outside the cluster
and carry no cluster label, so the matcher correctly excludes them. Per-cloud provider evidence
needs a separately cloud-scoped capture. Recording the absence as a coverage gap would be exactly
backwards. See RKSY-0026.
<!-- SECTION:NOTES:END -->
