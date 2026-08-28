---
id: SKT-0016
title: 'CI e2e fails: 131 CloudWatch families declared by -dump are never received'
status: To Do
assignee: []
created_date: '2026-08-28 09:03'
updated_date: '2026-08-28 13:48'
labels: []
dependencies: []
priority: high
type: bug
ordinal: 90000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
**This task's original description was WRONG and is replaced.** It said the failure was a state-volume permission probe. It is not. The `permission denied` lines in the log come from `readiness_test.go`'s `synthkit-readiness-unwritable` container, which is a deliberate negative test that makes the volume unwritable on purpose. That output is expected and has always been there.

The real failure, from the `e2e` job log at `29a6fac`:

    e2e_test.go:226: received: 648 metrics, 3 log sources, 3 trace services, 3 sigil kinds
    e2e_test.go:231: expected (from -dump): 646 metrics, 3 log sources, 3 trace services, 3 sigil kinds
    e2e_test.go:239: telemetry declared by -dump but NOT received (131 entries):
    --- FAIL: TestDockerE2E

The two sides disagree in **both** directions: the receiver sees two families `-dump` does not declare, and 131 families `-dump` declares never arrive.

**It is not intermittent.** The count is byte-identical -- 648 received, 646 expected, 131 missing -- on every one of `4fa184e6`, `c2c5ae0f`, `10197de6`, `6e74d7ee` and `29a6facf`. The earlier belief that it was flaky came from reading the permission-denied noise rather than the assertion.

**It regressed at `4fa184e`,** the SKT-0013.06 commit that stopped the capture receiver inferring classic histograms from metric-name suffixes. `e2e` passed on the five commits before it and has failed on every commit since.

**The likely cause, and it is a name collision the repository already warns about.** All 131 missing entries are CloudWatch `aws_*` families, and they arrive in pairs: `<name>` and `<name>_sample`. CloudWatch five-stat expansion produces `<name>_sample_count`, which ends in `_count` -- a classic-histogram component suffix. `ClassicHistogramFamily` (`internal/inventory/histogram.go:37`) trims it and yields the base `<name>_sample`, a family no emitter ever produced. Its own doc comment at `:34` names this exact hazard. The receiver and `e2e/inventory.ParseDump` now apply that rule differently, so they disagree about which names exist.

This is the metric-side twin of SKT-0010.14, which unified the same kind of three-way desync for logs and deliberately left metrics alone.

**Do not fix this by relaxing the fold rule back to suffix inference.** SKT-0013.06 established that `le` is the only proof of a classic histogram, and measured that change: it altered the histogram block of no shared family and exactly one name. Reverting it would put inferred evidence back in the corpus. The correct fix makes the CloudWatch five-stat suffixes non-foldable, or makes both producers apply the one shared rule, or both.

Reproduce with `make e2e` before proposing a cause. `e2e/` is `//go:build e2e` so `go test ./...` does not cover it.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 The receiver and ParseDump agree on which metric families exist, so received and expected match
- [ ] #2 CloudWatch five-stat names are not folded as classic-histogram components, and a test pins that
- [ ] #3 The le-as-proof rule from SKT-0013.06 is preserved, not reverted
- [ ] #4 make e2e is reproduced locally before a cause is proposed, then passes locally and in CI, and ci-success goes green on main
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 make gate (build vet test race rw-proto-check spdx-check forbidden-words)
- [ ] #2 make blueprint-schema (only if a blueprint field or construct/workload config struct changed)
- [ ] #3 DRY_RUN=true go run ./cmd/synthkit -once -dump — inventory diffed against signals/
<!-- DOD:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
2026-08-28 wave covering check: exact-SHA CI run 33168159591 at 6e74d7ee62d8c3a02209de5c4bb093b069843440 failed only in e2e job 98839548298. TestDockerE2E logged control persist and startup state-volume probe failures for both ./.control-state-3038999027.tmp and /app/.control-state-3794945131.tmp with permission denied; the remaining exact-SHA jobs passed. This matches the pre-existing intermittent state-volume permission failure and is outside the OTLP/high-DPM wave. Resume at AC #1: run make e2e locally more than once until the failure is reproduced, then inspect the failing container image runtime UID/GID, working directory, configured control-state path, mount target, and directory ownership before changing code. Do not weaken the startup writability probe.

2026-08-28 correction: the original description and acceptance criteria were WRONG and have been replaced. They read the readiness_test.go synthkit-readiness-unwritable negative test's expected permission-denied output as the failure. The real assertion is the -dump-vs-receiver subset correlation, constant at 648 received / 646 expected / 131 missing across 4fa184e6, c2c5ae0f, 10197de6, 6e74d7ee and 29a6facf. Not intermittent; regressed at 4fa184e. All 131 are CloudWatch aws_* families in <name> / <name>_sample pairs.
<!-- SECTION:NOTES:END -->
