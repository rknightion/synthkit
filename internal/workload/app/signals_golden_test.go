// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"bytes"
	"context"
	cryptorand "crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/core/coretest"
	"github.com/rknightion/synthkit/internal/fixture"
	"github.com/rknightion/synthkit/internal/ledger"
	"github.com/rknightion/synthkit/internal/shape"
	"github.com/rknightion/synthkit/internal/sink/loki"
	"github.com/rknightion/synthkit/internal/sink/otlp"
	"github.com/rknightion/synthkit/internal/sink/promrw"
	"gopkg.in/yaml.v3"
)

type appParityCounts struct {
	MetricSeries int `json:"metric_series"`
	LogStreams   int `json:"log_streams"`
	LogLines     int `json:"log_lines"`
	TraceBlocks  int `json:"trace_blocks"`
	Spans        int `json:"spans"`
}

type appParityGolden struct {
	Traces string          `json:"traces"`
	Counts appParityCounts `json:"counts"`
}

// Regenerate only deliberately: go test ./internal/workload/app -run '^TestAppDefaultWireParity$' -app-parity-update
var appParityUpdate = flag.Bool("app-parity-update", false, "regenerate canonical app parity fixture")

type appParityWire struct {
	Metrics json.RawMessage `json:"metrics"`
	Logs    json.RawMessage `json:"logs"`
}

var appParityGoldens = map[string]appParityGolden{
	// AI Factory adds five declarations; existing Stage A pins remain unchanged.
	"168ecc243b35/workload-0": {
		Traces: "dd616c23daa7d9875f5ca8bdb372b3b75f2b87cced9638d792e16fefe01c940f",
		Counts: appParityCounts{MetricSeries: 2, LogStreams: 0, LogLines: 0, TraceBlocks: 1, Spans: 6},
	},
	"168ecc243b35/workload-1": {
		Traces: "f3c71aedeff4f2e05b384166797c6a2c8616ca7848f4ec767219b2334d09e676",
		Counts: appParityCounts{MetricSeries: 2, LogStreams: 0, LogLines: 0, TraceBlocks: 1, Spans: 8},
	},
	"168ecc243b35/workload-2": {
		Traces: "86554b8528c4e6d25ae666d3c8def0cab17d24c854d45829abace836f3e3ad10",
		Counts: appParityCounts{MetricSeries: 2, LogStreams: 0, LogLines: 0, TraceBlocks: 1, Spans: 10},
	},
	"168ecc243b35/workload-3": {
		Traces: "4593de7d16eeaa2006bedf15279709111bb898d02c10d9cd3fec582fbb0a70cb",
		Counts: appParityCounts{MetricSeries: 2, LogStreams: 0, LogLines: 0, TraceBlocks: 1, Spans: 6},
	},
	"168ecc243b35/workload-4": {
		Traces: "630647b6f06f01029ba77c8f3ffb78ca484e129a82188cb619ce230444f3d0d2",
		Counts: appParityCounts{MetricSeries: 2, LogStreams: 0, LogLines: 0, TraceBlocks: 1, Spans: 6},
	},
	// Fresh live correlation consumes crypto-random bytes before projection, while
	// the trace-local root SpanID retains deterministic agent/tool selection.
	// These six trace pins reflect only the child span/parent ID stream shift in
	// this seeded-reader harness; audited base/candidate payloads preserve all
	// non-ID fields, metrics, logs, counts and parent topology.
	"3bfda9556c3d/workload-0": {
		Traces: "772c075029860afb7898bf13960943ddbe4f7180e6b8cd438a45e535667b293c",
		Counts: appParityCounts{MetricSeries: 21703, LogStreams: 1, LogLines: 1, TraceBlocks: 1, Spans: 2},
	},
	"3d6e63e34731/workload-0": {
		Traces: "d67e19ca399875b8bd39ef2cb474d0481dcab0860ffb7b16c50b78d773a509ae",
		Counts: appParityCounts{MetricSeries: 44557, LogStreams: 4, LogLines: 4, TraceBlocks: 6, Spans: 15},
	},
	"30c71ab79cac/workload-0": {
		Traces: "541911c92d2b510d65ba55504eb871a8f09fb077f1d85266ea6232a7fcaef387",
		Counts: appParityCounts{MetricSeries: 23018, LogStreams: 3, LogLines: 3, TraceBlocks: 4, Spans: 14},
	},
	"30c71ab79cac/workload-1": {
		Traces: "000d86ae8b5720d4089461c1ae78e4ef5e3489a8926bf65c6dbb737d60d588e9",
		Counts: appParityCounts{MetricSeries: 22203, LogStreams: 1, LogLines: 1, TraceBlocks: 2, Spans: 9},
	},
	"30c71ab79cac/workload-2": {
		Traces: "a0bc3e9cd006a96af49a3eb780c950f40723b8c1aa056bbd0aab2f137e1023b5",
		Counts: appParityCounts{MetricSeries: 22119, LogStreams: 2, LogLines: 2, TraceBlocks: 1, Spans: 7},
	},
	"30c71ab79cac/workload-3": {
		Traces: "82fcd96b37780fc4c107176415db4030a39f3abb151a9405635e76a66ecd0a84",
		Counts: appParityCounts{MetricSeries: 22119, LogStreams: 2, LogLines: 2, TraceBlocks: 1, Spans: 7},
	},
	"4292cbaa24fb/workload-2": {
		Traces: "f43fc7d250b910aa0cb0ab0c04405afa62fe3bc7b973af5f5c39e80d553bf3dc",
		Counts: appParityCounts{MetricSeries: 192, LogStreams: 0, LogLines: 0, TraceBlocks: 3, Spans: 5},
	},
	"span-metrics-fixture": {
		Traces: "3cc2dd80149b25c1e634853d22f85b200e00fc3bfbf3bd6418c262289e92340c",
		Counts: appParityCounts{MetricSeries: 170, LogStreams: 0, LogLines: 0, TraceBlocks: 2, Spans: 4},
	},
}

