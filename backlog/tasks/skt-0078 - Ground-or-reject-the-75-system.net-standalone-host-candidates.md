---
id: SKT-0078
title: Ground or reject the 75 system.net standalone-host candidates
status: Done
assignee:
  - '@codex'
created_date: '2026-09-11 19:04'
updated_date: '2026-09-11 20:23'
labels: []
dependencies: []
ordinal: 174000
---

## Description

<!-- SECTION:DESCRIPTION:BEGIN -->
Network candidates require a source-backed per-family decision before implementation.
<!-- SECTION:DESCRIPTION:END -->

## Acceptance Criteria
<!-- AC:BEGIN -->
- [x] #1 A complete 75-family proposal names an admissible existing basis per family, or explicitly rejects unsupported mappings and names the exact missing material.
- [x] #2 The investigation emits no network families and changes no production source.
<!-- AC:END -->

## Definition of Done
<!-- DOD:BEGIN -->
- [x] #1 just check (fmt-check, lint, gen-check, env-check, docs-check, test, race, hygiene, ui-check, compose-check, helm-test, lab-check, signal-fidelity)
- [x] #2 just gen (only if a blueprint field, construct/workload config struct, or a skill under plugins/synthkit/skills/ changed)
- [x] #3 just dump — inventory diffed against signals/
<!-- DOD:END -->

## Implementation Plan

<!-- SECTION:PLAN:BEGIN -->
Execute the frozen campaign lane contract; root integrates and measures before any claim adjustment, then validates the exact tree and reconciles acceptance.
<!-- SECTION:PLAN:END -->

## Implementation Notes

<!-- SECTION:NOTES:BEGIN -->
Root integration: storage 296d83a91c267c9004ffa79e4cd41825a0b0d4e4 CI 34639635537 success; swap c6e9f9ff9926f7171b76952395dd5aacb2909f71 CI 34641719112 success; profile c500ec61618b13d83218d9b1a4292225e46aeac1 CI34642964976 success. Ordered gen-check, SPDX 736 and full just check passed. just gen regenerated the profile field and earlier storage doc index. Dump comparison proves 36 added host pairs; sample-time Kubernetes termination and topology-conflict changes are separately identified. No cloud, capture, cluster, lab run, second nightly dispatch, release retry or PR mutation occurred.
Mapping-only task:75 expected,75 unique,missing[],extra[]. No system.net emission added. Conditional generation is not applicable to the mapping itself. Full proposal follows:

# L4 mapping: Datadog `system.net` candidates

## Verdict

**Confirmed unresolved: `internal/nodeexp` network physics is not an admissible grounding basis for these 75 host-only Datadog families.** SKT-0073 therefore remains valid for this lane. No emission or tracked-file change is proposed.

The distinction from L3 is material. The nodeexp disk/filesystem mechanics are the existing device/filesystem model explicitly named by the wave brief as grounded against real captures. The network code does not have equivalent source-grounded Datadog arithmetic: `nodeRxBase` uses fixed 5 MiB + factor-scaled 80 MiB baselines for every non-loopback interface; transmit is a random fraction of receive; packet counts divide by a fixed 1400; error/drop/FIFO/compression values are constant zero; multicast is a host hash; MTU and transmit queue length are fixed 9001 and 1000; netstat rates are hardcoded (2000 TCP segments/s, 3,000,000 IP octets/s, 200 UDP datagrams/s, 5 ICMP messages/s, zero otherwise); sockstat values are hand-shaped constants/hash expressions; conntrack entries are `200 + hostHash*2000` and limit is fixed 131072. The source comments identify these as ports from the Kubernetes node exporter or “plausible” values, not Datadog Agent system-check mechanics.

The existing native host artifact proves family names, value type, placement and attribute envelopes only. Its limitations explicitly elide values and state that no absent family is classified unsupported. It cannot supply the missing rates, optional-publication behavior, or zero/nonzero observations.

## Per-family result

