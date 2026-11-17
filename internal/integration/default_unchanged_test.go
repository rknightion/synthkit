// SPDX-License-Identifier: AGPL-3.0-only

package integration

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime/debug"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"testing/cryptotest"
	"time"

	"github.com/rknightion/synthkit/internal/blueprint"
	"github.com/rknightion/synthkit/internal/control"
	"github.com/rknightion/synthkit/internal/core/coretest"
	"github.com/rknightion/synthkit/internal/runner"
	"github.com/rknightion/synthkit/internal/sigil"
	"github.com/rknightion/synthkit/internal/sink/otlp"
	pyrosink "github.com/rknightion/synthkit/internal/sink/pyroscope"
)

var (
	defaultUnchangedUpdate = flag.Bool("default-unchanged-update", false,
		"regenerate internal/integration/testdata/default-unchanged/<blueprint>.json from the current tree")
	defaultUnchangedDump = flag.String("default-unchanged-dump", "",
		"directory for the canonical rows of differing families (default: a new temporary directory, printed)")
	defaultUnchangedAgainst = flag.String("default-unchanged-against", "",
		"on a mismatch, run this guard at the given git revision and print a row diff of the differing families")
	defaultUnchangedDumpFamilies = flag.String("default-unchanged-dump-families", "",
		`dump-only mode: write the rows of the families listed in this file ("<lane>\t<family>" lines), or of every family with "all", under -default-unchanged-dump and compare nothing`)
)

const (
	duFixtureDir = "testdata/default-unchanged"
	// duRandomSeed seeds crypto/rand (testing/cryptotest.SetGlobalRandom) for each blueprint run.
	duRandomSeed = 0x5ee0_d0c1_0168_0915
)

// duLanes are the captured signal lanes the fixture compares, in report order.
var duLanes = []string{"metrics", "logs", "otlp_metrics", "otlp_logs", "traces"}

// TestShippedBlueprintsDefaultUnchanged is the default-unchanged guard for the causal request
// model: a blueprint that declares no environments[].resources must emit exactly what it emitted
// before the model existed.
//
// For every shipped blueprint (blueprints/*.yaml), separately, it loads the blueprint through the
// real loader and catalog, builds a fresh runner wired to capture sinks for every lane (Prometheus
// remote-write, Loki, OTLP traces, OTLP metrics, OTLP logs, Faro RUM, Pyroscope and Sigil, as the
// dry-run binary wires them), opts the blueprint into span-metrics emission (as buildEstate does,
// so the spanmetrics and service-graph families are covered), and drives a fixed clock: six
// MasterTicks 5 s apart from 2026-06-17T11:00:00Z, then RunOnce at +30 s. crypto/rand is replaced
// by a seeded deterministic source for the run, so request-scoped IDs and every value derived from
// them (for example the ledger's trace-ID-keyed render offset, which moves span and log
// timestamps) are reproducible.
//
// It then canonicalises five lanes into rows:
//
//   - metrics: one row per remote-write series sample (name, labels, value, timestamp, kind,
//     producer metadata, exemplars, native histogram);
//   - logs: one row per Loki line (stream labels, timestamp, line, structured metadata);
//   - otlp_metrics: one row per OTLP data point (resource, scope, metric metadata, point);
//   - otlp_logs: one row per OTLP log record (resource, scope, record);
//   - traces: one row per span (resource, scope, name, kind, start, end, attributes, status,
//     events). The default app request duration (app/minter.go drawDuration) reaches no metric
//     or log, only span timing, so without this lane the guard cannot see a latency change.
//
// Faro RUM beacons, Pyroscope profiles and Sigil exports are wired so those lanes run as in the
// dry-run binary, but are not compared.
//
// Rows are masked (duMask), quantised (duQuantise), grouped into families (duFamilyOf) and sorted.
// The committed testdata/default-unchanged/<blueprint>.json holds, per family, the row count and
// the first 16 hex digits of the sha256 of its rows; the guard fails on any family whose count or
// digest differs, or that appears or disappears.
//
// Quantisation and its cost: every number is reduced to 6 significant digits before hashing,
// because CI runs amd64 while maintainers and the gate run arm64, and FMA fusion makes thousands of
// float values differ in their last bits (the digests generated on a Mac match on the arm64 Linux
// gate and under GOARCH=amd64). A float change smaller than half a unit in the sixth significant
// digit (about 5e-7 relative), and any change to an integer of 10^6 or more below that quantum, is
// invisible. Timestamps, labels, names and strings stay exact; numbers inside a JSON log body are
// quantised the same way, every other body byte stays exact.
//
// On a mismatch the failure lists blueprint, lane, family and the expected and produced count and
// digest, and writes the produced rows of every differing family to -default-unchanged-dump=<dir>
// (default: a new temporary directory; the path is printed). Add
// -default-unchanged-against=<git revision> to see why: the guard extracts `git archive <rev>` into
// a temporary directory, installs this test file there, reruns itself for that blueprint in
// dump-only mode and prints a capped -/+ diff of each differing family's rows, with the changed
// fields of the first aligned pairs:
//
//	go test ./internal/integration -run 'TestShippedBlueprintsDefaultUnchanged/<blueprint>' -count=1 -default-unchanged-against=main
//
// A shipped blueprint whose output changes on purpose (for example when a later task opts a
// shipped blueprint into resources:) regenerates its digests and names the blueprint in the change:
//
//	go test ./internal/integration -run 'TestShippedBlueprintsDefaultUnchanged/<blueprint>' -default-unchanged-update -count=1
//
// A new shipped blueprint needs a digest file too (the same command); a digest file with no
// blueprint fails.
func TestShippedBlueprintsDefaultUnchanged(t *testing.T) {
	paths, err := filepath.Glob(filepath.Join("..", "..", "blueprints", "*.yaml"))
	if err != nil || len(paths) == 0 {
		t.Fatalf("blueprints: %v (%d)", err, len(paths))
	}
	// Hand the large per-blueprint captures back to the OS before later tests in the package
	// build the shared estate.
	t.Cleanup(debug.FreeOSMemory)
	shipped := map[string]bool{}
	for _, path := range paths {
		name := strings.TrimSuffix(filepath.Base(path), ".yaml")
		shipped[name] = true
		t.Run(name, func(t *testing.T) {
			cryptotest.SetGlobalRandom(t, duRandomSeed)
			got := duCanonical(t, duRun(t, path))
			digests := filepath.Join(duFixtureDir, name+".json")
			switch {
			case *defaultUnchangedDumpFamilies != "":
				duDumpOnly(t, name, got)
			case *defaultUnchangedUpdate:
				duWriteDigests(t, digests, name, got)
			default:
				duCompareDigests(t, name, digests, got)
			}
		})
	}
	if *defaultUnchangedUpdate || *defaultUnchangedDumpFamilies != "" {
		return
	}
	files, err := filepath.Glob(filepath.Join(duFixtureDir, "*.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		if name := strings.TrimSuffix(filepath.Base(file), ".json"); !shipped[name] {
			t.Errorf("digest file %s has no shipped blueprint blueprints/%s.yaml", file, name)
		}
	}
}

