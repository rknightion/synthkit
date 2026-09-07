---
id: SKT-0058
title: k3d image import reports success while the nodes loaded nothing
status: To Do
assignee: []
created_date: '2026-09-07 12:29'
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
- [ ] #1 The import phase proves the receiver image is resident in every cluster node containerd namespace after import, rather than trusting the k3d exit code
- [ ] #2 A failed or partial import is retried once, and a still-absent image fails the import-images phase with a message naming the node and the image reference
- [ ] #3 A retained diagnostic records the per-node residency check result on both the passing and the failing path
- [ ] #4 The skcapture lab runner applies the same residency proof, because it imports through the same unreliable path
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [ ] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [ ] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->
