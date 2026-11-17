// SPDX-License-Identifier: AGPL-3.0-only

package engine

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"sync"
	"time"
)

// Journal is the append-only JSONL record of one run: every inbound event, director call,
// accepted and rejected line, post intent and post result. A post is journaled as an intent
// before it is sent and as a result (with its ts) after.
type Journal struct {
	mu  sync.Mutex
	w   io.Writer
	err error
}

// NewJournal writes JSONL to w. A nil w discards.
func NewJournal(w io.Writer) *Journal { return &Journal{w: w} }

// Entry is one journal line.
type Entry struct {
	At      time.Time `json:"at"`
	RunID   string    `json:"run_id,omitempty"`
	Kind    string    `json:"kind"`
	Detail  any       `json:"detail,omitempty"`
	Message string    `json:"msg,omitempty"`
}

// Write appends one entry. The first write error is kept and reported by Err.
func (j *Journal) Write(e Entry) {
	if j == nil || j.w == nil {
		return
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	b, err := json.Marshal(e)
	if err != nil {
		b, _ = json.Marshal(Entry{At: e.At, RunID: e.RunID, Kind: e.Kind, Message: fmt.Sprintf("unencodable detail: %v", err)})
	}
	if _, err := j.w.Write(append(b, '\n')); err != nil && j.err == nil {
		j.err = err
	}
}

// Err reports the first write failure.
func (j *Journal) Err() error {
	if j == nil {
		return nil
	}
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.err
}

// UtteranceID is the stable id of the seq-th outbound line of a run.
func UtteranceID(runID string, seq int) string {
	sum := sha256.Sum256(fmt.Appendf(nil, "%s\x00%d", runID, seq))
	return hex.EncodeToString(sum[:8])
}