// TestAppDefaultWireParity fixes one deterministic request at a fixed clock for each app
// declaration discovered in the blueprint catalog. It compares canonical metrics and logs
// against a fixture, while traces and counts retain their exact Stage A pins. Variants must
// match the base bytes exactly. Only the metric-series slice is sorted: evidence shows that
// State.Collect's map iteration changes that order while preserving every series value.
func TestAppDefaultWireParity(t *testing.T) {
	now := time.Date(2026, 9, 27, 9, 30, 0, 0, time.UTC)
	goldens := appParityGoldens
	cases := appParityDeclarations(t)
	cases = append(cases, appParityCase{key: "span-metrics-fixture", cfg: parityFixtureConfig()})
	if len(goldens) != len(cases) && os.Getenv("APP_PARITY_PRINT") == "" {
		t.Fatalf("golden catalog has %d cases, discovered %d; run with APP_PARITY_PRINT=1 to capture base values", len(goldens), len(cases))
	}

	got := make(map[string]appParityGolden, len(cases))
	for _, tc := range cases {
		t.Run(tc.key, func(t *testing.T) {
			base, baseWire := runAppParityCase(t, paritySignalsConfig(t, tc.cfg, "omitted"), now, tc.key)
			got[tc.key] = base
			compareAppParityFixture(t, tc.key, baseWire)
			for _, variant := range []string{"empty", "explicit_true", "null"} {
				t.Run(variant, func(t *testing.T) {
					got, wire := runAppParityCase(t, paritySignalsConfig(t, tc.cfg, variant), now, tc.key)
					if got != base || !bytes.Equal(wire.Metrics, baseWire.Metrics) || !bytes.Equal(wire.Logs, baseWire.Logs) {
						t.Fatalf("signals %s changed default wire output:\n got: %+v\nwant: %+v", variant, got, base)
					}
				})
			}
		})
	}
	if os.Getenv("APP_PARITY_PRINT") != "" {
		b, _ := yaml.Marshal(got)
		t.Logf("base wire goldens:\n%s", b)
		t.FailNow()
	}
	for key, want := range goldens {
		if got[key] != want {
			t.Errorf("wire output for %s changed:\n got: %+v\nwant: %+v", key, got[key], want)
		}
	}
}

