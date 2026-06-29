# Network Topology Exporter — ScopeSubstrate

Self-hosted SNMP-based network-topology discovery exporter
(`github.com/colinedwardwood/network-topology-exporter`). Polls device neighbours via
LLDP/CDP/BGP/OSPF/FDB/IS-IS/MPLS-TE SNMP walks, reconciles a live topology graph, and
exposes `network_topology_*` metrics. Substrate-scoped: disambiguated by `(job, instance)`;
never carries a `blueprint` label. Global rules: see [`00-canon.md`](00-canon.md) — scoping
`[slug: scoping]`, cardinality `[slug: cardinality]`, shape rules `[slug: shape-rules]`.

*Provenance: exporter source ../network-topology-exporter (github.com/colinedwardwood/network-topology-exporter) read 2026-06-17; synthesized by internal/construct/nettopo.*

---

## Identity and scoping [slug: nettopo-identity]

**ScopeSubstrate.** Every series carries `job` + `instance` as the substrate disambiguation
pair. `job` defaults to `"integrations/network-topology-exporter"`; `instance` is the exporter
scrape endpoint (e.g. `"netobs-dc1:9100"`). No `blueprint` label is stamped (see
`[slug: scoping]`). A second blueprint declaring a different `instance` produces a disjoint
series set; the same `instance` across two blueprints is rejected at load as a collision.

Sub-families are **presence-gated** (active when the corresponding config is declared, passive
otherwise — no explicit enable flags):

| Gate | Families enabled |
|---|---|
| `role: hub` | federation hub families (`nettopo-federation` hub half) |
| `role: spoke` | federation spoke families (`nettopo-federation` spoke half) |
| `session_pool: true` | SNMP session pool (`nettopo-session-pool`) |
| `otlp_output: true` | OTLP push accounting (`nettopo-otlp`) |
| `out_of_scope_neighbours > 0` | `network_topology_out_of_scope_neighbours_total` + hub boundary observations |

---

## Device inventory [slug: nettopo-inventory]

One `network_topology_device_info` info gauge (value=1) per discovered device; a companion
`device_uptime_seconds` gauge per device; plus two scalar graph totals. Per-device labels
(`device_id`, `vendor`, `os_version`, `site`) carry **all** device identity — they are
low-cardinality within a real deployment (device counts are bounded by estate size, not
per-request cardinality).

> ⚠ **`model` is OMITTED** from `device_info` labels. The real exporter has no ENTITY-MIB
> (system object OID) / model-name source — no model walk is performed. Synthkit omits the
> label entirely per the no-empty-label invariant (absent dimension → label omitted, never
> `""` or `"NA"`). Model strings appearing in blueprint `devices:` entries are accepted for
> human reference only and do not propagate to any emitted series.

**Real vendor/OS examples** (from the exporter vendor catalogue):

| vendor | os_version |
|---|---|
| cisco | 17.12.1 |
| arista | 4.36.0F |
| juniper | 25.4R1 |
| nokia | 25.7.R2 |

**Uptime dynamics (updated 2026-06-17):** `device_uptime_seconds` climbs monotonically like
real SNMP `sysUpTime`. Each device is anchored to a fixed cold-start time derived from the
blueprint-declared `uptime:` value (or a deterministic per-device hash in [3 d, 400 d]). The
value is bounded strictly BELOW the ~497-day 32-bit sysUpTime wrap — no wrap is modelled
(real exporter wraps only after 497 d of uninterrupted uptime; synthkit clamps below the
ceiling). On rare deterministic reboots (probability ~0.2–1 per year per device, hash-seeded)
uptime resets to a post-reboot value and resumes climbing from there.

> ⚠ **Cold-start restart tradeoff:** uptime is coldStart-relative (honours the declared
> `uptime:` base and is consistent with the existing cold-start model). A synthkit process
> restart resets the anchor, so uptime jumps back to its declared base on restart — the same
> way the cold-start discovery burst re-fires. A wall-clock-stable schedule (persisting the
> anchor across restarts) is a possible future refinement.

