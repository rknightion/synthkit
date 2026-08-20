// SPDX-License-Identifier: AGPL-3.0-only

package bpsource

import "github.com/rknightion/synthkit/internal/control"

// storeSourceConfig adapts a *control.Store to the SourceConfig interface, storing configured
// git sources in control.State.BlueprintSources. bpsource may import control (composition
// layer); control must never import bpsource (I24 isolation).
type storeSourceConfig struct {
	store *control.Store
}

// NewStoreSourceConfig returns a SourceConfig backed by the given control.Store.
func NewStoreSourceConfig(store *control.Store) SourceConfig {
	return &storeSourceConfig{store: store}
}

// Sources returns the current list of configured git sources, mapped SourceView→Source.
func (s *storeSourceConfig) Sources() []Source {
	views := s.store.Snapshot().BlueprintSources
	out := make([]Source, len(views))
	for i, v := range views {
		out[i] = viewToSource(v)
	}
	return out
}

// UpsertSource adds or replaces the source with matching ID in the store.
func (s *storeSourceConfig) UpsertSource(src Source) error {
	s.store.Update(func(st *control.State) {
		v := sourceToView(src)
		for i, existing := range st.BlueprintSources {
			if existing.ID == src.ID {
				st.BlueprintSources[i] = v
				return
			}
		}
		st.BlueprintSources = append(st.BlueprintSources, v)
	})
	return nil
}

// RemoveSource removes the source with the given ID from the store. No-op if not found.
func (s *storeSourceConfig) RemoveSource(id string) error {
	s.store.Update(func(st *control.State) {
		filtered := st.BlueprintSources[:0]
		for _, v := range st.BlueprintSources {
			if v.ID != id {
				filtered = append(filtered, v)
			}
		}
		// Preserve the non-nil invariant (I24).
		if filtered == nil {
			filtered = []control.SourceView{}
		}
		st.BlueprintSources = filtered
	})
	return nil
}

// sourceToView maps bpsource.Source → control.SourceView field-for-field.
// JSON tags on both structs are identical so this is a trivial field-for-field copy.
func sourceToView(s Source) control.SourceView {
	return control.SourceView{
		ID:               s.ID,
		Name:             s.Name,
		Namespace:        s.Namespace,
		URL:              s.URL,
		Ref:              s.Ref,
		Subpath:          s.Subpath,
		TokenEnvVar:      s.TokenEnvVar,
		FetchedSHA:       s.FetchedSHA,
		FetchedFileCount: s.FetchedFileCount,
		EffectiveNames:   cloneStrings(s.EffectiveNames),
		ObservedSHA:      s.ObservedSHA,
		LastFetchMs:      s.LastFetchMs,
		LastErr:          s.LastErr,
		LoadedSHA:        s.LoadedSHA,
		PendingRestart:   s.PendingRestart,
		LoadedNames:      cloneStrings(s.LoadedNames),
		Skipped:          cloneStrings(s.Skipped),
	}
}

// viewToSource maps control.SourceView → bpsource.Source field-for-field.
func viewToSource(v control.SourceView) Source {
	return Source{
		ID:               v.ID,
		Name:             v.Name,
		Namespace:        v.Namespace,
		URL:              v.URL,
		Ref:              v.Ref,
		Subpath:          v.Subpath,
		TokenEnvVar:      v.TokenEnvVar,
		FetchedSHA:       v.FetchedSHA,
		FetchedFileCount: v.FetchedFileCount,
		EffectiveNames:   cloneStrings(v.EffectiveNames),
		ObservedSHA:      v.ObservedSHA,
		LastFetchMs:      v.LastFetchMs,
		LastErr:          v.LastErr,
		LoadedSHA:        v.LoadedSHA,
		PendingRestart:   v.PendingRestart,
		LoadedNames:      cloneStrings(v.LoadedNames),
		Skipped:          cloneStrings(v.Skipped),
	}
}

func cloneStrings(values []string) []string {
	if values == nil {
		return nil
	}
	return append([]string{}, values...)
}
