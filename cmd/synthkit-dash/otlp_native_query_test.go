// SPDX-License-Identifier: AGPL-3.0-only

//go:build integration

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	metricspb "go.opentelemetry.io/proto/otlp/metrics/v1"
	"gopkg.in/yaml.v3"

	"github.com/rknightion/synthkit/e2e/receiver"
	"github.com/rknightion/synthkit/internal/blueprint"
	"github.com/rknightion/synthkit/internal/dashgen"
	"github.com/rknightion/synthkit/internal/runner"
	"github.com/rknightion/synthkit/internal/sink/loki"
	"github.com/rknightion/synthkit/internal/sink/otlp"
	"github.com/rknightion/synthkit/internal/sink/promrw"
)

// This opt-in proof requires a real Prometheus query engine (promtool), never a mocked
// evaluator. It decodes actual runner HTTP output, then projects only the native HTTP
// instruments using the retained collector contract in signals/otlp-metrics.md. This is
// sourced receiver projection plus query execution, not live gateway or browser evidence.
func TestOTLPNativeQueriesAgainstEmittedHTTPMetrics(t *testing.T) {
	promtool, err := exec.LookPath("promtool")
	if err != nil {
		t.Fatal("integration query proof requires promtool on PATH")
	}
	raw, err := os.ReadFile(filepath.Join(generated(t, otlpNativeBlueprint), "otlp-native-http.json"))
	if err != nil {
		t.Fatal(err)
	}
	inventory, err := dashgen.InventoryFromDashboard(raw)
	if err != nil {
		t.Fatal(err)
	}
	fixture := captureNativeHTTP(t)
	tests := make([]any, 0)
	for _, panel := range inventory.Panels {
		for _, query := range panel.Queries {
			tests = append(tests, map[string]any{
				"expr": query.Expression, "eval_time": "11m", "exp_samples": fixture.expected(t, panel.ID, query.RefID),
			})
		}
	}
	if len(tests) == 0 {
		t.Fatal("generated native template has no queries to execute")
	}
	// A second deployment shares both service names but has different environment and
	// instance identity, high errors and slower requests. Every generated query must
	// exclude its points before aggregation, using the actual resource target_info.
	input := append(fixture.inputSeries(), fixture.foreignSeries(t)...)
	output, err := runPromtoolFixture(t, promtool, input, tests)
	if err != nil {
		t.Fatalf("native panel expressions failed against actual emitted points: %v\n%s", err, output)
	}
	observations := nativeObservations(inventory, "rendered", "Real promtool evaluated emitted HTTP points: "+output)
	assertNativeVerification(t, inventory, observations, "rendered", "")

	// The same real expressions on an empty datasource must remain empty rather than
	// fabricating healthy zero. Promtool validates an empty result for every new query.
	for _, test := range tests {
		test.(map[string]any)["exp_samples"] = []any{}
	}
	emptyOutput, err := runPromtoolFixture(t, promtool, nil, tests)
	if err != nil {
		t.Fatalf("native query unexpectedly produced data from empty emission: %v\n%s", err, emptyOutput)
	}
	assertNativeVerification(t, inventory, nativeObservations(inventory, "empty", "Real promtool confirmed empty input: "+emptyOutput), "empty", "emission_gap")

	// Ordinary application points still exist here. Without their matching resource
	// inventory, no deployment attribution is possible and every query stays empty.
	var withoutTargets []any
	for _, series := range input {
		if !strings.HasPrefix(series.(map[string]any)["series"].(string), "target_info{") {
			withoutTargets = append(withoutTargets, series)
		}
	}
	missingTargetsOutput, err := runPromtoolFixture(t, promtool, withoutTargets, tests)
	if err != nil {
		t.Fatalf("native query selected ordinary points without target_info: %v\n%s", err, missingTargetsOutput)
	}
	assertNativeVerification(t, inventory, nativeObservations(inventory, "empty", "Real promtool confirmed missing target_info: "+missingTargetsOutput), "empty", "emission_gap")

	// Obtain a real parser refusal, then pass that response through the public verifier.
	errorOutput, err := runPromtoolFixture(t, promtool, nil, []any{map[string]any{
		"expr": "sum(", "eval_time": "11m", "exp_samples": []any{},
	}})
	if err == nil || !strings.Contains(errorOutput, "parse error") {
		t.Fatalf("malformed expression did not reach real parser: %v\n%s", err, errorOutput)
	}
	assertNativeVerification(t, inventory, nativeObservations(inventory, "errored", errorOutput), "errored", "query_defect")

	// Datasource resolution is the process edge. A local Grafana-shaped 404 fixture
	// supplies actual HTTP evidence, without claiming a deployed datasource exists.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"message":"Data source not found"}`))
	}))
	defer srv.Close()
	client := &http.Client{Timeout: time.Second}
	response, err := client.Get(srv.URL + "/api/datasources/name/missing")
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	var missing struct {
		Message string `json:"message"`
	}
	if err := json.NewDecoder(response.Body).Decode(&missing); err != nil || response.StatusCode != http.StatusNotFound {
		t.Fatalf("datasource-resolution fixture: status=%d error=%v", response.StatusCode, err)
	}
	assertNativeVerification(t, inventory, nativeObservations(inventory, "errored", fmt.Sprintf("HTTP %d: %s", response.StatusCode, missing.Message)), "errored", "missing_datasource")
}