**os_version dynamics (updated 2026-06-17):** on a fraction of reboots (~1 in 3), the device
advances to the next os_version in a per-vendor ordered upgrade list (e.g. arista:
`4.33.0F → 4.34.0F → 4.35.0F → 4.36.0F`). Blueprint-declared `os_version:` values are
custom strings returned verbatim and are never auto-advanced — only fabric-generated devices
participate in the upgrade list. Provenance: vendor upgrade paths synthesised from
`network-topology-exporter` vendor catalogue, 2026-06-17.

```yaml signals
family: network_topology_inventory
scope: substrate
sink: promrw
labels:
  job: integrations/network-topology-exporter
  instance: <host:port>
  device_id: <device-id>       # e.g. "spine-01", "leaf-03"
  vendor: cisco|arista|juniper|nokia
  # model: OMITTED — no ENTITY-MIB/model walk in real exporter; label absent per no-empty invariant
  os_version: <os>             # e.g. "4.36.0F"; advances on planned upgrade-reboot for fabric devices
  site: <site>                 # e.g. "dc1", "lon-core"
metrics:
  - {root: network_topology_device_info, type: gauge, unit: info, v: ok, note: "value=1; labels: job,instance,device_id,vendor,os_version,site — model OMITTED (no ENTITY-MIB source in real exporter)"}
  - {root: network_topology_device_uptime_seconds, type: gauge, unit: seconds, v: ok, note: "labels: job,instance,device_id only — no vendor/os on uptime series; bounded below 497d sysUpTime wrap; resets on rare deterministic reboot"}
  - {root: network_topology_graph_devices_total, type: gauge, unit: count, v: ok, note: "labels: job,instance only"}
  - {root: network_topology_graph_edges_total, type: gauge, unit: count, v: ok, note: "labels: job,instance only"}
```

> ⚠ `network_topology_device_uptime_seconds` carries **only** `{job, instance, device_id}` —
> the vendor/os labels are on `device_info`, not uptime. Join on `device_id` in queries.

---

## Topology edges [slug: nettopo-edges]

One `network_topology_edge_info` info gauge (value=1) per reconciled topology edge. The
**Prometheus series is deliberately scoped to 7 edge-identity keys** (`src_device`, `src_port`,
`dst_device`, `dst_port`, `discovery_proto`, `link_kind`, `direction`) to bound cardinality.

> ⚠ **Cardinality omission (intentional design):** `confidence`, `adjacency`,
> `precedence_rank`, and per-edge `metadata` keys (`bgp.remote_as`,
> `mpls_te.admin_status`, `peer_chassis_mac`, `degraded`) are **deliberately absent** from
> the Prometheus `edge_info` labels. Adding them would multiply series cardinality by the
> number of metadata key/value combinations across edges. They are only available via the
> OTLP projection (see below).

**Label enum values:**

| Label | Values |
|---|---|
| `discovery_proto` | `lldp`, `cdp`, `bgp`, `ospf`, `fdb`, `isis`, `mpls_te`, `configured` |
| `link_kind` | `ethernet`, `ip`, `mpls-te`, `logical` |
| `direction` | `bidirectional`, `unidirectional` |

> ⚠ For OSPF, BGP, and IS-IS edges `dst_port` is **empty/absent** (no interface OID in those
> MIBs). For MPLS-TE edges `src_port` is a tunnel name (e.g. `"te-tunnel42"`) and `dst_port`
> is absent. Absent ports are **omitted** from the label set — never `""` or `"NA"`
> (see `[slug: cardinality]`).

```yaml signals
family: network_topology_edges
scope: substrate
sink: promrw
labels:
  job: integrations/network-topology-exporter
  instance: <host:port>
  src_device: <device-id>
  src_port: <interface>      # absent on bgp/ospf/isis dst; tunnel name on mpls_te src
  dst_device: <device-id>
  dst_port: <interface>      # absent on bgp/ospf/isis/mpls_te
  discovery_proto: lldp|cdp|bgp|ospf|fdb|isis|mpls_te|configured
  link_kind: ethernet|ip|mpls-te|logical
  direction: bidirectional|unidirectional
metrics:
  - {root: network_topology_edge_info, type: gauge, unit: info, v: ok, note: "value=1; one series per edge; confidence/adjacency/metadata omitted (cardinality design)"}
```

### OTLP projection (alternate emission)

When `otlp_output: true` the exporter also ships edges as OTLP spans/events. The OTLP
projection **adds** the fields omitted from Prometheus:

