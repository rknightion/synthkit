# Solace event broker (→ Mimir) — ScopeSubstrate

Event-broker telemetry via the community `solace_prometheus_exporter`
(`github.com/solacecommunity/solace-prometheus-exporter`, default listen port `9628`, `README.md:20`), which
polls the broker's SEMP management API and exposes `solace_*` metrics. Synthkit models one
scrape per declared broker; the construct is `internal/construct/solace`, declared under a
blueprint's `integrations:` map. Substrate-scoped: a series is identified by `(job, instance)`
plus its own `vpn_name` / `queue_name`; no `blueprint` label is ever stamped. Global rules:
[`00-canon.md`](00-canon.md) — scoping `[slug: scoping]`, cardinality `[slug: cardinality]`.

*Provenance: exporter source read at release tag **`v1.21.0`** (commit
`710f4968c5914c1396a5ac9eb07c88465cce9916`), files cited below as `file:line`. Every name, label
key and scrape-target name in this document is read from that tag; none is inferred. No synthkit
live capture of a real broker exists, so every row is `v: assumed` (names and labels are
source-verified; values are synthetic). Paths are relative to the exporter repository root.*

---

## Identity and labels [slug: solace-identity]

- **Prefix.** `NewSemDesc` prepends `solace_` to every family name
  (`internal/semp/semp.desc.struct.go:21`, `namespace = "solace"` at `internal/semp/metricDesc.go:4`).
- **No broker label.** The exporter attaches only the labels in `internal/semp/metricDesc.go:8-53`.
  A broker is the scrape instance. `job` and `instance` are scrape-time labels, not exporter
  labels: synthkit defaults `job` to `solace-prometheus-exporter` (an operator choice, overridable
  by `job:`) and `instance` to `<broker name>:9628`, overridable per broker by `instance:`.
- **Label sets used here:**

| Group | Label keys | Source |
|---|---|---|
| per queue | `vpn_name`, `queue_name` | `variableLabelsVpnQueue`, `internal/semp/metricDesc.go:29` |
| per VPN | `vpn_name` | `variableLabelsVpn`, `internal/semp/metricDesc.go:15` |
| per broker | none | `nil` label list on `GlobalStats` and `Spool` rows, `internal/semp/metricDesc.go:141-152,165-172` |
| per scrape target | `endpoint` (plus `error`, see below) | `variableLabelsUp`, `internal/semp/metricDesc.go:8` |

- **`queue_name` and `vpn_name` are the exporter's own labels.** Queue and VPN counts are bounded
  by the declared estate; per-client series (`client_name`, `client_address`) exist in the exporter
  (`internal/semp/metricDesc.go:16-21`) and are deliberately **not modelled**: they would put
  per-connection identity into labels (`[slug: cardinality]`).
- **Omitted, never empty.** A healthy scrape emits `solace_up{error="",endpoint=…}`
  (`internal/exporter/exporter.collect.go:200`); an empty label value is an absent label, so
  synthkit omits `error`. Optional capacity gauges (`cpu_cores`, `memory_gib`, `max_connections`)
  are omitted when the blueprint does not declare them.

## Scrape targets [slug: solace-targets]

