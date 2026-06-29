// SPDX-License-Identifier: AGPL-3.0-only

// Package blueprint is the wiring layer: the YAML schema a blueprint author writes,
// the strict loader that validates it against the construct registry, and the
// topology resolver that turns declarations into shared fixtures + buildable
// instances (ARCHITECTURE §3). This package is single-owner wiring code — constructs
// and workloads never import it.
package blueprint

import (
	"fmt"

	"gopkg.in/yaml.v3"
)

// RegionDecl declares one geographic contributor to a follow-the-sun composite load curve.
// timezone is an IANA name; weight is relative (normalised across regions). Setting `regions`
// is mutually exclusive with the single `timezone` field (validated at resolve).
type RegionDecl struct {
	Name     string  `yaml:"name"`
	Timezone string  `yaml:"timezone"`
	Weight   float64 `yaml:"weight"`
}

// Metadata is optional human-facing annotation surfaced by the operator web UI. It NEVER
// affects emission — purely descriptive. Declarable at the blueprint and environment levels;
// the same shape at both. Every field is optional and free-form (no validation rules); the
// strict decoder still rejects unknown sub-keys.
type Metadata struct {
	Description string            `yaml:"description"` // free-text summary shown in the UI
	Tags        []string          `yaml:"tags"`        // free-form labels for filtering/grouping
	Owner       string            `yaml:"owner"`       // owning team/person
	Links       map[string]string `yaml:"links"`       // named external references (name → url)
	Category    string            `yaml:"category"`    // single classification (e.g. demo/reference/customer)
}

// IsZero reports whether no metadata field was set (used to omit empty payloads downstream).
func (m Metadata) IsZero() bool {
	return m.Description == "" && len(m.Tags) == 0 && m.Owner == "" && len(m.Links) == 0 && m.Category == ""
}

// Decl is the raw YAML blueprint document (strict-decoded; unknown fields fail loud).
type Decl struct {
	Name         string               `yaml:"name"`
	Label        string               `yaml:"label"`    // sink-stamped selector; defaults to Name
	Metadata     Metadata             `yaml:"metadata"` // optional human-facing annotation (UI only)
	Shape        string               `yaml:"shape"`    // default shape profile
	Timezone     string               `yaml:"timezone"` // business-hours anchor (default Europe/Zurich)
	Regions      []RegionDecl         `yaml:"regions"`  // follow-the-sun multi-tz composite (mutually exclusive with timezone)
	SeriesBudget int                  `yaml:"series_budget"`
	Environments []EnvDecl            `yaml:"environments"`
	Workloads    []WorkloadDecl       `yaml:"workloads"`
	Features     map[string]yaml.Node `yaml:"features"`     // Grafana Cloud products (sm, fleet); `enabled` reserved (default true)
	Integrations map[string]yaml.Node `yaml:"integrations"` // external sources GC ingests (cloudflare, csp_*); same decode + `enabled` key
	Incidents    []IncidentDecl       `yaml:"incidents"`
	Scenarios    []ScenarioDecl       `yaml:"scenarios"`
	Hosts        []HostDecl           `yaml:"hosts"` // traditional non-k8s machines (node/windows/macos exporter + optional docker)
}

// HostDecl declares one traditional non-Kubernetes machine running Grafana Alloy's
// node/windows/macos exporter (+ optional Docker cadvisor). Substrate-scoped; the
// hostname is the `instance` identity and must be unique across blueprints.
type HostDecl struct {
	// Name is the hostname — the `instance` label and identity. Required, unique.
	Name string `yaml:"name"`
	// OS selects the exporter vocabulary: linux | windows | macos. Default: linux.
	OS string `yaml:"os"`
	// IP is the optional private address (node_network_info / windows). Omitted when "".
	IP string `yaml:"ip"`
	// CPUs is the logical CPU count. Default: 2.
	CPUs int `yaml:"cpus"`
	// MemoryGB is total RAM in GiB. Default: 8.
	MemoryGB int `yaml:"memory_gb"`
	// MetricsProfile selects the kept metric set: integration (cost-controlled GC
	// integration allowlist) | full (broad default-Alloy surface). Default: integration.
	MetricsProfile string `yaml:"metrics_profile"`
	// OSVersion feeds *_os_info (e.g. "22.04" / "Server 2022" / "14.5"). Optional; sensible default per OS.
	OSVersion string `yaml:"os_version"`
	// Kernel feeds node_uname_info (linux/macos, e.g. "6.8.0-40-generic"). Optional; sensible default per OS.
	Kernel string `yaml:"kernel"`
	// Observability gates the docker lane and host logs.
	Observability *HostObservabilityDecl `yaml:"observability"`
}

