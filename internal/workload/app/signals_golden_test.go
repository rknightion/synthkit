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
	"runtime"
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
		Metrics: "889ebbbfe5baf0d7e8edced41290ebf1842a15b7352ff3fedd36e98bea7e66f4",
		Logs:    "23edf7feedcc16e5491aa14ae84283d99a6ddf90f496d0793ae12b98ca45a463",
		Traces:  "d67e19ca399875b8bd39ef2cb474d0481dcab0860ffb7b16c50b78d773a509ae",
		Counts:  appParityCounts{MetricSeries: 44557, LogStreams: 4, LogLines: 4, TraceBlocks: 6, Spans: 15},
	},
	"30c71ab79cac/workload-0": {
		Metrics: "1f5f80a8b6ab3cd1e04e3c731c293a1ba201a12c4b64c8cd5ffe252c157431c0",
		Logs:    "60ff66f38e69c028dcaae666bdd2208cde6c28cbe22353df726985e2bd524d82",
		Traces:  "541911c92d2b510d65ba55504eb871a8f09fb077f1d85266ea6232a7fcaef387",
		Counts:  appParityCounts{MetricSeries: 23018, LogStreams: 3, LogLines: 3, TraceBlocks: 4, Spans: 14},
	},
	"30c71ab79cac/workload-1": {
		Metrics: "663967d2bec196883f891639d2cfa67c44ac5c929f74943038508ae84d1fcff3",
		Logs:    "4bce2a78c544d85585669fa4cbeb21363c03e2b983dbac3434720fc964b56217",
		Traces:  "000d86ae8b5720d4089461c1ae78e4ef5e3489a8926bf65c6dbb737d60d588e9",
		Counts:  appParityCounts{MetricSeries: 22203, LogStreams: 1, LogLines: 1, TraceBlocks: 2, Spans: 9},
	},
	"30c71ab79cac/workload-2": {
		Metrics: "e33902955af0c9bf5542762f42a9b6a6484542339dcb91b93b7ee7cb2e3fc2a4",
		Logs:    "f9d1e8e94b914eb8e5a326d33be3ff1cf052072f69c04c3d8991469d8a6f4115",
		Traces:  "a0bc3e9cd006a96af49a3eb780c950f40723b8c1aa056bbd0aab2f137e1023b5",
		Counts:  appParityCounts{MetricSeries: 22119, LogStreams: 2, LogLines: 2, TraceBlocks: 1, Spans: 7},
	},
	"30c71ab79cac/workload-3": {
		Metrics: "90185435f990e8ff5ceb84085d722b5035a41d9553746419a32551f6e39394d2",
		Logs:    "6ebe2c718f476910c35047d54aa3c9a8a392194468bceaf81b41174fdcd1ca49",
		Traces:  "82fcd96b37780fc4c107176415db4030a39f3abb151a9405635e76a66ecd0a84",
		Counts:  appParityCounts{MetricSeries: 22119, LogStreams: 2, LogLines: 2, TraceBlocks: 1, Spans: 7},
	},
	"4292cbaa24fb/workload-2": {
		Metrics: "2c5d014bbcd7071306ea32fab208227248bf42890f6a7fb2ec455af558ba8064",
		Logs:    "74234e98afe7498fb5daf1f36ac2d78acc339464f950703b8c019892f982b90b",
		Traces:  "f43fc7d250b910aa0cb0ab0c04405afa62fe3bc7b973af5f5c39e80d553bf3dc",
		Counts:  appParityCounts{MetricSeries: 192, LogStreams: 0, LogLines: 0, TraceBlocks: 3, Spans: 5},
	},
	"span-metrics-fixture": {
		Metrics: "e65b8ddca27425db76305ca1b1514652e12fca95556b820a79c25b15a27a3ff8",
		Logs:    "74234e98afe7498fb5daf1f36ac2d78acc339464f950703b8c019892f982b90b",
		Traces:  "3cc2dd80149b25c1e634853d22f85b200e00fc3bfbf3bd6418c262289e92340c",
		Counts:  appParityCounts{MetricSeries: 170, LogStreams: 0, LogLines: 0, TraceBlocks: 2, Spans: 4},
	},
}

