---
id: SKT-0168
title: >-
  Model a generic CRM data-pipeline estate: AWS data-service CloudWatch
  families, coherent pipeline runs and Solace
status: To Do
assignee: []
created_date: '2026-10-22T05:34:00Z'
labels:
  - feature
  - cw
  - data-pipelines
dependencies: []
priority: medium
ordinal: 312000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Owner-approved 2026-10-22. Backs a one-off demo: a CRM -> MDM -> integration -> warehouse ETL -> marketing CDP estate (SaaS CRM, MDM hub, integration platform, Solace event broker, AWS Glue/MWAA/Lambda/SQS/DynamoDB/Redshift). Split decided by the owner: synthkit gets only reusable, sourced pieces (new CloudWatch families, a shared pipeline-run mechanic, Solace). The demo blueprint (crm-data-pipelines), its SaaS custom-poller metrics and its dashboards live in the private deployment repo and load through blueprintSources as an external blueprint; demo-chosen SaaS metric names never enter signals/ or blueprints/. No customer identifier, programme name or internal system name enters this repo (child .01 guards that). Rates in any shipped default cite a real-world basis per AGENTS.md.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 Children .01-.04 Done
- [ ] #2 The demo blueprint loads externally against a build containing these children with no synthkit-side demo names
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [ ] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [ ] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->
