// SPDX-License-Identifier: AGPL-3.0-only

//go:build e2e

// Package e2e is the Docker-level integration test for synthkit.
//
// It:
//  1. Builds and starts the receiver sidecar (e2e/receiver/Dockerfile).
//  2. Builds and starts a synthkit container (Dockerfile) in -once mode, wired to the
//     receiver over a shared Docker network, running ONLY the otlp-native blueprint.
//  3. Waits for the synthkit container to exit (RunOnce flushes the delivery queue before
//     returning, so exit = all pushes complete).
//  4. Fetches the receiver's /__inventory, builds the expected schema from `go run -once -dump`
//     on the host (DRY_RUN=true, isolated blueprint dir), and asserts expected ⊆ received.
//
// Tagged //go:build e2e so it is excluded from `go test ./...` (the normal gate).
// Run via `make e2e` which passes -tags e2e -timeout 15m.
package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/network"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/rknightion/synthkit/e2e/inventory"
)

// e2eBlueprint is the smoke blueprint: emits all lanes (RW2 metrics, OTLP metrics,
// OTLP traces for two services, Loki logs). Two independent -once -dump runs are
// byte-identical (deterministic seeding), so correlation is stable.
const e2eBlueprint = "otlp-native"

// TestDockerE2E is the integration capstone: builds both images, runs them, and
// asserts that every metric / log source / trace service declared by -dump arrived
// at the receiver.
func TestDockerE2E(t *testing.T) {
	ctx := context.Background()

	// Shared Docker network — both containers join it so synthkit can reach
	// "receiver:9099" by name.
	net, err := network.New(ctx)
	if err != nil {
		t.Fatalf("create docker network: %v", err)
	}
	testcontainers.CleanupNetwork(t, net)

	// ── 1. Receiver sidecar ────────────────────────────────────────────────────
	// Build from e2e/receiver/Dockerfile; context is repo root (build needs the
	// full Go module). KeepImage=true avoids a rebuild on every test run.
	t.Log("starting receiver sidecar…")
	receiverC, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			FromDockerfile: testcontainers.FromDockerfile{
				Context:    "..",
				Dockerfile: "e2e/receiver/Dockerfile",
				KeepImage:  true,
			},
			ExposedPorts: []string{"9099/tcp"},
			Networks:     []string{net.Name},
			NetworkAliases: map[string][]string{
				net.Name: {"receiver"},
			},
			WaitingFor: wait.ForListeningPort("9099/tcp").WithStartupTimeout(2 * time.Minute),
		},
		Started: true,
	})
	if err != nil {
		t.Fatalf("receiver container: %v", err)
	}
	testcontainers.CleanupContainer(t, receiverC)

	// ── 2. synthkit in -once mode ──────────────────────────────────────────────
	// The production Dockerfile bakes ALL blueprints at /app/blueprints. We want
	// only otlp-native, so we:
	//   • Copy blueprints/otlp-native.yaml into the container at
	//     /app/blueprints-e2e/otlp-native.yaml via ContainerFile.
	//   • Set BLUEPRINTS=/app/blueprints-e2e so synthkit finds only that one.
	//
	// The file must be readable by the distroless nonroot uid (65532); FileMode
	// 0o644 satisfies that without needing ownership change in a shell.
	blueprintHostPath, err := filepath.Abs("../blueprints/" + e2eBlueprint + ".yaml")
	if err != nil {
		t.Fatalf("resolve blueprint path: %v", err)
	}
	if _, err := os.Stat(blueprintHostPath); err != nil {
		t.Fatalf("blueprint file not found at %s: %v", blueprintHostPath, err)
	}

	t.Log("building synthkit image and running -once…")
	synthC, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			FromDockerfile: testcontainers.FromDockerfile{
				Context:    "..",
				Dockerfile: "Dockerfile",
				KeepImage:  true,
			},
			Networks: []string{net.Name},
			Env: map[string]string{
				"DRY_RUN":              "false",
				"GC_TOKEN":             "e2e",
				"GC_PROM_RW":           "http://receiver:9099/api/v1/write",
				"GC_PROM_USER":         "e2e",
				"GC_OTLP_ENDPOINT":     "http://receiver:9099",
				"GC_OTLP_USER":         "e2e",
				"GC_LOKI":              "http://receiver:9099/loki/api/v1/push",
				"GC_LOKI_USER":         "e2e",
				"GC_SIGIL_ENDPOINT":    "http://receiver:9099",
				"GC_SIGIL_TENANT_ID":   "e2e",
				"GC_SIGIL_TOKEN":       "e2e",
				"BLUEPRINTS":           "/app/blueprints-e2e",
			},
			Files: []testcontainers.ContainerFile{
				{
					HostFilePath:      blueprintHostPath,
					ContainerFilePath: "/app/blueprints-e2e/" + e2eBlueprint + ".yaml",
					FileMode:          0o644,
				},
			},
			Cmd: []string{"-once"},
			// RunOnce flushes the delivery queue before returning → exit = pushes done.
			WaitingFor: wait.ForExit().WithExitTimeout(5 * time.Minute),
		},
		Started: true,
	})
	if err != nil {
		// Dump synthkit container logs to aid diagnosis.
		if synthC != nil {
			printContainerLogs(t, ctx, synthC, "synthkit")
		}
		t.Fatalf("synthkit container: %v", err)
	}
	// Log synthkit output for debugging failures.
	printContainerLogs(t, ctx, synthC, "synthkit")
	testcontainers.CleanupContainer(t, synthC)

	// ── 3. Fetch the accumulated schema from the receiver ─────────────────────
	host, err := receiverC.Host(ctx)
	if err != nil {
		t.Fatalf("receiver host: %v", err)
	}
	port, err := receiverC.MappedPort(ctx, "9099/tcp")
	if err != nil {
		t.Fatalf("receiver mapped port: %v", err)
	}
	inventoryURL := fmt.Sprintf("http://%s:%s/__inventory", host, port.Port())
	t.Logf("fetching inventory from %s", inventoryURL)

	resp, err := http.Get(inventoryURL) //nolint:noctx // test-only helper
	if err != nil {
		t.Fatalf("GET /__inventory: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("/__inventory returned HTTP %d: %s", resp.StatusCode, body)
	}

	var received inventory.Schema
	if err := json.NewDecoder(resp.Body).Decode(&received); err != nil {
		t.Fatalf("decode inventory JSON: %v", err)
	}
	t.Logf("received: %d metrics, %d log sources, %d trace services, %d sigil kinds",
		len(received.Metrics), len(received.LogSources), len(received.Traces), len(received.Sigil))

	// ── 4. Build the expected schema from -dump on the host ───────────────────
	expected := dumpSchema(t)
	t.Logf("expected (from -dump): %d metrics, %d log sources, %d trace services, %d sigil kinds",
		len(expected.Metrics), len(expected.LogSources), len(expected.Traces), len(expected.Sigil))

	// ── 5. Correlation: every -dump-declared name must be present in received ──
	// received is a SUPERSET (it also captures native OTLP metric names not in
	// -dump); Subset only checks expected ⊆ received, so extras are fine.
	missing := expected.Subset(received)
	if len(missing) > 0 {
		t.Fatalf("telemetry declared by -dump but NOT received (%d entries):\n  %s",
			len(missing), strings.Join(missing, "\n  "))
	}
	t.Logf("PASS: all %d declared metrics + %d log sources + %d trace services + %d sigil kinds received",
		len(expected.Metrics), len(expected.LogSources), len(expected.Traces), len(expected.Sigil))
}

