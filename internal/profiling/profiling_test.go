// SPDX-License-Identifier: AGPL-3.0-only

package profiling

import (
	"bytes"
	"errors"
	"log"
	"reflect"
	"slices"
	"testing"
	"time"

	"github.com/grafana/pyroscope-go"
)

// errorOnlyLogger must drop Debugf/Infof (the per-send "uploading at ..." noise) and forward only
// Errorf, so synthkit's log only carries Pyroscope warnings/errors, never routine sends.
func TestErrorOnlyLogger(t *testing.T) {
	var buf bytes.Buffer
	lg := &errorOnlyLogger{out: log.New(&buf, "", 0)}
	lg.Debugf("uploading at %s", "http://x") // routine send — must be silent
	lg.Infof("starting profiling session:")  // session banner — must be silent
	if buf.Len() != 0 {
		t.Fatalf("Debugf/Infof leaked output: %q", buf.String())
	}

	lg.Errorf("upload profile: %v", "boom")
	got := buf.String()
	if got == "" {
		t.Fatal("Errorf produced no output")
	}
	want := "profiling: upload profile: boom\n"
	if got != want {
		t.Fatalf("Errorf output = %q, want %q", got, want)
	}
}

// The configured Pyroscope logger must be our error-only filter, not the all-level StandardLogger.
func TestStartUsesErrorOnlyLogger(t *testing.T) {
	if _, ok := newPyroscopeLogger().(*errorOnlyLogger); !ok {
		t.Fatalf("expected *errorOnlyLogger, got %T", newPyroscopeLogger())
	}
}

// Start must be a safe no-op (nil profiler, nil error) when disabled or under-configured, so main
// can wire it unconditionally. These cases never touch the network.
func TestStart_NoOpCases(t *testing.T) {
	full := Options{Enabled: true, URL: "http://x", User: "u", Password: "p"}
	cases := []struct {
		name string
		opts Options
	}{
		{"disabled", Options{Enabled: false, URL: "http://x", User: "u", Password: "p"}},
		{"missing URL", func() Options { o := full; o.URL = ""; return o }()},
		{"missing user", func() Options { o := full; o.User = ""; return o }()},
		{"missing password", func() Options { o := full; o.Password = ""; return o }()},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			prof, err := Start(tc.opts)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if prof != nil {
				t.Fatalf("expected nil profiler, got %v", prof)
			}
		})
	}
}

func TestProfileTypes(t *testing.T) {
	has := slices.Contains[[]pyroscope.ProfileType, pyroscope.ProfileType]

	// Defaults + goroutines are always present; mutex/block gated on their rate.
	base := profileTypes(0, 0)
	for _, p := range []pyroscope.ProfileType{
		pyroscope.ProfileCPU, pyroscope.ProfileAllocObjects, pyroscope.ProfileAllocSpace,
		pyroscope.ProfileInuseObjects, pyroscope.ProfileInuseSpace, pyroscope.ProfileGoroutines,
	} {
		if !has(base, p) {
			t.Errorf("base set missing %v", p)
		}
	}
	if has(base, pyroscope.ProfileMutexCount) || has(base, pyroscope.ProfileBlockCount) {
		t.Errorf("mutex/block must be absent when rates are 0: %v", base)
	}

	if got := profileTypes(5, 0); !has(got, pyroscope.ProfileMutexCount) || !has(got, pyroscope.ProfileMutexDuration) {
		t.Errorf("mutex profiles missing when fraction>0: %v", got)
	}
	if has(profileTypes(5, 0), pyroscope.ProfileBlockCount) {
		t.Errorf("block profile present when block rate is 0")
	}
	if got := profileTypes(0, 5); !has(got, pyroscope.ProfileBlockCount) || !has(got, pyroscope.ProfileBlockDuration) {
		t.Errorf("block profiles missing when block rate>0: %v", got)
	}
}

func TestBuildTags(t *testing.T) {
	got := buildTags("", nil)
	if got["service_name"] != appName || got["version"] != "dev" {
		t.Errorf("default tags wrong: %v", got)
	}
	// Caller tags merge on top and can override the built-ins.
	got = buildTags("abc123", map[string]string{"env": "prod", "version": "override"})
	want := map[string]string{"service_name": appName, "version": "override", "env": "prod"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("merged tags = %v, want %v", got, want)
	}
}

func TestParseTags(t *testing.T) {
	cases := []struct {
		in   string
		want map[string]string
	}{
		{"", nil},
		{"   ", nil},
		{"no-equals", nil},
		{"env=prod", map[string]string{"env": "prod"}},
		{" env = prod , team=platform ", map[string]string{"env": "prod", "team": "platform"}},
		{"=novalue,k=v", map[string]string{"k": "v"}}, // empty key skipped
		{"k=", map[string]string{"k": ""}},            // empty value kept
	}
	for _, tc := range cases {
		if got := ParseTags(tc.in); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("ParseTags(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

// TestRedactURL mirrors the selfobs test: credentials stripped, non-credential URLs unchanged,
// malformed / empty inputs return the hard sentinel. Duplicated here because profiling must not
// import selfobs (package isolation).
func TestRedactURL(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"https://user:pass@profiles.grafana.net/pyroscope", "https://profiles.grafana.net/pyroscope"},
		{"https://1234:token@profiles.grafana.net", "https://profiles.grafana.net"},
		{"https://profiles.grafana.net", "https://profiles.grafana.net"}, // no userinfo — unchanged
		{"http://localhost:4040", "http://localhost:4040"},               // local — unchanged
		{"", "<unparseable endpoint>"},                                   // empty
		{"://bad url \x00", "<unparseable endpoint>"},                    // unparseable
	}
	for _, c := range cases {
		got := redactURL(c.in)
		if got != c.want {
			t.Errorf("redactURL(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// slowStopper implements stopper and sleeps for d before returning its configured error.
type slowStopper struct {
	delay time.Duration
	err   error
}

func (s *slowStopper) Stop() error {
	time.Sleep(s.delay)
	return s.err
}

// TestStopWithTimeout_Completes verifies that a stopper which completes quickly returns nil.
func TestStopWithTimeout_Completes(t *testing.T) {
	s := &slowStopper{delay: 0, err: nil}
	if err := StopWithTimeout(s, 100*time.Millisecond); err != nil {
		t.Fatalf("expected nil, got %v", err)
	}
}

// TestStopWithTimeout_CompletesFastWithError verifies that a fast stopper's error propagates.
func TestStopWithTimeout_CompletesFastWithError(t *testing.T) {
	boom := errors.New("stop failed")
	s := &slowStopper{delay: 0, err: boom}
	if err := StopWithTimeout(s, 100*time.Millisecond); err != boom {
		t.Fatalf("expected %v, got %v", boom, err)
	}
}

// TestStopWithTimeout_TimesOut verifies that when Stop() is slower than the timeout, a timeout
// error is returned promptly (well within double the timeout budget).
func TestStopWithTimeout_TimesOut(t *testing.T) {
	s := &slowStopper{delay: 200 * time.Millisecond, err: nil}
	start := time.Now()
	err := StopWithTimeout(s, 20*time.Millisecond)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected a timeout error, got nil")
	}
	if elapsed > 80*time.Millisecond {
		t.Errorf("StopWithTimeout returned after %v — expected ≤80ms", elapsed)
	}
}