type duOTLPMetricCapture struct {
	mu        sync.Mutex
	resources []otlp.MetricResource
}

func (c *duOTLPMetricCapture) Write(_ context.Context, resources []otlp.MetricResource) error {
	c.mu.Lock()
	c.resources = append(c.resources, resources...)
	c.mu.Unlock()
	return nil
}

type duOTLPLogCapture struct {
	mu        sync.Mutex
	resources []otlp.LogResource
}

func (c *duOTLPLogCapture) Write(_ context.Context, resources []otlp.LogResource) error {
	c.mu.Lock()
	c.resources = append(c.resources, resources...)
	c.mu.Unlock()
	return nil
}

type duSigilCapture struct {
	mu      sync.Mutex
	exports []sigil.Export
}

func (c *duSigilCapture) Write(_ context.Context, exports []sigil.Export) error {
	c.mu.Lock()
	c.exports = append(c.exports, exports...)
	c.mu.Unlock()
	return nil
}

type duProfileCapture struct {
	mu     sync.Mutex
	series []pyrosink.Series
}

func (c *duProfileCapture) Write(_ context.Context, series []pyrosink.Series) error {
	c.mu.Lock()
	c.series = append(c.series, series...)
	c.mu.Unlock()
	return nil
}

type duCaptures struct {
	metrics     *coretest.MetricCapture
	logs        *coretest.LogCapture
	traces      *coretest.TraceCapture
	rum         *coretest.RUMCapture
	otlpMetrics *duOTLPMetricCapture
	otlpLogs    *duOTLPLogCapture
	sigil       *duSigilCapture
	profiles    *duProfileCapture
}

