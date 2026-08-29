---
id: SKT-0025
title: 'Reclassify subset label values as coverage gaps, not contradictions'
status: To Do
assignee: []
created_date: '2026-08-29 09:22'
labels: []
dependencies: []
priority: high
type: bug
ordinal: 116000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Two contradictions are the opposite shape to the other 65: synthkit's label VALUE set is a subset of reality's, not a superset.

- `kube_pod_info`, field `labels.created_by_kind`: `only-in-reality=[AutoscalingListener, EphemeralRunner]`; synth emits `[DaemonSet, Job, ReplicaSet, StatefulSet]`.
- `kube_pod_info`, field `labels.host_network`: `only-in-reality=[true]`; synth emits `[false]` only.

Both mean synthkit models less than reality, which is the definition of a coverage gap. SKT-0010.01 decided that absent evidence is a gap and never a contradiction, and this is the same principle applied to a label's value set rather than to a signal's presence: a value synthkit has never emitted is not a value it contradicts.

Decided with Rob 2026-08-29: reclassify. A value present in reality and absent from synth is a coverage gap. A value synthkit emits that reality never shows stays a contradiction — that direction is a genuine claim about the world that the evidence refutes.

Log the resulting gaps rather than losing them: the two underlying facts, that synthkit models no host-network pods and only four owner kinds, are real accuracy limits worth closing. `AutoscalingListener` and `EphemeralRunner` are Actions Runner Controller kinds specific to the captured estate, so whether synthkit should model them at all is a judgement, not an obligation. SKT-0010.13 already covers modelling pods with no Deployment owner and no node, and is the natural home for the host-network case.

This is the last comparator-semantics correction blocking the gate flip, so it lands before SKT-0010.05 rather than alongside it.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 A label value present in reality and absent from synth reports as a coverage gap
- [ ] #2 A label value synthkit emits that reality never shows still reports as a contradiction
- [ ] #3 The two kube_pod_info findings move out of the Contradictions section and are still visible as gaps
- [ ] #4 The reclassification is a rule in the comparator, not a filter on those two findings
- [ ] #5 The host-network and owner-kind accuracy limits are recorded as tracked work rather than lost with the reclassification
- [ ] #6 docs/reality-corpus.md states the value-set rule in both directions
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 make gate (build vet test race rw-proto-check spdx-check forbidden-words)
- [ ] #2 make blueprint-schema (only if a blueprint field or construct/workload config struct changed)
- [ ] #3 DRY_RUN=true go run ./cmd/synthkit -once -dump — inventory diffed against signals/
<!-- DOD:END -->
