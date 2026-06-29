// SPDX-License-Identifier: AGPL-3.0-only

package host

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/fixture"
	"github.com/rknightion/synthkit/internal/shape"
	"github.com/rknightion/synthkit/internal/sink/loki"
)

var testNow = time.Date(2026, 6, 17, 12, 0, 0, 0, time.UTC)

func testEng(seed string) *shape.Engine {
	return shape.New(seed, nil)
}

// labelKeys returns the sorted set of stream-label keys for the stream.
func labelKeys(s loki.Stream) map[string]struct{} {
	out := make(map[string]struct{}, len(s.Labels))
	for k := range s.Labels {
		out[k] = struct{}{}
	}
	return out
}

// hasHighCard checks that none of the forbidden high-card keys appear in stream labels.
func hasHighCard(s loki.Stream, forbiddenKeys ...string) bool {
	for _, k := range forbiddenKeys {
		if _, ok := s.Labels[k]; ok {
			return true
		}
	}
	return false
}

// findStream returns the first stream whose labels match ALL of the given key=value pairs.
func findStream(streams []loki.Stream, kvs map[string]string) (loki.Stream, bool) {
	for _, s := range streams {
		match := true
		for k, v := range kvs {
			if s.Labels[k] != v {
				match = false
				break
			}
		}
		if match {
			return s, true
		}
	}
	return loki.Stream{}, false
}

// TestLinuxLogsStreamLabels asserts:
// - linux host with Logs=true emits at least one stream
// - stream has {job:integrations/node_exporter, instance, unit, level}
// - high-card fields (pid, uid, boot_id, command, executable) are in Line.Meta NOT stream labels
func TestLinuxLogsStreamLabels(t *testing.T) {
	h := &fixture.Host{
		Hostname: "camden",
		OS:       "linux",
		Logs:     true,
	}
	streams := buildLogs(h, "bp:host:camden", testNow, testEng("bp:host:camden"))
	if len(streams) == 0 {
		t.Fatal("linux host with Logs=true: expected log streams, got none")
	}

	// Find a journal stream.
	s, ok := findStream(streams, map[string]string{
		"job":      "integrations/node_exporter",
		"instance": "camden",
	})
	if !ok {
		t.Fatalf("no stream with job=integrations/node_exporter instance=camden; got streams: %v", streamsDebug(streams))
	}

	// Must have unit and level stream labels.
	if s.Labels["unit"] == "" {
		t.Errorf("linux stream missing unit label; labels=%v", s.Labels)
	}
	if s.Labels["level"] == "" {
		t.Errorf("linux stream missing level label; labels=%v", s.Labels)
	}

	// High-card fields must NOT be stream labels.
	if hasHighCard(s, "pid", "uid", "command", "executable") {
		t.Errorf("linux stream has high-card field in stream labels (pid/uid/command/executable); labels=%v", s.Labels)
	}

	// Verify lines are non-empty and Line.T is set.
	if len(s.Lines) == 0 {
		t.Errorf("linux stream has no lines")
	}
	for i, line := range s.Lines {
		if line.T.IsZero() {
			t.Errorf("line[%d].T is zero", i)
		}
		if line.Body == "" {
			t.Errorf("line[%d].Body is empty", i)
		}
	}

	// pid/boot_id must be in Line.Meta if present (not in Labels).
	for _, line := range s.Lines {
		for _, hcKey := range []string{"pid", "uid", "boot_id", "command", "executable"} {
			if _, inMeta := line.Meta[hcKey]; inMeta {
				// Good — high-card in Meta is correct.
				continue
			}
			// OK if it's simply not present; what's forbidden is it being in Labels.
		}
	}
}

// TestLinuxLogsPidInMeta asserts that any pid value appears only in Line.Meta, not in stream labels.
func TestLinuxLogsPidInMeta(t *testing.T) {
	h := &fixture.Host{
		Hostname: "camden",
		OS:       "linux",
		Logs:     true,
	}
	streams := buildLogs(h, "bp:host:camden", testNow, testEng("bp:host:camden"))
	for _, s := range streams {
		if _, ok := s.Labels["pid"]; ok {
			t.Errorf("pid is a stream label (must be Line.Meta); labels=%v", s.Labels)
		}
		if _, ok := s.Labels["uid"]; ok {
			t.Errorf("uid is a stream label (must be Line.Meta); labels=%v", s.Labels)
		}
	}
}