// duRun drives one blueprint through the real loader and runner on the fixed clock.
func duRun(t *testing.T, path string) *duCaptures {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	res, err := blueprint.Load(data, runner.Catalog())
	if err != nil {
		t.Fatalf("load %s: %v", path, err)
	}
	if err := blueprint.ValidateSet([]*blueprint.Resolved{res}); err != nil {
		t.Fatalf("ValidateSet %s: %v", path, err)
	}
	c := &duCaptures{
		metrics: &coretest.MetricCapture{}, logs: &coretest.LogCapture{}, traces: &coretest.TraceCapture{},
		rum: &coretest.RUMCapture{}, otlpMetrics: &duOTLPMetricCapture{}, otlpLogs: &duOTLPLogCapture{},
		sigil: &duSigilCapture{}, profiles: &duProfileCapture{},
	}
	r := runner.New(runner.Sinks{
		Metrics: c.metrics, Logs: c.logs, Traces: c.traces, RUM: c.rum, Profiles: c.profiles,
		OTLPMetrics: c.otlpMetrics, OTLPLogs: c.otlpLogs, Sigil: c.sigil,
	}, runner.Catalog(), runner.Options{})
	if err := r.AddBlueprint(res); err != nil {
		t.Fatalf("AddBlueprint %q: %v", res.Name, err)
	}
	cs := control.DefaultState()
	cs.SpanMetricsBlueprints = append(cs.SpanMetricsBlueprints, res.Name)
	r.ApplyControl(cs)
	ctx := context.Background()
	now := time.Date(2026, 6, 17, 11, 0, 0, 0, time.UTC)
	for i := range 6 {
		if err := r.MasterTick(ctx, now.Add(time.Duration(i*5)*time.Second)); err != nil {
			t.Fatalf("MasterTick: %v", err)
		}
	}
	if err := r.RunOnce(ctx, now.Add(30*time.Second)); err != nil {
		t.Fatalf("RunOnce: %v", err)
	}
	drainCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	r.DrainQueues(drainCtx)
	return c
}

// duFamilies is one run's canonical output: lane -> family -> sorted canonical JSON rows.
type duFamilies map[string]map[string][]string

func duCanonical(t *testing.T, c *duCaptures) duFamilies {
	t.Helper()
	rows := map[string][]any{}
	for _, s := range c.metrics.All() {
		rows["metrics"] = append(rows["metrics"], s)
	}
	for _, stream := range c.logs.Streams {
		for _, line := range stream.Lines {
			rows["logs"] = append(rows["logs"], map[string]any{
				"Labels": stream.Labels, "T": line.T, "Body": line.Body, "Meta": line.Meta,
			})
		}
	}
	for _, res := range c.otlpMetrics.resources {
		head := map[string]any{
			"Attrs": res.Attrs, "Scope": res.Scope, "ResourceSchemaURL": res.ResourceSchemaURL,
			"PreserveEmptyScope": res.PreserveEmptyScope, "Producer": res.Producer,
		}
		for _, m := range res.Metrics {
			meta := map[string]any{
				"Name": m.Name, "Description": m.Description, "Unit": m.Unit, "Kind": m.Kind,
				"Monotonic": m.Monotonic, "Temporality": m.Temporality,
			}
			add := func(kind string, point any) {
				rows["otlp_metrics"] = append(rows["otlp_metrics"], map[string]any{
					"Resource": head, "Metric": meta, "PointKind": kind, "Point": point,
				})
			}
			for _, p := range m.Numbers {
				add("number", p)
			}
			for _, p := range m.Histograms {
				add("histogram", p)
			}
			for _, p := range m.ExpHistograms {
				add("exponential_histogram", p)
			}
			for _, p := range m.Summaries {
				// Quantiles is map[float64]float64, which encoding/json cannot key.
				qs := make([][2]float64, 0, len(p.Quantiles))
				for q, v := range p.Quantiles {
					qs = append(qs, [2]float64{q, v})
				}
				sort.Slice(qs, func(i, j int) bool { return qs[i][0] < qs[j][0] })
				add("summary", map[string]any{
					"Attrs": p.Attrs, "Time": p.Time, "Count": p.Count, "Sum": p.Sum, "Quantiles": qs,
				})
			}
		}
	}
	for _, res := range c.otlpLogs.resources {
		head := map[string]any{"CollectorProfile": res.CollectorProfile, "Attrs": res.Attrs, "Scope": res.Scope}
		for _, rec := range res.Records {
			rows["otlp_logs"] = append(rows["otlp_logs"], map[string]any{"Resource": head, "Record": rec})
		}
	}
	for _, res := range c.traces.Resources {
		head := map[string]any{"Attrs": res.Attrs, "Scope": res.Scope}
		for _, span := range res.Spans {
			rows["traces"] = append(rows["traces"], map[string]any{"Resource": head, "Span": span})
		}
	}
	out := duFamilies{}
	for _, lane := range duLanes {
		byFamily := map[string][]string{}
		for _, row := range rows[lane] {
			tree := duTree(t, row)
			duMask(lane, tree)
			tree = duQuantise(tree, "")
			m, _ := tree.(map[string]any)
			b, err := json.Marshal(tree)
			if err != nil {
				t.Fatalf("%s row: %v", lane, err)
			}
			family := duFamilyOf(lane, m)
			byFamily[family] = append(byFamily[family], string(b))
		}
		for _, r := range byFamily {
			sort.Strings(r)
		}
		out[lane] = byFamily
	}
	return out
}