// Linux and Darwin base digests were recorded at Stage A e182fc2, before per-node signals.
// Metric and log digests differ across these platforms, so each is pinned to
// its own pre-feature values. Counts and trace digests are shared.
var linuxAppParityDigests = map[string]struct{ metrics, logs string }{
	"3bfda9556c3d/workload-0": {metrics: "e18558a6188a5e17f4e3002ad28d41292429edae59c34f558c5e042af21ab574", logs: "bf687b40f07cdfbd4b86a6d8917a3180d26742e07095f0b1408d0af876584e5c"},
	"3d6e63e34731/workload-0": {metrics: "f163b61a69239e5ff2d0400d2293ce06108509aa87242c2ad6970305949b4267", logs: "b19e948d0fb3c819ac979a5f48f3ef7d1946fbe69f778b9be0df1ace7be693c7"},
	"30c71ab79cac/workload-0": {metrics: "abee8d0de73c61ffcf577c41fcd2f72637752bf28d2aa381e171a47de4126b56", logs: "60ff66f38e69c028dcaae666bdd2208cde6c28cbe22353df726985e2bd524d82"},
	"30c71ab79cac/workload-1": {metrics: "15913ddd0997c2990dd9e9c74af95807014da23992283e0ba3e858f8d41b66d9", logs: "4bce2a78c544d85585669fa4cbeb21363c03e2b983dbac3434720fc964b56217"},
	"30c71ab79cac/workload-2": {metrics: "d3402054d46d98e9cea734dc8d0aaf4907c520bdb4545ea2c122b237923b8069", logs: "f9d1e8e94b914eb8e5a326d33be3ff1cf052072f69c04c3d8991469d8a6f4115"},
	"30c71ab79cac/workload-3": {metrics: "496d01be782f09f8b0d5bb3a66fbb9c962875d4444e5de63e05c7d8ff549673d", logs: "6ebe2c718f476910c35047d54aa3c9a8a392194468bceaf81b41174fdcd1ca49"},
	"4292cbaa24fb/workload-2": {metrics: "d9d8eeb9b2228ad730497a25b33cfb47db51f2a5b55510d0cc4fde784e6457fd", logs: "74234e98afe7498fb5daf1f36ac2d78acc339464f950703b8c019892f982b90b"},
	"span-metrics-fixture":    {metrics: "fbe23ae2a941666e9e940edf413db86f9c075f74a261187f16a6c734d38ca35b", logs: "74234e98afe7498fb5daf1f36ac2d78acc339464f950703b8c019892f982b90b"},
}

func appParityGoldensForPlatform(t *testing.T) map[string]appParityGolden {
	t.Helper()
	goldens := make(map[string]appParityGolden, len(appParityGoldens))
	for key, value := range appParityGoldens {
		goldens[key] = value
	}
	switch runtime.GOOS + "/" + runtime.GOARCH {
	case "darwin/arm64":
		return goldens
	case "linux/amd64":
		if len(linuxAppParityDigests) != len(goldens) {
			t.Fatalf("Linux parity goldens cover %d of %d cases", len(linuxAppParityDigests), len(goldens))
		}
		for key, digest := range linuxAppParityDigests {
			value, ok := goldens[key]
			if !ok {
				t.Fatalf("Linux parity golden has unknown case %q", key)
			}
			value.Metrics, value.Logs = digest.metrics, digest.logs
			goldens[key] = value
		}
		return goldens
	default:
		t.Fatalf("no Stage A parity goldens for %s/%s", runtime.GOOS, runtime.GOARCH)
		return nil
	}
}

// TestAppDefaultWireParity fixes one deterministic request at a fixed clock for each app
// declaration discovered in the blueprint catalog. It hashes canonical JSON encodings of the
// sink batches, so the assertion includes metric values, trace ancestry, log correlation fields
// and item counts. Only the metric-series slice is sorted: repeated-run evidence shows that
// State.Collect's map iteration changes that order while preserving every series value.
func TestAppDefaultWireParity(t *testing.T) {
	now := time.Date(2026, 9, 27, 9, 30, 0, 0, time.UTC)
	goldens := appParityGoldensForPlatform(t)
	cases := appParityDeclarations(t)
	cases = append(cases, appParityCase{key: "span-metrics-fixture", cfg: parityFixtureConfig()})
	if len(goldens) != len(cases) && os.Getenv("APP_PARITY_PRINT") == "" {
		t.Fatalf("golden catalog has %d cases, discovered %d; run with APP_PARITY_PRINT=1 to capture base values", len(goldens), len(cases))
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
