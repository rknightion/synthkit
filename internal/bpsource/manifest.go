// SPDX-License-Identifier: AGPL-3.0-only

package bpsource

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
)

func writeManifest(dir string, m Manifest) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	tmp := filepath.Join(dir, manifestFile+".tmp")
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(dir, manifestFile)) // atomic within dir (I25)
}

func readManifest(dir string) Manifest {
	var m Manifest
	b, err := os.ReadFile(filepath.Join(dir, manifestFile))
	if err != nil {
		return Manifest{SourceSHAs: map[string]string{}}
	}
	if json.Unmarshal(b, &m) != nil {
		return Manifest{SourceSHAs: map[string]string{}}
	}
	if m.SourceSHAs == nil {
		m.SourceSHAs = map[string]string{}
	}
	return m
}

func diffPending(boot Manifest, staged []ManifestEntry, latestSHAs map[string]string) Pending {
	bootSet := map[string]bool{}
	for _, e := range boot.Blueprints {
		bootSet[e.Name] = true
	}
	stagedSet := map[string]bool{}
	for _, e := range staged {
		stagedSet[e.Name] = true
	}
	var p Pending
	for n := range stagedSet {
		if !bootSet[n] {
			p.Added = append(p.Added, n)
		}
	}
	for n := range bootSet {
		if !stagedSet[n] {
			p.Removed = append(p.Removed, n)
		}
	}
	for id, sha := range latestSHAs {
		if boot.SourceSHAs[id] != sha {
			p.Changed = append(p.Changed, id)
		}
	}
	sort.Strings(p.Added)
	sort.Strings(p.Removed)
	sort.Strings(p.Changed)
	p.Restart = len(p.Added)+len(p.Removed)+len(p.Changed) > 0
	return p
}