// duTree turns a captured value into a generic JSON tree whose numbers keep their exact encoding.
func duTree(t *testing.T, v any) any {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("encode %T: %v", v, err)
	}
	return duDecode(t, b)
}

func duDecode(t *testing.T, b []byte) any {
	t.Helper()
	d := json.NewDecoder(bytes.NewReader(b))
	d.UseNumber()
	var tree any
	if err := d.Decode(&tree); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if d.Decode(new(any)) != io.EOF {
		t.Fatal("trailing JSON content")
	}
	return tree
}

const (
	duMasked = "masked:"     // prefix for a masked request-scoped ID: masked:trace_id
	duHost   = "masked:host" // a value read from the machine running the test
)

var (
	duHex32 = regexp.MustCompile(`^[0-9a-f]{32}$`)
	duHex16 = regexp.MustCompile(`^[0-9a-f]{16}$`)
	duUUID  = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)
)

// duIDShapes are the ledger's request-scoped correlation IDs (internal/ledger
// NewCorrelation/NewSpanID/NewTraceID: crypto/rand W3C trace and span IDs and v4 UUIDs) by the
// key that carries them, with the exact value shape each must have to be masked. A value of any
// other shape under one of these keys is compared as it is, so a format change still fails.
var duIDShapes = map[string]*regexp.Regexp{
	"trace_id":         duHex32,
	"span_id":          duHex16,
	"request_id":       duUUID,
	"session_id":       duUUID,
	"correlation_id":   duUUID,
	"portkey_trace_id": duUUID,
	"run_id":           duUUID,
	// Span attributes carrying the same ledger IDs under semantic-convention keys
	// (webservice/traces.go, telemetryspec profiles, aiagent/traces.go userID).
	"session.id":             duUUID,
	"enduser.id":             duUUID,
	"app.correlation_id":     duUUID,
	"app.user_action_id":     duUUID,
	"gen_ai.conversation.id": duUUID,
	"user.id":                duUUID,
}

func duMaskIDs(m map[string]any) {
	for key, shape := range duIDShapes {
		if v, ok := m[key].(string); ok && shape.MatchString(v) {
			m[key] = duMasked + key
		}
	}
}

// duMask applies, in place, the only two kinds of masking the guard does:
//
//  1. Request-scoped IDs (duIDShapes), wherever the run emits them: Loki structured metadata,
//     remote-write exemplar labels, OTLP log record TraceID/SpanID and attributes, and span
//     TraceID/SpanID/ParentID (an empty ParentID, a root, stays empty) and span and span-event
//     attributes. No metric label, Loki line or JSON log body carries a ledger ID in any
//     shipped blueprint (every 32-hex, 16-hex and UUID value in the fixtures was classified;
//     the others are deterministic fixture identities, which stay compared). These are
//     already reproducible here (crypto/rand is seeded), but they are identities, not behaviour:
//     masking them keeps the guard about values and timing. A real change next to an ID (the
//     exemplar value, a sibling label, the line, the timestamp) is still compared exactly.
//     Log timestamps are NOT masked: the ledger offsets each request's render time by a hash of
//     its trace ID, so an unseeded run moves timestamps as well as IDs; seeding crypto/rand,
//     rather than masking time, is what makes the run reproducible.
//  2. Host facts: beyla.internal.build.info's obi.goarch/obi.goos/obi.goversion, which the
//     emitter reads from the Go runtime running the test (so they differ between a Mac, the
//     arm64 Linux gate and amd64 CI).
func duMask(lane string, tree any) {
	row, ok := tree.(map[string]any)
	if !ok {
		return
	}
	switch lane {
	case "metrics":
		exemplars, _ := row["Exemplars"].([]any)
		for _, e := range exemplars {
			if ex, ok := e.(map[string]any); ok {
				if labels, ok := ex["Labels"].(map[string]any); ok {
					duMaskIDs(labels)
				}
			}
		}
	case "logs":
		if meta, ok := row["Meta"].(map[string]any); ok {
			duMaskIDs(meta)
		}
	case "otlp_logs":
		if rec, ok := row["Record"].(map[string]any); ok {
			if v, ok := rec["TraceID"].(string); ok && duHex32.MatchString(v) {
				rec["TraceID"] = duMasked + "trace_id"
			}
			if v, ok := rec["SpanID"].(string); ok && duHex16.MatchString(v) {
				rec["SpanID"] = duMasked + "span_id"
			}
			if attrs, ok := rec["Attrs"].(map[string]any); ok {
				duMaskIDs(attrs)
			}
		}
	case "traces":
		span, ok := row["Span"].(map[string]any)
		if !ok {
			return
		}
		for field, shape := range map[string]*regexp.Regexp{"TraceID": duHex32, "SpanID": duHex16, "ParentID": duHex16} {
			if v, ok := span[field].(string); ok && shape.MatchString(v) {
				span[field] = duMasked + field
			}
		}
		if attrs, ok := span["Attrs"].(map[string]any); ok {
			duMaskIDs(attrs)
		}
		events, _ := span["Events"].([]any)
		for _, e := range events {
			if ev, ok := e.(map[string]any); ok {
				if attrs, ok := ev["Attrs"].(map[string]any); ok {
					duMaskIDs(attrs)
				}
			}
		}
	case "otlp_metrics":
		metric, _ := row["Metric"].(map[string]any)
		if metric["Name"] == "beyla.internal.build.info" {
			// beylaagent/native_otlp.go stamps the test host's runtime.GOARCH, runtime.GOOS and
			// runtime.Version(): darwin on a Mac, linux on the gate, amd64 in CI.
			if point, ok := row["Point"].(map[string]any); ok {
				if pattrs, ok := point["Attrs"].(map[string]any); ok {
					for _, k := range []string{"obi.goarch", "obi.goos", "obi.goversion"} {
						if _, ok := pattrs[k]; ok {
							pattrs[k] = duHost
						}
					}
				}
			}
		}
	}
}

