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
}
type Report struct {
	YAMLBlocks      int      `json:"yaml_blocks"`
	ParseGaps       []Gap    `json:"parse_gaps"`
	Resolved        int      `json:"resolved"`
	Total           int      `json:"total"`
	UnresolvedNames []string `json:"unresolved_names"`
	Limitations     string   `json:"limitations"`
}

var blockRE = regexp.MustCompile("(?s)```ya?ml signals[^\\n]*\\n(.*?)\\n```")
var codeRE = regexp.MustCompile("`([^`\\n]+)`")
var nameRE = regexp.MustCompile(`^[A-Za-z_:][A-Za-z0-9_:.{}*,|/-]*$`)

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
		c.Names[section][n] = true
		if section == DumpPrometheus && (kind == "histogram" || kind == "summary") {
			for _, suffix := range []string{"_sum", "_count"} {
				c.Names[section][n+suffix] = true
			}
			if kind == "histogram" {
				c.Names[section][n+"_bucket"] = true
			}
		}
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

func ParseSignals(files map[string]string) (Contract, error) {
	c := Contract{Names: map[string]map[string]bool{DumpPrometheus: {}, DumpOTLPMetrics: {}}, ParseGaps: []Gap{}}
	paths := make([]string, 0, len(files))
	for p := range files {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, path := range paths {
		content := files[path]
		for _, match := range blockRE.FindAllStringSubmatch(content, -1) {
			c.YAMLBlocks++
			var b map[string]any
			if err := yaml.Unmarshal([]byte(match[1]), &b); err != nil {
				family := ""
				for _, line := range strings.Split(match[1], "\n") {
					line = strings.TrimSpace(line)
					if strings.HasPrefix(line, "family:") {
						family = strings.TrimSpace(strings.TrimPrefix(line, "family:"))
						break
					}
				}
				c.ParseGaps = append(c.ParseGaps, Gap{path, family, err.Error()})
				continue
			}
			sink := str(b["sink"])
			section := DumpPrometheus
			if sink == "otlp" || sink == "otlp_metrics" {
				section = DumpOTLPMetrics
			}
			if sink == "loki" || sink == "otlp_logs" || sink == "otlp_traces" || sink == "pyroscope" || sink == "sigil" {
				continue
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
		// Prose names are section-aware: dotted names describe native OTLP names.
		// Underscore names remain Prometheus names; no automatic wire-name conversion.
		prose := blockRE.ReplaceAllString(content, "")
		for _, m := range codeRE.FindAllStringSubmatch(prose, -1) {
			n := m[1]
			if !nameRE.MatchString(n) {
				continue
			}
			if !strings.ContainsAny(n, "_.") {
				continue
			}
			section := DumpPrometheus
			if strings.Contains(n, ".") {
				section = DumpOTLPMetrics
			}
			c.add(section, n, "")
		}
	}
	return c, nil
}
func Compare(c Contract, d Dump) Report {
	r := Report{YAMLBlocks: c.YAMLBlocks, ParseGaps: c.ParseGaps, UnresolvedNames: []string{}, Limitations: "Name resolution only. Labels are retained from the dump but not validated. A documented prose mention is name evidence, not proof of implementation or a valid envelope. Unresolved names and parse gaps are not proven renderer defects."}
	for section, names := range d.Metrics {
		for name := range names {
			r.Total++
			found := c.Names[section][name]
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
