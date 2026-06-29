// SPDX-License-Identifier: AGPL-3.0-only

package sigil

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"sync"
)

// Archetype names — the corpus file keys AND the aiagent Config.Archetype values (frozen seam).
const (
	ArchetypeCodingClaudeCode      = "coding_claude_code"
	ArchetypeCodingCodex           = "coding_codex"
	ArchetypeGeneralOrchestration  = "general_orchestration"
	ArchetypeGeneralAutonomousLoop = "general_autonomous_loop"
	ArchetypeGeneralMultiturn      = "general_multiturn"
	ArchetypeGeneralSingleShot     = "general_single_shot"
)

// AllArchetypes is every modelled archetype (one corpus file each).
var AllArchetypes = []string{
	ArchetypeCodingClaudeCode, ArchetypeCodingCodex,
	ArchetypeGeneralOrchestration, ArchetypeGeneralAutonomousLoop,
	ArchetypeGeneralMultiturn, ArchetypeGeneralSingleShot,
}

// IsCoding reports whether an archetype is a coding agent (sdk-go, fs/shell tools, heavy cache).
func IsCoding(archetype string) bool {
	return archetype == ArchetypeCodingClaudeCode || archetype == ArchetypeCodingCodex
}

// CorpusRecord is one line of an embedded corpus .jsonl(.gz) file. It is the FROZEN schema the
// offline corpus-generation fleet writes and the loader reads (spec §6). Content is
// technology-generic / fictional (forbidden-words clean).
type CorpusRecord struct {
	Archetype  string       `json:"archetype"` // which roster archetype it feeds (Archetype* values)
	Kind       string       `json:"kind"`      // turn | tool_result | system_prompt | title
	Role       string       `json:"role"`      // user | assistant (for kind=turn)
	Parts      []CorpusPart `json:"parts,omitempty"`
	Text       string       `json:"text,omitempty"` // for kind=system_prompt|title (single string)
	StopReason string       `json:"stop_reason,omitempty"`
	Facets     []string     `json:"facets,omitempty"` // optional sampling facets (e.g. topic tags)
}

// CorpusPart is one content part within a corpus turn.
type CorpusPart struct {
	Text       string            `json:"text,omitempty"`
	Thinking   string            `json:"thinking,omitempty"`
	ToolCall   *CorpusToolCall   `json:"tool_call,omitempty"`
	ToolResult *CorpusToolResult `json:"tool_result,omitempty"`
}

// CorpusToolCall is the corpus form of an assistant tool invocation (arguments are arbitrary JSON).
type CorpusToolCall struct {
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments"`
}

// CorpusToolResult is the corpus form of a tool output.
type CorpusToolResult struct {
	Name    string `json:"name"`
	Content string `json:"content"`
	IsError bool   `json:"is_error,omitempty"`
}

//go:embed corpus/*.jsonl.gz
var corpusFS embed.FS

var (
	corpusMu    sync.Mutex
	corpusCache = map[string][]CorpusRecord{}
)

// LoadCorpus returns the embedded, gzip-decoded records for an archetype, cached after first read.
// The file is corpus/<archetype>.jsonl.gz. Returns an error if the file is absent or malformed.
func LoadCorpus(archetype string) ([]CorpusRecord, error) {
	corpusMu.Lock()
	defer corpusMu.Unlock()
	if recs, ok := corpusCache[archetype]; ok {
		return recs, nil
	}
	raw, err := corpusFS.ReadFile("corpus/" + archetype + ".jsonl.gz")
	if err != nil {
		return nil, fmt.Errorf("sigil: corpus %q: %w", archetype, err)
	}
	recs, err := decodeCorpus(raw)
	if err != nil {
		return nil, fmt.Errorf("sigil: corpus %q: %w", archetype, err)
	}
	corpusCache[archetype] = recs
	return recs, nil
}

// decodeCorpus gunzips a JSONL blob and unmarshals one CorpusRecord per non-empty line.
func decodeCorpus(gz []byte) ([]CorpusRecord, error) {
	zr, err := gzip.NewReader(bytes.NewReader(gz))
	if err != nil {
		return nil, err
	}
	defer zr.Close()
	var out []CorpusRecord
	sc := bufio.NewScanner(zr)
	sc.Buffer(make([]byte, 0, 64*1024), 8*1024*1024) // tolerate long content lines
	for sc.Scan() {
		line := bytes.TrimSpace(sc.Bytes())
		if len(line) == 0 {
			continue
		}
		var rec CorpusRecord
		if err := json.Unmarshal(line, &rec); err != nil {
			return nil, err
		}
		out = append(out, rec)
	}
	if err := sc.Err(); err != nil && err != io.EOF {
		return nil, err
	}
	return out, nil
}