| OTLP attribute | Values / notes |
|---|---|
| `confidence` | `high`, `medium`, `low` |
| `adjacency` | `direct`, `indirect`, `unknown` |
| `precedence_rank` | integer string (`"1"` … `"8"`) |
| `network.topology.bgp.remote_as` | remote ASN string (BGP edges) |
| `network.topology.mpls_te.admin_status` | `"up"` (MPLS-TE edges) |
| `network.topology.peer_chassis_mac` | lowercase MAC (LLDP/CDP edges) |
| `network.topology.degraded` | `"true"` when degraded |

OTLP resource attributes: `service.name="network-topology-exporter"`, `service.version`,
`service.instance.id` (= `instance` / `spoke_id` on spokes).

### LD-10 precedence ladder

The exporter resolves multi-protocol edge conflicts by precedence rank:

| discovery_proto | rank | confidence | link_kind | adjacency |
|---|---|---|---|---|
| configured | 1 | high | ethernet | direct |
| lldp | 2 | high | ethernet | direct |
| cdp | 3 | high | ethernet | direct |
| fdb | 4 | medium | ethernet | direct |
| isis | 5 | medium | ip | direct |
| ospf | 6 | medium | ip | direct |
| bgp | 7 | low | ip | unknown |
| mpls_te | 8 | medium | mpls-te | unknown |

Lower rank = higher precedence. When the same physical link is seen by multiple protocols
the entry with the lowest rank wins the reconciled `edge_info` series.

---

## Change and conflict events [slug: nettopo-changes]

Counters that accumulate topology mutation events. `network_topology_change_total` increments
on each discovered add/remove/update per protocol. `network_topology_conflict_total` fires
when two discovery sources disagree on the same adjacency (neighbour_disagreement).
`network_topology_out_of_scope_neighbours_total` is a gauge of currently-tracked out-of-scope
neighbours (declared by `out_of_scope_neighbours` in config).

> ⚠ Use `increase()` (not `rate()`) for `change_total` and `conflict_total` — both reset to
> zero on exporter restart, producing a Prometheus counter reset. `rate()` over a restart
> window returns a falsely-inflated spike.

**Real edge churn dynamics (updated 2026-06-17):** `change_total` now reflects real
topology visibility transitions. Edges physically drop from `edge_info` while they are
flapping (link-layer instability, ~1 flap per 200 edges per day) or while an endpoint device
is undergoing a reboot (all edges touching that device disappear for the reboot window, then
re-appear). The `change_total` counter increments on each visibility transition — the
`change_kind=added/removed` counter values are exact mirrors of the `edge_info` inventory at
each tick (counter ↔ inventory are consistent).

> ⚠ `change_kind=updated` is in the documented enum but is **NOT currently emitted** by
> synthkit. Real link-layer flaps and device reboots generate `added`/`removed` transitions
> only. Attribute-change "updated" events (e.g. a metadata field change with no topology
> change) are not modelled. The `updated` value is retained in the enum for schema
> completeness.

**conflict_type** is `neighbour_disagreement` only — that is the sole grounded value from the
exporter source (no additional conflict_type values are documented, so none are invented).

```yaml signals
family: network_topology_changes
scope: substrate
sink: promrw
labels:
  job: integrations/network-topology-exporter
  instance: <host:port>
  change_kind: added|removed|updated       # on change_total; "updated" in enum but NOT currently synth-emitted (see note above)
  discovery_proto: lldp|cdp|bgp|ospf|fdb|isis|mpls_te|configured   # on change_total
  conflict_type: neighbour_disagreement    # on conflict_total; sole grounded value from exporter source
metrics:
  - {root: network_topology_change_total, type: counter, unit: count, v: ok, note: "labels: job,instance,change_kind,discovery_proto; increase() not rate(); added/removed reflect real edge churn (flaps+reboots); updated NOT currently synth-emitted"}
  - {root: network_topology_conflict_total, type: counter, unit: count, v: ok, note: "labels: job,instance,conflict_type=neighbour_disagreement; increase() not rate()"}
  - {root: network_topology_out_of_scope_neighbours_total, type: gauge, unit: count, v: ok, note: "labels: job,instance only; steady-state neighbour count outside declared scope"}
```

---

## Discovery health [slug: nettopo-discovery-health]