type nativeHTTPFixture struct {
	series map[string][]string
	totals map[int]map[string]*nativeHTTPTotals
}

type nativeHTTPTotals struct {
	count   uint64
	buckets map[float64]uint64 // cumulative counts, keyed by the actual seconds bound
	active  float64
}

func captureNativeHTTP(t *testing.T) nativeHTTPFixture {
	t.Helper()
	rec := receiver.New()
	srv := httptest.NewServer(rec.Handler())
	defer srv.Close()
	data, err := os.ReadFile(otlpNativeBlueprint)
	if err != nil {
		t.Fatal(err)
	}
	reg := runner.Catalog()
	resolved, err := blueprint.Load(data, reg)
	if err != nil {
		t.Fatal(err)
	}
	if err := blueprint.ValidateSet([]*blueprint.Resolved{resolved}); err != nil {
		t.Fatal(err)
	}
	r := runner.New(runner.Sinks{
		Metrics: promrw.New("", "", "", true, nil), Logs: loki.New("", "", "", true), Traces: otlp.New("", "", "", true),
		OTLPMetrics: otlp.NewMetrics(srv.URL+"/otlp", "", "", false),
	}, reg, runner.Options{})
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := r.JoinQueues(ctx); err != nil {
			t.Errorf("join native fixture senders: %v", err)
		}
	}()
	if err := r.AddBlueprint(resolved); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	start := time.Date(2026, time.June, 17, 12, 0, 0, 0, time.UTC)
	for tick := 0; tick < 12; tick++ {
		if err := r.RunOnce(ctx, start.Add(time.Duration(tick)*time.Minute)); err != nil {
			t.Fatalf("actual native HTTP emission tick %d: %v", tick, err)
		}
	}
	f := nativeHTTPFixture{series: map[string][]string{}, totals: map[int]map[string]*nativeHTTPTotals{}}
	for _, resource := range rec.OTLPMetrics() {
		attrs := nativeAttrs(resource.GetResource().GetAttributes())
		service := attrs["service.name"]
		if service != "otlp-api-enriched" && service != "otlp-api-naked" {
			continue
		}
		// The August same-source dual-ingest capture supersedes the broader June
		// promotion list: ordinary application series carry only these five resource
		// attributes plus standard job/instance. Full resource identity stays on
		// target_info; deployment.environment.name is deliberately absent here.
		resourceLabels := map[string]string{}
		for _, key := range []string{"service.name", "service.namespace", "service.instance.id", "deployment.environment.name"} {
			value := attrs[key]
			if value == "" {
				t.Fatalf("native HTTP resource lacks %s", key)
			}
		}
		for _, key := range []string{"service.name", "service.version", "k8s.deployment.name", "k8s.namespace.name", "k8s.pod.name"} {
			if value, exists := attrs[key]; exists {
				resourceLabels[strings.ReplaceAll(key, ".", "_")] = value
			}
		}
		resourceLabels["job"] = attrs["service.namespace"] + "/" + service
		resourceLabels["instance"] = attrs["service.instance.id"]
		targetLabels := projectedLabels(nil, resource.GetResource().GetAttributes())
		targetLabels["job"], targetLabels["instance"] = resourceLabels["job"], resourceLabels["instance"]
		for _, scope := range resource.GetScopeMetrics() {
			for _, metric := range scope.GetMetrics() {
				switch metric.GetName() {
				case "http.server.request.duration":
					hist := metric.GetHistogram()
					if hist == nil || metric.GetUnit() != "s" || hist.GetAggregationTemporality() != metricspb.AggregationTemporality_AGGREGATION_TEMPORALITY_CUMULATIVE {
						t.Fatalf("duration source is not the sourced cumulative explicit histogram in seconds: %v", metric)
					}
					for _, point := range hist.GetDataPoints() {
						tick := nativeTick(t, point.GetTimeUnixNano(), start)
						f.add("target_info", targetLabels, tick, 1)
						labels := projectedLabels(resourceLabels, point.GetAttributes())
						if len(point.GetBucketCounts()) != len(point.GetExplicitBounds())+1 {
							t.Fatal("invalid native HTTP histogram bucket shape")
						}
						total := f.at(tick, service)
						total.count += point.GetCount()
						f.add("http_server_request_duration_seconds_count", labels, tick, float64(point.GetCount()))
						f.add("http_server_request_duration_seconds_sum", labels, tick, point.GetSum())
						var cumulative uint64
						for i, count := range point.GetBucketCounts() {
							cumulative += count
							bound := math.Inf(1)
							if i < len(point.GetExplicitBounds()) {
								bound = point.GetExplicitBounds()[i]
							}
							labels["le"] = strconv.FormatFloat(bound, 'g', -1, 64)
							f.add("http_server_request_duration_seconds_bucket", labels, tick, float64(cumulative))
							total.buckets[bound] += cumulative
						}
						if cumulative != point.GetCount() {
							t.Fatal("native HTTP bucket counts do not sum to count")
						}
					}
				case "http.server.active_requests":
					sum := metric.GetSum()
					if sum == nil || sum.GetIsMonotonic() || metric.GetUnit() != "{request}" {
						t.Fatalf("active requests source is not the sourced non-monotonic request sum: %v", metric)
					}
					for _, point := range sum.GetDataPoints() {
						tick := nativeTick(t, point.GetTimeUnixNano(), start)
						f.add("target_info", targetLabels, tick, 1)
						value := point.GetAsDouble()
						f.at(tick, service).active += value
						f.add("http_server_active_requests", projectedLabels(resourceLabels, point.GetAttributes()), tick, value)
					}
				}
			}
		}
	}
	for _, service := range []string{"otlp-api-enriched", "otlp-api-naked"} {
		for _, tick := range []int{7, 11} {
			if f.totals[tick][service] == nil || f.totals[tick][service].count == 0 || f.totals[tick][service].active <= 0 {
				t.Fatalf("native fixture has no complete HTTP emission for %s at tick %d", service, tick)
			}
		}
	}
	return f
}

