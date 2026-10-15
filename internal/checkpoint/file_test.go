// SPDX-License-Identifier: AGPL-3.0-only

package checkpoint

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/ha"
)

func proofBinding() Binding {
	return Binding{Deployment: "checkpoint-proof", DestinationSHA256: digest([]byte("destination")), ConfigurationSHA256: digest([]byte("config")), Format: "opaque-proof/v1"}
}
func proofLimits() Limits {
	return Limits{MaxPayloadBytes: 1024, MaxRetainedBytes: 64 * 1024, MaxFiles: 16}
}
func proofStore(t *testing.T, dir string, gate ha.LeaderGate) *FileStore {
	t.Helper()
	if !checkpointPlatformSupported() {
		t.Skip("filesystem backend is intentionally unsupported on this platform")
	}
	s, err := NewFileStore(dir, proofBinding(), proofLimits(), gate)
	if err != nil {
		t.Fatal(err)
	}
	return s
}
func acquireProof(t *testing.T, s *FileStore) Snapshot {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	before, err := s.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	owned, err := s.AcquireEpoch(ctx, before.Head)
	if err != nil {
		t.Fatal(err)
	}
	return owned
}

func TestFileCheckpointImmutableRoundTripAndCAS(t *testing.T) {
	s := proofStore(t, t.TempDir(), ha.AlwaysLeader{})
	before, err := s.Read(context.Background())
	if err != nil || before.Exists {
		t.Fatalf("initial read: %+v %v", before, err)
	}
	owned := acquireProof(t, s)
	payload := []byte(`{"counter":3}`)
	first, err := s.Commit(context.Background(), owned.Head, payload)
	if err != nil {
		t.Fatal(err)
	}
	payload[0] = 'x'
	first.Payload[0] = 'y'
	read, err := s.Read(context.Background())
	if err != nil || string(read.Payload) != `{"counter":3}` {
		t.Fatalf("caller mutation reached persisted bytes: %q %v", read.Payload, err)
	}
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for _, data := range []string{"next-a", "next-b"} {
		wg.Add(1)
		go func(data string) {
			defer wg.Done()
			_, err := s.Commit(context.Background(), read.Head, []byte(data))
			results <- err
		}(data)
	}
	wg.Wait()
	close(results)
	passed, conflicts := 0, 0
	for err := range results {
		if err == nil {
			passed++
		} else if errors.Is(err, ErrConflict) {
			conflicts++
		} else {
			t.Fatal(err)
		}
	}
	if passed != 1 || conflicts != 1 {
		t.Fatalf("CAS winners=%d conflicts=%d", passed, conflicts)
	}
	final, err := s.Read(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if final.Head.Revision != read.Head.Revision+1 || (string(final.Payload) != "next-a" && string(final.Payload) != "next-b") {
		t.Fatalf("bad final state %+v", final.Head)
	}
	for _, name := range []string{"head.json", filepath.Base(s.payloadPath(final.Head.PayloadSHA256))} {
		info, err := os.Stat(filepath.Join(s.dir, name))
		if err != nil || info.Mode().Perm() != 0600 {
			t.Fatalf("private mode %s: %v %v", name, info, err)
		}
	}
}

func TestReadOnlyCheckpointHandleDoesNotCreateStorage(t *testing.T) {
	if !checkpointPlatformSupported() {
		t.Skip("unsupported filesystem backend")
	}
	dir := filepath.Join(t.TempDir(), "absent")
	if _, err := NewFileStore(dir, proofBinding(), proofLimits(), nil); err == nil {
		t.Fatal("read-only handle created absent storage")
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("read-only construction changed filesystem")
	}
	s := proofStore(t, t.TempDir(), nil)
	read, err := s.Read(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AcquireEpoch(context.Background(), read.Head); !errors.Is(err, ha.ErrNotLeader) {
		t.Fatalf("nil authority published an epoch: %v", err)
	}
}

func TestCheckpointManifestChecksumProtectsEpoch(t *testing.T) {
	s := proofStore(t, t.TempDir(), ha.AlwaysLeader{})
	owned := acquireProof(t, s)
	if _, err := s.Commit(context.Background(), owned.Head, []byte("valid")); err != nil {
		t.Fatal(err)
	}
	read, err := s.Read(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	head := read.Head
	head.Epoch++
	raw, _ := json.Marshal(head)
	if err := os.WriteFile(filepath.Join(s.dir, "head.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Read(context.Background()); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("changed epoch escaped manifest checksum: %v", err)
	}
}

func TestCheckpointGateAndEpochCannotBeRefreshedByOldWriter(t *testing.T) {
	dir := t.TempDir()
	g1 := ha.NewGate()
	s1 := proofStore(t, dir, g1)
	var first Snapshot
	err := g1.Activate(context.Background(), func(ctx context.Context) error {
		read, err := s1.Read(ctx)
		if err != nil {
			return err
		}
		first, err = s1.AcquireEpoch(ctx, read.Head)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	first, err = s1.Commit(context.Background(), first.Head, []byte("old"))
	if err != nil {
		t.Fatal(err)
	}
	g2 := ha.NewGate()
	s2 := proofStore(t, dir, g2)
	var second Snapshot
	if err := g2.Activate(context.Background(), func(ctx context.Context) error {
		read, err := s2.Read(ctx)
		if err != nil {
			return err
		}
		second, err = s2.AcquireEpoch(ctx, read.Head)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if string(second.Payload) != "old" || second.Head.Epoch != first.Head.Epoch+1 {
		t.Fatal("takeover lost payload or epoch")
	}
	second, err = s2.Commit(context.Background(), second.Head, []byte("new"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s1.Commit(context.Background(), first.Head, []byte("stale")); !errors.Is(err, ErrConflict) {
		t.Fatalf("old epoch commit: %v", err)
	}
	fresh, err := s1.Read(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s1.Commit(context.Background(), fresh.Head, []byte("adopted")); !errors.Is(err, ErrNotOwner) {
		t.Fatalf("old writer adopted refreshed epoch: %v", err)
	}
	if _, err := s1.AcquireEpoch(context.Background(), fresh.Head); !errors.Is(err, ErrNotOwner) {
		t.Fatalf("old instance reacquired epoch: %v", err)
	}
	g2.Revoke()
	if _, err := s2.Commit(context.Background(), second.Head, []byte("revoked")); !errors.Is(err, ha.ErrNotLeader) {
		t.Fatalf("revoked authority wrote: %v", err)
	}
	final, err := s1.Read(context.Background())
	if err != nil || string(final.Payload) != "new" {
		t.Fatal("denied writer changed persisted state")
	}
}

func TestCheckpointBindingCorruptionAndCapacityFailClosed(t *testing.T) {
	s := proofStore(t, t.TempDir(), ha.AlwaysLeader{})
	owned := acquireProof(t, s)
	committed, err := s.Commit(context.Background(), owned.Head, []byte("private-payload"))
	if err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(filepath.Join(s.dir, "head.json"))
	if err != nil {
		t.Fatal(err)
	}
	binding := proofBinding()
	binding.DestinationSHA256 = digest([]byte("other"))
	other, err := NewFileStore(s.dir, binding, proofLimits(), ha.AlwaysLeader{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := other.Read(context.Background()); !errors.Is(err, ErrBinding) {
		t.Fatalf("destination mismatch admitted: %v", err)
	}
	if err := os.WriteFile(s.payloadPath(committed.Head.PayloadSHA256), []byte("corrupted"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Commit(context.Background(), committed.Head, []byte("replacement")); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("corruption silently replaced: %v", err)
	}
	after, _ := os.ReadFile(filepath.Join(s.dir, "head.json"))
	if !bytes.Equal(before, after) {
		t.Fatal("failed commit changed head")
	}
	limited := proofLimits()
	limited.MaxFiles = 4
	q, err := NewFileStore(t.TempDir(), proofBinding(), limited, ha.AlwaysLeader{})
	if err != nil {
		t.Fatal(err)
	}
	state := acquireProof(t, q)
	state, err = q.Commit(context.Background(), state.Head, []byte("one"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := q.Commit(context.Background(), state.Head, []byte("two")); !errors.Is(err, ErrCapacity) {
		t.Fatalf("capacity bypassed: %v", err)
	}
	retained, err := q.Read(context.Background())
	if err != nil || retained.Head != state.Head || string(retained.Payload) != "one" {
		t.Fatal("capacity failure lost last checkpoint")
	}
}

func TestCheckpointRejectsUnboundDataAndSymlinks(t *testing.T) {
	for _, name := range []string{"legacy.bin", ".lock"} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, name), []byte("existing"), 0600); err != nil {
				t.Fatal(err)
			}
			s := proofStore(t, dir, ha.AlwaysLeader{})
			if _, err := s.Read(context.Background()); !errors.Is(err, ErrUnbound) {
				t.Fatalf("unbound data admitted: %v", err)
			}
		})
	}
	dir := t.TempDir()
	s := proofStore(t, dir, ha.AlwaysLeader{})
	owned := acquireProof(t, s)
	outside := filepath.Join(t.TempDir(), "outside")
	data := []byte("outside payload")
	if err := os.WriteFile(outside, data, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, s.payloadPath(digest(data))); err != nil {
		t.Fatal(err)
	}
	head := owned.Head
	head.PayloadSHA256 = digest(data)
	head.Checksum = headChecksum(head)
	raw, _ := json.Marshal(head)
	if err := os.WriteFile(filepath.Join(dir, "head.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Read(context.Background()); err == nil {
		t.Fatal("payload symlink followed")
	}
	unchanged, _ := os.ReadFile(outside)
	if !bytes.Equal(unchanged, data) {
		t.Fatal("outside file modified")
	}
}

func TestCheckpointProcessHelper(t *testing.T) {
	mode := os.Getenv("SYNTHKIT_CHECKPOINT_PROOF_MODE")
	if mode == "" {
		return
	}
	dir := os.Getenv("SYNTHKIT_CHECKPOINT_PROOF_DIR")
	if mode == "lock" {
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		defer cancel()
		f, err := lockCheckpointFile(ctx, filepath.Join(dir, ".lock"))
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		fmt.Println("locked")
		var b [1]byte
		if _, err := io.ReadFull(os.Stdin, b[:]); err != nil {
			t.Fatal(err)
		}
		return
	}
	s := proofStore(t, dir, ha.AlwaysLeader{})
	owned := acquireProof(t, s)
	if _, err := s.Commit(context.Background(), owned.Head, []byte("child")); err != nil {
		t.Fatal(err)
	}
}

func checkpointChild(ctx context.Context, dir, mode string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestCheckpointProcessHelper$")
	cmd.Env = append(os.Environ(), "SYNTHKIT_CHECKPOINT_PROOF_DIR="+dir, "SYNTHKIT_CHECKPOINT_PROOF_MODE="+mode, "GORACE="+os.Getenv("GORACE")+" atexit_sleep_ms=0")
	return cmd
}

func TestCheckpointCrossProcessLockAndEpoch(t *testing.T) {
	s := proofStore(t, t.TempDir(), ha.AlwaysLeader{})
	initial := acquireProof(t, s)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := checkpointChild(ctx, s.dir, "lock")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	var diagnostics bytes.Buffer
	cmd.Stderr = &diagnostics
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		stdin.Close()
		if cmd.ProcessState == nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	}()
	line, err := bufio.NewReader(stdout).ReadString('\n')
	if err != nil || strings.TrimSpace(line) != "locked" {
		t.Fatalf("child lock witness: %q %v %s", line, err, diagnostics.String())
	}
	bound, stop := context.WithTimeout(context.Background(), 50*time.Millisecond)
	_, err = s.Commit(bound, initial.Head, []byte("blocked"))
	stop()
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("cross-process lock ignored or wrong failure: %v", err)
	}
	_, _ = stdin.Write([]byte("x"))
	_ = stdin.Close()
	if err := cmd.Wait(); err != nil {
		t.Fatalf("child: %v %s", err, diagnostics.String())
	}
	takeover := checkpointChild(ctx, s.dir, "takeover")
	if out, err := takeover.CombinedOutput(); err != nil {
		t.Fatalf("takeover child: %v %s", err, out)
	}
	read, err := s.Read(context.Background())
	if err != nil || string(read.Payload) != "child" {
		t.Fatalf("child publication: %v", err)
	}
	if _, err := s.Commit(context.Background(), read.Head, []byte("stale-parent")); !errors.Is(err, ErrNotOwner) {
		t.Fatalf("parent adopted child epoch: %v", err)
	}
}
