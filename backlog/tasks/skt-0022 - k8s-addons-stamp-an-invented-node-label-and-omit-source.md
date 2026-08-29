---
id: SKT-0022
title: k8s-addons stamp an invented node label and omit source
status: To Do
assignee: []
created_date: '2026-08-29 09:22'
labels: []
dependencies: []
priority: high
type: bug
ordinal: 113000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
23 of the 67 contradictions, all in `signals/k8s-addons.md` on substrate `k3s` from the `k3d_lab` capture, are one defect stamped on every addon family: synthkit adds a `node` label that real k8s-monitoring does not, and omits the `source` label that it does.

Verified 2026-08-29 across `coredns_cache_entries`, `coredns_cache_hits_total`, `coredns_cache_misses_total` and twenty more, every one reporting `only-in-synth=[node]; only-in-reality=[source]` against otherwise identical label sets.

The seam is `internal/k8saddon/k8saddon.go:139`, which sets `m["node"] = id.Node`. `internal/k8saddon/k8saddon_test.go:95-123` pins the current behaviour in both directions — that the label is present when a node is assigned and omitted when it is not — so the test is asserting the defect and must be corrected alongside it, not worked around.

Two things are wrong and both need fixing: the invented `node` label goes, and `source` is stamped as real k8s-monitoring stamps it. Confirm from the capture what value `source` carries and on which families before setting it; do not infer it from the k8s pollers, which stamp `source` under different rules — see the sibling task covering kube-proxy, where synthkit stamps `source` and reality does not.

Note the interaction: several of these families also appear as `unexpected_label_key` coverage gaps, because a finding diverging in both directions is emitted in both sections. Fixing this clears the contradiction; the gap line for the same family may persist.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 The invented node label is no longer stamped on addon families
- [ ] #2 source is stamped as the capture shows real k8s-monitoring stamping it, with the value and family scope confirmed from the capture rather than inferred
- [ ] #3 The k8saddon tests assert the corrected shape rather than the current one
- [ ] #4 signals/k8s-addons.md records the correction with provenance
- [ ] #5 The 23 contradictions in this class are gone from the fidelity report
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 make gate (build vet test race rw-proto-check spdx-check forbidden-words)
- [ ] #2 make blueprint-schema (only if a blueprint field or construct/workload config struct changed)
- [ ] #3 DRY_RUN=true go run ./cmd/synthkit -once -dump — inventory diffed against signals/
<!-- DOD:END -->
