---
id: SKT-0089
title: 'Model Synthetic Monitoring network check types (ping, DNS, TCP, traceroute)'
status: To Do
assignee: []
created_date: '2026-10-05 11:57'
labels:
  - network
  - synthetic-monitoring
dependencies: []
references:
  - signals/sm.md
  - 'https://github.com/grafana/synthetic-monitoring-agent'
priority: medium
type: feature
ordinal: 211000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
The sm construct emits only HTTP checks (check_name=http). Grafana Synthetic Monitoring's network check types are the core of a network-path story: ping emits probe_icmp_duration_seconds{phase=resolve|setup|rtt}, probe_icmp_packets_sent/received_count, rtt min/max/stddev; DNS emits probe_dns_duration_seconds{phase=resolve|connect|request} and answer/authority/additional rrs; TCP emits probe_tls_version_info, probe_ssl_earliest_cert_expiry and friends; traceroute emits probe_traceroute_total_hops, _route_hash and _packet_loss_percent (a 0-1 fraction despite the name) plus one Loki logfmt line per hop (TTL, Hosts, ElapsedTime, LossPercent, Sent, TracerouteID). Source every name from grafana/synthetic-monitoring-agent internal/prober/* and the vendored blackbox_exporter probers, grow signals/sm.md with provenance, and register the matching check type in sm-provision so the SM app's per-type dashboards light up.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [ ] #1 signals/sm.md documents each new check type's metrics, labels and logs with source provenance
- [ ] #2 A blueprint can declare ping, dns, tcp and traceroute checks and they emit the sourced series
- [ ] #3 sm-provision registers those checks with the right type so the SM app renders them
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [ ] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [ ] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [ ] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->
