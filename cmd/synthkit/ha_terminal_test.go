// SPDX-License-Identifier: AGPL-3.0-only
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/config"
	"github.com/rknightion/synthkit/internal/ha"
	lease "github.com/rknightion/synthkit/internal/ha/kubernetes"
)

// These are coordinator interleavings, not substitutes for the real client-go
// renewal-loss/HTTP tests. Only the election callback scheduling is controlled;
// standby construction, activation, producers, joins and terminal decisions run
// through runHALifecycle. The fatal exit is held briefly to expose competition.
func TestHATerminalExitWindows(t *testing.T) {
	for _, scenario := range []string{"former-leader", "standby-role", "standby-exit", "seal", "release", "release-return", "planned-exit"} {
		t.Run(scenario, func(t *testing.T) {
			root := t.TempDir()
			exe, err := os.Executable()
			if err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, exe, "-test.run=^TestHATerminalExitWindowHelper$")
			cmd.Env = append(os.Environ(), "SYNTHKIT_HA_TERMINAL_SCENARIO="+scenario, "SYNTHKIT_HA_TERMINAL_DIR="+root, "GORACE="+os.Getenv("GORACE")+" atexit_sleep_ms=0")
			output, err := cmd.CombinedOutput()
			log := readHALossEvents(filepath.Join(root, "events.jsonl"))
			var exit *exec.ExitError
			if !errors.As(err, &exit) || exit.ExitCode() != 1 || ctx.Err() != nil {
				t.Errorf("fatal selection lost terminal arbitration: %v\n%s\n%s", err, log, output)
			}
			lines := strings.Split(strings.TrimSpace(log), "\n")
			fatal := false
			for _, line := range lines {
				if strings.Contains(line, `"kind":"exit-1"`) {
					fatal = true
				}
				if fatal && (strings.Contains(line, `"kind":"seal"`) || strings.Contains(line, `"kind":"release"`) || strings.Contains(line, `"kind":"exit-0"`)) {
					t.Errorf("normal terminal action after fatal selection: %s", log)
					break
				}
			}
			if !fatal || !strings.Contains(log, `"kind":"window"`) {
				t.Errorf("test did not reach its terminal window: %s\n%s", log, output)
			}
			t.Logf("scenario=%s bounded coordinator evidence:\n%s", scenario, log)
		})
	}
}

// admissionProbeContext observes the real ctx.Err check inside terminal.admit's
// mutex. Returning nil records successful local admission before that mutex is
// unlocked, without adding a production hook or moving the admission point.
type admissionProbeContext struct {
	context.Context
	checked func()
}

func (c admissionProbeContext) Err() error {
	err := c.Context.Err()
	if err == nil {
		c.checked()
	}
	return err
}

func TestHATerminalAdmissionOrder(t *testing.T) {
	t.Run("earlier-admitted-action", func(t *testing.T) {
		root := t.TempDir()
		exe, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, exe, "-test.run=^TestHATerminalAdmissionOrderHelper$")
		cmd.Env = append(os.Environ(), "SYNTHKIT_HA_ADMISSION_DIR="+root, "GORACE="+os.Getenv("GORACE")+" atexit_sleep_ms=0")
		output, err := cmd.CombinedOutput()
		log := readHALossEvents(filepath.Join(root, "events.jsonl"))
		var exit *exec.ExitError
		if !errors.As(err, &exit) || exit.ExitCode() != 1 || ctx.Err() != nil {
			t.Fatalf("admission window did not hard exit1: %v\n%s\n%s", err, log, output)
		}
		if err := haAdmissionOrderError(log); err != nil {
			t.Fatalf("terminal admission contract: %v\n%s\n%s", err, log, output)
		}
		t.Logf("real admit unlock, fatal selection, earlier action entry, process exit1:\n%s", log)
	})
	t.Run("reject-after-fatal", func(t *testing.T) {
		// Crash's exit is non-returning. Seed its terminal state to exercise the
		// rejection branch independently of the mutex held until process exit.
		terminal := &haTerminal{fatal: true}
		checked, entered := false, false
		ctx := admissionProbeContext{Context: context.Background(), checked: func() { checked = true }}
		action := func(context.Context) error { entered = true; return nil }
		err := terminal.admit(ctx, action)
		if !errors.Is(err, ha.ErrNotLeader) || checked || entered {
			t.Fatalf("post-fatal action admitted: err=%v context_checked=%v entered=%v", err, checked, entered)
		}
	})
}

func haAdmissionOrderError(log string) error {
	var admitted, fatal, entered, locked, attempted bool
	for _, line := range strings.Split(strings.TrimSpace(log), "\n") {
		var event struct {
			Kind string `json:"kind"`
		}
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			return err
		}
		switch event.Kind {
		case "seal-admitted":
			if fatal || admitted {
				return errors.New("new action admitted after terminal selection or duplicate admission")
			}
			admitted = true
		case "exit-1":
			fatal = true
		case "terminal-lock-held":
			locked = fatal
		case "seal":
			if !admitted || !fatal || entered {
				return errors.New("action entry without prior admission in forced post-selection window")
			}
			entered = true
		case "post-fatal-attempt":
			attempted = fatal
		default:
			return fmt.Errorf("unexpected terminal action %q", event.Kind)
		}
	}
	if !admitted || !fatal || !entered || !locked || !attempted {
		return errors.New("incomplete admission/unlock/exit window")
	}
	return nil
}

