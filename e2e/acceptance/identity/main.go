// SPDX-License-Identifier: AGPL-3.0-only

// identity verifies runtime blueprint identity, delivery readiness, and inventory-backed metric
// queryability. It deliberately obtains names only from the running control schema: filenames are
// an implementation detail and cannot prove a selectable runtime identity.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/rknightion/synthkit/internal/control"
	"github.com/rknightion/synthkit/internal/pushstatus"
)

type stringsFlag []string

func (f *stringsFlag) String() string { return strings.Join(*f, ",") }
func (f *stringsFlag) Set(value string) error {
	if strings.TrimSpace(value) == "" {
		return errors.New("blueprint name must not be empty")
	}
	*f = append(*f, value)
	return nil
}

type config struct {
	controlURL       string
	controlToken     string
	promURL          string
	promUser         string
	promToken        string
	expectedNames    []string
	expectedCount    int
	emissionInterval time.Duration
	deliveryDeadline time.Duration
}

type verdict string

const (
	verdictHealthy     verdict = "healthy"
	verdictSetup       verdict = "setup"
	verdictLaneFailure verdict = "lane_failure"
)

// row is one actionable result for one runtime identity. A failed check always identifies the
// blocked lane (or runtime-inventory boundary) rather than presenting no data as success.
type row struct {
	Blueprint      string  `json:"blueprint"`
	Verdict        verdict `json:"verdict"`
	PreventingLane string  `json:"preventing_lane,omitempty"`
	Detail         string  `json:"detail"`
	MetricChecks   int     `json:"metric_checks"`
}

type report struct {
	ExpectedCount int    `json:"expected_count,omitempty"`
	RuntimeCount  int    `json:"runtime_count"`
	Waited        string `json:"waited"`
	Rows          []row  `json:"rows"`
}

const metricQueryConcurrency = 16

type metricCheck struct {
	metric   string
	kind     string
	name     string
	identity *control.QueryIdentity
}

type metricResult struct {
	queryable bool
	err       error
}

