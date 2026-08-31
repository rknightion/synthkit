---
id: SKT-0046
title: 'Model the OTLP-native label set, not just the Alloy-converted one'
status: To Do
assignee: []
created_date: '2026-08-31 10:40'
labels: []
dependencies: []
ordinal: 137000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Raised from synthkit-terraform RKSY-0019, which ran the same application metrics down BOTH ingest lanes into one tenant and diffed them.

MEASURED on captures/rksy-20260831T102646Z.capture.json in synthkit-terraform, same metric, same estate, same moment, separated by rksy_ingest:

  shared by both lanes
    __name__ cluster http_request_method http_response_status_code http_route job
    k8s_cluster_name network_protocol_version server_address server_port url_scheme

  Alloy-converted (promrw) ONLY
    cloud, rksy_ingest        - the labs own destination extraLabels, not the pipeline

  OTLP-native ONLY
    k8s_deployment_name, k8s_namespace_name, k8s_pod_name, service_name, service_version

THE NAMES ARE IDENTICAL on both lanes - http_server_request_duration_seconds_count either way, same unit suffix, same _count component. Useful negative result: no per-lane naming model is needed.

THE DIFFERENCE IS RESOURCE-ATTRIBUTE PROMOTION. The Grafana Cloud OTLP gateway promotes k8s.* and service.* resource attributes onto EVERY series. Alloy OTLP-to-Prometheus conversion leaves them on target_info and requires a join to recover them.

WHY IT MATTERS. Any query, dashboard, recording rule or entity-graph edge keyed on k8s_pod_name works directly against an OTel-native estate and needs a target_info join against an Alloy-converted one. synthkit models only the second shape, so a blueprint declaring OTLP-native ingest emits application metrics carrying fewer labels than a real OTLP estate would.

Scope to decide rather than assume:
  - the promoted attribute list IS enumerable from the capture. Do not guess it from the OTel
    specification; the gateway applies its own policy.
  - whether this is a per-workload emission change or a lane-level transform in the OTLP sink
  - whether target_info should still be emitted on the OTLP lane when the attributes are also
    promoted. The capture shows target_info present on BOTH lanes, so probably yes.
  - interaction with SKT-0008.02, which already covers emitting target_info for the app workload

Relates to SKT-0007, whose measured finding is that 44 of 45 catalog packages emit promrw and exactly one has an OTLP-native metrics lane.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 The resource attributes the OTLP gateway promotes are enumerated from the capture, not from the specification
- [ ] #2 A blueprint declaring OTLP-native ingest emits application metrics carrying the promoted label set
- [ ] #3 The Alloy-converted lane keeps its current shape, so both realities remain modellable
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [ ] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [ ] #3 just dump — inventory diffed against signals/
- [ ] #4 just check
<!-- DOD:END -->
