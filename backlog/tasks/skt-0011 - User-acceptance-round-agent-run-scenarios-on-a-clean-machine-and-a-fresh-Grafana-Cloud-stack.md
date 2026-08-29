---
id: SKT-0011
title: >-
  User-acceptance round: agent-run scenarios on a clean machine and a fresh
  Grafana Cloud stack
status: To Do
assignee: []
created_date: '2026-08-27 07:06'
updated_date: '2026-08-29 17:33'
labels: []
dependencies: []
priority: high
type: feature
ordinal: 41000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Everything the project claims it does gets exercised by an agent, from scratch, on hardware and a stack with no prior synthkit state. The project is getting outside attention, and every existing verification runs on a machine that has been carrying synthkit state for months — so the one thing never tested is the path a new user actually takes.

Rob provisions the machine and the stack separately; this task is the scenario catalogue an agent runs against them, and the findings register it produces.

The distinguishing constraint is that a fresh stack has NO pre-existing dashboards, datasources, recording rules, or folder structure. Anything synthkit needs and does not create is a defect the current lab environment hides, and it is the defect class most likely to reach a new user first.

Scenario areas to cover, each ending in an observable assertion against the live stack rather than a local exit code:

- First deployment from a clean clone through the shipped `initial-setup` skill and `docs/getting-started.md`, with no undocumented step required.
- Credential configuration for every declared lane, including the optional ones: Faro/RUM, Pyroscope, Synthetic Monitoring, Fleet Management, sigil.
- Each shipped blueprint deployed and its declared signals confirmed arriving, so `verify-deployment` is checked against something it has never seen.
- The control plane and admin UI: blueprint selection, live mutation, restart, custom blueprint upload, status and readiness endpoints.
- Failure modes and scenarios driven through the control plane and observed landing in the data.
- Upgrade and rollback through the published image digest.
- Generated dashboards pushed to the fresh stack and checked for panels that render empty against the data synthkit actually produces.

Every scenario records a verdict and, where it fails, what a new user would have seen. Findings become tracked work; the catalogue itself is durable and re-runnable for the next release rather than a one-off script.

Do not name the stack, account or tenant in tracker text, code, docs or commit messages: this repository is public and carries a forbidden-words guard.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 A durable scenario catalogue exists that an agent can execute end to end without a human filling in gaps
- [ ] #2 Each scenario states its precondition, the action, and an observable assertion against the live stack rather than a local exit code
- [ ] #3 Every shipped blueprint is deployed and its declared signals confirmed arriving
- [ ] #4 Every optional lane is exercised with real credentials, including the lanes that silently disable themselves when unconfigured
- [ ] #5 Anything synthkit needs on a stack but does not create is identified, since the existing lab environment hides that class of defect
- [ ] #6 Generated dashboards are pushed to the fresh stack and panels that render empty against real synthkit output are recorded
- [ ] #7 Every scenario carries a pass or fail verdict, and failures record what a new user would have seen
- [ ] #8 Findings become tracked work rather than living only in the run output
- [ ] #9 The catalogue is re-runnable for a future release without being rewritten
- [ ] #10 No stack, account or tenant identifier appears in any committed text
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 make gate (build vet test race rw-proto-check spdx-check forbidden-words)
- [ ] #2 make blueprint-schema (only if a blueprint field or construct/workload config struct changed)
- [ ] #3 DRY_RUN=true go run ./cmd/synthkit -once -dump — inventory diffed against signals/
<!-- DOD:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Scenario catalogue written 2026-08-27: e2e/acceptance/SCENARIOS.md. Eight groups, A through H, every scenario shaped as precondition -> action -> assertion -> verdict, and every assertion an observable fact about the live stack rather than a local exit code — a clean docker compose up proves the process started, not that a single series arrived.

