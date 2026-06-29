// SPDX-License-Identifier: AGPL-3.0-only

package blueprint

import (
	"fmt"
	"log"
	"sort"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/failuremode"
	"github.com/rknightion/synthkit/internal/fixture"
	"github.com/rknightion/synthkit/internal/genai"
)

// Topology construct kinds the resolver emits from typed declarations. Their configs
// are EMPTY (everything they need arrives via fixture.Set); add-ons/features/workloads
// carry their own YAML config (the two decode paths — ARCHITECTURE §3).
//
// DocDB and Neptune are engine variants of the databases: declaration (same fixture path
// as RDS; the engine discriminator in the DB CW pass selects the right kind).
// AOSS, MWAA, and Glue are cloud-service constructs emitted from cloud.<service>: blocks;
// their identity is carried in their Config (Collections/Environments/Jobs), not in a fixture.
const (
	KindK8sCluster  = "k8s_cluster"
	KindEC2         = "ec2"
	KindCWInfra     = "cw_infra"
	KindRDS         = "rds"
	KindElastiCache = "elasticache"
	// Substrate-scoped traditional host (node/windows/macos exporter + optional docker),
	// emitted from the top-level hosts: list (Group "" topology kind, like ec2/rds).
	KindHost           = "host"
	KindDbo11yMySQL    = "dbo11y_mysql"
	KindDbo11yPostgres = "dbo11y_postgres"
	// New in catalog-expansion 2a:
	KindDocDB   = "docdb"
	KindNeptune = "neptune"
	KindAOSS    = "aoss"
	KindMWAA    = "mwaa"
	KindGlue    = "glue"
	// New in catalog-expansion 2b (AI cloud-services):
	KindBedrock   = "bedrock"
	KindAgentCore = "agentcore"
	// Fleet Management mirror: emitted from the cluster path when k8s_monitoring.fleet_management
	// is true. Must equal the string the fleetmgmt construct registers as its Kind.
	KindFleetManagement = "fleet_management"
	// Continuous profiling (Pyroscope): emitted from the cluster path when
	// k8s_monitoring.features.profiling is true (the Alloy feature-profiling eBPF lane).
	KindK8sProfiling = "k8s_profiling"
)

// TopologyKinds lists the resolver-emitted construct kinds (registry must cover them
// when the corresponding declarations are used).
func TopologyKinds() []string {
	return []string{
		KindK8sCluster, KindEC2, KindCWInfra, KindRDS, KindElastiCache, KindHost,
		KindDbo11yMySQL, KindDbo11yPostgres,
		KindDocDB, KindNeptune, KindAOSS, KindMWAA, KindGlue,
		KindBedrock, KindAgentCore,
		KindFleetManagement,
		KindK8sProfiling,
	}
}

// defaultReplicas is a workload's pod count when `replicas` is omitted.
const defaultReplicas = 2

// classifyCall validates a CallDecl sets exactly one primary key and resolves it to a hop
// kind, an AI carrier (nil for db/cache/service), and the primary name used for `via`
// nesting + targetIndex. AI hops carry their gen_ai operation + provider/model/subject from
// the decl (no backing fixture — unlike db/cache/service). Provider/Model are modifiers, not
// counted in the one-of.
func classifyCall(c CallDecl) (kind string, ai *fixture.AICall, primary string, err error) {
	type opt struct{ kind, val, op string }
	opts := []opt{
		{"db", c.DB, ""},
		{"cache", c.Cache, ""},
		{"service", c.Service, ""},
		{fixture.KindLLMGateway, c.Gateway, genai.OpChat},
		{fixture.KindLLMModel, c.LLM, genai.OpChat},
		{fixture.KindAgent, c.Agent, genai.OpInvokeAgent},
		{fixture.KindTool, c.Tool, genai.OpExecuteTool},
		{fixture.KindWorkflow, c.Workflow, genai.OpInvokeWorkflow},
		{fixture.KindRetrieval, c.Retrieval, genai.OpRetrieval},
	}
	var set []opt
	for _, o := range opts {
		if o.val != "" {
			set = append(set, o)
		}
	}
	if len(set) != 1 {
		return "", nil, "", fmt.Errorf("each call must set exactly one of db|cache|service|gateway|llm|agent|tool|workflow|retrieval")
	}
	o := set[0]
	switch o.kind {
	case "db", "cache", "service":
		return o.kind, nil, o.val, nil
	}
	// AI hop: build the gen_ai carrier from the decl.
	ai = &fixture.AICall{Op: o.op, Provider: c.Provider}
	switch o.kind {
	case fixture.KindLLMModel:
		ai.Model = o.val // the `llm` value IS the model id
	case fixture.KindLLMGateway:
		ai.Subject = o.val // the gateway name
		ai.Model = c.Model // the routed model id (optional)
	default:
		ai.Subject = o.val // agent / tool / workflow / retrieval name
	}
	return o.kind, ai, o.val, nil
}

// defaultDigests is a dbo11y database's query-catalogue size when omitted.
const defaultDigests = 40

// defaultAlloyVersion is the canonical Alloy version when k8s_monitoring.alloy is on
// and no version is declared (the "v" prefix is load-bearing for alloy_build_info).
const defaultAlloyVersion = "v1.16.3"

// ConstructInstance is one buildable construct: kind + decoded config + fixtures.
type ConstructInstance struct {
	Kind     string
	Name     string // instance identity for diagnostics (cluster/db/cache/feature name)
	Config   any
	Fixtures *fixture.Set
}

// WorkloadInstance is one buildable workload: kind + decoded config + resolved wiring.
type WorkloadInstance struct {
	Kind     string
	Name     string
	Config   any
	Replicas int
	RUM      bool // the workload declared rum: true (creds resolved by the runner)
	Env      *fixture.Env
	Cluster  *fixture.Cluster
	Calls    []fixture.CallTarget
	// Databases are the resolved fixtures for the databases declared in this instance's env
	// (nil when the env declares none). Threaded onto core.Binding so a workload can resolve a
	// db-leaf node to its env's RDS instance without minting identity (the resolver owns it).
	Databases []*fixture.DB
}

// Target is one addressable failure/scaling target in a blueprint.
type Target struct {
	Name     string
	Axis     failuremode.Axis
	Scalable *ScaleBounds // non-nil ⇒ live-scalable
}

// ScaleBounds bounds a live-scalable dimension.
type ScaleBounds struct {
	Dimension string // "replicas" | "read_replicas"
	Default   int
	Min, Max  int
}

// ResolvedScenario is a validated scenario: display fields + validated effects (target kept
// as written, including any "axis:*" — live expansion happens in the runner against the inventory).
type ResolvedScenario struct {
	Name, Title, Summary string
	Effects              []EffectDecl
}

// Resolved is a fully-validated, fixture-resolved blueprint, ready for the runner.
// ResolvedEnvMeta carries one declared environment's name + optional metadata for the operator
// UI (decl order; one entry per environment, metadata empty when the env declared none). This is
// display-only — authoritative env identity lives in the fixtures, not here.
type ResolvedEnvMeta struct {
	Name     string
	Metadata Metadata
}

type Resolved struct {
	Name         string
	Label        string
	Metadata     Metadata          // blueprint-level human-facing annotation (UI only)
	Environments []ResolvedEnvMeta // per-env metadata for the UI (decl order)
	Shape        string
	Timezone     string
	Regions      []RegionDecl // follow-the-sun multi-tz composite; mutually exclusive with Timezone
	SeriesBudget int
	Constructs   []ConstructInstance
	Workloads    []WorkloadInstance
	Incidents    []string // shape-engine schedule entries: kind@at/for[#intensity][@target]
	Targets      []Target
	Scenarios    []ResolvedScenario
	// Warnings are non-fatal resolve-time notes (e.g. a database/cache declaration that produced
	// zero emitting constructs) surfaced to operators via the control-plane diagnostics panel.
	Warnings []string
	// Source is the verbatim YAML the blueprint was loaded from (operator UI display).
	Source string
}

// vocabulary returns the union of failure modes across all registered construct + workload kinds —
// the set the resolver validates every scenario/incident effect against.
func vocabulary(reg *core.Registry) []failuremode.Mode {
	return reg.AllFailureModes()
}

// envCtx pairs an environment's resolved fixtures (env identity + cloud) with its raw decl.
// It is carried through the resolver passes and into per-env integration/workload fan-out.
type envCtx struct {
	env   *fixture.Env
	cloud *fixture.Cloud
	decl  *EnvDecl
}

