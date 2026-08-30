// SPDX-License-Identifier: AGPL-3.0-only

// scenarios proves that a schema-declared scenario changes emitted telemetry.
// It is intentionally a client of the public control and query APIs: control state
// is used only to activate/deactivate; the verdict comes from the data-plane query.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/rknightion/synthkit/internal/control"
	"github.com/rknightion/synthkit/internal/failuremode"
)

type config struct {
	controlURL   string
	promURL      string
	lokiURL      string
	controlToken string
	promUser     string
	promToken    string
	lokiUser     string
	lokiToken    string
	siblingEnv   string
	settle       time.Duration
	minChange    float64
}

type disposition struct {
	ID       string   `json:"id"`
	Mode     string   `json:"mode,omitempty"`
	Source   string   `json:"source,omitempty"`
	Verdict  string   `json:"verdict"`
	Detail   string   `json:"detail"`
	Baseline *float64 `json:"baseline,omitempty"`
	Active   *float64 `json:"active,omitempty"`
	Cleared  *float64 `json:"cleared,omitempty"`
	Sibling  *float64 `json:"sibling,omitempty"`
}

type report struct {
	ScenarioCount  int           `json:"scenario_count"`
	AssertionCount int           `json:"assertion_coverage_count"`
	Dispositions   []disposition `json:"dispositions"`
}

