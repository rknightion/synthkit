// SPDX-License-Identifier: AGPL-3.0-only

package bpsource

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/rknightion/synthkit/internal/blueprint"
)

// NewManager constructs a Manager from the given Options.
// The boot manifest is read from disk immediately (empty manifest if absent).
// latestSHAs is initialised to an empty map.
func NewManager(opts Options) *Manager {
	now := opts.Now
	if now == nil {
		now = func() int64 { return time.Now().UnixMilli() }
	}
	m := &Manager{
		bakedDir:   opts.BakedDir,
		dataDir:    opts.DataDir,
		reg:        opts.Registry,
		git:        opts.Git,
		cfg:        opts.Config,
		now:        now,
		latestSHAs: map[string]string{},
		selection:  make(map[string]struct{}, len(opts.BlueprintNames)),
		available:  map[string]struct{}{},
	}
	for _, name := range opts.BlueprintNames {
		if name = strings.TrimSpace(name); name != "" {
			m.selection[name] = struct{}{}
		}
	}
	m.boot = readManifest(m.dataDir)
	if m.cfg != nil {
		for _, source := range m.cfg.Sources() {
			if source.ObservedSHA != "" {
				m.latestSHAs[source.ID] = source.ObservedSHA
			}
		}
	}
	return m
}

// SelectionError reports requested runtime blueprint names that were absent from every configured
// source. It is checked by the composition root before any runner is built.
func (m *Manager) SelectionError() error {
	if len(m.selection) == 0 {
		return nil
	}
	missing := make([]string, 0)
	for name := range m.selection {
		if _, ok := m.available[name]; !ok {
			missing = append(missing, name)
		}
	}
	if len(missing) == 0 {
		return nil
	}
	available := make([]string, 0, len(m.available))
	for name := range m.available {
		available = append(available, name)
	}
	sort.Strings(missing)
	sort.Strings(available)
	return fmt.Errorf("bpsource: requested blueprint name(s) %s not found; available: %s", strings.Join(missing, ", "), strings.Join(available, ", "))
}

// BootManifest returns the last manifest read from disk (set at construction and after Resolve).
func (m *Manager) BootManifest() Manifest {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.boot
}

// Sources returns configured sources with their persisted fetched-versus-loaded state. The
// returned state never performs git I/O, so it is safe for the control-plane polling route.
func (m *Manager) Sources() []Source {
	sources := m.sourceSnapshot()
	for i := range sources {
		sources[i].PendingRestart = sources[i].FetchedSHA != "" && sources[i].FetchedSHA != sources[i].LoadedSHA
	}
	return sources
}

// UpsertSource validates a source before persistence. When its location or namespace changes,
// the old staged snapshot is no longer attributable to the new configuration and is hidden until
// a fresh FetchNow stages it. Loaded results remain, because they describe the running process
// until the next restart.
func (m *Manager) UpsertSource(source Source) error {
	if err := ValidateSource(source); err != nil {
		return err
	}
	if m.cfg == nil {
		return fmt.Errorf("bpsource: source configuration is not available")
	}
	m.sourceMu.Lock()
	defer m.sourceMu.Unlock()
	found := false
	for _, old := range m.cfg.Sources() {
		if old.ID != source.ID {
			continue
		}
		found = true
		if source.URL != old.URL || source.Ref != old.Ref || source.Subpath != old.Subpath || source.Namespace != old.Namespace {
			source.FetchedSHA = ""
			source.FetchedFileCount = 0
			source.EffectiveNames = []string{}
			source.ObservedSHA = ""
			source.LastFetchMs = 0
			source.LastErr = ""
		} else {
			source.FetchedSHA = old.FetchedSHA
			source.FetchedFileCount = old.FetchedFileCount
			source.EffectiveNames = append([]string{}, old.EffectiveNames...)
			source.ObservedSHA = old.ObservedSHA
			source.LastFetchMs = old.LastFetchMs
			source.LastErr = old.LastErr
		}
		source.LoadedSHA = old.LoadedSHA
		source.LoadedNames = append([]string{}, old.LoadedNames...)
		source.Skipped = append([]string{}, old.Skipped...)
		break
	}
	if !found {
		// The API body describes configuration only. Status fields are server-owned and must not be
		// accepted from an initial source-create request.
		source.FetchedSHA = ""
		source.FetchedFileCount = 0
		source.EffectiveNames = []string{}
		source.ObservedSHA = ""
		source.LastFetchMs = 0
		source.LastErr = ""
		source.LoadedSHA = ""
		source.LoadedNames = []string{}
		source.Skipped = []string{}
	}
	source.PendingRestart = source.FetchedSHA != "" && source.FetchedSHA != source.LoadedSHA
	return m.cfg.UpsertSource(source)
}

