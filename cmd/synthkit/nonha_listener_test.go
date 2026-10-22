// SPDX-License-Identifier: AGPL-3.0-only
package main

import (
	"context"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"testing"
	"time"
)

// controlLogLine matches the startup line the non-HA path logs once its control
// listener is bound. The captured group is the address that listener actually holds.
var controlLogLine = regexp.MustCompile(`control \+ Infinity JSON host \(HTTP\) on (\S+) `)

// buildSynthkit builds the shipped binary into dir and returns its path.
func buildSynthkit(t *testing.T, dir string) string {
	t.Helper()
	binary := filepath.Join(dir, "synthkit")
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	if out, err := exec.CommandContext(ctx, "go", "build", "-o", binary, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	return binary
}

// startNonHA starts the binary on a non-HA path with a :0 control address and
// returns the log file it writes. The control listener is bound by the process
// itself; nothing is reserved in the test.
func startNonHA(t *testing.T, ctx context.Context, binary, root string, extra ...string) string {
	t.Helper()
	cmd := exec.CommandContext(ctx, binary, "-env", filepath.Join(root, "absent.env"))
	cmd.Env = append([]string{"PATH=" + os.Getenv("PATH"), "HOME=" + root, "DRY_RUN=true", "JSON_HTTP_ADDR=" + childHTTPAddr,
		// A :0 control address has no fixed port to validate against the host exposure, so the
		// loopback host bind is declared and the container exposure path applies on any host.
		"SYNTHKIT_IN_CONTAINER=1", "SYNTHKIT_BIND=127.0.0.1",
		"BLUEPRINTS=" + root, "BLUEPRINT_NAMES=", "BLUEPRINT_DATA_DIR=" + filepath.Join(root, "blueprints"),
		"CONFIG_SNAPSHOT_PATH=" + filepath.Join(root, "state.json")}, extra...)
	logPath := filepath.Join(root, "process.log")
	logFile, err := os.Create(logPath)
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stdout, cmd.Stderr = logFile, logFile
	if err := cmd.Start(); err != nil {
		logFile.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Wait(); logFile.Close() })
	return logPath
}

// awaitLoggedControlAddr waits for the process to log its bound control address.
func awaitLoggedControlAddr(t *testing.T, logPath string) string {
	t.Helper()
	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		if b, err := os.ReadFile(logPath); err == nil {
			if m := controlLogLine.FindSubmatch(b); m != nil {
				return string(m[1])
			}
		}
		time.Sleep(30 * time.Millisecond)
	}
	b, _ := os.ReadFile(logPath)
	t.Fatalf("process never logged its control address:\n%s", b)
	return ""
}

// TestNonHAControlListenerLogsBoundAddress starts the shipped binary with a :0
// control address and requires the logged address to be the one it bound.
func TestNonHAControlListenerLogsBoundAddress(t *testing.T) {
	root := t.TempDir()
	binary := buildSynthkit(t, root)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	logPath := startNonHA(t, ctx, binary, root)
	addr := awaitLoggedControlAddr(t, logPath)
	_, port, err := net.SplitHostPort(addr)
	if err != nil || port == "0" {
		t.Fatalf("logged control address %q is not a bound address (port %q, err %v)", addr, port, err)
	}
	client := &http.Client{Timeout: 2 * time.Second}
	deadline := time.Now().Add(10 * time.Second)
	for {
		resp, err := client.Get("http://" + addr + "/healthz")
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("GET /healthz status %d", resp.StatusCode)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("logged control address %q is not connectable: %v", addr, err)
		}
		time.Sleep(30 * time.Millisecond)
	}
}
