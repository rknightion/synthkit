// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestValidateCLI(t *testing.T) {
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	tmp := t.TempDir()
	binary := filepath.Join(tmp, "synthkit")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	build := exec.CommandContext(ctx, "go", "build", "-o", binary, "./cmd/synthkit")
	build.Dir = root
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build: %v: %s", err, out)
	}
	var calls atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); w.WriteHeader(500) }))
	defer server.Close()
	fixture := filepath.Join(tmp, "fixture")
	if err := os.Mkdir(fixture, 0700); err != nil {
		t.Fatal(err)
	}
	original, err := os.ReadFile(filepath.Join(root, "blueprints", "k8s-minimal.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fixture, "a.yaml"), original, 0600); err != nil {
		t.Fatal(err)
	}
	collision := strings.Replace(string(original), "name: k8s-minimal", "name: collision", 1)
	if collision == string(original) {
		t.Fatal("fixture blueprint identity not replaced")
	}
	if err := os.WriteFile(filepath.Join(fixture, "b.yaml"), []byte(collision), 0600); err != nil {
		t.Fatal(err)
	}
	schemaDir := filepath.Join(tmp, "schema")
	if err := os.Mkdir(schemaDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(schemaDir, "bad.yaml"), []byte("name: bad\nunknown_field: true\n"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, dir, selection string
		ok                   bool
	}{
		{"shipped", filepath.Join(root, "blueprints"), "otlp-native", true},
		{"collision", fixture, "*", false},
		{"missing", fixture, "missing", false},
		{"schema", schemaDir, "bad", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			state := filepath.Join(tmp, tc.name+"-state")
			cmd := exec.CommandContext(ctx, binary, "-validate", "-env", filepath.Join(tmp, "absent.env"))
			cmd.Dir = tmp
			cmd.Env = append(os.Environ(), "BLUEPRINTS="+tc.dir, "BLUEPRINT_NAMES="+tc.selection, "BLUEPRINT_DATA_DIR="+state, "CONFIG_SNAPSHOT_PATH="+state+".json", "DRY_RUN=false", "SELFOBS_ENABLED=true", "GC_PROM_RW="+server.URL, "GC_LOKI="+server.URL, "GC_OTLP_ENDPOINT="+server.URL, "GC_SELF_OTLP_ENDPOINT="+server.URL, "GC_PYROSCOPE_SERVER="+server.URL)
			var stderr bytes.Buffer
			cmd.Stderr = &stderr
			out, err := cmd.Output()
			if (err == nil) != tc.ok {
				t.Fatalf("exit: %v, stdout: %s, stderr: %s", err, out, stderr.String())
			}
			var report struct {
				OK         bool `json:"ok"`
				Blueprints []struct {
					Name        string `json:"name"`
					Cardinality int    `json:"cardinality"`
				} `json:"blueprints"`
				Diagnostics []json.RawMessage `json:"diagnostics"`
			}
			if err := json.Unmarshal(out, &report); err != nil {
				t.Fatalf("JSON: %v: %s", err, out)
			}
			if report.OK != tc.ok {
				t.Fatalf("ok=%v", report.OK)
			}
			if tc.ok && (len(report.Blueprints) != 1 || report.Blueprints[0].Name != "otlp-native" || report.Blueprints[0].Cardinality <= 0) {
				t.Fatalf("missing native metric projection: %+v", report)
			}
			if !tc.ok && len(report.Diagnostics) == 0 {
				t.Fatal("missing diagnostics")
			}
			if tc.name == "collision" && !strings.Contains(string(out), "collision") {
				t.Fatalf("no collision diagnostic: %s", out)
			}
			if tc.name == "schema" && !strings.Contains(string(out), "unknown_field") {
				t.Fatalf("no schema diagnostic: %s", out)
			}
			for _, path := range []string{state, state + ".json"} {
				if _, err := os.Stat(path); !os.IsNotExist(err) {
					t.Fatalf("state path touched: %s: %v", path, err)
				}
			}
		})
	}
	if calls.Load() != 0 {
		t.Fatalf("network calls: %d", calls.Load())
	}
}

func TestValidateStagedSourcesReadOnly(t *testing.T) {
	tmp := t.TempDir()
	baked := filepath.Join(tmp, "baked")
	data := filepath.Join(tmp, "data")
	custom := filepath.Join(data, "custom")
	git := filepath.Join(data, "git", "local")
	for _, dir := range []string{baked, custom, git} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
	}
	snapshot := filepath.Join(tmp, "state.json")
	files := map[string]string{
		filepath.Join(custom, "upload__a.yaml"):    "name: a\nenvironments: [{name: test}]\n",
		filepath.Join(git, "b.yaml"):               "name: b\nenvironments: [{name: test}]\n",
		filepath.Join(data, ".boot-manifest.json"): "{}",
		snapshot: `{"blueprint_sources":[{"id":"local","name":"local","namespace":"repo","url":"https://example.invalid/repo.git","ref":"refs/heads/main","fetched_sha":"abc"}]}`,
	}
	for path, content := range files {
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}
	type stamp struct {
		Hash     [32]byte
		Mode     os.FileMode
		Modified time.Time
	}
	capture := func() map[string]stamp {
		t.Helper()
		result := map[string]stamp{}
		err := filepath.Walk(tmp, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			s := stamp{Mode: info.Mode(), Modified: info.ModTime()}
			if !info.IsDir() {
				b, err := os.ReadFile(path)
				if err != nil {
					return err
				}
				s.Hash = sha256.Sum256(b)
			}
			result[path] = s
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	before := capture()
	t.Setenv("BLUEPRINTS", baked)
	t.Setenv("BLUEPRINT_DATA_DIR", data)
	t.Setenv("CONFIG_SNAPSHOT_PATH", snapshot)
	t.Setenv("BLUEPRINT_NAMES", "upload/a,repo/b")
	var out bytes.Buffer
	if err := runValidate(filepath.Join(tmp, "absent.env"), &out); err != nil {
		t.Fatalf("validate: %v: %s", err, out.String())
	}
	var report validateReport
	if err := json.Unmarshal(out.Bytes(), &report); err != nil {
		t.Fatal(err)
	}
	if len(report.Blueprints) != 2 || report.Blueprints[0].Name != "repo/b" || report.Blueprints[1].Name != "upload/a" {
		t.Fatalf("source selection: %s", out.String())
	}
	after := capture()
	if len(before) != len(after) {
		t.Fatal("state files added or removed")
	}
	for path, expected := range before {
		if after[path] != expected {
			t.Fatalf("state mutated: %s", path)
		}
	}
}
