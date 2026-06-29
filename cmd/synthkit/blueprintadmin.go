// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"context"

	"github.com/rknightion/synthkit/internal/bpsource"
	"github.com/rknightion/synthkit/internal/control"
)

// blueprintAdminAdapter wraps *bpsource.Manager + bpsource.SourceConfig and implements
// control.BlueprintAdmin, mapping bpsource↔control DTOs field-for-field.
// control never imports bpsource (I24); the composition root does the mapping here,
// exactly as toControlConfigView does for config.RedactedConfig → control.ConfigView.
type blueprintAdminAdapter struct {
	mgr *bpsource.Manager
	sc  bpsource.SourceConfig
}

// StageUpload stages a blueprint upload into the custom staging directory.
func (a *blueprintAdminAdapter) StageUpload(ns, name string, yaml []byte) error {
	return a.mgr.StageUpload(ns, name, yaml)
}

// RemoveUpload removes a staged upload by its namespaced name "<ns>/<name>".
func (a *blueprintAdminAdapter) RemoveUpload(nsName string) error {
	return a.mgr.RemoveUpload(nsName)
}

// ListStaged returns all staged blueprints (uploads + on-disk git blobs).
func (a *blueprintAdminAdapter) ListStaged() []control.StagedBlueprint {
	staged := a.mgr.ListStaged()
	out := make([]control.StagedBlueprint, len(staged))
	for i, s := range staged {
		out[i] = control.StagedBlueprint{
			Name:       s.Name,
			Provenance: string(s.Provenance),
			SourceID:   s.SourceID,
		}
	}
	return out
}

// Validate loads and inspects a YAML blueprint, returning a control.ValidationResult.
func (a *blueprintAdminAdapter) Validate(yaml []byte) control.ValidationResult {
	vr := a.mgr.Validate(yaml)
	return control.ValidationResult{
		OK:          vr.OK,
		Name:        vr.Name,
		Cardinality: vr.Cardinality,
		Estimated:   vr.Estimated,
		Diagnostics: vr.Diagnostics,
	}
}

// Pending returns the staged-vs-manifest diff. Does no git I/O.
func (a *blueprintAdminAdapter) Pending() control.PendingChanges {
	p := a.mgr.Pending()
	return control.PendingChanges{
		Added:   p.Added,
		Removed: p.Removed,
		Changed: p.Changed,
		Restart: p.Restart,
	}
}

// Sources returns all configured git sources via the SourceConfig.
func (a *blueprintAdminAdapter) Sources() []control.SourceView {
	srcs := a.sc.Sources()
	out := make([]control.SourceView, len(srcs))
	for i, s := range srcs {
		out[i] = control.SourceView{
			ID:          s.ID,
			Name:        s.Name,
			Namespace:   s.Namespace,
			URL:         s.URL,
			Ref:         s.Ref,
			Subpath:     s.Subpath,
			TokenEnvVar: s.TokenEnvVar,
			LastSHA:     s.LastSHA,
			LastFetchMs: s.LastFetchMs,
			LastErr:     s.LastErr,
		}
	}
	return out
}

// UpsertSource adds or replaces a git source configuration.
func (a *blueprintAdminAdapter) UpsertSource(sv control.SourceView) error {
	return a.sc.UpsertSource(bpsource.Source{
		ID:          sv.ID,
		Name:        sv.Name,
		Namespace:   sv.Namespace,
		URL:         sv.URL,
		Ref:         sv.Ref,
		Subpath:     sv.Subpath,
		TokenEnvVar: sv.TokenEnvVar,
		LastSHA:     sv.LastSHA,
		LastFetchMs: sv.LastFetchMs,
		LastErr:     sv.LastErr,
	})
}

// RemoveSource removes a git source configuration by ID.
func (a *blueprintAdminAdapter) RemoveSource(id string) error {
	return a.sc.RemoveSource(id)
}

// FetchNow triggers an immediate fetch for a single git source by ID.
func (a *blueprintAdminAdapter) FetchNow(id string) error {
	return a.mgr.FetchNow(context.Background(), id)
}