// duSignificantDigits is the float quantum: every JSON number in a canonical row (and every number
// inside a JSON log body) is rewritten with strconv.FormatFloat(v, 'g', 6, 64) before hashing.
const duSignificantDigits = 6

// duQuantise returns v with every number quantised to duSignificantDigits. Integers below 10^6
// are unchanged by this; larger integers and every fraction keep six significant digits. A JSON
// log body (a Body string that is valid JSON) has its number tokens quantised in place, so its
// key order and every other byte still count.
func duQuantise(v any, key string) any {
	switch x := v.(type) {
	case json.Number:
		return json.Number(duQuantiseNumber(string(x)))
	case map[string]any:
		for k, e := range x {
			x[k] = duQuantise(e, k)
		}
		return x
	case []any:
		for i, e := range x {
			x[i] = duQuantise(e, key)
		}
		return x
	case string:
		if key == "Body" && len(x) > 0 && (x[0] == '{' || x[0] == '[') && json.Valid([]byte(x)) {
			return duQuantiseJSONText(x)
		}
		return x
	}
	return v
}

func duQuantiseNumber(s string) string {
	f, err := strconv.ParseFloat(s, 64)
	if err != nil || math.IsInf(f, 0) || math.IsNaN(f) {
		return s
	}
	return strconv.FormatFloat(f, 'g', duSignificantDigits, 64)
}

// duQuantiseJSONText rewrites the number tokens of valid JSON text, leaving strings untouched.
func duQuantiseJSONText(s string) string {
	var b strings.Builder
	inString, escaped := false, false
	for i := 0; i < len(s); {
		c := s[i]
		switch {
		case inString:
			b.WriteByte(c)
			if escaped {
				escaped = false
			} else if c == '\\' {
				escaped = true
			} else if c == '"' {
				inString = false
			}
			i++
		case c == '"':
			inString = true
			b.WriteByte(c)
			i++
		case c == '-' || (c >= '0' && c <= '9'):
			j := i + 1
			for j < len(s) && strings.IndexByte("0123456789.eE+-", s[j]) >= 0 {
				j++
			}
			b.WriteString(duQuantiseNumber(s[i:j]))
			i = j
		default:
			b.WriteByte(c)
			i++
		}
	}
	return b.String()
}

// duFamilyOf names the signal family a canonical row belongs to; each family gets one digest.
//
//   - metrics: the remote-write series name;
//   - otlp_metrics: the OTLP metric name;
//   - logs: the stream's job label (every shipped stream has one) plus its level label when set,
//     so streams of one emitter across clusters and environments share a family;
//   - otlp_logs: the instrumentation scope name plus the resource service.name;
//   - traces: the resource service.name plus the span name.
func duFamilyOf(lane string, row map[string]any) string {
	str := func(m any, k string) string {
		mm, _ := m.(map[string]any)
		s, _ := mm[k].(string)
		return s
	}
	switch lane {
	case "metrics":
		return str(row, "Name")
	case "otlp_metrics":
		return str(row["Metric"], "Name")
	case "logs":
		family := "job=" + str(row["Labels"], "job")
		if level := str(row["Labels"], "level"); level != "" {
			family += " level=" + level
		}
		return family
	case "otlp_logs":
		res, _ := row["Resource"].(map[string]any)
		return "scope=" + str(res["Scope"], "Name") + " service.name=" + str(res["Attrs"], "service.name")
	default:
		res, _ := row["Resource"].(map[string]any)
		return "service.name=" + str(res["Attrs"], "service.name") + " span=" + str(row["Span"], "Name")
	}
}

// duDigest is one family's committed fingerprint: its row count and the first 16 hex digits
// (64 bits) of the sha256 of its sorted canonical rows, each followed by a newline.
type duDigest struct {
	Count int
	SHA   string
}

