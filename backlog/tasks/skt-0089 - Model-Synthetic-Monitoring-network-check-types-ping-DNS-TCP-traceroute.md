---
id: SKT-0089
title: 'Model Synthetic Monitoring network check types (ping, DNS, TCP, traceroute)'
status: In Progress
assignee:
  - '@codex'
created_date: '2026-10-05 11:57'
updated_date: '2026-10-14 20:56'
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

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Implement pinned agent v0.66.1 ping/DNS/TCP/traceroute contracts, preserving HTTP output and legacy snapshot hashes. Add typed YAML/config and optional snapshot type/settings with HTTP canonicalized to legacy omitted form; hash binds every non-HTTP setting. Type-aware provisioner reconciliation/ownership fingerprints reject conflicting or unknown settings. Real fake API and composed emission proofs, sourced inventory comparison and mutation controls. Isolated implementation based on accepted per-probe location candidate, root integration and security review/gates.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Pinned source v0.66.1 implemented for ping, DNS A/UDP/53, TCP/TLS and default traceroute. Existing HTTP snapshot hash regression passes unchanged. Actual built provisioner/fake API and registered just dump exercise all four types; 32/32 metric names and label-key sets match sourced catalogue, with missing-phase negative control. Root consistency test failed for constant explicit-IP hash and moving same-certificate expiry; fixed per-target hashes and bounded 90-day validity/60-day renewal with old TLS gauges retired. CodeRabbit completed 6 minor findings: validation wording clarified, timeout contract clarified; duplicate bucket comments already covered by the real updated catalogue (review slice omitted docs). DNS query-hash suggestion rejected against pinned upstream adapter/prober/utils: hash belongs to resolver. Negative-control mutation confirms resolver-hash regression catches the suggested wrong behavior. Final composed gate/CI pending; no live SM API calls.

Final review also reproduced nonzero elapsed time for all-lost traceroute hops. Pinned MTR Avg returns zero without replies; corrected zero duration and integer-millisecond projection, with fail-first regression. Final local composed gate did not pass because unchanged control-dashboard race proof timed out at host load255.01. All ordinary tests, focused races, registered dump and final static checks passed. Awaiting owner CI-first decision; local failure retained.
<!-- SECTION:NOTES:END -->
