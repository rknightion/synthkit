---
id: SKT-0045
title: Teach the fidelity comparator what an unknown metric type means
status: To Do
assignee: []
created_date: '2026-08-30 18:41'
updated_date: '2026-09-01 18:26'
labels: []
dependencies: []
ordinal: 136000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
The reality corpus now records a metric type of unknown for a large share of families, and that is CORRECT rather than a capture defect. The comparator has not been told, so it would read those as contradictions against synthkit emitting a counter or a gauge.

MEASURED on the three-cloud capture rksy-20260830T140435Z, 4733 families:

  gauge      2432
  unknown    2083
  histogram   112
  counter      105
  summary       1

THE CAUSE, established in synthkit-terraform RKSY-0013 and now stated as a first-class limitation inside every capture document. Alloy caches the metadata of everything it SCRAPES and forwards none of it. Metadata survives only for what Alloy RECEIVES over OTLP. The split is clean along that line: 15 name prefixes have no metadata at all, and every one is scraped - apiserver_ authentication_ azure_ container_ grafana_ kube_ kubelet_ machine_ node_ pod_ promhttp_ rest_ scrape_ stackdriver_ workqueue_.

So unknown is a property OF THE SENDER, not of the family and not of the capture. Any Alloy-shipped estate produces it. It is not recoverable by re-capturing, and a second limitation in the same document rules out the obvious workaround: /api/v1/metadata is non-deterministic in BOTH directions - 2347, 2290 and 2348 entries on three successive probes in one run - so neither retrying nor taking the largest response is sound.

WHAT THE COMPARATOR MUST DO. Treat unknown as ABSENT EVIDENCE, never as a refutation, which is the semantics SKT-0010.01 already established for the coverage case. A synthkit counter compared against a corpus family typed unknown is an unanswered question, not a contradiction. Getting this wrong fails the gate on 2083 families for a property of Alloy.

The capture also records name_suffix_hint separately and deliberately does not use it to infer a type - 124 of the undetermined names end in _total. That design should not change here either: a hint is evidence for a human, not grounds for the comparator to manufacture a verdict.

Check whether type_source is already carried through corpus ingestion, since the distinction the comparator needs is undetermined versus observed versus metadata, not the bare type string.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 A corpus family typed unknown never produces a fidelity contradiction against any emitted type
- [ ] #2 The distinction between an undetermined type and an observed one survives corpus ingestion and is available to the comparator
- [ ] #3 name_suffix_hint is not used to infer a type anywhere in the comparison path
- [ ] #4 A test pins the unknown-is-absent-evidence semantics so a later change cannot silently reintroduce the contradiction
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [ ] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [ ] #3 just dump — inventory diffed against signals/
- [ ] #4 just check
<!-- DOD:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
CORRECTION, same day as filing. The description states the cause as "Alloy caches the metadata of
everything it scrapes and forwards none of it". That cause is NOT established - see
synthkit-terraform RKSY-0013, which was closed on it and then reopened.

A second explanation fits the identical measurements: the scraped lane ships over Prometheus
REMOTE WRITE v2, whose metadata support Mimir documents as limited and experimental, while the
OTLP lane goes through the OTLP gateway, which is not remote write. The pointer that it is the
protocol rather than the collector is that the project's other staff stack is also Alloy-shipped
and does carry metadata for the same scraped families. An experiment to decide it is wired and pending.

WHY THIS TASK IS UNAFFECTED AND SHOULD STILL PROCEED. Under BOTH explanations the corpus contains
a large share of families typed unknown, that typing is honest, and it is not recoverable by
re-capturing. Treating unknown as absent evidence rather than as a contradiction is correct
either way.

What the outcome does change is how much of the corpus stays unknown in future. If the protocol is
the cause, a stack ingesting over RW1 or over OTLP would carry metadata for those families, so the
unknown share is a property of a wire-format choice rather than a permanent fact about Alloy
estates. Do not encode "Alloy estates have no scraped metadata" as a durable assumption anywhere
in the comparator or in signals documentation.

2026-09-01 privacy redaction. The 'CORRECTION, same day as filing' note named a Grafana Cloud stack by name. That identifier is on the repository's forbidden-words list, so this file failed the hosted hygiene job and left main red from 8e9c5b7 through e2ab3ce — five consecutive ci runs. The Backlog CLI offers no append-only redaction, so the name was removed by a narrow in-place edit and replaced with 'the project's other staff stack'; the technical claim it supports is unchanged. No acceptance criterion, plan or status was touched.
<!-- SECTION:NOTES:END -->