func duDigestOf(rows []string) duDigest {
	h := sha256.New()
	for _, r := range rows {
		h.Write([]byte(r))
		h.Write([]byte{'\n'})
	}
	return duDigest{Count: len(rows), SHA: hex.EncodeToString(h.Sum(nil))[:16]}
}

const duDigestNote = "per family: [row count, first 16 hex digits of sha256 over the sorted canonical masked rows]; numbers quantised to 6 significant digits"

// duWriteDigests writes the digest file by hand so its order is fixed: lanes in duLanes order,
// families sorted, one family per line.
func duWriteDigests(t *testing.T, path, name string, fams duFamilies) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	q := func(s string) string {
		b, err := json.Marshal(s)
		if err != nil {
			t.Fatal(err)
		}
		return string(b)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "{\n  \"blueprint\": %s,\n  \"digest\": %s,\n  \"families\": {", q(name), q(duDigestNote))
	firstLane := true
	for _, lane := range duLanes {
		if len(fams[lane]) == 0 {
			continue
		}
		if !firstLane {
			b.WriteString(",")
		}
		firstLane = false
		fmt.Fprintf(&b, "\n    %s: {", q(lane))
		for i, family := range duSortedKeys(fams[lane]) {
			if i > 0 {
				b.WriteString(",")
			}
			d := duDigestOf(fams[lane][family])
			fmt.Fprintf(&b, "\n      %s: [%d, %s]", q(family), d.Count, q(d.SHA))
		}
		b.WriteString("\n    }")
	}
	b.WriteString("\n  }\n}\n")
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
}

func duReadDigests(t *testing.T, path string) map[string]map[string]duDigest {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("default-unchanged digests %s: %v (a new shipped blueprint needs -default-unchanged-update)", path, err)
	}
	var file struct {
		Families map[string]map[string][2]json.RawMessage `json:"families"`
	}
	if err := json.Unmarshal(data, &file); err != nil {
		t.Fatalf("digests %s: %v", path, err)
	}
	out := map[string]map[string]duDigest{}
	for lane, fams := range file.Families {
		out[lane] = map[string]duDigest{}
		for family, pair := range fams {
			var d duDigest
			if json.Unmarshal(pair[0], &d.Count) != nil || json.Unmarshal(pair[1], &d.SHA) != nil {
				t.Fatalf("digests %s: malformed %s %q", path, lane, family)
			}
			out[lane][family] = d
		}
	}
	return out
}

func duSortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

type duMismatch struct {
	lane, family string
	want, got    duDigest
}

const duReportLimit = 20

// duCompareDigests checks every family's digest. On a mismatch it writes the produced rows of each
// differing family under the dump directory and, with -default-unchanged-against, diffs them
// against the same family produced at that revision.
func duCompareDigests(t *testing.T, name, path string, fams duFamilies) {
	t.Helper()
	want := duReadDigests(t, path)
	var mism []duMismatch
	for _, lane := range duLanes {
		keys := map[string]bool{}
		for f := range want[lane] {
			keys[f] = true
		}
		for f := range fams[lane] {
			keys[f] = true
		}
		for _, family := range duSortedKeys(keys) {
			var got duDigest
			if rows, ok := fams[lane][family]; ok {
				got = duDigestOf(rows)
			}
			if w := want[lane][family]; w != got {
				mism = append(mism, duMismatch{lane: lane, family: family, want: w, got: got})
			}
		}
	}
	if len(mism) == 0 {
		return
	}
	dir := *defaultUnchangedDump
	if dir == "" {
		var err error
		if dir, err = os.MkdirTemp("", "default-unchanged-"); err != nil {
			t.Fatal(err)
		}
	}
	var b strings.Builder
	fmt.Fprintf(&b, "blueprint %s: %d families differ from %s (regenerate with -default-unchanged-update only if the change is intended, and name the blueprint in the change):\n",
		name, len(mism), path)
	for i, m := range mism {
		if i == duReportLimit {
			fmt.Fprintf(&b, "  ... %d more\n", len(mism)-i)
			break
		}
		fmt.Fprintf(&b, "  lane %s family %q: rows fixture %d -> produced %d, sha256 %s -> %s\n",
			m.lane, m.family, m.want.Count, m.got.Count, duOr(m.want.SHA, "(absent)"), duOr(m.got.SHA, "(absent)"))
		duDumpFamily(t, dir, name, m.lane, m.family, fams[m.lane][m.family])
	}
	fmt.Fprintf(&b, "  produced rows of the differing families: %s\n", filepath.Join(dir, name))
	if *defaultUnchangedAgainst != "" {
		b.WriteString(duAgainst(t, *defaultUnchangedAgainst, name, mism, fams))
	} else {
		b.WriteString("  rerun with -default-unchanged-against=<known-good git revision> for a row diff\n")
	}
	t.Error(b.String())
}

