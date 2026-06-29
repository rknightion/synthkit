// SPDX-License-Identifier: AGPL-3.0-only

package bpsource

import (
	"context"
	"os"
	"path/filepath"
	"sort"

	"github.com/rknightion/synthkit/internal/blueprint"
)

// loadDir loads every accepted file in dir. nsFor returns the sanitized namespace
// prefix for a filename and whether to accept it. prov==ProvBuiltin ⇒ plain Load
// (bare names); otherwise LoadNamespaced applies the prefix BEFORE resolve
// (consistent seed+label — see BLOCKER fix in seams.md).
func (m *Manager) loadDir(dir string, prov Provenance, sourceID string, nsFor func(fn string) (string, bool)) ([]Loaded, []Diag) {
	var out []Loaded
	var diags []Diag
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, nil // absent dir = nothing staged, not an error
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	for _, fn := range names {
		ns, ok := nsFor(fn)
		if !ok {
			continue
		}
		data, rerr := os.ReadFile(filepath.Join(dir, fn))
		if rerr != nil {
			diags = append(diags, Diag{"error", fn, "read", rerr.Error()})
			continue
		}
		var res *blueprint.Resolved
		var lerr error
		if prov == ProvBuiltin {
			res, lerr = blueprint.Load(data, m.reg)
		} else {
			res, lerr = blueprint.LoadNamespaced(data, SanitizeNS(ns), m.reg)
		}
		if lerr != nil {
			diags = append(diags, Diag{"error", fn, "load", lerr.Error()})
			continue
		}
		out = append(out, Loaded{Resolved: res, Provenance: prov, SourceID: sourceID})
	}
	return out, diags
}

// scanBaked loads every *.yaml from the built-in baked blueprints directory.
// Built-ins use plain Load (no namespace prefix — bare names are intentional).
func (m *Manager) scanBaked() ([]Loaded, []Diag) {
	return m.loadDir(m.bakedDir, ProvBuiltin, "", func(fn string) (string, bool) {
		return "", filepath.Ext(fn) == ".yaml"
	})
}

// scanCustom loads every namespace-prefixed *.yaml from the custom (upload) directory.
// Files must be named "<ns>__<name>.yaml"; others are silently skipped.
func (m *Manager) scanCustom() ([]Loaded, []Diag) {
	return m.loadDir(filepath.Join(m.dataDir, customDir), ProvUpload, "", func(fn string) (string, bool) {
		ns, _, ok := parseUploadFilename(fn)
		return ns, ok
	})
}

// scanGitDirs loads every *.yaml from every configured git source's on-disk directory,
// namespacing blueprints by the source's configured Namespace field.
func (m *Manager) scanGitDirs() ([]Loaded, []Diag) {
	var out []Loaded
	var diags []Diag
	for _, s := range m.cfg.Sources() {
		dir := filepath.Join(m.dataDir, gitDir, s.ID)
		ld, d := m.loadDir(dir, ProvGit, s.ID, func(fn string) (string, bool) {
			return s.Namespace, filepath.Ext(fn) == ".yaml"
		})
		out = append(out, ld...)
		diags = append(diags, d...)
	}
	return out, diags
}

// Resolve is the startup entry-point: it fetches all git sources (degrade-on-error),
// scans all directories, builds and persists a Manifest, and returns the merged
// Loaded set plus any diagnostics.
//
// If m.git is nil, git sources are skipped (no fetch, no git-dir scan).
func (m *Manager) Resolve(ctx context.Context) ([]Loaded, Manifest, []Diag) {
	var allLoaded []Loaded
	var allDiags []Diag

	// 1. Fetch git sources (degrade on error; seeds latestSHAs).
	if m.git != nil {
		for _, s := range m.cfg.Sources() {
			if err := m.FetchNow(ctx, s.ID); err != nil {
				allDiags = append(allDiags, Diag{
					Severity: "error",
					Source:   s.ID,
					Stage:    "fetch",
					Detail:   err.Error(),
				})
				// Continue: keep whatever on-disk copy remains.
			}
		}
	}

	// 2. Scan all three source trees.
	baked, bd := m.scanBaked()
	allLoaded = append(allLoaded, baked...)
	allDiags = append(allDiags, bd...)

	custom, cd := m.scanCustom()
	allLoaded = append(allLoaded, custom...)
	allDiags = append(allDiags, cd...)

	if m.git != nil {
		git, gd := m.scanGitDirs()
		allLoaded = append(allLoaded, git...)
		allDiags = append(allDiags, gd...)
	}

	// 3. Build the Manifest.
	m.mu.Lock()
	shas := make(map[string]string, len(m.latestSHAs))
	for k, v := range m.latestSHAs {
		shas[k] = v
	}
	m.mu.Unlock()

	// The boot manifest tracks ONLY custom/git blueprints — the staged-vs-loaded set that can
	// change between restarts. Built-ins are baked into the image and never staged, so including
	// them here would make diffPending (which compares against ListStaged, custom/git only) report
	// every built-in as "removed" and pin the "restart to apply" banner permanently lit.
	entries := make([]ManifestEntry, 0, len(allLoaded))
	for _, l := range allLoaded {
		if l.Provenance == ProvBuiltin {
			continue
		}
		entries = append(entries, ManifestEntry{
			Name:       l.Resolved.Name,
			Provenance: l.Provenance,
			SourceID:   l.SourceID,
		})
	}
	man := Manifest{
		Blueprints: entries,
		SourceSHAs: shas,
	}

	// 4. Persist manifest + update boot.
	_ = writeManifest(m.dataDir, man) // best-effort; non-fatal if disk is unhappy
	m.mu.Lock()
	m.boot = man
	m.mu.Unlock()

	return allLoaded, man, allDiags
}
