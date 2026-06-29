// SPDX-License-Identifier: AGPL-3.0-only

package dashgen

import (
	"context"
	"fmt"
	"os"
	"sort"
	"time"

	"github.com/rknightion/synthkit/dashboard"
	"github.com/rknightion/synthkit/internal/blueprint"
	"github.com/rknightion/synthkit/internal/control"
	"github.com/rknightion/synthkit/internal/fixture"
	"github.com/rknightion/synthkit/internal/runner"
	"github.com/rknightion/synthkit/internal/sink/loki"
	"github.com/rknightion/synthkit/internal/sink/otlp"
	"github.com/rknightion/synthkit/internal/sink/promrw"
)

// warmupCycles is how many dry cycles to run so trace/log lanes (which depend on minted
// traffic) populate. Metrics appear on the first tick; spans need a minted batch.
const warmupCycles = 12

// peakInstant is a deterministic weekday-midday instant used for derivation so diurnal
// shapes are at/near peak (off-peak ticks can mint zero traffic → empty span inventory).
func peakInstant(tz string) time.Time {
	loc, err := time.LoadLocation(tz)
	if err != nil || loc == nil {
		loc = time.UTC
	}
	return time.Date(2026, time.June, 17, 12, 0, 0, 0, loc) // a Wednesday, 12:00 local
}

// Derive loads the blueprint at path, runs it once through the runner with dry-run sinks,
// and assembles the dashboard.Manifest from the resolved topology + the sink inventories.
func Derive(path string) (*dashboard.Manifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	reg := runner.Catalog()
	res, err := blueprint.Load(data, reg)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if err := blueprint.ValidateSet([]*blueprint.Resolved{res}); err != nil {
		return nil, err
	}

	// Dry-run sinks: no creds, never touch the network (dryRun=true). capFn nil = unlimited.
	prom := promrw.New("", "", "", true, nil)
	lokiSink := loki.New("", "", "", true)
	otlpSink := otlp.New("", "", "", true)
	r := runner.New(runner.Sinks{Metrics: prom, Logs: lokiSink, Traces: otlpSink}, reg, runner.Options{})
	if err := r.AddBlueprint(res); err != nil {
		return nil, err
	}

	// Dashboards must reflect the full declared inventory incl. backend spanmetrics/service-graph,
	// which now default OFF per-blueprint — opt this blueprint in for the dry inventory harvest.
	cs := control.DefaultState()
	cs.SpanMetricsBlueprints = []string{res.Name}
	r.ApplyControl(cs)

	now := peakInstant(res.Timezone)
	ctx := context.Background()
	for i := 0; i < warmupCycles; i++ {
		if err := r.RunOnce(ctx, now.Add(time.Duration(i)*time.Minute)); err != nil {
			return nil, fmt.Errorf("dry cycle %d: %w", i, err)
		}
	}

	m := &dashboard.Manifest{Blueprint: res.Name, Label: res.Label}
	buildTopology(m, res)
	m.Metrics = ClassifyMetrics(prom.Inventory(), prom.Kinds(), prom.Natives())
	m.LogSources = logSources(lokiSink)
	m.Spans = spanSources(otlpSink)
	return m, nil
}

