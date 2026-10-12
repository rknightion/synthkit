---
id: SKT-0129
title: >-
  Project declarative git blueprint sources without overwriting instance-owned
  state
status: To Do
assignee: []
created_date: '2026-10-12 12:21'
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
- [ ] #1 A documented declarative input (env, mounted file or named ConfigMap) declares the desired git source set; the engine reconciles added, changed and removed declared sources into its state on start and on change, without a restart where the backend supports watching
- [ ] #2 Reconciling declared sources never rewrites instance-owned state: sync status, applied revisions and console-added sources survive byte-identical, proved by a test against the Kubernetes state backend fake and the file backend
- [ ] #3 Ownership is explicit: a declared source cannot be edited or deleted through the control API (refused with a reason the console shows), console-added sources are unaffected, and docs/kubernetes.md describes the contract
- [ ] #4 Chart exposes the declarative input with schema validation and render tests; file mode behaviour is unchanged
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [ ] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [ ] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->
