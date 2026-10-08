// SPDX-License-Identifier: AGPL-3.0-only
package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/config"
	"github.com/rknightion/synthkit/internal/control"
	"github.com/rknightion/synthkit/internal/ha"
	lease "github.com/rknightion/synthkit/internal/ha/kubernetes"
	"github.com/rknightion/synthkit/internal/sink/httpretry"
	"github.com/rknightion/synthkit/internal/sink/promrw"
	"github.com/rknightion/synthkit/internal/sink/queue"
)

type observedAdmission struct {
	gate *ha.Gate
	mu   sync.Mutex
	path string
}

func (g *observedAdmission) Role() ha.Role { return g.gate.Role() }
func (g *observedAdmission) record(kind string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	f, err := os.OpenFile(g.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		panic(err)
	}
	defer f.Close()
	_ = json.NewEncoder(f).Encode(map[string]any{"kind": kind, "at": time.Now().UnixNano()})
}
func (g *observedAdmission) Do(ctx context.Context, op ha.Operation, fn func(context.Context) error) error {
	return g.gate.Do(ctx, op, func(ctx context.Context) error { g.record("admission"); return fn(ctx) })
}

func TestHALossSubprocessRealHTTP(t *testing.T) {
	for _, scenario := range []string{"retry", "partial", "drain", "preparation", "sigterm", "unjoined"} {
		t.Run(scenario, func(t *testing.T) {
			var mu sync.Mutex
			var requests []string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				requests = append(requests, r.URL.Path)
				mu.Unlock()
				w.WriteHeader(503)
			}))
			defer srv.Close()
			exe, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			dir := t.TempDir()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, exe, "-test.run=^TestHALossSubprocessHelper$")
			cmd.Env = append(os.Environ(), "SYNTHKIT_HA_LOSS_SCENARIO="+scenario, "SYNTHKIT_HA_LOSS_URL="+srv.URL, "SYNTHKIT_HA_LOSS_DIR="+dir)
			output, err := cmd.CombinedOutput()
			var exit *exec.ExitError
			if !errors.As(err, &exit) || exit.ExitCode() != 1 || ctx.Err() != nil {
				t.Fatalf("hard-loss subprocess did not exit 1: %v %s", err, output)
			}
			data, err := os.ReadFile(filepath.Join(dir, "admissions.jsonl"))
			if err != nil {
				t.Fatal(err)
			}
			fenced := false
			count := 0
			for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
				var event struct {
					Kind string `json:"kind"`
				}
				if err := json.Unmarshal([]byte(line), &event); err != nil {
					t.Fatal(err)
				}
				if event.Kind == "fence" {
					fenced = true
				}
				if event.Kind == "admission" {
					count++
					if fenced {
						t.Fatalf("new operation admitted after OnStoppedLeading fence: %s", data)
					}
				}
			}
			if !fenced {
				t.Fatal("callback was not observed")
			}
			mu.Lock()
			n := len(requests)
			mu.Unlock()
			if scenario == "partial" && n != 0 {
				t.Fatalf("partial batch drained on loss: %d requests", n)
			}
			if scenario != "partial" && n == 0 {
				t.Fatal("test did not exercise real HTTP")
			}
			t.Logf("scenario=%s observed_admissions=%d actual_HTTP=%d; callback fence followed by process exit", scenario, count, n)
		})
	}
}

