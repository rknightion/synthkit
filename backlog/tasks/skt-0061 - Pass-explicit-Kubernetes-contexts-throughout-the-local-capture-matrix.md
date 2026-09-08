---
id: SKT-0061
title: Pass explicit Kubernetes contexts throughout the local capture matrix
status: Done
assignee:
  - '@codex'
created_date: '2026-09-08 00:16'
updated_date: '2026-09-08 01:34'
labels:
  - lab
dependencies: []
type: bug
ordinal: 157000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
The matrix deploy scripts rely on the mutable current kubeconfig context. A stale current context blocks the run, and parallel cluster creation can silently route calls to a different permutation.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 Every matrix kubectl and Helm call targets the cluster owned by that permutation explicitly.
- [x] #2 Static lab checks and a bounded real matrix run preserve failed, empty and partial outcomes and retain residency evidence.
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [ ] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [ ] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Integrated in 4dbe491. Matrix commands carry explicit owned-cluster contexts; readonly child environments, nested-module invocation and relative output paths were corrected. Five permutations captured successfully after the failed first attempt. Both nodes per permutation passed image residency after first import on execution base7cbafe0 plus harness patch cda6bda011c23aa52c9dbab132894cac3c46dd01e5d8b6ccb6f62e745be12ecb. Ten clusters created/deleted across attempts, zero retained. Static checks, matrix package checks, second CodeRabbit batch with zero findings and full integrated gate passed.
<!-- SECTION:FINAL_SUMMARY:END -->
