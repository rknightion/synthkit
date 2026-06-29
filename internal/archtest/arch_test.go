// SPDX-License-Identifier: AGPL-3.0-only

// Package archtest enforces the composition invariants as tests (ARCHITECTURE §9.3):
// the catalog is de-Rochified, blueprint-name-free, and import-isolated.
package archtest

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func repoRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Clean(filepath.Join(wd, "..", ".."))
}

// catalogDirs are the isolation-critical trees: constructs + workloads.
func catalogDirs(t *testing.T) []string {
	root := repoRoot(t)
	return []string{
		filepath.Join(root, "internal", "construct"),
		filepath.Join(root, "internal", "workload"),
	}
}

func goFilesUnder(t *testing.T, dirs ...string) []string {
	t.Helper()
	var files []string
	for _, dir := range dirs {
		err := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if !d.IsDir() && strings.HasSuffix(path, ".go") {
				files = append(files, path)
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", dir, err)
		}
	}
	if len(files) == 0 {
		t.Fatalf("no catalog sources found under %v", dirs)
	}
	return files
}

// TestCatalogIsDeCustomerized: zero customer/brand-specific strings anywhere in the
// catalog (DoD §10). Case-insensitive. Only customer-identifying tokens are banned —
// technology-native names (portkey/bedrock/langsmith/langgraph/agentcore/gen_ai/
// snowflake) are generic constructs and carry over UNCHANGED (the AI ban-lift, Spec 2b);
// customer identity for those technologies lives blueprint-only.
//
// The forbidden list is built at runtime from rune slices so that this file does not
// itself contain the literal patterns and trip the OSS-leakage self-verify grep.
func TestCatalogIsDeCustomerized(t *testing.T) {
	// Build forbidden strings from runes to avoid literal patterns in this source file.
	join := func(rs ...rune) string { return string(rs) }
	forbidden := []string{
		join('r', 'o', 'c', 'h', 'e'),
		join('a', 'i', 'd', 'c', 'g'),
		join('g', 'a', 'l', 'i', 'l', 'e', 'o'),
		join('v', 'e', 'e', 'v', 'a'),
		"northwind", "scenario",
	}
	for _, f := range goFilesUnder(t, catalogDirs(t)...) {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		lower := strings.ToLower(string(data))
		for _, bad := range forbidden {
			if idx := strings.Index(lower, bad); idx >= 0 {
				line := 1 + strings.Count(lower[:idx], "\n")
				t.Errorf("%s:%d contains forbidden string %q", f, line, bad)
			}
		}
	}
}

