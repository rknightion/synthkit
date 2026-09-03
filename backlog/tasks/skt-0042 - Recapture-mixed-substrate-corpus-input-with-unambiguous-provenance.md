---
id: SKT-0042
title: Recapture mixed-substrate corpus input with unambiguous provenance
status: Parked
assignee:
  - '@codex'
created_date: '2026-08-30 08:40'
updated_date: '2026-09-03 20:50'
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
- [x] #1 Each captured document has provenance that unambiguously identifies every substrate represented by its signal families
- [x] #2 Capture duration and load/soak conditions are retained for each substrate
- [ ] #3 The synthkit corpus ingests the replacement without relabelling or splitting the source artefact by inference
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [x] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [x] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
After SKT-0045 is integrated, resolve the canonical seven-document set from the sibling tracker, ingest it without source mutation or inferred relabelling, and run the comparator at real corpus scale.

After SKT-0010.19 is proven, regenerate the canonical seven projections from immutable captures, ingest only with explicit producer-scoped comparison, and rerun signal-fidelity with zero exemptions.

2026-09-03 wave: Review the seven immutable capture hashes against an explicit family-to-signals-area manifest; test that unmapped families cannot be routed by name or prefix; regenerate producer-scoped projections without source mutation; run focused fidelity evidence with zero exemptions; return exact review gaps if any mapping remains unapproved.

2026-09-04 corrected boundary: deduplicate the seven immutable captures before review; route distinct families first by explicit producer identity and then by exact existing signals-area membership; review only the residue; report rows and distinct families separately; wire atomic ProjectCaptureV2 promotion and producer-scoped fidelity only where no inference remains.

2026-09-05 wave plan: Lane B changes ProjectCaptureV2 from all-or-nothing to per-family promotion: promote the 2,286 directly producer-identified distinct names, persist each of the 2,918 producerless names as explicitly unrouted with its reason, retain fail-closed comparison of unrouted families, mutate no captures, and add zero exemptions.
<!-- SECTION:PLAN:END -->

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

## Superseded by a larger set, 2026-08-31. Use these, not the 2026-08-30 three.

The 30 August captures remain valid and are still per-substrate-clean, but the estate has since
grown three ingest lanes they cannot contain. A new set of SEVEN was taken, all sharing ONE PINNED
`--end` of 2026-08-31T12:03:05Z so the scopes partition exactly rather than sliding against each
other. In `~/repos/synthkit-terraform/captures/` (gitignored there; promote deliberately).

    file                             scope              families
    rksy-20260831T120310Z            cluster rksy-aws        1447
    rksy-20260831T120454Z            cluster rksy-azure-aks  1437
    rksy-20260831T120750Z            cluster rksy-gcp-gke    1038
    rksy-20260831T120943Z            cloud   aws             1455
    rksy-20260831T121127Z            cloud   azure           2171
    rksy-20260831T121351Z            cloud   gcp             1509
    rksy-20260831T122931Z            FULL                    5204

BOTH SCOPES ARE NEEDED NOW, and that is new. Three of the six ingest lanes carry a `cloud` label and
NO `cluster` label — the two managed cloud scrapers, the Alloy Azure Monitor exporter and the
standalone dbo11y collectors all live outside the cluster or stamp their own external labels. A
cluster-scoped capture correctly excludes every one of them. So the per-substrate files are the
Kubernetes/host/application statement, the per-cloud files are the provider statement, and neither
is a superset of the other. The FULL file is the only one containing all six lanes at once.

IGNORE rksy-20260831T121706Z AND rksy-20260831T122247Z. They are earlier attempts at the same FULL
capture, kept rather than deleted, whose `metadata_by_ingest_path` block failed with a 400. The
canonical FULL capture is 122931Z. All three share the same pinned window.

