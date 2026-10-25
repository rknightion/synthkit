---
id: SKT-0184
title: Bind rds, docdb and agentcore constructs to causal resources
status: To Do
assignee: []
created_date: '2026-10-25T02:46:00Z'
labels: []
dependencies:
  - SKT-0168.09.15
priority: medium
ordinal: 376000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
The causal model binds lambda, sqs, dynamodb and solace. A causal database or AgentCore slowdown does not move the rds/docdb CloudWatch or agentcore series, so migrated data-store and gateway scenarios keep legacy axis effects alongside causal ones and the two are not correlated by cause. Add resource bindings for these constructs following the lambda/sqs pattern. Found by the SKT-0182.01 sweep (doc-0005, gap G4).
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 A bound rds or docdb instance's latency and connection series follow a dependency resource's state
- [ ] #2 A bound agentcore runtime's throttle series follow a concurrency resource
- [ ] #3 Unbound output unchanged
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [ ] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [ ] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->