func nativeAttrs(attrs []*commonpb.KeyValue) map[string]string {
	out := make(map[string]string, len(attrs))
	for _, attr := range attrs {
		switch value := attr.GetValue().GetValue().(type) {
		case *commonpb.AnyValue_StringValue:
			out[attr.GetKey()] = value.StringValue
		case *commonpb.AnyValue_IntValue:
			out[attr.GetKey()] = strconv.FormatInt(value.IntValue, 10)
		}
	}
	return out
}

func projectedLabels(resource map[string]string, attrs []*commonpb.KeyValue) map[string]string {
	out := make(map[string]string, len(resource)+len(attrs))
	for key, value := range resource {
		out[key] = value
	}
	for key, value := range nativeAttrs(attrs) {
		out[strings.ReplaceAll(key, ".", "_")] = value
	}
	return out
}

func nativeTick(t *testing.T, timestamp uint64, start time.Time) int {
	t.Helper()
	tick := int(time.Unix(0, int64(timestamp)).Sub(start) / time.Minute)
	if tick < 0 || tick >= 12 {
		t.Fatalf("native fixture emitted timestamp outside its bounded window: %d", timestamp)
	}
	return tick
}

func (f nativeHTTPFixture) at(tick int, service string) *nativeHTTPTotals {
	if f.totals[tick] == nil {
		f.totals[tick] = map[string]*nativeHTTPTotals{}
	}
	if f.totals[tick][service] == nil {
		f.totals[tick][service] = &nativeHTTPTotals{buckets: map[float64]uint64{}}
	}
	return f.totals[tick][service]
}