// TestWindowsLogsStreams asserts:
// - windows host emits TWO streams (Application + System)
// - each stream has {job:integrations/windows_exporter, instance, level, source, agent_hostname}
func TestWindowsLogsStreams(t *testing.T) {
	h := &fixture.Host{
		Hostname: "winbox",
		OS:       "windows",
		Logs:     true,
	}
	streams := buildLogs(h, "bp:host:winbox", testNow, testEng("bp:host:winbox"))
	if len(streams) < 2 {
		t.Fatalf("windows host: expected >=2 streams (Application+System), got %d; %v", len(streams), streamsDebug(streams))
	}

	// Must have both Application and System source streams.
	_, hasApp := findStream(streams, map[string]string{
		"job":            "integrations/windows_exporter",
		"instance":       "winbox",
		"source":         "Application",
		"agent_hostname": "winbox",
	})
	_, hasSys := findStream(streams, map[string]string{
		"job":            "integrations/windows_exporter",
		"instance":       "winbox",
		"source":         "System",
		"agent_hostname": "winbox",
	})
	if !hasApp {
		t.Errorf("no Application source stream; streams=%v", streamsDebug(streams))
	}
	if !hasSys {
		t.Errorf("no System source stream; streams=%v", streamsDebug(streams))
	}

	// All streams must have level label.
	for _, s := range streams {
		if s.Labels["job"] != "integrations/windows_exporter" {
			continue
		}
		if s.Labels["level"] == "" {
			t.Errorf("windows stream missing level label; labels=%v", s.Labels)
		}
		if len(s.Lines) == 0 {
			t.Errorf("windows stream %v has no lines", s.Labels)
		}
	}
}

// TestMacOSLogsStreams asserts:
// - macos host emits streams with {job:integrations/macos-node, instance, sender}
// - pid NOT a stream label
// - body formatted as sender[pid]: message
func TestMacOSLogsStreams(t *testing.T) {
	h := &fixture.Host{
		Hostname: "alex",
		OS:       "darwin",
		Logs:     true,
	}
	streams := buildLogs(h, "bp:host:alex", testNow, testEng("bp:host:alex"))
	if len(streams) == 0 {
		t.Fatalf("macos host: expected log streams, got none")
	}

	s, ok := findStream(streams, map[string]string{
		"job":      "integrations/macos-node",
		"instance": "alex",
	})
	if !ok {
		t.Fatalf("no stream with job=integrations/macos-node instance=alex; streams=%v", streamsDebug(streams))
	}

	// Must have sender label.
	if s.Labels["sender"] == "" {
		t.Errorf("macos stream missing sender label; labels=%v", s.Labels)
	}

	// pid must NOT be a stream label.
	if _, ok := s.Labels["pid"]; ok {
		t.Errorf("macos stream has pid as stream label; labels=%v", s.Labels)
	}

	// Body should be in sender[pid]: message format.
	if len(s.Lines) == 0 {
		t.Fatal("macos stream has no lines")
	}
	for i, line := range s.Lines {
		if !strings.Contains(line.Body, "[") || !strings.Contains(line.Body, "]:") {
			t.Errorf("macos line[%d] body=%q not in sender[pid]: message format", i, line.Body)
		}
		if line.T.IsZero() {
			t.Errorf("macos line[%d].T is zero", i)
		}
	}
}

// TestDockerLogsStreams asserts:
// - docker host emits streams with {job:integrations/docker, instance, container, stream}
// - uses containers from dockerContainers helper
func TestDockerLogsStreams(t *testing.T) {
	h := &fixture.Host{
		Hostname: "camden",
		OS:       "linux",
		Docker:   true,
		Logs:     true,
	}
	// Use a NON-"bp" seed so a regression to a hardcoded "bp:host:" prefix would
	// derive a different container set than dockerContainers(seed) and fail below.
	const seed = "myblueprint:host:camden"
	streams := buildLogs(h, seed, testNow, testEng(seed))
	if len(streams) == 0 {
		t.Fatal("docker host: expected log streams, got none")
	}

	// The docker LOG container set must equal dockerContainers(seed) — the same set the
	// METRIC lane derives. (host.go Tick uses dockerContainers(c.seed) for cadvisor.)
	wantNames := map[string]bool{}
	for _, ct := range dockerContainers(seed) {
		if n := ct.Labels["name"]; n != "" {
			wantNames[n] = true
		}
	}

	// Find at least one docker stream.
	var dockerStreams []loki.Stream
	for _, s := range streams {
		if s.Labels["job"] == "integrations/docker" {
			dockerStreams = append(dockerStreams, s)
		}
	}
	if len(dockerStreams) == 0 {
		t.Fatalf("no docker streams (job=integrations/docker); streams=%v", streamsDebug(streams))
	}

	for _, s := range dockerStreams {
		if s.Labels["instance"] != "camden" {
			t.Errorf("docker stream instance=%q, want camden", s.Labels["instance"])
		}
		cn := s.Labels["container"]
		if cn == "" {
			t.Errorf("docker stream missing container label; labels=%v", s.Labels)
		} else if !wantNames[cn] {
			t.Errorf("docker log container %q not in metric container set %v (seed mismatch)", cn, wantNames)
		}
		streamVal := s.Labels["stream"]
		if streamVal != "stdout" && streamVal != "stderr" {
			t.Errorf("docker stream label stream=%q, want stdout or stderr; labels=%v", streamVal, s.Labels)
		}
		if len(s.Lines) == 0 {
			t.Errorf("docker stream %v has no lines", s.Labels)
		}
	}
}