// expandedWorkload is one concrete workload placement after for_each_env fan-out: the source decl,
// the resolved instance name (fanned: "<name>-<env>"; else decl.Name), the target cluster name
// (runsOn), and the env name (envName; "" for a non-fanned workload — env comes from the cluster).
type expandedWorkload struct {
	decl    *WorkloadDecl
	name    string
	runsOn  string
	envName string
}

// expandWorkloads resolves for_each_env fan-out into concrete placements: a fanned workload binds
// to each target env's cluster (named "<name>-<env>"); a non-fanned workload keeps its explicit
// runs_on. A fanned workload may not declare a db/cache call — those name globally-unique physical
// resources a shared declaration cannot resolve per-env (declare a per-env workload instead).
func expandWorkloads(d *Decl, clusterByEnv map[string]string, allEnvNames []string) ([]expandedWorkload, error) {
	var out []expandedWorkload
	for i := range d.Workloads {
		w := &d.Workloads[i]
		if !(w.ForEachEnv || len(w.Envs) > 0) {
			out = append(out, expandedWorkload{decl: w, name: w.Name, runsOn: w.RunsOn})
			continue
		}
		for _, c := range w.Calls {
			if c.DB != "" || c.Cache != "" {
				return nil, fmt.Errorf("blueprint %q: workload %q is fanned (for_each_env) and may not declare a db/cache call (%q) — declare a per-env workload instead",
					d.Name, w.Name, c.DB+c.Cache)
			}
		}
		targets := w.Envs
		if len(targets) == 0 {
			targets = allEnvNames
		}
		for _, en := range targets {
			cl, ok := clusterByEnv[en]
			if !ok {
				return nil, fmt.Errorf("blueprint %q: workload %q for_each_env targets env %q which has no cluster", d.Name, w.Name, en)
			}
			out = append(out, expandedWorkload{decl: w, name: w.Name + "-" + en, runsOn: cl, envName: en})
		}
	}
	return out, nil
}

