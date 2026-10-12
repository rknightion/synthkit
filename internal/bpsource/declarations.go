// SPDX-License-Identifier: AGPL-3.0-only
package bpsource

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"reflect"
	"time"

	"github.com/rknightion/synthkit/internal/control"
	"github.com/rknightion/synthkit/internal/ha"
)

const maxDeclaredBytes = 262144

func (s *storeSourceConfig) previewDeclarations(desired []control.DeclaredSource) error {
	if err := control.CheckDeclaredSources(s.store.Snapshot(), desired); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.preview = append([]control.DeclaredSource{}, desired...)
	return nil
}
func (s *storeSourceConfig) commitDeclarations(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.preview == nil {
		return nil
	}
	err := s.store.ReconcileDeclaredSources(ctx, s.preview)
	s.preview = nil
	return err
}

// ReadDeclarations is bounded and strict. Status, ownership flags and credential values are
// not part of the input schema. Missing/unreadable files never mean an empty desired set.
func ReadDeclarations(path string, policy SourcePolicy) ([]control.DeclaredSource, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("read GIT_SOURCES_FILE: %w", err)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("GIT_SOURCES_FILE must be a regular file")
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("read GIT_SOURCES_FILE: %w", err)
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxDeclaredBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxDeclaredBytes {
		return nil, fmt.Errorf("GIT_SOURCES_FILE exceeds 256 KiB")
	}
	// Reject duplicate object keys as well as duplicate source IDs.
	if err := uniqueJSONKeys(json.NewDecoder(bytes.NewReader(data))); err != nil {
		return nil, fmt.Errorf("GIT_SOURCES_FILE: %w", err)
	}
	d := json.NewDecoder(bytes.NewReader(data))
	d.DisallowUnknownFields()
	var desired []control.DeclaredSource
	if err := d.Decode(&desired); err != nil {
		return nil, fmt.Errorf("GIT_SOURCES_FILE: %w", err)
	}
	if desired == nil || len(desired) > 128 {
		return nil, fmt.Errorf("GIT_SOURCES_FILE must be an array of at most 128 sources (use [] to clear)")
	}
	var extra any
	if err := d.Decode(&extra); err != io.EOF {
		return nil, fmt.Errorf("GIT_SOURCES_FILE has trailing data")
	}
	seen := map[string]bool{}
	for _, s := range desired {
		if seen[s.ID] {
			return nil, fmt.Errorf("duplicate declared source ID %q", s.ID)
		}
		seen[s.ID] = true
		if err := policy.ValidateSource(Source{ID: s.ID, Name: s.Name, Namespace: s.Namespace, URL: s.URL, Ref: s.Ref, Subpath: s.Subpath, TokenEnvVar: s.TokenEnvVar}); err != nil {
			return nil, fmt.Errorf("declared source %q: %w", s.ID, err)
		}
	}
	return desired, nil
}
func uniqueJSONKeys(d *json.Decoder) error {
	tok, err := d.Token()
	if err != nil {
		return err
	}
	delim, ok := tok.(json.Delim)
	if !ok {
		return nil
	}
	switch delim {
	case '{':
		seen := map[string]bool{}
		for d.More() {
			key, err := d.Token()
			if err != nil {
				return err
			}
			name, ok := key.(string)
			if !ok || seen[name] {
				return fmt.Errorf("duplicate or invalid object key")
			}
			seen[name] = true
			switch name {
			case "id", "name", "namespace", "url", "ref", "subpath", "token_env_var":
			default:
				return fmt.Errorf("unknown source field %q", name)
			}
			if err := uniqueJSONKeys(d); err != nil {
				return err
			}
		}
	case '[':
		for d.More() {
			if err := uniqueJSONKeys(d); err != nil {
				return err
			}
		}
	default:
		return fmt.Errorf("unexpected JSON delimiter")
	}
	_, err = d.Token()
	return err
}

func (m *Manager) previewDeclaredFile(ctx context.Context, path string) error {
	cfg, ok := m.cfg.(*storeSourceConfig)
	if !ok {
		return fmt.Errorf("declarative sources require a control store")
	}
	desired, err := ReadDeclarations(path, m.policy)
	if err != nil {
		return err
	}
	if reflect.DeepEqual(cfg.store.Snapshot().DeclaredBlueprintSources, desired) {
		return cfg.previewDeclarations(desired)
	}
	docs := map[string]sourceDocument{}
	// Durable sources require existing named slots. Validate all reads before publishing a set.
	if m.backend != nil {
		for _, s := range desired {
			key, err := control.GitSourceKey(s.ID)
			if err != nil {
				return err
			}
			snap, err := m.backend.Load(ctx, key)
			if err != nil {
				return err
			}
			doc, err := decodeSourceDocument(snap.Data)
			if err != nil {
				return err
			}
			docs[s.ID] = doc
		}
	}
	if err := cfg.previewDeclarations(desired); err != nil {
		return err
	}
	for id, doc := range docs {
		m.backendDocs[id] = doc
	}
	return nil
}

// PreviewDeclaredFile is read-only, including on HA standby and during acquisition. Resolve can
// inspect prospective desired definitions without persisting them before Activate's preparation.
func (m *Manager) PreviewDeclaredFile(ctx context.Context, path string) error {
	if path == "" {
		return nil
	}
	m.sourceMu.Lock()
	defer m.sourceMu.Unlock()
	return m.previewDeclaredFile(ctx, path)
}

// CommitDeclared persists only the previously inspected desired definitions, not runtime status.
func (m *Manager) CommitDeclared(ctx context.Context) error {
	m.sourceMu.Lock()
	defer m.sourceMu.Unlock()
	if cfg, ok := m.cfg.(*storeSourceConfig); ok {
		return cfg.commitDeclarations(ctx)
	}
	return nil
}

// ReconcileDeclaredFile reopens the path, so Kubernetes projected-volume symlink swaps and
// ordinary atomic file replacement are observed without watching an obsolete inode.
func (m *Manager) ReconcileDeclaredFile(ctx context.Context, path string) error {
	if path == "" {
		return nil
	}
	return m.gate.Do(ctx, ha.Mutation, func(c context.Context) error {
		m.sourceMu.Lock()
		defer m.sourceMu.Unlock()
		if err := m.previewDeclaredFile(c, path); err != nil {
			return err
		}
		return m.cfg.(*storeSourceConfig).commitDeclarations(c)
	})
}

// WatchDeclared polls local projected input only, independent of git polling. The caller supplies
// its existing bounded leader mutation seam; the context owns the watcher's lifetime.
func (m *Manager) WatchDeclared(ctx context.Context, path string, run func(context.Context, func(context.Context) error) error, report func(error)) {
	if path == "" {
		return
	}
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	lastError := ""
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			err := run(ctx, func(c context.Context) error { return m.ReconcileDeclaredFile(c, path) })
			if err != nil {
				if report != nil && err.Error() != lastError {
					report(err)
				}
				lastError = err.Error()
			} else {
				lastError = ""
			}
		}
	}
}
