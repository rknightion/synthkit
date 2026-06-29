// SPDX-License-Identifier: AGPL-3.0-only

// Package blueprintschema derives the COMPLETE blueprint authoring schema — every YAML key
// a blueprint author may write — by reflecting over the live Go types at runtime:
//
//   - the top-level blueprint document + its nested env/cloud/cluster/database/workload/
//     incident/scenario structs (internal/blueprint.Decl), and
//   - every registered construct/workload Config struct (via the composition-root registry),
//     i.e. the per-kind config blocks under features:/integrations:/addons + workloads[].config.
//
// Because it walks the live types, the schema AUTO-UPDATES as constructs, workloads, and the
// blueprint surface evolve — a new config field appears here the moment it is added. Field
// DESCRIPTIONS come from the Go doc comments, extracted into an embedded index (see docs.go)
// and kept in lockstep by the gate test (TestSchemaCurrent), the same drift-proof pattern as
// the env-surface gate.
//
// This is wiring/tooling code (like internal/runner) — it may import blueprint + core + the
// registry's config types; it is NOT a construct/workload and is not subject to the three-tier
// import isolation. Constructs/workloads must never import it.
package blueprintschema

import (
	"reflect"
	"sort"
	"strings"

	"github.com/rknightion/synthkit/internal/blueprint"
	"github.com/rknightion/synthkit/internal/core"
)

// Field is one schema field. Nested objects carry child Fields; slices of objects describe
// their element via Fields with Repeated=true.
type Field struct {
	Key      string   `json:"key"`                // YAML key
	Type     string   `json:"type"`               // human-readable type (string, int, bool, []X, map[K]V, object)
	Doc      string   `json:"doc,omitempty"`      // description from the Go doc comment
	Optional bool     `json:"optional,omitempty"` // pointer field (absence is meaningful)
	Repeated bool     `json:"repeated,omitempty"` // slice/array — Fields describe ONE element
	Enum     []string `json:"enum,omitempty"`     // reserved; not auto-derivable from reflection
	Fields   []Field  `json:"fields,omitempty"`   // nested object fields
}

// Section groups fields under one authoring location.
type Section struct {
	Title  string  `json:"title"`           // human title
	Path   string  `json:"path,omitempty"`  // where it attaches in YAML (e.g. "integrations.<kind>")
	Doc    string  `json:"doc,omitempty"`   // section description
	Kind   string  `json:"kind,omitempty"`  // construct/workload kind (for config sections)
	Group  string  `json:"group,omitempty"` // "blueprint" | "integration" | "feature" | "topology" | "workload" | "failure_modes"
	Fields []Field `json:"fields"`
}

// Doc is the full derived blueprint schema.
type Doc struct {
	Sections []Section `json:"sections"`
}

// docLookup resolves a field description by "<pkgPath>.<TypeName>.<FieldName>".
type docLookup map[string]string

func (d docLookup) get(pkgPath, typeName, field string) string {
	if d == nil {
		return ""
	}
	return d[pkgPath+"."+typeName+"."+field]
}

// Build derives the complete schema from the live blueprint.Decl type + the registry's
// construct/workload Config types. docs supplies field descriptions (may be nil → no docs).
func Build(reg *core.Registry, docs docLookup) Doc {
	var d Doc

	// 1. The blueprint document itself (top level + all nested decl structs, reached by
	//    walking the typed fields of Decl).
	d.Sections = append(d.Sections, Section{
		Title:  "Blueprint document",
		Path:   "(top level)",
		Doc:    "The blueprint YAML document. Strict-decoded: any key not listed here fails to load.",
		Group:  "blueprint",
		Fields: walkStruct(reflect.TypeOf(blueprint.Decl{}), docs, map[string]bool{}),
	})

	// 2. Construct config sections, grouped by declaration section.
	for _, kind := range reg.ConstructKinds() {
		cr, ok := reg.Construct(kind)
		if !ok {
			continue
		}
		group, path := constructPlacement(cr.Group, kind)
		d.Sections = append(d.Sections, Section{
			Title:  kind + " config",
			Path:   path,
			Doc:    cr.Doc,
			Kind:   kind,
			Group:  group,
			Fields: walkConfig(cr.NewConfig(), docs),
		})
	}

	// 3. Workload config sections.
	for _, kind := range reg.WorkloadKinds() {
		wr, ok := reg.Workload(kind)
		if !ok {
			continue
		}
		d.Sections = append(d.Sections, Section{
			Title:  kind + " workload config",
			Path:   "workloads[].config (type: " + kind + ")",
			Doc:    wr.Doc,
			Kind:   kind,
			Group:  "workload",
			Fields: walkConfig(wr.NewConfig(), docs),
		})
	}

	// 4. Failure-mode vocabulary (the incident/scenario `mode:` values).
	d.Sections = append(d.Sections, failureModeSection(reg))

	return d
}

