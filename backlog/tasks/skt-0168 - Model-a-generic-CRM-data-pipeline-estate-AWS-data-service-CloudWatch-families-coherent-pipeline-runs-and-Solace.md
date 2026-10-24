---
id: SKT-0168
title: >-
  Model a generic CRM data-pipeline estate: AWS data-service CloudWatch
  families, coherent pipeline runs and Solace
status: Parked
assignee:
  - '@loop54'
created_date: '2026-10-22T05:34:00Z'
updated_date: '2026-10-24T02:19:00Z'
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

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Loop54: independent confidential Argo manifest and demo object preparation; root live activation only after exact reviewed artifacts, compatible image, destination and credential preconditions. Layer D waits captured public constructs.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
2026-10-23 re-scope: the demo now covers one flow, MDM data change requests (DCRs, the standard term in public CRM and MDM vendor docs) handled by a serverless intake service on Lambda, SQS and DynamoDB, with a broker response leg. Needed first: SKT-0168.02's Lambda, SQS and DynamoDB families. SKT-0168.04 (Solace) is a stretch. SKT-0168.03 and SKT-0168.05 are parked until after the demo. DCR state metrics keep demo-chosen names and live in the private external blueprint, never here.

Loop53 delivered sourced Solace, generic incident endpoint, shared pipeline mechanic slice, MWAA outcomes and sparse queue carry. The external bounded offline packet validates at unchanged rates with real nominal-cadence queue emission and metadata/onset proof; it is not full demo/backend/deployment acceptance. AWS service-family capture and dependent adapters remain owner-blocked on the exact private gate-route decision; no live apply or activation occurred.

Dependency hold at loop53 completion: bounded offline composition is proven at unchanged nominal rates, but the six-family live capture and dependent service/log adapters remain owner-blocked on the private gate route. Full demo/backend/deployment acceptance is incomplete. Resume the capture only after the named gate-route decision; do not treat the authored/offline packet as live readiness.

Loop54 private manual Argo bootstrap source and illustrative object packet reviewed and committed, but no live emitter or Grafana object activation yet. Existing-auth ConfigMap projection avoids a runtime Git credential; fresh ephemeral state leaves historical storage untouched and no-prune/no-cascade holds apply. Backend/object acceptance awaits an authorized instance credential; service constructs and private AWS layer remain pending sourced implementation. Public intensity/error-chain repairs landed with exact gates; no full-demo readiness claim.

Loop54 closeout: no authorized unblocked implementation remains. Bounded public telemetry work and private authored source progressed, but full child/source coverage and external demo acceptance remain incomplete. Private activation and actual dashboard/delivery depend on explicit owner decisions; source/HA parks and charter exclusions remain. No live deployment/backend or complete end-to-end success claimed.

Closeout audit finding retained: prescribed remote-change audit exited1 because per-run push log was missing, so automated attribution is NOT clean. Accepted source/gate/CI and normal-push evidence retained; separate foreign bot/default-branch moves disclosed. Owner-scoped toolchain receipt repair remains outside this run authority; no retrospective fake log or audit exception. No universal no-ungranted-change claim.
<!-- SECTION:NOTES:END -->
