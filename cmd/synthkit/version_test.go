// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"encoding/json"
	"errors"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestVersionFlagReportsStampedIdentity(t *testing.T) {
	const (
		wantVersion  = "1.3.0-rc.test"
		wantRevision = "0123456789abcdef0123456789abcdef01234567"
	)

	binary := filepath.Join(t.TempDir(), "synthkit")
	build := exec.Command("go", "build", "-ldflags", "-X main.version="+wantVersion+" -X main.revision="+wantRevision, "-o", binary, ".")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build stamped synthkit: %v\n%s", err, output)
	}

	output, err := exec.Command(binary, "-version").Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			t.Fatalf("synthkit -version: %v\n%s", err, exitErr.Stderr)
		}
		t.Fatalf("synthkit -version: %v", err)
	}
	var got struct {
		Version  string `json:"version"`
		Revision string `json:"revision"`
	}
	if err := json.Unmarshal(output, &got); err != nil {
		t.Fatalf("decode version JSON %q: %v", output, err)
	}
	if got.Version != wantVersion || got.Revision != wantRevision {
		t.Fatalf("version identity = %+v, want version=%q revision=%q", got, wantVersion, wantRevision)
	}
}
