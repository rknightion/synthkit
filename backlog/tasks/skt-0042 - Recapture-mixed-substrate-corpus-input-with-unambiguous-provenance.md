---
id: SKT-0042
title: Recapture mixed-substrate corpus input with unambiguous provenance
status: To Do
assignee: []
created_date: '2026-08-30 08:40'
updated_date: '2026-08-31 10:32'
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

2026-08-31: SUPERSEDING SET. Use these four, not the 2026-08-30 files named above.

  captures/rksy-20260831T102151Z.capture.json   rksy-aws         eks   1432 families    560,764 spans
  captures/rksy-20260831T102333Z.capture.json   rksy-azure-aks   aks   1433 families    651,296 spans
  captures/rksy-20260831T102517Z.capture.json   rksy-gcp-gke     gke   1038 families    576,647 spans
  captures/rksy-20260831T102646Z.capture.json   FULL, unscoped         5178 families  1,787,478 spans

All three per-substrate entries: scope.not_applied EMPTY, declared_observed_mismatch NULL. The full
one: ZERO critical limitations.

WHY THESE REPLACE YESTERDAY. Yesterday read a destroyed estate, and the tool has had two defects
fixed since, both of which affected exactly this task acceptance criteria:

  - the cluster-exclusion measurement silently returned null on every cluster-scoped capture, so
    yesterday entries did not record that they structurally exclude all cloud-provider families
  - the estate block described the whole STACK while the rest of the document described the scope,
    so a correctly-declared per-cluster capture was flagged DECLARED_ESTATE_MISMATCH against three
    clouds. That is the provenance ambiguity this task was raised for, and it would have shipped
    inside the replacement.

GCP CARRIES 394 FEWER FAMILIES THAN THE OTHER TWO AND IT IS FULLY EXPLAINED, so it is not a
coverage gap to chase:
  pg_ 372          the chart PostgreSQL integration, which GCP cannot run - Cloud SQL here is
                   reachable only through the Auth Proxy, which the collector cannot carry
  node_ 50, kube_ 6  a GENUINE substrate difference between GKE and EKS, and worth keeping

AC#1 and AC#2 are met at source. AC#3 remains synthkit work: ingest these without relabelling or
splitting. No inference should be needed - each file is one substrate and says so.

READ BEFORE INGESTING. The three per-substrate files contain ZERO aws_, azure_ and stackdriver_
families, correctly, because those are ingested outside the cluster and carry no cluster label.
Each file now states this itself in CLUSTER_SCOPE_EXCLUDED_FAMILIES with the exact count (3814 /
3813 / 4206) and a per-prefix breakdown. Cloud-provider evidence is in the FULL capture.

One caveat on the full capture: it is unscoped, so its traces and profiles are stack-wide by
nature and cannot be attributed to a cloud. Use the per-cluster entries for those.

The three per-cluster span counts sum to 1,788,707 against the full capture 1,787,478 - a 0.07%
difference, not an exact partition, because the four captures ran sequentially over ~5 minutes and
each took its own 3h window ending at its own start. Yesterday exact match came from pinning
--end. Pin it when a partition proof is wanted; every file is internally self-consistent either
way, and each carries its own window in provenance.
<!-- SECTION:NOTES:END -->