func buildTopology(m *dashboard.Manifest, res *blueprint.Resolved) {
	envs := map[string]dashboard.EnvRef{}
	accounts := map[string]dashboard.AccountRef{}
	clusters := map[string]dashboard.ClusterRef{}
	dbs := map[string]dashboard.DBRef{}
	caches := map[string]dashboard.CacheRef{}

	// Workloads carry resolved Env/Cluster/Calls — the richest topology source.
	for _, wi := range res.Workloads {
		w := dashboard.WorkloadRef{Name: wi.Name, Kind: wi.Kind}
		if wi.Cluster != nil {
			w.Cluster = wi.Cluster.Name
			recordCluster(clusters, accounts, envs, wi.Cluster)
		}
		if wi.Env != nil {
			w.Env = wi.Env.Name
		}
		for _, c := range wi.Calls {
			switch c.Kind {
			case "db":
				if c.DB != nil {
					w.Calls = append(w.Calls, dashboard.CallRef{Kind: "db", Name: c.DB.Name})
					recordDB(dbs, accounts, envs, c.DB)
				}
			case "cache":
				if c.Cache != nil {
					w.Calls = append(w.Calls, dashboard.CallRef{Kind: "cache", Name: c.Cache.Name})
					recordCache(caches, envs, c.Cache)
				}
			}
		}
		m.Workloads = append(m.Workloads, w)
	}

	// Constructs cover infra with no workload reference (clusters without workloads,
	// standalone DBs/caches). Their fixtures live in ci.Fixtures.
	for _, ci := range res.Constructs {
		fx := ci.Fixtures
		if fx == nil {
			continue
		}
		if fx.Cluster != nil {
			recordCluster(clusters, accounts, envs, fx.Cluster)
		}
		if fx.DB != nil {
			recordDB(dbs, accounts, envs, fx.DB)
		}
		for _, d := range fx.DBs {
			recordDB(dbs, accounts, envs, d)
		}
		if fx.Cache != nil {
			recordCache(caches, envs, fx.Cache)
		}
		for _, c := range fx.Caches {
			recordCache(caches, envs, c)
		}
	}

	m.Environments = sortedEnvs(envs)
	m.Accounts = sortedAccounts(accounts)
	m.Clusters = sortedClusters(clusters)
	m.Databases = sortedDBs(dbs)
	m.Caches = sortedCaches(caches)
	m.Integrations = deriveIntegrations(m, res)
}

// recordCluster records a cluster + its account + env from the fixture.
func recordCluster(cl map[string]dashboard.ClusterRef, ac map[string]dashboard.AccountRef, en map[string]dashboard.EnvRef, c *fixture.Cluster) {
	if c == nil {
		return
	}
	ref := dashboard.ClusterRef{
		Name:       c.Name,
		Type:       c.Type,
		K8sMonitor: c.K8sMonitoring.Enabled,
		OpenCost:   c.K8sMonitoring.OpenCost,
		Kepler:     c.K8sMonitoring.Kepler,
	}
	if c.Env != nil {
		ref.Env = c.Env.Name
		recordEnv(en, c.Env, c.Cloud)
	}
	if c.Cloud != nil {
		ref.Account = c.Cloud.AccountID
		recordAccount(ac, c.Cloud)
	}
	cl[c.Name] = ref
}

// recordDB records a database + its account + env from the fixture.
func recordDB(dbs map[string]dashboard.DBRef, ac map[string]dashboard.AccountRef, en map[string]dashboard.EnvRef, d *fixture.DB) {
	if d == nil {
		return
	}
	ref := dashboard.DBRef{Engine: d.Engine, Version: d.EngineVersion, Name: d.Name}
	if d.Env != nil {
		ref.Env = d.Env.Name
		recordEnv(en, d.Env, d.Cloud)
	}
	if d.Cloud != nil {
		ref.Account = d.Cloud.AccountID
		recordAccount(ac, d.Cloud)
	}
	dbs[d.Name] = ref
}

// recordCache records a cache + its env from the fixture (caches carry no distinct account).
func recordCache(caches map[string]dashboard.CacheRef, en map[string]dashboard.EnvRef, c *fixture.Cache) {
	if c == nil {
		return
	}
	ref := dashboard.CacheRef{Engine: c.Engine, Version: c.EngineVersion, Name: c.Name}
	if c.Env != nil {
		ref.Env = c.Env.Name
		recordEnv(en, c.Env, c.Cloud)
	}
	caches[c.Name] = ref
}

