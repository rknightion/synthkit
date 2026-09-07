---
id: SKT-0058
title: k3d image import reports success while the nodes loaded nothing
status: Done
assignee:
  - '@codex'
created_date: '2026-09-07 12:29'
updated_date: '2026-09-07 14:05'
labels:
  - lab
dependencies: []
references:
  - e2e/lab/permutation.sh
  - e2e/lab/skcapture/run.sh
  - e2e/lab/run.sh
priority: high
type: bug
ordinal: 154000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
The nightly signal-fidelity-k3d matrix has been red on three of the last five runs, losing a different permutation each time. Run 34101218418 lost alloy-otlp-podlogs; run 34020767181 lost otel-collector-prom and otel-receivers. The retained diagnostic shows the cause is not the permutation under test: k3d writes the image tarball to the per-cluster image volume, both nodes fail to read it back with "ctr: open /k3d/images/<cluster>-images-<ts>.tar: no such file or directory", and k3d then prints "Successfully imported image(s)" and exits 0. e2e/lab/permutation.sh trusts that exit code, so the receiver Deployment is admitted with an image that is never pulled, sits in ErrImageNeverPull, and the permutation dies in deploy-receiver having observed nothing. A harness failure is being spent as a permutation outcome, which is the confusion the existing wait_for_apiserver comment in that file exists to prevent. The race is timing-dependent, so a non-zero exit code cannot be relied on and residency must be proven directly.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 The import phase proves the receiver image is resident in every cluster node containerd namespace after import, rather than trusting the k3d exit code
- [x] #2 A failed or partial import is retried once, and a still-absent image fails the import-images phase with a message naming the node and the image reference
- [x] #3 A retained diagnostic records the per-node residency check result on both the passing and the failing path
- [x] #4 The skcapture lab runner applies the same residency proof, because it imports through the same unreliable path
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [ ] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [ ] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Prove exact image residency in every node containerd namespace after import in both runners, test the false-success case before the fix, retry once on missing residency, retain per-node diagnostics, validate statically, then root reviews and integrates.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Implemented direct per-node containerd k8s.io residency proof in both runners, one bounded import retry, and retained diagnostics on pass and failure. Simulations cover immediate pass, pass after two imports, persistent miss after two imports naming both nodes/reference, and untagged-reference normalization. Retained evidence: codex/scratch/wave-2026-09-11/residency/. bash -n, shellcheck and static lab-check passed. L1/L2 CodeRabbit completed; the sole minor normalization finding was reproduced and fixed in both scripts. No live lab or live containerd proof was run.

Final integrated just check passed. One safe explicit-selection dump exists; its machine-name comparison against signals is incomplete for prose/expanded families and is not claimed full catalogue conformance. No renderer or construct changed; no live k3d run or e2e was authorized for this lane. Conditional generation DoD is not applicable to these scripts. The unchecked dump DoD is explicitly unproven, not a hidden pass.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
The import decision no longer trusts k3d exit status. Static and mocked acceptance is proven; next nightly supplies live residency evidence.
<!-- SECTION:FINAL_SUMMARY:END -->
