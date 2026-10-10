// SPDX-License-Identifier: AGPL-3.0-only

package bpsource

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/rknightion/synthkit/internal/control"
	"github.com/rknightion/synthkit/internal/ha"
	"os"
	"path/filepath"
	"sort"
	"time"
)

func writeManifest(dir string, m Manifest) error {
	if err := ensurePrivateDir(dir); err != nil {
		return err
	}
	b, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return writePrivateFile(filepath.Join(dir, manifestFile), b)
}

func readManifest(dir string) Manifest {
	var m Manifest
	b, err := os.ReadFile(filepath.Join(dir, manifestFile))
	if err != nil {
		return Manifest{SourceSHAs: map[string]string{}}
	}
	if json.Unmarshal(b, &m) != nil {
		return Manifest{SourceSHAs: map[string]string{}}
	}
	if m.SourceSHAs == nil {
		m.SourceSHAs = map[string]string{}
	}
	return m
}

type documentReceipt struct {
	ID          string `json:"operation_id"`
	Fingerprint string `json:"fingerprint"`
	Sequence    uint64 `json:"sequence"`
}
type sourceDocument struct {
	SchemaVersion        int               `json:"schema_version"`
	ConfigFingerprint    string            `json:"config_fingerprint"`
	FetchedSHA           string            `json:"fetched_sha"`
	Files                map[string][]byte `json:"files"`
	FetchStatus          fetchStatus       `json:"fetch_status"`
	Sequence             uint64            `json:"sequence"`
	ReceiptFloorSequence uint64            `json:"receipt_floor_sequence"`
	Receipts             []documentReceipt `json:"receipts"`
}
type fetchStatus struct {
	ObservedSHA string   `json:"observed_sha"`
	LastFetchMs int64    `json:"last_fetch_ms"`
	LastErr     string   `json:"last_err"`
	LoadedSHA   string   `json:"loaded_sha"`
	LoadedNames []string `json:"loaded_names"`
	Skipped     []string `json:"skipped"`
}

