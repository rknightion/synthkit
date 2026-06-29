// SPDX-License-Identifier: AGPL-3.0-only

package host

import (
	"context"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/fixture"
	"github.com/rknightion/synthkit/internal/shape"
	"github.com/rknightion/synthkit/internal/sink/loki"
	"github.com/rknightion/synthkit/internal/sink/promrw"
)

// captureWriter records the series written during a Tick.
type captureWriter struct{ series []promrw.Series }

func (w *captureWriter) Write(_ context.Context, batch []promrw.Series) error {
	w.series = append(w.series, batch...)
	return nil
}

// captureLogWriter records the log streams written during a Tick.
type captureLogWriter struct{ streams []loki.Stream }

func (w *captureLogWriter) Write(_ context.Context, batch []loki.Stream) error {
	w.streams = append(w.streams, batch...)
	return nil
}

// tickHost builds the construct from a fixture.Host, Ticks it once with a seeded
// engine + fixed time, and returns the captured series.
func tickHost(t *testing.T, h *fixture.Host) []promrw.Series {
	t.Helper()
	set := &fixture.Set{Seed: "bp:host:" + h.Hostname, Host: h}
	c, err := Build(&Config{}, set)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	cw := &captureWriter{}
	w := &core.World{Shape: shape.New("", nil), Metrics: cw}
	if err := c.Tick(context.Background(), time.Date(2026, 6, 17, 12, 0, 0, 0, time.UTC), w); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	return cw.series
}

func hasSeries(series []promrw.Series, name string) bool {
	for _, s := range series {
		if s.Name == name {
			return true
		}
	}
	return false
}

func jobInstanceFor(series []promrw.Series, name string) (job, instance string, found bool) {
	for _, s := range series {
		if s.Name == name {
			return s.Labels["job"], s.Labels["instance"], true
		}
	}
	return "", "", false
}

func TestLinuxHostIdentityAndCPU(t *testing.T) {
	h := &fixture.Host{Hostname: "camden", OS: "linux", Profile: "integration", NumCPU: 4, MemTotal: 8 << 30}
	series := tickHost(t, h)

	if len(series) == 0 {
		t.Fatal("no series emitted")
	}
	// (a) every series carries instance=hostname, no blueprint/cluster label.
	for _, s := range series {
		if s.Labels["instance"] != "camden" {
			t.Errorf("series %q instance=%q, want camden", s.Name, s.Labels["instance"])
		}
		if _, ok := s.Labels["blueprint"]; ok {
			t.Errorf("series %q carries forbidden blueprint label", s.Name)
		}
		if _, ok := s.Labels["cluster"]; ok {
			t.Errorf("series %q carries forbidden cluster label", s.Name)
		}
	}
	// (b) node_cpu_seconds_total present with job=integrations/node_exporter.
	job, _, ok := jobInstanceFor(series, "node_cpu_seconds_total")
	if !ok {
		t.Fatal("node_cpu_seconds_total not emitted")
	}
	if job != "integrations/node_exporter" {
		t.Errorf("node_cpu_seconds_total job=%q, want integrations/node_exporter", job)
	}
}

func TestWindowsHost(t *testing.T) {
	h := &fixture.Host{Hostname: "winbox", OS: "windows", Profile: "integration", NumCPU: 2, MemTotal: 16 << 30}
	series := tickHost(t, h)

	if !hasSeries(series, "windows_cpu_time_total") {
		t.Error("windows_cpu_time_total not emitted")
	}
	job, _, ok := jobInstanceFor(series, "windows_cpu_time_total")
	if !ok || job != "integrations/windows_exporter" {
		t.Errorf("windows_cpu_time_total job=%q, want integrations/windows_exporter", job)
	}
}

func TestMacOSHost(t *testing.T) {
	h := &fixture.Host{Hostname: "alex", OS: "darwin", Profile: "integration", NumCPU: 8, MemTotal: 32 << 30}
	series := tickHost(t, h)

	if !hasSeries(series, "node_memory_total_bytes") {
		t.Error("node_memory_total_bytes not emitted")
	}
	job, _, ok := jobInstanceFor(series, "node_memory_total_bytes")
	if !ok || job != "integrations/macos-node" {
		t.Errorf("node_memory_total_bytes job=%q, want integrations/macos-node", job)
	}
}

