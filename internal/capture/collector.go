// SPDX-License-Identifier: AGPL-3.0-only

package capture

import "context"

// CaptureOpts carries the flag-driven knobs every collector honours.
type CaptureOpts struct {
	Namespaces           []string // allow-list; empty = all namespaces
	ExcludeNamespaces    []string // deny-list, applied after the allow-list
	IncludeSecretData    bool     // read Secret data values (default false = metadata only)
	IncludeConfigMapData bool     // read ConfigMap data values (default false = metadata only)
	Collectors           []string // enabled collector names; empty = all registered
}

// Collector populates one section of an Inventory from one source. Implementations fill only
// their own fields so additional collectors (aws, db) are purely additive.
type Collector interface {
	Name() string // "k8s", later "aws", "db"
	Collect(ctx context.Context, inv *Inventory, opts CaptureOpts) error
}
