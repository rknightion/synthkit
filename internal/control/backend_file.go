// SPDX-License-Identifier: AGPL-3.0-only
package control

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"sync"

	"github.com/rknightion/synthkit/internal/ha"
)

type fileBackend struct {
	gate     ha.LeaderGate
	paths    map[Key]string
	mu       sync.Mutex
	versions map[Key]fileVersion
}

func NewFileBackend(o FileBackendOptions) (StateBackend, error) {
	if o.Gate == nil || len(o.Paths) == 0 {
		return nil, fmt.Errorf("state: invalid file backend options")
	}
	b := &fileBackend{gate: o.Gate, paths: map[Key]string{}, versions: map[Key]fileVersion{}}
	paths := map[string]bool{}
	for k, p := range o.Paths {
		p = filepath.Clean(p)
		if !validKey(k) || p == "." || paths[p] {
			return nil, fmt.Errorf("state: invalid or duplicate path")
		}
		paths[p] = true
		b.paths[k] = p
	}
	return b, nil
}

type fileVersion struct {
	digest   Revision
	sequence uint64
}

func (b *fileBackend) revision(key Key, data []byte, commit bool) Revision {
	digest := fileRevision(data)
	v := b.versions[key]
	if v.sequence == 0 || digest != v.digest || commit {
		v.sequence++
		v.digest = digest
		b.versions[key] = v
	}
	return Revision(strconv.FormatUint(v.sequence, 10) + "/" + string(v.digest))
}
func fileRevision(data []byte) Revision {
	sum := sha256.Sum256(data)
	return Revision(hex.EncodeToString(sum[:]))
}
func (b *fileBackend) read(ctx context.Context, key Key) (Snapshot, error) {
	if err := ctx.Err(); err != nil {
		return Snapshot{}, err
	}
	p, ok := b.paths[key]
	if !ok {
		return Snapshot{}, fmt.Errorf("state: unmapped key")
	}
	data, err := os.ReadFile(p)
	if os.IsNotExist(err) {
		data = nil
		err = nil
	}
	if err != nil {
		return Snapshot{}, err
	}
	return Snapshot{Data: data, Revision: b.revision(key, data, false)}, nil
}
func (b *fileBackend) Load(ctx context.Context, key Key) (Snapshot, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.read(ctx, key)
}
func (b *fileBackend) CompareAndSwap(ctx context.Context, key Key, rev Revision, data []byte) (Revision, error) {
	owned := append([]byte(nil), data...)
	var out Revision
	err := b.gate.Do(ctx, ha.Mutation, func(c context.Context) error {
		b.mu.Lock()
		defer b.mu.Unlock()
		snap, err := b.read(c, key)
		if err != nil {
			return err
		}
		if snap.Revision != rev {
			return ErrConflict
		}
		p := b.paths[key]
		tmp, err := os.CreateTemp(filepath.Dir(p), ".state-*.tmp")
		if err != nil {
			return err
		}
		defer os.Remove(tmp.Name())
		if _, err := tmp.Write(owned); err != nil {
			tmp.Close()
			return err
		}
		if err := tmp.Close(); err != nil {
			return err
		}
		if err := c.Err(); err != nil {
			return err
		}
		if err := os.Rename(tmp.Name(), p); err != nil {
			return err
		}
		out = b.revision(key, owned, true)
		return nil
	})
	return out, err
}
