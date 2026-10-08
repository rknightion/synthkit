// SPDX-License-Identifier: AGPL-3.0-only

package bpsource

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/rknightion/synthkit/internal/blueprint"
	"github.com/rknightion/synthkit/internal/ha"
	"gopkg.in/yaml.v3"
)

// loadDir loads every accepted file in dir. nsFor returns the sanitized namespace
// prefix for a filename and whether to accept it. prov==ProvBuiltin ⇒ plain Load
// (bare names); otherwise LoadNamespaced applies the prefix BEFORE resolve
// (consistent seed+label — see BLOCKER fix in seams.md).
func (m *Manager) loadDir(dir string, prov Provenance, sourceID string, applySelection bool, nsFor func(fn string) (string, bool)) ([]Loaded, []Diag) {
	var out []Loaded
	var diags []Diag
	if m.available == nil {
		m.available = map[string]struct{}{}
	}
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
			diags = append(diags, Diag{"error", diagSource(sourceID, fn), "read", rerr.Error()})
			continue
		}
		declaredName, nerr := declaredBlueprintName(data)
		if nerr != nil {
			diags = append(diags, Diag{"error", diagSource(sourceID, fn), "load", nerr.Error()})
			continue
		}
		name := declaredName
		if prov != ProvBuiltin {
			name = SanitizeNS(ns) + "/" + declaredName
		}
		m.available[name] = struct{}{}
		if _, selected := m.selection[name]; applySelection && !m.selectAll && !selected {
			if sourceID != "" && len(m.selection) > 0 {
				diags = append(diags, Diag{"info", diagSource(sourceID, fn), "selection", "deselected by runtime blueprint selection"})
			}
			continue
		}

		var res *blueprint.Resolved
		var lerr error
		if prov == ProvBuiltin {
			res, lerr = blueprint.Load(data, m.reg, m.limits)
		} else {
			res, lerr = blueprint.LoadNamespaced(data, SanitizeNS(ns), m.reg, m.limits)
		}
		if lerr != nil {
			diags = append(diags, Diag{"error", diagSource(sourceID, fn), "load", lerr.Error()})
			continue
		}
		out = append(out, Loaded{Resolved: res, Provenance: prov, SourceID: sourceID})
	}
	return out, diags
}

func diagSource(sourceID, filename string) string {
	if sourceID == "" {
		return filename
	}
	return sourceID + "/" + filename
}

// declaredBlueprintName reads only the declared identity used for source selection. Full schema
// validation and topology resolution happen later through blueprint.Load.
func declaredBlueprintName(data []byte) (string, error) {
	var header struct {
		Name string `yaml:"name"`
	}
	if err := yaml.Unmarshal(data, &header); err != nil {
		return "", err
	}
	if header.Name == "" {
		return "", fmt.Errorf("blueprint name is required")
	}
	return header.Name, nil
}

// scanBaked loads every *.yaml from the built-in baked blueprints directory.
// Built-ins use plain Load (no namespace prefix — bare names are intentional).
func (m *Manager) scanBaked() ([]Loaded, []Diag) {
	return m.loadDir(m.bakedDir, ProvBuiltin, "", false, func(fn string) (string, bool) {
		return "", filepath.Ext(fn) == ".yaml"
	})
}

func (m *Manager) scanBakedSelected() ([]Loaded, []Diag) {
	return m.loadDir(m.bakedDir, ProvBuiltin, "", true, func(fn string) (string, bool) {
		return "", filepath.Ext(fn) == ".yaml"
	})
}

// scanCustom loads every namespace-prefixed *.yaml from the custom (upload) directory.
// Files must be named "<ns>__<name>.yaml"; others are silently skipped.
func (m *Manager) scanCustom() ([]Loaded, []Diag) {
	return m.scanCustomWithSelection(false)
}

func (m *Manager) scanCustomWithSelection(applySelection bool) ([]Loaded, []Diag) {
	dir := filepath.Join(m.dataDir, customDir)
	if !m.readOnly {
		if err := ensurePrivateDir(m.dataDir); err != nil {
			return nil, []Diag{{"error", "custom", "secure", err.Error()}}
		}
		if err := secureBlueprintDir(dir); err != nil {
			return nil, []Diag{{"error", "custom", "secure", err.Error()}}
		}
	} else {
		for _, path := range []string{m.dataDir, dir} {
			if err := validateReadOnlyBlueprintDir(path); err != nil {
				return nil, []Diag{{"error", "custom", "secure", err.Error()}}
			}
		}
	}
	return m.loadDir(dir, ProvUpload, "", applySelection, func(fn string) (string, bool) {
		ns, _, ok := parseUploadFilename(fn)
		return ns, ok
	})
}

// scanGitDirs loads every *.yaml from every configured git source's on-disk directory,
// namespacing blueprints by the source's configured Namespace field.
func (m *Manager) scanGitDirs() ([]Loaded, []Diag) {
	return m.scanGitDirsWithSelection(false)
}

