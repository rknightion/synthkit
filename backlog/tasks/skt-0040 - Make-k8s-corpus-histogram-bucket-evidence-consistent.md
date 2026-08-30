---
id: SKT-0040
title: Make k8s corpus histogram bucket evidence consistent
status: To Do
assignee: []
created_date: '2026-08-29 23:23'
labels:
  - needs-triage
dependencies: []
references:
  - reality-corpus/k8s/k3d-lab.json
  - reality-corpus/k8s-addons/k3d-lab.json
priority: high
type: bug
ordinal: 131000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
The k8s corpus producer records classic-histogram identity while eliding every le value, but the k8s-addons corpus retains real le values for equivalent families. Resolve the producer policy from observed egress so bucket evidence is represented consistently and downstream emitter work never chooses bounds.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 The k8s corpus producer records observed le values and bucket bounds for classic histograms, or a documented evidence rule explains and consistently represents why a value cannot be retained
- [ ] #2 Instrument type and classic-histogram classification come from observed series structure, never a metric-name suffix
- [ ] #3 A regression fixture covers a real finite bucket and +Inf without exposing deployment identity
- [ ] #4 The refreshed corpus and comparator report make retained versus absent bucket evidence explicit
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [ ] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [ ] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->
