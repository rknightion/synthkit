# Synthetic Monitoring (fake SM checks) — ScopeSubstrate

Substrate-scoped: disambiguated by check identity (`job`/`instance`/`probe`/`config_version`/`check_name`);
never carries a `blueprint` label (SK-22 resolved). Global rules: see [`00-canon.md`](00-canon.md) — scoping
`[slug: scoping]`, cardinality `[slug: cardinality]`, shape rules `[slug: shape-rules]`.

*Provenance: predecessor extract 06 + `internal/canon/sm.go`. **Live-validated** against the Grafana Synthetic
Monitoring product stack (Rob) — `v: ok`, not assumed.*

---

## Synthetic Monitoring (fake SM checks) [slug: sm-checks]
*Provenance: predecessor extract 06 + `internal/canon/sm.go`, `internal/emit/sm.go`, `cmd/sm-provision/main.go`.*

Two cleanly-separated halves over `canon/sm.go`: a **one-shot control-plane provisioner**
(`cmd/sm-provision`) that registers an offline private probe + checks via the SM API (mandatory — the
SM app hard-guards on an empty check list, so metric injection alone does not light it up), and a
**per-tick data-plane** (`emit/sm.go`) that supplies ALL telemetry (no real probe ever runs).

**Offline probe (canon):** name (config-driven), region, lat/lon, derived geohash (precision 12).
Blueprint checks can supply paired `latitude`/`longitude`; omission retains Frankfurt
(50.1109, 8.6821). Registration and emission use the same resolved location. Checks sharing
a probe name must share region and coordinates. This adds configuration, not a signal family.
Registered private with no agent; never executes. The app shows it as `"<name> (offline)"`;
`probesUp` still reads 1/1 (driven by recent metric presence, not connection state).

**Check templates** (config-driven; `<workload>-health-{env}` pattern, one per enabled env →
`len(checks) × len(envs)`). Fields: `Type="http"`, `FrequencyMs`, `BaseLatency`, `AlertSensitivity`,
`{env}` = `strings.ToLower(env.Name)`, `Timeout = SMTimeoutSeconds(3.0)×1000` ms.

**Base label set (every series):** `job` (check Job), `instance` (check Target URL), `probe`
(probe name), `config_version` (⚠ stable join-key constant = `modified×1e9`; the SM app joins all
series + `sm_check_info` on `(instance,job,probe,config_version)` — any check edit changes `modified`→
`config_version`→ all series must be re-stamped, T1). User labels ride as `label_<k>` (e.g.
`label_test`).

