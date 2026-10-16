// SPDX-License-Identifier: AGPL-3.0-only
package main

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/control"
)

// Exercise the shipped process, not a proxy for runtime config and watch wiring.
func TestDeclarativeSourcesProcessBoundary(t *testing.T) {
	root := t.TempDir()
	binary := filepath.Join(root, "synthkit")
	buildCtx, buildCancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer buildCancel()
	if out, err := exec.CommandContext(buildCtx, "go", "build", "-o", binary, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	desired := filepath.Join(root, "sources.json")
	write := func(data string) {
		t.Helper()
		tmp := desired + ".next"
		if err := os.WriteFile(tmp, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(tmp, desired); err != nil {
			t.Fatal(err)
		}
	}
	write(`[{"id":"declared","name":"First","namespace":"declared","url":"https://example.invalid/repo","ref":"refs/heads/main"}]`)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := listener.Addr().String()
	listener.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, "-env", filepath.Join(root, "absent.env"))
	cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + root, "DRY_RUN=true", "JSON_HTTP_ADDR=" + addr,
		// Inside a container (a containerised CI runner or gate host) the exposure check requires the
		// host bind; the listener is loopback, so declare a loopback bind and the test runs anywhere.
		"SYNTHKIT_BIND=127.0.0.1", "BLUEPRINTS=" + root, "BLUEPRINT_NAMES=", "BLUEPRINT_DATA_DIR=" + filepath.Join(root, "blueprints"), "CONFIG_SNAPSHOT_PATH=" + filepath.Join(root, "state.json"), "GIT_SOURCES_FILE=" + desired}
	logFile, err := os.Create(filepath.Join(root, "process.log"))
	if err != nil {
		t.Fatal(err)
	}
	defer logFile.Close()
	cmd.Stdout, cmd.Stderr = logFile, logFile
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { cancel(); _ = cmd.Wait() }()
	client := &http.Client{Timeout: time.Second}
	sources := func() ([]control.SourceView, error) {
		resp, e := client.Get("http://" + addr + "/control/blueprints/sources")
		if e != nil {
			return nil, e
		}
		defer resp.Body.Close()
		var got []control.SourceView
		e = json.NewDecoder(resp.Body).Decode(&got)
		return got, e
	}
	await := func(name string, count int) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			got, e := sources()
			if e == nil && len(got) == count {
				if count == 0 {
					return
				}
				for _, source := range got {
					if source.Name == name {
						return
					}
				}
			}
			time.Sleep(30 * time.Millisecond)
		}
		got, e := sources()
		t.Fatalf("desired set not projected through real HTTP process: want %q count %d; got %+v error %v", name, count, got, e)
	}
	await("First", 1)
	write(`[{"id":"declared","name":"Changed","namespace":"declared","url":"https://example.invalid/other","ref":"refs/heads/main"}]`)
	await("Changed", 1)
	console, err := client.Post("http://"+addr+"/control/blueprints/sources", "application/json", strings.NewReader(`{"id":"console","name":"Console","namespace":"console","url":"https://example.invalid/repo","ref":"refs/heads/main"}`))
	if err != nil {
		t.Fatal(err)
	}
	console.Body.Close()
	if console.StatusCode != 200 {
		t.Fatalf("console create status %d", console.StatusCode)
	}
	write(`[{"id":"declared","name":"Changed","namespace":"declared","url":"https://example.invalid/other","ref":"refs/heads/main"},{"id":"added","name":"Added","namespace":"added","url":"https://example.invalid/repo","ref":"main"}]`)
	await("Added", 3)
	req, _ := http.NewRequest(http.MethodDelete, "http://"+addr+"/control/blueprints/sources?id=declared", nil)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("declared delete status %d", resp.StatusCode)
	}
	req, _ = http.NewRequest(http.MethodPost, "http://"+addr+"/control/blueprints/sources", strings.NewReader(`{"id":"declared","name":"Edited","namespace":"declared","url":"https://example.invalid/repo","ref":"refs/heads/main"}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err = client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("declared edit status %d", resp.StatusCode)
	}
	write(`[]`)
	await("Console", 1)
}
