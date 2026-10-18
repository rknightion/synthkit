// SPDX-License-Identifier: AGPL-3.0-only

package selfobs

import (
	"compress/gzip"
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"regexp"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
	metrics "go.opentelemetry.io/proto/otlp/collector/metrics/v1"
	metricpb "go.opentelemetry.io/proto/otlp/metrics/v1"
	"google.golang.org/protobuf/proto"
)

type exportedPoint struct {
	metric string
	value  float64
	attrs  map[string]string
}

// exportManifest runs one real Start/Shutdown cycle against a local OTLP receiver and returns
// the delivery-manifest points it received.
func exportManifest(t *testing.T, leaseIdentity string, source func() DeliveryManifest) []exportedPoint {
	t.Helper()
	var mu sync.Mutex
	var got []exportedPoint
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer w.WriteHeader(http.StatusOK)
		if r.URL.Path != "/v1/metrics" {
			_, _ = io.Copy(io.Discard, r.Body)
			return
		}
		var reader io.Reader = r.Body
		if r.Header.Get("Content-Encoding") == "gzip" {
			gz, err := gzip.NewReader(r.Body)
			if err != nil {
				t.Error(err)
				return
			}
			defer gz.Close()
			reader = gz
		}
		body, err := io.ReadAll(reader)
		if err != nil {
			t.Error(err)
			return
		}
		var req metrics.ExportMetricsServiceRequest
		if err := proto.Unmarshal(body, &req); err != nil {
			t.Error(err)
			return
		}
		mu.Lock()
		defer mu.Unlock()
		for _, rm := range req.ResourceMetrics {
			for _, sm := range rm.ScopeMetrics {
				for _, m := range sm.Metrics {
					if m.Name != MetricManifestLane && m.Name != MetricManifestAdopted {
						continue
					}
					for _, dp := range m.GetGauge().GetDataPoints() {
						attrs := map[string]string{}
						for _, a := range dp.Attributes {
							attrs[a.Key] = a.Value.GetStringValue()
						}
						v := dp.GetAsDouble()
						if _, ok := dp.Value.(*metricpb.NumberDataPoint_AsInt); ok {
							v = float64(dp.GetAsInt())
						}
						got = append(got, exportedPoint{metric: m.Name, value: v, attrs: attrs})
					}
				}
			}
		}
	}))
	defer srv.Close()
	so, err := Start(Options{Enabled: true, Endpoint: srv.URL, User: "user", Password: "token", MetricInterval: time.Hour, LeaseIdentity: leaseIdentity}, Gauges{DeliveryManifest: source})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	so.Shutdown(ctx)
	mu.Lock()
	defer mu.Unlock()
	return got
}