func main() {
	cfg := config{expectedCount: -1, emissionInterval: time.Minute, deliveryDeadline: 5 * time.Second}
	var names stringsFlag
	flag.StringVar(&cfg.controlURL, "control-url", "http://127.0.0.1:8088", "control-plane base URL")
	flag.StringVar(&cfg.controlToken, "control-token", os.Getenv("CONTROL_TOKEN"), "control-plane Basic-auth password")
	flag.StringVar(&cfg.promURL, "prom-url", "", "Prometheus-compatible query base URL")
	flag.StringVar(&cfg.promUser, "prom-user", os.Getenv("GC_PROM_USER"), "Prometheus Basic-auth user")
	flag.StringVar(&cfg.promToken, "prom-token", os.Getenv("GC_TOKEN"), "Prometheus Basic-auth password")
	flag.Var(&names, "expected-blueprint", "exact runtime identity expected in this deployment (repeatable)")
	flag.IntVar(&cfg.expectedCount, "expected-count", -1, "expected loaded runtime-name count; -1 derives count from the live schema")
	flag.DurationVar(&cfg.emissionInterval, "emission-interval", time.Minute, "declared emission interval used for the bounded two-interval wait")
	flag.DurationVar(&cfg.deliveryDeadline, "delivery-deadline", 5*time.Second, "delivery deadline added after two emission intervals")
	flag.Parse()
	cfg.expectedNames = names

	r, err := run(context.Background(), cfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	if err := json.NewEncoder(os.Stdout).Encode(r); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	for _, row := range r.Rows {
		if row.Verdict == verdictLaneFailure {
			os.Exit(1)
		}
	}
}

func run(ctx context.Context, cfg config) (report, error) {
	if cfg.emissionInterval < 0 || cfg.deliveryDeadline < 0 {
		return report{}, errors.New("emission interval and delivery deadline must not be negative")
	}
	if err := uniqueExpectedNames(cfg.expectedNames); err != nil {
		return report{}, err
	}
	c := &http.Client{Timeout: 30 * time.Second}
	schema, err := getSchema(ctx, c, cfg)
	if err != nil {
		return report{}, err
	}
	names, duplicate := runtimeNames(schema.Blueprints)
	expectedCount := cfg.expectedCount
	if expectedCount < 0 {
		expectedCount = len(names)
	}
	r := report{ExpectedCount: expectedCount, RuntimeCount: len(names), Waited: (2*cfg.emissionInterval + cfg.deliveryDeadline).String()}
	if err := wait(ctx, 2*cfg.emissionInterval+cfg.deliveryDeadline); err != nil {
		return report{}, err
	}
	var inventory control.InventoryReport
	if err := getControl(ctx, c, cfg, "/control/inventory", &inventory); err != nil {
		return report{}, fmt.Errorf("GET /control/inventory: %w", err)
	}
	var status control.StatusReport
	if err := getControl(ctx, c, cfg, "/control/status", &status); err != nil {
		return report{}, fmt.Errorf("GET /control/status: %w", err)
	}

	all := unionNames(names, cfg.expectedNames)
	for _, name := range all {
		r.Rows = append(r.Rows, evaluate(ctx, c, cfg, name, contains(names, name), duplicate[name], schema, inventory, status, expectedCount, len(names) == expectedCount))
	}
	return r, nil
}

func evaluate(ctx context.Context, c *http.Client, cfg config, name string, loaded, duplicate bool, schema control.Schema, inventory control.InventoryReport, status control.StatusReport, expectedCount int, countMatches bool) row {
	r := row{Blueprint: name, Verdict: verdictLaneFailure}
	if duplicate {
		r.PreventingLane, r.Detail = "runtime_identity", "runtime schema returned this selectable identity more than once"
		return r
	}
	if !loaded {
		r.PreventingLane, r.Detail = "runtime_identity", "expected runtime identity was absent from the loaded schema"
		return r
	}
	if !countMatches {
		r.PreventingLane = "runtime_identity"
		r.Detail = fmt.Sprintf("loaded runtime schema has %d names, expected %d", len(schema.Blueprints), expectedCount)
		return r
	}
	readiness := status.Readiness
	if readiness == nil {
		r.PreventingLane, r.Detail = "control_status", "authenticated status omitted readiness"
		return r
	}
	if readiness.SetupRequired {
		r.Verdict, r.Detail = verdictSetup, "runtime reports intentional setup mode; no selected blueprint is emitting"
		return r
	}
	if !readiness.LiveReady {
		r.PreventingLane, r.Detail = blockingLane(readiness.Lanes, readiness.Reasons)
		return r
	}
	bp, ok := inventoryBlueprint(inventory, name)
	if !ok {
		r.PreventingLane, r.Detail = "inventory", "loaded runtime identity has no live inventory entry after the bounded wait"
		return r
	}
	checks := make([]metricCheck, 0)
	for _, construct := range bp.Constructs {
		for _, metric := range construct.MetricNames {
			identity := construct.Identity
			if perFamily, ok := construct.MetricIdentities[metric]; ok {
				identity = perFamily
			}
			checks = append(checks, metricCheck{metric: metric, kind: construct.Kind, name: construct.Name, identity: identity})
		}
	}
	r.MetricChecks = len(checks)
	if len(checks) == 0 {
		r.PreventingLane, r.Detail = "inventory", "loaded runtime identity has zero synthetic metric families in live inventory after the bounded wait"
		return r
	}
	if cfg.promURL == "" {
		r.PreventingLane, r.Detail = "prometheus", "metric inventory requires -prom-url for live queryability proof"
		return r
	}
	results := queryMetrics(ctx, c, cfg, checks)
	for i, result := range results {
		check := checks[i]
		if result.err != nil {
			r.PreventingLane, r.Detail = "prometheus", fmt.Sprintf("query %q for %s/%s: %v", check.metric, check.kind, check.name, result.err)
			return r
		}
		if !result.queryable {
			r.PreventingLane, r.Detail = "prometheus", fmt.Sprintf("metric %q from inventory %s/%s was not queryable", check.metric, check.kind, check.name)
			return r
		}
	}
	r.Verdict, r.Detail = verdictHealthy, "live readiness is green and every inventory-derived metric family was queryable"
	return r
}

// queryMetrics bounds Grafana request concurrency while retaining the inventory's stable order for
// verdict selection. Every family is queried; concurrency changes only wall time, not the assertion.
func queryMetrics(ctx context.Context, c *http.Client, cfg config, checks []metricCheck) []metricResult {
	results := make([]metricResult, len(checks))
	jobs := make(chan int)
	workers := min(metricQueryConcurrency, len(checks))
	var wg sync.WaitGroup
	wg.Add(workers)
	for range workers {
		go func() {
			defer wg.Done()
			for i := range jobs {
				results[i].queryable, results[i].err = queryMetric(ctx, c, cfg, checks[i].metric, checks[i].identity)
			}
		}()
	}
	for i := range checks {
		jobs <- i
	}
	close(jobs)
	wg.Wait()
	return results
}

func blockingLane(lanes []pushstatus.LaneStatus, reasons []string) (string, string) {
	for _, lane := range lanes {
		if !lane.LiveReady && !lane.Disabled {
			return lane.Name, fmt.Sprintf("delivery lane %q is %s", lane.Name, lane.State)
		}
	}
	if len(reasons) > 0 {
		return "control_readiness", strings.Join(reasons, "; ")
	}
	return "control_readiness", "runtime is not live-ready without a named lane status"
}

func getSchema(ctx context.Context, c *http.Client, cfg config) (control.Schema, error) {
	var schema control.Schema
	if err := getControl(ctx, c, cfg, "/control/schema", &schema); err != nil {
		return control.Schema{}, fmt.Errorf("GET /control/schema: %w", err)
	}
	return schema, nil
}

func getControl(ctx context.Context, c *http.Client, cfg config, path string, out any) error {
	return requestJSON(ctx, c, strings.TrimRight(cfg.controlURL, "/")+path, cfg.controlToken, "", "", out)
}

func queryMetric(ctx context.Context, c *http.Client, cfg config, metric string, identity *control.QueryIdentity) (bool, error) {
	u, err := url.Parse(strings.TrimRight(cfg.promURL, "/") + "/api/v1/query")
	if err != nil {
		return false, err
	}
	q := u.Query()
	q.Set("query", metricQuery(metric, identity))
	u.RawQuery = q.Encode()
	var response struct {
		Status string `json:"status"`
		Error  string `json:"error"`
		Data   struct {
			Result []json.RawMessage `json:"result"`
		} `json:"data"`
	}
	if err := requestJSON(ctx, c, u.String(), "", cfg.promUser, cfg.promToken, &response); err != nil {
		return false, err
	}
	if response.Status != "success" {
		return false, fmt.Errorf("query status %q: %s", response.Status, response.Error)
	}
	return len(response.Data.Result) > 0, nil
}

func metricQuery(metric string, identity *control.QueryIdentity) string {
	labels := map[string]string{"__name__": metric}
	if identity != nil {
		for key, value := range identity.Labels {
			labels[key] = value
		}
	}
	keys := make([]string, 0, len(labels))
	for key := range labels {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, key+`="`+queryLabelValue(labels[key])+`"`)
	}
	return "{" + strings.Join(parts, ",") + "}"
}

