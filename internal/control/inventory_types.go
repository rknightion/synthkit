// SPDX-License-Identifier: AGPL-3.0-only

package control

// InventoryReport is the live emission inventory served at GET /control/inventory: per-blueprint
// rollups + per-construct detail + grand totals. Built by the runner (control.InventorySource),
// mirroring how Schema is built by control.SchemaSource — control owns the type so the runner can
// return it without importing control's consumers and without an import cycle.
type InventoryReport struct {
	Blueprints []BlueprintInventory `json:"blueprints"`
	Totals     InventoryTotals      `json:"totals"`
}

// BlueprintInventory rolls up every construct in one blueprint.
type BlueprintInventory struct {
	Blueprint      string               `json:"blueprint"`
	DistinctSeries int64                `json:"distinct_series"` // summed across constructs (metrics)
	MetricNames    int                  `json:"metric_names"`    // distinct union
	LabelKeys      int                  `json:"label_keys"`      // distinct union
	Constructs     []ConstructInventory `json:"constructs"`
}

// ConstructInventory is one construct/workload instance's emitted shape (names + label KEYS only —
// never values; this is internal bookkeeping, never stamped on the wire).
type ConstructInventory struct {
	Kind             string                    `json:"kind"`
	Name             string                    `json:"name"`
	Identity         *QueryIdentity            `json:"identity,omitempty"`          // default declared low-cardinality selector evidence
	MetricIdentities map[string]*QueryIdentity `json:"metric_identities,omitempty"` // explicit per-family overrides; an empty Labels map is meaningful
	DistinctSeries   int64                     `json:"distinct_series"`             // metrics signature count (capped; see Capped)
	Capped           bool                      `json:"capped"`                      // true once the signature set hit its cap
	MetricNames      []string                  `json:"metric_names"`                // sorted
	MetricLabels     []string                  `json:"metric_label_keys"`           // sorted union of metric label keys
	LogSources       []string                  `json:"log_sources"`                 // sorted
	LogLabelKeys     []string                  `json:"log_label_keys"`              // sorted union (stream + structured-metadata keys)
	SpanServices     []string                  `json:"span_services"`               // sorted
	SpanNames        []string                  `json:"span_names"`                  // sorted
	SpanAttrKeys     []string                  `json:"span_attr_keys"`              // sorted union (span + resource attr keys)
}

// QueryIdentity is a safe live-query selector. Scope is explicit
// because substrate output deliberately omits blueprint; Labels contains only declared,
// low-cardinality identity needed to query the emitted family (never request IDs, secrets, or
// arbitrary emitted label values). Missing dimensions are absent from Labels, never sentinels.
type QueryIdentity struct {
	Scope  string            `json:"scope"`  // "blueprint" or "substrate"
	Labels map[string]string `json:"labels"` // stable declared query selectors
}

// InventoryTotals are process-wide rollups.
type InventoryTotals struct {
	DistinctSeries int64 `json:"distinct_series"`
	Constructs     int   `json:"constructs"`
	Blueprints     int   `json:"blueprints"`
}

// InventorySource supplies the live inventory. Implemented by the runner; control depends only on
// this interface (mirrors SchemaSource).
type InventorySource interface {
	Inventory() InventoryReport
}