func duOr(s, alt string) string {
	if s == "" {
		return alt
	}
	return s
}

// duFamilyFile is the dump file for one family: <dir>/<blueprint>/<lane>/<sanitised family>-<hash>.rows.
func duFamilyFile(dir, name, lane, family string) string {
	safe := []byte(family)
	for i, c := range safe {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '.' || c == '_' || c == '-' || c == '=') {
			safe[i] = '_'
		}
	}
	if len(safe) > 100 {
		safe = safe[:100]
	}
	sum := sha256.Sum256([]byte(family))
	return filepath.Join(dir, name, lane, string(safe)+"-"+hex.EncodeToString(sum[:4])+".rows")
}

func duDumpFamily(t *testing.T, dir, name, lane, family string, rows []string) {
	t.Helper()
	path := duFamilyFile(dir, name, lane, family)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	var b strings.Builder
	for _, r := range rows {
		b.WriteString(r)
		b.WriteByte('\n')
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
}

// duDumpOnly serves -default-unchanged-dump-families: it writes the named families' rows (a file of
// "<lane>\t<family>" lines, or the word "all") under -default-unchanged-dump and compares nothing.
func duDumpOnly(t *testing.T, name string, fams duFamilies) {
	t.Helper()
	if *defaultUnchangedDump == "" {
		t.Fatal("-default-unchanged-dump-families needs -default-unchanged-dump=<dir>")
	}
	if *defaultUnchangedDumpFamilies == "all" {
		for lane, byFamily := range fams {
			for family, rows := range byFamily {
				duDumpFamily(t, *defaultUnchangedDump, name, lane, family, rows)
			}
		}
		return
	}
	data, err := os.ReadFile(*defaultUnchangedDumpFamilies)
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		lane, family, ok := strings.Cut(line, "\t")
		if !ok {
			t.Fatalf("dump families: malformed line %q", line)
		}
		duDumpFamily(t, *defaultUnchangedDump, name, lane, family, fams[lane][family])
	}
}

var (
	duArchiveMu   sync.Mutex
	duArchiveDirs = map[string]string{}
)

// duArchive extracts `git archive <rev>` of this repository into a temporary directory once per
// revision and installs this test file in it, so the old tree runs today's guard.
func duArchive(rev string) (string, error) {
	duArchiveMu.Lock()
	defer duArchiveMu.Unlock()
	if dir, ok := duArchiveDirs[rev]; ok {
		return dir, nil
	}
	top, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return "", fmt.Errorf("git rev-parse --show-toplevel: %w", err)
	}
	dir, err := os.MkdirTemp("", "default-unchanged-rev-")
	if err != nil {
		return "", err
	}
	archive := exec.Command("git", "-C", strings.TrimSpace(string(top)), "archive", "--format=tar", rev)
	untar := exec.Command("tar", "-x", "-C", dir)
	pipe, err := archive.StdoutPipe()
	if err != nil {
		return "", err
	}
	untar.Stdin = pipe
	var stderr bytes.Buffer
	archive.Stderr = &stderr
	if err := untar.Start(); err != nil {
		return "", err
	}
	if err := archive.Run(); err != nil {
		_ = untar.Wait()
		return "", fmt.Errorf("git archive %s: %v %s", rev, err, stderr.String())
	}
	if err := untar.Wait(); err != nil {
		return "", fmt.Errorf("tar: %w", err)
	}
	self, err := os.ReadFile("default_unchanged_test.go")
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(filepath.Join(dir, "internal", "integration", "default_unchanged_test.go"), self, 0o644); err != nil {
		return "", err
	}
	duArchiveDirs[rev] = dir
	return dir, nil
}

const (
	duDiffFamilies = 5  // families diffed per blueprint
	duDiffLines    = 12 // diff lines printed per family
)