// resolve turns a validated Decl into fixtures + instances. reg supplies config types
// and validates kinds.
func resolve(d *Decl, reg *core.Registry) (*Resolved, error) {
	if d.Timezone != "" && len(d.Regions) > 0 {
		return nil, fmt.Errorf("blueprint %q: set either timezone or regions, not both", d.Name)
	}
	r := &Resolved{
		Name:         d.Name,
		Label:        d.Label,
		Metadata:     d.Metadata,
		Shape:        d.Shape,
		Timezone:     d.Timezone,
		Regions:      d.Regions,
		SeriesBudget: d.SeriesBudget,
	}
	for i := range d.Environments {
		r.Environments = append(r.Environments, ResolvedEnvMeta{Name: d.Environments[i].Name, Metadata: d.Environments[i].Metadata})
	}
	if r.Label == "" {
		r.Label = d.Name
	}
	if r.Shape == "" {
		r.Shape = "business_hours_plateau"
	}
	seed := d.Name // determinism root: same blueprint name → same identities

	clusters := map[string]*fixture.Cluster{} // cluster name → fixture (shared pointers)
	dbs := map[string]*fixture.DB{}
	dbsByEnv := map[string][]*fixture.DB{} // env name → its declared db fixtures (decl order)
	caches := map[string]*fixture.Cache{}

	// Pass 1 — env-level fixtures (cloud, db, cache) + cluster shells (nodes resolved
	// in pass 2, after workload placements are known).
	var envs []envCtx
	for i := range d.Environments {
		e := &d.Environments[i]
		env := &fixture.Env{Name: e.Name, Weight: 1.0, NonProd: e.Name != "prod"}
		if e.Weight != nil {
			env.Weight = *e.Weight
		}
		if e.Production != nil {
			env.NonProd = !*e.Production
		}
		var cloud *fixture.Cloud
		if e.Cloud != nil {
			cloud = &fixture.Cloud{
				Provider:  e.Cloud.Provider,
				AccountID: e.Cloud.AccountID,
				Region:    e.Cloud.Region,
				VpcID:     e.Cloud.VpcID,
			}
			for n := range e.Cloud.NATGateways {
				cloud.NATGatewayIDs = append(cloud.NATGatewayIDs,
					fixture.NATGatewayID(seed, e.Name, "nat", strconv.Itoa(n)))
			}
		}
		envs = append(envs, envCtx{env: env, cloud: cloud, decl: e})

		if e.Cluster != nil {
			cl := &fixture.Cluster{
				Name:  e.Cluster.Name,
				Type:  e.Cluster.Type,
				Env:   env,
				Cloud: cloud,
				K8sMonitoring: fixture.K8sMonitoring{
					Enabled:             e.Cluster.K8sMonitoring.Enabled,
					ChartVersion:        e.Cluster.K8sMonitoring.ChartVersion,
					Alloy:               e.Cluster.K8sMonitoring.Alloy,
					AlloyVersion:        canonicalAlloyVersion(e.Cluster.K8sMonitoring),
					OpenCost:            e.Cluster.K8sMonitoring.OpenCost,
					Kepler:              e.Cluster.K8sMonitoring.Kepler,
					Features:            e.Cluster.K8sMonitoring.Features,
					MetricsReplicas:     e.Cluster.K8sMonitoring.MetricsReplicas,
					ReceiverAsDaemonset: e.Cluster.K8sMonitoring.ReceiverAsDaemonset,
					FleetManagement:     e.Cluster.K8sMonitoring.FleetManagement,
					ControlPlane: fixture.ControlPlane{
						ApiServer:             e.Cluster.K8sMonitoring.ControlPlane.ApiServer,
						KubeProxy:             e.Cluster.K8sMonitoring.ControlPlane.KubeProxy,
						KubeScheduler:         e.Cluster.K8sMonitoring.ControlPlane.KubeScheduler,
						KubeControllerManager: e.Cluster.K8sMonitoring.ControlPlane.KubeControllerManager,
						KubeletProbes:         e.Cluster.K8sMonitoring.ControlPlane.KubeletProbes,
					},
					PodLogsMethod: resolvePodLogsMethod(e.Cluster.K8sMonitoring),
				},
				Platform: resolvePlatform(e.Cluster.Platform),
			}
			// Capture addon names so k8scluster can populate the kube-system KSM inventory
			// (k8scluster never imports blueprint — it reads names only via fixture.Cluster).
			for _, a := range e.Cluster.Addons {
				cl.Addons = append(cl.Addons, a.Name)
			}
			clusters[cl.Name] = cl
		}
		for j := range e.Databases {
			db := &e.Databases[j]
			fx := buildDBFixture(seed, db, env, cloud)
			dbs[db.Name] = fx
			dbsByEnv[e.Name] = append(dbsByEnv[e.Name], fx)
		}
		for j := range e.Caches {
			c := &e.Caches[j]
			cacheInstanceClass := c.InstanceClass
			if cacheInstanceClass == "" {
				cacheInstanceClass = "cache.r6g.large"
			}
			caches[c.Name] = &fixture.Cache{
				Engine:        c.Engine,
				EngineVersion: c.Version,
				Name:          c.Name,
				InstanceClass: cacheInstanceClass,
				NodeIDs:       []string{c.Name + "-0001"},
				Env:           env,
				Cloud:         cloud,
			}
		}
	}

	// Fan-out expansion: a for_each_env workload becomes one placement per target env (bound to
	// that env's cluster, name-suffixed "<name>-<env>"). Computed once and reused by Pass 2
	// (placement) + Pass 5 (instances). A non-fanned workload keeps its explicit runs_on.
	clusterByEnv := map[string]string{}
	var allEnvNames []string
	for _, ec := range envs {
		if ec.decl.Cluster != nil {
			clusterByEnv[ec.decl.Name] = ec.decl.Cluster.Name
			allEnvNames = append(allEnvNames, ec.decl.Name)
		}
	}
	expanded, expErr := expandWorkloads(d, clusterByEnv, allEnvNames)
	if expErr != nil {
		return nil, expErr
	}

	// Pass 2 — workload placements onto clusters, then node derivation + node builds.
	for _, ew := range expanded {
		cl, ok := clusters[ew.runsOn]
		if !ok {
			return nil, fmt.Errorf("blueprint %q: workload %q runs_on %q — no such cluster (declared clusters: %s)",
				d.Name, ew.name, ew.runsOn, strings.Join(sortedKeys(clusters), ", "))
		}
		// Pod names are minted with a CLUSTER-SCOPED seed (seed:cluster) so the SAME deployment placed
		// on multiple for_each_env clusters gets distinct pod hashes — real EKS fleets never share pod
		// hashes across clusters (the cluster is a label, never part of the pod string). Matches the
		// Pass-2c addon precedent + the liveCluster re-mint in k8scluster.go (same seed both sides).
		clusterSeed := seed + ":" + cl.Name
		// An `app` workload places ONE cluster workload PER service node (each node is its own
		// deployment), so the cluster's pod total — and thus the node cascade + the fleet-manager
		// instances that follow — sums per service node (per-service scaling, §6.6). The node names
		// match the AxisService scaling targets (buildTargets), so a live POST /control/scaling on a
		// node resolves through scale.Source.Count(nodeName, declared). A non-app workload places a
		// single deployment as before.
		if nodes := appServiceNodes(*ew.decl); len(nodes) > 0 {
			for _, n := range nodes {
				if n.external {
					continue // remote/managed service (e.g. a SaaS gateway in another team's estate): emits its connected trace hop but is NOT a k8s deployment on this cluster
				}
				// Q4: an app service node is the traced golden-thread lane — DaemonSets don't emit
				// app traces IRL, so a daemonset controller on an app node is a resolve-time error.
				if n.controller == "daemonset" {
					return nil, fmt.Errorf("blueprint %q: app workload %q service node %q: controller: daemonset is not allowed on a traced app service node (DaemonSets do not emit app traces); declare it as a runs_in_cluster integration instead",
						d.Name, ew.name, n.name)
				}
				rep := n.replicas
				if rep <= 0 {
					rep = defaultReplicas
				}
				ns := n.namespace
				if ns == "" {
					ns = n.name
				}
				placement := fixture.Workload{
					Name: n.name, Namespace: ns, Replicas: rep, Runtime: n.runtime, Resources: n.resources,
					Controller: n.controller, HasHPA: n.hpa, VolumeClaims: n.volumeClaims,
				}
				placement.PodNames = fixture.WorkloadPodNames(clusterSeed, placement, nil)
				cl.Workloads = append(cl.Workloads, placement)
			}
			continue
		}
		replicas := ew.decl.Replicas
		if replicas <= 0 {
			replicas = defaultReplicas
		}
		placement := fixture.Workload{
			Name:      ew.name,
			Namespace: ew.name,
			Replicas:  replicas,
		}
		placement.PodNames = fixture.WorkloadPodNames(clusterSeed, placement, nil)
		cl.Workloads = append(cl.Workloads, placement)
	}

	// Pass 2b — integration `runs_in_cluster` deployed components. An integration that declares a
	// `runs_in_cluster` block places a workload on every target env's cluster (the integration's
	// software running IN the estate, e.g. an API-poller Deployment). It REQUIRES for_each_env/envs
	// (it needs a cluster to land on). Daemonset PodNames are minted per-node below (after buildNodes).
	if err := placeRunsInCluster(d, seed, clusters, clusterByEnv); err != nil {
		return nil, err
	}

	// Reject duplicate (namespace, name) workloads on a cluster — pod identities + KSM series key on
	// (namespace, workload) and a collision would silently double-emit (mirrors the cluster-name
	// uniqueness check). Runs across ALL placed workloads (app nodes, generic, runs_in_cluster).
	for _, cl := range clusters {
		seen := map[string]bool{}
		for _, w := range cl.Workloads {
			k := w.Namespace + "/" + w.Name
			if seen[k] {
				return nil, fmt.Errorf("blueprint %q: cluster %q has duplicate workload %q in namespace %q",
					d.Name, cl.Name, w.Name, w.Namespace)
			}
			seen[k] = true
		}
	}

	// A daemonset workload scales WITH the nodes (one pod per node), so its Replicas must NOT inflate
	// node demand. Zero it before buildNodes (every node-count summer skips n≤0); its per-node pods +
	// node placement are minted below, once the node set exists (B2 / spec §3.4).
	for _, cl := range clusters {
		for wi := range cl.Workloads {
			if cl.Workloads[wi].Controller == "daemonset" {
				cl.Workloads[wi].Replicas = 0
				cl.Workloads[wi].PodNames = nil
			}
		}
	}

	// Pass 2c — populate SubstrateWorkloads: addon/baseline pods (cert-manager, coredns, karpenter, …).
	// These stay OUT of cl.Workloads so they do not inflate node demand or appear in node-count summers.
	// PodNames are minted with a CLUSTER-SCOPED seed (seed:cluster) so each cluster gets distinct pod
	// hashes — real EKS pod names are cluster-unique (a fleet of clusters does NOT share pod hashes).
	// liveCluster keeps SubstrateWorkloads verbatim (no re-mint), so these names are the single source.
	// A (namespace,name) pair already present is skipped (dedup: metrics_server is in BaselineWorkloads
	// AND in AddonWorkloads).
	for _, cl := range clusters {
		seen := map[string]bool{}
		clusterSeed := seed + ":" + cl.Name
		appendSubstrate := func(wl fixture.Workload) {
			key := wl.Namespace + "/" + wl.Name
			if seen[key] {
				return
			}
			seen[key] = true
			wl.PodNames = fixture.WorkloadPodNames(clusterSeed, wl, nil)
			cl.SubstrateWorkloads = append(cl.SubstrateWorkloads, wl)
		}
		for _, wl := range fixture.BaselineWorkloads() {
			appendSubstrate(wl)
		}
		// Alloy collector pods (monitoring ns) only when k8s_monitoring is enabled (exact prior
		// presence condition from helpers.go). alloy-logs is a DaemonSet — its PodNames are minted
		// per-node in the post-buildNodes pass below (nodes are nil here).
		if cl.K8sMonitoring.Enabled || cl.K8sMonitoring.Alloy {
			for _, wl := range fixture.MonitoringBaselineWorkloads(cl.K8sMonitoring.MetricsReplicas) {
				appendSubstrate(wl)
			}
		}
		for _, addonKey := range cl.Addons {
			for _, wl := range fixture.AddonWorkloads(addonKey) {
				appendSubstrate(wl)
			}
		}
	}

	for _, ec := range envs {
		if ec.decl.Cluster == nil {
			continue
		}
		cl := clusters[ec.decl.Cluster.Name]
		buildNodes(seed, cl, ec.decl.Cluster, ec.cloud)
		// Pod→node placement once nodes exist. DaemonSet workloads run one pod per node (names keyed
		// by node hostname, NodeIdx = [0..n-1]); all other controllers round-robin over Replicas.
		for wi := range cl.Workloads {
			pl := &cl.Workloads[wi]
			pl.NodeIdx = pl.NodeIdx[:0]
			if pl.Controller == "daemonset" {
				// Cluster-scoped seed (matches the app-pod mint above + the liveCluster DaemonSet
				// re-mint); a DaemonSet's suffix already varies by node hostname, but scoping the
				// seed keeps ALL app-workload pod minting on one rule (seed:cluster).
				pl.PodNames = fixture.WorkloadPodNames(seed+":"+cl.Name, *pl, cl.Nodes)
				for ni := range cl.Nodes {
					pl.NodeIdx = append(pl.NodeIdx, ni)
				}
				continue
			}
			for p := range pl.Replicas {
				pl.NodeIdx = append(pl.NodeIdx, p%len(cl.Nodes))
			}
		}
		// Substrate workloads (addon/baseline pods) need the same post-buildNodes pod→node
		// placement: a DaemonSet substrate workload (alloy-logs) was minted with nil PodNames in
		// Pass-2c (no nodes yet), so re-mint it per-node now with the cluster-scoped seed and assign
		// one NodeIdx per node. Deployment/StatefulSet substrate workloads round-robin their replicas
		// over the nodes so node placement is deterministic (was previously left to nodeAssignment).
		for wi := range cl.SubstrateWorkloads {
			pl := &cl.SubstrateWorkloads[wi]
			pl.NodeIdx = pl.NodeIdx[:0]
			if pl.Controller == "daemonset" {
				pl.PodNames = fixture.WorkloadPodNames(seed+":"+cl.Name, *pl, cl.Nodes)
				for ni := range cl.Nodes {
					pl.NodeIdx = append(pl.NodeIdx, ni)
				}
				continue
			}
			for p := range len(pl.PodNames) {
				pl.NodeIdx = append(pl.NodeIdx, p%len(cl.Nodes))
			}
		}
	}

	// Pass 3 — emit construct instances.
	for _, ec := range envs {
		e := ec.decl
		baseSet := func() *fixture.Set {
			return &fixture.Set{Seed: seed + ":" + e.Name, Env: ec.env, Cloud: ec.cloud}
		}
		if e.Cluster != nil {
			cl := clusters[e.Cluster.Name]
			set := baseSet()
			set.Cluster = cl
			set.Seed = seed + ":" + cl.Name
			// k8s substrate always emits; the per-node EC2 CloudWatch lane is gated by the
			// cluster's emission switch (the cloud-provider view of the nodes — §3.2).
			kinds := []string{KindK8sCluster}
			if e.Cluster.Observability.enabled() {
				kinds = append(kinds, KindEC2)
			}
			// Continuous profiling (the Alloy feature-profiling analog). Instantiate the construct
			// if ANY of its three independent sub-lanes is enabled: features.profiling (eBPF
			// process_cpu, all pods), features.profiling_pprof (Go pprof scrape), features.profiling_java
			// (JVM async-profiler). Substrate-scoped; joins workload pods by shared fixture identity
			// (signals/profiles.md). The construct re-checks each key per lane.
			km := e.Cluster.K8sMonitoring.Features
			if km["profiling"] || km["profiling_pprof"] || km["profiling_java"] {
				kinds = append(kinds, KindK8sProfiling)
			}
			for _, kind := range kinds {
				ci, err := emptyConfigInstance(reg, kind, cl.Name, set, d.Name)
				if err != nil {
					return nil, err
				}
				r.Constructs = append(r.Constructs, *ci)
			}
			for _, a := range e.Cluster.Addons {
				creg, ok := reg.Construct(a.Name)
				if !ok {
					return nil, fmt.Errorf("blueprint %q: cluster %q declares unknown addon %q (registered constructs: %s)",
						d.Name, cl.Name, a.Name, strings.Join(reg.ConstructKinds(), ", "))
				}
				cfg := creg.NewConfig()
				if err := strictDecode(&a.Config, cfg, fmt.Sprintf("blueprint %q: addon %q config", d.Name, a.Name)); err != nil {
					return nil, err
				}
				r.Constructs = append(r.Constructs, ConstructInstance{
					Kind: a.Name, Name: cl.Name + "/" + a.Name, Config: cfg, Fixtures: set,
				})
			}
			// Fleet Management mirror: emit a fleet_management construct instance from the
			// cluster path when k8s_monitoring.fleet_management is true. The instance carries
			// the cluster fixture (set.Cluster is populated above), which is what makes the
			// fleetmgmt construct run in mirror mode (DD6b — mirrorEnabled derives from fixture).
			if e.Cluster.K8sMonitoring.FleetManagement {
				if !e.Cluster.K8sMonitoring.Enabled {
					return nil, fmt.Errorf("blueprint %q: cluster %q: k8s_monitoring.fleet_management requires k8s_monitoring.enabled",
						d.Name, cl.Name)
				}
				ci, err := emptyConfigInstance(reg, KindFleetManagement, cl.Name+"/fleet", set, d.Name)
				if err != nil {
					return nil, err
				}
				r.Constructs = append(r.Constructs, *ci)
			}
		}
		if ec.cloud != nil {
			set := baseSet()
			if e.Cluster != nil {
				set.Cluster = clusters[e.Cluster.Name]
			}
			for _, db := range e.Databases {
				set.DBs = append(set.DBs, dbs[db.Name])
			}
			for _, c := range e.Caches {
				set.Caches = append(set.Caches, caches[c.Name])
			}
			// cw_infra is fixture-resolved (topology) but config-decoded: the env's
			// `cloud.cloudwatch` block carries the per-family emission switches, strict-
			// decoded into the construct's own config via the registry (no construct import).
			creg, ok := reg.Construct(KindCWInfra)
			if !ok {
				return nil, fmt.Errorf("blueprint %q: requires construct kind %q which is not registered (registered: %s)",
					d.Name, KindCWInfra, strings.Join(reg.ConstructKinds(), ", "))
			}
			cfg := creg.NewConfig()
			if e.Cloud.CloudWatch.Kind != 0 {
				if err := strictDecode(&e.Cloud.CloudWatch, cfg, fmt.Sprintf("blueprint %q: env %q cloudwatch config", d.Name, e.Name)); err != nil {
					return nil, err
				}
			}
			r.Constructs = append(r.Constructs, ConstructInstance{Kind: KindCWInfra, Name: e.Name, Config: cfg, Fixtures: set})

			// Cloud-service constructs: AOSS, MWAA, Glue — each gated by the presence
			// of its cloud.<service> block (Kind != 0 means the YAML node was decoded).
			// Identity (Collections/Environments/Jobs) comes from the decoded Config;
			// account/region come from the cloud fixture already in set.
			cloudServices := []struct {
				kind string
				node *yaml.Node
			}{
				{KindAOSS, &e.Cloud.AOSS},
				{KindMWAA, &e.Cloud.MWAA},
				{KindGlue, &e.Cloud.Glue},
				{KindBedrock, &e.Cloud.Bedrock},
				{KindAgentCore, &e.Cloud.AgentCore},
			}
			for _, cs := range cloudServices {
				if cs.node.Kind == 0 {
					continue // block absent → construct not emitted
				}
				csreg, ok := reg.Construct(cs.kind)
				if !ok {
					return nil, fmt.Errorf("blueprint %q: requires construct kind %q which is not registered (registered: %s)",
						d.Name, cs.kind, strings.Join(reg.ConstructKinds(), ", "))
				}
				cscfg := csreg.NewConfig()
				if err := strictDecode(cs.node, cscfg, fmt.Sprintf("blueprint %q: env %q %s config", d.Name, e.Name, cs.kind)); err != nil {
					return nil, err
				}
				r.Constructs = append(r.Constructs, ConstructInstance{Kind: cs.kind, Name: e.Name, Config: cscfg, Fixtures: set})
			}
		}
		for _, dbDecl := range e.Databases {
			fx := dbs[dbDecl.Name]
			set := &fixture.Set{Seed: seed + ":" + dbDecl.Name, Env: ec.env, Cloud: ec.cloud, DB: fx}
			// Emission switch: a database fans into the CloudWatch lane and/or the
			// dbo11y lane independently (ARCHITECTURE §3). The DB fixture is built either
			// way (it is also the workload call-target identity), so a both-off DB is a
			// valid call target that emits no infra telemetry of its own.
			//
			// Engine discriminator: docdb→KindDocDB, neptune→KindNeptune, else KindRDS.
			// Note: docdb/neptune construct reads only db.Name from the fixture; the
			// buildDBFixture host/InstanceKey (rds.amazonaws.com / postgresql://) is
			// carried but unused by those constructs. A future dbo11y-for-docdb lane
			// must replace that key with the correct DocumentDB endpoint form.
			if dbDecl.Observability.cloudWatchEnabled() {
				cwKind := KindRDS
				switch dbDecl.Engine {
				case "docdb":
					cwKind = KindDocDB
				case "neptune":
					cwKind = KindNeptune
				}
				ci, err := emptyConfigInstance(reg, cwKind, dbDecl.Name, set, d.Name)
				if err != nil {
					return nil, err
				}
				r.Constructs = append(r.Constructs, *ci)
			}
			if dbDecl.Observability.dbo11yEnabled() {
				kind := KindDbo11yPostgres
				if dbDecl.Engine == "mysql" {
					kind = KindDbo11yMySQL
				}
				ci, err := emptyConfigInstance(reg, kind, dbDecl.Name, set, d.Name)
				if err != nil {
					return nil, err
				}
				r.Constructs = append(r.Constructs, *ci)
			}
		}
		for _, cDecl := range e.Caches {
			// Emission switch: the cache fixture is always built (it is the workload
			// call-target identity); the elasticache CloudWatch lane is gated, so a
			// cloudwatch:false cache is a call-target only (§3.2).
			if !cDecl.Observability.enabled() {
				continue
			}
			set := &fixture.Set{Seed: seed + ":" + cDecl.Name, Env: ec.env, Cloud: ec.cloud, Cache: caches[cDecl.Name]}
			ci, err := emptyConfigInstance(reg, KindElastiCache, cDecl.Name, set, d.Name)
			if err != nil {
				return nil, err
			}
			r.Constructs = append(r.Constructs, *ci)
		}
	}

	// Pass 3b — top-level hosts: list (a flat fleet; NO env context — hosts are global,
	// substrate-scoped). One host construct instance per declared host; within-blueprint
	// hostname uniqueness enforced here, cross-blueprint collisions in ValidateSet (load.go).
	seenHost := map[string]bool{}
	for _, hd := range d.Hosts {
		h, err := toFixtureHost(hd)
		if err != nil {
			return nil, fmt.Errorf("blueprint %q: host %q: %w", d.Name, hd.Name, err)
		}
		if seenHost[h.Hostname] {
			return nil, fmt.Errorf("blueprint %q: duplicate host name %q", d.Name, h.Hostname)
		}
		seenHost[h.Hostname] = true
		set := &fixture.Set{Seed: seed + ":host:" + h.Hostname, Host: h}
		ci, err := emptyConfigInstance(reg, KindHost, h.Hostname, set, d.Name)
		if err != nil {
			return nil, err
		}
		r.Constructs = append(r.Constructs, *ci)
	}

	// Pass 4 — top-level direct-config constructs, in two sections that share one decode
	// path: `features:` (Grafana Cloud products) and `integrations:` (external sources GC
	// ingests). `enabled` is a reserved wiring key (default true). The construct's declared
	// Group must match the section, so a mis-bucketed declaration fails loudly.
	if err := resolveSection(r, d, reg, seed, d.Features, core.GroupFeature, "feature", nil); err != nil {
		return nil, err
	}
	if err := resolveSection(r, d, reg, seed, d.Integrations, core.GroupIntegration, "integration", envs); err != nil {
		return nil, err
	}

	// workloadNames is the set of declared workload instance names, for `service:` call
	// validation. Built before Pass 5 so a service hop can reference any workload (order-free).
	workloadNames := make(map[string]struct{}, len(expanded))
	for _, ew := range expanded {
		workloadNames[ew.name] = struct{}{}
	}

	// Pass 5 — workload instances (config decode + call resolution), over the fan-out expansion.
	for _, ew := range expanded {
		w := ew.decl
		wreg, ok := reg.Workload(w.Type)
		if !ok {
			return nil, fmt.Errorf("blueprint %q: workload %q has unknown type %q (registered workloads: %s)",
				d.Name, ew.name, w.Type, strings.Join(reg.WorkloadKinds(), ", "))
		}
		cfg := wreg.NewConfig()
		if err := strictDecode(&w.Config, cfg, fmt.Sprintf("blueprint %q: workload %q config", d.Name, ew.name)); err != nil {
			return nil, err
		}
		cl := clusters[ew.runsOn]
		var calls []fixture.CallTarget
		// targetIndex maps a resolved call's target name → its index, so `via` can nest.
		targetIndex := map[string]int{}
		for _, c := range w.Calls {
			kind, ai, primary, err := classifyCall(c)
			if err != nil {
				return nil, fmt.Errorf("blueprint %q: workload %q: %w", d.Name, ew.name, err)
			}

			parent := -1
			if c.Via != "" {
				idx, ok := targetIndex[c.Via]
				if !ok {
					return nil, fmt.Errorf("blueprint %q: workload %q: call via %q must reference an earlier call's target in this workload", d.Name, ew.name, c.Via)
				}
				parent = idx
			}

			switch kind {
			case "db":
				db, ok := dbs[c.DB]
				if !ok {
					return nil, fmt.Errorf("blueprint %q: workload %q calls db %q — no such database (declared: %s)",
						d.Name, ew.name, c.DB, strings.Join(sortedKeys(dbs), ", "))
				}
				calls = append(calls, fixture.CallTarget{Kind: "db", DB: db, ParentHop: parent})
			case "cache":
				ca, ok := caches[c.Cache]
				if !ok {
					return nil, fmt.Errorf("blueprint %q: workload %q calls cache %q — no such cache (declared: %s)",
						d.Name, ew.name, c.Cache, strings.Join(sortedKeys(caches), ", "))
				}
				calls = append(calls, fixture.CallTarget{Kind: "cache", Cache: ca, ParentHop: parent})
			case "service":
				// Same-env resolution: a fanned workload's service hop prefers the same-env
				// instance "<service>-<env>" when it exists, else the global workload name.
				target := c.Service
				if ew.envName != "" {
					if _, ok := workloadNames[c.Service+"-"+ew.envName]; ok {
						target = c.Service + "-" + ew.envName
					}
				}
				if _, ok := workloadNames[target]; !ok {
					return nil, fmt.Errorf("blueprint %q: workload %q calls service %q — no such workload (declared: %s)",
						d.Name, ew.name, c.Service, strings.Join(sortedKeys(workloadNames), ", "))
				}
				calls = append(calls, fixture.CallTarget{Kind: "service", Service: target, ParentHop: parent})
			default: // AI hop — no backing fixture; identity comes from the decl + genai vocab.
				calls = append(calls, fixture.CallTarget{Kind: kind, AI: ai, ParentHop: parent})
			}
			targetIndex[primary] = len(calls) - 1
		}
		replicas := w.Replicas
		if replicas <= 0 {
			replicas = defaultReplicas
		}
		// The instance carries its env's declared databases so a workload (app db-leaf node) can
		// resolve to its env's RDS identity. The env is the workload's resolved cluster env
		// (cl.Env) — covers both the fanned (per-env cluster) and non-fanned (explicit runs_on)
		// cases; nil/unknown env ⇒ no databases.
		var envDBs []*fixture.DB
		if cl.Env != nil {
			envDBs = dbsByEnv[cl.Env.Name]
		}
		r.Workloads = append(r.Workloads, WorkloadInstance{
			Kind: w.Type, Name: ew.name, Config: cfg, Replicas: replicas,
			RUM: rumDeclared(&w.Config), Env: cl.Env, Cluster: cl, Calls: calls,
			Databases: envDBs,
		})
	}

	// Pass 6 — addressable-target inventory, scenario resolution, then incident compilation.
	//
	// Target inventory + ambiguity check (a name reused across two axes is rejected).
	r.Targets = buildTargets(d)
	axes, err := targetIndex(r.Targets)
	if err != nil {
		return nil, fmt.Errorf("blueprint %q: %w", d.Name, err)
	}
	vocab := vocabulary(reg)
	multi := failuremode.MultiAxis(vocab)

	// Validate + resolve scenarios (must precede the incident loop: a scheduled incident may
	// reference a scenario by name).
	scNames := map[string]bool{}
	for _, sc := range d.Scenarios {
		if sc.Name == "" {
			return nil, fmt.Errorf("blueprint %q: scenario with empty name", d.Name)
		}
		if scNames[sc.Name] {
			return nil, fmt.Errorf("blueprint %q: duplicate scenario %q", d.Name, sc.Name)
		}
		scNames[sc.Name] = true
		for _, e := range sc.Effects {
			if err := validateEffect(e, axes, vocab, multi); err != nil {
				return nil, fmt.Errorf("blueprint %q scenario %q: %w", d.Name, sc.Name, err)
			}
		}
		title := sc.Title
		if title == "" {
			title = sc.Name
		}
		r.Scenarios = append(r.Scenarios, ResolvedScenario{Name: sc.Name, Title: title, Summary: sc.Summary, Effects: sc.Effects})
	}

	// Incidents → shape schedule entries. A scenario-ref incident expands every effect (sharing
	// the incident's at/for); a single-mode incident validates against the inventory + vocabulary
	// then compiles, expanding any "<axis>:*" target into one entry per matching instance.
	scByName := map[string]ResolvedScenario{}
	for _, s := range r.Scenarios {
		scByName[s.Name] = s
	}
	for _, inc := range d.Incidents {
		if inc.Scenario != "" {
			if inc.Kind != "" || inc.Target != "" {
				return nil, fmt.Errorf("blueprint %q: incident sets both scenario and kind/target", d.Name)
			}
			sc, ok := scByName[inc.Scenario]
			if !ok {
				return nil, fmt.Errorf("blueprint %q: incident references unknown scenario %q", d.Name, inc.Scenario)
			}
			for _, e := range sc.Effects {
				inten := e.Intensity
				if inten <= 0 {
					inten = 1.0
				}
				for _, scope := range expandScopes(e.Target, r.Targets) {
					r.Incidents = append(r.Incidents, scheduleEntry(e.Mode, inc.At, inc.For, inten, scope))
				}
			}
			continue
		}
		// single-mode incident: validate against the inventory + vocabulary, then compile.
		if err := validateEffect(EffectDecl{Mode: inc.Kind, Target: inc.Target, Intensity: inc.Intensity}, axes, vocab, multi); err != nil {
			return nil, fmt.Errorf("blueprint %q incident %q: %w", d.Name, inc.Kind, err)
		}
		// An incident is either absolute/daily (At) or interval-recurring (Every) — never both.
		// For the interval form, emit an "every<dur>" time token (validated as a Go duration here)
		// that the shape engine recognises; At must be empty.
		timeTok := inc.At
		if inc.Every != "" {
			if inc.At != "" {
				return nil, fmt.Errorf("blueprint %q incident %q: sets both every and at (use one)", d.Name, inc.Kind)
			}
			if _, perr := time.ParseDuration(inc.Every); perr != nil {
				return nil, fmt.Errorf("blueprint %q incident %q: invalid every duration %q: %w", d.Name, inc.Kind, inc.Every, perr)
			}
			timeTok = "every" + inc.Every
		}
		for _, scope := range expandScopes(inc.Target, r.Targets) {
			r.Incidents = append(r.Incidents, scheduleEntry(inc.Kind, timeTok, inc.For, inc.Intensity, scope))
		}
	}
	for _, w := range zeroConstructWarnings(r, d) {
		log.Printf("INFO blueprint %q: %s", d.Name, w)
		r.Warnings = append(r.Warnings, w)
	}
	return r, nil
}

