// SPDX-License-Identifier: AGPL-3.0-only

package control

// BlueprintAdmin is the control-plane-facing capability set for the external/custom blueprints
// feature. Implemented by *bpsource.Manager; control depends ONLY on this interface —
// never imports bpsource (I24 isolation: control defines its own DTOs, mapped field-for-field
// at the composition root exactly as toControlConfigView does for config).
type BlueprintAdmin interface {
	StageUpload(ns, name string, yaml []byte) error
	RemoveUpload(nsName string) error
	ListStaged() []StagedBlueprint
	Validate(yaml []byte) ValidationResult
	Pending() PendingChanges
	// source management:
	Sources() []SourceView
	UpsertSource(SourceView) error
	RemoveSource(id string) error
	FetchNow(id string) error
}

// StagedBlueprint describes a blueprint that has been staged (upload or git). NO omitempty (I24).
type StagedBlueprint struct {
	Name       string `json:"name"`
	Provenance string `json:"provenance"`
	SourceID   string `json:"source_id"`
}

// ValidationResult is the outcome of a Validate call. NO omitempty (I24).
// JSON tags match bpsource.ValidationResult so the composition-root mapping is trivial.
type ValidationResult struct {
	OK          bool     `json:"ok"`
	Name        string   `json:"name"`        // bare name from YAML (pre-namespace)
	Cardinality int      `json:"cardinality"` // projected distinct series (-1 if estimate unavailable)
	Estimated   bool     `json:"estimated"`   // true = multiplier fallback, not exact projection
	Diagnostics []string `json:"diagnostics"` // load errors / warnings
}

// PendingChanges is the staged-vs-manifest diff driving the "restart to apply" banner.
// NO omitempty (I24). JSON tags match bpsource.Pending.
type PendingChanges struct {
	Added   []string `json:"added"`   // namespaced names staged but not in boot manifest
	Removed []string `json:"removed"` // in boot manifest but no longer staged
	Changed []string `json:"changed"` // git sources whose latest SHA != applied SHA
	Restart bool     `json:"restart"` // any of the above non-empty
}

// SourceView is the wire/UI shape of a configured git source. It mirrors bpsource.Source with
// IDENTICAL json tags so the composition-root field-for-field map compiles with zero friction.
// INVARIANT: no raw token ever appears here — only the name of the env var that holds it
// (TokenEnvVar). NO omitempty (I24: persisted into control.State in Task 14; must round-trip).
type SourceView struct {
	ID          string `json:"id"`            // stable slug, also the on-disk dir name
	Name        string `json:"name"`          // human label
	Namespace   string `json:"namespace"`     // prefix applied to its blueprints
	URL         string `json:"url"`           // https://… (no SSH)
	Ref         string `json:"ref"`           // e.g. "refs/heads/main"
	Subpath     string `json:"subpath"`       // dir within repo holding *.yaml ("" = root)
	TokenEnvVar string `json:"token_env_var"` // NAME of env var holding the token ("" = public)
	LastSHA     string `json:"last_sha"`      // last fetched commit SHA (status only)
	LastFetchMs int64  `json:"last_fetch_ms"` // unix ms of last successful fetch (status only)
	LastErr     string `json:"last_err"`      // last fetch error ("" = ok)
}
