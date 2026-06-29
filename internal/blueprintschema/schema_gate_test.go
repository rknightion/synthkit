// SPDX-License-Identifier: AGPL-3.0-only

package blueprintschema_test

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/rknightion/synthkit/internal/blueprintschema"
	"github.com/rknightion/synthkit/internal/runner"
)

// moduleRoot locates the repo root from this test file's location.
func moduleRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	root, err := blueprintschema.FindModuleRoot(filepath.Dir(file))
	if err != nil {
		t.Fatalf("find module root: %v", err)
	}
	return root
}

// TestSchemaCurrent is the drift gate: the committed BLUEPRINT-SCHEMA.md + the embedded
// fielddocs.json MUST match what regenerating from the live Go types produces. A new/changed
// blueprint or construct/workload config field, or an edited doc comment, fails this test until
// `make blueprint-schema` is re-run — the same drift-proof pattern as TestEnvSurfaceAligned.
func TestSchemaCurrent(t *testing.T) {
	root := moduleRoot(t)
	docsJSON, markdown, err := blueprintschema.Generate(runner.Catalog(), root)
	if err != nil {
		t.Fatalf("generate: %v", err)
	}

	for _, tc := range []struct {
		path string
		want []byte
	}{
		{blueprintschema.MarkdownPath, []byte(markdown)},
		{blueprintschema.DocsIndexPath, docsJSON},
	} {
		got, err := os.ReadFile(filepath.Join(root, tc.path))
		if err != nil {
			t.Fatalf("read %s: %v", tc.path, err)
		}
		if string(got) != string(tc.want) {
			t.Errorf("%s is STALE — regenerate with `make blueprint-schema` (go run ./cmd/blueprint-schema)", tc.path)
		}
	}
}

// TestSchemaCoverage sanity-checks that the schema includes the blueprint document, every
// registered construct/workload kind as its own config section, and the failure-mode vocab.
func TestSchemaCoverage(t *testing.T) {
	reg := runner.Catalog()
	d := blueprintschema.Build(reg, nil)

	byKind := map[string]bool{}
	var hasBlueprint, hasFailureModes bool
	for _, s := range d.Sections {
		switch s.Group {
		case "blueprint":
			hasBlueprint = true
			if len(s.Fields) == 0 {
				t.Error("blueprint document section has no fields")
			}
		case "failure_modes":
			hasFailureModes = true
		}
		if s.Kind != "" {
			byKind[s.Kind] = true
		}
	}
	if !hasBlueprint {
		t.Error("missing blueprint document section")
	}
	if !hasFailureModes {
		t.Error("missing failure modes section")
	}
	for _, k := range reg.ConstructKinds() {
		if !byKind[k] {
			t.Errorf("construct kind %q missing a config section", k)
		}
	}
	for _, k := range reg.WorkloadKinds() {
		if !byKind[k] {
			t.Errorf("workload kind %q missing a config section", k)
		}
	}
}

// TestCustomDecodeKeysPresent pins the customDecodeFields override (H1 regression): the wiring
// keys of the custom-UnmarshalYAML blueprint structs (WorkloadDecl, AddonRef) must appear in
// the schema. Reflection cannot see them (no yaml tags), so if the override is dropped these
// keys silently vanish and this fails.
func TestCustomDecodeKeysPresent(t *testing.T) {
	d := blueprintschema.Build(runner.Catalog(), nil)
	var bp *blueprintschema.Section
	for i := range d.Sections {
		if d.Sections[i].Group == "blueprint" {
			bp = &d.Sections[i]
			break
		}
	}
	if bp == nil {
		t.Fatal("no blueprint document section")
	}
	keys := map[string]bool{}
	var walk func(prefix string, fs []blueprintschema.Field)
	walk = func(prefix string, fs []blueprintschema.Field) {
		for _, f := range fs {
			k := prefix + f.Key
			if f.Repeated {
				k += "[]"
			}
			keys[k] = true
			walk(k+".", f.Fields)
		}
	}
	walk("", bp.Fields)

	for _, want := range []string{
		"workloads[].type", "workloads[].name", "workloads[].runs_on",
		"workloads[].replicas", "workloads[].calls[].db", "workloads[].calls[].cache",
		"environments[].cluster.addons[].name",
	} {
		if !keys[want] {
			t.Errorf("schema missing custom-decode key %q (customDecodeFields override dropped?)", want)
		}
	}
}