// zeroConstructWarnings returns one human-readable string per database or cache
// declaration that produced zero emitting constructs (call-target only). This
// happens when observability.cloudwatch:false AND observability.dbo11y:false for
// databases, or observability.cloudwatch:false for caches. The resource still
// resolves as a workload call-target; the warning is purely informational.
func zeroConstructWarnings(r *Resolved, d *Decl) []string {
	// Build index of (kind, name) pairs that have at least one emitting construct.
	type key struct{ kind, name string }
	emitting := map[key]bool{}
	for _, ci := range r.Constructs {
		switch ci.Kind {
		case KindRDS, KindDbo11yPostgres, KindDbo11yMySQL, KindDocDB, KindNeptune:
			emitting[key{"database", ci.Name}] = true
		case KindElastiCache:
			emitting[key{"cache", ci.Name}] = true
		}
	}
	var warns []string
	for _, e := range d.Environments {
		for _, db := range e.Databases {
			if !emitting[key{"database", db.Name}] {
				warns = append(warns, fmt.Sprintf("database %q is call-target only (no emitting constructs — both cloudwatch and dbo11y are off)", db.Name))
			}
		}
		for _, c := range e.Caches {
			if !emitting[key{"cache", c.Name}] {
				warns = append(warns, fmt.Sprintf("cache %q is call-target only (no emitting constructs — cloudwatch is off)", c.Name))
			}
		}
	}
	return warns
}