// FetchNow fetches (or skip-fetches) a single git source by ID.
// It updates latestSHAs[id] under m.mu and persists LastSHA/LastFetchMs/LastErr
// via cfg.UpsertSource.
func (m *Manager) FetchNow(ctx context.Context, id string) error {
	if m.cfg == nil {
		return fmt.Errorf("bpsource: source configuration is not available")
	}
	if m.git == nil {
		return fmt.Errorf("bpsource: git fetching is not configured")
	}
	var src Source
	found := false
	for _, s := range m.sourceSnapshot() {
		if s.ID == id {
			src = s
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("bpsource: unknown source %q", id)
	}

	headSHA, err := m.git.HeadSHA(ctx, src.URL, src.Ref, src.TokenEnvVar)
	if err != nil {
		// Record the error in config but return it for the caller.
		if _, persistErr := m.mergeFetchStatus(id, src, func(current *Source) {
			current.LastErr = err.Error()
		}); persistErr != nil {
			return fmt.Errorf("bpsource: recording fetch error for %q: %w", id, persistErr)
		}
		return fmt.Errorf("bpsource: HeadSHA for %q: %w", id, err)
	}

	// Always seed latestSHAs with the observed HEAD.
	m.mu.Lock()
	m.latestSHAs[id] = headSHA
	m.mu.Unlock()

	gitIDDir := filepath.Join(m.dataDir, gitDir, id)

	// If HEAD matches what we already have on-disk and the dir is non-empty, skip fetch.
	if headSHA == src.FetchedSHA && dirNonEmpty(gitIDDir) {
		merged, err := m.mergeFetchStatus(id, src, func(current *Source) {
			current.ObservedSHA = headSHA
			current.LastErr = ""
		})
		if err != nil {
			return fmt.Errorf("bpsource: persisting source %q: %w", id, err)
		}
		if !merged {
			return sourceChangedDuringFetch(id)
		}
		return nil
	}

	// Fetch new YAML blobs.
	blobs, err := m.git.FetchYAML(ctx, src.URL, src.Ref, src.Subpath, src.TokenEnvVar)
	if err != nil {
		if _, persistErr := m.mergeFetchStatus(id, src, func(current *Source) {
			current.ObservedSHA = headSHA
			current.LastErr = err.Error()
		}); persistErr != nil {
			return fmt.Errorf("bpsource: recording fetch error for %q: %w", id, persistErr)
		}
		return fmt.Errorf("bpsource: FetchYAML for %q: %w", id, err)
	}
	if !m.sourceStillMatches(src) {
		return sourceChangedDuringFetch(id)
	}

	// Clear stale *.yaml files before writing fresh ones.
	if err := removeYAMLFiles(gitIDDir); err != nil {
		return fmt.Errorf("bpsource: clearing stale files for %q: %w", id, err)
	}

	// Write fetched files.
	if err := os.MkdirAll(gitIDDir, 0o755); err != nil {
		return fmt.Errorf("bpsource: mkdir %q: %w", gitIDDir, err)
	}
	for fn, data := range blobs {
		if filepath.Base(fn) != fn || filepath.Ext(fn) != ".yaml" {
			return fmt.Errorf("bpsource: refusing invalid fetched filename %q for source %q", fn, id)
		}
		if err := os.WriteFile(filepath.Join(gitIDDir, fn), data, 0o644); err != nil {
			return fmt.Errorf("bpsource: writing %q for source %q: %w", fn, id, err)
		}
	}

	// Persist the new metadata onto a fresh source value, so a concurrent configuration edit
	// cannot be reverted by this older fetch result.
	merged, err := m.mergeFetchStatus(id, src, func(current *Source) {
		current.FetchedSHA = headSHA
		current.FetchedFileCount = len(blobs)
		current.EffectiveNames = effectiveNames(current.Namespace, blobs)
		current.ObservedSHA = headSHA
		current.LastFetchMs = m.now()
		current.LastErr = ""
	})
	if err != nil {
		return fmt.Errorf("bpsource: persisting fetched source %q: %w", id, err)
	}
	if !merged {
		return sourceChangedDuringFetch(id)
	}
	return nil
}

func effectiveNames(namespace string, blobs map[string][]byte) []string {
	names := make([]string, 0, len(blobs))
	for _, data := range blobs {
		if name, err := declaredBlueprintName(data); err == nil {
			names = append(names, Namespace(namespace, name))
		}
	}
	sort.Strings(names)
	return names
}

// sourceSnapshot takes a stable, detached configuration snapshot. SourceConfig implementations
// are allowed to retain their backing slice, so callers must never mutate the returned values.
func (m *Manager) sourceSnapshot() []Source {
	if m.cfg == nil {
		return []Source{}
	}
	m.sourceMu.Lock()
	defer m.sourceMu.Unlock()
	return cloneSources(m.cfg.Sources())
}

func cloneSources(sources []Source) []Source {
	cloned := make([]Source, len(sources))
	for i, source := range sources {
		cloned[i] = source
		cloned[i].EffectiveNames = cloneStrings(source.EffectiveNames)
		cloned[i].LoadedNames = cloneStrings(source.LoadedNames)
		cloned[i].Skipped = cloneStrings(source.Skipped)
	}
	return cloned
}

func sameFetchConfiguration(current, fetched Source) bool {
	return current.ID == fetched.ID &&
		current.URL == fetched.URL &&
		current.Ref == fetched.Ref &&
		current.Subpath == fetched.Subpath &&
		current.Namespace == fetched.Namespace &&
		current.TokenEnvVar == fetched.TokenEnvVar
}

// mergeFetchStatus applies only server-owned fields to a fresh persisted source. It deliberately
// rejects a result produced for an older URL/ref/subpath/namespace, rather than restoring that
// stale client configuration over an operator's concurrent edit.
func (m *Manager) mergeFetchStatus(id string, fetched Source, merge func(*Source)) (bool, error) {
	m.sourceMu.Lock()
	defer m.sourceMu.Unlock()
	if m.cfg == nil {
		return false, nil
	}
	for _, current := range m.cfg.Sources() {
		if current.ID != id {
			continue
		}
		if !sameFetchConfiguration(current, fetched) {
			return false, nil
		}
		merge(&current)
		current.PendingRestart = current.FetchedSHA != "" && current.FetchedSHA != current.LoadedSHA
		if err := m.cfg.UpsertSource(current); err != nil {
			return true, err
		}
		return true, nil
	}
	return false, nil
}

func (m *Manager) sourceStillMatches(fetched Source) bool {
	m.sourceMu.Lock()
	defer m.sourceMu.Unlock()
	if m.cfg == nil {
		return false
	}
	for _, current := range m.cfg.Sources() {
		if current.ID == fetched.ID {
			return sameFetchConfiguration(current, fetched)
		}
	}
	return false
}

func sourceChangedDuringFetch(id string) error {
	return fmt.Errorf("bpsource: source %q configuration changed while fetch was in progress; fetched result was discarded", id)
}

// StageUpload validates the exact prospective staged set and writes a blueprint to the custom
// staging directory. The form name is required to match the YAML name; the resulting runtime
// identity is therefore Namespace(ns, name). Validation runs before the write so a rejected
// prospective collision cannot leave a staged file that would fail on restart.
func (m *Manager) StageUpload(ns, name string, data []byte) error {
	if _, err := m.validateUpload(ns, name, data); err != nil {
		return err
	}
	dir := filepath.Join(m.dataDir, customDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("bpsource: mkdir custom: %w", err)
	}
	dst := filepath.Join(dir, uploadFilename(ns, name))
	if err := os.WriteFile(dst, data, 0o644); err != nil {
		return fmt.Errorf("bpsource: writing upload: %w", err)
	}
	return nil
}

// EffectiveBlueprintIdentity returns the namespaced identity that an accepted upload will have
// after restart. It is kept beside StageUpload so control callers do not duplicate namespace
// sanitisation.
func (m *Manager) EffectiveBlueprintIdentity(ns, name string) string {
	return Namespace(ns, name)
}

// validateUpload uses the same loader and set validation as restart, replacing any existing
// upload with the same effective identity in memory. Existing invalid staged files are ignored
// here just as startup records them as diagnostics and continues loading the rest of the set.
func (m *Manager) validateUpload(ns, name string, data []byte) (*blueprint.Resolved, error) {
	if strings.Contains(name, "/") || strings.Contains(name, "__") {
		return nil, fmt.Errorf("blueprint name %q must not contain '/' or '__'", name)
	}
	declaredName, err := declaredBlueprintName(data)
	if err != nil {
		return nil, fmt.Errorf("bpsource: invalid blueprint: %w", err)
	}
	if name != declaredName {
		return nil, fmt.Errorf("blueprint form name %q must match YAML name %q", name, declaredName)
	}
	res, err := blueprint.LoadNamespaced(data, SanitizeNS(ns), m.reg)
	if err != nil {
		return nil, fmt.Errorf("bpsource: invalid blueprint: %w", err)
	}

	// Build the same source set restart evaluates, but replace any prior staged instance with the
	// candidate before checking its cross-blueprint substrate identities.
	baked, _ := m.scanBaked()
	custom, _ := m.scanCustom()
	var git []Loaded
	if m.git != nil {
		git, _ = m.scanGitDirs()
	}
	loadedSet := append(baked, custom...)
	loadedSet = append(loadedSet, git...)
	var candidate []*blueprint.Resolved
	for _, loaded := range loadedSet {
		if loaded.Resolved.Name != res.Name {
			candidate = append(candidate, loaded.Resolved)
		}
	}
	candidate = append(candidate, res)
	if err := blueprint.ValidateSet(candidate); err != nil {
		return nil, fmt.Errorf("bpsource: prospective staged set: %w", err)
	}
	return res, nil
}

// RemoveUpload deletes a staged upload by its namespaced name "<ns>/<name>".
func (m *Manager) RemoveUpload(nsName string) error {
	parts := strings.SplitN(nsName, "/", 2)
	if len(parts) < 2 {
		return fmt.Errorf("bpsource: RemoveUpload: %q is not a namespaced name (want <ns>/<name>)", nsName)
	}
	dst := filepath.Join(m.dataDir, customDir, uploadFilename(parts[0], parts[1]))
	if err := os.Remove(dst); err != nil {
		return fmt.Errorf("bpsource: RemoveUpload: %w", err)
	}
	return nil
}

// ListStaged returns all staged blueprints: uploads from the custom dir + git blobs
// already on-disk. It does NOT perform any git I/O.
func (m *Manager) ListStaged() []StagedInfo {
	var out []StagedInfo

	// Uploads: custom/<ns>__<name>.yaml
	customPath := filepath.Join(m.dataDir, customDir)
	if entries, err := os.ReadDir(customPath); err == nil {
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			ns, name, ok := parseUploadFilename(e.Name())
			if !ok {
				continue
			}
			out = append(out, StagedInfo{
				Name:       Namespace(ns, name),
				Provenance: ProvUpload,
			})
		}
	}

	// Git sources use names captured during FetchNow rather than reconstructing identities from
	// filenames. A fetched file whose YAML has no valid declared name is intentionally absent here:
	// it is reported as skipped after restart, but must not keep the restart banner permanently on.
	for _, s := range m.sourceSnapshot() {
		names := s.EffectiveNames
		if s.FetchedSHA != "" && s.FetchedSHA == s.LoadedSHA {
			names = s.LoadedNames
		}
		for _, name := range names {
			out = append(out, StagedInfo{
				Name:       name,
				Provenance: ProvGit,
				SourceID:   s.ID,
			})
		}
	}
	return out
}