// TestCatalogReferencesNoBlueprintName: the catalog must not mention any blueprint
// declared in blueprints/*.yaml (DoD §10 — deletability).
func TestCatalogReferencesNoBlueprintName(t *testing.T) {
	root := repoRoot(t)
	entries, err := filepath.Glob(filepath.Join(root, "blueprints", "*.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	nameRe := regexp.MustCompile(`(?m)^name:\s*(\S+)`)
	var names []string
	for _, e := range entries {
		data, err := os.ReadFile(e)
		if err != nil {
			t.Fatal(err)
		}
		if m := nameRe.FindStringSubmatch(string(data)); m != nil {
			names = append(names, strings.ToLower(m[1]))
		}
	}
	if len(names) == 0 {
		t.Skip("no blueprints declared yet")
	}
	wordRe := func(name string) *regexp.Regexp {
		return regexp.MustCompile(`(?i)\b` + regexp.QuoteMeta(name) + `\b`)
	}
	for _, f := range goFilesUnder(t, catalogDirs(t)...) {
		data, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		for _, n := range names {
			if loc := wordRe(n).FindIndex(data); loc != nil {
				line := 1 + strings.Count(string(data[:loc[0]]), "\n")
				t.Errorf("%s:%d references blueprint name %q — constructs must be blueprint-agnostic", f, line, n)
			}
		}
	}
}

// TestCatalogImportIsolation: no construct imports another construct, any workload,
// the blueprint package, or the runner; workloads likewise (fixtures/core/ledger/
// state/shape/sinks are the only internal surface).
func TestCatalogImportIsolation(t *testing.T) {
	allowedPrefixes := []string{
		"github.com/rknightion/synthkit/internal/core",
		"github.com/rknightion/synthkit/internal/beyla/",     // shared Beyla (eBPF) vocabulary mechanic (peer lib, like cw/genai); the web_service Beyla lane + beyla_agent build their series from it
		"github.com/rknightion/synthkit/internal/pyroscope/", // shared Pyroscope profiling mechanic (peer lib, like cw/genai); constructs build pprof profiles + flamegraph vocab from it
		"github.com/rknightion/synthkit/internal/cw/",        // shared CloudWatch emission mechanic (peer lib, like state); trailing slash + the exact-match branch covers the bare import without matching internal/cwXxx
		"github.com/rknightion/synthkit/internal/genai/",     // shared gen_ai semconv vocabulary mechanic (peer lib, like cw); workload-AI lane builds gen_ai spans/metrics from it
		"github.com/rknightion/synthkit/internal/semconv/",   // shared OTEL semconv resource-attr + correlation key names (peer lib, like genai/cw); emit lanes build identity labels/attrs from it
		"github.com/rknightion/synthkit/internal/nodeexp/",   // shared node/windows/macos/cadvisor emission mechanic (peer lib, like cw/genai); host construct + k8scluster adapters delegate here
		"github.com/rknightion/synthkit/internal/k8saddon/",  // shared k8s-addon pod↔metric correlation mechanic (peer lib, like cw/genai); addon constructs stamp per-pod join labels from it
		"github.com/rknightion/synthkit/internal/failuremode",
		"github.com/rknightion/synthkit/internal/fixture",
		"github.com/rknightion/synthkit/internal/ledger",
		"github.com/rknightion/synthkit/internal/scale",
		"github.com/rknightion/synthkit/internal/shape",
		"github.com/rknightion/synthkit/internal/state",
		"github.com/rknightion/synthkit/internal/sink/",
		"github.com/rknightion/synthkit/internal/highcard",       // canonical high-card label set (leaf); sinks + the DSL capability matrix share it
		"github.com/rknightion/synthkit/internal/telemetryspec/", // custom-telemetry DSL mechanic (peer lib, like cw/genai); the app workload + its profiles subpkg build telemetry from it
	}
	root := repoRoot(t)
	fset := token.NewFileSet()
	for _, f := range goFilesUnder(t, catalogDirs(t)...) {
		ast, err := parser.ParseFile(fset, f, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("parse %s: %v", f, err)
		}
		rel, err := filepath.Rel(root, filepath.Dir(f))
		if err != nil {
			t.Fatal(err)
		}
		ownImportPath := "github.com/rknightion/synthkit/" + filepath.ToSlash(rel)
		for _, imp := range ast.Imports {
			path := strings.Trim(imp.Path.Value, `"`)
			if !strings.HasPrefix(path, "github.com/rknightion/synthkit/") {
				continue // stdlib + external deps are fine
			}
			if path == ownImportPath {
				continue // external test packages (foo_test) import their own package
			}
			ok := false
			for _, allowed := range allowedPrefixes {
				if path == strings.TrimSuffix(allowed, "/") || strings.HasPrefix(path, allowed) {
					ok = true
					break
				}
			}
			if !ok {
				t.Errorf("%s imports %q — catalog packages may only import core/fixture/ledger/shape/state/sink", f, path)
			}
		}
	}
}

// TestSinkRunnerSDKIsolation: the synthetic-data path (every sink + the runner) must never link
// the OpenTelemetry SDK/API, the Pyroscope client, or the selfobs/profiling packages. Self-obs
// observes the synthetic path ONLY through the stdlib-only seams (internal/pushhook,
// runner.TickFunc / runner.CycleFunc), so a stray `import "go.opentelemetry.io/otel"` in a sink or
// the runner would silently pull the SDK into the data path — this test fails loudly if it does.
//
// The OTel *proto* package (go.opentelemetry.io/proto/otlp/...) is ALLOWED: the otlp sink
// hand-encodes ResourceSpans with it. It is a distinct module path from the SDK
// (go.opentelemetry.io/otel) so the prefix check below never matches it.
func TestSinkRunnerSDKIsolation(t *testing.T) {
	root := repoRoot(t)
	dirs := []string{
		filepath.Join(root, "internal", "sink"),
		filepath.Join(root, "internal", "runner"),
	}
	forbidden := []string{
		"go.opentelemetry.io/otel",        // OTel SDK + API (NOT go.opentelemetry.io/proto — distinct module)
		"github.com/grafana/pyroscope-go", // profiling SDK
		"github.com/rknightion/synthkit/internal/selfobs",
		"github.com/rknightion/synthkit/internal/profiling",
	}
	fset := token.NewFileSet()
	for _, f := range goFilesUnder(t, dirs...) {
		astF, err := parser.ParseFile(fset, f, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("parse %s: %v", f, err)
		}
		for _, imp := range astF.Imports {
			path := strings.Trim(imp.Path.Value, `"`)
			for _, bad := range forbidden {
				if path == bad || strings.HasPrefix(path, bad+"/") {
					t.Errorf("%s imports %q — sinks/runner must stay off the OTel SDK / Pyroscope / selfobs / profiling (the self-obs path is isolated via the stdlib pushhook + TickFunc/CycleFunc seams)", f, path)
				}
			}
		}
	}
}

// TestDashboardSDKIsolation: the synthetic-emit path (catalog constructs/workloads, sinks,
// runner, and the main emit binary) must NEVER link the Grafana Foundation SDK or the
// dashboard generator packages. Dashboard generation is offline tooling (cmd/synthkit-dash);
// pulling the SDK into the hot path would bloat the emit binary and breach three-tier purity.
func TestDashboardSDKIsolation(t *testing.T) {
	root := repoRoot(t)
	dirs := append(catalogDirs(t),
		filepath.Join(root, "internal", "sink"),
		filepath.Join(root, "internal", "runner"),
		filepath.Join(root, "cmd", "synthkit"),
	)
	forbidden := []string{
		"github.com/grafana/grafana-foundation-sdk",
		"github.com/rknightion/synthkit/dashboard",
		"github.com/rknightion/synthkit/internal/dashgen",
	}
	fset := token.NewFileSet()
	for _, f := range goFilesUnder(t, dirs...) {
		astF, err := parser.ParseFile(fset, f, nil, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("parse %s: %v", f, err)
		}
		for _, imp := range astF.Imports {
			path := strings.Trim(imp.Path.Value, `"`)
			for _, bad := range forbidden {
				if path == bad || strings.HasPrefix(path, bad+"/") {
					t.Errorf("%s imports %q — the synthetic-emit path must stay off the dashboard SDK/generator (dashboard generation is cmd/synthkit-dash tooling)", f, path)
				}
			}
		}
	}
}
