// SPDX-License-Identifier: AGPL-3.0-only

// Command conformance compares observed dump names with documented signal names.
package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	DumpPrometheus  = "prometheus"
	DumpOTLPMetrics = "otlp_metrics"
)

type Shape struct {
	Keys []string `json:"keys"`
}
type Dump struct{ Metrics map[string]map[string]Shape }
type Gap struct {
	File   string `json:"file"`
	Family string `json:"family"`
	Error  string `json:"error"`
}
type Contract struct {
	YAMLBlocks int
	ParseGaps  []Gap
	Names      map[string]map[string]bool
	// OptIn lists the blocks fenced as `yaml signals opt-in`. Their names are
	// documented (they resolve in Compare) but emitted only when the blueprint
	// field each block names is set; they are never part of a default block.
	OptIn []OptInBlock

	collect      map[string]bool // final names of the block being added, when non-nil
	defaultNames map[string]string
}

// OptInTag is the fence info-string tag that marks an opt-in block. A block fenced
// exactly `yaml signals` is the default-emission contract; any other tag is a gap.
const OptInTag = "opt-in"

// OptInBlock is one opt-in block: the names it declares and the dimension key sets
// each name may carry. A name with no declared profiles has its key set unchecked.
type OptInBlock struct {
	File   string
	Family string
	// Field is the blueprint field path (BLUEPRINT-SCHEMA.md spelling, prefixed by
	// its declaration) that enables emission, from the block's `opt_in:` key.
	Field string
	// Source is the provenance the block cites, from its `source:` key.
	Source string
	// Names maps each final name to its allowed sorted, comma-joined profile key sets.
	Names map[string][]string
	// ProfileKeys is the union of every declared profile key in the block. The
	// emission check also treats every dimension_* label as a profile key.
	ProfileKeys map[string]bool
}

// EmittedSeries is one series observed from the engine: its final name and labels.
type EmittedSeries struct {
	Name   string
	Labels map[string]string
}

type Report struct {
	YAMLBlocks      int      `json:"yaml_blocks"`
	OptInBlocks     int      `json:"opt_in_blocks"`
	ParseGaps       []Gap    `json:"parse_gaps"`
	Resolved        int      `json:"resolved"`
	Total           int      `json:"total"`
	UnresolvedNames []string `json:"unresolved_names"`
	Limitations     string   `json:"limitations"`
}

var blockRE = regexp.MustCompile("(?s)```ya?ml signals([^\\n]*)\\n(.*?)\\n```")
var codeRE = regexp.MustCompile("`([^`\\n]+)`")
var textBlockRE = regexp.MustCompile("(?s)```text[^\\n]*\\n(.*?)\\n```")
var headingRE = regexp.MustCompile(`(?m)^#{1,6}\s+.*$`)
var nameRE = regexp.MustCompile(`^[A-Za-z_:][A-Za-z0-9_:.{}*,|/-]*$`)

var cwStatSuffixes = []string{"_sum", "_average", "_maximum", "_minimum", "_sample_count"}

func ParseDump(r io.Reader) (Dump, error) {
	d := Dump{Metrics: map[string]map[string]Shape{DumpPrometheus: {}, DumpOTLPMetrics: {}}}
	section := ""
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 4096), 4*1024*1024)
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, "== ") {
			section = ""
			if strings.HasPrefix(line, "== metrics: series") {
				section = DumpPrometheus
			}
			if strings.HasPrefix(line, "== otlp metrics: series") {
				section = DumpOTLPMetrics
			}
			continue
		}
		if section == "" {
			continue
		}
		at := strings.Index(line, "  {")
		if at < 0 {
			continue
		}
		name := strings.TrimSpace(line[:at])
		keys := strings.Fields(strings.Trim(strings.TrimSpace(line[at:]), "{}[] "))
		sort.Strings(keys)
		d.Metrics[section][name] = Shape{Keys: keys}
	}
	return d, sc.Err()
}

func expand(name string) []string {
	start := strings.IndexByte(name, '{')
	if start < 0 {
		return []string{name}
	}
	end := strings.IndexByte(name[start:], '}')
	if end < 0 {
		return []string{name}
	}
	end += start
	var out []string
	for _, part := range strings.Split(name[start+1:end], ",") {
		out = append(out, expand(name[:start]+part+name[end+1:])...)
	}
	return out
}
func (c *Contract) add(section, name, kind string) {
	name = strings.TrimSpace(name)
	if !nameRE.MatchString(name) {
		return
	}
	for _, n := range expand(name) {
		c.mark(section, n)
		if section == DumpPrometheus && (kind == "histogram" || kind == "summary") {
			for _, suffix := range []string{"_sum", "_count"} {
				c.mark(section, n+suffix)
			}
			if kind == "histogram" {
				c.mark(section, n+"_bucket")
			}
		}
	}
}
func (c *Contract) mark(section, name string) {
	c.Names[section][name] = true
	if c.collect != nil {
		c.collect[section+"\x00"+name] = true
	}
}
func list(v any) []string {
	var out []string
	switch x := v.(type) {
	case string:
		out = []string{x}
	case []any:
		for _, a := range x {
			if s, ok := a.(string); ok {
				out = append(out, s)
			}
		}
	}
	return out
}
func str(v any) string { s, _ := v.(string); return s }