// Pending returns the staged-vs-manifest diff. It does NO git I/O — it reads
// the cached latestSHAs under mu and calls ListStaged (disk-only).
func (m *Manager) Pending() Pending {
	m.mu.Lock()
	boot := m.boot
	m.mu.Unlock()

	fetched := make(map[string]string)
	for _, source := range m.sourceSnapshot() {
		if source.FetchedSHA != "" {
			fetched[source.ID] = source.FetchedSHA
		}
	}
	staged := m.stagedEntries()
	return diffPending(boot, staged, fetched)
}

// PollSources refreshes latestSHAs via HeadSHA for each source. Called only by
// the background poll goroutine — never by Pending.
func (m *Manager) PollSources(ctx context.Context) {
	if m.git == nil || m.cfg == nil {
		return
	}
	for _, s := range m.sourceSnapshot() {
		sha, err := m.git.HeadSHA(ctx, s.URL, s.Ref, s.TokenEnvVar)
		if err != nil {
			// Keep prior cached value on error (degrade-and-continue).
			_, _ = m.mergeFetchStatus(s.ID, s, func(current *Source) {
				current.LastErr = err.Error()
			})
			continue
		}
		m.mu.Lock()
		m.latestSHAs[s.ID] = sha
		m.mu.Unlock()
		_, _ = m.mergeFetchStatus(s.ID, s, func(current *Source) {
			current.ObservedSHA = sha
			current.LastErr = ""
		})
	}
}