func (f nativeHTTPFixture) add(name string, labels map[string]string, tick int, value float64) {
	keys := make([]string, 0, len(labels))
	for key := range labels {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var matchers []string
	for _, key := range keys {
		matchers = append(matchers, key+"="+strconv.Quote(labels[key]))
	}
	identity := name + "{" + strings.Join(matchers, ",") + "}"
	values := f.series[identity]
	if values == nil {
		values = make([]string, 12)
		for i := range values {
			values[i] = "_"
		}
	}
	values[tick] = strconv.FormatFloat(value, 'g', -1, 64)
	f.series[identity] = values
}

func (f nativeHTTPFixture) inputSeries() []any {
	keys := make([]string, 0, len(f.series))
	for key := range f.series {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make([]any, 0, len(keys))
	for _, key := range keys {
		out = append(out, map[string]any{"series": key, "values": strings.Join(f.series[key], " ")})
	}
	return out
}

// foreignSeries keeps the source's metric/label shapes but supplies an explicit
// negative deployment. Its counters have high failures and durations near one
// second, so incorrectly including it changes every panel's expected result.
func (f nativeHTTPFixture) foreignSeries(t *testing.T) []any {
	t.Helper()
	instancePattern := regexp.MustCompile(`instance="([^"]+)"`)
	boundPattern := regexp.MustCompile(`le="([^"]+)"`)
	var out []any
	for _, series := range f.inputSeries() {
		row := series.(map[string]any)
		identity := row["series"].(string)
		matches := instancePattern.FindStringSubmatch(identity)
		if len(matches) != 2 {
			t.Fatalf("projected source series has no standard instance identity: %s", identity)
		}
		identity = strings.ReplaceAll(identity, strconv.Quote(matches[1]), strconv.Quote("foreign-"+matches[1]))
		isTarget := strings.HasPrefix(identity, "target_info{")
		if isTarget {
			if !strings.Contains(identity, `deployment_environment_name="prod"`) {
				t.Fatalf("target_info lacks the actual source environment: %s", identity)
			}
			identity = strings.ReplaceAll(identity, `deployment_environment_name="prod"`, `deployment_environment_name="foreign"`)
		} else if strings.Contains(identity, "deployment_environment") || strings.Contains(identity, "service_namespace") || strings.Contains(identity, "service_instance_id") {
			t.Fatalf("ordinary application series manufactured resource promotion: %s", identity)
		}
		values := make([]string, 12)
		for i := range values {
			value := 1.0
			if !isTarget {
				step := 100.0
				if strings.Contains(identity, `http_response_status_code="500"`) {
					step = 10000
				}
				value = step * float64(i+1)
				switch {
				case strings.HasPrefix(identity, "http_server_active_requests{"):
					value = 1000
				case strings.HasPrefix(identity, "http_server_request_duration_seconds_sum{"):
					value *= .8
				case strings.HasPrefix(identity, "http_server_request_duration_seconds_bucket{"):
					bound := boundPattern.FindStringSubmatch(identity)
					if len(bound) != 2 {
						t.Fatalf("projected bucket has no bound: %s", identity)
					}
					limit, err := strconv.ParseFloat(bound[1], 64)
					if err != nil {
						t.Fatal(err)
					}
					if limit < 1 {
						value = 0
					}
				}
			}
			values[i] = strconv.FormatFloat(value, 'g', -1, 64)
		}
		out = append(out, map[string]any{"series": identity, "values": strings.Join(values, " ")})
	}
	return out
}

func (f nativeHTTPFixture) expected(t *testing.T, panel, refID string) []any {
	t.Helper()
	var out []any
	for _, service := range []string{"otlp-api-enriched", "otlp-api-naked"} {
		b := f.totals[11][service]
		var value float64
		switch panel {
		case "request-rate":
			value = f.windowDelta(t, service, "http_server_request_duration_seconds_count", "") / (4 * 60)
		case "error-ratio":
			value = f.windowDelta(t, service, "http_server_request_duration_seconds_count", `http_response_status_code="500"`) / f.windowDelta(t, service, "http_server_request_duration_seconds_count", "")
		case "active-requests":
			value = b.active
		case "latency":
			quantile := map[string]float64{"A": .5, "B": .95, "C": .99}[refID]
			if quantile == 0 {
				t.Fatalf("unexpected latency query ref %q", refID)
			}
			var bounds []float64
			for bound := range b.buckets {
				bounds = append(bounds, bound)
			}
			sort.Float64s(bounds)
			rank := quantile * f.windowDelta(t, service, "http_server_request_duration_seconds_count", "")
			var previousBound, previousCount float64
			for _, bound := range bounds {
				count := f.windowDelta(t, service, "http_server_request_duration_seconds_bucket", `le=`+strconv.Quote(strconv.FormatFloat(bound, 'g', -1, 64)))
				if count >= rank {
					value = previousBound + (bound-previousBound)*(rank-previousCount)/(count-previousCount)
					break
				}
				previousBound, previousCount = bound, count
			}
		default:
			t.Fatalf("new native HTTP query has no independent source expectation: %s/%s", panel, refID)
		}
		if value < 0 || (value == 0 && panel != "error-ratio") || math.IsInf(value, 0) || math.IsNaN(value) {
			t.Fatalf("source expectation for %s/%s is invalid: %v", panel, service, value)
		}
		out = append(out, map[string]any{"labels": `{service_name=` + strconv.Quote(service) + `}`, "value": value})
	}
	return out
}

// windowDelta reads each source counter independently. A first observation is not an
// increment from an invented zero. This fixture permits a late-created counter only
// when its remaining observations are flat, so its range rate is unambiguously zero;
// a different late-series shape refuses this oracle instead of guessing extrapolation.
func (f nativeHTTPFixture) windowDelta(t *testing.T, service, metric, extraMatcher string) float64 {
	t.Helper()
	var total float64
	for identity, values := range f.series {
		if !strings.HasPrefix(identity, metric+"{") || !strings.Contains(identity, `service_name=`+strconv.Quote(service)) || (extraMatcher != "" && !strings.Contains(identity, extraMatcher)) {
			continue
		}
		first := 7
		for first < 12 && values[first] == "_" {
			first++
		}
		if first == 12 {
			continue
		}
		a, err := strconv.ParseFloat(values[first], 64)
		if err != nil {
			t.Fatal(err)
		}
		b, err := strconv.ParseFloat(values[11], 64)
		if err != nil {
			t.Fatal(err)
		}
		if first != 7 {
			for _, value := range values[first:] {
				if value != values[first] {
					t.Fatalf("late source series needs a different range-rate oracle: %s %v", identity, values)
				}
			}
		} else {
			total += b - a
		}
	}
	return total
}

func runPromtoolFixture(t *testing.T, promtool string, series, expressions []any) (string, error) {
	t.Helper()
	fixture := map[string]any{
		"rule_files": []string{}, "evaluation_interval": "1m", "fuzzy_compare": true,
		"tests": []any{map[string]any{"interval": "1m", "input_series": series, "promql_expr_test": expressions}},
	}
	data, err := yaml.Marshal(fixture)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "queries.yaml")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	output, err := exec.CommandContext(ctx, promtool, "test", "rules", path).CombinedOutput()
	return string(output), err
}

func nativeObservations(inventory dashgen.PanelInventory, state, evidence string) dashgen.ObservationFile {
	var file dashgen.ObservationFile
	for _, panel := range inventory.Panels {
		for _, query := range panel.Queries {
			file.Observations = append(file.Observations, dashgen.Observation{
				Dashboard: panel.Dashboard, PanelID: panel.ID, RefID: query.RefID, State: state, Evidence: evidence,
			})
		}
	}
	return file
}

func assertNativeVerification(t *testing.T, inventory dashgen.PanelInventory, observations dashgen.ObservationFile, disposition, classification string) {
	t.Helper()
	dir := t.TempDir()
	paths := []string{filepath.Join(dir, "native-panel-inventory.json"), filepath.Join(dir, "observations.json")}
	for i, value := range []any{inventory, observations} {
		data, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(paths[i], data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	result := runCLI(t, "-verify-inventory", paths[0], "-observations", paths[1])
	if result.code != 0 {
		t.Fatalf("public verification refused complete template observations: %s%s", result.stdout, result.stderr)
	}
	var report dashgen.Report
	if err := json.Unmarshal([]byte(result.stdout), &report); err != nil {
		t.Fatal(err)
	}
	queries := 0
	for _, panel := range report.Panels {
		if len(panel.Queries) == 0 {
			continue
		}
		if panel.Disposition != disposition || panel.Classification != classification {
			t.Errorf("public verifier classified %s as %s/%s, want %s/%s", panel.PanelID, panel.Disposition, panel.Classification, disposition, classification)
		}
		queries += len(panel.Queries)
	}
	if queries != len(observations.Observations) {
		t.Errorf("verifier covered %d queries, want %d", queries, len(observations.Observations))
	}
}
