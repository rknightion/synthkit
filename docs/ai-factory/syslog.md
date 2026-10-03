---
title: Syslog received-record shapes
description: Shared synthetic Loki and OTel syslog rendering, bounded identity and receiver health.
---

# Syslog received-record shapes

`internal/syslog` is a shared mechanic for consumers modeling appliances that
send syslog. It renders **decoded synthetic RFC5424/RFC3164 facts** and explicit
source-decoder failures through the existing Loki or OTLP log writer. It is not a
network receiver or raw RFC parser. No new runtime construct, listener, transport
implementation or physical device inventory is added here.

The source contract is Alloy **v1.20.1**, its Contrib **v0.161.0** parser and
Collector receiverhelper **v1.67.0**. Names, fields and URLs are catalogued in
[syslog log shapes](../../signals/logs.md#shared-received-syslog-renderer-slug-logs-syslog)
and [receiver health](../../signals/fm.md#opt-in-syslog-receiver-health-slug-fm-syslog-health).

## Blueprint selection and health

The existing cluster addon can select the receiver profile per blueprint. This
selects **health only**, not device logs:

```yaml
addons:
  - name: alloy_health
    syslog:
      receiver: otel # or loki, which does not require receiver_id
      receiver_id: syslog
    # Operator assumption: a quiet management appliance estate produces one
    # routine state/audit record per minute in aggregate, across both HA pods.
    syslog_records_per_min: 1
```

Omit `syslog` for unchanged existing addon output. Healthy intake defaults to
zero if the rate is absent. Enabled profiles add **6 series per cluster**, two
existing HA scrape identities times three cumulative counters. Traffic is split
evenly and follows the cluster's business-hours shape, with no forced floor.
The fixture `e2e/fixtures/ai-factory-syslog.yaml` exercises the public loader and
runner. Both Loki and OTel profiles use the same topology.

| Profile | Counters per HA target | Error meaning |
|---|---|---|
| Loki | entries, parsing_errors, empty_messages | Decoder failure drops and increments parsing errors |
| OTel | accepted_log_records, refused_log_records, failed_log_records | Actual downstream handoff; default gate off maps errors to refused and leaves failed zero |

Loki instruments are unlabelled; the existing five cluster/namespace/job/instance
scrape labels are separate. OTel adds bounded receiver ID and omits the adapter's
empty transport attribute. Feature-gated receiver requests and TCP listener
connection metrics are not emitted by this decoded renderer.

## Consumer rendering contract

Owning device consumers select `syslog.Config` within their own configuration and
supply synthetic `Message` facts. Device integrations outside this lane remain
parked. The health addon must not be used as a parallel device emitter.

- Loki defaults to message text. Its five optional stream assignments (`site`,
  `device`, `source_type`, `severity`, `service`) are fixed operator configuration,
  not sender header extraction. Operators map declared bounded inventory to those
  assignments. Absent assignments are omitted.
- Sender headers, PID, message ID and structured data are not indexed. Opt-in
  `use_rfc5424_message` preserves them inside the full RFC5424 body. Default Loki
  removal of internal syslog labels intentionally does not retain those headers.
- Connection IP is distinct from sender hostname. `preserve_connection_ip` is an
  explicit Loki operator mapping of the sourced internal
  `__syslog_connection_ip_address` key into structured metadata, never a stream
  label. It is not Alloy's default output. Addresses must be generic or derived
  from fixture topology, never copied from a hardware capture.
- OTel preserves the original line as body and parsed facts as RECORD attributes;
  RFC5424 structured data stays nested. Timestamp and severity are promoted to
  their log-record fields. It adds no default resource attributes. Optional
  `add_attributes` retains a supplied connection IP as `net.peer.ip`; false by
  default. No source ports or hostnames are inferred.
- `ParseError` is a source decoder failure, not a downstream writer failure.
  Default OTel `on_error: send` forwards the unparsed original line, returns the
  decoder error and counts it accepted when handoff succeeds. `send_quiet`
  suppresses only decoder errors. `drop` and `drop_quiet` do not hand off. There
  is **no OTel parse-failure counter**. All modes preserve real writer errors.
- Loki drops transport-empty messages and RFC3164 empty MSG. RFC5424 empty MSG
  increments the empty counter and forwards only with `rfc5424_allow_empty_msg`.
  Successful writes increment entries, not failed writes.

## What was exercised

Public blueprint loading and runner ticks prove profile selection, six-series
inventory, disabled behavior and cumulative healthy counters. Local tests run
pinned vendor RFC examples through the real Loki JSON/gzip and OTLP protobuf/gzip
sinks to loopback HTTP servers, verifying body, attributes, nested structured
data, promoted severity and downstream failure behavior. Dry-run captures prove
bounded labels and optional source-IP retention. These are renderer and sink
wire proofs, **not** raw parser fidelity, live listener behavior or CLI device
log emission. No live stack is used.
