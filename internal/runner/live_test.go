// SPDX-License-Identifier: AGPL-3.0-only

package runner_test

// live_test.go drives POST/DELETE /control/blueprints/live end to end: the real control handler,
// the real catalog, the real loader and a running runner. Fixtures are synthetic shapes only.

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/blueprint"
	"github.com/rknightion/synthkit/internal/bpsource"
	"github.com/rknightion/synthkit/internal/control"
	"github.com/rknightion/synthkit/internal/core/coretest"
	"github.com/rknightion/synthkit/internal/runner"
	"github.com/rknightion/synthkit/internal/sink/otlp"
)

const liveToken = "live-test-token"

// bootYAML is the boot-set blueprint (BLUEPRINT_NAMES selection) every harness starts with.
const bootYAML = `name: base
environments:
  - name: prod
    cloud: {provider: aws, account_id: "100000000001", region: eu-west-1, vpc_id: vpc-base}
    cluster:
      type: eks
      name: base-cluster
      node_groups: [{name: worker, instance_type: m6i.large, desired: 1}]
workloads:
  - type: web_service
    name: base-api
    runs_on: base-cluster
`

// liveYAML renders a live-applied blueprint: bare name, its cluster and its workload.
func liveYAML(name, cluster, workload string) string {
	return fmt.Sprintf(`name: %s
environments:
  - name: prod
    cloud: {provider: aws, account_id: "100000000002", region: eu-west-1, vpc_id: vpc-live}
    cluster:
      type: eks
      name: %s
      node_groups: [{name: worker, instance_type: m6i.large, desired: 1}]
workloads:
  - type: web_service
    name: %s
    runs_on: %s
`, name, cluster, workload, cluster)
}

// liveAdmin supplies only the namespace sanitiser the live routes need (the real bpsource one).
type liveAdmin struct{ control.BlueprintAdmin }

func (liveAdmin) EffectiveBlueprintIdentity(ns, name string) string {
	return bpsource.Namespace(ns, name)
}

type liveHarness struct {
	t       *testing.T
	r       *runner.Runner
	store   *control.Store
	handler http.Handler
	metrics *coretest.MetricCapture
	traces  *traceCounter
}

// traceCounter counts trace resources per stamped blueprint attribute (concurrency-safe).
type traceCounter struct {
	mu     sync.Mutex
	counts map[string]int
}

func (c *traceCounter) Write(_ context.Context, resources []otlp.Resource) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.counts == nil {
		c.counts = map[string]int{}
	}
	for _, res := range resources {
		bp, _ := res.Attrs["blueprint"].(string)
		c.counts[bp]++
	}
	return nil
}

func (c *traceCounter) count(blueprint string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.counts[blueprint]
}

func newLiveHarness(t *testing.T, statePath string, opts runner.Options, boot ...*blueprint.Resolved) *liveHarness {
	t.Helper()
	mc, lc, tc := &coretest.MetricCapture{}, &coretest.LogCapture{}, &traceCounter{}
	r := runner.New(runner.Sinks{Metrics: mc, Logs: lc, Traces: tc}, runner.Catalog(), opts)
	for _, res := range boot {
		if err := r.AddBlueprint(res); err != nil {
			t.Fatalf("AddBlueprint(%s): %v", res.Name, err)
		}
	}
	store := control.NewStore(statePath)
	r.ApplyControl(store.Snapshot())
	h := control.NewHandler(store, r.ApplyControl, liveToken, r).SetBlueprintAdmin(liveAdmin{}).SetInventory(r)
	return &liveHarness{t: t, r: r, store: store, handler: h, metrics: mc, traces: tc}
}

func loadBoot(t *testing.T, yaml, ns string) *blueprint.Resolved {
	t.Helper()
	res, err := blueprint.LoadNamespaced([]byte(yaml), ns, runner.Catalog())
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func (h *liveHarness) do(method, target string, body any, auth bool) (int, string) {
	h.t.Helper()
	var reader *bytes.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			h.t.Fatal(err)
		}
		reader = bytes.NewReader(data)
	} else {
		reader = bytes.NewReader(nil)
	}
	req := httptest.NewRequest(method, target, reader)
	if auth {
		req.Header.Set("Authorization", "Basic "+base64.StdEncoding.EncodeToString([]byte("control:"+liveToken)))
	}
	rec := httptest.NewRecorder()
	h.handler.ServeHTTP(rec, req)
	return rec.Code, rec.Body.String()
}

type liveBody struct {
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
	YAML      string `json:"yaml"`
	Owner     string `json:"owner,omitempty"`
}

func (h *liveHarness) apply(b liveBody) (int, string) {
	return h.do(http.MethodPost, "/control/blueprints/live", b, true)
}

