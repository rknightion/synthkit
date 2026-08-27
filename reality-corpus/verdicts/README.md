# Coverage-gap verdicts

A coverage gap is an `extra_metric` finding from the report-only signal-fidelity check
(`make signal-fidelity`): a metric family a real collector shipped that synthkit does not
emit at all. This directory records a decided verdict for every one of them, so a reader
can tell a genuine emission hole from a family synthkit deliberately does not model.

The machine-readable record is [`coverage-verdicts.json`](./coverage-verdicts.json). This
file is the summary a human reads.

Nothing here is wired into the comparator. The record is a standalone document that a later
change may consume; see [Consuming the record](#consuming-the-record).

## Verdicts

| Verdict | Meaning |
| --- | --- |
| `should_emit` | A real deployment of something synthkit already claims to model produces this family. Its absence is a hole in a claim the project makes. |
| `out_of_scope` | Reality produces it, but it belongs to a component synthkit does not claim to model. The rationale records *why*, so the next audit does not re-litigate it. |
| `unresolved` | Cannot be decided from the corpus and available documentation. Carries a `cantfind.md` PENDING id instead of a guessed verdict. |

A family is never verdicted from its name. Every entry cites evidence: the corpus document
and its capture date, plus repository source or vendor documentation where the decision
turns on those.

Each entry splits the captured label keys into `observed.emission_label_keys` and
`observed.read_path_label_keys`, so an implementer working from a `should_emit` verdict does
not emit read-path enrichment. The split follows the corpus document's own
`source.enrichment_labels` declaration.

## Triage of 2026-08-27

Measured against `make signal-fidelity` on `main`, 2026-08-27: **367** `extra_metric`
findings, which fold to **77** distinct metric families.

**15 of the 367 findings are not coverage gaps at all.** They are five kube-proxy histogram
families synthkit demonstrably emits; the finding is a producer asymmetry, not missing
emission. See [Not coverage gaps](#not-coverage-gaps). The real coverage-gap population is
**72 families / 352 findings**.

| Group | Owner | Signals | Families | should emit | out of scope | unresolved | Findings |
| --- | --- | --- | ---: | ---: | ---: | ---: | ---: |
| `cwinfra/AWS-Firehose` | `internal/construct/cwinfra` | `signals/cw.md` `[slug: cw-firehose]` | 27 | 20 | 0 | 7 | 135 |
| `cwinfra/AWS-ApplicationELB` | `internal/construct/cwinfra` | `signals/cw.md` `[slug: cw-alb]` | 17 | 10 | 0 | 7 | 85 |
| `ec2/AWS-EC2` | `internal/construct/ec2` | `signals/cw.md` `[slug: cw-ec2]` | 11 | 11 | 0 | 0 | 55 |
| `cwinfra/AWS-NATGateway` | `internal/construct/cwinfra` | `signals/cw.md` `[slug: cw-natgw]` | 9 | 9 | 0 | 0 | 45 |
| `cwinfra/AWS-EBS` | `internal/construct/cwinfra` | `signals/cw.md` `[slug: cw-ebs]` | 6 | 6 | 0 | 0 | 30 |
| `k8scluster/kubelet-storage` | `internal/construct/k8scluster` | `signals/k8s.md` | 1 | 1 | 0 | 0 | 1 |
| `coredns/hosts-plugin` | `internal/construct/coredns` | `signals/k8s-addons.md` | 1 | 0 | 1 | 0 | 1 |
| **Total** | | | **72** | **57** | **1** | **14** | **352** |

Every `should_emit` family sits in a group whose resource type synthkit already models and
already emits sibling roots for, under dimensions the contract in `signals/cw.md` (or
`signals/k8s.md`) already documents. There is no group here that synthkit models by accident.

### Corpus triaged

| Area | Source | Substrate | Collector | Captured |
| --- | --- | --- | --- | --- |
| `cw` | `gcx_live_readback` | `eks` | `grafana/gcx` 1.1.1 | 2026-08-25 |
| `k8s` | `gcx_live_readback` | `eks` | `grafana/gcx` 1.1.1 | 2026-08-25 |
| `k8s` | `k3d_lab` | `k3s` | `grafana/k8s-monitoring` 4.4.0 | 2026-08-25 |
| `k8s-addons` | `k3d_lab` | `k3s` | `grafana/k8s-monitoring` 4.4.0 | 2026-08-25 |

A `(area, substrate)` pair absent from this table has not been triaged. A gap in an
untriaged pair is untriaged, not implicitly out of scope.

## Should emit, by group

`cwinfra/AWS-NATGateway` — 9 families. synthkit emits **one of the four directional byte
counters** (`bytes_out_to_destination`) and **none of the four packet counters**, so any
ingress/egress split or drop-ratio view over the modelled estate is arithmetically
incomplete. Missing: `bytes_in_from_source`, `bytes_in_from_destination`,
`bytes_out_to_source`, `packets_in_from_source`, `packets_in_from_destination`,
`packets_out_to_source`, `packets_out_to_destination`, `peak_bytes_per_second`,
`peak_packets_per_second`. One dimension (`dimension_NatGatewayId`), all five stat suffixes.

`ec2/AWS-EC2` — 11 families. `network_packets_in` / `network_packets_out` are the packet
counterparts of the byte counters synthkit already emits. The burstable credit family
(`cpucredit_usage`, `cpusurplus_credit_balance`, `cpusurplus_credits_charged`) completes the
`cpucredit_balance` root already emitted. `ebsbyte_balance_percent`, `ebsiobalance_percent`
and the two `instance_ebs*_check` roots complete the instance-level EBS view;
`metadata_no_token` / `metadata_no_token_rejected` are the IMDS counters. Note
`instance_ebsiopsexceeded_check`, `instance_ebsthroughput_exceeded_check` and
`metadata_no_token_rejected` were captured with `dimension_InstanceId` only, with no
ASG-level rollup observed.

`cwinfra/AWS-ApplicationELB` — 10 families. `request_count_per_target` and
`httpcode_elb_3_xx_count` / `httpcode_elb_4_xx_count` are the target-level and ELB-side
response views missing from a namespace synthkit otherwise covers well.
`client_tlsnegotiation_error_count`, `desync_mitigation_mode_non_compliant_request_count`,
`http_redirect_count`, `http_fixed_response_count`, `rule_evaluations`, `consumed_lcus`,
`peak_lcus` complete it.

Captured names carry a naming trap worth recording in `signals/cw.md`: the newer roots
mangle as `unhealthy_state_dns` / `unhealthy_state_routing`, **not** `un_healthy_*`, while
the existing `un_healthy_host_count` trap still holds. Both spellings are real and coexist
in one namespace.

`cwinfra/AWS-Firehose` — 20 families. `signals/cw.md` already records that only two roots
are emitted and that the namespace has more. The four
`delivery_to_http_endpoint_{bytes,records,processed_bytes,processed_records}` roots are the
same delivery path synthkit already models and are the strongest of the group. The source
side (`incoming_*`, `put_record_*`, `put_record_batch_*`, `describe_delivery_stream_*`,
`throttled_records`) and the three `*_limit` roots are absent entirely.

`cwinfra/AWS-EBS` — 6 families. `volume_avg_iops`, `volume_avg_throughput`,
`volume_idle_time` and the three status checks (`volume_iopsexceeded_check`,
`volume_stalled_iocheck`, `volume_throughput_exceeded_check`). Dimensions are
`dimension_VolumeId` co-labelled with `dimension_InstanceId`, which `signals/cw.md` already
documents as live-verified.

`k8scluster/kubelet-storage` — 1 family, and a shape defect rather than a plain absence.
The real kubelet publishes `storage_operation_duration_seconds` as a classic histogram
(corpus `instrument_types: ["histogram"]`, `histogram.classic: true`). synthkit publishes a
standalone **counter** named `storage_operation_duration_seconds_count`, so the `_bucket`
and `_sum` components do not exist and no quantile can be computed. Same resource, same
scrape, same label set (`operation_name`, `status`, `volume_plugin`, `migrated`, `node`).

## Out of scope

`coredns_hosts_entries` — the CoreDNS `hosts` plugin exports it, and the corpus shows the
plugin loaded against `/etc/coredns/NodeHosts`, a k3s-specific Corefile. Corpus authority is
`k3s` only; no EKS-substrate evidence covers CoreDNS. synthkit models a Corefile with no
`hosts` block, a decision already recorded in `internal/construct/coredns/coredns.go`, where
`coredns_hosts_reload_timestamp_seconds` is pinned at 0 with the comment "hosts plugin not
used". Do not re-litigate without EKS-substrate evidence that the modelled Corefile loads
the plugin.

## Unresolved

14 families, four proposed `cantfind.md` PENDING ids. Each is withheld because emitting the
family unconditionally could be wrong, not because the name is in doubt — the names come
from a live capture and are authoritative.

| PENDING | Families | What is undecided |
| --- | --- | --- |
| `SK-92` | `aws_applicationelb_{anomalous_host_count,mitigated_host_count,healthy_state_dns,healthy_state_routing,unhealthy_state_dns,unhealthy_state_routing}` | Whether a default-configured load balancer publishes these continuously, or only while an optional target-health or zonal capability is active. |
| `SK-93` | `aws_applicationelb_capacity_utilization` | Its unit, and whether a load balancer with no capacity reservation publishes it. Siblings `consumed_lcus` / `peak_lcus` are verdicted `should_emit`. |
| `SK-94` | `aws_firehose_{kmskey_access_denied,kmskey_disabled,kmskey_invalid_state,kmskey_not_found,secrets_manager_access_denied_exception}` | Whether a stream publishes these continuously at zero or only on an error event. A continuous synthetic zero would fabricate a steady state that does not exist if the real metric is event-only. |
| `SK-95` | `aws_firehose_failed_validation_{bytes,records}` | These carry `dimension_SourcePartitionId`, absent from the `signals/cw.md` AWS/Firehose contract and corresponding to a stream capability synthkit does not model. |

In every case the corpus records the value set as elided, so the capture itself carries no
value evidence, and the documentation retrieved when the verdict was decided did not settle
the publication condition.

## Not coverage gaps

Five families appear as 15 `extra_metric` findings but are emitted by synthkit today:

```
kubeproxy_conntrack_reconciler_sync_duration_seconds
kubeproxy_network_programming_duration_seconds
kubeproxy_sync_full_proxy_rules_duration_seconds
kubeproxy_sync_partial_proxy_rules_duration_seconds
kubeproxy_sync_proxy_rules_duration_seconds
```

The `gcx_live_readback` producer records raw Prometheus classic-histogram component series
(`_bucket`, `_count`, `_sum`) as three separate metric entries. `internal/inventory/synth.go`
and `e2e/inventory/inventory.go` both fold those components into the histogram family base,
so the synth side and the k3d lab side agree with each other and disagree with the read-back
producer. Folding component suffixes in the read-back producer removes all 15 findings and
changes no verdict in this record, because the record is keyed on the folded family name.

## How the record survives a corpus refresh

The key is `(area, substrate, family)`, and `family` is the **folded** family name:
CloudWatch five-stat suffixes and Prometheus classic-histogram component suffixes are
stripped before lookup, matching the fold in `internal/inventory/synth.go`.

That gives four properties:

- A refresh that adds or drops individual stat suffixes or histogram components resolves to
  the same key, so the verdict still applies.
- A refresh that changes `captured_on` does not invalidate anything. The capture date is
  recorded inside each entry's `evidence` as the date the verdict was decided against, not
  as part of the key.
- Fixing the read-back producer's suffix folding renames those series but not their folded
  family, so the record is already correct for the fixed producer.
- A `should_emit` verdict is self-retiring. Once synthkit emits the family the comparator
  stops reporting it and the entry becomes inert; no deletion pass is needed.

What a refresh *can* legitimately produce is a **new** family with no verdict. That is the
one thing the record must not hide, which is what the untriaged bucket below is for.

## Consuming the record

Not wired. The proposal, for whoever changes the report step:

1. Load `reality-corpus/verdicts/coverage-verdicts.json`.
2. For each `extra_metric` finding, fold the signal name to its family with the same fold as
   `internal/inventory/synth.go`, then look up `(area, substrate, family)`.
3. Route by verdict:
   - `out_of_scope` — suppress from the report body, keep a one-line tally.
   - `should_emit` — keep, grouped by `owner.package`, annotated with `group`.
   - `unresolved` — keep, annotated with `cantfind_pending`.
   - **no entry** — keep, and print under an `UNTRIAGED` heading.
4. Coverage gaps stay report-only in every branch. Only contradictions fail CI.

Step 3's untriaged branch is what keeps the record honest: a refreshed corpus that adds
families surfaces them as untriaged rather than quietly passing.

## Maintaining the record

Add an entry when a refresh surfaces an untriaged family. Change an existing entry only on
new evidence, and say what the evidence is. `decided_on` is the date the verdict was taken;
`evidence[].captured_on` is the date of the capture it was taken against. Both are kept so a
later reader can see whether a verdict predates the evidence it now sits on.