// constructPlacement maps a construct's Group to a (group, yaml-path) pair.
func constructPlacement(g core.Group, kind string) (group, path string) {
	switch g {
	case core.GroupIntegration:
		return "integration", "integrations." + kind
	case core.GroupFeature:
		return "feature", "features." + kind
	default:
		// Topology/add-on kinds are config-gated by the env/cloud/cluster/database declarations
		// (e.g. env.cloud.cloudwatch sub-toggles, env.databases[].observability), not a named
		// section keyed by kind.
		return "topology", "(config-gated by env/cloud/cluster/database declarations)"
	}
}

// walkConfig walks a *Config value (the registry hands a pointer).
func walkConfig(cfg any, docs docLookup) []Field {
	t := reflect.TypeOf(cfg)
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return nil
	}
	return walkStruct(t, docs, map[string]bool{})
}

// customDecodeFields hand-describes the YAML keys for blueprint structs that consume their
// keys via a custom UnmarshalYAML — those keys are NOT carried on yaml struct tags, so
// reflection cannot see them and would otherwise render the type as an empty object. This
// table IS the documented contract for those types; it must be updated by hand when a custom
// UnmarshalYAML grows a key (TestCustomDecodeKeysPresent pins the current set). Keyed by
// "<pkgPath>.<TypeName>".
var customDecodeFields = map[string][]Field{
	"github.com/rknightion/synthkit/internal/blueprint.WorkloadDecl": {
		{Key: "type", Type: "string", Doc: "workload kind — the registry key (e.g. web_service)"},
		{Key: "name", Type: "string", Doc: "unique workload instance name"},
		{Key: "runs_on", Type: "string", Doc: "the cluster/environment this workload runs on"},
		{Key: "replicas", Type: "int", Doc: "pod count driving node derivation (default 2)"},
		{Key: "calls", Type: "object", Repeated: true, Doc: "downstream DB/cache hops this workload makes", Fields: []Field{
			{Key: "db", Type: "string", Doc: "name of a declared database this workload calls"},
			{Key: "cache", Type: "string", Doc: "name of a declared cache this workload calls"},
		}},
		{Key: "<kind config…>", Type: "raw yaml", Doc: "every remaining key is the workload kind's own config — see the matching `… workload config` section."},
	},
	"github.com/rknightion/synthkit/internal/blueprint.AddonRef": {
		{Key: "name", Type: "string", Doc: "add-on construct kind (registry key). Also accepts the bare-scalar form `- core_dns`."},
		{Key: "<kind config…>", Type: "raw yaml", Doc: "every remaining key is the add-on construct's own config — see the matching `… config` section."},
	},
}