func (h *liveHarness) remove(name string) (int, string) {
	return h.do(http.MethodDelete, "/control/blueprints/live?name="+name, nil, true)
}

func (h *liveHarness) inventory() (control.InventoryReport, map[string]json.RawMessage) {
	h.t.Helper()
	code, body := h.do(http.MethodGet, "/control/inventory", nil, true)
	if code != http.StatusOK {
		h.t.Fatalf("GET /control/inventory = %d %s", code, body)
	}
	var rep control.InventoryReport
	if err := json.Unmarshal([]byte(body), &rep); err != nil {
		h.t.Fatal(err)
	}
	var raw struct {
		Blueprints []json.RawMessage `json:"blueprints"`
	}
	if err := json.Unmarshal([]byte(body), &raw); err != nil {
		h.t.Fatal(err)
	}
	byName := map[string]json.RawMessage{}
	for i, bi := range rep.Blueprints {
		byName[bi.Blueprint] = raw.Blueprints[i]
	}
	return rep, byName
}

func findInventory(rep control.InventoryReport, name string) (control.BlueprintInventory, bool) {
	for _, bi := range rep.Blueprints {
		if bi.Blueprint == name {
			return bi, true
		}
	}
	return control.BlueprintInventory{}, false
}

// lanes renders a blueprint's construct/workload lanes as sorted "kind:name" keys.
func lanes(bi control.BlueprintInventory) []string {
	out := make([]string, 0, len(bi.Constructs))
	for _, c := range bi.Constructs {
		out = append(out, c.Kind+":"+c.Name)
	}
	sort.Strings(out)
	return out
}

var tickBase = time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC)

// tick runs one RunOnce and returns only the metric series it produced.
func (h *liveHarness) tick(at time.Time) []seriesView {
	h.t.Helper()
	before := len(h.metrics.All())
	if err := h.r.RunOnce(context.Background(), at); err != nil {
		h.t.Fatalf("RunOnce: %v", err)
	}
	var out []seriesView
	for _, s := range h.metrics.All()[before:] {
		out = append(out, seriesView{name: s.Name, labels: s.Labels, value: s.Value})
	}
	return out
}

type seriesView struct {
	name   string
	labels map[string]string
	value  float64
}

func countSeries(series []seriesView, label, value string) int {
	n := 0
	for _, s := range series {
		if s.labels[label] == value {
			n++
		}
	}
	return n
}

func (h *liveHarness) persistedLive() []control.LiveBlueprint {
	return h.store.Snapshot().LiveBlueprints
}

const liveID = "synthetic/live-svc"

func liveApplyBody(owner string) liveBody {
	return liveBody{Namespace: "Synthetic", Name: "live-svc", YAML: liveYAML("live-svc", "live-cluster", "live-api"), Owner: owner}
}

// AC1: an authenticated apply emits on the next tick and inventory lists owner + lanes.
func TestLiveApplyEmitsOnNextTickWithOwnerAndLanes(t *testing.T) {
	h := newLiveHarness(t, filepath.Join(t.TempDir(), "state.json"), runner.Options{}, loadBoot(t, bootYAML, ""))
	if got := h.tick(tickBase); countSeries(got, "blueprint", liveID) != 0 || countSeries(got, "cluster", "live-cluster") != 0 {
		t.Fatal("live blueprint emitted before apply")
	}
	_, rawBefore := h.inventory()
	baseBefore := string(rawBefore["base"])

	if code, body := h.do(http.MethodPost, "/control/blueprints/live", liveApplyBody("operator-7"), false); code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated apply = %d %s, want 401", code, body)
	}
	code, body := h.apply(liveApplyBody("operator-7"))
	if code != http.StatusOK {
		t.Fatalf("apply = %d %s", code, body)
	}
	var resp map[string]string
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		t.Fatal(err)
	}
	if want := map[string]string{"status": "applied", "blueprint": liveID, "owner": "operator-7"}; fmt.Sprint(resp) != fmt.Sprint(want) {
		t.Fatalf("apply body = %v, want %v", resp, want)
	}

	got := h.tick(tickBase.Add(time.Minute))
	if countSeries(got, "blueprint", liveID) == 0 {
		t.Fatal("live blueprint-scoped lanes did not emit on the next tick")
	}
	if countSeries(got, "cluster", "live-cluster") == 0 {
		t.Fatal("live substrate-scoped cluster lane did not emit on the next tick")
	}
	if countSeries(got, "blueprint", "base") == 0 {
		t.Fatal("boot blueprint stopped emitting")
	}

	rep, raw := h.inventory()
	bi, ok := findInventory(rep, liveID)
	if !ok || !bi.Live || bi.Owner != "operator-7" {
		t.Fatalf("inventory live entry = %+v (found %v), want live with owner", bi, ok)
	}
	got2 := lanes(bi)
	for _, want := range []string{"web_service:live-api", "k8s_cluster:live-cluster"} {
		if !slices.Contains(got2, want) {
			t.Fatalf("inventory lanes %v missing %s", got2, want)
		}
	}
	if strings.Contains(string(raw["base"]), `"live"`) || strings.Contains(string(raw["base"]), `"owner"`) {
		t.Fatalf("non-live inventory entry gained live/owner keys: %s", raw["base"])
	}
	if !strings.Contains(baseBefore, `"blueprint": "base"`) {
		t.Fatalf("base inventory entry missing before apply: %s", baseBefore)
	}
	if persisted := h.persistedLive(); len(persisted) != 1 || persisted[0].Name != liveID || persisted[0].Owner != "operator-7" || persisted[0].Namespace != "synthetic" {
		t.Fatalf("persisted live set = %+v", persisted)
	}
}

