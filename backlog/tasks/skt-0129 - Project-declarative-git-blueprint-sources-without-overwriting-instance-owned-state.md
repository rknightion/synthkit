---
id: SKT-0129
title: >-
  Project declarative git blueprint sources without overwriting instance-owned
  state
status: Done
assignee:
  - '@loop-root'
created_date: '2026-10-12 12:21'
updated_date: '2026-10-12 16:45'
labels:
  - kubernetes
  - control
dependencies: []
priority: medium
ordinal: 252000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
An external controller that manages a Kubernetes-backed instance needs to declare which git blueprint sources the instance should track, and change that set over time. Today the configured sources live only in mutable control state (control.State.BlueprintSources, written through the control API), and STATE_GIT_SOURCE_CONFIGMAPS only maps source IDs to storage ConfigMap names. A controller therefore has two bad options: ignore later source changes, or overwrite the instance's control state and clobber what the running instance wrote (sync status, applied revisions, console-added sources). The engine needs a supported declarative input for the desired source set that it reconciles into its own state, with a clear ownership split between declared and console-managed sources. Nothing deployment-specific belongs here: the seam must work for any operator or Helm user.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 A documented declarative input (env, mounted file or named ConfigMap) declares the desired git source set; the engine reconciles added, changed and removed declared sources into its state on start and on change, without a restart where the backend supports watching
- [x] #2 Reconciling declared sources never rewrites instance-owned state: sync status, applied revisions and console-added sources survive byte-identical, proved by a test against the Kubernetes state backend fake and the file backend
- [x] #3 Ownership is explicit: a declared source cannot be edited or deleted through the control API (refused with a reason the console shows), console-added sources are unaffected, and docs/kubernetes.md describes the contract
- [x] #4 Chart exposes the declarative input with schema validation and render tests; file mode behaviour is unchanged
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [ ] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [ ] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Reproduce missing declarative projection through the public boundary; implement independent desired-set ownership reconciliation preserving runtime state; prove file and fake Kubernetes backends, API refusal and schema/render contract; guarded review and exact composed gate before publication.

Runtime wiring ownership gap resolved: include ha_main.go declarative setup and leader-fenced start/change wiring, blueprintadmin.go and ha_blueprintadmin.go ownership DTO/refusal plumbing, plus focused tests. Preserve read-only standby/acquisition and Activate preparation-gated mutations; no core HA seam change.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
loop18: two change/verify cycles including one bounded root composition repair, review-repairround1. Fail-first real executable/API proved missing declared input; desired-set reconciliation preserves owned lifecycle/console bytes on file/fake Kubernetes, refuses actual API edits/deletes with UI explanation, projects whole-directory source file changes and chart schema/render. Original full local0/securityPASS/CodeRabbit33of33complete0. Shared SM composition delta review caught2HA proof calls using old signature; root added only false/true publication args, all assertions unchanged, same focused process/HA/backend check0 and securityrepairdeltaPASS. Final38c8935398e46bef0c2c3325626efefb0f817a4e exact CI38276281184 attempt1 all11required jobs success, none skipped/cancelled. Owner CI-first composed amendment used; no live cluster/credentials/downgrade or browser proof claimed. Accepted and Done. Evidence retained privately in root loop artifacts, not public files.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Declarative source projection landed38c8935398e46bef0c2c3325626efefb0f817a4e with file/fake Kubernetes state-byte preservation, actual executable/API ownership refusal, guarded full/delta review and all-required exact CI38276281184 green.
<!-- SECTION:FINAL_SUMMARY:END -->