The full SNMP discovery and walker health surface. All metrics carry `{job, instance}` as the
base; per-metric additional labels are listed below.

**`discovery_devices_total` status/reason enums:**

| status | reason values |
|---|---|
| `success` | `n/a` |
| `failed` | `unreachable`, `auth_failed`, `timeout`, `snmp_error`, `mib_unsupported`, `dns_failed`, `outside_allow_list`, `no_credentials`, `budget_expired`, `panic` |

**`snmp_walks_total` status:** `ok`, `timeout`, `error` (note: a DIFFERENT status enum than `discovery_devices_total`, which uses `success`/`failed`). `reason=n/a` on `ok`/`timeout` rows; the `reason` breakdown applies to `error` rows.

**`module` label values:** `snmp`, `lldp`, `cdp`, `bgp`, `ospf`, `fdb`, `isis`, `mpls_te`.

**Walker-outcome bucket semantics:**

| outcome | meaning |
|---|---|
| `edges` | walk produced ≥1 new edges |
| `mib_unimplemented` | device does not implement the required MIB OIDs |
| `no_neighbours` | walk completed successfully but found zero neighbours |
| `walker_drift` | PDUs arrived but EVERY row was rejected by the decoder — the MIB IS implemented, our decoder doesn't match this firmware (page-level signal) |
| `error` | walk failed with a hard error |
| `malformed_index` | (BGP walker only) index in MIB response could not be decoded |

**`module_last_status` values:** `0` = ok, `1` = degraded, `2` = hard-failed.

