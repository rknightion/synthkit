// SPDX-License-Identifier: AGPL-3.0-only

// Package checkpoint supplies an optional, bounded filesystem persistence primitive.
// It does not snapshot emitters, replay telemetry or establish Kubernetes leadership.
package checkpoint

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"github.com/rknightion/synthkit/internal/ha"
)

var (
	ErrConflict    = errors.New("checkpoint revision or epoch changed")
	ErrNotOwner    = errors.New("checkpoint epoch is not owned by this store instance")
	ErrBinding     = errors.New("checkpoint deployment, destination, configuration or format binding differs")
	ErrCorrupt     = errors.New("checkpoint data or manifest is corrupt")
	ErrUnbound     = errors.New("checkpoint directory contains unbound data")
	ErrCapacity    = errors.New("checkpoint capacity exceeded")
	ErrUnsupported = errors.New("checkpoint platform or format is unsupported")
	ErrInvalid     = errors.New("invalid checkpoint configuration")
	ErrIO          = errors.New("checkpoint filesystem operation failed")
)

const headLimit = 4096

type ioFailure struct{ cause error }

func (e ioFailure) Error() string        { return ErrIO.Error() }
func (e ioFailure) Unwrap() error        { return e.cause }
func (e ioFailure) Is(target error) bool { return target == ErrIO }
func failedIO(err error) error {
	if err == nil {
		return nil
	}
	return ioFailure{err}
}

// Binding contains fingerprints, never credentials. Format names the caller's
// versioned payload contract; the store cannot prove the caller captured all state.
type Binding struct {
	Deployment          string `json:"deployment"`
	DestinationSHA256   string `json:"destination_sha256"`
	ConfigurationSHA256 string `json:"configuration_sha256"`
	Format              string `json:"format"`
}

type Limits struct {
	MaxPayloadBytes  int64
	MaxRetainedBytes int64 // Unique-inode file content; not an allocated-block estimate.
	MaxFiles         int
}

type Head struct {
	Version       int     `json:"version"`
	Binding       Binding `json:"binding"`
	Epoch         uint64  `json:"epoch"`
	Revision      uint64  `json:"revision"`
	PayloadSHA256 string  `json:"payload_sha256"`
	Checksum      string  `json:"checksum"`
}

type Snapshot struct {
	Head    Head
	Payload []byte
	Exists  bool // false is positively observed uninitialized storage, not a read failure.
}

// Store is the persistence seam; payload format and complete emitter coverage are
// caller responsibilities. The first implementation is optional filesystem storage.
type Store interface {
	Read(context.Context) (Snapshot, error)
	AcquireEpoch(context.Context, Head) (Snapshot, error)
	Commit(context.Context, Head, []byte) (Snapshot, error)
}

type FileStore struct {
	dir       string
	binding   Binding
	limits    Limits
	gate      ha.LeaderGate
	heldEpoch atomic.Uint64
}

// NewFileStore creates a private dedicated directory for writers. Nil gate opens
// only an existing directory and rejects publication. HA callers must supply the actual lease-backed Gate,
// including its preparation context when acquiring an epoch. AlwaysLeader is only
// appropriate for explicitly single-writer non-HA use. No runtime enables this yet.
func NewFileStore(dir string, binding Binding, limits Limits, gate ha.LeaderGate) (*FileStore, error) {
	if !checkpointPlatformSupported() {
		return nil, ErrUnsupported
	}
	if dir == "" || !validIdentifier(binding.Deployment) || !validIdentifier(binding.Format) || !validHash(binding.DestinationSHA256) || !validHash(binding.ConfigurationSHA256) || limits.MaxPayloadBytes < 1 || limits.MaxPayloadBytes > 1<<30 || limits.MaxRetainedBytes < limits.MaxPayloadBytes+2*headLimit || limits.MaxFiles < 4 || limits.MaxFiles > 100000 {
		return nil, ErrInvalid
	}
	if gate != nil {
		if err := os.MkdirAll(dir, 0700); err != nil {
			return nil, failedIO(err)
		}
	}
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return nil, failedIO(err)
	}
	resolved, err = filepath.Abs(resolved)
	if err != nil {
		return nil, failedIO(err)
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return nil, failedIO(err)
	}
	if !info.IsDir() || info.Mode().Perm()&0022 != 0 {
		return nil, ErrInvalid
	}
	return &FileStore{dir: resolved, binding: binding, limits: limits, gate: gate}, nil
}

func validIdentifier(s string) bool {
	if len(s) == 0 || len(s) > 128 {
		return false
	}
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("._-:/", r)) {
			return false
		}
	}
	return true
}
func validHash(s string) bool {
	if len(s) != 64 {
		return false
	}
	for _, r := range s {
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f') {
			return false
		}
	}
	return true
}
func digest(data []byte) string  { sum := sha256.Sum256(data); return hex.EncodeToString(sum[:]) }
func headChecksum(h Head) string { h.Checksum = ""; raw, _ := json.Marshal(h); return digest(raw) }