// buildNodes resolves the cluster's worker nodes by delegating to fixture.DeriveNodes — the SINGLE
// deterministic node-set function the constructs also call at tick time with the live pod total. It
// first carries the static node-group shapes, owning region, and bare blueprint seed onto the
// cluster fixture so constructs can re-derive an identical node set live (the node count cascades
// with the live pod count; identities are pure seed+ordinal). The baseline cl.Nodes is byte-
// identical to the previous inline loop because DeriveNodes is that loop, parameterized on pods.
//
// cl.Seed is the BARE blueprint seed (not a scoped Set.Seed): live derivation must reproduce these
// resolved identities byte-for-byte, so the construct must read the same seed buildNodes/PodName use.
func buildNodes(seed string, cl *fixture.Cluster, decl *ClusterDecl, cloud *fixture.Cloud) {
	pods := 0
	for _, w := range cl.Workloads {
		pods += w.Replicas
	}
	specs := make([]fixture.NodeGroupSpec, 0, len(decl.NodeGroups))
	for _, g := range decl.NodeGroups {
		prov := g.Provisioner
		if prov == "" {
			prov = "managed"
		}
		os := g.OS
		if os == "" {
			os = "linux"
		}
		specs = append(specs, fixture.NodeGroupSpec{Name: g.Name, InstanceType: g.InstanceType, Desired: g.Desired, Provisioner: prov, OS: os})
	}
	region := ""
	if cloud != nil {
		region = cloud.Region
	}
	cl.NodeGroups = specs
	cl.Region = region
	cl.Seed = seed
	// Apply env-weighted node floor: production clusters get a larger minimum so the resolved
	// node set is realistic even for small workloads (signals/k8s.md [slug: k8s-node-floor-env]).
	floor := fixture.EnvNodeFloor(0)
	if cl.Env != nil {
		floor = fixture.EnvNodeFloor(cl.Env.Weight)
	}
	cl.Nodes = fixture.DeriveNodesFloor(seed, cl.Name, specs, region, pods, floor)
}