All 75 entries have the same verdict envelope: `category=observed_unimplemented_fixture_mechanics_candidate`; `instrument=gauge`; `unit=""`; `aggregation_temporality=""`; `is_monotonic=null`; source scope `github.com/open-telemetry/opentelemetry-collector-contrib/receiver/datadogreceiver/internal/translator`, version `v1.19.2`. The listed `envelope_count` and attribute placement are retained below from the immutable verdict. No nodeexp mechanic is assigned because assigning one would imply unsupported grounding.

### Interface I/O and interface state (13)

Missing source basis: `pkg/collector/corechecks/net/network/network.go:submitInterfaceMetrics` and `pkg/collector/corechecks/net/networkv2/network_linux.go:submitInterfaceMetrics` / `submitInterfaceSysMetrics`; `/proc/net/dev` plus `/sys/class/net/<iface>` and queue directories.

| Family | Envelopes | Datapoint attributes | Resource attributes |
|---|---:|---|---|
| `system.net.bytes_rcvd` | 2 | device_name, mtu, speed / device_name, mtu | device, host.name, source / device, host.name, source |
| `system.net.bytes_sent` | 2 | device_name, mtu, speed / device_name, mtu | device, host.name, source / device, host.name, source |
| `system.net.iface.mtu` | 1 | iface | host.name, source |
| `system.net.iface.num_rx_queues` | 1 | iface | host.name, source |
| `system.net.iface.num_tx_queues` | 1 | iface | host.name, source |
| `system.net.iface.tx_queue_len` | 1 | iface | host.name, source |
| `system.net.iface.up` | 1 | iface | host.name, source |
| `system.net.packets_in.count` | 2 | device_name, mtu, speed / device_name, mtu | device, host.name, source / device, host.name, source |
| `system.net.packets_in.drop` | 2 | device_name, mtu, speed / device_name, mtu | device, host.name, source / device, host.name, source |
| `system.net.packets_in.error` | 2 | device_name, mtu, speed / device_name, mtu | device, host.name, source / device, host.name, source |
| `system.net.packets_out.count` | 2 | device_name, mtu, speed / device_name, mtu | device, host.name, source / device, host.name, source |
| `system.net.packets_out.drop` | 2 | device_name, mtu, speed / device_name, mtu | device, host.name, source / device, host.name, source |
| `system.net.packets_out.error` | 2 | device_name, mtu, speed / device_name, mtu | device, host.name, source / device, host.name, source |

### Conntrack (5)

Missing source basis: `pkg/collector/corechecks/net/networkv2/network_linux.go:collectConntrackMetrics`; `/proc/sys/net/netfilter/nf_conntrack_*` for gauges, with optional `conntrack -S` for other conntrack counters.

| Family | Envelopes | Datapoint attributes | Resource attributes |
|---|---:|---|---|
| `system.net.conntrack.count` | 1 | none | host.name, source |
| `system.net.conntrack.expect_max` | 1 | none | host.name, source |
| `system.net.conntrack.max` | 1 | none | host.name, source |
| `system.net.conntrack.tcp_max_retrans` | 1 | none | host.name, source |
| `system.net.conntrack.tcp_timeout_max_retrans` | 1 | none | host.name, source |

### IP protocol (22)

Missing source basis: `pkg/collector/corechecks/net/network/network.go` protocol mapping plus `network.go:submitProtocolMetrics`; Linux `/proc/net/snmp` protocol counters.

