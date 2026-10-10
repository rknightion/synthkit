---
id: SKT-0112
title: Keep the control-dash race package inside the test timeout
status: Parked
assignee:
  - '@loop-root'
created_date: '2026-10-09 11:19'
updated_date: '2026-10-10 21:37'
labels: []
dependencies: []
type: bug
ordinal: 234000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Under `just race`, cmd/synthkit-control-dash took 363s (CI 37810460433), 437s (37884775156) and 551s (37889782301), and exceeded the default 10m test timeout in CI 37855685768 at 14719af with TestShippedScenarioImpactQueriesUseDerivedFamilies still running at 9m54s. The same timeout made local `just check` red for unrelated lanes. The test dry-runs every shipped scenario serially under the race detector, so main CI and every composed gate will go red as the catalogue grows.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 go test -race ./cmd/synthkit-control-dash -count=1 completes in under 5 minutes on the CI go job, measured at the landed SHA
- [ ] #2 TestShippedScenarioImpactQueriesUseDerivedFamilies still covers every shipped scenario and keeps every assertion; no test is skipped under race and no -timeout is raised
- [ ] #3 just check exits 0 at the landed SHA
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [ ] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [ ] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
loop7: record race timing before/after test-only optimization retaining every scenario/assertion; root review and CI timing after landing.

loop8: uncontended phase1 race timing first; only if over180s profile and optimize generic derivation with identical output and all scenario assertions preserved. Root reviews and lands; CI plus composed gate prove remaining criteria.

Generic profiled hot-path optimization preserving retained output golden and shipped scenario assertions; uncontended race timing twice under180s, high independent review and root landing; exact-SHA CI plus composed gate.

Apply exact retained reviewed patch, green local gate, root land and exact-SHA CI timing under300s; revert on red or slow verdict. No new implementation attempt.

Profile unchanged package before any edit; use cause-backed structural test reduction preserving all scenarios and assertions. Two authorized implementation cycles. Exact commit CI below300s or immediate revert.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
loop7 attempt1 unsuccessful: GOMAXPROCS2 experiment >240s reverted, baseline >420s, unchanged gate >900s; no candidate. Root cause-based retry inspects serial unique-blueprint derivation and repeated full catalogue in generator-shape tests; preserve real shipped scenarios/all assertions, no runtime change.

loop7 attempts2 test-only experimental cycles, both reverted clean; race baseline terminal default-timeout600.516s, single-blueprint experiment externally bounded210s. All14 scenarios share1 blueprint; shape-generator tests do not derive (root initial hypothesis corrected). Load~180/10CPUs materially confounds timings; no idle impossibility claim. Park needs=owner; recommend uncontended exact package timing before deciding runtime scope. No timeout/skip/assertion/justfile/runtime changes retained.

loop8 phase1 sole loop lane: exact base a9a31e1121cd25d6b025584b99d14d6af88196aa race package passed exit0 real569.58s user564.15 sys50.12; load36.73/38.68/46.28 before9.89/19.61/32.74 after. Over180s releases authorized phase2 profile/generic optimization. Ambient load not cause proof; no source changes in measurement.

loop8 attempts0/2 implementation cycles; read-only profile409.76s race exit0,53.5GB allocations dominated shared runner/state/promrw; app2.58%. No candidate or changes. Park needs=owner: grant bounded shared-path generic optimization outside explicit current dirs, preserving all output, or revise requirement. Resume from preserved profile/golden; all14 scenarios/11 queries/full manifest repeat-identical on unchanged code, not fix proof.

loop10: complex implementation cycles2/2 and owner-granted SUPER1/1 spent, no ceiling reset. First revised candidate measured229.46/227.35s uncontended and failed180s. Final generic synchronized writer-local clone cache candidate preserves golden/scenarios/assertions, unchanged focused/race/reference/CLI proofs and three independent correctness verdicts; real allocation9.82% lower with28.85% cross-call reuse. Final under180 timing and full gate UNOBSERVED: two bounded20m host quiet-cut episodes failed with continuing foreign gates. Park needs owner to reserve exclusive host window, then verify same retained candidate, not recode; no permission to stop siblings or relax acceptance. If final timings fail, further implementation requires owner ceiling/scope decision. Retained cost estimate roughly290MiB across84 diagnostic writers;16MiB/16384 caps apply per-writer charged cache, not global heap/RSS. Final candidate/evidence operator-held; no source land or CI performance claim.

Final retained candidate landed at9e53587826f0c5053cf27ff1482c3b66b715093e; CI38041646449 green, but go just race measured489.278s for control-dash, above300s criterion. Immediately reverted asc3492b04510375b8e9b20af9465367b47a9edf11. No new implementation attempts; previous ceiling exhausted. Park owner scope/ceiling decision, recovery CI pending.

Recovery CI38043010547 atc3492b04510375b8e9b20af9465367b47a9edf11 succeeded withci-success. Criterion remains failed489.278s; prerequisite chain parked.

This run: profile baseline timed out600.791s/wall610.74s, one of two new authorized change-and-verify cycles used. Exact8file candidatepatch4c3cb85f4c9aadb3c61ca7d2dffcd5fe9ad590a4a2a9ad2203d16f4a9cd3115a independently reviewed clean, standalone race274.083s but module-wide516.005s, full gategreen beforelatestprefixbase. Latestbase851d879 gate failed staleignored embeddedUI output; generation via existing justui denied beforeexecution on overwrite permission. No bypass/no CItrial/no land/no revert needed. Parkdefect toolpermission: owner must permit archiving and overwriting ignoredUI output, then regenerateactualembedinputs/gate samecandidate andexactSHA CI under300s orrevert. Priorattempt history notreset; onecycleleft.

loop12: one of two new authorized implementation cycles used; previous-loop counts preserved. Park defect on denied ignored-output overwrite prerequisite, latest-base gate not green and no exact CI trial or performance land.
<!-- SECTION:NOTES:END -->
