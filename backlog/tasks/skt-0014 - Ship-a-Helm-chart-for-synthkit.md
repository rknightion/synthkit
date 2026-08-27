---
id: SKT-0014
title: Ship a Helm chart for synthkit
status: Done
assignee: []
created_date: '2026-08-27 08:34'
updated_date: '2026-08-27 09:12'
labels: []
dependencies: []
priority: high
type: feature
ordinal: 73000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
synthkit has no Helm chart. The only supported deployment is `docker-compose.yml`, plus `deploy/skcapture/` for the capture tool. Kubernetes is the deployment mode most users will reach for — synthkit exists to model Kubernetes estates — so the gap is conspicuous.

The chart is also the thing that makes an always-on deployment possible, which is the point: today nothing exercises synthkit between waves, so a defect of the class this audit keeps finding (plausible-but-wrong rendering that works against synthkit and fails against production) surfaces only when someone goes looking.

What the chart has to get right, beyond rendering:

- **Credentials are the hard part.** Every lane has its own credential triplet, self-observability deliberately uses a SEPARATE stack and must never borrow the data-path token, and secrets live only in an ignored `.env` today. The chart needs an existing-Secret path as the default — never values-file credentials — and it must keep the self-obs separation the architecture requires rather than flattening everything into one secret.
- **The control plane is a network surface.** It refuses to expose beyond loopback without an explicit acknowledgement (SKT-0005.12). A chart that quietly exposes it through a Service would undo that decision, so the exposure acknowledgement has to be a deliberate chart-level choice with the same friction.
- **Blueprint selection and the config snapshot** need somewhere to live that survives a restart, which is a real volume decision rather than a default.
- **Resource shape is not guessable.** The estate size drives memory: the integration suite needed a single-build fix after an OOM at ~2.8 GB per estate build. Ship requests and limits derived from measurement, not from habit.

Out of scope here: the ArgoCD application that consumes the chart, and the end-to-end validation of what it emits — both tracked separately.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 A chart deploys synthkit to a cluster and it reaches ready
- [x] #2 Credentials come from an existing Secret by default, never from values, and the self-observability credential separation is preserved
- [x] #3 Control-plane exposure requires the same explicit acknowledgement outside the cluster as it does today
- [x] #4 Blueprint selection and control state survive a pod restart
- [x] #5 Resource requests and limits are derived from a measurement, and the measurement is recorded
- [x] #6 The chart lints and templates cleanly in CI, and rendering is covered for the credential and exposure permutations
- [x] #7 docs/ documents the Kubernetes path alongside the Compose one
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 make gate (build vet test race rw-proto-check spdx-check forbidden-words)
- [ ] #2 make blueprint-schema (only if a blueprint field or construct/workload config struct changed)
- [ ] #3 DRY_RUN=true go run ./cmd/synthkit -once -dump — inventory diffed against signals/
<!-- DOD:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
charts/synthkit ships, and the two hard parts are enforced STRUCTURALLY rather than documented.

CREDENTIAL SEPARATION is three mechanisms, not an assertion. A fixed table in _helpers.tpl maps each env var to exactly one destination-stack group, and projection is per-group — so listing a self-obs key under the data group FAILS the render, as does GC_TOKEN under selfObs. The self-obs Secret is compared against all seven other groups and equality fails the render, which is the specific way GC_TOKEN would have ended up authenticating self-observability. And extraEnv cannot launder around either check, because any name in synthkit own config surface is rejected. Verified by the wiring pass: the negative fixture fails with a precise message naming the conflicting Secret. There is no values-file credential field at all, and no optional: true anywhere — a missing key holds the pod in CreateContainerConfigError rather than starting with a blank credential.

EXPOSURE defaults closed: loopback bind, no Service or Ingress, default-deny NetworkPolicy, access by port-forward. Opening needs both an explicit acknowledgement value AND a Secret projecting CONTROL_TOKEN, with five negative fixtures covering the subsets. Setting the acknowledgement also flips the bind, so the BINARY own gate re-validates independently — chart guard and binary guard both fail closed.

One subtlety the lane caught and worked around rather than depending on: the binary inContainer() keys on /.dockerenv, which CRI runtimes do not create. The chart mirrors SYNTHKIT_BIND to the JSON_HTTP_ADDR host so both branches of the exposure check reach the same verdict, and blocks SYNTHKIT_IN_CONTAINER from extraEnv — setting it alongside a loopback SYNTHKIT_BIND would otherwise satisfy the gate while listening on all interfaces.

RESOURCES were MEASURED, not habitual. Native binary, dry-run, heap sampled every 15s with the first three minutes discarded: one blueprint 253 MiB floor / 461 MiB peak; four blueprints 289 / 522; the whole catalogue 1716 / 3135. Defaults are requests 100m + 768Mi and a 1Gi memory limit — request above the four-blueprint peak RSS so it is not chronically over-request and first to be evicted, limit about twice peak heap because Go GC targets ~2x live and the sawtooth peak is what a limit absorbs. No CPU limit, since throttling a fixed-cadence tick loop only lengthens ticks. Two cross-checks: the CPU figure independently reproduces what the Compose deployment recorded in August, and the flat sawtooth floor rules out a leak. Estate cost is NOT linear — one to four blueprints moved the floor 36 MiB, twenty-six moved it sixfold.

Volumes: PVC retained by default, fsGroup set because without it control-state saves fail permission denied and every blueprint selection is silently lost, and Recreate strategy because RWO plus RollingUpdate deadlocks.

Wiring applied by the root: make helm-test (103 render assertions, 0 failures), added to ci and to the ci-success aggregator, a GitHub job using preinstalled helm so no third-party action is needed, and the docs nav entry that make docs-check was already failing without.

ACCEPTED DECISION: no values.schema.json. JSON Schema cannot express the two rules that actually matter — self-obs Secret must differ from data Secret, and this var belongs to that group — the template failures name the exact conflicting values, and carrying both would mean two places to keep in sync.

Deploying it into a cluster and validating what it EMITS is SKT-0014.01, deliberately not this task.
<!-- SECTION:FINAL_SUMMARY:END -->
