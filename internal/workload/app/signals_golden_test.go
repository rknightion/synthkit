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
	Metrics string          `json:"metrics"`
	Logs    string          `json:"logs"`
	Traces  string          `json:"traces"`
	Counts  appParityCounts `json:"counts"`
}

var appParityGoldens = map[string]appParityGolden{
	"3bfda9556c3d/workload-0": {
		Metrics: "dbb067f3cc90aa1342abcb9b458339d729da259077a4c9ee9a6197d93358af4c",
		Logs:    "bf687b40f07cdfbd4b86a6d8917a3180d26742e07095f0b1408d0af876584e5c",
		Traces:  "772c075029860afb7898bf13960943ddbe4f7180e6b8cd438a45e535667b293c",
		Counts:  appParityCounts{MetricSeries: 21703, LogStreams: 1, LogLines: 1, TraceBlocks: 1, Spans: 2},
	},
	"3d6e63e34731/workload-0": {
		Metrics: "26f4a327b8d3ee3c2dac6fa6a58bcd2dc87a13ae28787d8c2b22a5dd14268ec1",
		Logs:    "23edf7feedcc16e5491aa14ae84283d99a6ddf90f496d0793ae12b98ca45a463",
		Traces:  "d67e19ca399875b8bd39ef2cb474d0481dcab0860ffb7b16c50b78d773a509ae",
		Counts:  appParityCounts{MetricSeries: 44557, LogStreams: 4, LogLines: 4, TraceBlocks: 6, Spans: 15},
	},
	"30c71ab79cac/workload-0": {
		Metrics: "84aa3a7dd152bed43f3fc831ebb2d4edf1fc34ea173af99744be77a2bebfe126",
		Logs:    "60ff66f38e69c028dcaae666bdd2208cde6c28cbe22353df726985e2bd524d82",
		Traces:  "541911c92d2b510d65ba55504eb871a8f09fb077f1d85266ea6232a7fcaef387",
		Counts:  appParityCounts{MetricSeries: 23018, LogStreams: 3, LogLines: 3, TraceBlocks: 4, Spans: 14},
	},
	"30c71ab79cac/workload-1": {
		Metrics: "7c88f4158d6a682b6616a3a9dac41806e1fab5ae785804cdcdc63caf83981b91",
		Logs:    "4bce2a78c544d85585669fa4cbeb21363c03e2b983dbac3434720fc964b56217",
		Traces:  "000d86ae8b5720d4089461c1ae78e4ef5e3489a8926bf65c6dbb737d60d588e9",
		Counts:  appParityCounts{MetricSeries: 22203, LogStreams: 1, LogLines: 1, TraceBlocks: 2, Spans: 9},
	},
	"30c71ab79cac/workload-2": {
		Metrics: "977ceb47a636f88bff8ff9a6623f807892a10d66a9c63a03ca575c89916bf30c",
		Logs:    "f9d1e8e94b914eb8e5a326d33be3ff1cf052072f69c04c3d8991469d8a6f4115",
		Traces:  "a0bc3e9cd006a96af49a3eb780c950f40723b8c1aa056bbd0aab2f137e1023b5",
		Counts:  appParityCounts{MetricSeries: 22119, LogStreams: 2, LogLines: 2, TraceBlocks: 1, Spans: 7},
	},
	"30c71ab79cac/workload-3": {
		Metrics: "cf685fd9b47ceb6c8446f59fe09fcd90658fe2452b5083f155a7c12c9be6ecbc",
		Logs:    "6ebe2c718f476910c35047d54aa3c9a8a392194468bceaf81b41174fdcd1ca49",
		Traces:  "82fcd96b37780fc4c107176415db4030a39f3abb151a9405635e76a66ecd0a84",
		Counts:  appParityCounts{MetricSeries: 22119, LogStreams: 2, LogLines: 2, TraceBlocks: 1, Spans: 7},
	},
	"4292cbaa24fb/workload-2": {
		Metrics: "afd206010e1c2cf460a628af824fb9b9cdde430fa823e786e227c70e3af0274b",
		Logs:    "74234e98afe7498fb5daf1f36ac2d78acc339464f950703b8c019892f982b90b",
		Traces:  "f43fc7d250b910aa0cb0ab0c04405afa62fe3bc7b973af5f5c39e80d553bf3dc",
		Counts:  appParityCounts{MetricSeries: 192, LogStreams: 0, LogLines: 0, TraceBlocks: 3, Spans: 5},
	},
	"span-metrics-fixture": {
		Metrics: "1f3f259e2eeaec198743c93685a67585f7b641f5ef5f823daa537ed66f0d40e9",
		Logs:    "74234e98afe7498fb5daf1f36ac2d78acc339464f950703b8c019892f982b90b",
		Traces:  "3cc2dd80149b25c1e634853d22f85b200e00fc3bfbf3bd6418c262289e92340c",
		Counts:  appParityCounts{MetricSeries: 170, LogStreams: 0, LogLines: 0, TraceBlocks: 2, Spans: 4},
	},
}