// recordEnv records an environment, enriching with cloud identity when available. A bare
// entry never clobbers a previously-recorded richer (cloud-carrying) one.
func recordEnv(en map[string]dashboard.EnvRef, e *fixture.Env, cloud *fixture.Cloud) {
	if e == nil {
		return
	}
	ref := dashboard.EnvRef{Name: e.Name}
	if cloud != nil {
		ref.Provider, ref.Account, ref.Region, ref.VpcID = cloud.Provider, cloud.AccountID, cloud.Region, cloud.VpcID
	}
	if existing, ok := en[e.Name]; ok && ref.Provider == "" {
		ref = existing
	}
	en[e.Name] = ref
}

// recordAccount records a cloud account keyed by its id.
func recordAccount(ac map[string]dashboard.AccountRef, cloud *fixture.Cloud) {
	if cloud == nil || cloud.AccountID == "" {
		return
	}
	ac[cloud.AccountID] = dashboard.AccountRef{Provider: cloud.Provider, ID: cloud.AccountID, Region: cloud.Region}
}

func sortedEnvs(m map[string]dashboard.EnvRef) []dashboard.EnvRef {
	out := make([]dashboard.EnvRef, 0, len(m))
	for _, v := range m {
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func sortedAccounts(m map[string]dashboard.AccountRef) []dashboard.AccountRef {
	out := make([]dashboard.AccountRef, 0, len(m))
	for _, v := range m {
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func sortedClusters(m map[string]dashboard.ClusterRef) []dashboard.ClusterRef {
	out := make([]dashboard.ClusterRef, 0, len(m))
	for _, v := range m {
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func sortedDBs(m map[string]dashboard.DBRef) []dashboard.DBRef {
	out := make([]dashboard.DBRef, 0, len(m))
	for _, v := range m {
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func sortedCaches(m map[string]dashboard.CacheRef) []dashboard.CacheRef {
	out := make([]dashboard.CacheRef, 0, len(m))
	for _, v := range m {
		out = append(out, v)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// logSources converts the loki dry-run inventory (keyed by `source`) into LogSources.
func logSources(s *loki.Sink) []dashboard.LogSource {
	stream, meta := s.Inventory()
	out := make([]dashboard.LogSource, 0, len(stream))
	for src, keys := range stream {
		out = append(out, dashboard.LogSource{Source: src, StreamKeys: keys, MetadataKeys: meta[src]})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Source < out[j].Source })
	return out
}

// spanSources converts the otlp dry-run inventory (keyed by service.name) into SpanSources.
func spanSources(s *otlp.Sink) []dashboard.SpanSource {
	resAttrs, spanNames, spanAttrs := s.Inventory()
	out := make([]dashboard.SpanSource, 0, len(resAttrs))
	for svc := range resAttrs {
		out = append(out, dashboard.SpanSource{
			Service:      svc,
			SpanNames:    spanNames[svc],
			AttrKeys:     spanAttrs[svc],
			ResourceKeys: resAttrs[svc],
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Service < out[j].Service })
	return out
}

// deriveIntegrations emits one IntegrationRef per off-the-shelf vendor surface this estate
// lights up: k8s (any k8s-monitored cluster), the cloud providers seen on accounts, and
// cloudflare (declared as a construct). Deduped + sorted; the thin index links only the
// ones with a configured deep-link target (see dashboard.IndexDashboard).
func deriveIntegrations(m *dashboard.Manifest, res *blueprint.Resolved) []dashboard.IntegrationRef {
	seen := map[string]bool{}
	var out []dashboard.IntegrationRef
	add := func(kind string) {
		if kind == "" || seen[kind] {
			return
		}
		seen[kind] = true
		out = append(out, dashboard.IntegrationRef{Kind: kind})
	}
	for _, c := range m.Clusters {
		if c.K8sMonitor {
			add("k8s")
		}
	}
	for _, a := range m.Accounts {
		switch a.Provider {
		case "aws", "gcp", "azure":
			add(a.Provider)
		}
	}
	for _, ci := range res.Constructs {
		if ci.Kind == "cloudflare" {
			add("cloudflare")
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Kind < out[j].Kind })
	return out
}