func TestConstructMetadata(t *testing.T) {
	h := &fixture.Host{Hostname: "camden", OS: "linux", Profile: "integration"}
	set := &fixture.Set{Seed: "bp:host:camden", Host: h}
	c, err := Build(&Config{}, set)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if c.Kind() != "host" {
		t.Errorf("Kind()=%q, want host", c.Kind())
	}
	if c.Interval() != 60*time.Second {
		t.Errorf("Interval()=%v, want 60s", c.Interval())
	}
	sigs := c.Signals()
	if len(sigs) != 2 || sigs[0] != core.Metrics || sigs[1] != core.Logs {
		t.Errorf("Signals()=%v, want [Metrics Logs]", sigs)
	}
}

func TestBuildRejectsMissingHost(t *testing.T) {
	if _, err := Build(&Config{}, &fixture.Set{Seed: "x"}); err == nil {
		t.Error("Build with nil Host should error")
	}
	if _, err := Build(&Config{}, nil); err == nil {
		t.Error("Build with nil fixture set should error")
	}
}

func TestDockerLane(t *testing.T) {
	h := &fixture.Host{Hostname: "camden", OS: "linux", Profile: "integration", Docker: true}
	series := tickHost(t, h)
	if !hasSeries(series, "container_cpu_usage_seconds_total") {
		t.Error("docker host should emit container_cpu_usage_seconds_total")
	}
	if !hasSeries(series, "machine_memory_bytes") {
		t.Error("docker host should emit machine_memory_bytes")
	}
}

// TestDockerMetricsCarryCadvisorLabels asserts that the Docker cadvisor metric series
// carry the NATIVE cadvisor labels {name, image, id} — NOT a `container` label. Per the
// a live homelab reference capture, cadvisor metrics expose `name` (container name),
// `image` (image:tag), and `id` (cgroup path), while the `container` label is a LOGS-only
// relabel applied by Alloy's docker discovery. (host-capture.md "Docker label schema".)
func TestDockerMetricsCarryCadvisorLabels(t *testing.T) {
	h := &fixture.Host{Hostname: "camden", OS: "linux", Profile: "integration", Docker: true}
	series := tickHost(t, h)

	var checked int
	for _, s := range series {
		// Only the container-scoped cadvisor series carry per-container identity.
		if s.Name != "container_cpu_usage_seconds_total" &&
			s.Name != "container_memory_usage_bytes" &&
			s.Name != "container_network_receive_bytes_total" &&
			s.Name != "container_fs_usage_bytes" {
			continue
		}
		checked++
		if _, ok := s.Labels["container"]; ok {
			t.Errorf("%s carries forbidden `container` metric label (cadvisor metrics use name/image/id); labels=%v", s.Name, s.Labels)
		}
		if v := s.Labels["name"]; v == "" {
			t.Errorf("%s missing `name` cadvisor label; labels=%v", s.Name, s.Labels)
		}
		if v := s.Labels["image"]; v == "" {
			t.Errorf("%s missing `image` cadvisor label; labels=%v", s.Name, s.Labels)
		}
		if v := s.Labels["id"]; v == "" {
			t.Errorf("%s missing `id` cadvisor label; labels=%v", s.Name, s.Labels)
		}
	}
	if checked == 0 {
		t.Fatal("no container-scoped cadvisor series found to assert labels on")
	}
}

// TestDockerLogsStillUseContainerLabel asserts the LOGS lane keeps the `container` stream
// label (that IS correct per the Alloy docker log discovery relabel) even though metrics
// now use name/image/id.
func TestDockerLogsStillUseContainerLabel(t *testing.T) {
	h := &fixture.Host{Hostname: "camden", OS: "linux", Profile: "integration", Docker: true, Logs: true}
	set := &fixture.Set{Seed: "bp:host:camden", Host: h}
	c, err := Build(&Config{}, set)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	lw := &captureLogWriter{}
	w := &core.World{Shape: shape.New("", nil), Metrics: &captureWriter{}, Logs: lw}
	if err := c.Tick(context.Background(), time.Date(2026, 6, 17, 12, 0, 0, 0, time.UTC), w); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	var found bool
	for _, s := range lw.streams {
		if s.Labels["job"] == "integrations/docker" {
			if v := s.Labels["container"]; v == "" {
				t.Errorf("docker log stream missing `container` label; labels=%v", s.Labels)
			} else {
				found = true
			}
		}
	}
	if !found {
		t.Error("no docker log stream with a `container` label found")
	}
}