func TestHATerminalAdmissionOrderHelper(t *testing.T) {
	root := os.Getenv("SYNTHKIT_HA_ADMISSION_DIR")
	if root == "" {
		return
	}
	events := &observedAdmission{path: filepath.Join(root, "events.jsonl")}
	fatalCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	admitted, resume, finished := make(chan struct{}), make(chan struct{}), make(chan struct{})
	selected, attempted := make(chan struct{}), make(chan struct{})
	terminal := &haTerminal{gate: ha.NewGate(), cancel: cancel}
	terminal.exit = func(code int) {
		events.record("exit-" + strconv.Itoa(code))
		if terminal.mu.TryLock() {
			terminal.mu.Unlock()
			panic("fatal exit did not retain admission mutex")
		}
		events.record("terminal-lock-held")
		close(selected)
		<-finished
		<-attempted
		os.Exit(code)
	}
	probe := admissionProbeContext{Context: fatalCtx, checked: func() {
		events.record("seal-admitted")
		close(admitted)
	}}
	action := func(context.Context) error { events.record("seal"); return nil }
	go func() {
		_ = terminal.admit(probe, func(ctx context.Context) error {
			// First instruction is the test-only scheduling hook. The actual
			// action cannot enter until crash acquires the now-unlocked mutex.
			<-resume
			err := action(ctx)
			close(finished)
			return err
		})
	}()
	<-admitted
	go terminal.crash()
	<-selected
	close(resume)
	events.record("post-fatal-attempt")
	close(attempted)
	// Crash holds admission closed through its non-returning exit. A fresh,
	// uncanceled context must not let a second action enter that interval.
	_ = terminal.admit(context.Background(), func(context.Context) error {
		events.record("late-action")
		return nil
	})
	t.Fatal("post-fatal admission returned before process exit")
}

type terminalWindowElection struct {
	opts     lease.Options
	events   *observedAdmission
	term     chan struct{}
	scenario string
	fatal    func()
}

func (e *terminalWindowElection) Run(context.Context) {
	if !strings.HasPrefix(e.scenario, "standby-") {
		e.opts.Started(context.Background())
		e.events.record("leader-started")
	}
	close(e.term)
}
func (e *terminalWindowElection) Seal(context.Context) error {
	// A seal can win the canceled-context/channel select in the retained lock.
	// Deliberately don't use cancellation as a proxy for terminal admission.
	e.events.record("seal")
	return nil
}
func (e *terminalWindowElection) Release(ctx context.Context) error {
	e.events.record("release")
	if e.scenario == "release-return" {
		e.fatal()
	}
	return ctx.Err()
}

func TestHATerminalExitWindowHelper(t *testing.T) {
	scenario := os.Getenv("SYNTHKIT_HA_TERMINAL_SCENARIO")
	if scenario == "" {
		return
	}
	root := os.Getenv("SYNTHKIT_HA_TERMINAL_DIR")
	baked := filepath.Join(root, "baked")
	if err := os.Mkdir(baked, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(baked, "test.yaml"), []byte("name: test\nhosts: [{name: node, os: linux}]\n"), 0644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load("")
	if err != nil {
		t.Fatal(err)
	}
	cfg.HAMode, cfg.StateBackend = "lease", "file"
	cfg.DryRun, cfg.SelfObsEnabled = true, false
	cfg.BlueprintsDir, cfg.BlueprintDataDir = baked, filepath.Join(root, "data")
	cfg.BlueprintNames, cfg.SnapshotPath = []string{"test"}, filepath.Join(root, "state.json")
	cfg.MasterTick, cfg.SendDrainDeadline = time.Hour, 2*time.Second
	cfg.FMURL, cfg.SigilEndpoint, cfg.FaroCollector, cfg.ProfilesURL, cfg.ControlToken = "", "", "", "", ""
	cfg.GitPollInterval = 0
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	cfg.HTTPAddr = listener.Addr().String()
	_ = listener.Close()
	events := &observedAdmission{path: filepath.Join(root, "events.jsonl")}
	term, selected := make(chan struct{}), make(chan struct{})
	var stopped func()
	var selectedOnce, triggerOnce sync.Once
	trigger := func() {
		triggerOnce.Do(func() {
			events.record("window")
			go stopped()
			select {
			case <-selected:
			case <-time.After(time.Second):
				panic("fatal callback blocked before exit selection")
			}
		})
	}
	target := scenario
	if scenario == "former-leader" || scenario == "standby-role" {
		target = "role-check"
	}
	deps := productionHADependencies()
	deps.Preflight = func(context.Context, *config.Config) error { return nil }
	deps.BeforeHandoff = func(step string) {
		if step == target {
			trigger()
		}
	}
	deps.Exit = func(code int) {
		events.record("exit-" + strconv.Itoa(code))
		if code == 1 {
			selectedOnce.Do(func() { close(selected) })
			time.Sleep(200 * time.Millisecond)
		}
		os.Exit(code)
	}
	deps.Election = func(_ context.Context, opts lease.Options) (leaseElection, error) {
		stopped = opts.Stopped
		return &terminalWindowElection{opts: opts, events: events, term: term, scenario: scenario, fatal: trigger}, nil
	}
	if err := runHALifecycle(cfg, false, term, deps); err != nil {
		t.Fatal(err)
	}
}
