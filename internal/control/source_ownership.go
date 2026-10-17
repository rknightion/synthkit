// SPDX-License-Identifier: AGPL-3.0-only
package control

import (
	"context"
	"errors"
	"fmt"
	"reflect"
)

// DeclaredSource carries definitions only: the declarer cannot supply sync or applied state.
type DeclaredSource struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Namespace   string `json:"namespace"`
	URL         string `json:"url"`
	Ref         string `json:"ref"`
	Subpath     string `json:"subpath"`
	TokenEnvVar string `json:"token_env_var"`
}

var ErrDeclaredSource = errors.New("source is declared by GIT_SOURCES_FILE; edit or remove its declaration instead of using the console")

func SourceDefinition(s SourceView) DeclaredSource {
	return DeclaredSource{ID: s.ID, Name: s.Name, Namespace: s.Namespace, URL: s.URL, Ref: s.Ref, Subpath: s.Subpath, TokenEnvVar: s.TokenEnvVar}
}

// CheckDeclaredSources rejects adoption of a console ID, rather than silently transferring ownership.
func CheckDeclaredSources(st State, desired []DeclaredSource) error {
	for _, d := range desired {
		for _, source := range st.BlueprintSources {
			if source.ID == d.ID && !source.Declared {
				return fmt.Errorf("declared source %q conflicts with a console-managed source", d.ID)
			}
		}
	}
	return nil
}

// ProjectSources overlays desired definitions without modifying any persisted lifecycle record.
// Removed declarations leave dormant status records intact; they are not fetchable/loadable.
func ProjectSources(st State) []SourceView {
	out := []SourceView{}
	for _, source := range st.BlueprintSources {
		if !source.Declared {
			out = append(out, source)
		}
	}
	for _, d := range st.DeclaredBlueprintSources {
		source := SourceView{Declared: true}
		for _, record := range st.BlueprintSources {
			if record.ID == d.ID && record.Declared {
				source = record
				break
			}
		}
		previous := SourceDefinition(source)
		source.ID, source.Name, source.Namespace, source.URL, source.Ref, source.Subpath, source.TokenEnvVar = d.ID, d.Name, d.Namespace, d.URL, d.Ref, d.Subpath, d.TokenEnvVar
		// Never attribute old staged files to a changed fetch configuration. The stored record is
		// retained byte-identically; only its effective view hides obsolete fetched state.
		if previous.URL != d.URL || previous.Ref != d.Ref || previous.Subpath != d.Subpath || previous.Namespace != d.Namespace || previous.TokenEnvVar != d.TokenEnvVar {
			source.FetchedSHA = ""
			source.FetchedFileCount = 0
			source.EffectiveNames = []string{}
			source.ObservedSHA = ""
			source.LastFetchMs = 0
			source.LastErr = ""
			source.PendingRestart = false
		}
		out = append(out, source)
	}
	return out
}

func (s *Store) ReconcileDeclaredSources(ctx context.Context, desired []DeclaredSource) error {
	desired = append([]DeclaredSource{}, desired...)
	if reflect.DeepEqual(s.Snapshot().DeclaredBlueprintSources, desired) {
		return nil
	}
	var refused error
	_, err := s.UpdateContext(ctx, func(st *State) {
		refused = CheckDeclaredSources(*st, desired)
		if refused == nil {
			st.DeclaredBlueprintSources = append([]DeclaredSource{}, desired...)
		}
	})
	if err != nil {
		return err
	}
	return refused
}

func resetOwnedState(st *State) {
	next := DefaultState()
	next.DeclaredBlueprintSources = append([]DeclaredSource{}, st.DeclaredBlueprintSources...)
	// Live-applied blueprints are running topology, not knobs: a knob reset keeps them (and
	// their persisted record) so state and the running set never diverge. DELETE removes one.
	next.LiveBlueprints = append([]LiveBlueprint{}, st.LiveBlueprints...)
	for _, source := range st.BlueprintSources {
		if source.Declared {
			next.BlueprintSources = append(next.BlueprintSources, source)
		}
	}
	*st = next
}

func DeclaredSourceID(st State, id string) bool {
	for _, source := range st.DeclaredBlueprintSources {
		if source.ID == id {
			return true
		}
	}
	return false
}
