---
id: SKT-0010
title: Make the signal-fidelity comparator trustworthy and give the gate teeth
status: In Progress
assignee: []
created_date: '2026-08-27 07:05'
updated_date: '2026-09-05 16:04'
labels: []
dependencies: []
priority: high
type: feature
ordinal: 35000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
The signal-fidelity gate landed report-only under SKT-0006.02 and is now the mechanism the project relies on for technical accuracy. Measured on main 2026-08-27 with `make signal-fidelity` it produces 1806 findings, and the three dominant classes are comparator defects rather than real divergence, so the report cannot be read and the genuine findings are invisible.

Verified noise classes:

1. **596 `instrument_mismatch`, all false.** Every metric entry in both `reality-corpus/*/k3d-lab.json` and `reality-corpus/*/eks-live-readback.json` carries `instrument_types: ["unknown"]` — neither corpus producer records an instrument type today. `internal/inventory/diff.go` has no handling for that sentinel, so it diffs synth `[gauge]` against reality `[unknown]` and reports a contradiction for every CloudWatch and k8s family.
2. **320 `label_value_contradiction`, nearly all `labels.region`.** Synth emits six regions; the captured account runs in one. A capture from a single account or region is a SUBSET of what reality can produce, not a contradiction.
3. **561 `unexpected_label_key`** where reality carries `asserts_env`, `asserts_site`, `service`, `__aggregation__`. Those are Grafana Cloud read-path enrichment and recording-rule labels added AFTER ingest. Comparing them against collector egress is a category error, and the gcx read-back producer is the only one that can see them.

Genuine findings buried underneath: `kubelet_pod_start_duration_seconds` emitting default Prometheus buckets where the real kubelet uses its own 0.5s-3600s set, `coredns_proxy_request_duration_seconds` missing its top two buckets, two `extra_log` entries that are the SKT-0006.05 OTLP-logs transport gap, and 367 `extra_metric` coverage gaps.

Settled direction, decided with Rob 2026-08-27: once the false positives are gone the gate stops being advisory. A contradiction fails CI; a coverage gap stays report-only and routes to a cantfind.md PENDING.

Governing principle for the comparator, which the subtasks implement: **absent evidence is never a contradiction.** A corpus field the producer could not observe is a gap, not a divergence, and the fix is to make the producer observe it rather than to permanently exempt the field.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 A corpus field the producer could not observe never produces a contradiction finding
- [x] #2 Read-path enrichment labels are declared in the corpus with provenance rather than hard-coded in the comparator, and are excluded from emission-shape comparison
- [x] #3 Label-value comparison treats a single-account or single-region capture as a subset of reality, and reports a contradiction only where reality carries a value synth cannot emit
- [x] #4 Both corpus producers record real instrument types, so the unknown sentinel becomes rare rather than universal
- [x] #5 The real divergences the audit found are corrected in the emitters, with the corrected shape recorded in signals/ with provenance
- [x] #6 Every coverage-gap metric carries a recorded verdict: synthkit should emit it, it is deliberately out of scope, or it is unresolved with a cantfind.md PENDING
- [x] #7 CI fails on contradictions and reports coverage gaps without failing
- [ ] #8 The report is small enough for a human to read end to end, and its size is stated in docs/reality-corpus.md as the standard it is held to
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 make gate (build vet test race rw-proto-check spdx-check forbidden-words)
- [ ] #2 make blueprint-schema (only if a blueprint field or construct/workload config struct changed)
- [ ] #3 DRY_RUN=true go run ./cmd/synthkit -once -dump — inventory diffed against signals/
<!-- DOD:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
PRE-COMMIT REVIEW, 2026-08-27. CodeRabbit ran over the full wave diff (108 paths). Four findings: 2 major, 2 minor. Three verified valid and fixed with tests; one was stale.

FIXED — internal/inventory/synth.go addOTLPMetricResource had no case for MetricExponentialHistogram, so such a metric fell through to the GAUGE branch and was recorded with resource attributes only, losing every datapoint attribute. The e2e receiver already records it correctly as a native histogram, so the two sides of the correlation check would have disagreed on instrument type the moment any lane emitted the shape. Lane L8 flagged this as a hand-off and CodeRabbit found it independently. Now projects as a native histogram carrying the scale, with a test pinning the datapoint attribute that the fall-through dropped.