func TestHALossSubprocessHelper(t *testing.T) {
	scenario := os.Getenv("SYNTHKIT_HA_LOSS_SCENARIO")
	if scenario == "" {
		return
	}
	gate := ha.NewGate()
	observed := &observedAdmission{gate: gate, path: filepath.Join(os.Getenv("SYNTHKIT_HA_LOSS_DIR"), "admissions.jsonl")}
	// Delaying only the injected exit makes the local-admission fence observable;
	// production passes os.Exit directly and never waits here.
	stopped := haCrash(gate, func(code int) { observed.record("fence"); time.Sleep(700 * time.Millisecond); os.Exit(code) })
	if scenario == "preparation" {
		entered := make(chan struct{})
		go func() {
			_ = gate.Activate(context.Background(), func(ctx context.Context) error {
				_ = observed.Do(ctx, ha.Mutation, func(ctx context.Context) error {
					req, _ := http.NewRequestWithContext(ctx, "POST", os.Getenv("SYNTHKIT_HA_LOSS_URL")+"/prepare", nil)
					resp, err := http.DefaultClient.Do(req)
					if resp != nil {
						resp.Body.Close()
					}
					return err
				})
				close(entered)
				time.Sleep(200 * time.Millisecond)
				return observed.Do(ctx, ha.Mutation, func(context.Context) error { return nil })
			})
		}()
		<-entered
		stopped()
		return
	}
	if err := gate.Activate(context.Background(), func(context.Context) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if scenario == "unjoined" {
		bound := ha.Bounded{Gate: observed, Timeout: 50 * time.Millisecond, Margin: 20 * time.Millisecond, Crash: stopped}
		_ = bound.Run(context.Background(), ha.Delivery, func(ctx context.Context) error {
			_ = observed.Do(ctx, ha.Delivery, func(ctx context.Context) error {
				req, _ := http.NewRequestWithContext(ctx, "POST", os.Getenv("SYNTHKIT_HA_LOSS_URL")+"/unjoined", nil)
				resp, err := http.DefaultClient.Do(req)
				if resp != nil {
					resp.Body.Close()
				}
				return err
			})
			select {} // intentionally uncooperative real function must cause process exit
		})
		t.Fatal("unjoined sender treated as completed")
	}
	s := promrw.New(os.Getenv("SYNTHKIT_HA_LOSS_URL")+"/push", "user", "token", false, nil)
	s.SetDelivery(httpretry.Delivery{Gate: observed, HTTPTimeout: time.Second, RetryMaxElapsed: time.Second})
	entered := make(chan struct{})
	var once sync.Once
	batch := []promrw.Series{{Name: "up", Labels: map[string]string{"job": "test"}, Value: 1}}
	opts := queue.Options{Shards: 1, BatchMax: 1, Capacity: 2, Deadline: time.Hour}
	if scenario == "partial" {
		opts.BatchMax = 5000
		opts.Deadline = 100 * time.Millisecond
	}
	q := queue.New[promrw.Series](opts, func(ctx context.Context, b []promrw.Series) error {
		once.Do(func() { close(entered) })
		return s.Write(ctx, b)
	}, func(promrw.Series) uint64 { return 0 }, nil)
	q.Start()
	_ = q.Write(context.Background(), batch)
	if scenario == "partial" {
		stopped()
		return
	}
	<-entered
	// Ensure the first attempt finishes and the real retry loop is sleeping.
	time.Sleep(40 * time.Millisecond)
	if scenario == "drain" || scenario == "sigterm" {
		if scenario == "sigterm" {
			gate.Quiesce()
		}
		go func() { _ = q.DrainJoined(context.Background()) }()
	}
	stopped()
}

// Drive loss through the real coordinator and client-go renewal loop. Direct
// haCrash tests above separately observe admission linearization; these cases
// exercise crash-only loss. SIGTERM can already have admitted a handoff action;
// its later method entry is not evidence of a new admission after fatal selection.
type haLossHTTPRequest struct {
	path string
	at   int64
}

func TestHACoordinatorRenewalLossSuppressesCleanup(t *testing.T) {
	for _, scenario := range []string{"active", "partial", "preparation", "sigterm"} {
		t.Run(scenario, func(t *testing.T) {
			api, backend := newNamedLeaseRecorder(t)
			defer backend.Close()
			var deny atomic.Bool
			var apiMu sync.Mutex
			var releaseAttempts int
			lossAPI := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, err := io.ReadAll(r.Body)
				if err != nil {
					t.Error(err)
					return
				}
				if r.Method == http.MethodPut {
					var d leaseDocument
					if err := json.Unmarshal(body, &d); err != nil {
						t.Error(err)
						return
					}
					if d.Spec.HolderIdentity == "" {
						apiMu.Lock()
						releaseAttempts++
						apiMu.Unlock()
					}
				}
				if deny.Load() {
					w.Header().Set("Content-Type", "application/json")
					w.WriteHeader(http.StatusServiceUnavailable)
					_, _ = io.WriteString(w, `{"kind":"Status","apiVersion":"v1","status":"Failure","reason":"ServiceUnavailable","code":503}`)
					return
				}
				req, err := http.NewRequestWithContext(r.Context(), r.Method, backend.URL+r.URL.RequestURI(), bytes.NewReader(body))
				if err != nil {
					t.Error(err)
					return
				}
				req.Header = r.Header.Clone()
				resp, err := http.DefaultClient.Do(req)
				if err != nil {
					w.WriteHeader(http.StatusServiceUnavailable)
					return
				}
				defer resp.Body.Close()
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(resp.StatusCode)
				_, _ = io.Copy(w, resp.Body)
			}))
			defer lossAPI.Close()

			var remoteMu sync.Mutex
			var remotePaths []haLossHTTPRequest
			remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.Copy(io.Discard, r.Body)
				remoteMu.Lock()
				remotePaths = append(remotePaths, haLossHTTPRequest{path: r.URL.Path, at: time.Now().UnixNano()})
				remoteMu.Unlock()
				if r.URL.Path == "/push" {
					// A confirmed pre-fence attempt stays in flight while renewal
					// fails; no second attempt should be admitted after that loss.
					select {
					case <-r.Context().Done():
					case <-time.After(3 * time.Second):
					}
					return
				}
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, `{"content":"local-test-configuration"}`)
			}))
			defer remote.Close()
			root := t.TempDir()
			baked, data := filepath.Join(root, "baked"), filepath.Join(root, "data")
			if err := os.Mkdir(baked, 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(data, 0755); err != nil {
				t.Fatal(err)
			}
			// Deliberately fast traffic belongs only in this local fixture, never
			// a shipped blueprint. It exposes buffered delivery within a short bound.
			fixture := "name: test\nhigh_dpm: {metric_interval: 100ms}\nenvironments: [{name: test, weight: 1}]\nhosts: [{name: node, os: linux}]\nfeatures:\n  fleet_management:\n    enabled: true\n    collectors_per_os: {linux: 1}\n"
			if err := os.WriteFile(filepath.Join(baked, "test.yaml"), []byte(fixture), 0644); err != nil {
				t.Fatal(err)
			}
			statePath := filepath.Join(root, "state.json")
			writeHAJSON(t, statePath, control.DefaultState())
			cfg, err := config.Load("")
			if err != nil {
				t.Fatal(err)
			}
			cfg.HAMode, cfg.StateBackend, cfg.HANamespace, cfg.HALeaseName, cfg.PodUID = "lease", "file", "test", "lease", "pod"
			cfg.DryRun, cfg.SelfObsEnabled = false, false
			cfg.HALeaseDuration, cfg.HARenewDeadline, cfg.HARetryPeriod = 5*time.Second, 300*time.Millisecond, 50*time.Millisecond
			cfg.HAKubeRequestTimeout, cfg.HAReleaseTimeout = 100*time.Millisecond, time.Second
			cfg.HAHTTPTimeout, cfg.HARetryMaxElapsed, cfg.HAFlushTimeout, cfg.HAFenceMargin = time.Second, time.Second, 2*time.Second, 100*time.Millisecond
			cfg.SendDrainDeadline, cfg.MasterTick, cfg.MaxDPMPerSeries = 4*time.Second, 10*time.Millisecond, 600
			cfg.SendShards, cfg.SendBatchMax, cfg.SendCapacity, cfg.SendDeadline = 1, 1, 100000, time.Hour
			if scenario == "partial" {
				cfg.SendBatchMax = 100000
			}
			cfg.BlueprintsDir, cfg.BlueprintDataDir, cfg.BlueprintNames, cfg.SnapshotPath = baked, data, []string{"test"}, statePath
			cfg.Token, cfg.PromUser, cfg.LokiUser, cfg.OTLPUser = "test", "test", "test", "test"
			cfg.PromRWURL, cfg.LokiURL, cfg.OTLPEndpoint = remote.URL+"/push", remote.URL+"/loki", remote.URL+"/otlp"
			cfg.FMURL, cfg.FMToken, cfg.FMStackID = remote.URL+"/fleet", "test", "test"
			cfg.FaroCollector, cfg.ProfilesURL, cfg.SigilEndpoint, cfg.ControlToken = "", "", "", ""
			cfg.GitPollInterval = 0
			listen, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			cfg.HTTPAddr = listen.Addr().String()
			_ = listen.Close()
			cfgPath := filepath.Join(root, "config.json")
			writeHAJSON(t, cfgPath, cfg)
			exe, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, exe, "-test.run=^TestHACoordinatorLossHelper$")
			cmd.Env = append(os.Environ(), "SYNTHKIT_HA_COORD_CONFIG="+cfgPath, "SYNTHKIT_HA_COORD_API="+lossAPI.URL, "SYNTHKIT_HA_COORD_SCENARIO="+scenario, "GORACE="+os.Getenv("GORACE")+" atexit_sleep_ms=0")
			var output bytes.Buffer
			cmd.Stdout, cmd.Stderr = &output, &output
			if err := cmd.Start(); err != nil {
				t.Fatal(err)
			}
			wait := make(chan error, 1)
			go func() { wait <- cmd.Wait() }()
			endpoint, events := "http://"+cfg.HTTPAddr, filepath.Join(root, "coordinator-events.jsonl")
			awaitHA(t, func() bool { code, _ := haGet(t, endpoint+"/control/state"); return code == 200 })
			api.allowAcquisition()
			if scenario == "preparation" {
				awaitHA(t, func() bool { return strings.Contains(readHALossEvents(events), "\"kind\":\"preparation\"") })
			} else {
				awaitHA(t, func() bool {
					remoteMu.Lock()
					paths := append([]haLossHTTPRequest(nil), remotePaths...)
					remoteMu.Unlock()
					registered, heartbeat, push := false, false, false
					for _, p := range paths {
						registered = registered || strings.HasSuffix(p.path, "RegisterCollector")
						heartbeat = heartbeat || strings.HasSuffix(p.path, "GetConfig")
						push = push || p.path == "/push"
					}
					if !registered || !heartbeat {
						return false
					}
					if scenario == "partial" {
						return haBufferedDepth(t, endpoint) > 0
					}
					return push
				})
			}
			before := captureHALossState(t, statePath, data)
			deny.Store(true)
			// Use a real signal, while an admitted body remains active, to overlap
			// planned-shutdown entry with renewal failure rather than calling Quiesce.
			if scenario == "sigterm" {
				if err := cmd.Process.Signal(syscall.SIGTERM); err != nil {
					t.Fatal(err)
				}
			}
			awaitHA(t, func() bool { return strings.Contains(readHALossEvents(events), "\"kind\":\"exit-1\"") })
			if scenario == "partial" && haBufferedDepth(t, endpoint) == 0 {
				t.Fatal("loss drained the buffered partial batch")
			}
			select {
			case err := <-wait:
				var exit *exec.ExitError
				if !errors.As(err, &exit) || exit.ExitCode() != 1 {
					t.Fatalf("coordinator loss did not hard exit1: %v\n%s", err, output.String())
				}
			case <-ctx.Done():
				t.Fatal("coordinator loss failed to exit within bound")
			}
			log := readHALossEvents(events)
			if err := haLossCleanupError(log, scenario == "sigterm"); err != nil {
				t.Fatalf("actual coordinator loss violated terminal admission: %v\n%s\n%s", err, log, output.String())
			}
			apiMu.Lock()
			released := releaseAttempts
			apiMu.Unlock()
			if released != 0 {
				t.Fatalf("loss attempted %d explicit Lease releases", released)
			}
			remoteMu.Lock()
			paths := append([]haLossHTTPRequest(nil), remotePaths...)
			remoteMu.Unlock()
			fence := haLossStopTime(t, log)
			pushes, priorUnregisters := 0, 0
			for _, p := range paths {
				if strings.HasSuffix(p.path, "UnregisterCollector") {
					// SIGTERM legitimately starts planned cleanup while still
					// leader. It must stop, not restart, at the loss callback.
					if scenario != "sigterm" || p.at >= fence {
						t.Fatal("loss unregistered real Fleet collector", p, fence)
					}
					priorUnregisters++
				}
				if p.path == "/push" {
					if p.at >= fence {
						t.Fatal("post-callback real delivery", p, fence)
					}
					pushes++
				}
			}
			if scenario == "active" || scenario == "sigterm" {
				if pushes != 1 {
					t.Fatalf("post-loss delivery/retry: pushes=%d paths=%v", pushes, paths)
				}
			} else if pushes != 0 {
				t.Fatalf("preparation/partial batch caused delivery: %v", paths)
			}
			if scenario == "preparation" && len(paths) != 0 {
				t.Fatalf("preparation performed synthetic/Fleet side effects: %v", paths)
			}
			after := captureHALossState(t, statePath, data)
			if !equalHALossState(before, after) {
				t.Fatalf("loss performed state cleanup: before=%v after=%v", before, after)
			}
			t.Logf("scenario=%s actual client-go OnStoppedLeading, exit1, delivery=%d, pre-loss planned unregister=%d, no new terminal admission/release/state cleanup", scenario, pushes, priorUnregisters)
		})
	}
}