func requestJSON(ctx context.Context, c *http.Client, rawURL, controlToken, user, token string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return err
	}
	if controlToken != "" {
		req.SetBasicAuth("control", controlToken)
	} else if user != "" || token != "" {
		req.SetBasicAuth(user, token)
	}
	resp, err := c.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func runtimeNames(in []string) ([]string, map[string]bool) {
	seen, duplicate := make(map[string]bool, len(in)), map[string]bool{}
	for _, name := range in {
		if seen[name] {
			duplicate[name] = true
		}
		seen[name] = true
	}
	out := make([]string, 0, len(seen))
	for name := range seen {
		out = append(out, name)
	}
	sort.Strings(out)
	return out, duplicate
}

func uniqueExpectedNames(names []string) error {
	seen := map[string]bool{}
	for _, name := range names {
		if seen[name] {
			return fmt.Errorf("expected runtime identity %q was repeated", name)
		}
		seen[name] = true
	}
	return nil
}

func unionNames(a, b []string) []string {
	seen := map[string]bool{}
	for _, names := range [][]string{a, b} {
		for _, name := range names {
			seen[name] = true
		}
	}
	out := make([]string, 0, len(seen))
	for name := range seen {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

func contains(names []string, want string) bool {
	return sort.SearchStrings(names, want) < len(names) && names[sort.SearchStrings(names, want)] == want
}

func inventoryBlueprint(inventory control.InventoryReport, name string) (control.BlueprintInventory, bool) {
	for _, bp := range inventory.Blueprints {
		if bp.Blueprint == name {
			return bp, true
		}
	}
	return control.BlueprintInventory{}, false
}

func queryLabelValue(value string) string {
	replacer := strings.NewReplacer("\\", "\\\\", "\"", "\\\"", "\n", "\\n")
	return replacer.Replace(value)
}

func wait(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
