// SPDX-License-Identifier: AGPL-3.0-only
package main

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/config"
	"github.com/rknightion/synthkit/internal/control"
	logs "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	metrics "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	traces "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	common "go.opentelemetry.io/proto/otlp/common/v1"
	metricpb "go.opentelemetry.io/proto/otlp/metrics/v1"
	"google.golang.org/protobuf/proto"
)

// The observer contract is spelled out literally here, not through synthkit
// code: an external rollout observer joins exports to the Lease by itself.
const (
	sessionIDKey          = "synthkit.ha.session.id"
	sessionAttributionKey = "synthkit.ha.session.attribution"
)

func observerSessionID(holder string) string {
	sum := sha256.Sum256([]byte(holder))
	return hex.EncodeToString(sum[:])
}

// sessionObservation is one received resource, classified against the Lease
// holder at the moment the observer received it.
type sessionObservation struct {
	process, path, role, session, attribution, instance, class string
	cumulative                                                 map[string]uint64
}

func classifySession(role, attribution, session, holder string) string {
	switch {
	case attribution != "lease" || session == "":
		return "unknown"
	case role == "standby":
		return "standby"
	case role == "leader" && holder != "" && session == observerSessionID(holder):
		return "current"
	case role == "leader":
		return "stale"
	}
	return "unknown"
}

type pendingExport struct {
	process, path string
	body          []byte
}

// sessionCollector is a self-observability OTLP receiver fronted by a gateway
// that can buffer exports and deliver them later, as a real gateway might.
type sessionCollector struct {
	t            *testing.T
	api          *namedLeaseRecorder
	mu           sync.Mutex
	process      string
	buffering    bool
	pending      []pendingExport
	observations []sessionObservation
}

func (c *sessionCollector) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var reader io.Reader = r.Body
	if r.Header.Get("Content-Encoding") == "gzip" {
		gz, err := gzip.NewReader(r.Body)
		if err != nil {
			c.t.Error(err)
			return
		}
		defer gz.Close()
		reader = gz
	}
	body, err := io.ReadAll(reader)
	if err != nil {
		c.t.Error(err)
		return
	}
	c.mu.Lock()
	process := c.process
	if c.buffering {
		c.pending = append(c.pending, pendingExport{process: process, path: r.URL.Path, body: body})
		c.mu.Unlock()
		w.WriteHeader(http.StatusOK)
		return
	}
	c.mu.Unlock()
	c.deliver(process, r.URL.Path, body)
	w.WriteHeader(http.StatusOK)
}

// holder is the Lease holder an observer may treat as current: a recorded
// holder persists after a crash, so an expired Lease has no current holder.
func (c *sessionCollector) holder() string {
	c.api.mu.Lock()
	defer c.api.mu.Unlock()
	spec := c.api.document.Spec
	renewed, err := time.Parse("2006-01-02T15:04:05.000000Z07:00", spec.RenewTime)
	if err != nil || !renewed.Add(time.Duration(spec.LeaseDurationSeconds)*time.Second).After(time.Now()) {
		return ""
	}
	return spec.HolderIdentity
}

func (c *sessionCollector) deliver(process, path string, body []byte) {
	type resourceSet struct {
		attrs      []*common.KeyValue
		cumulative map[string]uint64
	}
	var sets []resourceSet
	switch path {
	case "/v1/metrics":
		var req metrics.ExportMetricsServiceRequest
		if err := proto.Unmarshal(body, &req); err != nil {
			c.t.Error(err)
		}
		for _, rm := range req.ResourceMetrics {
			starts := map[string]uint64{}
			for _, sm := range rm.ScopeMetrics {
				for _, m := range sm.Metrics {
					if sum := m.GetSum(); sum != nil && sum.AggregationTemporality == metricpb.AggregationTemporality_AGGREGATION_TEMPORALITY_CUMULATIVE {
						for _, dp := range sum.DataPoints {
							starts[m.Name+fmt.Sprint(dp.Attributes)] = dp.StartTimeUnixNano
						}
					}
				}
			}
			sets = append(sets, resourceSet{rm.Resource.Attributes, starts})
		}
	case "/v1/traces":
		var req traces.ExportTraceServiceRequest
		if err := proto.Unmarshal(body, &req); err != nil {
			c.t.Error(err)
		}
		for _, rs := range req.ResourceSpans {
			sets = append(sets, resourceSet{attrs: rs.Resource.Attributes})
		}
	case "/v1/logs":
		var req logs.ExportLogsServiceRequest
		if err := proto.Unmarshal(body, &req); err != nil {
			c.t.Error(err)
		}
		for _, rl := range req.ResourceLogs {
			sets = append(sets, resourceSet{attrs: rl.Resource.Attributes})
		}
	default:
		c.t.Errorf("unexpected operational path %s", path)
	}
	holder := c.holder()
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, set := range sets {
		o := sessionObservation{process: process, path: path, cumulative: set.cumulative}
		for _, a := range set.attrs {
			switch a.Key {
			case "ha.role":
				o.role = a.Value.GetStringValue()
			case sessionIDKey:
				o.session = a.Value.GetStringValue()
			case sessionAttributionKey:
				o.attribution = a.Value.GetStringValue()
			case "service.instance.id":
				o.instance = a.Value.GetStringValue()
			}
			if v := a.Value.GetStringValue(); holder != "" && strings.Contains(v, holder) {
				c.t.Errorf("export attribute %s carries the raw Lease holder identity", a.Key)
			}
		}
		o.class = classifySession(o.role, o.attribution, o.session, holder)
		c.observations = append(c.observations, o)
	}
}