// resolvePlatform expands a PlatformDecl (or nil) into the node platform identity, applying
// current-realistic defaults. The OS shorthand maps to the real os_image + container_runtime
// + node_os_info id strings; the kubernetes version drives the kubelet/build-info version.
func resolvePlatform(d *PlatformDecl) fixture.Platform {
	os, kver, kernel := "al2023", "1.31", "6.1.141"
	if d != nil {
		if d.OS != "" {
			os = d.OS
		}
		if d.KubernetesVersion != "" {
			kver = d.KubernetesVersion
		}
		if d.KernelVersion != "" {
			kernel = d.KernelVersion
		}
	}
	var osImage, osID, runtime string
	switch os {
	case "al2":
		osImage, osID, runtime = "Amazon Linux 2", "amzn", "containerd://1.7.27"
	case "bottlerocket":
		osImage, osID, runtime = "Bottlerocket OS 1.62.0 (aws-k8s-"+kver+")", "bottlerocket", "containerd://2.1.7+bottlerocket"
	default: // al2023
		osImage, osID, runtime = "Amazon Linux 2023", "amzn", "containerd://1.7.27"
	}
	return fixture.Platform{
		OSImage:           osImage,
		OSID:              osID,
		ContainerRuntime:  runtime,
		KubeletVersion:    "v" + kver + ".2-eks-f69f56f",
		KubernetesVersion: kver,
		KernelVersion:     kernel,
	}
}

// buildDBFixture resolves one database's shared identity + deterministic query
// catalogue (the estate.go pattern).
func buildDBFixture(seed string, db *DatabaseDecl, env *fixture.Env, cloud *fixture.Cloud) *fixture.DB {
	region := ""
	if cloud != nil {
		region = cloud.Region
	}
	host := fmt.Sprintf("%s.%s.%s.rds.amazonaws.com", db.Name, fixture.HexID(seed, 12, "rds", db.Name), region)
	logical := "app"
	instanceClass := db.InstanceClass
	if instanceClass == "" {
		instanceClass = "db.t3.medium"
	}
	fx := &fixture.DB{
		Engine:        db.Engine,
		EngineVersion: db.Version,
		Name:          db.Name,
		InstanceClass: instanceClass,
		ServerID:      fixture.ServerID(seed, db.Name),
		Databases:     []string{logical},
		Env:           env,
		Cloud:         cloud,
	}
	if db.Engine == "mysql" {
		fx.InstanceKey = fmt.Sprintf("tcp(%s:3306)/%s", host, logical)
	} else {
		fx.InstanceKey = fmt.Sprintf("postgresql://%s:5432/%s", host, logical)
	}
	digests := defaultDigests
	if db.Observability != nil && db.Observability.Digests > 0 {
		digests = db.Observability.Digests
	}
	fx.Queries = buildQueries(seed, db.Engine, db.Name, digests)
	return fx
}

// queryTemplates is the deterministic normalized-SQL catalogue (generic shapes; the
// ID, not the text, is the join key).
var queryTemplates = []struct {
	mysql, pg string
	tables    []string
}{
	{"SELECT * FROM users WHERE id = ?", "SELECT * FROM users WHERE id = $1", []string{"users"}},
	{"SELECT o.id, o.total FROM orders o JOIN users u ON o.user_id = u.id WHERE u.tenant_id = ?", "SELECT o.id, o.total FROM orders o JOIN users u ON o.user_id = u.id WHERE u.tenant_id = $1", []string{"orders", "users"}},
	{"UPDATE inventory SET qty = qty - ? WHERE sku = ?", "UPDATE inventory SET qty = qty - $1 WHERE sku = $2", []string{"inventory"}},
	{"SELECT count(*) FROM events WHERE created_at > ?", "SELECT count(*) FROM events WHERE created_at > $1", []string{"events"}},
	{"INSERT INTO audit_log (actor, action) VALUES (?, ?)", "INSERT INTO audit_log (actor, action) VALUES ($1, $2)", []string{"audit_log"}},
}