```yaml signals
family: network_topology_discovery_health
scope: substrate
sink: promrw
labels:
  job: integrations/network-topology-exporter
  instance: <host:port>
  status: success|failed                  # discovery_devices_total (snmp_walks_total uses a DIFFERENT enum: ok|timeout|error)
  reason: n/a|unreachable|auth_failed|timeout|snmp_error|mib_unsupported|dns_failed|outside_allow_list|no_credentials|budget_expired|panic   # discovery_devices_total
  module: snmp|lldp|cdp|bgp|ospf|fdb|isis|mpls_te   # discovery_module_duration_seconds, decode_issues, quarantined_rows, degraded, hard_fail, module_last_status
  oid: <oid-string>                       # discovery_decode_issues_total, discovery_quarantined_rows_total
  # discovery_decode_issues_total real OIDs and reason enums (source: network-topology-exporter docs/metrics.md #99, 2026-06-17):
  #   lldp  oid="1.0.8802.1.1.2.1.4.1"  reason ∈ {chassis_subtype_invalid, port_subtype_invalid, chassis_mac_bad_length, port_mac_bad_length, chassis_addr_malformed}
  #   cdp   oid="1.3.6.1.4.1.9.9.23.1.2.1"  reason ∈ {index_unparseable, empty_device_id}
  #   ospf  oid="1.3.6.1.2.1.14.10"  reason ∈ {oid_suffix_malformed, nbr_ip_undecodable}
  #   fdb   oid="1.3.6.1.2.1.17.1.4" (dot1dTpFdbTable, B-MIB)  reason ∈ {bridge_port_index_invalid, ifindex_unmapped}
  # discovery_degraded_total real reason enum (source: network-topology-exporter internal/discovery/discovery.go, 2026-06-17):
  #   reason ∈ {required_table_partial_decode, missing_srcport_mapping, missing_admin_status_walk, invalid_admin_status_decode, unsupported_ip_version}
  #   fdb module also contributes: reason ∈ {qbridge_walk_failed, vlan_walk_failed}  (source: docs/metrics.md #100)
  #   isis module also contributes: reason=unsupported_ip_version  (source: docs/metrics.md #102)
  # discovery_hard_fail_total real reason enum (source: network-topology-exporter internal/discovery/snmp/pdu.go + internal/app/probe_target.go, 2026-06-17):
  #   reason ∈ {required_table_no_valid_rows, required_table_invalid_ratio_exceeded, system_group_walk_error}
  walker: lldp|cdp|ospf|fdb              # walker_outcome_total
  outcome: edges|mib_unimplemented|no_neighbours|walker_drift|error   # walker_outcome_total
  reason_bgp: edges|no_peers|mib_unimplemented|walker_drift|error|malformed_index   # bgp_walker_outcome_total outcome
metrics:
  - {root: network_topology_discovery_devices_total, type: gauge, unit: count, v: ok, note: "labels: job,instance,status,reason; reason=n/a when status=ok"}
  - {root: network_topology_discovery_cycle_duration_seconds, type: histogram, unit: seconds, v: ok, note: "labels: job,instance; buckets 0.5,1,2,4,8,16,32,64,128,256s"}
  - {root: network_topology_discovery_module_duration_seconds, type: histogram, unit: seconds, v: ok, note: "labels: job,instance,module"}
  - {root: network_topology_snmp_walks_total, type: counter, unit: count, v: ok, note: "labels: job,instance,status(ok|timeout|error),reason; reason=n/a on ok/timeout rows, breakdown on error rows"}
  - {root: network_topology_discovery_decode_issues_total, type: counter, unit: count, v: ok, note: "labels: job,instance,module,oid,reason; FAULT-ONLY — absent in a healthy steady state; real reason/oid pairs documented in label comments above (sourced from exporter docs/metrics.md #99, 2026-06-17)"}
  - {root: network_topology_discovery_quarantined_rows_total, type: counter, unit: count, v: ok, note: "labels: job,instance,module,oid,reason; FAULT-ONLY — absent in a healthy steady state"}
  - {root: network_topology_discovery_degraded_total, type: counter, unit: count, v: ok, note: "labels: job,instance,module,reason; FAULT-ONLY — absent in healthy steady state; reason ∈ {required_table_partial_decode, missing_srcport_mapping, missing_admin_status_walk, invalid_admin_status_decode, unsupported_ip_version, qbridge_walk_failed, vlan_walk_failed} (sourced from exporter internal/discovery/discovery.go + docs/metrics.md #100/#102, 2026-06-17)"}
  - {root: network_topology_discovery_hard_fail_total, type: counter, unit: count, v: ok, note: "labels: job,instance,module,reason; FAULT-ONLY — absent in healthy steady state; reason ∈ {required_table_no_valid_rows, required_table_invalid_ratio_exceeded, system_group_walk_error} (sourced from exporter internal/discovery/snmp/pdu.go + internal/app/probe_target.go, 2026-06-17)"}
  - {root: network_topology_credential_trials_total, type: counter, unit: count, v: ok, note: "labels: job,instance,status"}
  - {root: network_topology_walker_outcome_total, type: counter, unit: count, v: ok, note: "labels: job,instance,walker(lldp|cdp|ospf|fdb),outcome(edges|mib_unimplemented|no_neighbours|walker_drift|error)"}
  - {root: network_topology_bgp_walker_outcome_total, type: counter, unit: count, v: ok, note: "labels: job,instance,walker(vendor_cisco|vendor_arista|vendor_juniper|vendor_nokia|rfc4273),outcome(edges|no_peers|mib_unimplemented|walker_drift|error|malformed_index)"}
  - {root: network_topology_system_walk_anomaly_total, type: counter, unit: count, v: ok, note: "labels: job,instance,reason(empty_sysname|unknown_vendor)"}
  - {root: network_topology_module_last_status, type: gauge, unit: state, v: ok, note: "labels: job,instance,module; 0=ok 1=degraded 2=hard-failed"}
  - {root: network_topology_cycle_budget_skips_total, type: counter, unit: count, v: ok, note: "labels: job,instance only"}
  - {root: network_topology_fdb_suppressed_macs_total, type: counter, unit: count, v: ok, note: "labels: job,instance only; MACs suppressed by FDB cardinality guard"}
  - {root: network_topology_snmp_rate_limit_wait_seconds, type: histogram, unit: seconds, v: ok, note: "labels: job,instance; time blocked on per-device SNMP rate-limit token bucket"}
```

---

## Graph freshness [slug: nettopo-freshness]

Gauges that track whether the in-memory topology graph is current and whether snapshot
persistence is healthy.