// HostObservabilityDecl gates a host's optional emission lanes.
type HostObservabilityDecl struct {
	// Docker emits the Docker cadvisor metric lane + container log streams. Default: false.
	Docker *bool `yaml:"docker"`
	// Logs emits the host log streams (journal/winevent/file). Default: true.
	Logs *bool `yaml:"logs"`
}

// EnvDecl declares one environment (its own cloud account + optional cluster).
type EnvDecl struct {
	Name       string         `yaml:"name"`
	Weight     *float64       `yaml:"weight"`     // default 1.0
	Production *bool          `yaml:"production"` // default: name == "prod"
	Metadata   Metadata       `yaml:"metadata"`   // optional human-facing annotation (UI only)
	Cloud      *CloudDecl     `yaml:"cloud"`
	Cluster    *ClusterDecl   `yaml:"cluster"`
	Databases  []DatabaseDecl `yaml:"databases"`
	Caches     []CacheDecl    `yaml:"caches"`
}

// CloudDecl declares the env's cloud-account identity.
//
// CloudWatch is a raw node carrying the per-family emission switches for the env's
// `cw_infra` construct (ALB/NLB/EBS/NAT/EKS/S3/Firehose sub-families). It is strict-decoded
// into that construct's own config via the registry — the blueprint package never imports
// the construct (same decode path as add-ons/features). Omitting it ⇒ all families emit.
//
// AOSS, MWAA, and Glue are raw nodes carrying the identity-bearing configs for those
// cloud-service constructs (decoded into each construct's own Config via the registry).
// Omitting a block ⇒ that construct is not emitted (unlike cw_infra which always emits).
type CloudDecl struct {
	Provider    string    `yaml:"provider"` // "aws" (v1)
	AccountID   string    `yaml:"account_id"`
	Region      string    `yaml:"region"`
	VpcID       string    `yaml:"vpc_id"`
	NATGateways int       `yaml:"nat_gateways"`
	CloudWatch  yaml.Node `yaml:"cloudwatch"` // cw_infra sub-family toggles (raw; decoded via registry)
	AOSS        yaml.Node `yaml:"aoss"`       // OpenSearch Serverless config (collections:); absent ⇒ not emitted
	MWAA        yaml.Node `yaml:"mwaa"`       // Managed Workflows for Apache Airflow (environments:); absent ⇒ not emitted
	Glue        yaml.Node `yaml:"glue"`       // AWS Glue ETL (jobs:); absent ⇒ not emitted
	Bedrock     yaml.Node `yaml:"bedrock"`    // AWS Bedrock CloudWatch (models:/sub_signals:); absent ⇒ not emitted
	AgentCore   yaml.Node `yaml:"agentcore"`  // AWS Bedrock-AgentCore CloudWatch (agents:/sub_signals:); absent ⇒ not emitted
}

// ClusterDecl declares one Kubernetes cluster.
//
// Observability gates the CLOUD-PROVIDER view of the nodes — the per-node `ec2` CloudWatch
// construct — independently of `k8s_monitoring` (which configures the IN-CLUSTER agent
// substrate). `observability: { cloudwatch: false }` keeps the k8s substrate but drops the
// EC2 CloudWatch lane. Omitting it ⇒ EC2 CloudWatch emitted (default true).
type ClusterDecl struct {
	Type          string            `yaml:"type"` // "eks" (v1)
	Name          string            `yaml:"name"`
	NodeGroups    []NodeGroupDecl   `yaml:"node_groups"`
	K8sMonitoring K8sMonitoringDecl `yaml:"k8s_monitoring"`
	Observability *CloudWatchToggle `yaml:"observability"` // gates the per-node ec2 CloudWatch lane
	Addons        []AddonRef        `yaml:"addons"`
	Platform      *PlatformDecl     `yaml:"platform"` // node OS/runtime/k8s version (defaults applied when omitted)
}