// buildQueries makes the per-instance catalogue; ~1 in 7 is flagged Slow (the
// realistic tail that populates slow-query/wait panels without an incident).
func buildQueries(seed, engine, instance string, n int) []fixture.Query {
	out := make([]fixture.Query, 0, n)
	for i := range n {
		t := queryTemplates[i%len(queryTemplates)]
		var id, text string
		if engine == "mysql" {
			id = fixture.MySQLDigest(seed, instance, strconv.Itoa(i))
			text = t.mysql
		} else {
			id = fixture.PostgresQueryID(seed, instance, strconv.Itoa(i))
			text = t.pg
		}
		out = append(out, fixture.Query{ID: id, Text: text, Tables: t.tables, Slow: i%7 == 0})
	}
	return out
}

// resolvePodLogsMethod resolves the pod-log collection method. When pod_logs feature is
// enabled and no explicit method is set, it defaults to "opentelemetry". An explicit
// PodLogsMethod value passes through unchanged. When pod_logs is absent/false and no
// method is set, returns "none".
func resolvePodLogsMethod(k K8sMonitoringDecl) string {
	if k.PodLogsMethod != "" {
		return k.PodLogsMethod
	}
	if k.Features["pod_logs"] {
		return "opentelemetry"
	}
	return "none"
}

// canonicalAlloyVersion normalizes the human-entered Alloy version to the
// load-bearing "v"-prefixed form (I22).
func canonicalAlloyVersion(k K8sMonitoringDecl) string {
	if !k.Alloy {
		return ""
	}
	v := k.AlloyVersion
	if v == "" {
		return defaultAlloyVersion
	}
	if !strings.HasPrefix(v, "v") {
		v = "v" + v
	}
	return v
}

// resolveSection decodes one top-level direct-config section (features|integrations) into
// construct instances. Every declared kind must be registered AND carry the section's
// Group, so a Grafana Cloud feature under `integrations:` (or an external source under
// `features:`, or a topology/add-on kind under either) is rejected with a clear error.
// noun is the singular section word used in messages ("feature"|"integration").
// envCtxs carries the per-env fixtures so an integration with `for_each_env`/`envs` fans into
// one instance per environment (each Set carries that env's identity + cloud). Pass nil to
// disable fan-out (features never fan). A declaration WITHOUT the fan-out keys keeps the legacy
// single aggregate instance (nil Env), so existing blueprints are byte-identical.
func resolveSection(r *Resolved, d *Decl, reg *core.Registry, seed string, section map[string]yaml.Node, want core.Group, noun string, envCtxs []envCtx) error {
	for _, key := range sortedNodeKeys(section) {
		node := section[key]
		enabled, rest, err := splitEnabled(&node)
		if err != nil {
			return fmt.Errorf("blueprint %q: %s %q: %w", d.Name, noun, key, err)
		}
		if !enabled {
			continue
		}
		forEach, envSubset, rest, err := splitFanout(rest)
		if err != nil {
			return fmt.Errorf("blueprint %q: %s %q: %w", d.Name, noun, key, err)
		}
		// runs_in_cluster is a reserved WIRING key (placement is done in Pass 2 — see
		// placeRunsInCluster); strip it here so the construct's own config decode never sees it.
		_, rest, err = splitDeployment(rest)
		if err != nil {
			return fmt.Errorf("blueprint %q: %s %q: %w", d.Name, noun, key, err)
		}
		creg, ok := reg.Construct(key)
		if !ok {
			return fmt.Errorf("blueprint %q: unknown %s %q (registered constructs: %s)",
				d.Name, noun, key, strings.Join(reg.ConstructKinds(), ", "))
		}
		if creg.Group != want {
			return fmt.Errorf("blueprint %q: %q cannot be declared in the %q section (its group is %q) — features = Grafana Cloud products, integrations = external sources",
				d.Name, key, noun+"s", groupLabel(creg.Group))
		}
		// mk emits one construct instance; env/cloud are nil for the aggregate (non-fanned) case.
		mk := func(env *fixture.Env, cloud *fixture.Cloud, scopeSeed, name string) error {
			cfg := creg.NewConfig()
			if err := strictDecode(rest, cfg, fmt.Sprintf("blueprint %q: %s %q config", d.Name, noun, key)); err != nil {
				return err
			}
			r.Constructs = append(r.Constructs, ConstructInstance{
				Kind: key, Name: name, Config: cfg,
				Fixtures: &fixture.Set{Seed: scopeSeed, Env: env, Cloud: cloud},
			})
			return nil
		}
		if fan := forEach || len(envSubset) > 0; !fan || len(envCtxs) == 0 {
			if err := mk(nil, nil, seed+":"+key, key); err != nil {
				return err
			}
			continue
		}
		only := envSubsetSet(envSubset)
		for _, ec := range envCtxs {
			if only != nil && !only[ec.env.Name] {
				continue
			}
			if err := mk(ec.env, ec.cloud, seed+":"+ec.env.Name+":"+key, key+"@"+ec.env.Name); err != nil {
				return err
			}
		}
	}
	return nil
}

// splitFanout extracts the reserved for_each_env (bool) + envs ([]string) fan-out keys from a
// config mapping node, returning the remainder for normal config decoding. A non-empty envs or
// for_each_env:true requests per-environment fan-out (resolveSection emits one instance per env).
func splitFanout(node *yaml.Node) (forEach bool, envs []string, rest *yaml.Node, err error) {
	rest = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	if node == nil || node.Kind == 0 {
		return false, nil, rest, nil
	}
	if node.Kind != yaml.MappingNode {
		return false, nil, node, nil
	}
	for i := 0; i+1 < len(node.Content); i += 2 {
		k, v := node.Content[i], node.Content[i+1]
		switch k.Value {
		case "for_each_env":
			if e := v.Decode(&forEach); e != nil {
				return false, nil, nil, fmt.Errorf("for_each_env: %w", e)
			}
		case "envs":
			if e := v.Decode(&envs); e != nil {
				return false, nil, nil, fmt.Errorf("envs: %w", e)
			}
		default:
			rest.Content = append(rest.Content, k, v)
		}
	}
	return forEach, envs, rest, nil
}

// runsInClusterSpec is the parsed `runs_in_cluster` wiring block on an integration: it declares the
// in-cluster deployed component (e.g. an API poller Deployment) that the integration runs as. It is
// placed as a fixture.Workload on every target env's cluster (Pass 2), independent of the
// integration construct's own config decode (the block is stripped before strictDecode).
type runsInClusterSpec struct {
	present      bool
	Name         string   `yaml:"name"`          // workload name; default = the integration key
	Namespace    string   `yaml:"namespace"`     // REQUIRED
	Replicas     int      `yaml:"replicas"`      // default defaultReplicas
	Controller   string   `yaml:"controller"`    // ""(deployment)|statefulset|daemonset
	HPA          bool     `yaml:"hpa"`           // → fixture.Workload.HasHPA
	VolumeClaims []string `yaml:"volume_claims"` // → fixture.Workload.VolumeClaims
}

// placeRunsInCluster scans every integration for a `runs_in_cluster` wiring block and places the
// declared deployed component as a fixture.Workload on each target env's cluster. The fan-out subset
// is the integration's own for_each_env/envs keys; a runs_in_cluster WITHOUT fan-out is an error (it
// has no cluster to land on). clusterByEnv maps env-name→cluster-name (only envs that declare a
// cluster); a fanned env without a cluster is skipped. Daemonset placements get Replicas=0 + no
// pod names here (per-node names are minted after buildNodes, like app-graph daemonsets).
func placeRunsInCluster(d *Decl, seed string, clusters map[string]*fixture.Cluster, clusterByEnv map[string]string) error {
	for _, key := range sortedNodeKeys(d.Integrations) {
		node := d.Integrations[key]
		enabled, rest, err := splitEnabled(&node)
		if err != nil || !enabled {
			continue // disabled / malformed integrations are reported by resolveSection
		}
		forEach, envSubset, rest, err := splitFanout(rest)
		if err != nil {
			continue
		}
		spec, _, err := splitDeployment(rest)
		if err != nil {
			return fmt.Errorf("blueprint %q: integration %q: %w", d.Name, key, err)
		}
		if !spec.present {
			continue
		}
		if !forEach && len(envSubset) == 0 {
			return fmt.Errorf("blueprint %q: integration %q: runs_in_cluster requires for_each_env or envs (it needs a cluster to run on)", d.Name, key)
		}
		if spec.Namespace == "" {
			return fmt.Errorf("blueprint %q: integration %q: runs_in_cluster requires a namespace", d.Name, key)
		}
		switch spec.Controller {
		case "", "deployment", "statefulset", "daemonset":
		default:
			return fmt.Errorf("blueprint %q: integration %q: runs_in_cluster controller %q invalid (want deployment|statefulset|daemonset)", d.Name, key, spec.Controller)
		}
		name := spec.Name
		if name == "" {
			name = key
		}
		replicas := spec.Replicas
		if replicas <= 0 {
			replicas = defaultReplicas
		}
		only := envSubsetSet(envSubset)
		for env, clName := range clusterByEnv {
			if only != nil && !only[env] {
				continue
			}
			cl := clusters[clName]
			if cl == nil {
				continue
			}
			placement := fixture.Workload{
				Name: name, Namespace: spec.Namespace, Replicas: replicas,
				Controller: spec.Controller, HasHPA: spec.HPA, VolumeClaims: spec.VolumeClaims,
			}
			// Cluster-scoped seed (seed:cluster) so a runs_in_cluster component placed on every
			// for_each_env cluster gets distinct pod hashes — see the app-pod mint in resolve().
			placement.PodNames = fixture.WorkloadPodNames(seed+":"+clName, placement, nil)
			cl.Workloads = append(cl.Workloads, placement)
		}
	}
	return nil
}