Groups: A first deployment from nothing (the agent follows the shipped instructions LITERALLY and records every improvised step, because an undocumented step an experienced operator supplies from memory is the defect being hunted); B credential and lane coverage, all 10 lanes including the ones that silently disable themselves when unconfigured; C all 26 shipped blueprints, individually and together; D control plane and admin UI across its 33 routes; E failure modes and scenarios, asserted in the DATA not just in control-plane state; F upgrade, rollback and digest reproducibility; G generated dashboards against real synthetic output; H documentation truth.

The group most likely to pay for the whole round is G5: enumerate anything synthkit needs on a stack but does not create — recording rules, folders, datasources. A fresh stack has none of the structure the established lab environment has accumulated, and that is the defect class the current environment structurally cannot reveal.

Deliberately out of scope and stated in the file so a runner does not report them: admin-UI accessibility (descoped by explicit direction), anything requiring a write to the Kubernetes clusters (that is SKT-0012 and needs the cluster owner decision), and fixing what the round finds — a round that repairs as it goes cannot tell you how bad the fresh-start experience actually is.

BLOCKED ON: Rob provisioning the clean machine and the fresh Grafana Cloud stack. The catalogue is complete and runnable without further authoring.

Note for whoever runs it: AWS SSO was expired on the authoring machine on 2026-08-27, so anything in the catalogue touching the AWS API needs a live session first.

STACK TOPOLOGY, confirmed by Rob 2026-08-27 — orientation for whoever runs this round.

Two existing stacks with distinct roles. The lab/staff stack holds the REAL data: source of truth for live captures, signals/ provenance and the reality-corpus read-back. A second, dedicated synthkit stack is the EMISSION-TEST target: where you check what synthkit output looks like once it lands in Grafana Cloud, usually deployed there from the dedicated deployment host, which already holds the credentials (a local deploy works too).

Why this round still needs a THIRD, FRESH stack rather than reusing the emission-test one: the emission-test stack has accumulated dashboards, folders and datasource structure over months. That is exactly what hides the defect class group G5 exists to find — anything synthkit needs on a stack but does not itself create. A stack that already has the structure cannot reveal a missing-structure defect.

So the division for this round: the fresh stack is the one-off cold-start test, and the existing emission-test stack remains the everyday check for whether emission looks right. Do not substitute one for the other.

Credentials for the emission path already exist on that deployment host if a comparison against the established stack is useful mid-round.

2026-08-29 — UNBLOCKED on the stack half. A dedicated bare Grafana Cloud stack has been vended for this round. Its slug, region, and the Secrets Manager paths holding its telemetry and admin credentials are recorded on **EKS-0069 in the private infrastructure tracker**, deliberately not here: this repository is public and the standing rule is that no stack, account or tenant identifier appears in committed text.

**It is genuinely bare, which took an extra step.** Both catalogs the vending machine ships set `baselineDashboards.enabled: true`, which creates three starter folders and dashboards. That is exactly the pre-existing structure group G5 must not have, so the request uses the minimal catalog *and* patches that field to false. A stack that arrives with folders already in it cannot reveal a folder synthkit fails to create.

Consequence a runner should expect and not report as a defect: the vending claim shows `Ready=False`. The only unready composed resource is the per-stack ProviderConfig, which nothing references because there are no dashboards or folders to manage. The stack, its credentials and its ingest endpoints are live and usable.

**The stack is temporary.** It is destroyed once this round completes, through a three-stage decommission recorded on EKS-0069. Do not treat it as a durable environment or build anything on it that needs to survive.

**Still needed before the round runs: the clean machine.** Group A requires a host with no synthkit history, since its whole purpose is that the agent follows the shipped instructions literally and records every improvised step. The remaining decision is whether that is a throwaway VM or a clean container.

Reminder for whoever runs this: the credentials cover the required lanes, but group B also exercises the optional ones that silently disable themselves when unconfigured. Check which of those the vended stack actually supports before recording a lane as failed — an unsupported product on the stack is a different verdict from a synthkit defect.
<!-- SECTION:NOTES:END -->
