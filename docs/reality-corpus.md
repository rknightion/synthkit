---
title: Reality-corpus refresh
description: How to refresh the credential-free reality corpus and review real signal drift versus capture noise.
---

# Reality-corpus refresh and review

The reality corpus is the committed, credential-free evidence used by the
signal-fidelity check. An unexempted contradiction fails that check; an
explicit exemption remains visible in the Contradictions section, and a
coverage gap is report-only. This page describes how to refresh the corpus and
how to decide whether a candidate records real signal drift or only a different
capture sample. The on-disk contract is defined in the
[reality-corpus README](https://github.com/rknightion/synthkit/blob/main/reality-corpus/README.md); this page is the
operating procedure built on that contract.

The contract is deliberately narrow:

- The version is `synthkit.telemetry.reality-corpus/v1alpha1`.
- Each document is one `signals/<area>.md` area and one generic producer at
  `reality-corpus/<signals-area>/<source-id>.json`. The loader reads only the
  corpus root and per-area subdirectories, so a sibling directory holding a
  different record kind is skipped rather than parsed as a corpus document.
- The `source` provenance and `authority.substrates` apply to every observation
  in that document.
- Documents from different substrates are never unioned before `Diff`.
- Capture counts and receipts explain how an observation was collected; they
  are not signal-contract evidence.
- Explicit contradiction decisions live in a separate versioned exemption
  document. They are review records, not changes to captured reality.

Use the [signal catalogue](https://github.com/rknightion/synthkit/blob/main/SIGNALS.md), its
[cross-cutting canon](https://github.com/rknightion/synthkit/blob/main/signals/00-canon.md), and the
[signal-area index](signal-areas.md) to identify the owning area and interpret
an observed shape. The Kubernetes and CloudWatch catalogues are examples of
area-level authorities: [Kubernetes](https://github.com/rknightion/synthkit/blob/main/signals/k8s.md) and
[CloudWatch](https://github.com/rknightion/synthkit/blob/main/signals/cw.md).

## The two producers

There are two allowed ways to obtain a refresh candidate. They produce
different evidence and must remain distinguishable in the envelope.

| Producer | What it does | Authority and safety boundary |
| --- | --- | --- |
| Credential-free k3d capture | Runs the disposable k3d capture lab and records collector-egress inventory. Its provenance uses the generic `k3d_lab` producer kind and the `k3s` substrate. | Root-owned and exclusive. It needs no service credentials. It may establish k3s-scoped shapes, but it cannot establish claims about another substrate. |
| Operator-selected live read-back | Reads the operator-selected live source through read-only queries and records the resulting inventory. Its provenance uses the generic `gcx_live_readback` producer kind and the substrate selected for that read-back. | Root-owned and exclusive. The operator chooses the target before the read-back; queries must not mutate the source. Credentials are used only by the root operation, never copied into a candidate, corpus document, or this page. |

The producer kind, substrate, collector name, collector version, and capture date
are part of the evidence. A source ID is a generic producer name, not a stack,
account, tenant, cluster, or other deployment identifier. The corpus must not
contain private deployment details.

In particular, k3s and EKS evidence are not interchangeable. Evidence from k3s
cannot correct an EKS-scoped claim, and evidence from EKS cannot correct a
k3s-scoped claim. Keep the documents separate and let `authority.substrates`
limit which synthetic claims each document may contradict.

## Cross-substrate findings

The comparator never merges evidence across substrates. A finding originates
from one document and names that document's substrate. When another captured
substrate contains the same signal and has no difference for the same field,
the finding reports it as `matching evidence on substrate(s)`. When a captured
substrate did not contain the signal, it reports `absent evidence on
substrate(s)`. Absence is not agreement and it never changes a contradiction
into a coverage gap or an exemption.

For example, the clean GCP capture observed
`label_cloud_google_com_gke_nodepool` on `kube_node_labels`, while the EKS
corpus observed `label_eks_amazonaws_com_nodegroup` on that family. This is a
recorded cross-cloud divergence: a synthkit claim that matches EKS but
contradicts the GCP label field names both substrates. It is not proof that
either capture is defective, and a short capture that omitted
`kube_node_labels` on a third substrate supplies no verdict at all. A synthkit
defect is established only by the evidence for the substrate to which the
finding applies; matching evidence elsewhere neither suppresses nor broadens
it.

`reality-corpus/gcp/rksy-gcp.capture.json` records the provenance of the first
clean GCP source capture. It is deliberately a capture-provenance record rather
than a comparator document: its metrics and logs still need area-by-area,
identity-safe normalization into the inventory envelope, while its profiles
and traces were status-only `unavailable` observations and therefore are
absent evidence, not negative evidence. The mixed AWS/Azure candidate is not
represented in the corpus and remains pending a clean recapture under SKT-0042.
Resume GCP normalization only when the root assigns a privacy-safe mapper that
splits the source-capture metrics and logs into reviewed per-area
`CorpusDocument` candidates, elides deployment-selected values, and validates
each accepted document through the normal corpus loader.

### EKS live read-back command

The EKS producer is manual and credentialed. The root must receive the target
selection from the operator and pass it explicitly; the target rejects the
former task-runner's unrelated default context rather than querying it:

```bash
just corpus-gcx <operator-selected-context>
```

`GCX_SINCE` optionally changes the bounded lookback from its `24h` default.
The command first runs `gcx config check --context <selected>` and stops with a
clear error if the named context, its credential, connectivity, or configured
Prometheus datasource is unavailable. It then uses only the read-only
`gcx metrics series` endpoint. It does not change the active context and never
calls a create, update, push, delete, apply, or other mutation command.

The query scope is deliberately explicit:

- EKS node and pod identity: `kube_node_info`, `kube_node_labels`,
  `kube_pod_info`, and `kube_pod_labels`, limited to clusters proven EKS by an
  `aws://` provider-ID observation and an EKS-marked kubelet version.
- EKS add-ons: `awscni_*`, `kubeproxy_*`, and kube-proxy's
  `kubernetes_build_info` series.
- Core CloudWatch catalogue families from `signals/cw.md`, including observed
  `_sum`, `_average`, `_maximum`, `_minimum`, and `_sample_count` names.

Bedrock, AppFlow, and every other AI/LLM family are excluded by construction.
The command writes only the generic paths `k8s/eks-live-readback.json`,
`k8s-addons/eks-live-readback.json`, and `cw/eks-live-readback.json`. Stable
enum, instance-type, and topology values are retained; stack, account, tenant,
cluster, resource, pod, node, IP, UID, ARN, and other deployment identities are
stored as key presence with sticky `values_elided: true`. CloudWatch `tag_*`
label keys are omitted because a tag name can itself contain a live deployment
identity, which the frozen schema cannot safely elide. The selected context is
never written to a document or printed in the report.

## Safe refresh sequence

The accepted corpus is never edited in place from an unreviewed capture. Build
a candidate, compare a temporary normalized result, then have the root accept
and commit the reviewed per-area files.

1. **Choose one producer and freeze its scope.** Record the source kind,
   substrate, collector/chart and version, and the configuration that produced
   the candidate. For k3d, the root owns the disposable lab operation. For live
   read-back, the root obtains the operator's source selection and performs only
   read-only queries. Do not mix producer paths or substrates in one refresh.

2. **Create a capture candidate.** Retain the inventory output and its
   provenance outside the committed corpus while it is being reviewed. Check
   that it declares `synthkit.telemetry.inventory/v1alpha1`, has the required
   signal-class arrays, and carries source provenance for the observations. Keep
   `capture_volume` and `inventory.receipts` as provenance only. Remove any
   credential material or private deployment detail before the candidate is
   shown to a reviewer.

3. **Split the candidate by area.** For every covered area, make a working
   document at
   `reality-corpus/<signals-area>/<source-id>.json`. The area is the basename of
   the owning file under `signals/` without `.md`; the source ID is generic.
   Each working document contains exactly one area and one producer. Do not put
   a full-capture document in the corpus, and do not combine observations from
   different substrates or producers merely because their field names look
   similar.

4. **Find the matching baseline.** A cumulative merge is allowed only when the
   path, producer, substrate, collector/chart, version, and relevant
   configuration describe the same source. Compare the working document with
   the existing document without overwriting it. A substrate change or
   collector-version change is a review boundary, not an ordinary refresh; stop
   and use the reviewer decisions below.

5. **Normalize repeated measurements.** If the candidate came from multiple
   runs of the same source/configuration, first project each run to structural
   inventory and form their cumulative union. Sort set-valued fields. Preserve
   stable evidence, but do not let run count, receipt count, timestamps, dynamic
   exemplars, or a transient sample decide whether a shape exists.

6. **Cumulatively merge with the baseline.** Merge the normalized candidate
   into a temporary copy of the matching document. The identity used for each
   signal class is:

   | Signal class | Identity for the union |
   | --- | --- |
   | Metric | Metric name |
   | Log | Transport plus stream-label and structured-metadata shape |
   | Trace | Service |
   | Profile | Profile type |
   | Sigil | Ingest kind |

   Union structural fields and sort them. Attribute keys are sticky: once a
   real key has been observed for an identity, a later subset does not remove
   it. Dynamic log-source exemplars are provenance, not family identity. A
   missing observation never deletes an established shape.

7. **Compare before accepting.** Compare the current document with the
   temporary merged result and classify every difference using the reviewer
   table below. Then run the fidelity comparison against the
   synthetic inventory. Inspect the value-bearing canonical inventory with
   `DRY_RUN=true BLUEPRINT_NAMES='*' go run ./cmd/synthkit -once -inventory-json`.
   The `-dump` path is label-key-only structural output and can help inspect
   names and keys, but it is not a substitute when values are under review. The
   corpus comparison is scoped to the families covered by each corpus document.
   A family absent from a document is not thereby a coverage gap for that
   document.

8. **Review the candidate, not just the diff count.** Confirm that any proposed
   addition is supported by the same substrate and configuration, that stable
   values are genuinely stable, and that no removal or narrowing was inferred
   from absence. The report is evidence-only: a finding does not authorize a
   corpus edit, a synthetic-code edit, or a deployment change. Once the corpus
   and exemption document load and validate successfully, findings fail the
   command only when an unexempted contradiction remains. Malformed input or a
   stale, overlapping, or count-mismatched exemption rule also fails closed.

9. **Commit only the accepted merge.** Update `captured_on` only when the
   reviewer accepts new structural evidence or confirmed stable-value evidence.
   Keep provenance-only count or receipt changes from changing the date. The
   root stages explicit accepted per-area paths, runs the repository's final
   documentation and fidelity checks, and owns the commit and push. An
   unreviewed candidate remains outside the committed corpus.

## Evidence rules the comparator applies

The comparator answers one question: does the corpus contain evidence that
contradicts a synthetic claim? Absent evidence is never a contradiction. A field
a producer could not observe is a coverage gap that routes to a PENDING, and the
fix is to make the producer observe it, not to exempt the field permanently. A
corpus producer must therefore understand what its output means:

- **Instrument type.** A metric whose `instrument_types` is exactly
  `["unknown"]` records that the producer could not observe an instrument shape.
  It yields a visible `unknown_instrument_evidence` coverage gap and PENDING
  stub, never an `instrument_mismatch` or contradiction. The report says that
  the corpus does not know; it does not silently filter the family. Any entry
  recording a real type contradicts normally when synth disagrees, including a
  set that mixes a real type with the sentinel. A producer that learns to read
  instrument types therefore turns absent evidence into real verdicts without
  any comparator change. This is the SKT-0021.01 decision: use absent evidence
  because the producer did not observe a type; inferring one from a metric-name
  suffix would invent evidence rather than enrich the corpus.
- **Label keys.** A key synthkit emits that the reality view does not carry is a
  contradiction; that is the never-invent-a-name rule and it is not relaxed. A
  key reality carries that synthkit does not emit is a coverage gap.
- **Folded producer families.** A corpus family that combines several jobs cannot
  establish the absence of a job-specific key. `kubernetes_build_info` currently
  folds kubelet and kube-proxy, so its `source` difference is a coverage gap that
  records the corpus modelling limit, not an exemption or a synth defect.
- **Read-path enrichment labels.** A label a producer's read path adds after
  collector egress is not evidence about the emitted shape. Declare it in that
  producer's `source.enrichment_labels`, with the provenance that says why the
  read path adds it. A declared key is removed from that document's reality view
  before comparison, in both the key and the value comparison. The declaration is
  per producer on purpose: one producer's read-path quirk must never govern
  another producer that does not add the key.
- **Destination-derived labels.** The corpus normally records collector egress,
  while synthkit may intentionally model a final destination shape. Loki derives
  `service_name` after ingestion when it is absent from an incoming stream. The
  manifest-stream exemption records that exact pre-ingest/post-ingest boundary;
  it does not claim that either the capture or synthkit is defective. The same
  finding's reality-only `instance` key remains a separate coverage gap under
  absent-evidence semantics and is not hidden by the exemption.
- **Read-path enrichment values.** A read path also writes markers into the
  value of a label that is otherwise genuine collector-egress evidence: Grafana
  Cloud Adaptive Metrics replaces a retained label's value with `<aggregated>`
  when it aggregates the series away. Declare those with the same block plus a
  `values` list. The key stays in the reality view and still compares as a key;
  only the declared values are removed before the value comparison. Use the
  `values` form whenever the key itself is real — a key-scoped declaration would
  silently stop the comparator noticing that synthkit never emits that key.
- **The synth producer's own selector labels.** synthkit's composition root
  stamps a blueprint selector label on every blueprint-scoped series, stream and
  span. It is synthkit's routing key rather than a vendor name synthkit
  invented, and no capture of collector egress can carry it, so comparing it
  against one is the same category error as comparing a read-path enrichment
  label. The synth export declares those keys in
  `provenance.selector_labels`, read from the constant the composition root
  defines so a rename cannot silently reopen the finding class, and the
  comparator removes them from the synth view. This runs in the synth-to-reality
  direction only and only for declared keys: every other synth-only key is still
  a contradiction, and this field must never become a general suppression list.
- **Substrate scoping is document-level.** When the synth export declares
  `provenance.substrate`, a corpus document whose `authority.substrates` does
  not include it is skipped entirely. That is the right scope for a
  substrate-specific claim and the wrong scope for everything else in the same
  document, so an export that models more than one substrate — the fidelity
  gate's does, because it is produced with `BLUEPRINT_NAMES='*'` — declares no
  substrate rather than a value that would drop whole documents of real,
  substrate-independent evidence. A capture-instance value that only one
  substrate can produce, such as a Kubernetes build string, is kept out of the
  comparison by the producer marking it `values_elided`, not by dropping the
  document that carries it.
- **Label values need closed-set evidence before they contradict.** A value
  synthkit emits that reality does not carry is a coverage gap by default: one
  deployment capture cannot enumerate an open set such as region, topology,
  protocol, operating system, or request type. The comparator keeps a short,
  explicit signal-and-field allow-list for values whose owning signal contract
  proves a closed enum; only those synth-only values are contradictions. A
  reality-only value is always a coverage gap. An unknown value set therefore
  follows the standing absent-evidence rule. Open two-way differences stay one
  coverage gap and name both directions; closed two-way differences remain one
  finding in each report section. Empty or elided values remain absent evidence
  and are not compared.
- **The current `kube_pod_info` limits remain visible.** The EKS evidence
  includes `created_by_kind` values `AutoscalingListener` and `EphemeralRunner`
  beyond synthkit's four modeled owner kinds, and includes `host_network=true`
  alongside `false`. These are coverage gaps, not contradictions, and are
  tracked as accuracy limits under existing SKT-0010.13 (pods without a
  Deployment owner or node, including the related host-network modeling work).
- **Elided or empty value sets.** A label marked `values_elided` carries no
  value evidence at all and runs no value comparison, and neither does a key
  observed on either side without any value. Presence of the key is still
  evidence.

## Explicit contradiction exemptions

An exemption is a narrow, reviewable decision for a contradiction that is
known and intentionally outside the current model. It must not hide a new
finding or turn a coverage gap into a pass. The command loads a separate JSON
document with this versioned shape:

```json
{
  "version": "synthkit.telemetry.contradiction-exemptions/v1alpha1",
  "exemptions": [
    {
      "id": "EX-001",
      "reason": "The selected account is not the complete region universe.",
      "area": "cw",
      "source_kind": "gcx_live_readback",
      "substrate": "eks",
      "finding_kind": "label_value_contradiction",
      "field": "labels.region",
      "signal_prefix": "aws_",
      "only_in_synth": ["eu-west-1", "us-east-1"],
      "expected_matches": 2
    }
  ]
}
```

`id`, `reason`, `area`, `source_kind`, `substrate`, `finding_kind`, `field`,
`only_in_synth`, and `expected_matches` are required. Exactly one selector is
also required: `signal` selects one exact signal, while `signal_prefix` selects
a prefix. `only_in_synth` must be a non-empty, sorted, duplicate-free list
that exactly equals `difference(synth_values, reality_values)` for each match.
`expected_matches` must be positive and protects against a stale rule when the
corpus changes. Unknown fields, duplicate IDs, malformed records, stale counts,
overlapping rules, and rules that match the wrong finding kind are errors.

Applying exemptions marks matching contradiction findings with the exemption ID
and reason in place. It never removes or reclassifies a finding. A finding may
match at most one rule, and every rule must match exactly its expected count.
The report keeps exempted findings under `Contradictions`, with the ID and
reason shown on the finding line. Only contradictions without an exemption ID
fail the command. Coverage gaps remain visible and report-only. If the
exemption document is intentionally optional, its caller may treat a missing
file as an empty list; malformed or present documents must still fail closed.
An exemption count mismatch also fails closed, but the command writes the full
report before returning that drift diagnostic so a stale rule and every
co-occurring contradiction are visible in the same run.

A declared enrichment label looks like this, and lives in the `source` block
beside the rest of the producer provenance:

```json
"enrichment_labels": [
  {
    "key": "<observed-key>",
    "provenance": "<why this producer's read path adds the key after ingest>"
  },
  {
    "key": "<observed-key-with-a-real-egress-meaning>",
    "values": ["<marker-the-read-path-writes-into-it>"],
    "provenance": "<why this producer's read path writes that value after ingest>"
  }
]
```

A declaration is curated evidence about a read path, so a cumulative refresh
never drops it: a producer re-run that omits the block keeps the established
declaration and may only add keys or values to it. A key-scoped declaration is
the broader claim, so merging a key-scoped and a value-scoped declaration of the
same key keeps it key-scoped and never narrows it. Removing a declaration is a
reviewer decision, made the same way as any other narrowing in the table below.

## Reviewer decision table

Use this table for every candidate difference. The question is not whether the
raw capture changed; it is whether the authoritative structural evidence for
the same producer, substrate, and configuration changed.

| Candidate observation | How to recognize it | Decision | Corpus action |
| --- | --- | --- | --- |
| New stable structure or value evidence | A new metric/log/trace/profile/sigil identity, structural field, or value is present in repeated measurements of the same source/configuration, or is independently confirmed by the same substrate. | Accept when the owning `signals/<area>.md` contract and provenance agree. | Add it through the cumulative structural union, retain confirmed stable values, and update `captured_on`. If the evidence disagrees with synth, correct synth toward the observed data; do not alter the observation to make synth pass. |
| Capture-volume or subset noise | Run counts, receipt counts, observed contract counts, timestamps, or the number of sampled instances changes; the candidate is only a subset of established shapes. | Treat as noise, not drift. | Keep established shapes and omit the candidate-only metadata change from `Diff`. `capture_volume` and receipts may document the run, but they never authorize deletion or narrowing and do not update `captured_on` by themselves. |
| Volatile identifiers | A real attribute key is present, but its values vary between runs or are per-run names/IDs. | Accept the key or structural presence, not the changing value set. | Store `values: []` with `values_elided: true`. This is presence-only evidence and must not become a list of synthetic identifiers. Keep dynamic log-source exemplars out of family identity. |
| Substrate-only difference | A shape or value appears in one substrate and not another, such as a k3s-specific or EKS-specific label. | Keep the evidence substrate-scoped. Never use it to contradict the other substrate. | Keep separate documents and `authority.substrates` values. K3s evidence cannot correct EKS-scoped claims and vice versa. |
| Collector-version change | The collector/chart version, or another configuration component that defines the source, differs from the baseline. | Do not silently merge it as the same source/configuration. | Preserve the old baseline while the root reviews the new provenance. Accept it only as a separately identified producer/configuration or after an explicit baseline decision; retain the version in `source.collector_version`. |
| Candidate removal or narrowing | A previously accepted identity, field, key, or value is absent from one later capture. | Absence in one capture is not deletion authority. Require separate confirmed drift evidence from the same substrate/configuration. | Do not remove or narrow the baseline during an ordinary refresh. If confirmed drift is later accepted, make that explicit review decision and update the affected document; otherwise retain the established evidence. |

The report is an evidence record and an input to the review above, not
permission to change captured reality. The command fails on an unexempted
contradiction and reports coverage gaps without failing. When an accepted,
substrate-scoped observation contradicts synthkit, the implementation and its
authoritative [signal catalogue](https://github.com/rknightion/synthkit/blob/main/SIGNALS.md) are corrected toward observed
reality. Captured evidence is not rewritten merely to silence a finding.

## Sticky values and `values_elided`

Attribute values are evidence about a key, not a contract to enumerate every
runtime value. The normal merge is a sorted set union for a stable value set.
When the same key has varying values across the measurement runs, the
canonical form is:

```json
{
  "key": "<observed-key>",
  "values": [],
  "values_elided": true
}
```

This means that the key is real and its values are open-ended capture noise. It
does not mean that the key was absent, that the value is empty, or that a
reviewer may choose an arbitrary replacement value. Once `values_elided` is
true, it is sticky: a later candidate containing one value cannot re-materialize
a finite value list or narrow the established evidence. Clearing the elision,
narrowing a value set, or treating a new stable value as confirmed drift
requires separate reviewer-confirmed evidence from the applicable source and
substrate.

The same rule protects structural fields. A later subset cannot erase an
attribute key, transport, histogram form, span name, or other established
shape. Only separately confirmed drift can authorize deletion or narrowing.

## Operational boundary and useful references

Live operations are root-only and exclusive. Reviewers and execution lanes may
inspect candidates and reports, but must not run the k3d lab or live tooling,
write to a live source, deploy anything, or issue a non-read-only query. Never
put credentials, credential-bearing URLs, private deployment identifiers, or
operator-selected target details in this page, a source ID, or a committed
corpus document.

For the exact envelope and path rules, see the
[reality-corpus README](https://github.com/rknightion/synthkit/blob/main/reality-corpus/README.md). For capture tooling and
inventory concepts, see [Capture & Tooling](tools.md) and
[CLI & Commands](cli.md). For signal naming and scope, use the
[catalogue contract](https://github.com/rknightion/synthkit/blob/main/SIGNALS.md), [cross-cutting canon](https://github.com/rknightion/synthkit/blob/main/signals/00-canon.md),
and the relevant [signal area](signal-areas.md).