FIXED — internal/capture/k8s.go alloyImageVersion split on IndexAny(ref, ":@"), so a reference carrying BOTH a tag and a digest (alloy:v1.19.0@sha256:...) split on the colon first and reported a version with the digest appended. A digest-only reference was handled by a sha256: prefix guard, which masked the tag+digest case entirely. Now strips the digest first. Digest-pinned deployments behind a private registry mirror are exactly the ones that hit this, and it was uncovered by any test — six cases now pin it.

FIXED — cantfind.md: SK-91 had been appended into the SKT-0007.01 scoping-study section, whose preamble states that each row BLOCKS an OTLP lane and that the kind emits nothing over OTLP. False for SK-91, whose kind (web_service) already has a working lane emitting cumulative today. Moved to its own section stating that distinction.

SKIPPED AS STALE — a major finding said blueprints/k8s-logs-events.yaml selects pod_logs_method: opentelemetry before the sink construction and inventory projection exist, so it would emit nothing rather than falling back to Loki. That was true of the tree CodeRabbit reviewed: the wiring pass completed the cmd/synthkit and inventory half after the review started. Verified after the fact — DRY_RUN=true BLUEPRINT_NAMES=k8s-logs-events -once -dump prints [dry-run otlplogs] receipts and the otlp_logs inventory section. The finding was correct about the snapshot and no longer describes the code.

Note the finding CodeRabbit did NOT raise and could not: nothing in this diff invents a metric, label or field name. That property is carried by the fidelity gate and the lane briefs, not by a code reviewer.

REVIEW ROUND 2 — the CodeRabbit run produced 9 findings in total (65 of the 108 changed paths reviewed), not the 4 visible when first checked. The five later arrivals:

FIXED (major) reality-corpus/k8s/eks-live-readback.json — capture_volume declared runs: 2 with a single observed_contract_counts entry, so one run had no associated count. Its cw sibling correctly carries two. Corrected to [31, 31], which is what actually happened: SKT-0010.02 records the k8s eks document as 0/31 typed before and after with no new families, so the second read-back observed the same 31. NOTE A VALIDATION GAP: nothing enforces len(observed_contract_counts) == runs, which is why this drifted silently through a merge. Fold that check into SKT-0010.10, which already touches corpus validation.

FIXED (minor) internal/sink/otlp/metrics_test.go — the guard read `if len(rms) != 1 || len(rms[0].GetScopeMetrics()) != 1` and then indexed rms[0] in the Fatalf ARGUMENTS. Go short-circuits the condition but not the format arguments, so an empty decode panicked on the way to reporting the failure it had correctly detected. Split into two checks.

FIXED (minor) signals/logs.md and signals/k8s.md — log_iostream and logtag were listed among the destination STREAM LABELS in the logs.md summary while k8s.md correctly classifies them as RECORD attributes that land as structured metadata. Only resource attributes are promotion candidates, so they can never be stream labels on the OTLP transport. The wrong half was inherited from the original SK-20 prose. Both files now state it explicitly and agree.

SKIPPED AS WRONG (minor) signals/k8s-addons.md — the finding claimed the coredns_proxy_request_duration_seconds provenance should reference reality-corpus/k8s/k3d-lab.json rather than the k8s-addons path. Checked: that family is present in reality-corpus/k8s-addons/k3d-lab.json and absent from the k8s one. The existing reference is correct.

Worth recording about the review itself: 65 of 108 changed paths were reviewed, so this was not full coverage of the wave, and a clean result on the remainder is not evidence. The gate and the fidelity comparator carry the properties a code reviewer structurally cannot check — that no metric, label or field name was invented.

2026-09-05 parent reconciliation: read the final summaries of all 19 Done subtasks. AC1-3: .01 and .10 carry absent-evidence, enrichment provenance and subset comparison proof. AC4: .02 records typed coverage rising from 12/676 to 513/706 without name inference. AC5: .03 and corrected .07 record actual emitter fixes and signal provenance. AC6: .04 verdicts and .06 delivery plus subsequent coverage work. AC7: .05 records the enforced contradiction gate; current hygiene-fix CI 33975458812 passed signal-fidelity. AC8 remains unchecked: no report-size/readability standard is stated in docs/reality-corpus.md. Done subtasks alone do not prove that parent criterion.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
2026-09-05: Reconciled to 7/8 evidenced acceptance criteria; remains In Progress at the explicit report-size/readability standard in AC8. No historical skipped check was counted as a pass.
<!-- SECTION:FINAL_SUMMARY:END -->
