---
id: SKT-0035
title: >-
  Make upgrade, rollback, and snapshot persistence reproducible from public
  releases
status: Done
assignee:
  - '@codex'
created_date: '2026-08-29 19:05'
updated_date: '2026-08-30 14:17'
labels: []
dependencies: []
references:
  - e2e/acceptance/2026-08-29-fresh-container-findings.md
priority: medium
type: feature
ordinal: 126000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
SKT-0011 scenarios F2-F4 could prove the running digest identity but could not locate a second verified public digest for upgrade/rollback, and a bounded restart did not establish selected-blueprint and control-state persistence. The documented lifecycle path is not independently reproducible from a clean clone.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 The documentation identifies two immutable published digests suitable for an upgrade and rollback exercise
- [x] #2 Upgrade and rollback verify reported version and revision against each digest
- [x] #3 Counter behavior across restart is checked without interpreting resets as spikes
- [x] #4 Selected blueprints and control state are read back after restart from a config snapshot
- [x] #5 The lifecycle acceptance path is runnable without private release metadata
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [x] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [x] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Lane A makes the public digest lifecycle and snapshot checks reproducible; root runs sequential published-image upgrade, rollback, and persistence evidence.

2026-08-31 Lane A: regress the stale signer-digest failure, trust the frozen workflow path plus issuer/source repository while retaining source digest, OCI labels, and binary identity; update public docs and prepare the root lifecycle verification.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
2026-08-30 closeout: rc.37 -> rc.38 -> rc.37 completed with exact index/platform/runtime identities and complete revisions. Eight selected blueprints and volume_multiplier=1.25 matched before upgrade, after upgrade, and after rollback. Live resets rose across both restarts and rate remained nonzero. Docker Desktop containerd identity was closed through ImageManifestDescriptor. The documented rc.37 verify-image signature step returned signature_verification_failed.

2026-08-31 correction: rc.37 and rc.38 are both properly signed and independently verified. The defect was the deploy helper pinning a June signer workflow SHA that matched no published image; the helper now trusts the frozen release workflow identity while retaining digest, source, OCI-label, platform-manifest, and binary checks. Final gates: just check, just dump, just e2e, and exact-digest rc.49 published-e2e passed.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
Parked at AC#5. The lifecycle mechanics and state persistence are proven, but the public procedure still requires rc.37 signature verification that fails. Resume by establishing a valid signed prior immutable release or correcting the public signature/provenance evidence without weakening verification.

2026-08-31: Corrected the stale signer-workflow pin and independently verified rc.37 and rc.38 signature/provenance plus immutable runtime identity. The public lifecycle path is reproducible; all final gates passed.
<!-- SECTION:FINAL_SUMMARY:END -->
