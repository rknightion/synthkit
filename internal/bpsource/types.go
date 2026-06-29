// SPDX-License-Identifier: AGPL-3.0-only

package bpsource

import (
	"context"
	"sync"

	"github.com/rknightion/synthkit/internal/blueprint"
	"github.com/rknightion/synthkit/internal/core"
)

// Provenance tags where a blueprint came from (for the UI badge + pending-diff).
type Provenance string

const (
	ProvBuiltin Provenance = "builtin"
	ProvUpload  Provenance = "upload"
	ProvGit     Provenance = "git"
)

// Source is one configured git source. Persisted in control.State (non-secret).
type Source struct {
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

// Loaded is one resolved blueprint plus its provenance, ready for AddBlueprint.
type Loaded struct {
	Resolved   *blueprint.Resolved
	Provenance Provenance
	SourceID   string // "" for builtin/upload; the Source.ID for git
}

// Manifest is what was loaded at startup; persisted to .boot-manifest.json for pending-diff.
type Manifest struct {
	Blueprints []ManifestEntry   `json:"blueprints"`  // namespaced name + provenance
	SourceSHAs map[string]string `json:"source_shas"` // SourceID → applied SHA
}

// ManifestEntry is one entry in the boot manifest.
type ManifestEntry struct {
	Name       string     `json:"name"`
	Provenance Provenance `json:"provenance"`
	SourceID   string     `json:"source_id"`
}

// Pending is the staged-vs-loaded diff driving the "restart to apply" banner.
type Pending struct {
	Added   []string `json:"added"`   // namespaced names staged but not in boot manifest
	Removed []string `json:"removed"` // in boot manifest but no longer staged
	Changed []string `json:"changed"` // git sources whose latest SHA != applied SHA
	Restart bool     `json:"restart"` // any of the above non-empty
}

// Diag is a load-time problem (mirrors control.Diagnostics severity/where/detail).
type Diag struct{ Severity, Source, Stage, Detail string }

// GitClient is the minimal git capability bpsource needs (implemented by nanogit; faked in tests).
type GitClient interface {
	// HeadSHA returns the commit SHA the ref currently points to (cheap; for poll + fetch).
	HeadSHA(ctx context.Context, url, ref, tokenEnvVar string) (string, error)
	// FetchYAML returns every *.yaml blob under subpath at the given ref, keyed by base filename.
	FetchYAML(ctx context.Context, url, ref, subpath, tokenEnvVar string) (map[string][]byte, error)
}

// SourceConfig abstracts the persisted source-config list (backed by control.Store in production).
type SourceConfig interface {
	Sources() []Source
	UpsertSource(Source) error
	RemoveSource(id string) error
}

// StagedInfo describes a blueprint that has been staged (upload or git).
type StagedInfo struct {
	Name       string     `json:"name"` // namespaced
	Provenance Provenance `json:"provenance"`
	SourceID   string     `json:"source_id"`
}

// ValidationResult is the result of a Validate call.
type ValidationResult struct {
	OK          bool     `json:"ok"`
	Name        string   `json:"name"`        // bare name from YAML (pre-namespace)
	Cardinality int      `json:"cardinality"` // projected distinct series (-1 if estimate unavailable)
	Estimated   bool     `json:"estimated"`   // true = multiplier fallback, not exact projection
	Diagnostics []string `json:"diagnostics"` // load errors / warnings
}

// Options configures a Manager at construction time.
type Options struct {
	BakedDir string         // cfg.BlueprintsDir (built-ins)
	DataDir  string         // <volume>/blueprints (custom + git + manifest)
	Registry *core.Registry // runner.Catalog()
	Git      GitClient      // nanogit adapter (may be nil → git sources skipped)
	Config   SourceConfig   // control.Store adapter
	Now      func() int64   // unix-ms clock (injectable for tests)
}

// Manager is the composition-root object wiring all of the above. Constructed in main.go.
// latestSHAs is a CACHE (mu-guarded) of the newest observed remote SHA per source, refreshed
// ONLY by FetchNow and the optional background poll goroutine — NEVER by Pending(). Pending() and
// the /control/blueprints/pending GET must do NO inline git I/O (the UI polls it every 5s on an
// unguarded route; an inline HeadSHA-per-source would be a rate-limit/DoS/latency footgun).
type Manager struct {
	bakedDir   string
	dataDir    string
	reg        *core.Registry
	git        GitClient
	cfg        SourceConfig
	now        func() int64
	boot       Manifest
	mu         sync.Mutex
	latestSHAs map[string]string
}
