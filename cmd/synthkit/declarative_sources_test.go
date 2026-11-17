// SPDX-License-Identifier: AGPL-3.0-only
package main

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/control"
)

// Exercise the shipped process, not a proxy for runtime config and watch wiring.
func TestDeclarativeSourcesProcessBoundary(t *testing.T) {
	root := t.TempDir()
	binary := buildSynthkit(t, root)
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
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	// The process binds its own ephemeral control port and logs it; the test reserves none.
	logPath := startNonHA(t, ctx, binary, root, "GIT_SOURCES_FILE="+desired)
	addr := awaitLoggedControlAddr(t, logPath)
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