// AC2: removal clears every lane and the cumulative state, leaving other blueprints unchanged.
func TestLiveRemoveClearsLanesAndCumulativeState(t *testing.T) {
	h := newLiveHarness(t, filepath.Join(t.TempDir(), "state.json"), runner.Options{}, loadBoot(t, bootYAML, ""))
	if code, body := h.apply(liveApplyBody("operator-7")); code != http.StatusOK {
		t.Fatalf("apply = %d %s", code, body)
	}
	first := h.tick(tickBase)
	second := h.tick(tickBase.Add(time.Minute))

	// A cumulative series of the live substrate lane that grew between the two ticks.
	firstVal := map[string]float64{}
	for _, s := range first {
		if s.labels["cluster"] == "live-cluster" {
			firstVal[s.name+fmt.Sprint(s.labels)] = s.value
		}
	}
	grown := map[string]float64{}
	for _, s := range second {
		key := s.name + fmt.Sprint(s.labels)
		if v, ok := firstVal[key]; ok && strings.HasSuffix(s.name, "_total") && s.value > v {
			grown[key] = s.value // the removed instance's last cumulative value
		}
	}
	if len(grown) == 0 {
		t.Fatal("no cumulative live substrate counter grew between ticks; fixture proves nothing")
	}

	rep, _ := h.inventory()
	live, ok := findInventory(rep, liveID)
	if !ok {
		t.Fatal("live blueprint missing from inventory before removal")
	}
	liveLanes := lanes(live)
	liveMetricNames := map[string]bool{}
	for _, c := range live.Constructs {
		for _, n := range c.MetricNames {
			liveMetricNames[n] = true
		}
	}
	baseBefore, _ := findInventory(rep, "base")

	code, body := h.remove(liveID)
	if code != http.StatusOK {
		t.Fatalf("remove = %d %s", code, body)
	}
	var resp map[string]string
	if err := json.Unmarshal([]byte(body), &resp); err != nil || resp["status"] != "removed" || resp["blueprint"] != liveID {
		t.Fatalf("remove body = %s (%v)", body, err)
	}

	rep, _ = h.inventory()
	if _, ok := findInventory(rep, liveID); ok {
		t.Fatal("removed blueprint still in inventory")
	}
	baseAfter, _ := findInventory(rep, "base")
	if fmt.Sprint(lanes(baseAfter)) != fmt.Sprint(lanes(baseBefore)) {
		t.Fatalf("base lanes changed: %v -> %v", lanes(baseBefore), lanes(baseAfter))
	}
	for _, bi := range rep.Blueprints {
		for _, c := range bi.Constructs {
			if slices.Contains(liveLanes, c.Kind+":"+c.Name) && bi.Blueprint != "base" {
				t.Fatalf("removed lane %s:%s still listed under %s", c.Kind, c.Name, bi.Blueprint)
			}
		}
	}

	after := h.tick(tickBase.Add(2 * time.Minute))
	if n := countSeries(after, "blueprint", liveID) + countSeries(after, "cluster", "live-cluster"); n != 0 {
		t.Fatalf("removed blueprint still pushed %d series on the next tick", n)
	}
	for _, s := range after {
		if s.labels["blueprint"] == liveID {
			t.Fatalf("removed blueprint lane %s pushed", s.name)
		}
	}
	if countSeries(after, "blueprint", "base") == 0 {
		t.Fatal("boot blueprint stopped emitting after removal")
	}
	if persisted := h.persistedLive(); len(persisted) != 0 {
		t.Fatalf("persisted live set after removal = %+v", persisted)
	}
	if len(liveMetricNames) == 0 {
		t.Fatal("live inventory reported no metric names before removal")
	}

	// Cumulative state went with it: re-applying restarts each counter from its first value.
	if code, body := h.apply(liveApplyBody("operator-7")); code != http.StatusOK {
		t.Fatalf("re-apply = %d %s", code, body)
	}
	// A continued counter can never fall below its last value; a fresh instance's first sample sits
	// near the first-tick value. Seeded noise can lift a few fresh samples, so require nearly all.
	reapplied := h.tick(tickBase)
	compared, below := 0, 0
	for _, s := range reapplied {
		key := s.name + fmt.Sprint(s.labels)
		if last, ok := grown[key]; ok {
			compared++
			if s.value < last {
				below++
			}
		}
	}
	if compared == 0 || float64(below) < 0.9*float64(compared) {
		t.Fatalf("re-applied counters continued from removed state: %d of %d below their last removed value", below, compared)
	}

	if code, _ := h.remove("base"); code != http.StatusNotFound {
		t.Fatalf("removing a boot blueprint = %d, want 404", code)
	}
	if code, _ := h.remove("synthetic/never-applied"); code != http.StatusNotFound {
		t.Fatalf("removing an unknown blueprint = %d, want 404", code)
	}
}