```yaml signals
family: network_topology_freshness
scope: substrate
sink: promrw
labels:
  job: integrations/network-topology-exporter
  instance: <host:port>
  reason: queue_full|write_in_flight    # snapshot_drops_total
metrics:
  - {root: network_topology_graph_stale, type: gauge, unit: bool, v: ok, note: "1 when the graph has not been updated within the configured staleness threshold; 0 otherwise"}
  - {root: network_topology_snapshot_last_written_timestamp_seconds, type: gauge, unit: unix_timestamp, v: ok, note: "labels: job,instance only; unix epoch of last successful snapshot write"}
  - {root: network_topology_snapshot_loaded_devices_total, type: gauge, unit: count, v: ok, note: "labels: job,instance only; device count in the last loaded snapshot"}
  - {root: network_topology_snapshot_queue_depth, type: gauge, unit: count, v: ok, note: "labels: job,instance only; pending snapshot write queue depth"}
  - {root: network_topology_snapshot_drops_total, type: counter, unit: count, v: ok, note: "labels: job,instance,reason(queue_full|write_in_flight)"}
```

---

## Process self-observability [slug: nettopo-self-obs]

Internal exporter process health metrics (scrape performance, runtime).

```yaml signals
family: network_topology_self_obs
scope: substrate
sink: promrw
labels:
  job: integrations/network-topology-exporter
  instance: <host:port>
  site: <site>    # panics_total only
metrics:
  - {root: network_topology_metrics_render_duration_seconds, type: histogram, unit: seconds, v: ok, note: "labels: job,instance; time to render the full Prometheus metrics page"}
  - {root: network_topology_metrics_payload_bytes, type: histogram, unit: bytes, v: ok, note: "labels: job,instance; size of the rendered metrics payload"}
  - {root: network_topology_last_scrape_duration_seconds, type: gauge, unit: seconds, v: ok, note: "labels: job,instance; duration of the last completed scrape"}
  - {root: network_topology_last_scrape_samples_total, type: gauge, unit: count, v: ok, note: "labels: job,instance; number of samples in the last scrape"}
  - {root: network_topology_goroutines, type: gauge, unit: count, v: ok, note: "labels: job,instance; current goroutine count"}
  - {root: network_topology_panics_total, type: counter, unit: count, v: ok, note: "labels: job,instance,site; incremented on recovered panics within the discovery loop"}
```

---

## SNMP session pool [slug: nettopo-session-pool]

**GATED: active only when `session_pool: true`** in the blueprint declaration.

When session pooling is enabled the exporter maintains a pool of reusable SNMP sessions per
device. These metrics track pool efficiency.

```yaml signals
family: network_topology_session_pool
scope: substrate
sink: promrw
labels:
  job: integrations/network-topology-exporter
  instance: <host:port>
  reason: idle|credential_rotation|connection_error    # snmp_session_pool_evictions_total
metrics:
  - {root: network_topology_snmp_session_pool_size, type: gauge, unit: count, v: ok, note: "labels: job,instance only; current pool size (active sessions)"}
  - {root: network_topology_snmp_session_pool_hits_total, type: counter, unit: count, v: ok, note: "labels: job,instance only; requests served from pool without re-establishing"}
  - {root: network_topology_snmp_session_pool_misses_total, type: counter, unit: count, v: ok, note: "labels: job,instance only; requests requiring a new session (pool miss)"}
  - {root: network_topology_snmp_session_pool_evictions_total, type: counter, unit: count, v: ok, note: "labels: job,instance,reason(idle|credential_rotation|connection_error)"}
```

---

## Federation [slug: nettopo-federation]

**GATED: active only when `role: hub` or `role: spoke`** (standalone instances emit nothing
from this family).

In a federated deployment a **hub** aggregates topology graphs from multiple **spokes** over
HTTP. The hub tracks spoke liveness and rejects out-of-budget or structurally-invalid updates;
spokes track push success and queue health.

### Hub metrics

