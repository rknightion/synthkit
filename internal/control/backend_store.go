// SPDX-License-Identifier: AGPL-3.0-only
package control

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/rknightion/synthkit/internal/ha"
)

type operationReceipt struct {
	ID          string `json:"operation_id"`
	Fingerprint string `json:"fingerprint"`
	Sequence    uint64 `json:"sequence"`
}
type controlDocument struct {
	SchemaVersion        int                `json:"schema_version"`
	Sequence             uint64             `json:"sequence"`
	ReceiptFloorSequence uint64             `json:"receipt_floor_sequence"`
	State                State              `json:"state"`
	Receipts             []operationReceipt `json:"receipts"`
}

func decodeControl(data []byte) (controlDocument, error) {
	d := controlDocument{SchemaVersion: 1, State: DefaultState(), Receipts: []operationReceipt{}}
	if len(data) == 0 {
		return d, nil
	}
	if trimmed := bytes.TrimSpace(data); len(trimmed) == 0 || trimmed[0] != '{' {
		return d, fmt.Errorf("state: control document must be an object")
	}
	var header struct {
		SchemaVersion int             `json:"schema_version"`
		State         json.RawMessage `json:"state"`
	}
	if err := json.Unmarshal(data, &header); err != nil {
		return d, err
	}
	if header.SchemaVersion == 0 {
		if err := json.Unmarshal(data, &d.State); err != nil {
			return d, err
		}
	} else {
		if state := bytes.TrimSpace(header.State); len(state) == 0 || state[0] != '{' {
			return d, fmt.Errorf("state: missing logical state object")
		}
		if header.SchemaVersion != 1 {
			return d, fmt.Errorf("state: unsupported control schema")
		}
		if err := json.Unmarshal(data, &d); err != nil {
			return d, err
		}
		if len(d.Receipts) > 256 || d.ReceiptFloorSequence > d.Sequence {
			return d, fmt.Errorf("state: invalid receipt journal")
		}
		for _, r := range d.Receipts {
			if r.ID == "" || r.Fingerprint == "" || r.Sequence > d.Sequence || r.Sequence <= d.ReceiptFloorSequence {
				return d, fmt.Errorf("state: corrupt receipt")
			}
		}
	}
	if d.State.Failures == nil {
		d.State.Failures = map[string]FailureSetting{}
	}
	if d.State.Scaling == nil {
		d.State.Scaling = map[string]int{}
	}
	return d, nil
}

// NewBackendStore reads the authoritative control document without a write.
// gate must be the same epoch gate supplied to the backend factory.
func NewBackendStore(ctx context.Context, b StateBackend, gate ha.LeaderGate, attempts int) (*Store, error) {
	if b == nil || gate == nil || attempts < 1 || attempts > 5 {
		return nil, fmt.Errorf("state: invalid store options")
	}
	snap, err := b.Load(ctx, Control)
	if err != nil {
		return nil, err
	}
	doc, err := decodeControl(snap.Data)
	if err != nil {
		return nil, fmt.Errorf("state: corrupt control: %w", err)
	}
	return &Store{state: doc.State, backend: b, gate: gate, strict: true, casAttempts: attempts, now: time.Now}, nil
}
func (s *Store) UsesBackend() bool { return s.backend != nil }

// ObserveBackendError latches an unresolved source/manifest write in readiness.
func (s *Store) ObserveBackendError(err error) {
	if err != nil {
		s.mu.Lock()
		defer s.mu.Unlock()
		s.recordPersist(err)
	}
}

func (s *Store) updateBackend(ctx context.Context, fn func(*State)) (State, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	var out State
	err := s.gate.Do(ctx, ha.Mutation, func(c context.Context) error {
		s.mu.Lock()
		defer s.mu.Unlock()
		out = cloneState(s.state)
		if s.outcomeUnknown {
			return ErrOutcomeUnknown
		}
		err := s.commitControl(c, fn, &out)
		s.recordPersist(err)
		return err
	})
	return out, err
}
func (s *Store) commitControl(ctx context.Context, fn func(*State), out *State) error {
	var idBytes [16]byte
	if _, err := rand.Read(idBytes[:]); err != nil {
		return err
	}
	id := hex.EncodeToString(idBytes[:])
	var base uint64
	var fingerprint string
	initialized := false
	ambiguous := false
	for attempt := 0; attempt <= s.casAttempts; attempt++ {
		snap, err := s.backend.Load(ctx, Control)
		if err != nil {
			if ambiguous {
				return fmt.Errorf("%w: reconciliation read failed", ErrOutcomeUnknown)
			}
			return err
		}
		doc, err := decodeControl(snap.Data)
		if err != nil {
			return err
		}
		if !initialized {
			base = doc.Sequence
			next := cloneState(doc.State)
			fn(&next)
			intent, err := json.Marshal(struct {
				Before State
				After  State
			}{doc.State, next})
			if err != nil {
				return err
			}
			sum := sha256.Sum256(intent)
			fingerprint = hex.EncodeToString(sum[:])
			initialized = true
		}
		for _, r := range doc.Receipts {
			if r.ID == id {
				if r.Fingerprint != fingerprint {
					return ErrConflict
				}
				s.state = cloneState(doc.State)
				*out = cloneState(doc.State)
				return nil
			}
		}
		if ambiguous && doc.ReceiptFloorSequence > base {
			return ErrOutcomeUnknown
		}
		if err := ctx.Err(); err != nil {
			if ambiguous {
				return ErrOutcomeUnknown
			}
			return err
		}
		if attempt == s.casAttempts {
			if ambiguous {
				return ErrOutcomeUnknown
			}
			return ErrConflict
		}
		next := cloneState(doc.State)
		fn(&next)
		doc.State = next
		doc.Sequence++
		doc.Receipts = append(doc.Receipts, operationReceipt{ID: id, Fingerprint: fingerprint, Sequence: doc.Sequence})
		if len(doc.Receipts) > 256 {
			doc.ReceiptFloorSequence = doc.Receipts[0].Sequence
			doc.Receipts = doc.Receipts[1:]
		}
		data, err := json.Marshal(doc)
		if err != nil {
			return err
		}
		_, err = s.backend.CompareAndSwap(ctx, Control, snap.Revision, data)
		if err == nil {
			s.state = cloneState(doc.State)
			*out = cloneState(doc.State)
			return nil
		}
		if errors.Is(err, ha.ErrNotLeader) {
			return err
		}
		if errors.Is(err, ErrOutcomeUnknown) {
			ambiguous = true
		} else if !errors.Is(err, ErrConflict) {
			return err
		}
	}
	return ErrOutcomeUnknown
}