// AC3: every rejection leaves the running set and persisted state unchanged with no partial lanes.
func TestLiveApplyRejectsAtomically(t *testing.T) {
	cases := []struct {
		name     string
		body     liveBody
		code     int
		contains string
	}{
		{"invalid yaml", liveBody{Namespace: "synthetic", Name: "broken", YAML: "name: broken\nenvironments: [\n"}, http.StatusBadRequest, "blueprint"},
		{"unknown field", liveBody{Namespace: "synthetic", Name: "odd", YAML: "name: odd\nnot_a_field: 1\n"}, http.StatusBadRequest, "not_a_field"},
		{"name mismatch", liveBody{Namespace: "synthetic", Name: "other-name", YAML: liveYAML("live-svc", "live-cluster", "live-api")}, http.StatusBadRequest, "synthetic/live-svc"},
		{"name separator", liveBody{Namespace: "synthetic", Name: "live__svc", YAML: liveYAML("live__svc", "live-cluster", "live-api")}, http.StatusBadRequest, "__"},
		{"foreign namespace", liveBody{Namespace: "synthetic", Name: "elsewhere/live-svc", YAML: liveYAML("live-svc", "live-cluster", "live-api")}, http.StatusBadRequest, "synthetic/live-svc"},
		{"owner too long", liveBody{Namespace: "synthetic", Name: "live-svc", YAML: liveYAML("live-svc", "live-cluster", "live-api"), Owner: strings.Repeat("o", 129)}, http.StatusBadRequest, "128"},
		{"owner control char", liveBody{Namespace: "synthetic", Name: "live-svc", YAML: liveYAML("live-svc", "live-cluster", "live-api"), Owner: "op\n1"}, http.StatusBadRequest, "control"},
		{"duplicate live name", liveBody{Namespace: "synthetic", Name: "first", YAML: liveYAML("first", "other-cluster", "other-api")}, http.StatusConflict, `"synthetic/first"`},
		{"substrate cluster claim", liveBody{Namespace: "synthetic", Name: "clash", YAML: liveYAML("clash", "base-cluster", "clash-api")}, http.StatusConflict, `cluster "base-cluster"`},
		{"workload claim", liveBody{Namespace: "synthetic", Name: "clash", YAML: liveYAML("clash", "clash-cluster", "base-api")}, http.StatusConflict, `workload "base-api"`},
		{"live substrate claim", liveBody{Namespace: "synthetic", Name: "clash", YAML: liveYAML("clash", "first-cluster", "clash-api")}, http.StatusConflict, `cluster "first-cluster"`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newLiveHarness(t, filepath.Join(t.TempDir(), "state.json"), runner.Options{}, loadBoot(t, bootYAML, ""))
			if code, body := h.apply(liveBody{Namespace: "synthetic", Name: "first", YAML: liveYAML("first", "first-cluster", "first-api"), Owner: "operator-1"}); code != http.StatusOK {
				t.Fatalf("seed apply = %d %s", code, body)
			}
			before, _ := h.inventory()
			persistedBefore := h.persistedLive()
			code, body := h.apply(tc.body)
			if code != tc.code || !strings.Contains(body, tc.contains) {
				t.Fatalf("apply = %d %q, want %d containing %q", code, body, tc.code, tc.contains)
			}
			after, _ := h.inventory()
			if len(after.Blueprints) != len(before.Blueprints) {
				t.Fatalf("running set changed: %d -> %d blueprints", len(before.Blueprints), len(after.Blueprints))
			}
			for _, bi := range before.Blueprints {
				got, ok := findInventory(after, bi.Blueprint)
				if !ok || fmt.Sprint(lanes(got)) != fmt.Sprint(lanes(bi)) || got.Owner != bi.Owner || got.Live != bi.Live {
					t.Fatalf("blueprint %s changed: %+v -> %+v", bi.Blueprint, bi, got)
				}
			}
			if fmt.Sprint(h.persistedLive()) != fmt.Sprint(persistedBefore) {
				t.Fatalf("persisted live set changed: %+v -> %+v", persistedBefore, h.persistedLive())
			}
			series := h.tick(tickBase)
			for _, rejected := range []string{"synthetic/clash", "synthetic/broken", "synthetic/odd", "synthetic/live-svc"} {
				if n := countSeries(series, "blueprint", rejected); n != 0 {
					t.Fatalf("rejected candidate %s produced %d series", rejected, n)
				}
			}
			for _, cluster := range []string{"clash-cluster", "live-cluster"} {
				if n := countSeries(series, "cluster", cluster); n != 0 {
					t.Fatalf("rejected candidate cluster %s produced %d series", cluster, n)
				}
			}
		})
	}
}

