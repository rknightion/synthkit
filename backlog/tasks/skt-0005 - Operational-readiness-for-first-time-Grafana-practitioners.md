---
id: SKT-0005
title: Operational readiness for first-time Grafana practitioners
status: In Progress
assignee:
  - '@codex'
created_date: '2026-08-20 10:58'
updated_date: '2026-08-20 13:11'
labels: []
dependencies: []
references:
  - README.md
  - docs/getting-started.md
  - docs/quickstart.md
  - plugins/synthkit/skills
  - AGENTS.md
priority: high
type: feature
ordinal: 5000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Make synthkit easy to discover, deploy, configure, author blueprints for, and operate for Grafana-fluent sales engineers and observability architects who have never used the project. This initiative excludes telemetry-signal realism, which is owned by a concurrent campaign. It owns the user journey from clone through a focused fake workload appearing in Grafana, plus repository-local agent instructions, distributable skills, marketplace metadata, and operational safety.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 A fresh user has one documented, focused path from clone to a running synthetic workload and can verify metrics, logs, and traces with concrete expected identities
- [ ] #2 Direct and Docker deployment defaults are safe, explicit about dry-run versus live mode, and validated through their real entry points
- [ ] #3 Blueprint creation, validation, selection, activation, staging, and restart semantics are understandable without reading Go source
- [ ] #4 Repository agent instructions and skills let Claude Code and Codex create a blueprint and guide local deployment using only the cloned repository
- [ ] #5 Every audit finding is either implemented, represented by a dependency-aware child task, or explicitly excluded as telemetry-realism work
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 make gate (build vet test race rw-proto-check spdx-check forbidden-words)
- [ ] #2 make blueprint-schema (only if a blueprint field or construct/workload config struct changed)
- [ ] #3 DRY_RUN=true go run ./cmd/synthkit -once -dump — inventory diffed against signals/
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
1. Run six independent read-only audits covering pickup/docs, deployment operations, blueprint/workload UX, agent instructions, skills/marketplace packaging, and realistic forward tests.
2. Synthesize every accepted finding into focused child tasks with explicit dependencies and entry-point acceptance checks.
3. Implement the first wave: safe first-run defaults, broken onboarding examples, canonical agent context, and skill/package correctness.
4. Forward-test changed skills and user journeys in isolated environments, run one integrated CodeRabbit review for code/config changes, then the proportionate repository gate.
5. Finalize each completed task with objective evidence; leave later waves To Do or Parked with precise boundaries.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Campaign started at repository head 76ce61c on 2026-08-20. Six read-only audit lanes were dispatched under doc-0001/doc-0002. Two completed lanes already prove: the primary live path can remain in DRY_RUN, direct execution can inherit a Docker-only 0.0.0.0 control bind with no token, README control examples use port 9090 instead of 8088 and an unqualified scaling key, the default dry-run loads 25 blueprints without naming a first workload, and custom blueprint staging/validation/restart states are underexplained. Remaining audits are still active; telemetry-shape findings are fenced out.

Audit synthesis complete: 16 child tasks now own every accepted finding. Wave 1 active: .01 safe clone-to-live defaults, .02 canonical agent context/sync, .03 helper/secret safety. Dependency chain then unlocks .04 focused first workload, .05 relocatable marketplace, .06 complete skills, .07 beginner docs, .08 upload validation, .09 git/apply state, .10 readiness, .11 loss/capacity, .12 control/file security, .13 optional product lanes, .14 update/rollback, .15 local docs verification, and .16 mutation-safe APIs. No telemetry-realism task or signal-catalog change was created.

Final completeness pass added .17 for mandatory live endpoint/identity/reachability preflight and .18 for reducing always-loaded maintainer history after canonicalization. The initiative now has 18 child tasks; all accepted audit findings have a durable owner.

Wave 1 landed and pushed: implementation 9c93f5c plus tracker finalization ecad9d5. SKT-0005.01 through .03 are Done after four CodeRabbit reviews (final zero findings), make gate, and 26-blueprint dry-run inventory. Wave 2 active with disjoint owners: .04 focused blueprint selection, .05 marketplace relocation, .15 local docs validation, .16 mutation-safe APIs, and .18 slim portable agent contract. Config/control security/readiness tasks remain queued behind these file owners.

Wave 2 landed and pushed on main: implementation e98411b, task finalization 1569d34. Completed SKT-0005.04, .05, .15, .16, and .18 after one integrated CodeRabbit review (5 minor issues, all fixed), make gate, and default/focused dry-run inventories. Wave 3 started with disjoint active lanes SKT-0005.06, .07, .08, and .17; root retains integration, review, Backlog, commit, and push ownership. docs.toml remains unrelated concurrent work and is excluded from campaign staging.

Wave 3 landed in implementation commit 265ecd9 and pushed at merged head dab60de after disjoint Renovate workflow commits advanced origin/main. Completed SKT-0005.06, .07, .08, and .17 after CodeRabbit reviews 7 and 8 (eight-review cap reached), make gate, focused UI/helper/docs checks, and default plus otlp-native dry-run inventories. No live Grafana calls occurred; docs.toml remains unrelated unstaged work. Next dependency-unblocked operational queue starts with .09 and .10, then .12.
<!-- SECTION:NOTES:END -->