// release delivers everything the gateway buffered, at the current Lease holder.
func (c *sessionCollector) release() int {
	c.mu.Lock()
	pending := c.pending
	c.pending, c.buffering = nil, false
	c.mu.Unlock()
	for _, p := range pending {
		c.deliver(p.process, p.path, p.body)
	}
	return len(pending)
}

func (c *sessionCollector) seen(fn func(sessionObservation) bool) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, o := range c.observations {
		if fn(o) {
			return true
		}
	}
	return false
}

// Two real lease-mode processes share one Pod UID and host, as a container
// restart does. The observer must tell current, stale-buffered, standby and
// restarted-session exports apart using only the exports and the Lease.
func TestHALeadershipSessionAcrossRestart(t *testing.T) {
	api, leaseSrv := newNamedLeaseRecorder(t)
	defer leaseSrv.Close()
	collector := &sessionCollector{t: t, api: api}
	otlpSrv := httptest.NewServer(collector)
	defer otlpSrv.Close()
	root := t.TempDir()
	baked := filepath.Join(root, "baked")
	if err := os.Mkdir(baked, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(baked, "test.yaml"), []byte("name: test\nhosts:\n  - name: first\n    os: linux\n"), 0644); err != nil {
		t.Fatal(err)
	}
	statePath := filepath.Join(root, "state.json")
	writeHAJSON(t, statePath, control.DefaultState())
	cfg, err := config.Load("")
	if err != nil {
		t.Fatal(err)
	}
	cfg.HAMode, cfg.StateBackend, cfg.HANamespace, cfg.HALeaseName, cfg.PodUID = "lease", "file", "test", "lease", "pod"
	cfg.DryRun, cfg.MasterTick, cfg.GitPollInterval = true, time.Hour, 0
	cfg.SelfObsEnabled, cfg.SelfOTLPEndpoint, cfg.SelfOTLPUser, cfg.SelfOTLPPassword = true, otlpSrv.URL, "user", "token"
	// Forged configured tags must not decide session attribution.
	cfg.SelfObsTags = sessionIDKey + "=forged," + sessionAttributionKey + "=forged"
	cfg.SelfObsMetricInterval = 100 * time.Millisecond
	cfg.HARetryPeriod, cfg.HARenewDeadline, cfg.HAKubeRequestTimeout = 50*time.Millisecond, 300*time.Millisecond, 100*time.Millisecond
	cfg.HAReleaseTimeout, cfg.SendDrainDeadline = time.Second, 3*time.Second
	cfg.BlueprintsDir, cfg.BlueprintDataDir, cfg.BlueprintNames, cfg.SnapshotPath = baked, filepath.Join(root, "data"), []string{"test"}, statePath
	cfg.FMURL, cfg.ControlToken, cfg.SigilEndpoint, cfg.FaroCollector, cfg.ProfilesURL, cfg.PyroscopeURL = "", "", "", "", "", ""
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	type child struct {
		wait   chan error
		cmd    *exec.Cmd
		output *bytes.Buffer
		cancel context.CancelFunc
	}
	start := func(process string) child {
		collector.mu.Lock()
		collector.process = process
		collector.mu.Unlock()
		listen, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		cfg.HTTPAddr = listen.Addr().String()
		_ = listen.Close()
		path := filepath.Join(root, process+".json")
		writeHAJSON(t, path, cfg)
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		cmd := exec.CommandContext(ctx, exe, "-test.run=^TestHAGenericLeaseFileHelper$")
		cmd.Env = append(os.Environ(), "SYNTHKIT_HA_CONFIG="+path, "SYNTHKIT_HA_API="+leaseSrv.URL, "GORACE="+os.Getenv("GORACE")+" atexit_sleep_ms=0")
		output := &bytes.Buffer{}
		cmd.Stdout, cmd.Stderr = output, output
		if err := cmd.Start(); err != nil {
			cancel()
			t.Fatal(err)
		}
		c := child{wait: make(chan error, 1), cmd: cmd, output: output, cancel: cancel}
		go func() { c.wait <- cmd.Wait() }()
		t.Cleanup(cancel)
		return c
	}
	stop := func(c child) {
		if err := c.cmd.Process.Signal(syscall.SIGTERM); err != nil {
			t.Fatal(err)
		}
		select {
		case err := <-c.wait:
			if err != nil {
				t.Fatalf("planned exit: %v\n%s", err, c.output.String())
			}
		case <-time.After(10 * time.Second):
			t.Fatalf("planned shutdown did not exit\n%s", c.output.String())
		}
		c.cancel()
	}
	holderFor := func(previous string) string {
		var holder string
		awaitHA(t, func() bool {
			holder = collector.holder()
			return strings.HasPrefix(holder, "pod/") && holder != previous
		})
		return holder
	}

	// Process A stands by while another process holds the Lease.
	first := start("first")
	awaitHA(t, func() bool {
		return collector.seen(func(o sessionObservation) bool { return o.process == "first" && o.class == "standby" })
	})
	api.allowAcquisition()
	firstHolder := holderFor("")
	awaitHA(t, func() bool {
		return collector.seen(func(o sessionObservation) bool { return o.process == "first" && o.class == "current" })
	})
	// A's final flush is held in the gateway across A's exit and B's acquisition.
	collector.mu.Lock()
	collector.buffering = true
	collector.mu.Unlock()
	stop(first)

	// Process B: the same Pod UID and host, a restarted container.
	second := start("second")
	secondHolder := holderFor(firstHolder)
	if released := collector.release(); released == 0 {
		t.Fatal("gateway buffered no exports from the old session")
	}
	awaitHA(t, func() bool {
		return collector.seen(func(o sessionObservation) bool { return o.process == "second" && o.class == "current" })
	})
	stop(second)

	collector.mu.Lock()
	defer collector.mu.Unlock()
	classes := map[string]map[string]bool{"first": {}, "second": {}}
	sessions := map[string]map[string]bool{"first": {}, "second": {}}
	hosts := map[string]bool{}
	starts := map[string]uint64{}
	for _, o := range collector.observations {
		if o.attribution != "lease" || o.session == "" || o.session == "forged" {
			t.Fatalf("lease-mode export lacks derived session attribution: %+v", o)
		}
		classes[o.process][o.class] = true
		sessions[o.process][o.session] = true
		// service.instance.id is host-pid; the host part is all a restart keeps.
		hosts[o.instance[:max(strings.LastIndex(o.instance, "-"), 0)]] = true
		// A cumulative stream belongs to one writer: (session, role) and its start.
		for stream, at := range o.cumulative {
			key := o.session + "|" + o.role + "|" + stream
			if previous, ok := starts[key]; ok && previous != at {
				t.Fatalf("cumulative writer %s restarted within one provider", key)
			}
			starts[key] = at
		}
	}
	if len(sessions["first"]) != 1 || len(sessions["second"]) != 1 {
		t.Fatalf("one process exported several session IDs: %v", sessions)
	}
	if !sessions["first"][observerSessionID(firstHolder)] || !sessions["second"][observerSessionID(secondHolder)] {
		t.Fatal("session IDs do not join to the Lease holder each process wrote")
	}
	if observerSessionID(firstHolder) == observerSessionID(secondHolder) {
		t.Fatal("restarted session reused the old session ID")
	}
	if len(hosts) != 1 {
		t.Fatalf("restart fixture did not share one host: %v", hosts)
	}
	if !classes["first"]["standby"] || !classes["first"]["current"] || !classes["first"]["stale"] {
		t.Fatalf("old process exports not standby, current and then stale-buffered: %v", classes["first"])
	}
	if !classes["second"]["current"] || classes["second"]["stale"] || classes["first"]["unknown"] || classes["second"]["unknown"] {
		t.Fatalf("restarted process classification: %v / %v", classes["first"], classes["second"])
	}
	t.Logf("old session: standby, current, stale-buffered; restarted session on the same Pod UID and host: current; observations=%d", len(collector.observations))
}
