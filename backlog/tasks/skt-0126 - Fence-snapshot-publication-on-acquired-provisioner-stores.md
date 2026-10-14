---
id: SKT-0126
title: Fence snapshot publication on acquired provisioner stores
status: In Progress
assignee:
  - '@codex'
created_date: '2026-10-11 19:48'
updated_date: '2026-10-14 20:08'
labels: []
dependencies:
  - SKT-0099
references:
  - internal/smstore/file.go
priority: low
type: bug
ordinal: 248000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Independent delta security review of diskless SM provisioning found a non-blocking lifecycle contract gap. AcquireFileStore returns an owned Store whose Load and UpdateProvision refuse use after release, but embedding fileStore inherits PublishSnapshot. That method can independently reacquire O_EXCL after release, contrary to the all-read/write lifetime wording; while still owned it self-contends. Production provisioner code never calls publication, so no lock bypass, activation race recurrence or runtime misuse was demonstrated. Keep the reviewed candidate unchanged and address this capability/documentation gap separately. Code acceptance still requires its outstanding full gate and publication evidence.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 The acquired provisioner store has an explicit consistent snapshot-publication capability contract, including after release, without inferring ownership from an existing lock file.
- [ ] #2 Ordinary emitter snapshot publication retains positive O_EXCL acquisition and explicit contention/stale-lock failures; provisioner read/update lifetime remains fenced.
- [ ] #3 A bounded real private-file check proves any forbidden publication leaves snapshot and pending/ownership evidence unchanged.
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [ ] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [ ] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Make acquired provisioner snapshot-publication capability explicit; fail-first private-file lifecycle proofs, preserve ordinary emitter lock behavior, then composed review/gate.
<!-- SECTION:PLAN:END -->