// BeforeHandoff is an eligibility marker, NOT an admission oracle. In the real
// coordinator, terminal.admit checks under the mutex which crash holds through
// the non-returning exit. Thus an entry during the injected exit delay can only
// be an earlier admission. TestHATerminalAdmissionOrder separately observes that
// actual admission check and forces the unlock-to-action-entry scheduling gap.
func haLossCleanupError(log string, sigterm bool) error {
	var stopped, fatal, signaled, priorSealHandoff, sealed bool
	for _, line := range strings.Split(strings.TrimSpace(log), "\n") {
		var event struct {
			Kind string `json:"kind"`
		}
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			return err
		}
		switch event.Kind {
		case "sigterm":
			signaled = true
		case "on-stopped":
			stopped = true
		case "exit-1":
			fatal = true
		case "seal-handoff":
			if !fatal && signaled {
				priorSealHandoff = true
			}
		case "seal":
			if !sigterm || !priorSealHandoff || sealed {
				return errors.New("seal without an earlier SIGTERM handoff")
			}
			sealed = true
		case "release", "exit-0":
			return fmt.Errorf("loss entered %s", event.Kind)
		}
	}
	if !stopped || !fatal {
		return errors.New("missing loss callback or fatal selection")
	}
	return nil
}

func haLossStopTime(t *testing.T, log string) int64 {
	t.Helper()
	for _, line := range strings.Split(strings.TrimSpace(log), "\n") {
		var event struct {
			Kind string `json:"kind"`
			At   int64  `json:"at"`
		}
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			t.Fatal(err)
		}
		if event.Kind == "on-stopped" {
			return event.At
		}
	}
	t.Fatal("no actual client-go loss callback timestamp")
	return 0
}