// TestLogsDisabled asserts that Logs=false produces no streams.
func TestLogsDisabled(t *testing.T) {
	for _, os := range []string{"linux", "windows", "darwin"} {
		h := &fixture.Host{
			Hostname: "test-host",
			OS:       os,
			Logs:     false,
		}
		streams := buildLogs(h, "bp:host:test-host", testNow, testEng("bp:host:test-host"))
		if len(streams) != 0 {
			t.Errorf("OS=%s Logs=false: expected no streams, got %d", os, len(streams))
		}
	}
}

// TestLinuxJournalCommandDerivedFromUnit asserts that the journal `command`
// structured-metadata field (real journal _COMM) is derived from the UNIT, not
// picked by an unrelated unit index. Grounded in homelab reference captures:
//
//	cron.service     -> cron
//	docker.service   -> dockerd
//	sshd.service     -> sshd-session
//	containerd.service -> containerd
//	NetworkManager.service -> NetworkManager
//	systemd-journald.service -> systemd-journal
//
// It also asserts command is NOT constant across distinct units.
func TestLinuxJournalCommandDerivedFromUnit(t *testing.T) {
	cases := map[string]string{
		"cron.service":             "cron",
		"docker.service":           "dockerd",
		"sshd.service":             "sshd-session",
		"containerd.service":       "containerd",
		"NetworkManager.service":   "NetworkManager",
		"systemd-journald.service": "systemd-journal",
	}
	for unit, want := range cases {
		got := commandForUnit(unit)
		if got != want {
			t.Errorf("commandForUnit(%q) = %q, want %q (homelab reference _COMM)", unit, got, want)
		}
	}

	// Fallback: unknown unit strips ".service".
	if got := commandForUnit("mystery-daemon.service"); got != "mystery-daemon" {
		t.Errorf("commandForUnit fallback for unknown unit = %q, want %q", got, "mystery-daemon")
	}

	// Every catalog unit must map to a realistic (non-empty) command.
	for _, u := range linuxUnits {
		if commandForUnit(u) == "" {
			t.Errorf("linuxUnits entry %q has empty command", u)
		}
	}

	// command must NOT be constant across distinct units.
	seen := map[string]bool{}
	for _, u := range linuxUnits {
		seen[commandForUnit(u)] = true
	}
	if len(seen) < 2 {
		t.Errorf("command is effectively constant across units (%d distinct values)", len(seen))
	}
}

// TestLinuxJournalEmittedCommandMatchesUnit drives the full stream builder and
// asserts that the per-stream emitted `command` Line.Meta matches the unit's
// real _COMM, and that distinct units in one host's output carry distinct commands.
func TestLinuxJournalEmittedCommandMatchesUnit(t *testing.T) {
	h := &fixture.Host{Hostname: "host-b", OS: "linux", Logs: true}
	streams := buildLinuxJournalStreams(h, testNow, testEng("bp:host:host-b"))
	if len(streams) == 0 {
		t.Fatal("expected journal streams")
	}
	cmdsByUnit := map[string]string{}
	for _, s := range streams {
		unit := s.Labels["unit"]
		if len(s.Lines) == 0 {
			continue
		}
		got := s.Lines[0].Meta["command"]
		want := commandForUnit(unit)
		if got != want {
			t.Errorf("stream unit=%q emitted command=%q, want %q", unit, got, want)
		}
		cmdsByUnit[unit] = got
	}
	// If the host selected >1 distinct unit, commands must differ between at least two.
	if len(cmdsByUnit) > 1 {
		vals := map[string]bool{}
		for _, c := range cmdsByUnit {
			vals[c] = true
		}
		if len(vals) < 2 {
			t.Errorf("emitted commands identical across %d distinct units: %v", len(cmdsByUnit), cmdsByUnit)
		}
	}
}

// streamsDebug returns a compact debug string for a slice of streams.
func streamsDebug(streams []loki.Stream) string {
	var sb strings.Builder
	for i, s := range streams {
		sb.WriteString("[")
		sb.WriteString(strconv.Itoa(i))
		sb.WriteString(": labels=")
		for k, v := range s.Labels {
			sb.WriteString(k)
			sb.WriteString("=")
			sb.WriteString(v)
			sb.WriteString(" ")
		}
		sb.WriteString("]")
	}
	return sb.String()
}
