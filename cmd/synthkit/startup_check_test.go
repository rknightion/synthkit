// SPDX-License-Identifier: AGPL-3.0-only
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/blueprint"
	"github.com/rknightion/synthkit/internal/control"
	"github.com/rknightion/synthkit/internal/ha"
	"github.com/rknightion/synthkit/internal/runner"
)

// This exercises both shipped CLI probes against the real, selected-blueprint
// process. Sparse input must not need a first delivery to finish booting.
func TestStartupCheckCLI(t *testing.T) {
	binary := buildSynthkit(t, t.TempDir())
	for _, dryRun := range []string{"true", "false"} {
		t.Run("dry_run="+dryRun, func(t *testing.T) {
			root := t.TempDir()
			// A daily request models a quiet service. Keep the master tick outside
			// this bounded proof so no stochastic first request can race the assertion.
			fixture := "name: quiet\nenvironments:\n  - name: prod\n    cloud: {provider: aws, account_id: '111122223333', region: us-east-1, vpc_id: vpc-quiet}\n    cluster: {type: eks, name: quiet-cluster, emit: false, node_groups: [{name: general, instance_type: m6i.xlarge}]}\nworkloads:\n  - type: app\n    name: intake\n    runs_on: quiet-cluster\n    traffic: {off_peak_rps: 0.000011574074074, peak_rps: 0.000011574074074}\n    services:\n      - name: api\n        type: web\n        entry: true\n        spans: [{name_template: intake}]\n"
			if _, err := blueprint.Load([]byte(fixture), runner.Catalog()); err != nil {
				t.Fatalf("invalid test fixture: %v", err)
			}
			if err := os.WriteFile(filepath.Join(root, "quiet.yaml"), []byte(fixture), 0600); err != nil {
				t.Fatal(err)
			}
			// Only process-edge I/O is faked; no live destination is reachable.
			receiver := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusServiceUnavailable) }))
			defer receiver.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			addr := startStartupProcess(t, ctx, binary, root, "DRY_RUN="+dryRun, "BLUEPRINT_NAMES=quiet", "TICK_DEFAULT=1h", "CONTROL_TOKEN=probe-test", "CONTROL_BASE_PATH=/console",
				"GC_PROM_RW="+receiver.URL+"/api/prom/push", "GC_PROM_USER=1", "GC_LOKI="+receiver.URL+"/loki/api/v1/push", "GC_LOKI_USER=2", "GC_OTLP_ENDPOINT="+receiver.URL+"/otlp", "GC_OTLP_USER=3", "GC_TOKEN=probe-fixture")
			probe := func(flag string) ([]byte, error) {
				c := exec.CommandContext(ctx, binary, flag, "-env", filepath.Join(root, "absent.env"))
				// Probes must not load/validate a new configuration, emit, or require a token.
				c.Env = []string{"JSON_HTTP_ADDR=" + addr, "DRY_RUN=invalid", "BLUEPRINT_NAMES=missing"}
				return c.CombinedOutput()
			}
			if out, err := probe("-healthcheck"); err == nil || !strings.Contains(string(out), "readiness returned HTTP 503") {
				t.Fatalf("selected quiet process must remain delivery-unready: %v: %s", err, out)
			}
			t.Log("existing -healthcheck refused delivery readiness with HTTP 503")
			if out, err := probe("-startupcheck"); err != nil {
				t.Fatalf("boot-complete process rejected startupcheck (delivery remains unready): %v: %s", err, out)
			}
			client := &http.Client{Timeout: time.Second}
			for _, prefix := range []string{"", "/console"} {
				resp, err := client.Get("http://" + addr + prefix + "/control/startup")
				if err != nil {
					t.Fatal(err)
				}
				var payload map[string]bool
				err = json.NewDecoder(resp.Body).Decode(&payload)
				resp.Body.Close()
				if err != nil || resp.StatusCode != http.StatusOK || len(payload) != 1 || !payload["started"] {
					t.Fatalf("minimal public startup probe: HTTP %d, %+v, %v", resp.StatusCode, payload, err)
				}
			}
			resp, err := client.Get("http://" + addr + "/control/readiness")
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			var readiness control.ReadinessProbe
			if err := json.NewDecoder(resp.Body).Decode(&readiness); err != nil {
				t.Fatal(err)
			}
			if resp.StatusCode != http.StatusServiceUnavailable || readiness.Ready || readiness.LiveReady || readiness.Blueprints.Loaded != 1 || !readiness.PersistedState.Writable {
				t.Fatalf("startup must not change readiness: HTTP %d, %+v", resp.StatusCode, readiness)
			}
			want := control.ReadinessLiveDeliveryDisabled
			if dryRun == "false" {
				want = control.ReadinessDeliveryNotAttempted
			}
			found := false
			for _, code := range readiness.ReasonCodes {
				found = found || code == want
			}
			if !found {
				t.Fatalf("readiness lost %s: %+v", want, readiness)
			}
		})
	}
	for _, other := range []string{"-healthcheck", "-validate", "-one-shot-deadline=2099-01-01T00:00:00Z"} {
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		c := exec.CommandContext(ctx, binary, "-startupcheck", other)
		c.Env = []string{"JSON_HTTP_ADDR=invalid"}
		out, err := c.CombinedOutput()
		cancel()
		if err == nil || !strings.Contains(string(out), "synthkit:") || strings.Contains(string(out), "synthkit startupcheck:") {
			t.Fatalf("incompatible mode %s was not rejected before probing: %v: %s", other, err, out)
		}
	}
	// No listener is reserved or closed to manufacture an unused address.
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	c := exec.CommandContext(ctx, binary, "-startupcheck")
	c.Env = []string{"JSON_HTTP_ADDR=127.0.0.1:0"}
	if out, err := c.CombinedOutput(); err == nil || !strings.Contains(string(out), "synthkit startupcheck:") {
		t.Fatalf("unstarted process must fail closed through the probe: %v: %s", err, out)
	}
}