// PlatformDecl declares the cluster's node OS / kubernetes version. All fields optional; the
// resolver applies current-realistic defaults. OS shorthand ∈ {al2, al2023, bottlerocket}
// expands to the real os_image + container_runtime strings (see resolvePlatform).
type PlatformDecl struct {
	OS                string `yaml:"os"`                 // "al2" | "al2023" | "bottlerocket" (default al2023)
	KubernetesVersion string `yaml:"kubernetes_version"` // e.g. "1.31" (default)
	KernelVersion     string `yaml:"kernel_version"`     // optional override of the node kernel string
}

// CloudWatchToggle is the single-lane emission switch shared by resource declarations
// whose only optional telemetry lane is CloudWatch (caches → elasticache; clusters →
// per-node ec2). Nil block or nil pointer ⇒ enabled (default true). This is the cache/
// cluster counterpart of the database's richer ObservabilityDecl (which also carries dbo11y).
type CloudWatchToggle struct {
	CloudWatch *bool `yaml:"cloudwatch"` // emit the CloudWatch lane (default true)
}

// enabled reports whether the CloudWatch lane should be built. Nil-safe: a missing block
// or missing key defaults to true (declaring the resource implies you want its cloud view).
func (t *CloudWatchToggle) enabled() bool {
	if t == nil || t.CloudWatch == nil {
		return true
	}
	return *t.CloudWatch
}

// NodeGroupDecl declares one node group. Desired 0 = derive from workload size:
// NodesNeeded = max(3, ceil(pods/8)), pods = Σ replicas of workloads bound here.
type NodeGroupDecl struct {
	Name         string `yaml:"name"`
	InstanceType string `yaml:"instance_type"`
	Desired      int    `yaml:"desired"`
	Provisioner  string `yaml:"provisioner"` // "managed" (default) | "karpenter"
	OS           string `yaml:"os"`          // ""|linux|windows ; "" defaults to linux
}

// ControlPlaneDecl gates individual Kubernetes control-plane component metrics families.
type ControlPlaneDecl struct {
	ApiServer             bool `yaml:"api_server"`
	KubeProxy             bool `yaml:"kube_proxy"`
	KubeScheduler         bool `yaml:"kube_scheduler"`
	KubeControllerManager bool `yaml:"kube_controller_manager"`
	KubeletProbes         bool `yaml:"kubelet_probes"`
}

// K8sMonitoringDecl configures the k8s-monitoring conformance layer.
type K8sMonitoringDecl struct {
	Enabled      bool   `yaml:"enabled"`
	ChartVersion string `yaml:"chart_version"`
	Alloy        bool   `yaml:"alloy"`
	AlloyVersion string `yaml:"alloy_version"` // human form ("1.16.3"); canonicalized to "v1.16.3"
	OpenCost     bool   `yaml:"opencost"`
	Kepler       bool   `yaml:"kepler"`
	// Features gates which Alloy collectors a real k8s-monitoring deploy would create.
	// Keys: cluster_metrics, cluster_events, pod_logs, node_logs, profiling,
	// application_observability. Absent/false ⇒ that collector role is not deployed.
	Features map[string]bool `yaml:"features"`
	// MetricsReplicas is the alloy-metrics StatefulSet replica count (does NOT scale with
	// nodes). 0 ⇒ default 1.
	MetricsReplicas int `yaml:"metrics_replicas"`
	// ReceiverAsDaemonset models alloy-receiver as a per-node DaemonSet instead of the
	// synth default (a single Deployment).
	ReceiverAsDaemonset bool `yaml:"receiver_as_daemonset"`
	// FleetManagement, when true, registers this cluster's Alloy collectors with the FM API
	// (a fleet_management construct instance is emitted from the cluster path). Requires Enabled.
	FleetManagement bool `yaml:"fleet_management"`
	// ControlPlane gates individual control-plane component metric families.
	ControlPlane ControlPlaneDecl `yaml:"control_plane"`
	// PodLogsMethod selects the pod-log collection mechanism. "" with pod_logs feature enabled
	// defaults to "opentelemetry"; explicit values pass through unchanged; absent pod_logs ⇒ "none".
	PodLogsMethod string `yaml:"pod_logs_method"` // ""|opentelemetry|kubernetes_api|loki|objects|none
}

// AddonRef references an add-on construct by registry kind, with optional config.
// YAML forms: a bare scalar (`- core_dns`) or a map with a `name` key whose remaining
// keys are the add-on's own config (`- { name: cluster_autoscaler, max_nodes: 10 }`).
type AddonRef struct {
	Name   string
	Config yaml.Node // mapping of the remaining keys; strict-decoded against the registry
}

