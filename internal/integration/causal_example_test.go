// SPDX-License-Identifier: AGPL-3.0-only

package integration_test

import (
	"bytes"
	"fmt"
	"math"
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/rknightion/synthkit/internal/blueprint"
	"github.com/rknightion/synthkit/internal/causal"
	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/ledger"
	"github.com/rknightion/synthkit/internal/runner"
	"github.com/rknightion/synthkit/internal/shape"
)

// The authoring guide (docs/blueprints.md, "Declaring causes") and the fault catalogue
// (docs/fault-catalogue.md) teach the causal form by example. These tests keep every example
// honest: the worked example is the e2e/fixture the guide quotes verbatim, it loads through the
// real loader, its resources are resource-axis targets, and the numbers the guide states come
// from the shared model; every other YAML block in the two pages is a complete blueprint that
// loads, and the catalogue covers each of the eight modes against the resource kind it targets.

const (
	causalExampleFixture = "../../e2e/fixtures/causal-example.yaml"
	blueprintsGuide      = "../../docs/blueprints.md"
	faultCatalogue       = "../../docs/fault-catalogue.md"
)

func exReadText(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

var exYAMLFence = regexp.MustCompile("(?s)```yaml\n(.*?)```")

// section returns the markdown under the given level-2 heading, up to the next level-2 heading.
func exSection(t *testing.T, doc, heading string) string {
	t.Helper()
	start := strings.Index(doc, "\n## "+heading+"\n")
	if start < 0 {
		t.Fatalf("no %q section", heading)
	}
	rest := doc[start+1:]
	if end := strings.Index(rest[3:], "\n## "); end >= 0 {
		rest = rest[:end+3]
	}
	return rest
}

func exYAMLBlocks(md string) []string {
	var out []string
	for _, m := range exYAMLFence.FindAllStringSubmatch(md, -1) {
		out = append(out, m[1])
	}
	return out
}

func TestCausalExampleFixtureLoads(t *testing.T) {
	text := exReadText(t, causalExampleFixture)
	res, err := blueprint.Load([]byte(text), runner.Catalog())
	if err != nil {
		t.Fatal(err)
	}
	var resourceTargets []string
	for _, tg := range res.Targets {
		if tg.Axis == causal.AxisResource {
			resourceTargets = append(resourceTargets, tg.Name)
		}
	}
	slices.Sort(resourceTargets)
	if want := []string{"intake-db-pool", "records-db"}; !slices.Equal(resourceTargets, want) {
		t.Fatalf("resource-axis targets %v, want %v", resourceTargets, want)
	}
	// The scenario resolves onto the resource axis: records-db is also a service node, and the
	// incident targets the dependency because dependency_slow lives on that axis only.
	if !slices.Contains(res.Incidents, "dependency_slow@10:30/40m#1@records-db") {
		t.Fatalf("incidents %v lack the scheduled dependency_slow on records-db", res.Incidents)
	}
	if len(res.Workloads) != 1 || res.Workloads[0].Kind != "app" {
		t.Fatalf("workloads %+v, want the one app", res.Workloads)
	}
}

// exampleModel compiles the fixture's declared resources and returns a function reading the
// intake pool's state at a wall time on the fixture's schedule.
func exampleModel(t *testing.T) (*causal.Model, *shape.Engine) {
	t.Helper()
	text := exReadText(t, causalExampleFixture)
	var doc struct {
		Environments []struct {
			Name      string            `yaml:"name"`
			Weight    float64           `yaml:"weight"`
			Resources []causal.Resource `yaml:"resources"`
		} `yaml:"environments"`
	}
	if err := yaml.Unmarshal([]byte(text), &doc); err != nil || len(doc.Environments) != 1 {
		t.Fatalf("decode: %v (%d environments)", err, len(doc.Environments))
	}
	e := doc.Environments[0]
	m, err := causal.Compile(causal.Set{Envs: []causal.Declaration{{Env: e.Name, Weight: e.Weight, Resources: e.Resources}}})
	if err != nil {
		t.Fatal(err)
	}
	res, err := blueprint.Load([]byte(text), runner.Catalog())
	if err != nil {
		t.Fatal(err)
	}
	return m, shape.New("UTC", res.Incidents)
}

func exNear(got, want, tol float64) bool { return math.Abs(got-want) <= tol }

// TestCausalExampleNumbers pins every figure the guide quotes for the worked example: healthy,
// during lock contention, and recovered, on a weekday inside the business plateau.
func TestCausalExampleNumbers(t *testing.T) {
	m, eng := exampleModel(t)
	day := time.Date(2026, 10, 21, 0, 0, 0, 0, time.UTC) // a Wednesday
	at := func(hm string) time.Time {
		tm, err := time.Parse("15:04", hm)
		if err != nil {
			t.Fatal(err)
		}
		return day.Add(time.Duration(tm.Hour())*time.Hour + time.Duration(tm.Minute())*time.Minute)
	}
	state := func(hm string) causal.State {
		now := at(hm)
		m.Advance(now, eng)
		s, ok := m.State("intake-db-pool", now)
		if !ok {
			t.Fatal("no state for intake-db-pool")
		}
		return s
	}

	healthy := state("10:00")
	if !exNear(healthy.Demand, 60, 0.5) || !exNear(healthy.HoldMS, 20, 1e-9) || !exNear(healthy.InUse, 1.2, 0.02) ||
		!exNear(healthy.Utilization, 0.12, 0.002) || healthy.RejectShare != 0 || healthy.WaitMS != 0 || healthy.Mode != "" {
		t.Fatalf("healthy pool %+v: want ~60 rps, 20 ms hold, 1.2 of 10 busy (0.12), no rejects or wait", healthy)
	}

	faulted := state("11:00")
	// The fault is declared on the dependency; the pool carries no fault of its own, so its
	// exhaustion is emergent from the longer hold.
	if dep, ok := m.State("records-db", at("11:00")); !ok || dep.Mode != causal.ModeDependencySlow || dep.LatencyFactor != 12 {
		t.Fatalf("records-db state %+v ok=%v, want dependency_slow at latency factor 12", dep, ok)
	}
	if faulted.Mode != "" {
		t.Fatalf("pool dominant fault %q, want none: the cause is on the dependency", faulted.Mode)
	}
	// h = 20 + 15 x (12 - 1) = 185 ms; offered 60 x 0.185 = 11.1 connections of 10 (1.11).
	if !exNear(faulted.LatencyFactor, 12, 1e-9) || !exNear(faulted.HoldMS, 185, 1e-9) ||
		!exNear(faulted.Utilization, 1.11, 0.01) || !exNear(faulted.InUse, 10, 1e-9) {
		t.Fatalf("faulted pool %+v: want 12x latency, 185 ms hold, utilization ~1.11, all 10 connections busy", faulted)
	}
	// About a tenth of arrivals (1 - 1/1.11) cannot get a connection and every admitted request
	// waits for the bound.
	if !exNear(faulted.RejectShare, 0.099, 0.005) || faulted.WaitMS <= 0 || faulted.WaitMS > 3000 {
		t.Fatalf("faulted pool %+v: want ~9.9%% rejected and a wait within the 3000 ms bound", faulted)
	}

	// The per-request effect the app takes on arrival: failures carry the declared error.
	now := at("11:00")
	fails := 0
	const n = 4000
	for i := 0; i < n; i++ {
		e := m.Apply("intake-db-pool", now, causal.UnitDraw(fmt.Sprintf("example-%d", i)).Scope("intake-api#0/intake-db-pool"))
		if !e.Fail {
			continue
		}
		fails++
		if e.Status != 503 || e.ErrorType != "SQLTransientConnectionException" || !strings.HasPrefix(e.Message, "intake-db-pool: connection is not available") {
			t.Fatalf("failed effect %+v, want the declared 503 error", e)
		}
	}
	if share := float64(fails) / n; !exNear(share, faulted.RejectShare, 0.02) {
		t.Fatalf("%d of %d arrivals failed (%.3f), want ~%.3f", fails, n, share, faulted.RejectShare)
	}

	// The incident ends at 11:10: the pool returns to the healthy figures.
	after := state("11:30")
	if after.Mode != "" || !exNear(after.Utilization, 0.12, 0.002) || after.RejectShare != 0 {
		t.Fatalf("recovered pool %+v, want healthy", after)
	}
}

// TestCausalExampleRequests builds the fixture's app through the registry, as the runner does,
// and checks what the guide says requests look like: none meet the pool while healthy, and
// during the contention window about a tenth fail at the entry with the pool's declared 503
// after waiting out the acquire timeout, while the queries behind the pool run at 180 ms.
func TestCausalExampleRequests(t *testing.T) {
	reg := runner.Catalog()
	res, err := blueprint.Load([]byte(exReadText(t, causalExampleFixture)), reg)
	if err != nil {
		t.Fatal(err)
	}
	wi := res.Workloads[0]
	wr, _ := reg.Workload(wi.Kind)
	build := func() core.Workload {
		w, err := wr.Build(wi.Config, core.Binding{Name: wi.Name, Cluster: wi.Cluster, Env: wi.Env})
		if err != nil {
			t.Fatal(err)
		}
		return w
	}
	eng := shape.New("UTC", res.Incidents)
	day := time.Date(2026, 10, 21, 0, 0, 0, 0, time.UTC)
	hopMS := func(reqs []*ledger.Request) float64 {
		var sum, n float64
		for _, r := range reqs {
			for _, c := range r.Calls {
				if c.Target == "records-db" {
					sum += float64(c.Duration) / float64(time.Millisecond)
					n++
				}
			}
		}
		return sum / math.Max(n, 1)
	}

	healthy := build().Minter().Mint(day.Add(10*time.Hour), 60, eng)
	if len(healthy) == 0 {
		t.Fatal("no healthy requests minted")
	}
	// The app's ordinary background error rate still applies; no request meets the pool.
	ambient := 0
	for _, r := range healthy {
		if r.Fault != nil && r.Fault.Resource == "intake-db-pool" {
			t.Fatalf("healthy request met the pool: %+v", r.Fault)
		}
		if r.StatusCode >= 500 {
			ambient++
		}
	}
	t.Logf("healthy: %d requests, %d ambient 5xx", len(healthy), ambient)
	if got := hopMS(healthy); got < 10 || got > 20 {
		t.Errorf("healthy records-db hop mean %.1f ms, want about the declared 15", got)
	}

	faulted := build().Minter().Mint(day.Add(11*time.Hour), 60, eng)
	pool := 0
	for _, r := range faulted {
		// An admitted request that waited for a connection also carries the pool in its Fault;
		// the failures are the ones that ended in the pool's 503.
		if r.Fault == nil || r.Fault.Resource != "intake-db-pool" || r.StatusCode != 503 {
			continue
		}
		pool++
		if r.Outcome != ledger.OutcomeServerError || r.ErrorKind != "SQLTransientConnectionException" ||
			!strings.HasPrefix(r.Fault.Message, "intake-db-pool: connection is not available") {
			t.Fatalf("pool failure facts: status %d outcome %v kind %q fault %+v", r.StatusCode, r.Outcome, r.ErrorKind, r.Fault)
		}
		if r.Duration < 3*time.Second {
			t.Fatalf("pool failure lasted %v, want at least the 3000 ms acquire timeout", r.Duration)
		}
	}
	if share := float64(pool) / float64(len(faulted)); !exNear(share, 0.099, 0.02) {
		t.Errorf("%d of %d requests failed at the pool (%.3f), want about 0.099", pool, len(faulted), share)
	}
	if got := hopMS(faulted); got < 150 || got > 220 {
		t.Errorf("slowed records-db hop mean %.1f ms, want about 180", got)
	}
}

// TestCausalGuideExampleIsTheFixture requires the guide's worked example to be the fixture file
// itself, byte for byte, so the example the reader copies is the one the tests load.
func TestCausalGuideExampleIsTheFixture(t *testing.T) {
	sec := exSection(t, exReadText(t, blueprintsGuide), "Declaring causes")
	blocks := exYAMLBlocks(sec)
	if len(blocks) == 0 {
		t.Fatal("the Declaring causes section has no YAML block")
	}
	fixture := exReadText(t, causalExampleFixture)
	found := false
	for _, b := range blocks {
		if strings.TrimSpace(b) == strings.TrimSpace(fixture) {
			found = true
		}
	}
	if !found {
		t.Fatal("no YAML block in the Declaring causes section equals e2e/fixtures/causal-example.yaml")
	}
	for _, name := range []string{"dependency_slow", "intake-db-pool", "records-db"} {
		if !strings.Contains(sec, name) {
			t.Errorf("the Declaring causes section never mentions %q", name)
		}
	}
}

// modeKinds is the resource kind each mode acts on (internal/causal doc: the fault vocabulary).
var modeKinds = map[string]causal.Kind{
	causal.ModePoolExhausted:         causal.KindPool,
	causal.ModeConcurrencyReduced:    causal.KindConcurrency,
	causal.ModeQuotaReduced:          causal.KindQuota,
	causal.ModeConsumerSlowdown:      causal.KindQueue,
	causal.ModeDependencySlow:        causal.KindDependency,
	causal.ModeDependencyUnavailable: causal.KindDependency,
	causal.ModeCredentialExpired:     causal.KindCredential,
	causal.ModeRuleRegression:        causal.KindRule,
}

// TestCausalDocSnippetsLoad loads every YAML block in the guide's section and in the fault
// catalogue as a complete blueprint through the real loader, and requires the catalogue to
// show each of the eight modes aimed at a resource of the kind the mode acts on.
func TestCausalDocSnippetsLoad(t *testing.T) {
	guide := exYAMLBlocks(exSection(t, exReadText(t, blueprintsGuide), "Declaring causes"))
	cat := exReadText(t, faultCatalogue)
	catalogue := exYAMLBlocks(cat)
	if len(catalogue) < len(modeKinds) {
		t.Fatalf("the fault catalogue has %d YAML blocks, want one per mode (%d)", len(catalogue), len(modeKinds))
	}
	reg := runner.Catalog()
	seen := map[string]bool{}
	for i, text := range append(slices.Clone(guide), catalogue...) {
		res, err := blueprint.Load([]byte(text), reg)
		if err != nil {
			t.Errorf("block %d does not load: %v\n%s", i, err, text)
			continue
		}
		if i < len(guide) {
			continue
		}
		var doc struct {
			Environments []struct {
				Resources []causal.Resource `yaml:"resources"`
			} `yaml:"environments"`
			Scenarios []struct {
				Effects []struct{ Mode, Target string } `yaml:"effects"`
			} `yaml:"scenarios"`
		}
		if err := yaml.NewDecoder(bytes.NewReader([]byte(text))).Decode(&doc); err != nil {
			t.Fatal(err)
		}
		kinds := map[string]causal.Kind{}
		for _, e := range doc.Environments {
			for _, r := range e.Resources {
				kinds[r.Name] = r.Kind
			}
		}
		var resourceTargets []string
		for _, tg := range res.Targets {
			if tg.Axis == causal.AxisResource {
				resourceTargets = append(resourceTargets, tg.Name)
			}
		}
		for _, s := range doc.Scenarios {
			for _, ef := range s.Effects {
				want, ok := modeKinds[ef.Mode]
				if !ok {
					continue
				}
				if got := kinds[ef.Target]; got != want {
					t.Errorf("block %d: %s targets %q, a %q resource; want a %q", i, ef.Mode, ef.Target, got, want)
				}
				if !slices.Contains(resourceTargets, ef.Target) {
					t.Errorf("block %d: %q is not a resource-axis target", i, ef.Target)
				}
				seen[ef.Mode] = true
			}
		}
	}
	for mode := range modeKinds {
		if !seen[mode] {
			t.Errorf("the fault catalogue shows no loadable snippet for %s", mode)
		}
		if !strings.Contains(cat, "`"+mode+"`") {
			t.Errorf("the fault catalogue never names %s", mode)
		}
	}
}
