---
id: SKT-0092
title: Add a lease-based HA mode with crash-only fencing
status: Done
assignee:
  - '@loop8'
created_date: '2026-10-07 20:30'
updated_date: '2026-10-09 17:29'
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
- [x] #1 HA_MODE=lease runs leader election against a named Lease; default behaviour unchanged when unset
- [x] #2 Losing the lease exits the process without draining; a test proves no push is attempted after OnStoppedLeading
- [x] #3 SIGTERM performs stop-tick, capped drain, explicit lease release, exit in that order
- [x] #4 Startup fails in HA mode when worst-case push lifetime >= leaseDuration - renewDeadline
- [x] #5 Standby rejects mutating control routes with 503 not-leader and performs no state writes
- [x] #6 Self-obs carries ha.role; docs describe handoff semantics
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [x] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [x] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Isolated candidate with frozen ownership; acceptance reproduction, focused checks and just check; independent review before root landing. HA seam design is independently challenged and frozen first.

Authorized attempt 4/review round 4: repair retained fatal/normal terminal arbitration; deterministic failing-first exit windows and real client-go renewal-loss proof, paired dump and final gate, then independent security delta review.

Authorized loop3 attempt5: reapply retained candidate, repair fatal/normal exit arbitration and build failure; failing-first seven exit-window tests, real client-go renewal loss proof, byte-identical HA-off dump, local gate and pre-land reviews. Attempt6 rescue only if attempt5 fails.

Proof-and-land only: reapply exact retained candidate without code changes; historical pre-arbiter red is accepted by owner, fresh green identity and security round5 required.

loop8 read-only V92 at detached2f38dbbba291b794213639a2428b69d3023e58cd after acceptedHAshutdownrepair, composedgate0 and exactCI37962316276 green. Verify allsixcriteria by namedexisting tests/docs, no codechange or extra implementation cycle.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
loop1: implementation attempts 3 (two worker full-gate cycles, one root rescue); review-repair rounds 3 reached. Do not reset counts next loop. Final independent delta review REJECT: fatal cancellation lacks terminal arbitration for former-leader/late normal exits; source-feasible late/standby windows still select Exit(0) after fatal selection. No live unsafe release claimed. Remaining implementation specialist attempt 4 cannot bypass exhausted review ceiling. Resume only after owner raises review-repair ceiling, then implement serialized fatal/normal exit admission and deterministic bounded late/standby interleaving proof without gate lock across network I/O. Initial integrated coordinator proof reproduced actual post-loss seal/release; shared fatal context repaired that overlap and retry cancellation identity, but additional arbitration remains. Local reviewed snapshot 5c0c189a29cd08eb5703871918e70481c9ab466f plus dirty four-file root delta retained on candidate branch; no HA landed or pushed. Evidence /tmp/H92I-root-rescue/security-manual-notes.log. Full-byte HA-off paired dump proved; actual client-go local loss/Fleet/delivery tests and exact delta CodeRabbit green. In-flight final local gate allowed to finish; not acceptance.

Final root rescue just check command exited 0, but its all-tracked before/after hash wrapper failed on symlink-to-directory; do not claim wrapper identity proof. Independent security rejection/round ceiling remains controlling. Final 51-file unlanded candidate and four-file exact review delta preserved; no candidate or tree deletion.

Owner decision for the next run: one further review-repair round (round 4) is granted on the retained candidate, within implementation attempt 4. The lane may also own the sink files it asked for: internal/sink/promrw/promrw.go and its test, internal/sink/loki/loki.go and its test, internal/sink/otlp/egress.go with its egress tests, internal/sink/pyroscope/pyroscope.go and its test.

loop2: implementation attempt 4 of 4 consumed; review round 4 not reached because candidate fails compilation (cmd/synthkit/ha_main.go:647 missing return), just check exit 1. Seven deterministic bounded exit-window tests reproduced retained races. No HA land, no green repaired lifecycle/dump/CodeRabbit. Failed tree 0747197737a2d19245ac465965d34711189b3650 retained in H92X worktree with exact binary patch capture. Resume only after owner authorizes further change-and-verify cycle; no ceiling reset.

loop3: implementation change-and-verify attempts5 completed; final authorized rescue slot6 used read-only (no extra change-and-verify cycle); review round4 REJECT major proof provenance. Exact retained candidate already includes arbiter; historical seven runtime-red cases precede it. Compiler-only panic repair passed unchanged seven tests, actual client-go/local Lease renewal-loss cases, HA-off coordinated full-byte dump, just check and CodeRabbit0. No test weakening, no commit/land. Final Astra confirms acceptance premise incompatible. Resume only genuine identity-bound missing red evidence or owner-corrected proof contract plus authorized security review. Current candidate tree2c03f0dad824ac42676b127f91d15555b770f4be retained uncommitted; full binary patch/base and all gate/review evidence operator-held local evidence; security evidence sibling H92XR/; no additional implementation authorized.

loop4: no new implementation attempt; security round5 ACCEPT0 critical/major/minor, fresh retained identity/focused tests/dump/gate0, code landed ddee479a9e4c2b56021de3f4de3dd2c7b9dfdcc0. Composed first env failure repaired append-only; second composed source357b29 red TestHAActualFaro5000WholeWrite late server count121->122. SKT-0109 (resolve late Faro POST proof-oracle failure) owns unresolved red: source shows server-arrival oracle defect,20 bounded focused passes insufficient. No runtime rollback justified. Not Done; resume only owner-authorized test-proof repair and green composed gate; prior attempts remain5, no new implementation.

2026-10-09 planner: composed just check green at 14719af after SKT-0109; closing this task now waits on SKT-0113 (flaky HA planned-release lifecycle test, red in CI 37860640279) and a clean CI run, then verification of #1-6 against main.

loop7 verification attempts0; V92 dependency park because SKT-0113 (flaky HA planned-release lifecycle test) returned unresolved deterministic reproduction question. No criteria checked. Resume read-only six-criterion verification after accepted repair and exact-SHA green upstream CI.

loop8 attempts0 implementation (prior5 unchanged), readonlysixcriterionverification atdetached2f38dbbba291b794213639a2428b69d3023e58cd.11 focusedcommands24actualtop-leveltests0/zeroskips/emptymatches; namedLease/defaultnonemptyemission, realrenewalloss/no newpush, signal/release/cap/order, equalitybudget/partialFaroSigiljoins, standby503/no statewrites, decodedOTLPstandby/leaderroles andexactdocs allPASS. CI37962316276 alltenjobs success; composed2f38justcheck0. DoDgen N/A readonly/configunchanged; no freshdump: prioracceptedHA-offpaired inventoryproof retained, no renderer/config changed by thisverification. LiveHAreliability/sharedstate/APIserverRBAC/network-saturatedhandoff/processprofiles notclaimed.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
All six lease-HA acceptance criteria verified read-only against exact integrated2f38SHA containing acceptedplanned-shutdownrepair, with namedexecutedtests, realHTTP/subprocess/OTLP boundaryevidence andexactdocumentation. UpstreamCI/composedgate0; livecluster reliability remains separate.
<!-- SECTION:FINAL_SUMMARY:END -->