// UnmarshalYAML implements the scalar-or-map form.
func (a *AddonRef) UnmarshalYAML(node *yaml.Node) error {
	switch node.Kind {
	case yaml.ScalarNode:
		a.Name = node.Value
		a.Config = yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		return nil
	case yaml.MappingNode:
		rest := yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		for i := 0; i+1 < len(node.Content); i += 2 {
			k, v := node.Content[i], node.Content[i+1]
			if k.Value == "name" {
				if err := v.Decode(&a.Name); err != nil {
					return fmt.Errorf("addon name: %w", err)
				}
				continue
			}
			rest.Content = append(rest.Content, k, v)
		}
		if a.Name == "" {
			return fmt.Errorf("addon map form requires a `name` key (line %d)", node.Line)
		}
		a.Config = rest
		return nil
	default:
		return fmt.Errorf("addon must be a string or a map (line %d)", node.Line)
	}
}

// DatabaseDecl declares one database instance. The name is the cross-product join
// key (cloud resource name == dbo11y db_instance_identifier).
type DatabaseDecl struct {
	Engine        string             `yaml:"engine"` // "postgres" | "mysql"
	Version       string             `yaml:"version"`
	Name          string             `yaml:"name"`
	InstanceClass string             `yaml:"instance_class"` // e.g. "db.t3.medium"; empty → resolver default
	Observability *ObservabilityDecl `yaml:"observability"`  // nil = CloudWatch only (the defaults)
}

// ObservabilityDecl independently toggles the two telemetry lanes a database fans into —
// the "emission switch". A `databases:` entry can emit the cloud-side CloudWatch family,
// the in-database dbo11y agent family, both, or neither (a neither-DB still exists as a
// workload call-target: its db.* spans/identity resolve, it just emits no infra telemetry
// of its own). This is the canonical pattern for "some blueprints emit X, some don't" —
// the blueprint gates which CONSTRUCTS a resource declaration fans into; the constructs
// themselves stay isolated and unconditional (ARCHITECTURE §3).
//
// Defaults make the block optional and back-compatible: omitting it ⇒ CloudWatch only.
type ObservabilityDecl struct {
	CloudWatch *bool `yaml:"cloudwatch"` // emit aws_rds_* CloudWatch family (default true)
	Dbo11y     bool  `yaml:"dbo11y"`     // emit database_observability_* lane (default false)
	Digests    int   `yaml:"digests"`    // dbo11y query-catalogue size (default 40); ignored unless dbo11y
}

// cloudWatchEnabled reports whether the RDS CloudWatch lane should be built. Nil receiver
// (no observability block) and nil pointer both default to true.
func (o *ObservabilityDecl) cloudWatchEnabled() bool {
	if o == nil || o.CloudWatch == nil {
		return true
	}
	return *o.CloudWatch
}

// dbo11yEnabled reports whether the dbo11y lane should be built (default false).
func (o *ObservabilityDecl) dbo11yEnabled() bool {
	return o != nil && o.Dbo11y
}

// CacheDecl declares one cache cluster. Observability gates the elasticache CloudWatch
// lane; `observability: { cloudwatch: false }` makes the cache a workload call-target only
// (its redis span identity resolves, no CloudWatch series). Omitting it ⇒ emitted.
type CacheDecl struct {
	Engine        string            `yaml:"engine"` // "redis"
	Version       string            `yaml:"version"`
	Name          string            `yaml:"name"`
	InstanceClass string            `yaml:"instance_class"` // e.g. "cache.r6g.large"; empty → resolver default
	Observability *CloudWatchToggle `yaml:"observability"`
}

// CallDecl is one downstream dependency of a workload. Exactly ONE primary key is set:
// db/cache/service OR an AI hop kind (gateway/llm/agent/tool/workflow/retrieval). Provider
// + Model are AI MODIFIERS (gen_ai.provider.name / the routed model on a gateway hop) — not
// counted in the one-of. Via names another call in the same workload's list (by its target
// name: the db/cache/service name, or the AI hop's primary value) that this hop nests under;
// unset = a direct child of the backend SERVER span.
type CallDecl struct {
	DB      string `yaml:"db"`
	Cache   string `yaml:"cache"`
	Service string `yaml:"service"`
	// AI hop kinds (Spec 2b). The value is the hop's subject/identity: gateway=gateway name,
	// llm=model id, agent/tool/workflow/retrieval=that resource's name.
	Gateway   string `yaml:"gateway"`
	LLM       string `yaml:"llm"`
	Agent     string `yaml:"agent"`
	Tool      string `yaml:"tool"`
	Workflow  string `yaml:"workflow"`
	Retrieval string `yaml:"retrieval"`
	Provider  string `yaml:"provider"` // AI modifier: gen_ai.provider.name (gateway/llm hops)
	Model     string `yaml:"model"`    // AI modifier: the routed model id on a gateway hop
	Via       string `yaml:"via"`
}