| Family | Envelopes | Datapoint attributes | Resource attributes |
|---|---:|---|---|
| `system.net.ip.forwarded_datagrams` | 1 | none | host.name, source |
| `system.net.ip.fragmentation_creates` | 1 | none | host.name, source |
| `system.net.ip.fragmentation_fails` | 1 | none | host.name, source |
| `system.net.ip.fragmentation_oks` | 1 | none | host.name, source |
| `system.net.ip.in_addr_errors` | 1 | none | host.name, source |
| `system.net.ip.in_csum_errors` | 1 | none | host.name, source |
| `system.net.ip.in_delivers` | 1 | none | host.name, source |
| `system.net.ip.in_discards` | 1 | none | host.name, source |
| `system.net.ip.in_header_errors` | 1 | none | host.name, source |
| `system.net.ip.in_no_routes` | 1 | none | host.name, source |
| `system.net.ip.in_receives` | 1 | none | host.name, source |
| `system.net.ip.in_truncated_pkts` | 1 | none | host.name, source |
| `system.net.ip.in_unknown_protos` | 1 | none | host.name, source |
| `system.net.ip.out_discards` | 1 | none | host.name, source |
| `system.net.ip.out_no_routes` | 1 | none | host.name, source |
| `system.net.ip.out_requests` | 1 | none | host.name, source |
| `system.net.ip.reassembly_fails` | 1 | none | host.name, source |
| `system.net.ip.reassembly_oks` | 1 | none | host.name, source |
| `system.net.ip.reassembly_overlaps` | 1 | none | host.name, source |
| `system.net.ip.reassembly_requests` | 1 | none | host.name, source |
| `system.net.ip.reassembly_timeouts` | 1 | none | host.name, source |
| `system.net.ip.reverse_path_filter` | 1 | none | host.name, source |

### TCP protocol (28)

Missing source basis: `pkg/collector/corechecks/net/network/network.go` protocol mapping plus `network.go:submitProtocolMetrics`; Linux `/proc/net/snmp` and `/proc/net/netstat` counters.

| Family | Envelopes | Datapoint attributes | Resource attributes |
|---|---:|---|---|
| `system.net.tcp.abort_on_timeout` | 1 | none | host.name, source |
| `system.net.tcp.active_opens` | 1 | none | host.name, source |
| `system.net.tcp.attempt_fails` | 1 | none | host.name, source |
| `system.net.tcp.backlog_drops` | 1 | none | host.name, source |
| `system.net.tcp.current_established` | 1 | none | host.name, source |
| `system.net.tcp.established_resets` | 1 | none | host.name, source |
| `system.net.tcp.failed_retransmits` | 1 | none | host.name, source |
| `system.net.tcp.from_zero_window` | 1 | none | host.name, source |
| `system.net.tcp.in_csum_errors` | 1 | none | host.name, source |
| `system.net.tcp.in_errors` | 1 | none | host.name, source |
| `system.net.tcp.in_segs` | 1 | none | host.name, source |
| `system.net.tcp.listen_drops` | 1 | none | host.name, source |
| `system.net.tcp.listen_overflows` | 1 | none | host.name, source |
| `system.net.tcp.out_resets` | 1 | none | host.name, source |
| `system.net.tcp.out_segs` | 1 | none | host.name, source |
| `system.net.tcp.passive_opens` | 1 | none | host.name, source |
| `system.net.tcp.paws_connection_drops` | 1 | none | host.name, source |
| `system.net.tcp.paws_established_drops` | 1 | none | host.name, source |
| `system.net.tcp.prune_called` | 1 | none | host.name, source |
| `system.net.tcp.prune_ofo_called` | 1 | none | host.name, source |
| `system.net.tcp.prune_rcv_drops` | 1 | none | host.name, source |
| `system.net.tcp.retrans_segs` | 1 | none | host.name, source |
| `system.net.tcp.syn_cookies_failed` | 1 | none | host.name, source |
| `system.net.tcp.syn_cookies_recv` | 1 | none | host.name, source |
| `system.net.tcp.syn_cookies_sent` | 1 | none | host.name, source |
| `system.net.tcp.syn_retrans` | 1 | none | host.name, source |
| `system.net.tcp.to_zero_window` | 1 | none | host.name, source |
| `system.net.tcp.tw_reused` | 1 | none | host.name, source |

### UDP protocol (7)

Missing source basis: `pkg/collector/corechecks/net/network/network.go` protocol mapping plus `network.go:submitProtocolMetrics`; Linux `/proc/net/snmp` counters.