```yaml signals
family: network_topology_federation_hub
scope: substrate
sink: promrw
labels:
  job: integrations/network-topology-exporter
  instance: <host:port>
  spoke_id: <spoke-id>    # federation_spoke_up, federation_spoke_last_push_timestamp_seconds
  peer_a: <device-id>     # boundary_observation_info
  peer_b: <device-id>     # boundary_observation_info
  reporting_device: <device-id>   # boundary_observation_info
  src_port: <interface>   # boundary_observation_info
  proto: lldp|cdp|bgp|ospf|fdb|isis|mpls_te|configured   # boundary_observation_info
  reason: size_budget_exceeded|invalid_label_key|invalid_label_value|structural_invalid|stale_generation   # graph_updates_rejected_total
metrics:
  - {root: network_topology_federation_spoke_up, type: gauge, unit: bool, v: ok, note: "1 = spoke has pushed within liveness window; labels: job,instance,spoke_id"}
  - {root: network_topology_federation_spoke_last_push_timestamp_seconds, type: gauge, unit: unix_timestamp, v: ok, note: "labels: job,instance,spoke_id"}
  - {root: network_topology_hub_oos_unmatched_total, type: counter, unit: count, v: ok, note: "labels: job,instance only; out-of-scope edges from spokes with no matching hub boundary"}
  - {root: network_topology_graph_updates_rejected_total, type: counter, unit: count, v: ok, note: "labels: job,instance,reason; graph pushes rejected by the hub"}
  - {root: network_topology_boundary_observation_info, type: gauge, unit: info, v: ok, note: "value=1; one series per hub-visible inter-domain boundary adjacency; labels: job,instance,peer_a,peer_b,reporting_device,src_port,proto"}
```

> ⚠ `boundary_observation_info` uses device-ID labels (`peer_a`, `peer_b`,
> `reporting_device`). Device IDs are bounded and low-cardinality within a real deployment
> (not UUID-class) so these are safe as Mimir labels — see `[slug: cardinality]`.

### Spoke metrics

```yaml signals
family: network_topology_federation_spoke
scope: substrate
sink: promrw
labels:
  job: integrations/network-topology-exporter
  instance: <host:port>
  reason: superseded|shutdown    # federation_spoke_push_drops_total
metrics:
  - {root: network_topology_federation_spoke_push_failures_total, type: counter, unit: count, v: ok, note: "labels: job,instance only; HTTP push attempts that received a non-2xx or network error"}
  - {root: network_topology_federation_spoke_push_last_success_unix, type: gauge, unit: unix_timestamp, v: ok, note: "labels: job,instance only; unix epoch of last successful push to hub"}
  - {root: network_topology_federation_spoke_push_drops_total, type: counter, unit: count, v: ok, note: "labels: job,instance,reason(superseded|shutdown)"}
  - {root: network_topology_federation_spoke_push_queue_depth, type: gauge, unit: count, v: ok, note: "labels: job,instance only; pending snapshot push queue depth"}
```

---

## OTLP push accounting [slug: nettopo-otlp]

**GATED: active only when `otlp_output: true`** in the blueprint declaration.

Tracks the outcome of edge-event pushes to a downstream OTLP receiver (e.g. Grafana Alloy or
an OTLP collector).

```yaml signals
family: network_topology_otlp
scope: substrate
sink: promrw
labels:
  job: integrations/network-topology-exporter
  instance: <host:port>
  status: ok|error|dropped
  reason: n/a|timeout|tls_error|http_4xx|http_5xx|payload_rejected|network
metrics:
  - {root: network_topology_otlp_push_total, type: counter, unit: count, v: ok, note: "labels: job,instance,status,reason; reason=n/a when status=ok"}
```

> ⚠ `reason` is present on all rows, not only error rows — value is `"n/a"` for `status=ok`.
> This matches the exporter's label-completion contract (no absent dimensions).

---

## Topology change and conflict logs [slug: nettopo-logs]

Two Loki stream families: **topology change** events (one stream per tick when topology
mutations occur) and **topology conflict** events (one stream per conflict resolution cycle).

High-cardinality device names (`src_device`, `src_port`, `dst_device`, `dst_port`,
`direction`) ride in the **log body** and as Loki **structured metadata** — never as stream
labels (see `[slug: cardinality]`). Stream labels are bounded: `source`, `change_kind`,
`discovery_proto`, `job`, `instance` for changes; `source`, `conflict_type`, `job`, `instance`
for conflicts.

### Topology change stream

```yaml signals
family: nettopo_change_logs
scope: substrate
sink: loki
stream_labels:
  source: network-topology-exporter
  change_kind: added|removed|updated
  discovery_proto: lldp|cdp|bgp|ospf|fdb|isis|mpls_te|configured
  job: <job>
  instance: <host:port>
structured_metadata:
  src_device: <device-id>
  src_port: <interface>
  dst_device: <device-id>
  dst_port: <interface>
  direction: bidirectional|unidirectional
body_fields:
  format: logfmt
  level: info|warn      # "warn" for removed edges
  source: network-topology-exporter
  event: topology_change
  change_kind: added|removed|updated
  proto: <discovery_proto>
  src_device: <device-id>
  src_port: <interface>
  dst_device: <device-id>
  dst_port: <interface>
  direction: bidirectional|unidirectional
```