func TestLiveRoutesNotConfiguredWithoutBlueprintAdmin(t *testing.T) {
	r := runner.New(runner.Sinks{}, runner.Catalog(), runner.Options{})
	store := control.NewStore(filepath.Join(t.TempDir(), "state.json"))
	h := &liveHarness{t: t, r: r, store: store, handler: control.NewHandler(store, r.ApplyControl, liveToken, r)}
	if code, body := h.apply(liveApplyBody("")); code != http.StatusNotFound || !strings.Contains(body, "blueprint admin not configured") {
		t.Fatalf("apply without admin = %d %s, want 404", code, body)
	}
	if code, _ := h.remove(liveID); code != http.StatusNotFound {
		t.Fatalf("remove without admin = %d, want 404", code)
	}
}

// AC4: the live set persists in the control snapshot, restores after the boot set, a removal does
// not return, and the boot set (BLUEPRINT_NAMES) takes precedence over a colliding persisted entry.
func TestLiveRestartRoundTripAndBootPrecedence(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "state.json")
	first := newLiveHarness(t, statePath, runner.Options{}, loadBoot(t, bootYAML, ""))
	if code, body := first.apply(liveApplyBody("operator-7")); code != http.StatusOK {
		t.Fatalf("apply = %d %s", code, body)
	}
	if code, body := first.apply(liveBody{Namespace: "synthetic", Name: "gone", YAML: liveYAML("gone", "gone-cluster", "gone-api")}); code != http.StatusOK {
		t.Fatalf("apply second = %d %s", code, body)
	}
	if code, body := first.remove("synthetic/gone"); code != http.StatusOK {
		t.Fatalf("remove = %d %s", code, body)
	}
	data, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"live_blueprints"`) || !strings.Contains(string(data), `"operator-7"`) || strings.Contains(string(data), "synthetic/gone") {
		t.Fatalf("snapshot does not hold exactly the applied live set: %s", data)
	}

	// Restart with the same boot set: the live blueprint returns with its owner; the removed one does not.
	restarted := newLiveHarness(t, statePath, runner.Options{}, loadBoot(t, bootYAML, ""))
	rep, _ := restarted.inventory()
	bi, ok := findInventory(rep, liveID)
	if !ok || !bi.Live || bi.Owner != "operator-7" {
		t.Fatalf("restored entry = %+v (found %v)", bi, ok)
	}
	if _, ok := findInventory(rep, "synthetic/gone"); ok {
		t.Fatal("removed live blueprint returned after restart")
	}
	if n := countSeries(restarted.tick(tickBase), "blueprint", liveID); n == 0 {
		t.Fatal("restored live blueprint did not emit")
	}
	if skips := restarted.r.LiveRestoreSkips(); len(skips) != 0 {
		t.Fatalf("unexpected restore skips: %+v", skips)
	}

	// Restart where BLUEPRINT_NAMES now selects the same identity in the boot set: boot wins.
	sameIdentity := loadBoot(t, liveYAML("live-svc", "live-cluster", "live-api"), "synthetic")
	precedence := newLiveHarness(t, statePath, runner.Options{}, loadBoot(t, bootYAML, ""), sameIdentity)
	rep, _ = precedence.inventory()
	bi, ok = findInventory(rep, liveID)
	if !ok || bi.Live || bi.Owner != "" {
		t.Fatalf("boot-selected identity = %+v (found %v), want the non-live boot blueprint", bi, ok)
	}
	skips := precedence.r.LiveRestoreSkips()
	if len(skips) != 1 || skips[0].Blueprint != liveID || !strings.Contains(skips[0].Reason, "already running") {
		t.Fatalf("restore skips = %+v, want the persisted entry reported", skips)
	}
	// Removing the skipped record clears the persisted entry and leaves the boot blueprint running
	// with its own per-name control state (here: a scaling override) intact.
	precedence.store.Update(func(s *control.State) { s.Scaling[liveID+"/live-api"] = 2 })
	if code, body := precedence.remove(liveID); code != http.StatusOK {
		t.Fatalf("remove skipped record = %d %s", code, body)
	}
	if persisted := precedence.persistedLive(); len(persisted) != 0 {
		t.Fatalf("persisted live set after removing skipped record = %+v", persisted)
	}
	if got := precedence.store.Snapshot().Scaling[liveID+"/live-api"]; got != 2 {
		t.Fatalf("removing the stale live record cleared the boot twin's scaling (got %d)", got)
	}
	if n := countSeries(precedence.tick(tickBase), "blueprint", liveID); n == 0 {
		t.Fatal("boot blueprint stopped after its persisted live twin was removed")
	}
}

func TestLiveRestoreSkipsSubstrateCollisionWithBootSet(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "state.json")
	first := newLiveHarness(t, statePath, runner.Options{}, loadBoot(t, bootYAML, ""))
	if code, body := first.apply(liveApplyBody("operator-7")); code != http.StatusOK {
		t.Fatalf("apply = %d %s", code, body)
	}
	// The next boot set gains a different blueprint that claims the live blueprint's cluster.
	claimant := loadBoot(t, liveYAML("claimant", "live-cluster", "claimant-api"), "")
	restarted := newLiveHarness(t, statePath, runner.Options{}, loadBoot(t, bootYAML, ""), claimant)
	rep, _ := restarted.inventory()
	if _, ok := findInventory(rep, liveID); ok {
		t.Fatal("colliding persisted live blueprint was restored")
	}
	skips := restarted.r.LiveRestoreSkips()
	if len(skips) != 1 || skips[0].Blueprint != liveID || !strings.Contains(skips[0].Reason, `cluster "live-cluster"`) {
		t.Fatalf("restore skips = %+v", skips)
	}
	if persisted := restarted.persistedLive(); len(persisted) != 1 {
		t.Fatalf("skipped entry must stay persisted for the operator, got %+v", persisted)
	}
}

// AC5: apply and remove while the live loop ticks, under -race.
func TestLiveApplyRemoveWhileTicking(t *testing.T) {
	h := newLiveHarness(t, filepath.Join(t.TempDir(), "state.json"), runner.Options{MasterTick: 5 * time.Millisecond, SendDeadline: 5 * time.Millisecond}, loadBoot(t, bootYAML, ""))
	ctx, cancel := context.WithCancel(context.Background())
	runDone := make(chan error, 1)
	go func() { runDone <- h.r.Run(ctx) }()

	var readers sync.WaitGroup
	stopReaders := make(chan struct{})
	readers.Add(1)
	go func() {
		defer readers.Done()
		for {
			select {
			case <-stopReaders:
				return
			default:
			}
			_, _ = h.do(http.MethodGet, "/control/inventory", nil, true)
			_, _ = h.do(http.MethodGet, "/control/schema", nil, true)
			_, _ = h.do(http.MethodPost, "/control/load", map[string]float64{"volume_multiplier": 1.5}, true)
			_ = h.r.DeliveryReadinessLanes()
			_ = h.r.FleetRosterCount()
			// Readiness and self-obs read these counts on every probe while live mutations run.
			_ = h.r.Blueprints()
			_ = h.r.BlueprintCount()
			_ = h.r.ActiveBlueprintCount()
			_ = h.r.LedgerSize()
			_ = h.r.ControlIncidents()
		}
	}()

	emitted := 0
	for i := 0; i < 8; i++ {
		if code, body := h.apply(liveApplyBody(fmt.Sprintf("operator-%d", i))); code != http.StatusOK {
			t.Fatalf("apply %d = %d %s", i, code, body)
		}
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			rep, _ := h.inventory()
			if bi, ok := findInventory(rep, liveID); ok {
				for _, c := range bi.Constructs {
					if len(c.SpanServices) > 0 {
						emitted++
						deadline = time.Time{}
						break
					}
				}
			}
			if !deadline.IsZero() {
				time.Sleep(5 * time.Millisecond)
			}
		}
		if code, body := h.remove(liveID); code != http.StatusOK {
			t.Fatalf("remove %d = %d %s", i, code, body)
		}
	}
	// The removed blueprint's loop was joined before DELETE returned: once already-queued
	// batches land, its trace lane stays silent while the boot blueprint keeps ticking.
	time.Sleep(200 * time.Millisecond)
	settled, baseSettled := h.traces.count(liveID), h.traces.count("base")
	// The boot blueprint must keep producing after the last removal. Its per-tick trace volume is
	// small and bursty, so poll for growth (bounded) rather than sample one fixed window. The
	// baseline is taken after the settle above, so batches queued before removal cannot satisfy it.
	baseGrew := false
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		if h.traces.count("base") > baseSettled {
			baseGrew = true
			break
		}
	}
	if !baseGrew {
		t.Fatal("boot blueprint stopped producing traces after the last live removal")
	}
	time.Sleep(300 * time.Millisecond)
	if got := h.traces.count(liveID); got != settled {
		t.Fatalf("removed blueprint pushed %d trace resources after removal", got-settled)
	}
	close(stopReaders)
	readers.Wait()
	cancel()
	select {
	case <-runDone:
	case <-time.After(10 * time.Second):
		t.Fatal("Run did not return after cancel")
	}
	if emitted != 8 {
		t.Fatalf("live blueprint emitted from its own loop in %d of 8 applies", emitted)
	}
	rep, _ := h.inventory()
	if _, ok := findInventory(rep, liveID); ok {
		t.Fatal("removed blueprint remained after the run")
	}
}

const smLiveYAML = `name: live-probe
environments:
  - name: prod
    weight: 1.0
