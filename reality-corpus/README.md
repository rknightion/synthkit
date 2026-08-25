# Reality corpus

The reality corpus is the committed, credential-free input to synthkit's
report-only signal-fidelity check. Each JSON document contains observations for
one [`signals/<area>.md`](../signals/) area from one producer. A producer's
substrate is part of its authority: evidence captured from one substrate must
not contradict a claim scoped to another.

## Layout

Corpus documents live at:

```text
reality-corpus/<signals-area>/<source-id>.json
```

`<signals-area>` is the basename of the owning file under `signals/`, without
the `.md` suffix. `<source-id>` is the generic identity of one producer and its
capture configuration. A materially different configuration uses a different
source ID rather than merging into the existing path. The ID must not contain a
live stack, account, tenant, cluster, or other deployment identifier.

## Version 1alpha1 envelope

Every document has this envelope:

```json
{
  "corpus_version": "synthkit.telemetry.reality-corpus/v1alpha1",
  "area": "k8s",
  "source": {
    "kind": "k3d_lab",
    "substrate": "k3s",
    "collector": "grafana/k8s-monitoring",
    "collector_version": "4.4.0",
    "captured_on": "2026-08-25"
  },
  "authority": {
    "substrates": ["k3s"]
  },
  "capture_volume": {
    "runs": 2,
    "observed_contract_counts": [39, 103]
  },
  "inventory": {
    "schema_version": "synthkit.telemetry.inventory/v1alpha1",
    "metrics": [],
    "logs": [],
    "traces": [],
    "profiles": [],
    "sigil": [],
    "receipts": []
  }
}
```

The `source` provenance applies to every observation in `inventory`. The
capture date is a calendar date so timestamps do not create corpus churn.
`inventory` uses the canonical schema defined by
[`internal/inventory`](../internal/inventory/schema.go).

## Authority and comparison

- Compare a corpus document independently. Never union documents from
  different substrates before diffing them.
- `authority.substrates` contains exactly `source.substrate`. Evidence from one
  substrate can therefore contradict only synthetic claims evaluated for that
  same substrate.
- Limit comparison to signal families present in that document. A family the
  corpus does not cover is not a coverage gap.
- Keep the check report-only. Findings are printed, but findings do not make
  the command fail in this wave.
- `capture_volume` and `inventory.receipts` are provenance only and are never
  comparison inputs.

## Canonical refresh and capture noise

Refresh uses a cumulative structural union only for the same path/source ID,
producer kind, substrate, collector, and collector version. The path/source ID
is the generic configuration identity; a materially different configuration is
a separate baseline. Family identity within that baseline is:

| Signal class | Identity |
| --- | --- |
| Metric | metric name |
| Log | transport plus stream-label and structured-metadata shape |
| Trace | service |
| Profile | profile type |
| Sigil | ingest kind |

Missing observations never remove established shapes. Set-valued structural
fields are unioned and sorted. Attribute keys are sticky. Values that are
identical across the measurement runs remain as observed evidence. Values that
vary between runs are stored as an empty value set with
`values_elided: true`; this records the key as real while treating its values
as open-ended capture noise. Once a value set is elided, it remains elided
until a reviewer supplies separate confirmed drift evidence.

Dynamic log-source exemplars are provenance, not authoritative family
identity. `captured_on` changes only when an accepted refresh adds structural
evidence or confirmed stable-value evidence. A later capture containing only a
subset therefore produces the same canonical document. Deletion or narrowing
requires separate confirmed drift evidence; a single absence is never deletion
authority.