// WorkloadDecl declares one workload instance. The wiring keys (type, name, runs_on,
// replicas, calls) are extracted here; every remaining key is the workload kind's own
// config, strict-decoded against the registry.
type WorkloadDecl struct {
	Type     string
	Name     string
	RunsOn   string
	Replicas int // pods driving node derivation; default 2
	Calls    []CallDecl
	// ForEachEnv fans this workload across environments: the resolver emits one instance per
	// target env, bound to that env's cluster, named "<name>-<env>". A fanned workload sets no
	// runs_on (it binds per-env) and may only declare AI/service calls (not db/cache).
	ForEachEnv bool
	// Envs optionally restricts the fan-out to a named env subset (empty + ForEachEnv ⇒ all envs
	// that declare a cluster). A non-empty Envs implies fan-out even without ForEachEnv.
	Envs   []string
	Config yaml.Node
}

// UnmarshalYAML splits wiring keys from workload config.
func (w *WorkloadDecl) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind != yaml.MappingNode {
		return fmt.Errorf("workload must be a map (line %d)", node.Line)
	}
	rest := yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	for i := 0; i+1 < len(node.Content); i += 2 {
		k, v := node.Content[i], node.Content[i+1]
		var err error
		switch k.Value {
		case "type":
			err = v.Decode(&w.Type)
		case "name":
			err = v.Decode(&w.Name)
		case "runs_on":
			err = v.Decode(&w.RunsOn)
		case "replicas":
			err = v.Decode(&w.Replicas)
		case "calls":
			err = v.Decode(&w.Calls)
		case "for_each_env":
			err = v.Decode(&w.ForEachEnv)
		case "envs":
			err = v.Decode(&w.Envs)
		default:
			rest.Content = append(rest.Content, k, v)
		}
		if err != nil {
			return fmt.Errorf("workload %s: %w", k.Value, err)
		}
	}
	w.Config = rest
	return nil
}

// IncidentDecl schedules one time-boxed incident. Compiled into a shape-engine
// schedule entry `kind@at/for[#intensity][@target]`.
type IncidentDecl struct {
	Kind     string `yaml:"kind"`
	Scenario string `yaml:"scenario"` // if set, schedules a whole ScenarioDecl; Kind/Target must be empty
	Target   string `yaml:"target"`   // workload/cluster/db/cache instance name ("" = blueprint-wide)
	At       string `yaml:"at"`       // RFC3339 or "2006-01-02T15:04[:05]" (blueprint timezone)
	// Every, when set, makes the incident INTERVAL-RECURRING: it fires for For out of every Every
	// (a Go duration, e.g. "10m"), repeating continuously and anchored on the Unix epoch. At is
	// ignored and must be empty when Every is set (load rejects setting both).
	Every     string  `yaml:"every"`
	For       string  `yaml:"for"` // Go duration ("20m")
	Intensity float64 `yaml:"intensity"`
}

// ScenarioDecl is a named, reusable, correlated incident bundle. It can be fired by schedule (an
// IncidentDecl referencing it) or live via the control plane.
type ScenarioDecl struct {
	Name    string       `yaml:"name"`
	Title   string       `yaml:"title"`   // human display; defaults to Name
	Summary string       `yaml:"summary"` // one-line "what it causes"
	Effects []EffectDecl `yaml:"effects"`
}

// EffectDecl is one (mode, target, intensity) tuple within a scenario.
//
// Target grammar: a declared instance name | "<axis>:*" (all instances of an axis) | "" (the
// mode's sole axis; rejected at load for a multi-axis mode).
type EffectDecl struct {
	Mode      string  `yaml:"mode"`
	Target    string  `yaml:"target"`
	Intensity float64 `yaml:"intensity"` // [0,1]; default 1.0 when <= 0
}