The construct's `targets:` list uses the exporter's own case-sensitive target names
(`internal/exporter/exporter.collect.go:21-130`; `README.md:109`, "Targets are dispatched
case-sensitively"). Synthkit's default: every target below, including `GlobalSystemInfo`, except
`QueueRates`, which the exporter lists as `DEPRECATED` (`docs/CONFIG.md:108`;
`internal/semp/getQueueRatesSemp1.go:13`). This is a synthkit selection policy, not an exporter default.

`GlobalStats` and `GlobalSystemInfo` dispatch different functions (`internal/exporter/exporter.collect.go:68-71`;
`docs/CONFIG.md:99-100`), even though both use the exporter's `GlobalStats` descriptor group.
Selecting either alone emits only that target's modelled families and its own `solace_up` endpoint.
`GlobalSystemInfo` always emits uptime; its three capacity families require explicit broker values.
Its sourced `solace_system_message_spool_quota` remains unmodelled, as documented below.

| Target | Families | Emitted by |
|---|---|---|
| `Vpn` | `solace_vpn_*` state | `internal/semp/getVpnSemp1.go:73-81` |
| `VpnStats` | `solace_vpn_*` traffic, discards, connections | `internal/semp/getVpnStatsSemp1.go:101-120` |
| `VpnSpool` | `solace_vpn_spool_*` | `internal/semp/getVpnSpoolSemp1.go:81-98` |
| `QueueDetails` | `solace_queue_spool_*`, `_binds`, `_subscriptions` | `internal/semp/getQueueDetailsSemp1.go:76-80` |
| `QueueStats` | `solace_queue_msg_*`, `_byte_spooled` | `internal/semp/getQueueStatsSemp1.go:91-104` |
| `QueueRates` | `solace_queue_{rx,tx}_{msg,byte}_rate[_avg]` | `internal/semp/getQueueRatesSemp1.go:83-90` |
| `GlobalStats` | `solace_system_*` connected clients and rx/tx totals/discards (seven families) | `GetGlobalStatsSemp1`, `internal/semp/getGlobalStatsSemp1.go:118-124` |
| `GlobalSystemInfo` | `solace_system_uptime_seconds` plus declared `_cpu_cores`, `_memory_bytes`, `_total_clients_quota` | `GetGlobalSystemInfoSemp1`, `internal/semp/getGlobalStatsSemp1.go:52-56` |
| `Spool` | `solace_system_spool_*` | `internal/semp/getSpoolSemp1.go:127-164` |

## Per-queue families [slug: solace-queue]

Sourced from `QueueDetails` (`internal/semp/metricDesc.go:617-621`), `QueueStats`
(`internal/semp/metricDesc.go:57-70`) and `QueueRates` (`internal/semp/metricDesc.go:607-614`).
Queue depth is `solace_queue_spool_usage_msgs` (messages) and `solace_queue_spool_usage_bytes`
(bytes). Counters are cumulative; the construct integrates publish minus consume rate, so a
consumer stall shows as a rising depth and, once the queue's spool quota is full, as
`solace_queue_msg_spool_usage_exceeded` (a full queue discards, it does not grow past its quota;
default quota 5000 MB, the Solace default for a queue's max spool usage per
`docs.solace.com/Messaging/Guaranteed-Msg/Message-Spooling.htm`).

> ⚠ **Exporter type quirk, reproduced.** `solace_queue_spool_usage_bytes` is registered as a
> **Counter** (`internal/semp/getQueueDetailsSemp1.go:77`) although its value is a level that falls as
> the queue drains. A real scrape carries counter type metadata, so synthkit emits it as a counter
> (value = the current level, no `_total` suffix; the value can fall). It is semantically a level:
> read it raw, not with `rate()`. Upstream semantics are deliberately not corrected.

> ⚠ The two ten-family tails of `QueueStats` that a healthy queue never moves
> (`redelivered`, `retransmitted`, `max_msg_size_exceeded`, `shutdown_discarded`, `ttl_*`,
> `max_redelivered_*`) are emitted at a constant 0, as the exporter always emits them.

```yaml signals
family: solace_queue
scope: substrate
sink: promrw
labels:
  job: solace-prometheus-exporter
  instance: <broker>:9628
  vpn_name: <message-vpn>
  queue_name: <queue>
metrics:
  # QueueDetails - internal/semp/getQueueDetailsSemp1.go:76-80
  - {root: solace_queue_spool_quota_bytes, type: gauge, unit: bytes, v: assumed, note: "metricDesc.go:617; getQueueDetailsSemp1.go:76"}
  - {root: solace_queue_spool_usage_bytes, type: counter, unit: bytes, v: assumed, note: "metricDesc.go:618; getQueueDetailsSemp1.go:77 - registered as a Counter upstream; semantically a level, emitted as a counter holding the level"}
  - {root: solace_queue_spool_usage_msgs, type: gauge, unit: count, v: assumed, note: "queue depth in messages; metricDesc.go:619; getQueueDetailsSemp1.go:78"}
  - {root: solace_queue_binds, type: gauge, unit: count, v: assumed, note: "consumers bound to the queue; metricDesc.go:620; getQueueDetailsSemp1.go:79"}
  - {root: solace_queue_subscriptions, type: gauge, unit: count, v: assumed, note: "metricDesc.go:621; getQueueDetailsSemp1.go:80"}
  # QueueStats - internal/semp/getQueueStatsSemp1.go:91-104 (all CounterValue)
  - {root: solace_queue_byte_spooled, type: counter, unit: bytes, v: assumed, note: "metricDesc.go:57; getQueueStatsSemp1.go:91"}
  - {root: solace_queue_msg_spooled, type: counter, unit: count, v: assumed, note: "metricDesc.go:58; getQueueStatsSemp1.go:92"}
  - {root: solace_queue_msg_redelivered, type: counter, unit: count, v: assumed, note: "metricDesc.go:59; getQueueStatsSemp1.go:93; constant 0"}
  - {root: solace_queue_msg_retransmitted, type: counter, unit: count, v: assumed, note: "metricDesc.go:60; getQueueStatsSemp1.go:94; constant 0"}
  - {root: solace_queue_msg_spool_usage_exceeded, type: counter, unit: count, v: assumed, note: "discards once the queue spool quota is full; metricDesc.go:61; getQueueStatsSemp1.go:95"}
  - {root: solace_queue_msg_max_msg_size_exceeded, type: counter, unit: count, v: assumed, note: "metricDesc.go:62; getQueueStatsSemp1.go:96; constant 0"}
  - {root: solace_queue_msg_total_deleted, type: counter, unit: count, v: assumed, note: "messages delivered and acknowledged, so deleted from the spool; metricDesc.go:63; getQueueStatsSemp1.go:97"}
  - {root: solace_queue_msg_shutdown_discarded, type: counter, unit: count, v: assumed, note: "metricDesc.go:64; getQueueStatsSemp1.go:98; constant 0"}
  - {root: solace_queue_msg_ttl_discarded, type: counter, unit: count, v: assumed, note: "metricDesc.go:65; getQueueStatsSemp1.go:99; constant 0"}
  - {root: solace_queue_msg_ttl_dmq, type: counter, unit: count, v: assumed, note: "metricDesc.go:66; getQueueStatsSemp1.go:100; constant 0"}
  - {root: solace_queue_msg_ttl_dmq_failed, type: counter, unit: count, v: assumed, note: "metricDesc.go:67; getQueueStatsSemp1.go:101; constant 0"}
  - {root: solace_queue_msg_max_redelivered_discarded, type: counter, unit: count, v: assumed, note: "metricDesc.go:68; getQueueStatsSemp1.go:102; constant 0"}
  - {root: solace_queue_msg_max_redelivered_dmq, type: counter, unit: count, v: assumed, note: "metricDesc.go:69; getQueueStatsSemp1.go:103; constant 0"}
  - {root: solace_queue_msg_max_redelivered_dmq_failed, type: counter, unit: count, v: assumed, note: "metricDesc.go:70; getQueueStatsSemp1.go:104; constant 0"}
  # QueueRates (opt-in, DEPRECATED upstream) - internal/semp/getQueueRatesSemp1.go:83-90 (all GaugeValue)
  - {root: solace_queue_rx_msg_rate, type: gauge, unit: per_second, v: assumed, note: "metricDesc.go:607; getQueueRatesSemp1.go:83; current-ingress-rate-per-second"}
  - {root: solace_queue_tx_msg_rate, type: gauge, unit: per_second, v: assumed, note: "metricDesc.go:608; getQueueRatesSemp1.go:84"}
  - {root: solace_queue_rx_byte_rate, type: gauge, unit: bytes_per_second, v: assumed, note: "metricDesc.go:609; getQueueRatesSemp1.go:85"}
  - {root: solace_queue_tx_byte_rate, type: gauge, unit: bytes_per_second, v: assumed, note: "metricDesc.go:610; getQueueRatesSemp1.go:86"}
  - {root: solace_queue_rx_msg_rate_avg, type: gauge, unit: per_second, v: assumed, note: "metricDesc.go:611; getQueueRatesSemp1.go:87; average-ingress-rate-per-minute"}
  - {root: solace_queue_tx_msg_rate_avg, type: gauge, unit: per_second, v: assumed, note: "metricDesc.go:612; getQueueRatesSemp1.go:88"}
  - {root: solace_queue_rx_byte_rate_avg, type: gauge, unit: bytes_per_second, v: assumed, note: "metricDesc.go:613; getQueueRatesSemp1.go:89"}
  - {root: solace_queue_tx_byte_rate_avg, type: gauge, unit: bytes_per_second, v: assumed, note: "metricDesc.go:614; getQueueRatesSemp1.go:90"}
```

## Per-VPN families [slug: solace-vpn]

Sourced from `Vpn` (`internal/semp/metricDesc.go:394-402`), `VpnStats`
(`internal/semp/metricDesc.go:551-570`) and `VpnSpool` (`internal/semp/metricDesc.go:474-486`).
VPN totals are the sum of the VPN's queues: `solace_vpn_rx_msgs_total` advances with every message
its queues accept, `solace_vpn_tx_msgs_total` with every message delivered, and
`solace_vpn_spool_usage_msgs` is the sum of queue depths, so the families agree. `Publishers` and
queue consumers are SMF client connections and guaranteed flows
(`solace_vpn_connections`, `solace_vpn_spool_current_{ingress,egress}_flows`); the other connection
services are emitted at 0.

> ⚠ `solace_vpn_spool_usage_pct` is `round(usage / quota * 100)`, and `-1` when no spool is
> allocated (`internal/semp/getVpnSpoolSemp1.go:84-88`).

> ⚠ **Not modelled although sourced:** `solace_vpn_quota_connections_{amqp,smf,web,mqtt,rest_in,rest_out}`
> (`internal/semp/metricDesc.go:565-570`) and the `solace_vpn_spool_maximum_*` limits
> (`internal/semp/metricDesc.go:479-485`); only the total connection quota is emitted, and only
> when the VPN declares `max_connections`.

```yaml signals
family: solace_vpn
scope: substrate
sink: promrw
labels:
  job: solace-prometheus-exporter
  instance: <broker>:9628
  vpn_name: <message-vpn>
metrics:
  # Vpn - internal/semp/getVpnSemp1.go:73-81 (all GaugeValue)
  - {root: solace_vpn_is_management_vpn, type: gauge, unit: bool, v: assumed, note: "metricDesc.go:394; getVpnSemp1.go:73"}
  - {root: solace_vpn_enabled, type: gauge, unit: bool, v: assumed, note: "metricDesc.go:395; getVpnSemp1.go:74"}
  - {root: solace_vpn_operational, type: gauge, unit: bool, v: assumed, note: "metricDesc.go:396; getVpnSemp1.go:75"}
  - {root: solace_vpn_locally_configured, type: gauge, unit: bool, v: assumed, note: "metricDesc.go:397; getVpnSemp1.go:76"}
  - {root: solace_vpn_local_status, type: gauge, unit: bool, v: assumed, note: "0=Down, 1=Up per encodeMetricMulti; metricDesc.go:398; getVpnSemp1.go:77"}
  - {root: solace_vpn_unique_subscriptions, type: gauge, unit: count, v: assumed, note: "metricDesc.go:399; getVpnSemp1.go:78"}
  - {root: solace_vpn_total_local_unique_subscriptions, type: gauge, unit: count, v: assumed, note: "metricDesc.go:400; getVpnSemp1.go:79"}
  - {root: solace_vpn_total_remote_unique_subscriptions, type: gauge, unit: count, v: assumed, note: "metricDesc.go:401; getVpnSemp1.go:80; constant 0"}
  - {root: solace_vpn_total_unique_subscriptions, type: gauge, unit: count, v: assumed, note: "metricDesc.go:402; getVpnSemp1.go:81"}
  # VpnStats - internal/semp/getVpnStatsSemp1.go:101-120
  - {root: solace_vpn_rx_msgs_total, type: counter, unit: count, v: assumed, note: "metricDesc.go:551; getVpnStatsSemp1.go:101"}
  - {root: solace_vpn_tx_msgs_total, type: counter, unit: count, v: assumed, note: "metricDesc.go:552; getVpnStatsSemp1.go:102"}
  - {root: solace_vpn_rx_bytes_total, type: counter, unit: bytes, v: assumed, note: "metricDesc.go:553; getVpnStatsSemp1.go:103"}
  - {root: solace_vpn_tx_bytes_total, type: counter, unit: bytes, v: assumed, note: "metricDesc.go:554; getVpnStatsSemp1.go:104"}
  - {root: solace_vpn_rx_discarded_msgs_total, type: counter, unit: count, v: assumed, note: "ingress discards, here the queue-spool-full discards; metricDesc.go:555; getVpnStatsSemp1.go:105"}
  - {root: solace_vpn_tx_discarded_msgs_total, type: counter, unit: count, v: assumed, note: "metricDesc.go:556; getVpnStatsSemp1.go:106; constant 0"}
  - {root: solace_vpn_connections, type: gauge, unit: count, v: assumed, note: "metricDesc.go:563; getVpnStatsSemp1.go:107"}
  - {root: solace_vpn_connections_service_amqp, type: gauge, unit: count, v: assumed, note: "metricDesc.go:557; getVpnStatsSemp1.go:108; constant 0"}
  - {root: solace_vpn_connections_service_mqtt, type: gauge, unit: count, v: assumed, note: "metricDesc.go:558; getVpnStatsSemp1.go:109; constant 0"}
  - {root: solace_vpn_connections_service_smf, type: gauge, unit: count, v: assumed, note: "metricDesc.go:559; getVpnStatsSemp1.go:110"}
  - {root: solace_vpn_connections_service_web, type: gauge, unit: count, v: assumed, note: "metricDesc.go:560; getVpnStatsSemp1.go:111; constant 0"}
  - {root: solace_vpn_connections_service_rest_in, type: gauge, unit: count, v: assumed, note: "metricDesc.go:561; getVpnStatsSemp1.go:112; constant 0"}
  - {root: solace_vpn_connections_service_rest_out, type: gauge, unit: count, v: assumed, note: "metricDesc.go:562; getVpnStatsSemp1.go:113; constant 0"}
  - {root: solace_vpn_quota_connections, type: gauge, unit: count, v: assumed, note: "only when the VPN declares max_connections; metricDesc.go:564; getVpnStatsSemp1.go:114"}
  # VpnSpool - internal/semp/getVpnSpoolSemp1.go:81-98 (all GaugeValue)
  - {root: solace_vpn_spool_quota_bytes, type: gauge, unit: bytes, v: assumed, note: "metricDesc.go:474; getVpnSpoolSemp1.go:81"}
  - {root: solace_vpn_spool_usage_bytes, type: gauge, unit: bytes, v: assumed, note: "metricDesc.go:475; getVpnSpoolSemp1.go:82"}
  - {root: solace_vpn_spool_usage_pct, type: gauge, unit: percent, v: assumed, note: "metricDesc.go:476; getVpnSpoolSemp1.go:84-88; -1 when no spool allocated"}
  - {root: solace_vpn_spool_usage_msgs, type: gauge, unit: count, v: assumed, note: "metricDesc.go:477; getVpnSpoolSemp1.go:89"}
  - {root: solace_vpn_spool_current_endpoints, type: gauge, unit: count, v: assumed, note: "metricDesc.go:478; getVpnSpoolSemp1.go:90"}
  - {root: solace_vpn_spool_current_egress_flows, type: gauge, unit: count, v: assumed, note: "metricDesc.go:480; getVpnSpoolSemp1.go:92"}
  - {root: solace_vpn_spool_current_ingress_flows, type: gauge, unit: count, v: assumed, note: "metricDesc.go:482; getVpnSpoolSemp1.go:94"}
  - {root: solace_vpn_spool_current_transacted_sessions, type: gauge, unit: count, v: assumed, note: "metricDesc.go:484; getVpnSpoolSemp1.go:96; constant 0"}
  - {root: solace_vpn_spool_current_transacted_msgs, type: gauge, unit: count, v: assumed, note: "metricDesc.go:486; getVpnSpoolSemp1.go:98; constant 0"}
```

## Per-broker families [slug: solace-system]

Sourced from targets `GlobalStats` and `GlobalSystemInfo` (shared descriptor group
`GlobalStats`, `internal/semp/metricDesc.go:141-152`) and `Spool`
(`internal/semp/metricDesc.go:165-209`). Carry only `job` and `instance`. Broker rx/tx totals are
the sum over the broker's VPNs; `solace_system_total_clients_connected` is the sum of VPN
connections; the spool and flow gauges are the sums over its queues and VPNs.

> ⚠ **Exporter type quirks, reproduced.** `solace_system_uptime_seconds` and
> `solace_system_total_clients_quota` are registered as **Counters**
> (`internal/semp/getGlobalStatsSemp1.go:52-53`). Uptime genuinely accumulates: it starts from a
> deterministic per-broker age and advances by the elapsed time. The client quota is semantically a
> constant limit registered as a counter upstream; synthkit emits it as a counter whose value is the
> level, matching the type metadata a real scrape carries. Upstream semantics are not corrected.

> ⚠ **Not modelled although sourced:** the remaining `Spool` rows (quotas, defragmentation, disk
> partitions, transactions, ADB) at `internal/semp/metricDesc.go:165-208`, `solace_system_message_spool_quota`
> (`:143`), and every target outside the Scrape targets table above
> (client, bridge, topic-endpoint, MQTT, RDP, hardware). They are sourced but not yet synthesised.

```yaml signals
family: solace_system
scope: substrate
sink: promrw
labels:
  job: solace-prometheus-exporter
  instance: <broker>:9628
metrics:
  # GlobalSystemInfo - GetGlobalSystemInfoSemp1, internal/semp/getGlobalStatsSemp1.go:52-56
  - {root: solace_system_total_clients_quota, type: counter, unit: count, v: assumed, note: "only when the broker declares max_connections; metricDesc.go:142; getGlobalStatsSemp1.go:53 - registered as a Counter upstream; semantically a constant limit, emitted as a counter holding the level"}
  - {root: solace_system_uptime_seconds, type: counter, unit: seconds, v: assumed, note: "metricDesc.go:144; getGlobalStatsSemp1.go:52 - CounterValue"}
  - {root: solace_system_cpu_cores, type: gauge, unit: count, v: assumed, note: "only when the broker declares cpu_cores; metricDesc.go:145; getGlobalStatsSemp1.go:55"}
  - {root: solace_system_memory_bytes, type: gauge, unit: bytes, v: assumed, note: "only when the broker declares memory_gib; metricDesc.go:146; getGlobalStatsSemp1.go:56"}
  # GlobalStats - GetGlobalStatsSemp1, internal/semp/getGlobalStatsSemp1.go:118-124
  - {root: solace_system_total_clients_connected, type: gauge, unit: count, v: assumed, note: "metricDesc.go:141; getGlobalStatsSemp1.go:118"}
  - {root: solace_system_rx_msgs_total, type: counter, unit: count, v: assumed, note: "metricDesc.go:147; getGlobalStatsSemp1.go:119"}
  - {root: solace_system_tx_msgs_total, type: counter, unit: count, v: assumed, note: "metricDesc.go:148; getGlobalStatsSemp1.go:120"}
  - {root: solace_system_rx_bytes_total, type: counter, unit: bytes, v: assumed, note: "metricDesc.go:149; getGlobalStatsSemp1.go:121"}
  - {root: solace_system_tx_bytes_total, type: counter, unit: bytes, v: assumed, note: "metricDesc.go:150; getGlobalStatsSemp1.go:122"}
  - {root: solace_system_total_rx_discards, type: counter, unit: count, v: assumed, note: "metricDesc.go:151; getGlobalStatsSemp1.go:123"}
  - {root: solace_system_total_tx_discards, type: counter, unit: count, v: assumed, note: "metricDesc.go:152; getGlobalStatsSemp1.go:124; constant 0"}
  # Spool - internal/semp/getSpoolSemp1.go (all GaugeValue)
  - {root: solace_system_spool_usage_bytes, type: gauge, unit: bytes, v: assumed, note: "metricDesc.go:169; getSpoolSemp1.go:127"}
  - {root: solace_system_spool_usage_msgs, type: gauge, unit: count, v: assumed, note: "metricDesc.go:170; getSpoolSemp1.go:129"}
  - {root: solace_system_spool_ingress_flows_count, type: gauge, unit: count, v: assumed, note: "metricDesc.go:187; getSpoolSemp1.go:131"}
  - {root: solace_system_spool_egress_flows_count, type: gauge, unit: count, v: assumed, note: "active + inactive + browser per getSpoolSemp1.go:134; metricDesc.go:189"}
  - {root: solace_system_spool_egress_flows_active, type: gauge, unit: count, v: assumed, note: "metricDesc.go:190; getSpoolSemp1.go:135"}
  - {root: solace_system_spool_egress_flows_inactive, type: gauge, unit: count, v: assumed, note: "metricDesc.go:191; getSpoolSemp1.go:136; constant 0"}
  - {root: solace_system_spool_egress_flows_browser, type: gauge, unit: count, v: assumed, note: "metricDesc.go:192; getSpoolSemp1.go:137; constant 0"}
  - {root: solace_system_spool_endpoints_queue, type: gauge, unit: count, v: assumed, note: "metricDesc.go:194; getSpoolSemp1.go:140"}
  - {root: solace_system_spool_endpoints_dte, type: gauge, unit: count, v: assumed, note: "metricDesc.go:195; getSpoolSemp1.go:141; constant 0"}
```

## Scrape health [slug: solace-up]

One `solace_up` per scrape target per broker, value `1` on a healthy scrape. Labels are `endpoint`
(the target name from the table above, `internal/exporter/exporter.collect.go:183,200`) and `error`;
`error` is `""` on success and so is omitted (`internal/semp/metricDesc.go:8,75`). Synthkit never
emits the failed-scrape shape. This is distinct from the Prometheus scrape's own `up` series, which
synthkit does not emit.

```yaml signals
family: solace_up
scope: substrate
sink: promrw
labels:
  job: solace-prometheus-exporter
  instance: <broker>:9628
  endpoint: <scrape-target>   # Vpn|VpnStats|VpnSpool|QueueDetails|QueueStats|QueueRates|GlobalStats|GlobalSystemInfo|Spool
  # error: OMITTED - empty on a healthy scrape, so absent per the no-empty-label invariant
metrics:
  - {root: solace_up, type: gauge, unit: bool, v: assumed, note: "metricDesc.go:75; exporter.collect.go:200 - value 1 per healthy target"}
```
