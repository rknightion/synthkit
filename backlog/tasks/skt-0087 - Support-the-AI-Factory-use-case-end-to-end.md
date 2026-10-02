---
id: SKT-0087
title: Support the AI Factory use case end to end
status: In Progress
assignee:
  - '@loop47-root'
created_date: '2026-10-02 14:15'
updated_date: '2026-10-02 15:33'
labels:
  - ai-factory
dependencies: []
priority: high
type: feature
ordinal: 183000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
An AI Factory is an on-premises GPU estate built from NVIDIA reference components: PCIe GPU servers, rack-scale NVL72 systems, Spectrum-X Ethernet fabrics, an NVLink management plane (NMX), Base Command Manager and Mission Control, a Kubernetes scheduler such as Run:ai, a high-performance storage platform such as VAST, metered power and liquid cooling. Observability designs for these estates collect GPU, host, BMC hardware, fabric, NVLink, storage, scheduler, platform-automation, power and cooling telemetry through local Alloy collectors and a clustered Alloy gateway pool, all converging on Grafana Cloud over OTLP at one data point per minute. synthkit models none of these sources today, so the estate cannot be demonstrated, dashboards cannot be built ahead of hardware, and correlation journeys (failed job, slow job, GPU fault, degraded NVLink domain, hardware replacement, observability failure) cannot be rehearsed. This parent tracks the full set; each child is independently deliverable. Customer-specific identifiers never enter the catalog or the reference blueprint.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 Every child task is Done or explicitly rejected with a recorded reason
- [ ] #2 docs/ carries an AI Factory coverage matrix mapping each telemetry category (GPU, host and driver logs, BMC hardware, syslog, fabric, NVLink, cluster manager, platform control plane and recovery, scheduler, storage, power and cooling, gateway health, automation traces) to the construct that emits it and its signals/ file
- [ ] #3 The reference ai-factory blueprint loads under BLUEPRINT_NAMES=* and its just dump inventory matches the signals/ files it cites
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [ ] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [ ] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
loop47: admitted
<!-- SECTION:NOTES:END -->