type appParityCase struct {
	key string
	cfg *Config
}

func paritySignalsConfig(t *testing.T, cfg *Config, mode string) *Config {
	t.Helper()
	encoded, err := yaml.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if mode == "null" {
		var doc yaml.Node
		if err := yaml.Unmarshal(encoded, &doc); err != nil {
			t.Fatal(err)
		}
		foundServices := false
		for i := 0; i+1 < len(doc.Content[0].Content); i += 2 {
			if doc.Content[0].Content[i].Value != "services" {
				continue
			}
			foundServices = true
			for _, service := range doc.Content[0].Content[i+1].Content {
				foundSignals := false
				for j := 0; j+1 < len(service.Content); j += 2 {
					if service.Content[j].Value == "signals" {
						service.Content[j+1] = &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!null", Value: "null"}
						foundSignals = true
						break
					}
				}
				if !foundSignals {
					service.Content = append(service.Content,
						&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: "signals"},
						&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!null", Value: "null"})
				}
			}
			break
		}
		if !foundServices {
			t.Fatal("parity config has no services")
		}
		encoded, err = yaml.Marshal(&doc)
		if err != nil {
			t.Fatal(err)
		}
	}
	var clone Config
	dec := yaml.NewDecoder(bytes.NewReader(encoded))
	dec.KnownFields(true)
	if err := dec.Decode(&clone); err != nil {
		t.Fatalf("clone app config: %v", err)
	}
	for i := range clone.Services {
		switch mode {
		case "omitted":
			clone.Services[i].Signals = nil
		case "null":
			if clone.Services[i].Signals != nil {
				t.Fatalf("signals: null decoded as non-nil for service %q", clone.Services[i].Name)
			}
		case "empty":
			clone.Services[i].Signals = &NodeSignals{}
		case "explicit_true":
			on := true
			clone.Services[i].Signals = &NodeSignals{Traces: &on, Logs: &on, Metrics: &on}
		default:
			t.Fatalf("unknown parity signals mode %q", mode)
		}
	}
	return &clone
}

func appParityDeclarations(t *testing.T) []appParityCase {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join("..", "..", "..", "blueprints", "*.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) == 0 {
		t.Fatal("no blueprint declarations found")
	}
	var cases []appParityCase
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		var doc yaml.Node
		if err := yaml.Unmarshal(data, &doc); err != nil {
			t.Fatalf("decode catalog document: %v", err)
		}
		root := doc.Content[0]
		for i := 0; i+1 < len(root.Content); i += 2 {
			if root.Content[i].Value != "workloads" || root.Content[i+1].Kind != yaml.SequenceNode {
				continue
			}
			for declIndex, workload := range root.Content[i+1].Content {
				var kind struct {
					Type string `yaml:"type"`
				}
				if err := workload.Decode(&kind); err != nil {
					t.Fatalf("decode workload kind in %s: %v", path, err)
				}
				if kind.Type != "app" {
					continue
				}
				configNode := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
				for j := 0; j+1 < len(workload.Content); j += 2 {
					k, v := workload.Content[j], workload.Content[j+1]
					switch k.Value {
					case "type", "name", "runs_on", "replicas", "calls", "for_each_env", "envs":
						continue
					default:
						configNode.Content = append(configNode.Content, k, v)
					}
				}
				configYAML, err := yaml.Marshal(configNode)
				if err != nil {
					t.Fatal(err)
				}
				var cfg Config
				dec := yaml.NewDecoder(bytes.NewReader(configYAML))
				dec.KnownFields(true)
				if err := dec.Decode(&cfg); err != nil {
					t.Fatalf("strict decode app config in catalog file: %v", err)
				}
				fileKey := sha256.Sum256([]byte(filepath.Base(path)))
				cases = append(cases, appParityCase{
					key: fmt.Sprintf("%x/workload-%d", fileKey[:6], declIndex), cfg: &cfg,
				})
			}
		}
	}
	if len(cases) == 0 {
		t.Fatal("no app workload declarations found")
	}
	return cases
}