**Series per check (60s):** `probe_success` (G 0/1; 0 under failure), `probe_duration_seconds` (G;
= `SMTimeoutSeconds` under failure), `probe_all_success_sum`/`_count` (Counters — a SUMMARY, no
buckets, separate `Add` calls — T3), `probe_all_duration_seconds_bucket{le}`/`_sum`/`_count`
(Histogram via `Observe` — ⚠ do NOT also `Add` `_sum`/`_count`, T2; buckets
`[0.005,0.01,0.025,0.05,0.1,0.25,0.5,1,2.5,5,10]`), `sm_check_info` (G=1; metadata anchor). `sm_check_info` extra labels:
`check_name`, `region`, `frequency` (decimal string), `geohash`, `alert_sensitivity` (the check's REAL
value — ⚠ independent of the provisioner's registered `"none"`, T4), `label_*`.

**Duration histogram profile:** HTTP and network checks target agent v0.66.1
([commit 1046e6ee](https://github.com/grafana/synthetic-monitoring-agent/blob/1046e6eeeb4f5b4ca7becc69a98325346e628c89/internal/scraper/scraper.go#L1455)).
Its shared `updateHistogramFromMetric` sets `Buckets: prometheus.DefBuckets` for all check types.
The [pinned dependency](https://github.com/grafana/synthetic-monitoring-agent/blob/1046e6eeeb4f5b4ca7becc69a98325346e628c89/go.mod)
is client_golang v1.24.1, whose [DefBuckets](https://github.com/prometheus/client_golang/blob/v1.24.1/prometheus/histogram.go#L271)
are `[0.005,0.01,0.025,0.05,0.1,0.25,0.5,1,2.5,5,10]` seconds, plus the cumulative `+Inf` bucket.
The predecessor contract recorded only `[0.1,0.25,0.5,1,2.5,5,10]`. That historical subset is
retained here as evidence of the previous synthetic contract, not established as an older agent
profile: no versioned upstream source or raw capture establishes why those four bounds were absent.
The corrected HTTP inventory adds `le="0.005"`, `"0.01"`, `"0.025"`, `"0.05"`; existing bounds,
labels, cumulative sum/count and registration hashes retain their meaning. This correction is
source-validated, not a new live capture, and does not rewrite historical capture data.

**Failure mode** `sm_probe_failure` (per-env): `probe_success→0`, `probe_duration_seconds` equals
the resolved check timeout (3.0s by default for HTTP/ping/DNS/TCP, 30s for traceroute), log
`msg="Check failed"`, stream `probe_success="0"`.

> ⚠ Provision-before-emit: the SM app clamps its native timeline to `check.created`; backfilled points
> before creation are hidden in SM's native views (raw metrics still in Explore). Always register
> first, then push forward-in-time data. synthkit's provisioner is a standalone `cmd/sm-provision` CLI
> (SK-21 resolved) reading config-driven check templates from blueprint YAML; the offline-probe identity
> is single-sourced through `sm.ResolveSpec`, with exported `sm.DefaultProbe*` constants for omitted fields.

```yaml signals
family: sm_checks
scope: substrate
sink: promrw
labels:
  job: <check-job>
  instance: <check-target-url>
  probe: <probe-name>
  config_version: <modified×1e9>   # ⚠ stable join-key; all series re-stamped on check edit (T1)
  label_*: <user-labels>           # e.g. label_test
metrics:
  - {root: probe_success, type: gauge, unit: bool, v: ok, note: "0 under failure"}
  - {root: probe_duration_seconds, type: gauge, unit: seconds, v: ok, note: "= SMTimeoutSeconds (3.0s) under failure"}
  - {root: probe_all_success_sum, type: counter, unit: count, v: ok, note: "SUMMARY — separate Add call (T3); no buckets"}
  - {root: probe_all_success_count, type: counter, unit: count, v: ok}
  - {root: probe_all_duration_seconds_bucket, type: histogram, unit: seconds, v: ok, note: "Observe only — do NOT also Add _sum/_count (T2); le buckets [0.005,0.01,0.025,0.05,0.1,0.25,0.5,1,2.5,5,10]"}
  - {root: probe_all_duration_seconds_sum, type: histogram, unit: seconds, v: ok}
  - {root: probe_all_duration_seconds_count, type: histogram, unit: count, v: ok}
  - {root: sm_check_info, type: gauge, unit: info, v: ok, note: "G=1; metadata anchor; extra labels below"}
info_series:
  name: sm_check_info
  extra_labels: [check_name, region, frequency, geohash, alert_sensitivity, "label_*"]
  note: "alert_sensitivity is the check's REAL value — ⚠ independent of the provisioner's registered 'none' (T4)"
```

## SM Loki logs [slug: sm-logs]

**Loki stream** (`{source="synthetic-monitoring-agent", check_name, instance, job, probe, region,
probe_success ("1"|"0" — ⚠ a STREAM label so failure queries `{…, probe_success="0"} | logfmt | msg=
"Check failed"` filter at stream speed), label_*}`; logfmt body with `msg="Check succeeded|failed"`,
`duration_seconds`).

```yaml signals
family: sm_logs
scope: substrate
sink: loki
stream_labels:
  source: synthetic-monitoring-agent
  check_name: <check-name>
  instance: <check-target-url>
  job: <check-job>
  probe: <probe-name>
  region: <probe-region>
  probe_success: '"1"|"0"'   # ⚠ STREAM label — failure queries filter at stream speed on probe_success="0"
  label_*: <user-labels>
body_fields:
  format: logfmt
  msg: '"Check succeeded"|"Check failed"'
  duration_seconds: <float>
```

## Network checks [slug: sm-network-checks]

Provenance: Grafana synthetic-monitoring-agent v0.66.1
[1046e6eeeb4f5b4ca7becc69a98325346e628c89](https://github.com/grafana/synthetic-monitoring-agent/tree/1046e6eeeb4f5b4ca7becc69a98325346e628c89).
Its [basic fixtures](https://github.com/grafana/synthetic-monitoring-agent/tree/1046e6eeeb4f5b4ca7becc69a98325346e628c89/internal/scraper/testdata)
source the following gauges and phase labels. These are source-validated, not a live capture.
`type` selects ping, dns, tcp or traceroute; omitted remains HTTP. DNS target is the
query hostname, while `dns.server` is the resolver. TCP target is host:port;
`tcp.tls` enables certificate metadata. Ping uses three packets per execution;
its packet-count gauges are not cumulative counters. Traceroute defaults to a
120000 ms frequency and the API-required 30000 ms timeout. Other new types default
60000/3000 ms. Network checks retain the common SM base/info/log labels and
success summary and the common agent v0.66.1 duration histogram profile documented above.

For DNS, `probe_ip_addr_hash` hashes the resolver address, not the query result:
the [adapter passes the server to ProbeDNS](https://github.com/grafana/synthetic-monitoring-agent/blob/1046e6eeeb4f5b4ca7becc69a98325346e628c89/internal/prober/dns/dns.go#L81),
whose [resolver lookup](https://github.com/grafana/synthetic-monitoring-agent/blob/1046e6eeeb4f5b4ca7becc69a98325346e628c89/internal/prober/dns/internal/bbe/prober/dns.go#L210)
records that address through `chooseProtocol`.

```yaml signals
family: sm_network_checks
scope: substrate
sink: promrw
labels:
  job: <check-job>
  instance: <original-target>
  probe: <probe-name>
  config_version: <modified-nanoseconds>
  label_*: <user-labels>
  phase: <resolve|setup|rtt|connect|request>
  version: <TLS-version>
  fingerprint_sha256: <leaf-fingerprint>
  subject: <leaf-subject>
  issuer: <leaf-issuer>
  subjectalternative: <leaf-DNS-names>
  serialnumber: <leaf-serial>
metrics:
  - {root: probe_dns_lookup_time_seconds, type: gauge, unit: seconds, v: ok, note: "ping/DNS/TCP resolver lookup"}
  - {root: probe_ip_protocol, type: gauge, unit: info, v: ok, note: "V4 profile: value 4"}
  - {root: probe_ip_addr_hash, type: gauge, unit: info, v: ok, note: "FNV-1a of resolved IP bytes"}
  - {root: probe_icmp_duration_seconds, type: gauge, unit: seconds, v: ok, note: "phase=resolve|setup|rtt"}
  - {root: probe_icmp_duration_rtt_min_seconds, type: gauge, unit: seconds, v: ok}
  - {root: probe_icmp_duration_rtt_max_seconds, type: gauge, unit: seconds, v: ok}
  - {root: probe_icmp_duration_rtt_stddev_seconds, type: gauge, unit: seconds, v: ok}
  - {root: probe_icmp_packets_sent_count, type: gauge, unit: count, v: ok}
  - {root: probe_icmp_packets_received_count, type: gauge, unit: count, v: ok}
  - {root: probe_icmp_reply_hop_limit, type: gauge, unit: count, v: ok}
  - {root: probe_dns_duration_seconds, type: gauge, unit: seconds, v: ok, note: "phase=resolve|connect|request"}
  - {root: probe_dns_answer_rrs, type: gauge, unit: count, v: ok}
  - {root: probe_dns_authority_rrs, type: gauge, unit: count, v: ok}
  - {root: probe_dns_additional_rrs, type: gauge, unit: count, v: ok}
  - {root: probe_dns_query_succeeded, type: gauge, unit: bool, v: ok}
  - {root: probe_failed_due_to_regex, type: gauge, unit: bool, v: ok}
  - {root: probe_failed_due_to_bytes, type: gauge, unit: bool, v: ok}
  - {root: probe_tls_version_info, type: gauge, unit: info, v: ok, note: "successful TLS only; version=TLS 1.3"}
  - {root: probe_ssl_earliest_cert_expiry, type: gauge, unit: timestamp, v: ok}
  - {root: probe_ssl_last_chain_expiry_timestamp_seconds, type: gauge, unit: timestamp, v: ok}
  - {root: probe_ssl_last_chain_info, type: gauge, unit: info, v: ok, note: "fingerprint_sha256,subject,issuer,subjectalternative,serialnumber"}
  - {root: probe_traceroute_total_hops, type: gauge, unit: count, v: ok}
  - {root: probe_traceroute_route_hash, type: gauge, unit: info, v: ok, note: "FNV-1 of Hosts concatenated in TTL order"}
  - {root: probe_traceroute_packet_loss_percent, type: gauge, unit: ratio, v: ok, note: "SUM lost / SUM sent: 0-1 fraction despite name"}
```

[TCP TLS provenance](https://github.com/prometheus/blackbox_exporter/blob/5a059bee8d8ffa4e75947c5055fb0abeefc582e6/prober/query_response.go).
Plaintext TCP omits TLS metadata; DNS A queries omit SOA serial. Synthetic profiles
use documentation-range IPv4 addresses, three hops and five sends per hop; these
are modelling assumptions, not claims about an actual network or hardware.

Traceroute adds one logfmt body per hop:
`Level=info Destination=<target> Hosts=<IP> TTL=<integer> ElapsedTime=<Go duration> LossPercent=<0-100> Sent=5 TracerouteID=<execution UUID>`.
All hops share one body-only execution UUID; no host/hop/UUID metric or stream label.
The [agent call](https://github.com/grafana/synthetic-monitoring-agent/blob/1046e6eeeb4f5b4ca7becc69a98325346e628c89/internal/prober/traceroute/traceroute.go#L128)
and [MTR Loss](https://github.com/grafana/mtr/blob/a9806fdda16646e637ff216c23a24698dcaf2844/pkg/hop/hop.go#L99)
confirm exact case and the hop percentage versus aggregate fraction distinction.
Common terminal success/failure logs retain the selected check_name.