// Observe the shipped process's bound-address log directly: no reserved ports,
// scheduling sleeps, or test-only listener hooks establish boot in this proof.
func startStartupProcess(t *testing.T, ctx context.Context, binary, root string, extra ...string) string {
	t.Helper()
	c := exec.CommandContext(ctx, binary, "-env", filepath.Join(root, "absent.env"))
	c.Env = append([]string{"DRY_RUN=true", "JSON_HTTP_ADDR=" + childHTTPAddr,
		"SYNTHKIT_IN_CONTAINER=1", "SYNTHKIT_BIND=127.0.0.1", "BLUEPRINTS=" + root,
		"BLUEPRINT_DATA_DIR=" + filepath.Join(root, "data"), "CONFIG_SNAPSHOT_PATH=" + filepath.Join(root, "state.json")}, extra...)
	pipe, err := c.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	c.Stderr = c.Stdout
	if err := c.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Wait() })
	address, output := make(chan string, 1), make(chan string, 1)
	go func() {
		var log strings.Builder
		scanner := bufio.NewScanner(pipe)
		for scanner.Scan() {
			log.WriteString(scanner.Text() + "\n")
			if match := controlLogLine.FindStringSubmatch(scanner.Text()); match != nil {
				select {
				case address <- match[1]:
				default:
				}
			}
		}
		output <- log.String()
	}()
	select {
	case addr := <-address:
		return addr
	case log := <-output:
		t.Fatalf("process exited before binding control: %s", log)
	case <-ctx.Done():
		t.Fatal("process did not publish bound control address within its deadline")
	}
	return ""
}

// The HA view must not expose a successful startup before its existing
// configuration/build/preflight boundary, and success must not grant leadership.
func TestStartupCheckHABootBoundary(t *testing.T) {
	gate := ha.NewGate()
	v, statePath := readinessHAView(t, gate)
	check := func(want int) {
		t.Helper()
		r := httptest.NewRecorder()
		v.handler.ServeHTTP(r, httptest.NewRequest(http.MethodGet, "/control/startup", nil))
		if r.Code != want {
			t.Fatalf("startup HTTP %d, want %d: %s", r.Code, want, r.Body.String())
		}
	}
	check(http.StatusServiceUnavailable)
	facts := control.HAReadiness{ConfigLoaded: true, RunnerBuilt: true}
	v.readinessFacts = func() control.HAReadiness { return facts }
	check(http.StatusServiceUnavailable)
	facts.PreflightPassed = true
	check(http.StatusOK)
	facts.Transitioning = true
	gate.Revoke()
	check(http.StatusOK) // boot completion is not ongoing role health
	probe := getReadinessProbe(t, v.handler, http.StatusServiceUnavailable)
	if probe.Ready || probe.LiveReady || gate.Role() == ha.RoleLeader {
		t.Fatalf("startup changed HA readiness/authority: %+v, %v", probe, gate.Role())
	}
	if _, err := os.Stat(filepath.Dir(statePath)); !os.IsNotExist(err) {
		t.Fatalf("startup created HA standby state: %v", err)
	}
}

func TestStartupCheckFatalInit(t *testing.T) {
	root := t.TempDir()
	binary := buildSynthkit(t, root)
	if err := os.WriteFile(filepath.Join(root, "bad.yaml"), []byte("name: bad\nunknown_field: true\n"), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	c := exec.CommandContext(ctx, binary, "-env", filepath.Join(root, "absent.env"))
	c.Env = []string{"DRY_RUN=true", "JSON_HTTP_ADDR=" + childHTTPAddr, "SYNTHKIT_IN_CONTAINER=1", "SYNTHKIT_BIND=127.0.0.1", "BLUEPRINTS=" + root, "BLUEPRINT_NAMES=bad", "BLUEPRINT_DATA_DIR=" + filepath.Join(root, "data"), "CONFIG_SNAPSHOT_PATH=" + filepath.Join(root, "state.json")}
	out, err := c.CombinedOutput()
	if err == nil || ctx.Err() != nil || !strings.Contains(string(out), "no blueprints loaded successfully") || controlLogLine.Match(out) {
		t.Fatalf("fatal blueprint initialization must exit before publishing startup: %v: %s", err, out)
	}
}