// cloudWatchSourceBase translates the source identity printed by a CloudWatch
// metric stream into the documented pre-mangled Prometheus base name. The
// source identity has no statistic, so Compare matches it only against a
// documented five-stat expansion.
func cloudWatchSourceBase(name string) (string, bool) {
	const sourcePrefix = "amazonaws.com/"
	if !strings.HasPrefix(name, sourcePrefix) {
		return "", false
	}
	parts := strings.Split(strings.TrimPrefix(name, sourcePrefix), "/")
	if len(parts) != 3 || parts[0] != "AWS" || parts[1] == "" || parts[2] == "" {
		return "", false
	}
	var metric strings.Builder
	previousLowerOrDigit := false
	for _, r := range parts[2] {
		switch {
		case r >= 'A' && r <= 'Z':
			if previousLowerOrDigit {
				metric.WriteByte('_')
			}
			metric.WriteRune(r + ('a' - 'A'))
			previousLowerOrDigit = false
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			metric.WriteRune(r)
			previousLowerOrDigit = true
		case r == '_' || r == '.':
			metric.WriteByte('_')
			previousLowerOrDigit = false
		case r == '%':
			metric.WriteString("_percent")
			previousLowerOrDigit = false
		default:
			return "", false
		}
	}
	return "aws_" + strings.ToLower(parts[1]) + "_" + metric.String(), true
}

func documentedCloudWatchStat(names map[string]bool, source string) bool {
	base, ok := cloudWatchSourceBase(source)
	if !ok {
		return false
	}
	for _, suffix := range cwStatSuffixes {
		if names[base+suffix] {
			return true
		}
	}
	return false
}

func (c *Contract) addYAMLBlock(b map[string]any) {
	sink := str(b["sink"])
	section := DumpPrometheus
	if sink == "otlp" || sink == "otlp_metrics" {
		section = DumpOTLPMetrics
	}
	if sink == "loki" || sink == "otlp_logs" || sink == "otlp_traces" || sink == "pyroscope" || sink == "sigil" {
		return
	}
	family := str(b["family"])
	stats := list(b["stats"])
	var walk func(any)
	walk = func(v any) {
		switch x := v.(type) {
		case map[string]any:
			if root := str(x["root"]); root != "" {
				kind := str(x["type"])
				c.add(section, root, kind)
				if len(stats) > 0 {
					base := root
					if family != "" && !strings.HasPrefix(root, family+"_") {
						base = family + "_" + root
					}
					for _, stat := range stats {
						c.add(section, base+stat, kind)
					}
				}
			}
			for key, z := range x {
				if key == "info_series" {
					for _, n := range list(z) {
						c.add(section, n, "gauge")
					}
				}
				walk(z)
			}
		case []any:
			for _, z := range x {
				walk(z)
			}
		}
	}
	walk(b)
}

func (c *Contract) addProseNameInSection(section, name string) {
	name = strings.TrimSpace(strings.Trim(name, ",;()[]."))
	if !nameRE.MatchString(name) || !strings.ContainsAny(name, "_.") {
		return
	}
	c.add(section, name, "")
}

func (c *Contract) addProseName(name string) {
	section := DumpPrometheus
	if strings.Contains(name, ".") {
		section = DumpOTLPMetrics
	}
	c.addProseNameInSection(section, name)
}