// splitDeployment extracts the reserved `runs_in_cluster` wiring key from an integration's config
// mapping, returning the parsed spec (present=false if absent) and the remainder for normal config
// decoding. Mirrors splitEnabled/splitFanout.
func splitDeployment(node *yaml.Node) (runsInClusterSpec, *yaml.Node, error) {
	var spec runsInClusterSpec
	rest := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	if node == nil || node.Kind == 0 {
		return spec, rest, nil
	}
	if node.Kind != yaml.MappingNode {
		return spec, node, nil
	}
	for i := 0; i+1 < len(node.Content); i += 2 {
		k, v := node.Content[i], node.Content[i+1]
		if k.Value == "runs_in_cluster" {
			if err := v.Decode(&spec); err != nil {
				return spec, nil, fmt.Errorf("runs_in_cluster: %w", err)
			}
			spec.present = true
			continue
		}
		rest.Content = append(rest.Content, k, v)
	}
	return spec, rest, nil
}

// envSubsetSet builds a membership set from an env-name subset (nil ⇒ all envs match).
func envSubsetSet(envs []string) map[string]bool {
	if len(envs) == 0 {
		return nil
	}
	m := make(map[string]bool, len(envs))
	for _, e := range envs {
		m[e] = true
	}
	return m
}

// groupLabel renders a construct's Group for error messages (empty = a topology/add-on
// kind, which is not declarable in a features/integrations section at all).
func groupLabel(g core.Group) string {
	if g == "" {
		return "none (topology/add-on kind — not declarable here)"
	}
	return string(g)
}

// toFixtureHost maps a HostDecl to a *fixture.Host, applying defaults + unit conversion
// and rejecting unsupported combinations. Errors are returned (not panics) so the resolver
// surfaces them as load errors.
//
// Defaults: OS "linux"; NumCPU 2; MemTotal 8 GiB; Profile "integration"; Docker false; Logs true.
// OS macos is normalised to "darwin" in the fixture. MemoryGB (GiB) → MemTotal (bytes).
// macOS + docker is rejected (no macOS cadvisor lane in v1).
func toFixtureHost(hd HostDecl) (*fixture.Host, error) {
	if hd.Name == "" {
		return nil, fmt.Errorf("host requires a name")
	}
	os := hd.OS
	if os == "" {
		os = "linux"
	}
	fxOS := os
	switch os {
	case "linux", "windows":
		// fxOS == os
	case "macos":
		fxOS = "darwin"
	default:
		return nil, fmt.Errorf("unsupported os %q (want linux|windows|macos)", hd.OS)
	}

	numCPU := hd.CPUs
	if numCPU == 0 {
		numCPU = 2
	}
	memGB := hd.MemoryGB
	if memGB == 0 {
		memGB = 8
	}
	memTotal := float64(memGB << 30)

	profile := hd.MetricsProfile
	if profile == "" {
		profile = "integration"
	}
	if profile != "integration" && profile != "full" {
		return nil, fmt.Errorf("unsupported metrics_profile %q (want integration|full)", hd.MetricsProfile)
	}

	docker := false
	logs := true
	if hd.Observability != nil {
		if hd.Observability.Docker != nil {
			docker = *hd.Observability.Docker
		}
		if hd.Observability.Logs != nil {
			logs = *hd.Observability.Logs
		}
	}
	if fxOS == "darwin" && docker {
		return nil, fmt.Errorf("observability.docker is unsupported on macos (no cadvisor lane in v1)")
	}

	return &fixture.Host{
		Hostname:  hd.Name,
		OS:        fxOS,
		PrivateIP: hd.IP,
		NumCPU:    numCPU,
		MemTotal:  memTotal,
		Profile:   profile,
		Docker:    docker,
		Logs:      logs,
		OSVersion: hd.OSVersion,
		Kernel:    hd.Kernel,
	}, nil
}

// emptyConfigInstance instantiates a topology kind (empty config; fixtures carry
// everything), validating the kind is registered.
func emptyConfigInstance(reg *core.Registry, kind, name string, set *fixture.Set, bp string) (*ConstructInstance, error) {
	creg, ok := reg.Construct(kind)
	if !ok {
		return nil, fmt.Errorf("blueprint %q: requires construct kind %q which is not registered (registered: %s)",
			bp, kind, strings.Join(reg.ConstructKinds(), ", "))
	}
	return &ConstructInstance{Kind: kind, Name: name, Config: creg.NewConfig(), Fixtures: set}, nil
}

// splitEnabled extracts the reserved `enabled` wiring key (default true) from a
// feature's mapping node, returning the remainder for config decoding.
func splitEnabled(node *yaml.Node) (bool, *yaml.Node, error) {
	enabled := true
	rest := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	if node.Kind == 0 {
		return true, rest, nil
	}
	if node.Kind != yaml.MappingNode {
		return false, nil, fmt.Errorf("feature config must be a map (line %d)", node.Line)
	}
	for i := 0; i+1 < len(node.Content); i += 2 {
		k, v := node.Content[i], node.Content[i+1]
		if k.Value == "enabled" {
			if err := v.Decode(&enabled); err != nil {
				return false, nil, fmt.Errorf("enabled: %w", err)
			}
			continue
		}
		rest.Content = append(rest.Content, k, v)
	}
	return enabled, rest, nil
}

// rumDeclared peeks at the workload config node for `rum: true` (a wiring concern —
// the runner resolves Faro credentials; the workload's own config also sees it).
// rumDeclared reports whether a workload config opts into Faro/RUM — via EITHER the web_service
// `rum: true` field OR the app workload's entry service node carrying the `rum_faro` profile (the app
// declares RUM through a frontend node's profile, not a config flag). The runner uses this to wire the
// Faro sink; it must match the app workload's own rumEnabled() (entry node has rum_faro).
func rumDeclared(cfg *yaml.Node) bool {
	if cfg == nil || cfg.Kind != yaml.MappingNode {
		return false
	}
	for i := 0; i+1 < len(cfg.Content); i += 2 {
		key, val := cfg.Content[i].Value, cfg.Content[i+1]
		switch key {
		case "rum": // web_service: rum: true
			var b bool
			_ = val.Decode(&b)
			if b {
				return true
			}
		case "services": // app workload: the ENTRY service node carries the rum_faro profile
			if val.Kind == yaml.SequenceNode && entryNodeHasRumFaro(val) {
				return true
			}
		}
	}
	return false
}

// entryNodeHasRumFaro scans an app `services:` sequence for the entry node (entry: true) and reports
// whether its `profiles:` list includes "rum_faro" — mirroring app.graph.entryHasRumFaro().
func entryNodeHasRumFaro(services *yaml.Node) bool {
	for _, svc := range services.Content {
		if svc.Kind != yaml.MappingNode {
			continue
		}
		entry, hasRumFaro := false, false
		for j := 0; j+1 < len(svc.Content); j += 2 {
			sk, sv := svc.Content[j].Value, svc.Content[j+1]
			switch sk {
			case "entry":
				_ = sv.Decode(&entry)
			case "profiles":
				if sv.Kind == yaml.SequenceNode {
					for _, p := range sv.Content {
						if p.Value == "rum_faro" {
							hasRumFaro = true
						}
					}
				}
			}
		}
		if entry && hasRumFaro {
			return true
		}
	}
	return false
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func sortedNodeKeys(m map[string]yaml.Node) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