func parityFixtureConfig() *Config {
	return &Config{
		Traffic: Traffic{OffPeakRPS: 1, PeakRPS: 1},
		Services: []ServiceNode{
			{Name: "frontend", Type: "frontend", Entry: true, Calls: []string{"backend"}},
			{Name: "backend", Type: "web", Calls: []string{"store"}},
			{Name: "store", Type: "db"},
		},
	}
}

func runAppParityCase(t *testing.T, cfg *Config, now time.Time, seed string) (appParityGolden, appParityWire) {
	t.Helper()
	previousReader := cryptorand.Reader
	cryptorand.Reader = newParityRandomReader(seed)
	defer func() { cryptorand.Reader = previousReader }()

	decs := make([]*fixture.DB, 0)
	seen := map[string]bool{}
	for _, svc := range cfg.Services {
		if svc.DBInstance == "" {
			continue
		}
		for _, name := range []string{svc.DBInstance, svc.DBInstance + "-prod"} {
			if seen[name] {
				continue
			}
			seen[name] = true
			decs = append(decs, &fixture.DB{Engine: "postgres", Name: name, InstanceKey: "postgresql://db.internal:5432/app", Env: coretest.Env(), Cloud: coretest.Cloud()})
		}
	}
	w, err := build(cfg, core.Binding{
		Name: "app-parity", Seed: seed, Env: coretest.Env(), Cluster: coretest.Cluster(), Databases: decs,
	})
	if err != nil {
		t.Fatalf("build app declaration: %v", err)
	}
	workload := w.(*Workload)
	metrics := &appParityMetricWriter{}
	logs := &appParityLogWriter{}
	traces := &appParityTraceWriter{}
	world := &core.World{Shape: shape.New("", nil), Metrics: metrics, Logs: logs, Traces: traces, EmitSpanMetrics: true}
	mintShape := shape.New("", nil)
	r := workload.m.mintOne(now, mintShape)
	if os.Getenv("APP_PARITY_PRINT") != "" {
		probe := shape.New("", nil)
		t.Logf("models=%+v probe=%.12f request=%+v", cfg.Models, probe.Float64(), r)
	}
	seedCorrelation(r, seed)
	if err := workload.Tick(context.Background(), now, world); err != nil {
		t.Fatalf("Tick: %v", err)
	}
	if err := workload.ProjectBatch(context.Background(), now, world, []*ledger.Request{r}); err != nil {
		t.Fatalf("ProjectBatch: %v", err)
	}
	if os.Getenv("APP_PARITY_PRINT") != "" && len(logs.batch) > 0 {
		encoded, _ := json.Marshal(logs.batch)
		t.Logf("fixed request route=%q outcome=%q trace=%q logs=%s", r.Route, r.Outcome, r.TraceID, encoded)
	}

	sortedMetrics := sortedMetricBatch(t, metrics.batch)
	wire := appParityWire{Metrics: parityJSON(t, sortedMetrics), Logs: parityJSON(t, logs.batch)}
	// Diagnostic only: retain canonical sink batches for cross-platform investigation.
	if dir := os.Getenv("APP_PARITY_DUMP_DIR"); dir != "" {
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
		data := parityJSON(t, struct {
			Metrics json.RawMessage `json:"metrics"`
			Logs    json.RawMessage `json:"logs"`
			Traces  []otlp.Resource `json:"traces"`
		}{wire.Metrics, wire.Logs, traces.batch})
		if err := os.WriteFile(filepath.Join(dir, strings.ReplaceAll(seed, "/", "__")+".json"), data, 0644); err != nil {
			t.Fatal(err)
		}
	}
	return appParityGolden{
		Traces: parityDigest(t, traces.batch),
		Counts: appParityCounts{
			MetricSeries: metrics.series,
			LogStreams:   logs.streams,
			LogLines:     logs.lines,
			TraceBlocks:  traces.blocks,
			Spans:        traces.spans,
		},
	}, wire
}

func seedCorrelation(r *ledger.Request, seed string) {
	r.Correlation = ledger.NewCorrelationFromSeed(seed)
	for i := range r.Calls {
		call := &r.Calls[i]
		call.SpanID = ledger.SpanIDFromSeed(seed, fmt.Sprintf("hop-%d-client", i))
		if call.PeerSpanID != "" {
			call.PeerSpanID = ledger.SpanIDFromSeed(seed, fmt.Sprintf("hop-%d-server", i))
		}
	}
}