// The real OTLP export carries every intended lane, including a quiet one with its long bound and
// the non-expected states, plus the adoption point, all under one session-bound revision.
func TestDeliveryManifestRealExport(t *testing.T) {
	manifest := DeliveryManifest{Fingerprint: "fp-1", Lanes: []ManifestLane{
		{Sink: "pyroscope", State: "expected", Freshness: "scheduled", FreshAfter: 5*time.Minute + 5*time.Second},
		{Sink: "promrw", State: "expected", Freshness: "scheduled", FreshAfter: 65 * time.Second},
		{Sink: "otlp", State: "unsupported", Freshness: "request_driven", FreshAfter: 65 * time.Second},
		{Sink: "loki", State: "disabled", Freshness: "scheduled", FreshAfter: 65 * time.Second},
		// Outside the closed sets: never exported.
		{Sink: "customer-sink", State: "expected", Freshness: "scheduled", FreshAfter: time.Second},
		{Sink: "sigil", State: "healthy", Freshness: "scheduled", FreshAfter: time.Second},
		{Sink: "faro", State: "expected", Freshness: "sometimes", FreshAfter: time.Second},
	}}
	const identity = "pod-a/00112233445566778899aabbccddeeff"
	points := exportManifest(t, identity, func() DeliveryManifest { return manifest })
	lanes := map[string]exportedPoint{}
	var adopted []exportedPoint
	revisions := map[string]bool{}
	for _, p := range points {
		revisions[p.attrs[AttrManifestRevision]] = true
		if p.metric == MetricManifestAdopted {
			adopted = append(adopted, p)
			continue
		}
		lanes[p.attrs["sink"]] = p
	}
	if len(lanes) != 4 || len(adopted) != 1 || len(revisions) != 1 {
		t.Fatalf("exported manifest points = %+v", points)
	}
	bounded, rejected := boundManifest(manifest)
	if rejected != 3 {
		t.Fatalf("rejected %d out-of-vocabulary lanes, want 3", rejected)
	}
	want := manifestRevision(processNonce(), SessionID(identity), bounded)
	if !revisions[want] || len(want) != 16 {
		t.Fatalf("revisions %v, want session-bound %q", revisions, want)
	}
	if p := lanes["pyroscope"]; p.attrs["state"] != "expected" || p.attrs["freshness"] != "scheduled" || p.value != 305 {
		t.Fatalf("quiet lane exported as %+v, want expected with a 305s bound", p)
	}
	if lanes["otlp"].attrs["state"] != "unsupported" || lanes["otlp"].attrs["freshness"] != "request_driven" || lanes["loki"].attrs["state"] != "disabled" {
		t.Fatalf("non-expected lanes not explicit: %+v", lanes)
	}
	if age := time.Now().Unix() - int64(adopted[0].value); age < 0 || age > 60 {
		t.Fatalf("adoption time %v is not this run", adopted[0].value)
	}
	for _, p := range points {
		if len(p.attrs) > 4 {
			t.Fatalf("unbounded manifest point attributes: %v", p.attrs)
		}
	}

	// The same manifest under another session, and under unknown attribution, is a new revision.
	other := exportManifest(t, "pod-b/ffeeddccbbaa99887766554433221100", func() DeliveryManifest { return manifest })
	if len(other) == 0 || other[0].attrs[AttrManifestRevision] == want {
		t.Fatalf("a new session kept revision %q: %+v", want, other)
	}
	unknown := exportManifest(t, "", func() DeliveryManifest { return manifest })
	if len(unknown) == 0 || unknown[0].attrs[AttrManifestRevision] != manifestRevision(processNonce(), SessionUnknown, bounded) {
		t.Fatalf("unknown attribution revision: %+v", unknown)
	}
}