// Read observes one atomic manifest and its immutable payload. There is no GC,
// so a concurrent publication cannot remove the object referenced by this read.
func (s *FileStore) Read(ctx context.Context) (Snapshot, error) {
	if err := ctx.Err(); err != nil {
		return Snapshot{}, err
	}
	raw, err := readCheckpointFile(filepath.Join(s.dir, "head.json"), headLimit)
	if errors.Is(err, os.ErrNotExist) {
		entries, _, err := s.inventory()
		if err != nil {
			return Snapshot{}, err
		}
		for _, entry := range entries {
			if entry.Name() != ".lock" {
				return Snapshot{}, ErrUnbound
			}
		}
		return Snapshot{Head: Head{Version: 1, Binding: s.binding}}, ctx.Err()
	}
	if err != nil {
		return Snapshot{}, failedIO(err)
	}
	var head Head
	if err := json.Unmarshal(raw, &head); err != nil {
		return Snapshot{}, ErrCorrupt
	}
	if head.Version != 1 {
		return Snapshot{}, ErrUnsupported
	}
	canonical, _ := json.Marshal(head)
	if !bytes.Equal(bytes.TrimSpace(raw), canonical) || head.Epoch == 0 || head.Revision == 0 || !validHash(head.Checksum) || head.Checksum != headChecksum(head) || (head.PayloadSHA256 != "" && !validHash(head.PayloadSHA256)) {
		return Snapshot{}, ErrCorrupt
	}
	if head.Binding != s.binding {
		return Snapshot{}, ErrBinding
	}
	out := Snapshot{Head: head, Exists: true}
	if head.PayloadSHA256 != "" {
		data, err := readCheckpointFile(s.payloadPath(head.PayloadSHA256), s.limits.MaxPayloadBytes)
		if err != nil {
			return Snapshot{}, failedIO(err)
		}
		if digest(data) != head.PayloadSHA256 {
			return Snapshot{}, ErrCorrupt
		}
		out.Payload = data
	}
	if err := ctx.Err(); err != nil {
		return Snapshot{}, err
	}
	return out, nil
}

// AcquireEpoch requires the caller's current authority and an exact prior head.
// One FileStore instance acquires at most one epoch. Re-reading a newer head does
// not let an old writer adopt another leader's epoch.
func (s *FileStore) AcquireEpoch(ctx context.Context, expected Head) (Snapshot, error) {
	var out Snapshot
	err := s.mutate(ctx, func(c context.Context) error {
		if s.heldEpoch.Load() != 0 {
			return ErrNotOwner
		}
		current, err := s.Read(c)
		if err != nil {
			return err
		}
		if current.Head != expected {
			return ErrConflict
		}
		if current.Head.Epoch == math.MaxUint64 || current.Head.Revision == math.MaxUint64 {
			return ErrCapacity
		}
		next := current.Head
		next.Epoch++
		next.Revision++
		if err := s.reserve(0, 1); err != nil {
			return err
		}
		if err := s.publishHead(c, &next); err != nil {
			return err
		}
		s.heldEpoch.Store(next.Epoch)
		out = Snapshot{Head: next, Payload: current.Payload, Exists: true}
		return nil
	})
	return out, err
}

// Commit publishes owned bytes only in this instance's acquired epoch. CAS,
// binding, authority and capacity failures do not replace the prior head. An I/O
// failure after rename can be ambiguous: re-read before retrying, never assume rollback.
func (s *FileStore) Commit(ctx context.Context, expected Head, payload []byte) (Snapshot, error) {
	if int64(len(payload)) > s.limits.MaxPayloadBytes {
		return Snapshot{}, ErrCapacity
	}
	owned := append([]byte(nil), payload...)
	var out Snapshot
	err := s.mutate(ctx, func(c context.Context) error {
		if s.heldEpoch.Load() == 0 || expected.Epoch != s.heldEpoch.Load() {
			return ErrNotOwner
		}
		current, err := s.Read(c)
		if err != nil {
			return err
		}
		if current.Head != expected || current.Head.Epoch != s.heldEpoch.Load() {
			return ErrConflict
		}
		if current.Head.Revision == math.MaxUint64 {
			return ErrCapacity
		}
		hash := digest(owned)
		existing, err := readCheckpointFile(s.payloadPath(hash), s.limits.MaxPayloadBytes)
		newPayload := errors.Is(err, os.ErrNotExist)
		if err != nil && !newPayload {
			return failedIO(err)
		}
		if !newPayload && !bytes.Equal(existing, owned) {
			return ErrCorrupt
		}
		bytesNeeded, filesNeeded := int64(0), 1
		if newPayload {
			bytesNeeded = int64(len(owned))
			filesNeeded++
		}
		if err := s.reserve(bytesNeeded, filesNeeded); err != nil {
			return err
		}
		if newPayload {
			if err := s.publish(c, s.payloadPath(hash), owned, true); err != nil {
				return err
			}
		}
		next := current.Head
		next.Revision++
		next.PayloadSHA256 = hash
		if err := s.publishHead(c, &next); err != nil {
			return err
		}
		out = Snapshot{Head: next, Payload: owned, Exists: true}
		return nil
	})
	return out, err
}

