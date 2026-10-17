// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/rknightion/synthkit/dashboard"
)

// Grafana dashboard and folder UIDs are at most 40 characters of [A-Za-z0-9_-]
// (https://grafana.com/docs/grafana/latest/whatsnew/whats-new-in-v10-1/).
const (
	maxUIDLen       = 40
	maxNamespaceLen = 12
	shortHashLen    = 8
)

var (
	// A namespace is lowercase alphanumerics and underscore only. It deliberately has no
	// hyphen: a derived UID is "<namespace>-<legacy UID>", and legacy UIDs contain hyphens,
	// so a hyphenated namespace could make ("a-b", "c") and ("a", "b-c") derive the same UID.
	namespacePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_]{0,` + fmt.Sprint(maxNamespaceLen-1) + `}$`)
	uidCharPattern   = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)
)

// uidNamespace derives every generated resource identity from (namespace, legacy UID). A nil
// namespace is the legacy behaviour: identities pass through unchanged.
type uidNamespace struct {
	name string
}

// newNamespace validates a namespace before anything is generated or written.
func newNamespace(raw string) (*uidNamespace, error) {
	if !namespacePattern.MatchString(raw) {
		return nil, fmt.Errorf("invalid -namespace %q: want 1-%d characters of a-z, 0-9 and _, starting with a letter or digit (no hyphen, which separates the namespace from the legacy UID)", raw, maxNamespaceLen)
	}
	return &uidNamespace{name: raw}, nil
}

// derive maps a legacy UID to its effective UID: "<namespace>-<legacy>" when that fits the
// 40-character Grafana limit, otherwise "<namespace>-<legacy prefix>-<hash>" where hash is the
// first 8 hex digits of sha256(namespace NUL legacy). Both forms are pure functions of the
// pair, so repeated runs agree; the namespace cannot contain a hyphen, so two namespaces
// never derive the same UID; and within a namespace the hash separates legacy UIDs that
// share a truncated prefix.
func (n *uidNamespace) derive(legacy string) (string, error) {
	if n == nil {
		return legacy, nil
	}
	if !uidCharPattern.MatchString(legacy) {
		return "", fmt.Errorf("cannot namespace identity %q: want characters of A-Z, a-z, 0-9, - and _ only", legacy)
	}
	if full := n.name + "-" + legacy; len(full) <= maxUIDLen {
		return full, nil
	}
	sum := sha256.Sum256([]byte(n.name + "\x00" + legacy))
	hash := hex.EncodeToString(sum[:])[:shortHashLen]
	keep := maxUIDLen - len(n.name) - len("-") - len("-") - shortHashLen
	prefix := strings.TrimRight(legacy[:min(keep, len(legacy))], "-_")
	return n.name + "-" + prefix + "-" + hash, nil
}

// label is the suffix that tells two namespaces' otherwise identical titles and file names apart.
func (n *uidNamespace) label() string {
	if n == nil {
		return ""
	}
	return n.name
}

// namespacePlan is the full set of effective identities for one generation, computed (and
// checked for collisions) before any file is written.
type namespacePlan struct {
	ns      *uidNamespace
	folder  string
	uids    map[string]string // legacy dashboard UID -> effective UID
	linkRE  *regexp.Regexp
	navTags map[string]string // legacy dropdown-link tag -> effective tag
}

func newNamespacePlan(ns *uidNamespace, legacyFolder string, dashboards []dashboard.Dashboard) (*namespacePlan, error) {
	p := &namespacePlan{ns: ns, uids: map[string]string{}, navTags: map[string]string{}}
	var err error
	if p.folder, err = ns.derive(legacyFolder); err != nil {
		return nil, fmt.Errorf("folder: %w", err)
	}
	if ns != nil {
		taken := map[string]string{}
		for _, d := range dashboards {
			eff, derr := ns.derive(d.UID)
			if derr != nil {
				return nil, fmt.Errorf("dashboard: %w", derr)
			}
			if other, dup := taken[eff]; dup {
				return nil, fmt.Errorf("dashboards %q and %q both derive UID %q", other, d.UID, eff)
			}
			taken[eff] = d.UID
			p.uids[d.UID] = eff
		}
	}
	return p, nil
}

// compileLinks builds the matcher for "/d/<generated dashboard UID>" references. Only UIDs
// this run generates are matched: a "/d/<vendor UID>" integration deep link is not ours.
func (p *namespacePlan) compileLinks() {
	legacy := make([]string, 0, len(p.uids))
	for uid := range p.uids {
		legacy = append(legacy, regexp.QuoteMeta(uid))
	}
	if len(legacy) == 0 {
		return
	}
	// Longest first: Go's leftmost-first alternation would otherwise take "a-b" from "/d/a-b-c".
	sort.Slice(legacy, func(i, j int) bool {
		if len(legacy[i]) != len(legacy[j]) {
			return len(legacy[i]) > len(legacy[j])
		}
		return legacy[i] < legacy[j]
	})
	p.linkRE = regexp.MustCompile(`/d/(` + strings.Join(legacy, "|") + `)`)
}

// collectNavTags records every tag a "dashboards"-type link selects on. Those tags group a
// dashboard set for cross-navigation, so they must be namespaced or one install's dropdown
// would list another's dashboards.
func (p *namespacePlan) collectNavTags(root map[string]any) error {
	spec, _ := root["spec"].(map[string]any)
	links, _ := spec["links"].([]any)
	for _, l := range links {
		link, _ := l.(map[string]any)
		if link["type"] != "dashboards" {
			continue
		}
		tags, _ := link["tags"].([]any)
		for _, t := range tags {
			tag, _ := t.(string)
			if _, seen := p.navTags[tag]; seen || tag == "" {
				continue
			}
			eff, err := p.ns.derive(tag)
			if err != nil {
				return fmt.Errorf("dashboard link tag: %w", err)
			}
			p.navTags[tag] = eff
		}
	}
	return nil
}

// rewrite applies the plan to one rendered dashboard: internal /d/ links in any string, the
// tags behind the cross-navigation dropdown, and nothing else (metadata.name and the folder
// annotation are already effective because they were set before rendering).
func (p *namespacePlan) rewrite(root map[string]any) {
	spec, _ := root["spec"].(map[string]any)
	if tags, ok := spec["tags"].([]any); ok {
		spec["tags"] = p.mapTags(tags)
	}
	if links, ok := spec["links"].([]any); ok {
		for _, l := range links {
			if link, ok := l.(map[string]any); ok && link["type"] == "dashboards" {
				if tags, ok := link["tags"].([]any); ok {
					link["tags"] = p.mapTags(tags)
				}
			}
		}
	}
	for k, v := range root {
		root[k] = p.rewriteStrings(v)
	}
}

func (p *namespacePlan) mapTags(tags []any) []any {
	out := make([]any, len(tags))
	for i, t := range tags {
		if tag, ok := t.(string); ok {
			if eff, found := p.navTags[tag]; found {
				out[i] = eff
				continue
			}
		}
		out[i] = t
	}
	return out
}

func (p *namespacePlan) rewriteStrings(v any) any {
	switch node := v.(type) {
	case map[string]any:
		for k, c := range node {
			node[k] = p.rewriteStrings(c)
		}
		return node
	case []any:
		for i, c := range node {
			node[i] = p.rewriteStrings(c)
		}
		return node
	case string:
		return p.rewriteLinks(node)
	default:
		return v
	}
}

// rewriteLinks replaces "/d/<legacy UID>" with its effective UID where the UID is a whole
// token (followed by a non-UID character or the end), so "/d/a-b-c" is never read as "/d/a-b".
func (p *namespacePlan) rewriteLinks(s string) string {
	if p.linkRE == nil || !strings.Contains(s, "/d/") {
		return s
	}
	var b strings.Builder
	last := 0
	for _, m := range p.linkRE.FindAllStringSubmatchIndex(s, -1) {
		end := m[3]
		if end < len(s) && isUIDChar(s[end]) {
			continue
		}
		b.WriteString(s[last:m[2]])
		b.WriteString(p.uids[s[m[2]:end]])
		last = end
	}
	b.WriteString(s[last:])
	return b.String()
}

func isUIDChar(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-' || c == '_'
}

// applyRules namespaces the folder and rule identities of generated rule groups in place.
func (p *namespacePlan) applyRules(groups []dashboard.RuleGroup) error {
	if p.ns == nil {
		return nil
	}
	var err error
	for gi := range groups {
		g := &groups[gi]
		if g.FolderUID != "" {
			if g.FolderUID, err = p.ns.derive(g.FolderUID); err != nil {
				return fmt.Errorf("rule group %q folder: %w", g.Name, err)
			}
		}
		for i := range g.Recordings {
			if g.Recordings[i].UID, err = p.deriveOptional(g.Recordings[i].UID); err != nil {
				return fmt.Errorf("rule group %q recording rule: %w", g.Name, err)
			}
		}
		for i := range g.Alerts {
			if g.Alerts[i].UID, err = p.deriveOptional(g.Alerts[i].UID); err != nil {
				return fmt.Errorf("rule group %q alert rule: %w", g.Name, err)
			}
		}
	}
	return nil
}

func (p *namespacePlan) deriveOptional(uid string) (string, error) {
	if uid == "" {
		return "", nil
	}
	return p.ns.derive(uid)
}

// reencode re-marshals a rewritten document with the same indentation Render uses.
func reencode(root map[string]any) ([]byte, error) {
	return json.MarshalIndent(root, "", "  ")
}