func readHALossEvents(path string) string { b, _ := os.ReadFile(path); return string(b) }
func haBufferedDepth(t *testing.T, endpoint string) int {
	t.Helper()
	code, b := haGet(t, endpoint+"/control/status")
	if code != 200 {
		t.Fatalf("loss unexpectedly stopped HTTP before hard exit: %d", code)
	}
	var s control.StatusReport
	if err := json.Unmarshal(b, &s); err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, q := range s.Queues {
		n += q.Depth
	}
	return n
}
func captureHALossState(t *testing.T, statePath, data string) map[string]string {
	t.Helper()
	result := map[string]string{}
	capture := func(path string) {
		fi, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		value := fmt.Sprintf("%v/%d", fi.Mode(), fi.ModTime().UnixNano())
		if !fi.IsDir() {
			b, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			value += fmt.Sprintf("/%x", sha256.Sum256(b))
		}
		result[path] = value
	}
	capture(statePath)
	if err := filepath.WalkDir(data, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		capture(path)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return result
}
func equalHALossState(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

type lossObservedElection struct {
	leaseElection
	events *observedAdmission
}

func (e lossObservedElection) Seal(ctx context.Context) error {
	e.events.record("seal")
	return e.leaseElection.Seal(ctx)
}
func (e lossObservedElection) Release(ctx context.Context) error {
	e.events.record("release")
	return e.leaseElection.Release(ctx)
}
func TestHACoordinatorLossHelper(t *testing.T) {
	path := os.Getenv("SYNTHKIT_HA_COORD_CONFIG")
	if path == "" {
		return
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var cfg config.Config
	if err := json.Unmarshal(b, &cfg); err != nil {
		t.Fatal(err)
	}
	events := &observedAdmission{path: filepath.Join(filepath.Dir(path), "coordinator-events.jsonl")}
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGTERM, os.Interrupt)
	term := make(chan struct{})
	go func() { <-signals; events.record("sigterm"); close(term) }()
	deps := productionHADependencies()
	calls := 0
	deps.Preflight = func(context.Context, *config.Config) error {
		calls++
		if calls == 2 && os.Getenv("SYNTHKIT_HA_COORD_SCENARIO") == "preparation" {
			events.record("preparation")
			select {}
		}
		return nil
	}
	deps.BeforeHandoff = func(step string) { events.record(step + "-handoff") }
	deps.Exit = func(code int) {
		events.record("exit-" + strconv.Itoa(code))
		// Test-only delay exposes earlier-admitted actions still in flight.
		// Production's immediate os.Exit is the final fence, not cancellation.
		time.Sleep(300 * time.Millisecond)
		os.Exit(code)
	}
	deps.Election = func(ctx context.Context, o lease.Options) (leaseElection, error) {
		stopped := o.Stopped
		o.Stopped = func() { events.record("on-stopped"); stopped() }
		e, err := lease.NewForClient(ctx, o, os.Getenv("SYNTHKIT_HA_COORD_API"), &http.Client{Timeout: o.RequestTimeout})
		return lossObservedElection{leaseElection: e, events: events}, err
	}
	if err := runHALifecycle(&cfg, false, term, deps); err != nil {
		t.Fatal(err)
	}
}