// duAgainst runs this guard at rev for one blueprint in dump mode and diffs the differing families.
func duAgainst(t *testing.T, rev, name string, mism []duMismatch, fams duFamilies) string {
	t.Helper()
	root, err := duArchive(rev)
	if err != nil {
		return fmt.Sprintf("  -default-unchanged-against: %v\n", err)
	}
	work, err := os.MkdirTemp("", "default-unchanged-against-")
	if err != nil {
		return fmt.Sprintf("  -default-unchanged-against: %v\n", err)
	}
	var list strings.Builder
	for _, m := range mism {
		fmt.Fprintf(&list, "%s\t%s\n", m.lane, m.family)
	}
	listPath := filepath.Join(work, "families.txt")
	if err := os.WriteFile(listPath, []byte(list.String()), 0o644); err != nil {
		return fmt.Sprintf("  -default-unchanged-against: %v\n", err)
	}
	oldDir := filepath.Join(work, "rows")
	cmd := exec.Command("go", "test", "./internal/integration", "-count=1",
		"-run", "^TestShippedBlueprintsDefaultUnchanged$/^"+regexp.QuoteMeta(name)+"$",
		"-default-unchanged-dump="+oldDir, "-default-unchanged-dump-families="+listPath)
	cmd.Dir = root
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Sprintf("  -default-unchanged-against: the guard at %s failed: %v\n%s\n", rev, err, duTrunc(string(out)))
	}
	var b strings.Builder
	fmt.Fprintf(&b, "  row diff against %s (tree %s, rows %s):\n", rev, root, oldDir)
	for i, m := range mism {
		if i == duDiffFamilies {
			fmt.Fprintf(&b, "  ... %d more families not diffed\n", len(mism)-i)
			break
		}
		var old []string
		if data, err := os.ReadFile(duFamilyFile(oldDir, name, m.lane, m.family)); err == nil && len(data) > 0 {
			old = strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
		}
		b.WriteString(duRowDiff(t, rev, m.lane, m.family, old, fams[m.lane][m.family]))
	}
	return b.String()
}

// duRowDiff prints the rows only at rev (-) and only in this tree (+). Sorted rows of one family
// mostly align, so the first pairs are printed together with the fields that changed; the rest
// is capped.
func duRowDiff(t *testing.T, rev, lane, family string, old, cur []string) string {
	t.Helper()
	var removed, added []string
	i, j := 0, 0
	for i < len(old) || j < len(cur) {
		switch {
		case j == len(cur) || (i < len(old) && old[i] < cur[j]):
			removed = append(removed, old[i])
			i++
		case i == len(old) || cur[j] < old[i]:
			added = append(added, cur[j])
			j++
		default:
			i++
			j++
		}
	}
	short := func(r string) string {
		if len(r) > 240 {
			return r[:240] + "..."
		}
		return r
	}
	var b strings.Builder
	fmt.Fprintf(&b, "  --- %s %s %q: %d rows, %d only here\n  +++ working tree %s %q: %d rows, %d only here\n",
		rev, lane, family, len(old), len(removed), lane, family, len(cur), len(added))
	printed, rows, k := 0, 0, 0
	for ; k < len(removed) && k < len(added) && printed+3 <= duDiffLines; k++ {
		fmt.Fprintf(&b, "  - %s\n  + %s\n    changed: %s\n", short(removed[k]), short(added[k]), duLeafDiff(t, removed[k], added[k]))
		printed, rows = printed+3, rows+2
	}
	unpaired := func(sign string, rest []string) {
		for _, r := range rest {
			if printed >= duDiffLines {
				return
			}
			fmt.Fprintf(&b, "  %s %s\n", sign, short(r))
			printed, rows = printed+1, rows+1
		}
	}
	unpaired("-", removed[k:])
	unpaired("+", added[k:])
	if omitted := len(removed) + len(added) - rows; omitted > 0 {
		fmt.Fprintf(&b, "  ... %d more -/+ rows; full rows under the dump directories above\n", omitted)
	}
	return b.String()
}

// duLeafDiff names the leaf paths whose values differ between two rows.
func duLeafDiff(t *testing.T, a, b string) string {
	t.Helper()
	la, lb := map[string]string{}, map[string]string{}
	duLeaves(duDecode(t, []byte(a)), "", la)
	duLeaves(duDecode(t, []byte(b)), "", lb)
	var diffs []string
	for k, v := range la {
		if w, ok := lb[k]; !ok {
			diffs = append(diffs, fmt.Sprintf("%s: %s -> (absent)", k, v))
		} else if w != v {
			diffs = append(diffs, fmt.Sprintf("%s: %s -> %s", k, v, w))
		}
	}
	for k, v := range lb {
		if _, ok := la[k]; !ok {
			diffs = append(diffs, fmt.Sprintf("%s: (absent) -> %s", k, v))
		}
	}
	sort.Strings(diffs)
	if len(diffs) > 8 {
		diffs = append(diffs[:8], fmt.Sprintf("... %d more", len(diffs)-8))
	}
	return duTrunc(strings.Join(diffs, "; "))
}

func duLeaves(v any, path string, out map[string]string) {
	switch x := v.(type) {
	case map[string]any:
		for k, e := range x {
			duLeaves(e, path+"."+k, out)
		}
	case []any:
		for i, e := range x {
			duLeaves(e, fmt.Sprintf("%s[%d]", path, i), out)
		}
	default:
		b, _ := json.Marshal(x)
		out[path] = string(b)
	}
}

func duTrunc(s string) string {
	const limit = 600
	if len(s) > limit {
		return s[:limit] + fmt.Sprintf("... (%d bytes)", len(s))
	}
	return s
}
