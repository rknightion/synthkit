---
id: SKT-0029
title: >-
  Exemption drift aborts before the report, hiding every co-occurring
  contradiction
status: Done
assignee: []
created_date: '2026-08-29 16:49'
updated_date: '2026-08-29 19:27'
labels: []
dependencies: []
priority: medium
type: bug
ordinal: 120000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
When an exemption's `expected_matches` drift guard fires, `ApplyContradictionExemptions` returns an error before the report is written, so the operator sees only the count mismatch and never the findings that actually changed.

Observed 2026-08-29. Injecting two simultaneous regressions — an invented label key on three `kubeproxy_*` families, and a bogus `plan9` value on `kube_node_labels` `label_kubernetes_io_os` — produced exactly one line:

    signal-fidelity: contradiction exemption "capture-k8s-node-os" expected_matches=1 but matched 0 findings

The invented label key was never named. Injected on its own it reports correctly, naming both the signal and the diverging label, so the gate is right in the ordinary case; it is the combined case that goes blind. That is the likely real-world shape, because a change that shifts an exempted value set is usually the same change that introduces other divergence.

SKT-0010.05's fifth acceptance criterion is that the failure output names the diverging signal and field. The drift-guard path does not meet it.

Fix by collecting exemption drift as a finding rather than a fatal error: still fail the build, still name the stale rule, but print the full report alongside it so every co-occurring contradiction is visible in the same run. An operator should never have to fix one error to discover the next.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 An exemption count mismatch still fails the build and still names the stale rule
- [x] #2 The full report is printed alongside it, so co-occurring contradictions are visible in the same run
- [x] #3 A run with both an exemption drift and an unrelated new contradiction names both
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [x] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [x] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
After SKT-0028, collect exemption-drift diagnostics without aborting report generation; retain fail-closed behavior while reporting stale rules and all co-occurring contradictions, with focused regression coverage.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Completed 2026-08-29. signal-fidelity now applies exemptions, writes the complete report, then returns any drift error. Adversarial runs proved a dropped rule still fails after printing the report, a corrupted expected_matches still names the stale rule, an injected CoreDNS label regression names the signal/field/label, and combined drift plus regression names both. Final wave gates passed; just gen was not required.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Moved exemption-drift failure after report generation without weakening fail-closed behavior. Verified standalone and combined drift/regression paths plus the full final gates.
<!-- SECTION:FINAL_SUMMARY:END -->
