// SPDX-License-Identifier: AGPL-3.0-only

package corpus_test

import (
	"strings"
	"testing"

	"github.com/rknightion/synthkit/internal/sigil"
)

// Exercise the real embedded loader: a missing asset must not silently use fallback text.
func TestRegulatedDocumentCorpus(t *testing.T) {
	records, err := sigil.LoadCorpus("regulated_document_authoring")
	if err != nil {
		t.Fatal(err)
	}
	kinds := map[string]bool{}
	facets := map[string]bool{}
	var content strings.Builder
	for _, r := range records {
		if r.Archetype != "regulated_document_authoring" {
			t.Fatalf("wrong archetype: %q", r.Archetype)
		}
		kinds[r.Kind] = true
		for _, f := range r.Facets {
			facets[f] = true
		}
		content.WriteString(r.Text)
		for _, p := range r.Parts {
			content.WriteString(p.Text)
			if p.ToolResult != nil {
				content.WriteString(p.ToolResult.Content)
			}
		}
	}
	for _, k := range []string{"system_prompt", "turn", "tool_result", "title"} {
		if !kinds[k] {
			t.Errorf("missing record kind %q", k)
		}
	}
	for _, f := range []string{"protocol", "clinical_summary", "safety_narrative", "redaction", "source_note_injection"} {
		if !facets[f] {
			t.Errorf("missing authoring coverage %q", f)
		}
	}
	for _, marker := range []string{"FICTIONAL", "SUBJ-SYN-", "INV-SYN-", "[REDACTED]", "UNTRUSTED SOURCE NOTE"} {
		if !strings.Contains(content.String(), marker) {
			t.Errorf("missing content %q", marker)
		}
	}
}