// TestDockerMetricLogContainerSetMatch asserts the docker LOG streams derive the SAME
// container set (names) as the docker METRIC series — both must come from the real
// per-construct seed (fx.Seed = "<blueprint>:host:<hostname>"), NOT a hardcoded
// "bp:host:<hostname>". A non-"bp" seed exposes the bug (logs.go hardcoded the prefix).
func TestDockerMetricLogContainerSetMatch(t *testing.T) {
	h := &fixture.Host{Hostname: "camden", OS: "linux", Profile: "integration", Docker: true, Logs: true}
	set := &fixture.Set{Seed: "myblueprint:host:camden", Host: h}
	c, err := Build(&Config{}, set)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	mw := &captureWriter{}
	lw := &captureLogWriter{}
	w := &core.World{Shape: shape.New("", nil), Metrics: mw, Logs: lw}
	if err := c.Tick(context.Background(), time.Date(2026, 6, 17, 12, 0, 0, 0, time.UTC), w); err != nil {
		t.Fatalf("Tick: %v", err)
	}

	// Metric container names: the `name` label on container-scoped cadvisor series.
	metricNames := map[string]bool{}
	for _, s := range mw.series {
		if s.Name == "container_cpu_usage_seconds_total" {
			if n := s.Labels["name"]; n != "" {
				metricNames[n] = true
			}
		}
	}
	if len(metricNames) == 0 {
		t.Fatal("no docker metric container names captured")
	}

	// Log container names: the `container` stream label on docker log streams.
	logNames := map[string]bool{}
	for _, s := range lw.streams {
		if s.Labels["job"] == "integrations/docker" {
			if n := s.Labels["container"]; n != "" {
				logNames[n] = true
			}
		}
	}
	if len(logNames) == 0 {
		t.Fatal("no docker log container names captured")
	}

	// The two sets must be identical (same container set drives metrics and logs).
	for n := range metricNames {
		if !logNames[n] {
			t.Errorf("metric container %q absent from log container set %v", n, keys(logNames))
		}
	}
	for n := range logNames {
		if !metricNames[n] {
			t.Errorf("log container %q absent from metric container set %v", n, keys(metricNames))
		}
	}
}

func keys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// TestTickLogStreams asserts that a linux host with Logs=true produces log streams
// via Tick — specifically that w.Logs.Write is called with streams carrying the
// correct job/instance labels and no high-card fields as stream labels.
func TestTickLogStreams(t *testing.T) {
	h := &fixture.Host{
		Hostname: "camden",
		OS:       "linux",
		Profile:  "integration",
		Logs:     true,
	}
	set := &fixture.Set{Seed: "bp:host:camden", Host: h}
	c, err := Build(&Config{}, set)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	lw := &captureLogWriter{}
	mw := &captureWriter{}
	w := &core.World{
		Shape:   shape.New("", nil),
		Metrics: mw,
		Logs:    lw,
	}
	if err := c.Tick(context.Background(), time.Date(2026, 6, 17, 12, 0, 0, 0, time.UTC), w); err != nil {
		t.Fatalf("Tick: %v", err)
	}

	if len(lw.streams) == 0 {
		t.Fatal("Tick with Logs=true: w.Logs.Write was never called (no streams)")
	}

	// Verify at least one stream has correct job+instance labels.
	var found bool
	for _, s := range lw.streams {
		if s.Labels["job"] == "integrations/node_exporter" && s.Labels["instance"] == "camden" {
			found = true
			// Ensure high-card fields are not stream labels.
			for _, hcKey := range []string{"pid", "uid", "command", "executable"} {
				if _, ok := s.Labels[hcKey]; ok {
					t.Errorf("Tick: stream has high-card label %q in stream labels; labels=%v", hcKey, s.Labels)
				}
			}
		}
	}
	if !found {
		t.Errorf("Tick: no stream with job=integrations/node_exporter instance=camden found in %d streams", len(lw.streams))
	}
}

// TestTickLogsDisabled asserts that Logs=false means w.Logs.Write is never called.
func TestTickLogsDisabled(t *testing.T) {
	h := &fixture.Host{
		Hostname: "camden",
		OS:       "linux",
		Profile:  "integration",
		Logs:     false,
	}
	set := &fixture.Set{Seed: "bp:host:camden", Host: h}
	c, err := Build(&Config{}, set)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	lw := &captureLogWriter{}
	mw := &captureWriter{}
	w := &core.World{
		Shape:   shape.New("", nil),
		Metrics: mw,
		Logs:    lw,
	}
	if err := c.Tick(context.Background(), time.Date(2026, 6, 17, 12, 0, 0, 0, time.UTC), w); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if len(lw.streams) != 0 {
		t.Errorf("Tick with Logs=false: expected no log streams, got %d", len(lw.streams))
	}
}
