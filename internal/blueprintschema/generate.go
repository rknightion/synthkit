// SPDX-License-Identifier: AGPL-3.0-only

package blueprintschema

import (
	"bytes"
	"encoding/json"

	"github.com/rknightion/synthkit/internal/core"
)

// Artifact names (repo-relative for the markdown; package-relative for the embedded index).
const (
	MarkdownPath  = "BLUEPRINT-SCHEMA.md"
	DocsIndexPath = "internal/blueprintschema/fielddocs.json"
)

// Generate produces the two committed artifacts from the live types: the field-description
// index JSON (embedded for runtime) and the Markdown reference. moduleRoot must be the repo
// root (source is parsed for doc comments).
func Generate(reg *core.Registry, moduleRoot string) (docsJSON []byte, markdown string, err error) {
	docMap, err := ExtractDocs(moduleRoot)
	if err != nil {
		return nil, "", err
	}
	docsJSON, err = marshalIndent(docMap)
	if err != nil {
		return nil, "", err
	}
	d := Build(reg, docLookup(docMap))
	return docsJSON, RenderMarkdown(d), nil
}

// JSON builds the schema with the EMBEDDED doc index and marshals it — the runtime form
// served at GET /control/blueprint-schema (no source needed). Returns nil on marshal error.
func JSON(reg *core.Registry) []byte {
	b, err := json.Marshal(Build(reg, EmbeddedDocs()))
	if err != nil {
		return nil
	}
	return b
}

// marshalIndent marshals with sorted keys (encoding/json sorts map keys) + indentation +
// trailing newline, so the committed file is stable and diff-friendly.
func marshalIndent(m map[string]string) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(m); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}