func parityDigest(t *testing.T, batch any) string {
	t.Helper()
	sum := sha256.Sum256(parityJSON(t, batch))
	return hex.EncodeToString(sum[:])
}

func parityJSON(t *testing.T, batch any) []byte {
	t.Helper()
	encoded, err := json.Marshal(batch)
	if err != nil {
		t.Fatalf("encode sink batch: %v", err)
	}
	return encoded
}

func sortedMetricBatch(t *testing.T, batch []promrw.Series) []promrw.Series {
	t.Helper()
	count := len(batch)
	type keyed struct {
		series promrw.Series
		key    string
	}
	items := make([]keyed, len(batch))
	for i, series := range batch {
		encoded, err := json.Marshal(series)
		if err != nil {
			t.Fatalf("encode metric series: %v", err)
		}
		items[i] = keyed{series: series, key: string(encoded)}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].key < items[j].key })
	result := make([]promrw.Series, len(items))
	for i, item := range items {
		result[i] = item.series
	}
	if len(result) != count {
		t.Fatalf("canonical metric ordering changed series count from %d to %d", count, len(result))
	}
	return result
}

func metricLabelSignature(labels map[string]string) string {
	keys := make([]string, 0, len(labels))
	for key := range labels {
		if key != "le" {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	pairs := make([]string, 0, len(keys))
	for _, key := range keys {
		pairs = append(pairs, strconv.Quote(key)+"="+strconv.Quote(labels[key]))
	}
	return strings.Join(pairs, ",")
}

type appParityMetricWriter struct {
	batch  []promrw.Series
	series int
}

func (w *appParityMetricWriter) Write(ctx context.Context, batch []promrw.Series) error {
	w.batch = append(w.batch, batch...)
	w.series += len(batch)
	return nil
}

type appParityLogWriter struct {
	batch   []loki.Stream
	streams int
	lines   int
}

func (w *appParityLogWriter) Write(ctx context.Context, batch []loki.Stream) error {
	w.batch = append(w.batch, batch...)
	w.streams += len(batch)
	for _, stream := range batch {
		w.lines += len(stream.Lines)
	}
	return nil
}

type appParityTraceWriter struct {
	batch  []otlp.Resource
	blocks int
	spans  int
}

func (w *appParityTraceWriter) Write(ctx context.Context, batch []otlp.Resource) error {
	w.blocks += len(batch)
	resources := make([]otlp.Resource, len(batch))
	count := 0
	for ri, resource := range batch {
		resources[ri] = resource
		resources[ri].Spans = append([]otlp.Span(nil), resource.Spans...)
		count += len(resources[ri].Spans)
	}
	if len(resources) != len(batch) || count != countResourceSpans(batch) {
		return fmt.Errorf("trace capture changed batch counts: resources %d/%d spans %d/%d", len(resources), len(batch), count, countResourceSpans(batch))
	}
	w.spans += count
	w.batch = append(w.batch, resources...)
	return nil
}

func countResourceSpans(resources []otlp.Resource) int {
	count := 0
	for _, resource := range resources {
		count += len(resource.Spans)
	}
	return count
}

type parityRandomReader struct {
	seed    []byte
	counter uint64
	block   []byte
	offset  int
}

func newParityRandomReader(seed string) *parityRandomReader {
	return &parityRandomReader{seed: []byte(seed)}
}

func (r *parityRandomReader) Read(p []byte) (int, error) {
	want := len(p)
	for len(p) > 0 {
		if r.offset == len(r.block) {
			input := make([]byte, len(r.seed)+8)
			copy(input, r.seed)
			binary.LittleEndian.PutUint64(input[len(r.seed):], r.counter)
			r.counter++
			block := sha256.Sum256(input)
			r.block = block[:]
			r.offset = 0
		}
		n := copy(p, r.block[r.offset:])
		p = p[n:]
		r.offset += n
	}
	return want, nil
}