// walkStruct reflects one struct type into Fields. seen guards against type cycles. Structs
// with a custom UnmarshalYAML are described from the customDecodeFields override (reflection
// cannot see their keys).
func walkStruct(t reflect.Type, docs docLookup, seen map[string]bool) []Field {
	if t.Kind() != reflect.Struct {
		return nil
	}
	typeKey := t.PkgPath() + "." + t.Name()
	if ov, ok := customDecodeFields[typeKey]; ok {
		return ov
	}
	if seen[typeKey] {
		return nil // cycle guard
	}
	seen[typeKey] = true
	defer delete(seen, typeKey)

	var out []Field
	for i := 0; i < t.NumField(); i++ {
		sf := t.Field(i)
		if !sf.IsExported() {
			continue
		}
		key := yamlKey(sf)
		if key == "" || key == "-" {
			continue
		}
		f := Field{
			Key: key,
			Doc: docs.get(t.PkgPath(), t.Name(), sf.Name),
		}
		describe(&f, sf.Type, docs, seen)
		out = append(out, f)
	}
	return out
}

// describe fills Type/Optional/Repeated/Fields for a field's Go type.
func describe(f *Field, ft reflect.Type, docs docLookup, seen map[string]bool) {
	switch ft.Kind() {
	case reflect.Pointer:
		f.Optional = true
		describe(f, ft.Elem(), docs, seen)
	case reflect.Slice, reflect.Array:
		f.Repeated = true
		el := ft.Elem()
		for el.Kind() == reflect.Pointer {
			el = el.Elem()
		}
		if isObject(el) {
			f.Type = "object"
			f.Fields = walkStruct(el, docs, seen)
		} else {
			f.Type = typeName(el)
		}
	case reflect.Map:
		f.Type = "map[" + typeName(ft.Key()) + "]" + typeName(ft.Elem())
	case reflect.Struct:
		if rawType(ft) {
			f.Type = typeName(ft)
			return
		}
		f.Type = "object"
		f.Fields = walkStruct(ft, docs, seen)
	default:
		f.Type = typeName(ft)
	}
}

// isObject reports whether t is a struct we should recurse into (not a raw/opaque struct).
func isObject(t reflect.Type) bool {
	return t.Kind() == reflect.Struct && !rawType(t)
}

// rawType reports types we render opaquely rather than recurse into.
func rawType(t reflect.Type) bool {
	switch t.PkgPath() + "." + t.Name() {
	case "time.Time", "time.Duration", "gopkg.in/yaml.v3.Node":
		return true
	}
	return false
}

// typeName renders a human-readable type name.
func typeName(t reflect.Type) string {
	for t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	switch t.Kind() {
	case reflect.String:
		return "string"
	case reflect.Bool:
		return "bool"
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return "int"
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return "uint"
	case reflect.Float32, reflect.Float64:
		return "float"
	case reflect.Slice, reflect.Array:
		return "[]" + typeName(t.Elem())
	case reflect.Map:
		return "map[" + typeName(t.Key()) + "]" + typeName(t.Elem())
	}
	if t.Name() == "Node" && strings.Contains(t.PkgPath(), "yaml") {
		return "raw yaml (see per-kind config sections)"
	}
	if t.Name() != "" {
		return t.Name()
	}
	return t.Kind().String()
}

// yamlKey extracts the yaml key from a struct field's tag.
func yamlKey(sf reflect.StructField) string {
	tag := sf.Tag.Get("yaml")
	if tag == "" {
		return ""
	}
	key, _, _ := strings.Cut(tag, ",")
	return key
}

// failureModeSection lists the incident/scenario `mode:` vocabulary (union across kinds).
func failureModeSection(reg *core.Registry) Section {
	type modeKey struct{ name, axis, help string }
	seen := map[modeKey]bool{}
	var fields []Field
	for _, m := range reg.AllFailureModes() {
		k := modeKey{m.Name, string(m.Axis), m.Help}
		if seen[k] {
			continue
		}
		seen[k] = true
		fields = append(fields, Field{Key: m.Name, Type: "axis: " + string(m.Axis), Doc: m.Help})
	}
	sort.Slice(fields, func(i, j int) bool { return fields[i].Key < fields[j].Key })
	return Section{
		Title:  "Failure modes",
		Path:   "incidents[].mode / scenarios[].effects[].mode",
		Doc:    "The valid `mode:` values an incident or scenario effect may reference (union across all registered kinds).",
		Group:  "failure_modes",
		Fields: fields,
	}
}
