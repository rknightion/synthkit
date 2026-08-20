// SPDX-License-Identifier: AGPL-3.0-only

package bpsource

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWritePrivateFileReplacesDestinationSymlinkWithoutFollowingIt(t *testing.T) {
	dir := t.TempDir()
	external := filepath.Join(t.TempDir(), "external.yaml")
	if err := os.WriteFile(external, []byte("external"), 0o644); err != nil {
		t.Fatal(err)
	}
	dst := filepath.Join(dir, "private.yaml")
	if err := os.Symlink(external, dst); err != nil {
		t.Fatal(err)
	}
	if err := writePrivateFile(dst, []byte("replacement")); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(external); err != nil || string(got) != "external" {
		t.Fatalf("external symlink target changed: data=%q err=%v", got, err)
	}
	info, err := os.Lstat(dst)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm() != 0o600 {
		t.Fatalf("destination mode = %v, want regular 0600 file", info.Mode())
	}
}