// TestAppDefaultWireParity fixes one deterministic request at a fixed clock for each app
// declaration discovered in the blueprint catalog. It hashes canonical JSON encodings of the
// sink batches, so the assertion includes metric values, trace ancestry, log correlation fields
// and item counts. Only the metric-series slice is sorted: repeated-run evidence shows that
// State.Collect's map iteration changes that order while preserving every series value.
func TestAppDefaultWireParity(t *testing.T) {
	now := time.Date(2026, 9, 27, 9, 30, 0, 0, time.UTC)
	cases := appParityDeclarations(t)
	cases = append(cases, appParityCase{key: "span-metrics-fixture", cfg: parityFixtureConfig()})
	if len(appParityGoldens) != len(cases) && os.Getenv("APP_PARITY_PRINT") == "" {
		t.Fatalf("golden catalog has %d cases, discovered %d; run with APP_PARITY_PRINT=1 to capture base values", len(appParityGoldens), len(cases))
	}

	got := make(map[string]appParityGolden, len(cases))
	for _, tc := range cases {
		t.Run(tc.key, func(t *testing.T) {
			base := runAppParityCase(t, paritySignalsConfig(t, tc.cfg, "omitted"), now, tc.key)
			got[tc.key] = base
			for _, variant := range []string{"empty", "explicit_true", "null"} {
				t.Run(variant, func(t *testing.T) {
					got := runAppParityCase(t, paritySignalsConfig(t, tc.cfg, variant), now, tc.key)
					if got != base {
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
	for key, want := range appParityGoldens {
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
			clone.Services[i].Signals = nil
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

func runAppParityCase(t *testing.T, cfg *Config, now time.Time, seed string) appParityGolden {
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

	return appParityGolden{
		Metrics: parityDigest(t, sortedMetricBatch(t, metrics.batch)),
		Logs:    parityDigest(t, logs.batch),
		Traces:  parityDigest(t, traces.batch),
		Counts: appParityCounts{
			MetricSeries: metrics.series,
			LogStreams:   logs.streams,
			LogLines:     logs.lines,
			TraceBlocks:  traces.blocks,
			Spans:        traces.spans,
		},
	}
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
	encoded, err := json.Marshal(batch)
	if err != nil {
		t.Fatalf("encode sink batch: %v", err)
	}
	sum := sha256.Sum256(encoded)
	return hex.EncodeToString(sum[:])
}

func sortedMetricBatch(t *testing.T, batch []promrw.Series) []promrw.Series {
	t.Helper()
	count := len(batch)
	result := append([]promrw.Series(nil), batch...)
	sort.SliceStable(result, func(i, j int) bool {
		if result[i].Name != result[j].Name {
			return result[i].Name < result[j].Name
		}
		left, right := metricLabelSignature(result[i].Labels), metricLabelSignature(result[j].Labels)
		if left != right {
			return left < right
		}
		leftLE, leftHasLE := result[i].Labels["le"]
		rightLE, rightHasLE := result[j].Labels["le"]
		if leftHasLE != rightHasLE {
			return !leftHasLE
		}
		return leftLE < rightLE
	})
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