func (s *FileStore) mutate(ctx context.Context, fn func(context.Context) error) error {
	if s.gate == nil {
		return ha.ErrNotLeader
	}
	return s.gate.Do(ctx, ha.Mutation, func(c context.Context) error {
		bounded, cancel := context.WithTimeout(c, 5*time.Second)
		defer cancel()
		lock, err := lockCheckpointFile(bounded, filepath.Join(s.dir, ".lock"))
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return err
			}
			return failedIO(err)
		}
		defer lock.Close()
		if err := bounded.Err(); err != nil {
			return err
		}
		return fn(bounded)
	})
}

func (s *FileStore) inventory() ([]os.DirEntry, int64, error) {
	f, err := os.Open(s.dir)
	if err != nil {
		return nil, 0, failedIO(err)
	}
	defer f.Close()
	entries, err := f.ReadDir(s.limits.MaxFiles + 1)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, 0, failedIO(err)
	}
	if len(entries) > s.limits.MaxFiles {
		return nil, 0, ErrCapacity
	}
	var size int64
	seen := map[[2]uint64]bool{}
	for _, entry := range entries {
		name := entry.Name()
		known := name == "head.json" || name == ".lock" || strings.HasPrefix(name, ".pending-") || (strings.HasPrefix(name, "payload-") && strings.HasSuffix(name, ".bin") && validHash(strings.TrimSuffix(strings.TrimPrefix(name, "payload-"), ".bin")))
		info, err := entry.Info()
		if err != nil {
			return nil, 0, failedIO(err)
		}
		if !known || !info.Mode().IsRegular() {
			return nil, 0, ErrUnbound
		}
		if name == ".lock" && info.Size() != 0 {
			return nil, 0, ErrUnbound
		}
		identity, ok := checkpointFileIdentity(info)
		if !ok {
			return nil, 0, ErrUnsupported
		}
		if seen[identity] {
			continue
		}
		seen[identity] = true
		if info.Size() > s.limits.MaxRetainedBytes-size {
			return nil, 0, ErrCapacity
		}
		size += info.Size()
	}
	return entries, size, nil
}

func (s *FileStore) reserve(payload int64, files int) error {
	entries, used, err := s.inventory()
	if err != nil {
		return err
	}
	if len(entries)+files > s.limits.MaxFiles || used > s.limits.MaxRetainedBytes-payload-headLimit {
		return ErrCapacity
	}
	return nil
}
func (s *FileStore) payloadPath(hash string) string {
	return filepath.Join(s.dir, "payload-"+hash+".bin")
}
func (s *FileStore) publishHead(ctx context.Context, head *Head) error {
	head.Checksum = headChecksum(*head)
	raw, err := json.Marshal(head)
	if err != nil {
		return ErrCorrupt
	}
	if len(raw) > headLimit {
		return ErrCapacity
	}
	return s.publish(ctx, filepath.Join(s.dir, "head.json"), raw, false)
}

func (s *FileStore) publish(ctx context.Context, target string, data []byte, immutable bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	f, err := os.CreateTemp(s.dir, ".pending-")
	if err != nil {
		return failedIO(err)
	}
	// Failed temporary writes remain bounded, inspectable evidence. Only a
	// successful hard link permits removing its byte-identical temporary name.
	if n, err := f.Write(data); err != nil || n != len(data) {
		f.Close()
		if err == nil {
			err = io.ErrShortWrite
		}
		return failedIO(err)
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return failedIO(err)
	}
	if err := f.Close(); err != nil {
		return failedIO(err)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if immutable {
		if err := os.Link(f.Name(), target); err != nil {
			return failedIO(err)
		}
		if err := os.Remove(f.Name()); err != nil {
			return failedIO(err)
		}
	} else if err := os.Rename(f.Name(), target); err != nil {
		return failedIO(err)
	}
	dir, err := os.Open(s.dir)
	if err != nil {
		return failedIO(err)
	}
	defer dir.Close()
	return failedIO(dir.Sync())
}

func readCheckpointFile(path string, limit int64) ([]byte, error) {
	f, err := openCheckpointFile(path, os.O_RDONLY, 0)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > limit {
		return nil, ErrCorrupt
	}
	data, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, ErrCorrupt
	}
	return data, nil
}