// dumpSchema runs `go run ../cmd/synthkit -once -dump` with DRY_RUN=true and a
// BLUEPRINTS dir containing ONLY the smoke blueprint, then parses the output.
func dumpSchema(t *testing.T) inventory.Schema {
	t.Helper()

	// Isolate: copy only the smoke blueprint into a temp dir so -dump reflects
	// exactly what the e2e container will emit.
	dir := t.TempDir()
	blueprintSrc, err := filepath.Abs("../blueprints/" + e2eBlueprint + ".yaml")
	if err != nil {
		t.Fatalf("resolve blueprint src: %v", err)
	}
	blueprintDst := filepath.Join(dir, e2eBlueprint+".yaml")
	if out, err := exec.Command("cp", blueprintSrc, blueprintDst).CombinedOutput(); err != nil {
		t.Fatalf("cp blueprint: %v (%s)", err, out)
	}

	cmd := exec.Command("go", "run", "../cmd/synthkit", "-once", "-dump")
	// Inherit the host environment so Go toolchain / module cache are available,
	// then override the vars that matter for a dry-run dump.
	env := os.Environ()
	env = setEnv(env, "DRY_RUN", "true")
	env = setEnv(env, "BLUEPRINTS", dir)
	// Keep synthkit's runtime state under the temp dir — the default ./data/blueprints
	// is relative to cwd (the e2e package dir), so without this the dump run litters an
	// e2e/data/ artifact into the repo tree.
	env = setEnv(env, "BLUEPRINT_DATA_DIR", filepath.Join(dir, "data"))
	cmd.Env = env

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		t.Fatalf("dump run failed: %v\nstderr:\n%s\nstdout:\n%s", err, stderr.String(), stdout.String())
	}

	s, err := inventory.ParseDump(&stdout)
	if err != nil {
		t.Fatalf("parse dump output: %v\nraw:\n%s", err, stdout.String())
	}
	return s
}

// setEnv returns a copy of env with KEY=VALUE, replacing an existing KEY if
// present.
func setEnv(env []string, key, value string) []string {
	prefix := key + "="
	for i, kv := range env {
		if strings.HasPrefix(kv, prefix) {
			out := make([]string, len(env))
			copy(out, env)
			out[i] = prefix + value
			return out
		}
	}
	return append(env, prefix+value)
}

// printContainerLogs fetches and logs container output for test diagnostics.
func printContainerLogs(t *testing.T, ctx context.Context, c testcontainers.Container, name string) {
	t.Helper()
	rc, err := c.Logs(ctx)
	if err != nil {
		t.Logf("[%s] could not retrieve logs: %v", name, err)
		return
	}
	defer rc.Close()
	var buf bytes.Buffer
	_, _ = io.Copy(&buf, rc)
	if buf.Len() > 0 {
		t.Logf("[%s] logs:\n%s", name, buf.String())
	}
}