func main() {
	cfg := config{}
	flag.StringVar(&cfg.controlURL, "control-url", "http://127.0.0.1:8088", "control-plane base URL")
	flag.StringVar(&cfg.promURL, "prom-url", "", "Prometheus-compatible query base URL")
	flag.StringVar(&cfg.lokiURL, "loki-url", "", "Loki query base URL")
	flag.StringVar(&cfg.controlToken, "control-token", os.Getenv("CONTROL_TOKEN"), "control-plane Basic-auth password")
	flag.StringVar(&cfg.promUser, "prom-user", os.Getenv("GC_PROM_USER"), "Prometheus Basic-auth user")
	flag.StringVar(&cfg.promToken, "prom-token", os.Getenv("GC_TOKEN"), "Prometheus Basic-auth password")
	flag.StringVar(&cfg.lokiUser, "loki-user", os.Getenv("GC_LOKI_USER"), "Loki Basic-auth user")
	flag.StringVar(&cfg.lokiToken, "loki-token", os.Getenv("GC_TOKEN"), "Loki Basic-auth password")
	flag.StringVar(&cfg.siblingEnv, "sibling-env", "", "sibling environment used to prove environment isolation")
	flag.DurationVar(&cfg.settle, "settle", 130*time.Second, "wait after each control mutation for two 60-second emissions and ingestion")
	flag.Float64Var(&cfg.minChange, "min-change", 0.20, "minimum proportional movement required for an active assertion")
	flag.Parse()

	ctx := context.Background()
	r, err := run(ctx, cfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	if err := json.NewEncoder(os.Stdout).Encode(r); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	for _, d := range r.Dispositions {
		if d.Verdict != "pass" {
			os.Exit(1)
		}
	}
}

func run(ctx context.Context, cfg config) (report, error) {
	c := &http.Client{Timeout: 30 * time.Second}
	schema, err := getSchema(ctx, c, cfg)
	if err != nil {
		return report{}, err
	}
	scenarios := append([]control.ScenarioInfo(nil), schema.Scenarios...)
	sort.Slice(scenarios, func(i, j int) bool {
		return scenarios[i].Blueprint+"/"+scenarios[i].Name < scenarios[j].Blueprint+"/"+scenarios[j].Name
	})

	r := report{ScenarioCount: len(scenarios), Dispositions: make([]disposition, 0, len(scenarios))}
	for _, sc := range scenarios {
		d := exercise(ctx, c, cfg, sc)
		if d.Mode != "" {
			r.AssertionCount++
		}
		r.Dispositions = append(r.Dispositions, d)
	}
	return r, nil
}

func exercise(ctx context.Context, c *http.Client, cfg config, sc control.ScenarioInfo) disposition {
	id := sc.Blueprint + "/" + sc.Name
	modes := make([]string, 0, len(sc.Effects))
	targets := make(map[string]string, len(sc.Effects))
	for _, effect := range sc.Effects {
		modes = append(modes, effect.Mode)
		targets[effect.Mode] = effect.Target
	}
	a, ok := failuremode.AssertionFor(modes)
	if !ok {
		d := disposition{ID: id, Verdict: "fail", Detail: "no sourced emitted-data assertion for any declared effect"}
		if err := mutate(ctx, c, cfg, "/control/scenarios/activate", id); err != nil {
			d.Detail += "; activate: " + err.Error()
			return d
		}
		if err := mutate(ctx, c, cfg, "/control/scenarios/deactivate", id); err != nil {
			d.Detail += "; deactivate: " + err.Error()
		}
		return d
	}
	d := disposition{ID: id, Mode: a.Mode, Source: a.Source, Verdict: "fail"}
	if a.SiblingQuery != "" && cfg.siblingEnv == "" {
		d.Detail = "environment-scoped assertion requires -sibling-env"
		return d
	}
	target := targets[a.Mode]
	baseline, err := observe(ctx, c, cfg, a, target, "")
	if err != nil {
		d.Detail = "baseline observation: " + err.Error()
		return d
	}
	d.Baseline = &baseline
	var siblingBaseline float64
	if a.SiblingQuery != "" {
		siblingBaseline, err = observe(ctx, c, cfg, a, target, cfg.siblingEnv)
		if err != nil {
			d.Detail = "sibling baseline observation: " + err.Error()
			return d
		}
	}
	if err := mutate(ctx, c, cfg, "/control/scenarios/activate", id); err != nil {
		d.Detail = "activate: " + err.Error()
		return d
	}
	deactivated := false
	defer func() {
		if !deactivated {
			_ = mutate(context.Background(), c, cfg, "/control/scenarios/deactivate", id)
		}
	}()
	if err := wait(ctx, cfg.settle); err != nil {
		d.Detail = err.Error()
		return d
	}
	active, err := observe(ctx, c, cfg, a, target, "")
	if err != nil {
		d.Detail = "active observation: " + err.Error()
		return d
	}
	d.Active = &active
	if !moved(a.Direction, baseline, active, cfg.minChange) {
		d.Detail = fmt.Sprintf("active value %.6g did not %s from baseline %.6g by %.0f%%", active, a.Direction, baseline, cfg.minChange*100)
		return d
	}
	if a.SiblingQuery != "" {
		sibling, err := observe(ctx, c, cfg, a, target, cfg.siblingEnv)
		if err != nil {
			d.Detail = "sibling active observation: " + err.Error()
			return d
		}
		d.Sibling = &sibling
		if !near(siblingBaseline, sibling, cfg.minChange) {
			d.Detail = fmt.Sprintf("sibling environment moved from %.6g to %.6g", siblingBaseline, sibling)
			return d
		}
	}
	if err := mutate(ctx, c, cfg, "/control/scenarios/deactivate", id); err != nil {
		d.Detail = "deactivate: " + err.Error()
		return d
	}
	deactivated = true
	if err := wait(ctx, cfg.settle); err != nil {
		d.Detail = err.Error()
		return d
	}
	cleared, err := observe(ctx, c, cfg, a, target, "")
	if err != nil {
		d.Detail = "cleared observation: " + err.Error()
		return d
	}
	d.Cleared = &cleared
	if !near(baseline, cleared, cfg.minChange) {
		d.Detail = fmt.Sprintf("deactivation did not return near baseline: baseline %.6g, cleared %.6g", baseline, cleared)
		return d
	}
	d.Verdict = "pass"
	d.Detail = "emitted-data assertion moved while active, sibling stayed baseline when applicable, and deactivation cleared the reversible observation"
	return d
}

func getSchema(ctx context.Context, c *http.Client, cfg config) (control.Schema, error) {
	var schema control.Schema
	if err := requestJSON(ctx, c, cfg.controlURL+"/control/schema", cfg.controlToken, "", "", http.MethodGet, nil, &schema); err != nil {
		return control.Schema{}, fmt.Errorf("GET /control/schema: %w", err)
	}
	return schema, nil
}

func mutate(ctx context.Context, c *http.Client, cfg config, path, id string) error {
	body := map[string]string{"scenario": id}
	return requestJSON(ctx, c, cfg.controlURL+path, cfg.controlToken, "", "", http.MethodPost, body, nil)
}

func observe(ctx context.Context, c *http.Client, cfg config, a failuremode.Assertion, target, sibling string) (float64, error) {
	query := a.Query
	if sibling != "" {
		query = a.SiblingQuery
	}
	query = strings.ReplaceAll(query, "{{target}}", queryLabelValue(target))
	query = strings.ReplaceAll(query, "{{sibling}}", queryLabelValue(sibling))
	base, user, token := cfg.promURL, cfg.promUser, cfg.promToken
	if a.QueryAPI == "loki" {
		base, user, token = cfg.lokiURL, cfg.lokiUser, cfg.lokiToken
	}
	if base == "" {
		return 0, fmt.Errorf("%s query URL is required for %s", a.QueryAPI, a.Mode)
	}
	u, err := url.Parse(strings.TrimRight(base, "/") + "/api/v1/query")
	if err != nil {
		return 0, err
	}
	q := u.Query()
	q.Set("query", query)
	u.RawQuery = q.Encode()
	var out queryResponse
	if err := requestJSON(ctx, c, u.String(), "", user, token, http.MethodGet, nil, &out); err != nil {
		return 0, err
	}
	if out.Status != "success" {
		return 0, fmt.Errorf("query status %q: %s", out.Status, out.Error)
	}
	if len(out.Data.Result) == 0 {
		return 0, errors.New("query returned no series")
	}
	var sum float64
	for _, result := range out.Data.Result {
		if len(result.Value) != 2 {
			return 0, errors.New("query result is not an instant vector")
		}
		text, ok := result.Value[1].(string)
		if !ok {
			return 0, errors.New("query value is not a string")
		}
		v, err := strconv.ParseFloat(text, 64)
		if err != nil {
			return 0, fmt.Errorf("parse query value %q: %w", text, err)
		}
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return 0, fmt.Errorf("query value is non-finite: %q", text)
		}
		sum += v
	}
	return sum, nil
}

