---
id: SKT-0084
title: Model per-node telemetry adoption in the app workload
status: To Do
assignee: []
created_date: '2026-09-27 10:19'
labels: []
dependencies: []
ordinal: 180000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
GitHub issue #172: real estates adopt telemetry unevenly; some services emit metrics and logs but no traces, some are not instrumented at all. The contributor proposed an app-wide traces: false (fork commit 85ce92943c410986b9fc1bf57311460196209ec1). Rob chose on 2026-09-27 to redesign it as a per-node signals block covering traces, logs and metrics. The fork version gated only the trace projection, so derived spanmetrics and service-graph series kept flowing for untraced services and Signals() still declared traces; both are impossible in a real stack. The same fork commits also dropped the default service.version, added service_namespace to SDK profiles and added cAdvisor enrichment labels; those are out of scope unless captured evidence supports them.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 An app services[] entry accepts a signals block with traces, logs and metrics switches; omitting it or any key leaves today's output unchanged (just dump inventory identical for every shipped blueprint)
- [ ] #2 A node with traces off emits no spans and contributes no derived spanmetrics or service-graph series, and trace propagation through it follows the semantics frozen in the loop's design packet
- [ ] #3 A node with logs off emits no log streams and a node with metrics off emits no app-level metrics; cluster and infrastructure series for its pods are unaffected
- [ ] #4 Signals() declares traces, logs and metrics only when at least one node emits them, and blueprint load rejects contradictory declarations the design packet names
- [ ] #5 BLUEPRINT-SCHEMA.md, fielddocs.json, signals/traces.md and signals/apm.md document the switch; no new metric, label or attribute name is introduced
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [ ] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [ ] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->
