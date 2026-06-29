// SPDX-License-Identifier: AGPL-3.0-only

package blueprintschema

import (
	"fmt"
	"strings"
)

// RenderMarkdown renders the schema as a Markdown reference: one section per authoring
// location, one table row per field (nested object fields flattened to dotted keys, slice
// elements marked with `[]`). This is the committed BLUEPRINT-SCHEMA.md artifact.
func RenderMarkdown(d Doc) string {
	var b strings.Builder
	b.WriteString("# synthkit blueprint schema (generated)\n\n")
	b.WriteString("> **Generated** by `internal/blueprintschema` from the live Go types — do NOT edit by hand.\n")
	b.WriteString("> Regenerate with `make blueprint-schema`; the `TestSchemaCurrent` gate fails on drift.\n")
	b.WriteString("> Every key a blueprint may contain is listed below. Strict-decoded: unknown keys fail to load.\n\n")

	for _, s := range d.Sections {
		b.WriteString("## " + s.Title + "\n\n")
		if s.Path != "" {
			b.WriteString("**Location:** `" + s.Path + "`")
			if s.Group != "" {
				b.WriteString("  ·  **group:** " + s.Group)
			}
			b.WriteString("\n\n")
		}
		if s.Doc != "" {
			b.WriteString(strings.TrimSpace(s.Doc) + "\n\n")
		}
		if len(s.Fields) == 0 {
			b.WriteString("_(no configurable fields)_\n\n")
			continue
		}
		b.WriteString("| key | type | optional | description |\n")
		b.WriteString("|---|---|---|---|\n")
		for _, f := range s.Fields {
			writeFieldRows(&b, "", f)
		}
		b.WriteString("\n")
	}
	return b.String()
}

// writeFieldRows writes one table row for f (key prefixed by parent path) and recurses into
// nested object fields. Slice-of-object keys get a trailing `[]`.
func writeFieldRows(b *strings.Builder, prefix string, f Field) {
	key := prefix + f.Key
	if f.Repeated {
		key += "[]"
	}
	opt := ""
	if f.Optional {
		opt = "yes"
	}
	typ := f.Type
	if len(f.Enum) > 0 {
		typ += " ∈ {" + strings.Join(f.Enum, ", ") + "}"
	}
	fmt.Fprintf(b, "| `%s` | %s | %s | %s |\n", key, typ, opt, oneLine(f.Doc))
	childPrefix := key + "."
	for _, c := range f.Fields {
		writeFieldRows(b, childPrefix, c)
	}
}

// oneLine collapses a doc comment to a single table-safe line.
func oneLine(s string) string {
	s = strings.TrimSpace(s)
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "|", "\\|")
	for strings.Contains(s, "  ") {
		s = strings.ReplaceAll(s, "  ", " ")
	}
	return s
}
