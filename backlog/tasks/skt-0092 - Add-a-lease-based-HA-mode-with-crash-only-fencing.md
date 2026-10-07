---
id: SKT-0092
title: Add a lease-based HA mode with crash-only fencing
status: In Progress
assignee:
  - '@loop-root'
created_date: '2026-10-07 20:30'
updated_date: '2026-10-07 21:41'
labels:
  - feature
  - ha
dependencies: []
priority: high
ordinal: 214000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Allow two synthkit pods to run one instance: one leader pushing, one hot standby. Needed so node churn, rollouts and evictions cost seconds of gap instead of minutes (today a restart pays PVC reattach + startup + delivery-aware readiness).

Design (from an adversarial review of the queue and runner code):
- client-go leader election on a pre-created coordination.k8s.io Lease named by config. Do NOT use ReleaseOnCancel: delivery queues flush with context.Background() (internal/sink/queue/queue.go) and drain for SEND_DRAIN_DEADLINE, so cancelling the run context does not stop pushes.
- Fencing is crash-only: OnStoppedLeading exits the process immediately with no drain; the kubelet restarts it as a fresh standby (queues and Runner.Run are start-once, so in-process demotion is not possible).
- Planned handoff on SIGTERM: stop ticking, drain under a hard cap, release the lease explicitly, exit.
- Startup validation in HA mode: worst-case push lifetime (HTTP timeout + per-sink retry budget, incl. the 5 min OTLP traces budget in internal/sink/httpretry) must be below leaseDuration minus renewDeadline; HA mode shortens drain and retry budgets to fit.
- Leader-only: every sink push, Fleet Management registration and heartbeats, RUM sessions. The standby builds the runner (no side effects at build time) but never calls Run.
- Standby: mutating /control/* routes return 503 with a not-leader code; no ProbeWrite, no fetch-status persistence.
- Self-obs resource attribute ha.role=leader|standby so standby gauges do not double count.
- Handoff has restart semantics (counter reset, shape RNG reset, pod-churn reset, in-flight traces truncated); identities stay stable. Document this.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 HA_MODE=lease runs leader election against a named Lease; default behaviour unchanged when unset
- [ ] #2 Losing the lease exits the process without draining; a test proves no push is attempted after OnStoppedLeading
- [ ] #3 SIGTERM performs stop-tick, capped drain, explicit lease release, exit in that order
- [ ] #4 Startup fails in HA mode when worst-case push lifetime >= leaseDuration - renewDeadline
- [ ] #5 Standby rejects mutating control routes with 503 not-leader and performs no state writes
- [ ] #6 Self-obs carries ha.role; docs describe handoff semantics
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [ ] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [ ] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Isolated candidate with frozen ownership; acceptance reproduction, focused checks and just check; independent review before root landing. HA seam design is independently challenged and frozen first.
<!-- SECTION:PLAN:END -->
