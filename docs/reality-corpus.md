---
title: Reality-corpus refresh
description: How to refresh the credential-free reality corpus and review real signal drift versus capture noise.
---

# Reality-corpus refresh and review

The reality corpus is the committed, credential-free evidence used by the
report-only signal-fidelity check. This page describes how to refresh it and
how to decide whether a candidate records real signal drift or only a different
capture sample. The on-disk contract is defined in the
[reality-corpus README](../reality-corpus/README.md); this page is the
operating procedure built on that contract.

The contract is deliberately narrow:

- The version is `synthkit.telemetry.reality-corpus/v1alpha1`.
- Each document is one `signals/<area>.md` area and one generic producer at
  `reality-corpus/<signals-area>/<source-id>.json`.
- The `source` provenance and `authority.substrates` apply to every observation
  in that document.
- Documents from different substrates are never unioned before `Diff`.
- Capture counts and receipts explain how an observation was collected; they
  are not signal-contract evidence.

Use the [signal catalogue](../SIGNALS.md), its
[cross-cutting canon](../signals/00-canon.md), and the
[signal-area index](signal-areas.md) to identify the owning area and interpret
an observed shape. The Kubernetes and CloudWatch catalogues are examples of
area-level authorities: [Kubernetes](../signals/k8s.md) and
[CloudWatch](../signals/cw.md).

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

### EKS live read-back command

The EKS producer is manual and credentialed. The root must receive the target
selection from the operator and pass it explicitly; the target rejects the
Makefile's unrelated default context rather than querying it:

```bash
GCX_CONTEXT=<operator-selected-context> make signal-fidelity-eks-readback
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
   table below. Then run the report-only fidelity comparison against the
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
   from absence. The report is report-only: a finding does not authorize a
   corpus edit, a synthetic-code edit, or a deployment change.

9. **Commit only the accepted merge.** Update `captured_on` only when the
   reviewer accepts new structural evidence or confirmed stable-value evidence.
   Keep provenance-only count or receipt changes from changing the date. The
   root stages explicit accepted per-area paths, runs the repository's final
   documentation and fidelity checks, and owns the commit and push. An
   unreviewed candidate remains outside the committed corpus.

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

The report remains report-only in this wave. A report finding is an input to the
review above, not permission to change captured reality. When an accepted,
substrate-scoped observation contradicts synthkit, the implementation and its
authoritative [signal catalogue](../SIGNALS.md) are corrected toward observed
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
[reality-corpus README](../reality-corpus/README.md). For capture tooling and
inventory concepts, see [Capture & Tooling](tools.md) and
[CLI & Commands](cli.md). For signal naming and scope, use the
[catalogue contract](../SIGNALS.md), [cross-cutting canon](../signals/00-canon.md),
and the relevant [signal area](signal-areas.md).