| Family | Envelopes | Datapoint attributes | Resource attributes |
|---|---:|---|---|
| `system.net.udp.in_csum_errors` | 1 | none | host.name, source |
| `system.net.udp.in_datagrams` | 1 | none | host.name, source |
| `system.net.udp.in_errors` | 1 | none | host.name, source |
| `system.net.udp.no_ports` | 1 | none | host.name, source |
| `system.net.udp.out_datagrams` | 1 | none | host.name, source |
| `system.net.udp.rcv_buf_errors` | 1 | none | host.name, source |
| `system.net.udp.snd_buf_errors` | 1 | none | host.name, source |

## Material required before a future implementation wave

1. **Pinned Agent descriptors and mechanics.** Retain the Datadog Agent **7.83.0** source used by the captured host and review the exact Linux implementations: `pkg/collector/corechecks/net/network/network.go`, `pkg/collector/corechecks/net/networkv2/network.go`, `pkg/collector/corechecks/net/networkv2/network_linux.go`, and `pkg/collector/corechecks/net/networkv2/const_linux.go`. The relevant descriptors are `submitInterfaceMetrics` for bytes/packets, `submitInterfaceSysMetrics` for MTU/up/queue fields, `submitProtocolMetrics` and its Linux protocol mapping for IP/TCP/UDP, `submitConnectionStateMetrics` where TCP state counts are sourced, and `collectConntrackMetrics` for conntrack. Preserve the exact source revision, configuration flags (`collect_rate_metrics`, `collect_count_metrics`, `collect_connection_queues`, `conntrack_path`, whitelist/blacklist), and procfs/sysfs inputs.

2. **A value-bearing native host capture.** Re-run the standalone host path with Agent **7.83.0** and Alloy Datadog receiver **v1.19.2**, retaining datapoint values, timestamps across at least two collection intervals, device attributes (`device_name`, `mtu`, `speed`), and resource attributes (`device`, `host.name`, `source`). The current 2026-09-09 capture is insufficient because values are elided. The host must expose the same relevant Linux sources: `/proc/net/dev`, `/proc/net/snmp`, `/proc/net/netstat`, `/sys/class/net/<iface>/{mtu,carrier,tx_queue_len}`, interface `queues/{rx-*,tx-*}`, and conntrack sysfs or the configured conntrack binary.

3. **Optional-publication evidence.** Capture both default and enabled/available conditions for conntrack and queue/state collection, including a host with conntrack unavailable and one with it available. This establishes whether a family is continuously emitted at zero, omitted, or emitted only when the capability/configuration exists. Do not infer this from the elided envelope.

Until those materials exist, no per-family rate, counter accumulation rule, zero value, or unconditional emission claim is admissible.

## Evidence paths read

- `codex/scratch/wave-2026-09-22/root/l4-brief.md`
- `reality-corpus/verdicts/datadog-receiver-host-classification.json`
- `e2e/acceptance/datadog-host-native-envelope-2026-09-09.json`
- `backlog/tasks/skt-0073 - Classify-every-standalone-Datadog-host-capture-family-before-further-implementation.md`
- `internal/nodeexp/physics.go`
- `internal/nodeexp/linux.go`
- `internal/nodeexp/nodeexp.go`
- `internal/nodeexp/profiles.go`
- `internal/nodeexp/physics_test.go`
- `internal/nodeexp/linux_test.go`
- `internal/construct/k8scluster/nodeexporter.go`
- `signals/host.md`
- `reality-corpus/verdicts/README.md`

External source consulted for descriptor identification: Datadog Agent tag `7.83.0` in the public `DataDog/datadog-agent` repository. No repository or external state was modified.
<!-- SECTION:NOTES:END -->

## Final Summary

<!-- SECTION:FINAL_SUMMARY:BEGIN -->
The complete75-family mapping rejects nodeexp network rates as sufficient Datadog grounding. SKT-0073 remains unresolved; retained pinned descriptors, value-bearing observations and optional-publication evidence define a future implementation boundary. Done2/2 for the proposal, not for emission.
<!-- SECTION:FINAL_SUMMARY:END -->