features:
  synthetic_monitoring:
    enabled: true
    checks:
      - name: live-probe-health
        target: "https://live.example.com/health"
        frequency: 60000
        probe: synthkit-private-emea
        region: EMEA
        latitude: 50.1109
        longitude: 8.6821
`

func seriesWithPrefix(series []seriesView, prefix string) int {
	n := 0
	for _, s := range series {
		if strings.HasPrefix(s.name, prefix) {
			n++
		}
	}
	return n
}

// Without the composition root's LiveAdmit hook a live apply cannot carry Synthetic Monitoring.
func TestLiveApplyRejectsSyntheticMonitoring(t *testing.T) {
	h := newLiveHarness(t, filepath.Join(t.TempDir(), "state.json"), runner.Options{}, loadBoot(t, bootYAML, ""))
	before, _ := h.inventory()
	code, body := h.apply(liveBody{Namespace: "synthetic", Name: "live-probe", YAML: smLiveYAML})
	if code != http.StatusBadRequest || !strings.Contains(body, "synthetic_monitoring") || !strings.Contains(body, "construct") {
		t.Fatalf("SM apply = %d %q, want 400 naming the kind and construct", code, body)
	}
	after, _ := h.inventory()
	if len(after.Blueprints) != len(before.Blueprints) {
		t.Fatalf("running set changed: %d -> %d", len(before.Blueprints), len(after.Blueprints))
	}
	if persisted := h.persistedLive(); len(persisted) != 0 {
		t.Fatalf("persisted live set changed: %+v", persisted)
	}
	if n := seriesWithPrefix(h.tick(tickBase), "probe_"); n != 0 {
		t.Fatalf("rejected SM blueprint emitted %d probe series", n)
	}
}

func TestLiveRestoreSkipsPersistedSyntheticMonitoring(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "state.json")
	control.NewStore(statePath).Update(func(s *control.State) {
		s.LiveBlueprints = []control.LiveBlueprint{{Name: "synthetic/live-probe", Namespace: "synthetic", YAML: smLiveYAML}}
	})
	h := newLiveHarness(t, statePath, runner.Options{}, loadBoot(t, bootYAML, ""))
	rep, _ := h.inventory()
	if _, ok := findInventory(rep, "synthetic/live-probe"); ok {
		t.Fatal("persisted SM live blueprint was restored")
	}
	skips := h.r.LiveRestoreSkips()
	if len(skips) != 1 || skips[0].Blueprint != "synthetic/live-probe" || !strings.Contains(skips[0].Reason, "synthetic_monitoring") {
		t.Fatalf("restore skips = %+v", skips)
	}
	if n := seriesWithPrefix(h.tick(tickBase), "probe_"); n != 0 {
		t.Fatalf("skipped SM blueprint emitted %d probe series", n)
	}
}

// DELETE removes any persisted live record, including one the running process never loaded.
func TestLiveRemoveDeletesOrphanedRecord(t *testing.T) {
	h := newLiveHarness(t, filepath.Join(t.TempDir(), "state.json"), runner.Options{}, loadBoot(t, bootYAML, ""))
	orphan := control.LiveBlueprint{Name: "synthetic/orphan", Namespace: "synthetic", YAML: liveYAML("orphan", "orphan-cluster", "orphan-api")}
	h.store.Update(func(s *control.State) { s.LiveBlueprints = append(s.LiveBlueprints, orphan) })
	if code, body := h.remove(orphan.Name); code != http.StatusOK {
		t.Fatalf("remove orphaned record = %d %s", code, body)
	}
	if persisted := h.persistedLive(); len(persisted) != 0 {
		t.Fatalf("orphaned record survived DELETE: %+v", persisted)
	}
	if code, _ := h.remove(orphan.Name); code != http.StatusNotFound {
		t.Fatalf("second DELETE = %d, want 404", code)
	}
}

// DELETE forgets the per-name control state, so a later re-apply starts enabled and fresh.
func TestLiveRemoveForgetsPerNameControlState(t *testing.T) {
	h := newLiveHarness(t, filepath.Join(t.TempDir(), "state.json"), runner.Options{}, loadBoot(t, bootYAML, ""))
	if code, body := h.apply(liveApplyBody("operator-7")); code != http.StatusOK {
		t.Fatalf("apply = %d %s", code, body)
	}
	if code, body := h.do(http.MethodPost, "/control/blueprints/disable", map[string]string{"blueprint": liveID}, true); code != http.StatusOK {
		t.Fatalf("disable = %d %s", code, body)
	}
	h.store.Update(func(s *control.State) {
		s.Scaling[liveID+"/live-api"] = 3
		s.RuntimeIncidents = append(s.RuntimeIncidents,
			control.RuntimeIncident{ID: "ri-live", Blueprint: liveID, Mode: "latency", At: "everyNm", For: "1m"},
			control.RuntimeIncident{ID: "ri-base", Blueprint: "base", Mode: "latency", At: "everyNm", For: "1m"})
		s.DisabledBlueprints = append(s.DisabledBlueprints, "base-other")
	})
	if n := countSeries(h.tick(tickBase), "blueprint", liveID); n != 0 {
		t.Fatalf("disabled live blueprint emitted %d series", n)
	}
	if code, body := h.remove(liveID); code != http.StatusOK {
		t.Fatalf("remove = %d %s", code, body)
	}
	st := h.store.Snapshot()
	if slices.Contains(st.DisabledBlueprints, liveID) || !slices.Contains(st.DisabledBlueprints, "base-other") {
		t.Fatalf("disabled list after remove = %v", st.DisabledBlueprints)
	}
	if _, ok := st.Scaling[liveID+"/live-api"]; ok {
		t.Fatalf("scaling kept removed blueprint's key: %v", st.Scaling)
	}
	if len(st.RuntimeIncidents) != 1 || st.RuntimeIncidents[0].ID != "ri-base" {
		t.Fatalf("runtime incidents after remove = %+v", st.RuntimeIncidents)
	}
	if code, body := h.apply(liveApplyBody("operator-7")); code != http.StatusOK {
		t.Fatalf("re-apply = %d %s", code, body)
	}
	if n := countSeries(h.tick(tickBase.Add(time.Minute)), "blueprint", liveID); n == 0 {
		t.Fatal("re-applied blueprint started disabled")
	}
}

// The live set is capped at control.MaxLiveBlueprints entries.
func TestLiveApplyLimit(t *testing.T) {
	h := newLiveHarness(t, filepath.Join(t.TempDir(), "state.json"), runner.Options{}, loadBoot(t, bootYAML, ""))
	for i := 0; i < control.MaxLiveBlueprints; i++ {
		name := fmt.Sprintf("cap-%02d", i)
		if code, body := h.apply(liveBody{Namespace: "synthetic", Name: name, YAML: liveYAML(name, name+"-cluster", name+"-api")}); code != http.StatusOK {
			t.Fatalf("apply %d = %d %s", i, code, body)
		}
	}
	code, body := h.apply(liveBody{Namespace: "synthetic", Name: "cap-over", YAML: liveYAML("cap-over", "cap-over-cluster", "cap-over-api")})
	if code != http.StatusConflict || !strings.Contains(body, "live blueprint limit reached (64)") {
		t.Fatalf("apply over limit = %d %q", code, body)
	}
	if persisted := h.persistedLive(); len(persisted) != control.MaxLiveBlueprints {
		t.Fatalf("persisted %d live blueprints, want %d", len(persisted), control.MaxLiveBlueprints)
	}
	if code, body := h.remove("synthetic/cap-00"); code != http.StatusOK {
		t.Fatalf("remove = %d %s", code, body)
	}
	if code, body := h.apply(liveBody{Namespace: "synthetic", Name: "cap-over", YAML: liveYAML("cap-over", "cap-over-cluster", "cap-over-api")}); code != http.StatusOK {
		t.Fatalf("apply after freeing a slot = %d %s", code, body)
	}
}