func (m *Manager) scanGitDirsWithSelection(applySelection bool) ([]Loaded, []Diag) {
	var out []Loaded
	var diags []Diag
	if !m.readOnly {
		if err := ensurePrivateDir(m.dataDir); err != nil {
			return nil, []Diag{{"error", "git", "secure", err.Error()}}
		}
		if err := ensurePrivateDir(filepath.Join(m.dataDir, gitDir)); err != nil {
			return nil, []Diag{{"error", "git", "secure", err.Error()}}
		}
	} else {
		for _, path := range []string{m.dataDir, filepath.Join(m.dataDir, gitDir)} {
			if err := validateReadOnlyBlueprintDir(path); err != nil {
				return nil, []Diag{{"error", "git", "secure", err.Error()}}
			}
		}
	}
	for _, s := range m.sourceSnapshot() {
		if s.FetchedSHA == "" {
			continue
		}
		dir := filepath.Join(m.dataDir, gitDir, s.ID)
		secure := secureBlueprintDir
		if m.readOnly {
			secure = validateReadOnlyBlueprintDir
		}
		if err := secure(dir); err != nil {
			diags = append(diags, Diag{"error", s.ID, "secure", err.Error()})
			continue
		}
		ld, d := m.loadDir(dir, ProvGit, s.ID, applySelection, func(fn string) (string, bool) {
			return s.Namespace, filepath.Ext(fn) == ".yaml"
		})
		out = append(out, ld...)
		diags = append(diags, d...)
	}
	return out, diags
}

// validateReadOnlyBlueprintDir retains the writable scan's symlink/regular-file
// admission without mkdir/chmod. Missing directories mean nothing staged.
func validateReadOnlyBlueprintDir(dir string) error {
	info, err := os.Lstat(dir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("staged blueprint directory must be a real directory")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if filepath.Ext(entry.Name()) != ".yaml" {
			continue
		}
		info, err := os.Lstat(filepath.Join(dir, entry.Name()))
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("staged YAML must be a regular file")
		}
	}
	return nil
}

// Resolve is the startup entry-point: it scans the already-fetched directories, builds and
// persists a Manifest, and returns the merged
// Loaded set plus any diagnostics.
//
// Fetching is deliberately separate: a configured source becomes staged only through FetchNow;
// restarting never reaches out to a newer remote revision and therefore never applies a change
// that the operator has not fetched first.
func (m *Manager) Resolve(ctx context.Context) ([]Loaded, Manifest, []Diag) {
	var allLoaded []Loaded
	var allDiags []Diag
	m.available = map[string]struct{}{}

	// 1. Scan all three source trees.
	baked, bd := m.scanBakedSelected()
	allLoaded = append(allLoaded, baked...)
	allDiags = append(allDiags, bd...)

	custom, cd := m.scanCustomWithSelection(true)
	allLoaded = append(allLoaded, custom...)
	allDiags = append(allDiags, cd...)

	git, gd := m.scanGitDirsWithSelection(true)
	allLoaded = append(allLoaded, git...)
	allDiags = append(allDiags, gd...)

	// 2. Build the Manifest from the fetched (not merely observed) SHA for each source.
	shas := make(map[string]string)
	for _, source := range m.sourceSnapshot() {
		if source.FetchedSHA != "" {
			shas[source.ID] = source.FetchedSHA
		}
	}

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

	// 3. Persist manifest + update boot.
	if !m.readOnly {
		_ = m.gate.Do(ctx, ha.Mutation, func(context.Context) error { return writeManifest(m.dataDir, man) })
	} // legacy best-effort persistence
	m.mu.Lock()
	m.boot = man
	m.mu.Unlock()
	if !m.readOnly {
		allDiags = append(allDiags, m.recordLoadResults(ctx, git, gd)...)
	}

	return allLoaded, man, allDiags
}

// recordLoadResults persists the startup result alongside each source's fetched metadata. This
// lets an operator distinguish "fetched but skipped" from "not fetched" after the process is
// running, rather than inferring load success from a restart banner that has already cleared.
// CommitResolved is the acquisition-only persistence phase. The preparation
// capability is passed through to the very same store, never replaced by a bypass gate.
func (m *Manager) CommitResolved(ctx context.Context, man Manifest, loaded []Loaded, diags []Diag) error {
	return m.gate.Do(ctx, ha.Mutation, func(c context.Context) error {
		if err := c.Err(); err != nil {
			return err
		}
		if err := writeManifest(m.dataDir, man); err != nil {
			return err
		}
		var gitLoaded []Loaded
		for _, l := range loaded {
			if l.Provenance == ProvGit {
				gitLoaded = append(gitLoaded, l)
			}
		}
		for _, d := range m.recordLoadResults(c, gitLoaded, diags) {
			if d.Severity == "error" {
				return fmt.Errorf("source load result: %s", d.Detail)
			}
		}
		return c.Err()
	})
}

func (m *Manager) recordLoadResults(ctx context.Context, loaded []Loaded, diags []Diag) []Diag {
	if m.cfg == nil {
		return nil
	}
	loadedNames := map[string][]string{}
	for _, entry := range loaded {
		loadedNames[entry.SourceID] = append(loadedNames[entry.SourceID], entry.Resolved.Name)
	}
	skipped := map[string][]string{}
	for _, diag := range diags {
		parts := strings.SplitN(diag.Source, "/", 2)
		if len(parts) != 2 {
			continue
		}
		skipped[parts[0]] = append(skipped[parts[0]], parts[1]+": "+diag.Detail)
	}
	var persistDiags []Diag
	m.sourceMu.Lock()
	defer m.sourceMu.Unlock()
	for _, source := range m.cfg.Sources() {
		if source.FetchedSHA == "" {
			continue
		}
		source.LoadedSHA = source.FetchedSHA
		source.PendingRestart = false
		source.LoadedNames = append([]string{}, loadedNames[source.ID]...)
		source.Skipped = append([]string{}, skipped[source.ID]...)
		sort.Strings(source.LoadedNames)
		sort.Strings(source.Skipped)
		if err := upsertSourceContext(ctx, m.cfg, source); err != nil {
			persistDiags = append(persistDiags, Diag{"error", source.ID, "persist", err.Error()})
		}
	}
	return persistDiags
}