type queryResponse struct {
	Status string `json:"status"`
	Error  string `json:"error"`
	Data   struct {
		Result []struct {
			Value []any `json:"value"`
		} `json:"result"`
	} `json:"data"`
}

func requestJSON(ctx context.Context, c *http.Client, rawURL, controlToken, user, token, method string, body any, out any) error {
	var reader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = strings.NewReader(string(b))
	}
	req, err := http.NewRequestWithContext(ctx, method, rawURL, reader)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if controlToken != "" {
		req.SetBasicAuth("control", controlToken)
	}
	if user != "" || token != "" {
		req.SetBasicAuth(user, token)
	}
	resp, err := c.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func moved(direction failuremode.Direction, baseline, active, minChange float64) bool {
	switch direction {
	case failuremode.DirectionIncrease:
		if baseline == 0 {
			return active > 0
		}
		return active >= baseline*(1+minChange)
	case failuremode.DirectionDecrease:
		return active <= baseline*(1-minChange)
	default:
		return false
	}
}

func near(want, got, tolerance float64) bool {
	scale := math.Max(math.Abs(want), 1)
	return math.Abs(want-got) <= scale*tolerance
}

// queryLabelValue keeps schema-declared names inside the quoted PromQL/LogQL
// placeholders. Custom blueprints are still declarations, not executable query
// fragments.
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