// Validate loads and inspects a YAML blueprint, returning a ValidationResult.
// This satisfies the BlueprintAdmin interface (Task 7).
func (m *Manager) Validate(data []byte) ValidationResult {
	res, err := blueprint.Load(data, m.reg)
	if err != nil {
		return ValidationResult{OK: false, Diagnostics: []string{err.Error()}}
	}
	cardinality, estimated := projectCardinality(m.reg, res)
	return ValidationResult{OK: true, Name: res.Name, Cardinality: cardinality, Estimated: estimated}
}

// stagedEntries converts ListStaged output into ManifestEntry slice for diffPending.
func (m *Manager) stagedEntries() []ManifestEntry {
	staged := m.ListStaged()
	entries := make([]ManifestEntry, 0, len(staged))
	for _, s := range staged {
		entries = append(entries, ManifestEntry{
			Name:       s.Name,
			Provenance: s.Provenance,
			SourceID:   s.SourceID,
		})
	}
	return entries
}

// dirNonEmpty reports whether dir exists and contains at least one *.yaml file.
func dirNonEmpty(dir string) bool {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return false
	}
	for _, e := range entries {
		if !e.IsDir() && filepath.Ext(e.Name()) == ".yaml" {
			return true
		}
	}
	return false
}

// removeYAMLFiles removes all *.yaml files in dir (non-recursive). dir absent = no-op.
func removeYAMLFiles(dir string) error {
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, e := range entries {
		if !e.IsDir() && filepath.Ext(e.Name()) == ".yaml" {
			if err := os.Remove(filepath.Join(dir, e.Name())); err != nil && !os.IsNotExist(err) {
				return err
			}
		}
	}
	return nil
}