// A configuration change between collections replaces every lane point with the new revision
// (no stale revision lingers) and moves the adoption time; an empty manifest still exports.
func TestDeliveryManifestFollowsConfigurationChange(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	defer func() { _ = mp.Shutdown(context.Background()) }()
	var current atomic.Pointer[DeliveryManifest]
	set := func(m DeliveryManifest) { current.Store(&m) }
	set(DeliveryManifest{Fingerprint: "a", Lanes: []ManifestLane{{Sink: "promrw", State: "expected", Freshness: "scheduled", FreshAfter: time.Minute}, {Sink: "promrw", State: "disabled", Freshness: "scheduled"}}})
	so := &SelfObs{enabled: true, sessionID: SessionID("pod/1")}
	if err := so.initInstruments(mp, Gauges{DeliveryManifest: func() DeliveryManifest { return *current.Load() }}); err != nil {
		t.Fatal(err)
	}
	collect := func() (map[string][]map[string]string, map[string]int64) {
		var rm metricdata.ResourceMetrics
		if err := reader.Collect(context.Background(), &rm); err != nil {
			t.Fatal(err)
		}
		lanes := map[string][]map[string]string{}
		adopted := map[string]int64{}
		for _, sm := range rm.ScopeMetrics {
			for _, m := range sm.Metrics {
				switch data := m.Data.(type) {
				case metricdata.Gauge[float64]:
					if m.Name != MetricManifestLane {
						continue
					}
					for _, dp := range data.DataPoints {
						attrs := map[string]string{}
						for _, kv := range dp.Attributes.ToSlice() {
							attrs[string(kv.Key)] = kv.Value.AsString()
						}
						lanes[attrs[AttrManifestRevision]] = append(lanes[attrs[AttrManifestRevision]], attrs)
					}
				case metricdata.Gauge[int64]:
					if m.Name != MetricManifestAdopted {
						continue
					}
					for _, dp := range data.DataPoints {
						v, _ := dp.Attributes.Value(attribute.Key(AttrManifestRevision))
						adopted[v.AsString()] = dp.Value
					}
				}
			}
		}
		return lanes, adopted
	}
	first, firstAdopted := collect()
	if len(first) != 1 || len(firstAdopted) != 1 {
		t.Fatalf("first collection lanes=%v adopted=%v", first, firstAdopted)
	}
	for _, points := range first {
		if len(points) != 1 || points[0]["state"] != "expected" {
			t.Fatalf("duplicate sink not bounded to one point: %v", points)
		}
	}

	set(DeliveryManifest{Fingerprint: "b", Lanes: []ManifestLane{{Sink: "promrw", State: "disabled", Freshness: "scheduled", FreshAfter: time.Minute}}})
	second, secondAdopted := collect()
	if len(second) != 1 || len(secondAdopted) != 1 {
		t.Fatalf("stale revision lingered: lanes=%v adopted=%v", second, secondAdopted)
	}
	for rev, points := range second {
		if _, old := first[rev]; old {
			t.Fatal("configuration change kept the old revision")
		}
		if points[0]["state"] != "disabled" {
			t.Fatalf("changed lane = %v", points)
		}
	}

	set(DeliveryManifest{Fingerprint: "c"})
	third, thirdAdopted := collect()
	if len(third) != 0 || len(thirdAdopted) != 1 {
		t.Fatalf("empty manifest export: lanes=%v adopted=%v", third, thirdAdopted)
	}
}

// manifestChildEnv makes the test binary act as a separate synthkit process that exports one
// manifest with HA off and prints its revision.
const manifestChildEnv = "SYNTHKIT_MANIFEST_REVISION_CHILD"

// Two processes with identical configuration and no session (HA off, where hostname and PID may
// repeat across a container restart) must publish different revisions.
func TestDeliveryManifestRevisionChangesPerProcess(t *testing.T) {
	manifest := DeliveryManifest{Fingerprint: "same-config", Lanes: []ManifestLane{{Sink: "promrw", State: "expected", Freshness: "scheduled", FreshAfter: 65 * time.Second}}}
	revisionOf := func(points []exportedPoint) string {
		for _, p := range points {
			if p.metric == MetricManifestAdopted {
				return p.attrs[AttrManifestRevision]
			}
		}
		return ""
	}
	if os.Getenv(manifestChildEnv) == "1" {
		fmt.Printf("REVISION=%s\n", revisionOf(exportManifest(t, "", func() DeliveryManifest { return manifest })))
		return
	}
	run := func() string {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestDeliveryManifestRevisionChangesPerProcess$", "-test.count=1")
		cmd.Env = append(os.Environ(), manifestChildEnv+"=1", "OTEL_RESOURCE_ATTRIBUTES=")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("child process: %v\n%s", err, out)
		}
		m := regexp.MustCompile(`REVISION=([0-9a-f]{16})`).FindSubmatch(out)
		if m == nil {
			t.Fatalf("child printed no revision:\n%s", out)
		}
		return string(m[1])
	}
	first, second := run(), run()
	if first == second {
		t.Fatalf("two processes with identical configuration exported the same revision %s", first)
	}
	// Within one process the revision is stable for one configuration.
	a := revisionOf(exportManifest(t, "", func() DeliveryManifest { return manifest }))
	b := revisionOf(exportManifest(t, "", func() DeliveryManifest { return manifest }))
	if a == "" || a != b || a == first || a == second {
		t.Fatalf("in-process revisions %q %q, child revisions %q %q", a, b, first, second)
	}
}