> ⚠ `change_kind` and `discovery_proto` in stream labels are set to the **dominant** (first
> protocol × "added") for the batch — all change lines from a tick share one stream regardless
> of their individual kind/proto. Query the body `change_kind=` and `proto=` fields for exact
> filtering per line.

### Topology conflict stream

```yaml signals
family: nettopo_conflict_logs
scope: substrate
sink: loki
stream_labels:
  source: network-topology-exporter
  conflict_type: neighbour_disagreement
  job: <job>
  instance: <host:port>
structured_metadata:
  src_device: <device-id>
  src_port: <interface>
body_fields:
  format: logfmt
  level: warn
  source: network-topology-exporter
  event: topology_conflict
  conflict_type: neighbour_disagreement
  src_device: <device-id>
  src_port: <interface>
  sources: <proto-list>   # e.g. "lldp,cdp" — the disagreeing discovery protocols
```

---

## Failure modes (AxisNetwork) [slug: nettopo-failure-modes]

*Added 2026-06-17. Sourced from `internal/construct/nettopo/failuremodes.go` +
`internal/construct/nettopo/health.go`.*

Five AxisNetwork failure modes are registered against the `network_topology` integration
construct. They are driven by **UNSCOPED incidents** (no `target:` field) — the fault applies
to the hub/standalone exporter instance for the blueprint. Per-instance or `network:*`
targeting is **not supported**: integration constructs are absent from the scenario
buildTargets index, so a named target or wildcard would silently no-op or error at load.

| mode | families driven |
|---|---|
| `nettopo_devices_unreachable` | `discovery_devices_total{status=failed,reason=unreachable}` row, `snmp_walks_total{status=timeout}` row |
| `nettopo_discovery_slow` | `discovery_cycle_duration_seconds` inflated, `cycle_budget_skips_total` increments, `graph_stale=1` forced |
| `nettopo_walker_degraded` | `walker_outcome_total{outcome=error/walker_drift}` for affected protocols, `module_last_status` ≥ 1, fault-only decode/degraded/hard_fail families emitted (`discovery_decode_issues_total`, `discovery_degraded_total`, `discovery_hard_fail_total`) |
| `nettopo_auth_failures` | `discovery_devices_total{status=failed,reason=auth_failed}` row, `credential_trials_total{status=failed}` row, `snmp_walks_total{status=timeout}` row |
| `nettopo_spoke_down` | (hub) `federation_spoke_up=0` + stale `last_push_timestamp` + `graph_updates_rejected_total{reason=stale_generation}` increments; (spoke) `federation_spoke_push_failures_total` increments |

**Fault-only families** (absent in healthy steady state, emitted ONLY when the fault is
active):

| family | fault trigger |
|---|---|
| `network_topology_discovery_decode_issues_total` | `nettopo_walker_degraded` |
| `network_topology_discovery_degraded_total` | `nettopo_walker_degraded` |
| `network_topology_discovery_hard_fail_total` | `nettopo_walker_degraded` |

> **Note — `network_topology_discovery_quarantined_rows_total`:** this family exists in the
> upstream exporter's signal surface (present in the discovery-health block above) but is
> **not currently synth-emitted**; it is intentionally absent from the fault-injection mapping
> above. Tracked as a known gap — add it here once the construct emits it.

**Incident YAML shape** (mirror the `every:`-interval ambient style used elsewhere):

```yaml
incidents:
  - { kind: nettopo_spoke_down,     every: 60m,  for: 5m,  intensity: 0.3 }
  - { kind: nettopo_discovery_slow, every: 120m, for: 8m,  intensity: 0.4 }
```

No `target:` field — leave unscoped so the mode applies to the blueprint's exporter instance.
Intensity scales the fraction of affected devices/modules (0.3 ≈ 30% of devices/walkers; at
any non-zero intensity the first sorted spoke is always taken down for `nettopo_spoke_down`).
