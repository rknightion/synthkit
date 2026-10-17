// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"gopkg.in/yaml.v3"
)

var (
	trimOnce sync.Once
	trimErr  error
)

// trimmedPlatformBlueprint writes a copy of blueprints/acme-ai-platform.yaml reduced to two of
// its seven environments and returns its path. The copy keeps the blueprint name, so the same
// registered dashboard templates (and their /d/ cross-links, tags and rules) are generated, but
// the dry cycle that derives the signal manifest costs about a third as much. It is derived from
// the shipped blueprint at test time, so it cannot drift from it, and nothing is added to
// blueprints/ (anything there is loadable by BLUEPRINT_NAMES=*).
func trimmedPlatformBlueprint(t *testing.T) string {
	t.Helper()
	src, err := os.ReadFile(platformBlueprint)
	if err != nil {
		t.Fatal(err)
	}
	trimmed, err := trimEnvironments(src, map[string]bool{"DEV1": true, "PRD": true})
	if err != nil {
		t.Fatalf("trim blueprint: %v", err)
	}
	// Written beside the built binary, so it is removed with it by TestMain.
	path := filepath.Join(binDirFor(t), "acme-ai-platform.yaml")
	trimOnce.Do(func() { trimErr = os.WriteFile(path, trimmed, 0o644) })
	if trimErr != nil {
		t.Fatal(trimErr)
	}
	return path
}

// trimEnvironments keeps only the named environments, and drops the incidents and scenarios
// aimed at a removed environment's cluster or database (their targets end in "-<env>").
func trimEnvironments(src []byte, keep map[string]bool) ([]byte, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(src, &doc); err != nil {
		return nil, err
	}
	root := doc.Content[0]
	field := func(key string) *yaml.Node {
		for i := 0; i+1 < len(root.Content); i += 2 {
			if root.Content[i].Value == key {
				return root.Content[i+1]
			}
		}
		return nil
	}
	scalar := func(n *yaml.Node, key string) string {
		for i := 0; i+1 < len(n.Content); i += 2 {
			if n.Content[i].Value == key {
				return n.Content[i+1].Value
			}
		}
		return ""
	}
	envs := field("environments")
	if envs == nil {
		return nil, fmt.Errorf("blueprint has no environments")
	}
	var kept []*yaml.Node
	var dropped []string
	for _, e := range envs.Content {
		name := scalar(e, "name")
		if keep[name] {
			kept = append(kept, e)
		} else {
			dropped = append(dropped, "-"+strings.ToLower(name))
		}
	}
	if len(kept) != len(keep) {
		return nil, fmt.Errorf("kept %d environments, want %d", len(kept), len(keep))
	}
	envs.Content = kept
	aimedAtDropped := func(target string) bool {
		for _, suffix := range dropped {
			if strings.HasSuffix(target, suffix) {
				return true
			}
		}
		return false
	}
	if incidents := field("incidents"); incidents != nil {
		var out []*yaml.Node
		for _, inc := range incidents.Content {
			if !aimedAtDropped(scalar(inc, "target")) {
				out = append(out, inc)
			}
		}
		incidents.Content = out
	}
	if scenarios := field("scenarios"); scenarios != nil {
		var out []*yaml.Node
		for _, sc := range scenarios.Content {
			hit := false
			for i := 0; i+1 < len(sc.Content); i += 2 {
				if sc.Content[i].Value != "effects" {
					continue
				}
				for _, eff := range sc.Content[i+1].Content {
					hit = hit || aimedAtDropped(scalar(eff, "target"))
				}
			}
			if !hit {
				out = append(out, sc)
			}
		}
		scenarios.Content = out
	}
	return yaml.Marshal(&doc)
}
