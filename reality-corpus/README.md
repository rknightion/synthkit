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

The loader reads the corpus root and the per-area subdirectories only. A
subdirectory whose name is not a signals area holds a different record kind and
is skipped, so a sibling record file is never parsed as a corpus document.

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

### `source.enrichment_labels`

`enrichment_labels` is optional and declares what this producer's read path adds
after collector egress. It exists so a read-path artefact is never counted as a
label or value synthkit is missing, and it is declared per producer because a
label a read path invents is a property of that read path: another producer
reading the same signal need not see it. Omit the field entirely when the
producer observes collector egress directly, as the k3d lab does.

```json
"enrichment_labels": [
  {
    "key": "asserts_env",
    "provenance": "Grafana Cloud Asserts pipeline label applied stack-side after ingest; no collector emits it at egress, so only a read-path producer observes it."
  },
  {
    "key": "ip_family",
    "values": ["<aggregated>"],
    "provenance": "Grafana Cloud Adaptive Metrics writes this marker into a retained label's value when it aggregates the series away; the key itself is real kube-proxy egress evidence."
  }
]
```

Each entry needs a non-empty `key` and a non-empty `provenance` saying why that
read path adds it. `values` is optional and decides the scope:

- **No `values` — key-scoped.** The whole key is a read-path invention and is
  removed from this document's reality view before comparison, in both the key
  and the value comparison.
- **With `values` — value-scoped.** The key is genuine collector-egress
  evidence and stays, so it still compares as a key; only the listed values are
  removed before the value comparison. Values must be non-empty and distinct.

Use the value-scoped form whenever the key itself is real. Declaring such a key
key-scoped would silently stop the comparator noticing that synthkit never emits
it.

Both forms leave the synth-to-reality direction untouched: a key synthkit emits
that the reality view does not carry is still a contradiction. A declaration is
curated evidence, so a cumulative refresh never drops it — a producer re-run
that omits the block keeps the established declaration and may only add to it,
and merging a key-scoped with a value-scoped declaration of the same key keeps
the broader key-scoped form.

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