func sourceFingerprint(s Source) string {
	data, _ := json.Marshal([]string{s.ID, s.URL, s.Ref, s.Subpath, s.Namespace, s.TokenEnvVar})
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
func decodeSourceDocument(data []byte) (sourceDocument, error) {
	d := sourceDocument{SchemaVersion: 1, Files: map[string][]byte{}, Receipts: []documentReceipt{}}
	if len(data) == 0 {
		return d, nil
	}
	d.SchemaVersion = 0
	if err := json.Unmarshal(data, &d); err != nil {
		return d, err
	}
	if d.SchemaVersion != 1 || d.ReceiptFloorSequence > d.Sequence || len(d.Receipts) > 256 {
		return d, fmt.Errorf("bpsource: invalid source document")
	}
	for fn := range d.Files {
		if filepath.Base(fn) != fn || filepath.Ext(fn) != ".yaml" {
			return d, fmt.Errorf("bpsource: invalid cached filename")
		}
	}
	if len(d.Files) > 0 && (d.FetchedSHA == "" || d.ConfigFingerprint == "") {
		return d, fmt.Errorf("bpsource: unbound cached files")
	}
	return d, nil
}
func joinSource(s Source, d sourceDocument) Source {
	// User configuration is authoritative, including reset and removal.
	s.FetchedSHA = ""
	s.FetchedFileCount = 0
	s.EffectiveNames = []string{}
	s.ObservedSHA = ""
	s.LastFetchMs = 0
	s.LastErr = ""
	s.LoadedSHA = ""
	s.LoadedNames = []string{}
	s.Skipped = []string{}
	s.PendingRestart = false
	if d.ConfigFingerprint != sourceFingerprint(s) {
		return s
	}
	s.FetchedSHA = d.FetchedSHA
	s.FetchedFileCount = len(d.Files)
	s.EffectiveNames = effectiveNames(s.Namespace, d.Files)
	s.ObservedSHA = d.FetchStatus.ObservedSHA
	s.LastFetchMs = d.FetchStatus.LastFetchMs
	s.LastErr = d.FetchStatus.LastErr
	s.LoadedSHA = d.FetchStatus.LoadedSHA
	s.LoadedNames = cloneStrings(d.FetchStatus.LoadedNames)
	s.Skipped = cloneStrings(d.FetchStatus.Skipped)
	s.PendingRestart = s.FetchedSHA != "" && s.FetchedSHA != s.LoadedSHA
	return s
}

// LoadBackend is a mandatory read-only acquisition/startup phase. It does not create directories.
func (m *Manager) LoadBackend(ctx context.Context) error {
	if m.backend == nil {
		return nil
	}
	if m.casAttempts < 1 || m.casAttempts > 5 || m.maxDocumentBytes < 1 || m.maxDocumentBytes > control.MaxStateDocumentBytes {
		return fmt.Errorf("bpsource: invalid backend options")
	}
	snap, err := m.backend.Load(ctx, control.BootManifest)
	if err != nil {
		return err
	}
	manifestDoc, err := decodeManifestDocument(snap.Data)
	if err != nil {
		return err
	}
	man := manifestDoc.Manifest
	if man.SourceSHAs == nil {
		man.SourceSHAs = map[string]string{}
	}
	m.sourceMu.Lock()
	defer m.sourceMu.Unlock()
	docs := map[string]sourceDocument{}
	if m.cfg != nil {
		for _, s := range m.cfg.Sources() {
			key, err := control.GitSourceKey(s.ID)
			if err != nil {
				return err
			}
			snap, err := m.backend.Load(ctx, key)
			if err != nil {
				return err
			}
			if len(snap.Data) > m.maxDocumentBytes {
				return fmt.Errorf("bpsource: stored source document exceeds byte cap")
			}
			doc, err := decodeSourceDocument(snap.Data)
			if err != nil {
				return err
			}
			docs[s.ID] = doc
		}
	}
	// A manifest does not give authority back to a reset/removed/reconfigured source.
	valid := map[string]bool{}
	if m.cfg != nil {
		for _, s := range m.cfg.Sources() {
			valid[s.ID] = docs[s.ID].ConfigFingerprint == sourceFingerprint(s)
		}
	}
	kept := man.Blueprints[:0]
	for _, e := range man.Blueprints {
		if e.Provenance != ProvGit || valid[e.SourceID] {
			kept = append(kept, e)
		}
	}
	man.Blueprints = kept
	for id := range man.SourceSHAs {
		if !valid[id] {
			delete(man.SourceSHAs, id)
		}
	}
	m.backendDocs = docs
	m.mu.Lock()
	m.boot = man
	m.mu.Unlock()
	return nil
}

// mutateSource replays only the affected source fields and journals the operation atomically.
// Caller holds sourceMu. Desired bytes, clocks and git results are captured outside fn.
func (m *Manager) mutateSource(ctx context.Context, s Source, fn func(*sourceDocument)) error {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	key, err := control.GitSourceKey(s.ID)
	if err != nil {
		return err
	}
	var rawID [16]byte
	if _, err = rand.Read(rawID[:]); err != nil {
		return err
	}
	id := hex.EncodeToString(rawID[:])
	fingerprint := ""
	var base uint64
	ambiguous := false
	err = m.gate.Do(ctx, ha.Mutation, func(c context.Context) error {
		for attempt := 0; attempt <= m.casAttempts; attempt++ {
			snap, err := m.backend.Load(c, key)
			if err != nil {
				if ambiguous {
					return control.ErrOutcomeUnknown
				}
				return err
			}
			if len(snap.Data) > m.maxDocumentBytes {
				return fmt.Errorf("bpsource: stored source document exceeds byte cap")
			}
			doc, err := decodeSourceDocument(snap.Data)
			if err != nil {
				return err
			}
			if fingerprint == "" {
				base = doc.Sequence
				intent := doc
				fn(&intent)
				data, err := json.Marshal(intent)
				if err != nil {
					return err
				}
				sum := sha256.Sum256(data)
				fingerprint = hex.EncodeToString(sum[:])
			}
			for _, r := range doc.Receipts {
				if r.ID == id {
					if r.Fingerprint != fingerprint {
						return control.ErrConflict
					}
					m.backendDocs[s.ID] = doc
					return nil
				}
			}
			if ambiguous && doc.ReceiptFloorSequence > base {
				return control.ErrOutcomeUnknown
			}
			if attempt == m.casAttempts {
				if ambiguous {
					return control.ErrOutcomeUnknown
				}
				return control.ErrConflict
			}
			if err := c.Err(); err != nil {
				if ambiguous {
					return control.ErrOutcomeUnknown
				}
				return err
			}
			fn(&doc)
			doc.Sequence++
			doc.Receipts = append(doc.Receipts, documentReceipt{ID: id, Fingerprint: fingerprint, Sequence: doc.Sequence})
			if len(doc.Receipts) > 256 {
				doc.ReceiptFloorSequence = doc.Receipts[0].Sequence
				doc.Receipts = doc.Receipts[1:]
			}
			data, err := json.Marshal(doc)
			if err != nil {
				return err
			}
			if len(data) > m.maxDocumentBytes {
				return fmt.Errorf("bpsource: encoded source document exceeds byte cap")
			}
			_, err = m.backend.CompareAndSwap(c, key, snap.Revision, data)
			if err == nil {
				m.backendDocs[s.ID] = doc
				return nil
			}
			if errors.Is(err, control.ErrOutcomeUnknown) {
				ambiguous = true
			} else if !errors.Is(err, control.ErrConflict) {
				return err
			}
		}
		return control.ErrOutcomeUnknown
	})
	if err != nil && m.backendError != nil {
		m.backendError(err)
	}
	return err
}

type manifestDocument struct {
	SchemaVersion        int               `json:"schema_version"`
	Manifest             Manifest          `json:"manifest"`
	Sequence             uint64            `json:"sequence"`
	ReceiptFloorSequence uint64            `json:"receipt_floor_sequence"`
	Receipts             []documentReceipt `json:"receipts"`
}

func decodeManifestDocument(data []byte) (manifestDocument, error) {
	d := manifestDocument{SchemaVersion: 1, Manifest: Manifest{SourceSHAs: map[string]string{}}, Receipts: []documentReceipt{}}
	if len(data) == 0 {
		return d, nil
	}
	d.SchemaVersion = 0
	if err := json.Unmarshal(data, &d); err != nil {
		return d, err
	}
	if d.SchemaVersion != 1 || len(d.Receipts) > 256 || d.ReceiptFloorSequence > d.Sequence {
		return d, fmt.Errorf("bpsource: invalid manifest document")
	}
	return d, nil
}
func (m *Manager) commitManifest(ctx context.Context, man Manifest) error {
	if m.backend == nil {
		return writeManifest(m.dataDir, man)
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	desired, err := json.Marshal(man)
	if err != nil {
		return err
	}
	sum := sha256.Sum256(desired)
	fingerprint := hex.EncodeToString(sum[:])
	var idBytes [16]byte
	if _, err = rand.Read(idBytes[:]); err != nil {
		return err
	}
	id := hex.EncodeToString(idBytes[:])
	err = m.gate.Do(ctx, ha.Mutation, func(c context.Context) error {
		ambiguous := false
		var base uint64
		for i := 0; i <= m.casAttempts; i++ {
			snap, err := m.backend.Load(c, control.BootManifest)
			if err != nil {
				if ambiguous {
					return control.ErrOutcomeUnknown
				}
				return err
			}
			doc, err := decodeManifestDocument(snap.Data)
			if err != nil {
				return err
			}
			if i == 0 {
				base = doc.Sequence
			}
			for _, r := range doc.Receipts {
				if r.ID == id {
					if r.Fingerprint != fingerprint {
						return control.ErrConflict
					}
					return nil
				}
			}
			if ambiguous && doc.ReceiptFloorSequence > base {
				return control.ErrOutcomeUnknown
			}
			if i == m.casAttempts {
				if ambiguous {
					return control.ErrOutcomeUnknown
				}
				return control.ErrConflict
			}
			doc.Manifest = man
			doc.Sequence++
			doc.Receipts = append(doc.Receipts, documentReceipt{ID: id, Fingerprint: fingerprint, Sequence: doc.Sequence})
			if len(doc.Receipts) > 256 {
				doc.ReceiptFloorSequence = doc.Receipts[0].Sequence
				doc.Receipts = doc.Receipts[1:]
			}
			data, err := json.Marshal(doc)
			if err != nil {
				return err
			}
			if len(data) > control.MaxStateDocumentBytes {
				return fmt.Errorf("bpsource: manifest exceeds cap")
			}
			_, err = m.backend.CompareAndSwap(c, control.BootManifest, snap.Revision, data)
			if err == nil {
				return nil
			}
			if errors.Is(err, control.ErrOutcomeUnknown) {
				ambiguous = true
			} else if !errors.Is(err, control.ErrConflict) {
				return err
			}
		}
		return control.ErrOutcomeUnknown
	})
	if err != nil && m.backendError != nil {
		m.backendError(err)
	}
	return err
}

func diffPending(boot Manifest, staged []ManifestEntry, latestSHAs map[string]string) Pending {
	bootSet := map[string]bool{}
	for _, e := range boot.Blueprints {
		bootSet[e.Name] = true
	}
	stagedSet := map[string]bool{}
	for _, e := range staged {
		stagedSet[e.Name] = true
	}
	var p Pending
	for n := range stagedSet {
		if !bootSet[n] {
			p.Added = append(p.Added, n)
		}
	}
	for n := range bootSet {
		if !stagedSet[n] {
			p.Removed = append(p.Removed, n)
		}
	}
	for id, sha := range latestSHAs {
		if boot.SourceSHAs[id] != sha {
			p.Changed = append(p.Changed, id)
		}
	}
	sort.Strings(p.Added)
	sort.Strings(p.Removed)
	sort.Strings(p.Changed)
	p.Restart = len(p.Added)+len(p.Removed)+len(p.Changed) > 0
	return p
}