func ParseSignals(files map[string]string) (Contract, error) {
	c := Contract{Names: map[string]map[string]bool{DumpPrometheus: {}, DumpOTLPMetrics: {}}, ParseGaps: []Gap{}, defaultNames: map[string]string{}}
	paths := make([]string, 0, len(files))
	for p := range files {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, path := range paths {
		content := files[path]
		for _, match := range blockRE.FindAllStringSubmatch(content, -1) {
			c.YAMLBlocks++
			tag := strings.TrimSpace(match[1])
			if tag != "" && tag != OptInTag {
				c.ParseGaps = append(c.ParseGaps, Gap{path, blockFamily(match[2]), fmt.Sprintf("unknown yaml signals tag %q (want none or %q)", tag, OptInTag)})
				continue
			}
			decoder := yaml.NewDecoder(strings.NewReader(match[2]))
			for {
				var b map[string]any
				err := decoder.Decode(&b)
				if err == io.EOF {
					break
				}
				if err == nil {
					if tag == OptInTag {
						c.addOptInBlock(path, b)
						continue
					}
					if _, ok := b["opt_in"]; ok {
						c.ParseGaps = append(c.ParseGaps, Gap{path, str(b["family"]), "opt_in key in a default block; fence it as `yaml signals " + OptInTag + "`"})
					}
					c.collect = map[string]bool{}
					c.addYAMLBlock(b)
					for key := range c.collect {
						c.defaultNames[key] = path
					}
					c.collect = nil
					continue
				}
				c.ParseGaps = append(c.ParseGaps, Gap{path, blockFamily(match[2]), err.Error()})
				break
			}
		}
		// Prose names are section-aware: dotted names describe native OTLP names.
		// Underscore names remain Prometheus names; no automatic wire-name conversion.
		prose := blockRE.ReplaceAllString(content, "")
		for _, m := range codeRE.FindAllStringSubmatch(prose, -1) {
			n := m[1]
			if !strings.Contains(n, "{") && strings.Contains(n, ",") {
				for _, candidate := range strings.Split(n, ",") {
					c.addProseName(candidate)
				}
				continue
			}
			c.addProseName(n)
		}
		for _, match := range textBlockRE.FindAllStringSubmatchIndex(prose, -1) {
			nativeOTLP := false
			headings := headingRE.FindAllString(prose[:match[0]], -1)
			if len(headings) > 0 {
				heading := headings[len(headings)-1]
				if strings.Contains(strings.ToLower(heading), "native otlp") {
					nativeOTLP = true
				}
			}
			for _, token := range strings.Fields(prose[match[2]:match[3]]) {
				if nativeOTLP {
					c.addProseNameInSection(DumpOTLPMetrics, token)
				} else {
					c.addProseName(token)
				}
			}
		}
	}
	c.checkOptInDisjoint()
	return c, nil
}

func blockFamily(text string) string {
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "family:") {
			return strings.TrimSpace(strings.TrimPrefix(line, "family:"))
		}
	}
	return ""
}

// addOptInBlock validates and records one opt-in block. Its names join the
// documented contract either way; only a valid block is recorded in c.OptIn.
func (c *Contract) addOptInBlock(path string, b map[string]any) {
	family := str(b["family"])
	gap := func(msg string) { c.ParseGaps = append(c.ParseGaps, Gap{path, family, msg}) }
	c.addYAMLBlock(b)
	field, source := strings.TrimSpace(str(b["opt_in"])), strings.TrimSpace(str(b["source"]))
	if field == "" {
		gap("opt-in block without an opt_in blueprint field")
		return
	}
	if source == "" {
		gap("opt-in block without a source")
		return
	}
	raw, err := yaml.Marshal(b)
	if err != nil {
		gap(err.Error())
		return
	}
	var typed struct {
		Family            string              `yaml:"family"`
		Stats             []string            `yaml:"stats"`
		DimensionProfiles map[string][]string `yaml:"dimension_profiles"`
		Metrics           []struct {
			Root     string   `yaml:"root"`
			Profiles []string `yaml:"profiles"`
		} `yaml:"metrics"`
	}
	if err := yaml.Unmarshal(raw, &typed); err != nil {
		gap("opt-in block: " + err.Error())
		return
	}
	if len(typed.Metrics) == 0 {
		gap("opt-in block without metrics")
		return
	}
	block := OptInBlock{File: path, Family: family, Field: field, Source: source, Names: map[string][]string{}, ProfileKeys: map[string]bool{}}
	for _, keys := range typed.DimensionProfiles {
		for _, k := range keys {
			block.ProfileKeys[k] = true
		}
	}
	for _, m := range typed.Metrics {
		if m.Root == "" || !nameRE.MatchString(m.Root) || strings.ContainsAny(m.Root, "{}*,|") {
			gap(fmt.Sprintf("opt-in block metric root %q is not one concrete name", m.Root))
			return
		}
		var profiles []string
		for _, p := range m.Profiles {
			keys, ok := typed.DimensionProfiles[p]
			if !ok {
				gap(fmt.Sprintf("opt-in metric %s names unknown profile %q", m.Root, p))
				return
			}
			keys = append([]string(nil), keys...)
			sort.Strings(keys)
			profiles = append(profiles, strings.Join(keys, ","))
		}
		base := m.Root
		if family != "" && !strings.HasPrefix(base, family+"_") {
			base = family + "_" + base
		}
		finals := []string{base}
		if len(typed.Stats) > 0 {
			finals = finals[:0]
			for _, stat := range typed.Stats {
				finals = append(finals, base+stat)
			}
		}
		for _, n := range finals {
			block.Names[n] = profiles
		}
	}
	c.OptIn = append(c.OptIn, block)
}