## What is in this set that was not in the last one

  - `signals.metrics.metadata_by_ingest_path` — metadata coverage attributed to the INGEST PATH,
    which the tool previously said it could not see. Six paths in the full capture:

        ingest_path              families  with_metadata  only_here  only_here_with_metadata
        (no label)                   3882           2534       2888                     2253
        promrw                       1504            281        877                        0
        azure-monitor-managed         712              1          0                        0
        dbo11y                        351              0          8                        0
        azure-monitor-alloy           202              0         20                        0
        otlp-native                     1              1          0                        0

    Read the *only_here* columns: they attribute. Only the unlabelled CloudWatch metric stream and
    the OTLP gateway supply metadata. Every Alloy path and BOTH managed cloud scrapers supply none.

  - limitation `UNTYPED_FAMILIES_ARE_A_SENDER_PROPERTY`, which states that an `unknown` type is
    absent evidence rather than a measured one, names which senders do and do not supply metadata,
    and says explicitly not to resolve it by inventing a type. It counts only families with NO
    metadata behind them: a family whose metadata DECLARES the type `unknown` is a measured answer
    and is reported separately (0 in this set). This is the direct input to SKT-0045.

  - three ingest lanes that did not exist on 30 August: `azure-monitor-alloy` (Alloy
    prometheus.exporter.azure beside the managed Azure scraper — RKSY-0023), and `dbo11y` on both
    RDS and Azure Flexible Server, 326 and 314 metric families plus 16 Loki streams under
    `job="integrations/db-o11y"` (RKSY-0021 AC#1).

AC#1 and AC#2 hold as before: every scoped file records its own scope, `scope.not_applied` is empty
on the per-substrate three, and the declared soak is 30m against a 1h window — which is why every
file carries `WINDOW_EXCEEDS_SOAK`. That warning is CORRECT and deliberate: the three newest lanes
had genuinely only been shipping for about half an hour, and declaring the estate's 24h age would
have overstated them.

AC#3 remains the open one. Nothing has been ingested into synthkit's corpus yet.

Wave result: the schema-2 converter preserved capture hashes, scope, warnings, schema/tool versions, instrument type source, and structural histogram bounds. Seven canonical source projections were generated and audited only as candidates, then excluded from reality-corpus because privacy promotion removes the producer-selecting label values used by the comparator. Comparing each candidate against the global synth union produced 304 false producer comparisons. No capture was modified and no fidelity exemption was added. Resume when direct per-family producer identity survives privacy promotion; regenerate the seven projections and rerun signal-fidelity before checking AC #3.

2026-09-02 resume boundary: the seven canonical immutable captures expose explicit producer identities but no reviewed hash-keyed family-to-signals-area routing manifest. Conversion has no production routing caller, so promotion would still guess document ownership. No capture or candidate corpus file was modified. Resume by reviewing a family-to-area projection keyed to the seven capture hashes, then regenerate the per-area projections and rerun producer-scoped fidelity with zero exemptions.

2026-09-03 evidence: added and focused-tested a fail-closed ProjectCaptureV2 routing seam keyed by immutable capture hash and exact family, preserving capture conditions and rejecting unmapped, stale, duplicate, unknown-area, or producer-mismatched rows. All seven captures total 14,261 families; no reviewed family-to-area manifest exists, and 3,389 families also lack direct producer identity. No production caller, corpus projection, capture mutation, or fidelity exemption was added. Integrated just check, safe just dump, and non-agent just e2e passed; just gen was not applicable. Resume with one reviewed 14,261-row hash/family routing manifest plus the cloud/full authority decision, then wire atomic promotion and rerun producer-scoped fidelity.

2026-09-04 closeout: the canonical seven captures contain 14,261 rows and 5,204 distinct family names. Direct producer identity exists for 2,286 names. A further 781 producerless names have one unique exact catalogue area, while 2,137 have neither producer nor area. All 2,918 producerless names remain inadmissible to promotion. ProjectCaptureV2 now has focused fail-closed coverage for missing, stale, duplicate, unknown-area, and producer-mismatched routes. just check, just dump, and just e2e passed; chart and published-compose e2e cases were skipped because their opt-ins were absent. Resume with exact source review for the 2,918 missing producers, including area decisions for 2,137, plus the cloud/full authority decision.

2026-09-03 decision by Rob: promote an ADMISSIBLE SUBSET rather than all-or-nothing. ProjectCaptureV2 changes from rejecting a partial manifest to promoting per-family: the 2,286 names carrying direct producer identity are promoted, and each of the 2,918 producerless names is recorded as explicitly unrouted with its reason. The gate stays fail-closed - comparing an unrouted family is still an error, so nothing is inferred and no exemption is added. Why: reviewing 2,918 families by hand is not wave-sized, and all-or-nothing has parked this task three waves running with nothing promoted. Incremental promotion lands real evidence now and leaves the residue explicitly visible instead of implicitly blocking.

2026-09-05 subset-promotion evidence: the seven immutable captures contain 14,261 rows and 5,204 distinct family names. Exact hash-keyed records now classify 10,872 rows / 2,286 distinct names with direct producer identity and 3,389 rows / 2,918 distinct names as unrouted. The residue is 851 rows / 781 names with one exact catalogue area but no producer and 2,538 rows / 2,137 names with neither. ProjectCaptureV2 promotes only direct routes, returns explicit residue, and rejects unrouted comparison plus stale, duplicate, mixed, unknown-area, and producer-mismatched records. Loading the seven candidate projections produced 251 genuine unexempted producer-scoped fidelity contradictions, so they remain non-loaded under manifests/candidates rather than weakening the gate. The final candidate-excluded just check and signal-fidelity passed; zero exemptions were added and no capture changed. Resume from the checked-in candidates and correct the 251 exact synth-versus-capture shape contradictions before moving them into the active corpus. One exploratory just dump incorrectly selected the agent blueprint in DRY_RUN; it made no Agent Observability request and was excluded from acceptance evidence.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Parked after source provenance and capture conditions were verified and the ingestion path was implemented and validated. AC #3 remains open because the privacy-safe projection cannot yet retain direct per-family producer identity; ingesting now would turn producer ambiguity into false contradictions. Resume at the producer-identity boundary, regenerate candidates, and rerun fidelity.

2026-09-02: Parked after producer identity was unblocked but before ingestion: explicit source identity exists, while the reviewed family-to-area routing manifest required for non-inferred promotion does not.

2026-09-03: Parked after landing the fail-closed explicit routing seam. AC3 remains open until the reviewed 14,261-row manifest and cloud/full authority decision exist; ingestion by inference remains forbidden.

2026-09-04: Parked after replacing the impossible 14,261-row review boundary with a deduplicated producer-aware boundary. No partial manifest is promoted; resume at exact source review for 2,918 producerless families and 2,137 missing areas, then take the cloud/full authority decision.

2026-09-05: Parked after replacing all-or-nothing routing with explicit admissible-subset records. The 2,286 direct-producer names and all 2,918 unrouted names are retained exactly, but active ingestion correctly fails on 251 producer-scoped fidelity contradictions. Resume by correcting those finite captured-shape mismatches, then promote the checked-in candidates without an exemption.
<!-- SECTION:FINAL_SUMMARY:END -->