// checkOptInDisjoint rejects an opt-in name that a default block also lists: the
// default blocks are the default-emission contract, so a name cannot be both.
func (c *Contract) checkOptInDisjoint() {
	for _, block := range c.OptIn {
		names := make([]string, 0, len(block.Names))
		for n := range block.Names {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, n := range names {
			for _, section := range []string{DumpPrometheus, DumpOTLPMetrics} {
				if where, ok := c.defaultNames[section+"\x00"+n]; ok {
					c.ParseGaps = append(c.ParseGaps, Gap{block.File, block.Family, fmt.Sprintf("opt-in name %s is also in a default block (%s)", n, where)})
				}
			}
		}
	}
}

// CheckOptInEmission compares one opt-in block with the series an engine run
// emitted with its opt_in field set (enabled) and with it unset (disabled). Every
// declared name and profile must be emitted when enabled, with no undeclared key
// set, and none of the block's names may be emitted when disabled.
func CheckOptInEmission(block OptInBlock, enabled, disabled []EmittedSeries) []string {
	var problems []string
	seen := map[string]map[string]bool{}
	for _, s := range enabled {
		allowed, ok := block.Names[s.Name]
		if !ok {
			continue
		}
		var keys []string
		for k := range s.Labels {
			// Any CloudWatch dimension label counts, declared or not, so an
			// undeclared dimension cannot hide from the profile comparison.
			if block.ProfileKeys[k] || strings.HasPrefix(k, "dimension_") {
				keys = append(keys, k)
			}
		}
		sort.Strings(keys)
		profile := strings.Join(keys, ",")
		if seen[s.Name] == nil {
			seen[s.Name] = map[string]bool{}
		}
		seen[s.Name][profile] = true
		if len(allowed) > 0 && !contains(allowed, profile) {
			problems = append(problems, fmt.Sprintf("%s emitted with undeclared profile {%s}", s.Name, profile))
		}
	}
	names := make([]string, 0, len(block.Names))
	for n := range block.Names {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		if seen[n] == nil {
			problems = append(problems, fmt.Sprintf("%s not emitted with %s set", n, block.Field))
			continue
		}
		for _, p := range block.Names[n] {
			if !seen[n][p] {
				problems = append(problems, fmt.Sprintf("%s profile {%s} not emitted with %s set", n, p, block.Field))
			}
		}
	}
	leaked := map[string]bool{}
	for _, s := range disabled {
		if _, ok := block.Names[s.Name]; ok && !leaked[s.Name] {
			leaked[s.Name] = true
			problems = append(problems, fmt.Sprintf("%s emitted without %s", s.Name, block.Field))
		}
	}
	sort.Strings(problems)
	return problems
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}

func Compare(c Contract, d Dump) Report {
	r := Report{YAMLBlocks: c.YAMLBlocks, OptInBlocks: len(c.OptIn), ParseGaps: c.ParseGaps, UnresolvedNames: []string{}, Limitations: "Name resolution only. Labels are retained from the dump but not validated. A documented prose mention is name evidence, not proof of implementation or a valid envelope. Unresolved names and parse gaps are not proven renderer defects."}
	for section, names := range d.Metrics {
		for name := range names {
			r.Total++
			found := c.Names[section][name]
			if !found && section == DumpOTLPMetrics {
				// CloudWatch metric streams arrive as OTLP source identities.
				// Their documented contract is the promrw five-stat expansion.
				found = documentedCloudWatchStat(c.Names[DumpPrometheus], name)
			}
			if !found {
				for pattern := range c.Names[section] {
					if !strings.Contains(pattern, "*") {
						continue
					}
					if ok, _ := filepath.Match(pattern, name); ok {
						found = true
						break
					}
				}
			}
			if found {
				r.Resolved++
			} else {
				r.UnresolvedNames = append(r.UnresolvedNames, name)
			}
		}
	}
	sort.Strings(r.UnresolvedNames)
	return r
}
func run() error {
	signalDir := flag.String("signals", "signals", "signals directory")
	dumpPath := flag.String("dump", "", "explicit-selection dump file")
	flag.Parse()
	if *dumpPath == "" {
		return fmt.Errorf("-dump is required")
	}
	paths, err := filepath.Glob(filepath.Join(*signalDir, "*.md"))
	if err != nil {
		return err
	}
	if len(paths) == 0 {
		return fmt.Errorf("no signals documents")
	}
	files := map[string]string{}
	for _, p := range paths {
		b, e := os.ReadFile(p)
		if e != nil {
			return e
		}
		files[p] = string(b)
	}
	contract, err := ParseSignals(files)
	if err != nil {
		return err
	}
	f, err := os.Open(*dumpPath)
	if err != nil {
		return err
	}
	defer f.Close()
	dump, err := ParseDump(f)
	if err != nil {
		return err
	}
	report := Compare(contract, dump)
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(report)
}
func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
