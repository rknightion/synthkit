// SPDX-License-Identifier: AGPL-3.0-only

// Package fixture is the shared-identity vocabulary of synthkit: the resolved,
// DETERMINISTIC fixtures the composition root hands to construct and workload
// instances. Fixtures are how two constructs agree on identity WITHOUT importing each
// other — the EC2-CloudWatch construct and the k8s construct both render the same
// []Node; the RDS construct and the dbo11y construct both render the same DB.Name.
//
// FROZEN SEAM (Phase 0). A change here is a high-blast-radius wiring event: every
// catalog lane codes against these types. Constructs must treat absent (nil/empty)
// fields as "not applicable" and OMIT the corresponding dimension entirely — never
// emit "" or a sentinel (ARCHITECTURE I13).
//
// Determinism: same blueprint → same identities every run. All derived identity comes
// from sha256 of a stable seed string ("<blueprint>:<path>") via the helpers in
// seed.go; request-scoped IDs (ledger) are random per run by design.
package fixture

// Env is one declared environment's identity + traffic shaping parameters.
type Env struct {
	Name    string  // "prod", "staging" — emitted as deployment_environment etc.
	Weight  float64 // traffic weight (prod ≫ staging ≫ dev)
	NonProd bool    // weekend traffic scales toward zero (shape.Factor)
}

// Cloud is the cloud-account identity every construct in an environment shares: the
// VPC tie that makes EKS + EC2 + NAT-GW + ALB + RDS + ElastiCache one coherent,
// cross-joinable estate.
type Cloud struct {
	Provider      string   // "aws"
	AccountID     string   // CloudWatch account_id label
	Region        string   // CloudWatch region label
	VpcID         string   // tag_VpcId on *_info series
	NATGatewayIDs []string // resolved "nat-…" ids (dimension_NatGatewayId), one per declared gateway
}

// Node is one worker node — simultaneously a Kubernetes node and an EC2 instance.
// THE EC2↔EKS correlation: InstanceID appears as dimension_InstanceId on aws_ec2_*
// AND as the node's instance identity in the k8s substrate; Hostname is the k8s node
// name (node-exporter/cAdvisor instance).
type Node struct {
	InstanceID   string // "i-0123456789abcdef0" (deterministic)
	Hostname     string // "ip-10-0-1-23.<region>.compute.internal"
	PrivateIP    string // "10.0.1.23"
	InstanceType string // "m6i.xlarge"
	NodeGroup    string // owning node-group name
	UID          string // k8s node UID (deterministic UUID) — kube_*/opencost `uid` label
	Provisioner  string // "managed" | "karpenter" — selects the kube_node_labels provisioner label
	OS           string // "linux" (default) | "windows"
}

// Workload is the placement of one workload instance onto a cluster: the pod identity
// shared by the k8s substrate (kube_pod_*, cAdvisor) and the workload's own
// target_info/resource attributes, so service→pod joins resolve.
type Workload struct {
	Name      string // workload instance name (service.name)
	Namespace string // k8s namespace
	Replicas  int
	PodNames  []string // deterministic, len == Replicas
	NodeIdx   []int    // pod→node placement: index into Cluster.Nodes, len == Replicas
	// Runtime is the pod's language runtime (go|jvm|node|python; "" = unknown/omitted, I13). It is
	// the shared pod-runtime identity any pod-aware construct joins on — set from an app
	// ServiceNode's runtime; "" for web_service pods (runtime-agnostic). A runtime-discriminated
	// collector (e.g. pprof/JVM profiling) keys off it; eBPF/process_cpu is runtime-agnostic.
	Runtime string
	// Resources optionally overrides this workload's container resource requests/limits and the
	// cAdvisor usage base (blueprint-declared `resources:` on an app service node). nil (or any
	// zero field) falls back to the k8scluster construct's deterministic per-workload defaults, so
	// this is fully back-compatible — an omitted block changes nothing.
	Resources *WorkloadResources
	// Controller is the k8s controller kind that owns this workload's pods: "" (⇒ "deployment"),
	// "statefulset", or "daemonset". It selects the KSM family (kube_<controller>_*), the pod-name
	// form (Deployment <name>-<rshash>-<podhash> / StatefulSet <name>-<ordinal> / DaemonSet
	// <name>-<5hex> one-per-node), and kube_pod_owner.owner_kind. "" reproduces today's output
	// byte-for-byte (every existing placement is a Deployment). A daemonset's Replicas is IGNORED
	// (count = schedulable node count) and is zeroed at resolve time so it never inflates node demand.
	Controller string
	// Container is the metrics container name for per-pod metric correlation; default = Name.
	// Set by the addon catalog for constructs whose main container name differs from the workload
	// name (e.g. karpenter deploy "karpenter" → container "controller"). Addon constructs stamp
	// this value as the `container` label so it joins kube_pod_container_info. "" ⇒ use Name.
	Container string
	// HasHPA requests a kube_horizontalpodautoscaler_* family for this workload (Deployment/
	// StatefulSet only — never DaemonSet). Default false: real clusters scope HPAs to a subset of
	// Deployments, not every workload.
	HasHPA bool
	// VolumeClaims are the PVC template names this workload mounts. nil ⇒ no PVC. StatefulSet ⇒ one
	// PVC per (template, ordinal) named "<template>-<name>-<ordinal>"; Deployment ⇒ one shared PVC
	// per template named "<template>-<name>". Stateless workloads omit this.
	VolumeClaims []string
}

// PodIdentity is one pod's join key: the labels an in-cluster construct stamps on its
// own metrics so they correlate with the cluster's kube_pod_*/cadvisor series.
type PodIdentity struct {
	Pod       string // kube_pod_info.pod
	Namespace string // kube_pod_info.namespace
	Node      string // kube_pod_info.node
	PodIP     string // kube_pod_info.pod_ip; metric instance = PodIP:port
	Container string // cadvisor/pod_container_info container
}

// PodIdentities returns one identity per pod of this workload. PodNames are the source
// of truth (minted by WorkloadPodNames); NodeIdx maps each pod to a node. PodIP reuses
// the SAME formula k8scluster's kube_pod_info uses (PodIP, extracted in 0.2) so a
// construct's `instance` label is byte-consistent with the pod's kube_pod_info.pod_ip.
func (w Workload) PodIdentities(nodes []Node) []PodIdentity {
	c := w.Container
	if c == "" {
		c = w.Name
	}
	out := make([]PodIdentity, 0, len(w.PodNames))
	for i, pn := range w.PodNames {
		node, nodeIdx := "", 0
		switch {
		case i < len(w.NodeIdx) && w.NodeIdx[i] >= 0 && w.NodeIdx[i] < len(nodes):
			nodeIdx = w.NodeIdx[i]
		case len(nodes) > 0:
			// NodeIdx not populated (substrate addons mint PodNames at resolve time with
			// nil NodeIdx). Derive placement via the SAME formula k8scluster uses for these
			// pods (ksm.go: nodeAssignment(ns, workloadName, ri, numNodes)) so the addon's
			// `node` label and `instance` podIP are byte-identical to kube_pod_info.
			nodeIdx = nodeAssignment(w.Namespace, w.Name, i, len(nodes))
		}
		if nodeIdx < len(nodes) {
			node = nodes[nodeIdx].Hostname
		}
		out = append(out, PodIdentity{
			Pod: pn, Namespace: w.Namespace, Node: node,
			PodIP: PodIP(nodeIdx, w.Namespace, pn, i), Container: c,
		})
	}
	return out
}

// WorkloadResources optionally overrides a workload's container resource requests/limits and the
// cAdvisor usage base. Zero fields fall back to the k8scluster construct's per-workload defaults.
type WorkloadResources struct {
	CPURequest   float64 `yaml:"cpu_request"`    // cores; 0 → default
	CPULimit     float64 `yaml:"cpu_limit"`      // cores; 0 → default
	MemRequest   float64 `yaml:"mem_request"`    // bytes; 0 → default
	MemLimit     float64 `yaml:"mem_limit"`      // bytes; 0 → default
	CPUUsageBase float64 `yaml:"cpu_usage_base"` // cores; peak-factor CPU usage target; 0 → default
}

// NodeGroupSpec is a cluster node group's static shape, carried in the fixture so constructs can
// re-derive the node set live (node count cascades with live pod count). Desired 0 = derive.
type NodeGroupSpec struct {
	Name         string
	InstanceType string
	Desired      int
	Provisioner  string // "managed" (label_eks_amazonaws_com_nodegroup) | "karpenter" (label_karpenter_sh_nodepool); default managed
	OS           string // "linux" (default) | "windows"
}

// Platform is a cluster's node OS/runtime/version identity — the values that flow to
// kube_node_info, node_uname_info, node_os_info, kubernetes_build_info, and the kubelet
// version. Blueprint-declared (resolver applies defaults when omitted).
type Platform struct {
	OSImage           string // kube_node_info.os_image, e.g. "Bottlerocket OS 1.62.0 (aws-k8s-1.35)" / "Amazon Linux 2023"
	OSID              string // node_os_info.id, e.g. "bottlerocket" / "amzn"
	ContainerRuntime  string // kube_node_info.container_runtime_version, e.g. "containerd://2.1.7+bottlerocket"
	KubeletVersion    string // kube_node_info.kubelet_version, e.g. "v1.35.2-eks-f69f56f"
	KubernetesVersion string // kubernetes_build_info git_version base, e.g. "1.35"
	KernelVersion     string // node_uname_info.release / kube_node_info.kernel_version, e.g. "6.12.88"
}

// ControlPlane carries the per-component control-plane metric gates.
type ControlPlane struct {
	ApiServer, KubeProxy, KubeScheduler, KubeControllerManager, KubeletProbes bool
}

// K8sMonitoring is the conformance configuration for lighting up the real
// k8s-monitoring app: exact discovery/join series with the right versions
// (ARCHITECTURE I22). AlloyVersion is CANONICAL "v"-prefixed form (e.g. "v1.16.3") —
// the resolver normalizes human input; constructs emit it verbatim.
type K8sMonitoring struct {
	Enabled      bool
	ChartVersion string // e.g. "4.1.4"
	Alloy        bool
	AlloyVersion string // canonical, "v"-prefixed
	OpenCost     bool
	Kepler       bool

	// Features gates which Alloy collectors a real k8s-monitoring deploy would create.
	// Keys: cluster_metrics, cluster_events, pod_logs, node_logs, profiling,
	// application_observability. Absent/false ⇒ that collector role is not deployed.
	Features map[string]bool
	// MetricsReplicas is the alloy-metrics StatefulSet replica count (does NOT scale with
	// nodes). 0 ⇒ default 1.
	MetricsReplicas int
	// ReceiverAsDaemonset models alloy-receiver as a per-node DaemonSet instead of the
	// synth default (a single Deployment).
	ReceiverAsDaemonset bool
	// FleetManagement, when true, registers this cluster's Alloy collectors with the FM API
	// (a fleetmgmt construct instance is emitted from the cluster path). Requires Enabled.
	FleetManagement bool
	// ControlPlane gates individual control-plane component metric families.
	ControlPlane ControlPlane
	// PodLogsMethod is the pod-log collection mechanism. "" with pod_logs feature enabled
	// defaults to "opentelemetry"; absent pod_logs ⇒ "none".
	PodLogsMethod string
}

// Cluster is one resolved Kubernetes cluster: nodes (= EC2 instances), the workloads
// placed on it, and its conformance config. Cluster.Name is the `cluster` /
// `k8s_cluster_name` label value — the substrate disambiguator (ARCHITECTURE I21) —
// and must be unique across enabled blueprints (validated at load).
type Cluster struct {
	Name          string
	Type          string // "eks"
	Env           *Env
	Cloud         *Cloud
	Nodes         []Node
	Workloads     []Workload
	K8sMonitoring K8sMonitoring
	NodeGroups    []NodeGroupSpec // static node-group shapes, for live node-count derivation
	Region        string          // owning cloud region (live node hostname derivation)
	Seed          string          // cluster identity seed (deterministic live node minting)
	Platform      Platform        // node OS/runtime/version identity (blueprint-declared; resolver-defaulted)
	// Addons is the list of addon construct names declared on this cluster (e.g.
	// "load_balancer_controller", "ebs_csi"). Used by k8scluster to populate the
	// kube-system KSM inventory with addon-derived deployments/daemonsets.
	// Populated by the resolver from blueprint ClusterDecl.Addons.
	Addons []string
	// SubstrateWorkloads holds addon/baseline pods (cert-manager, coredns, karpenter, …):
	// read by k8scluster for pod-shape AND by addon constructs for metric correlation.
	// EXCLUDED from node-count summers / k8sprofiling / ksmingress, which iterate only
	// Workloads. Populated by the resolver from the addon catalog (Task 0.4).
	SubstrateWorkloads []Workload
}

// DB is one database instance's shared identity: Name is simultaneously the RDS
// dimension_DBInstanceIdentifier AND the dbo11y connection_info.db_instance_identifier
// — the operator pivots cloud↔dbo11y on this single value (the estate.go pattern).
type DB struct {
	Engine        string // "mysql" | "postgres"
	EngineVersion string // e.g. "16.2"
	Name          string // THE cross-product join key
	InstanceClass string // RDS instance class e.g. "db.t3.medium" (blueprint-declared; resolver-defaulted)
	ServerID      string // 64-hex sha256 (dbo11y server_id)
	InstanceKey   string // dbo11y `instance` label (mysql "tcp(host:3306)/db"; pg "postgresql://host:5432/db")
	Databases     []string
	Queries       []Query // deterministic query catalogue (digest cardinality knob)
	Env           *Env
	Cloud         *Cloud
}

// Query is one entry in a DB's deterministic query catalogue. ID is the cross-signal
// join key: MySQL → 64-hex performance_schema digest; Postgres → int64 decimal string
// (queryid). The SAME ID must appear on the metric and every log op for that query.
type Query struct {
	ID     string
	Text   string   // normalized SQL (mysql "?" placeholders; postgres "$N")
	Tables []string // referenced tables
	Slow   bool     // part of the realistic slow-query tail
}

// Cache is one cache cluster's shared identity (dimension_CacheClusterId).
type Cache struct {
	Engine        string // "redis"
	EngineVersion string
	Name          string // dimension_CacheClusterId
	InstanceClass string // ElastiCache node type e.g. "cache.r6g.large" (blueprint-declared; resolver-defaulted)
	NodeIDs       []string
	Env           *Env
	Cloud         *Cloud
}

// AI hop kinds (Spec 2b) — the typed gen_ai / agent-workflow downstream hops a workload
// can make, composable with "db"/"cache"/"service" and nestable via ParentHop. Each stamps
// gen_ai.* span attrs (internal/genai) on the workload side; identity rides in AICall.
const (
	KindLLMGateway = "llm_gateway" // LLM gateway/proxy (e.g. Portkey); emits a connected SERVER span (Path-B)
	KindLLMModel   = "llm_model"   // model inference hop (op=chat)
	KindAgent      = "agent"       // op=invoke_agent
	KindTool       = "tool"        // op=execute_tool
	KindWorkflow   = "workflow"    // op=invoke_workflow
	KindRetrieval  = "retrieval"   // op=retrieval
)

// IsAIKind reports whether kind is one of the AI hop kinds (carries an AICall).
func IsAIKind(kind string) bool {
	switch kind {
	case KindLLMGateway, KindLLMModel, KindAgent, KindTool, KindWorkflow, KindRetrieval:
		return true
	}
	return false
}

// AICall carries the gen_ai identity of an AI hop (CallTarget.AI; nil for db/cache/service).
// Op is the genai operation value (chat, invoke_agent, …); Subject is the SpanName subject
// (agent/tool/workflow/data-source name); Model/Provider apply to gateway/model hops. The
// workload maps this to ledger.AICall (neither base package imports the other).
type AICall struct {
	Op       string // genai operation value
	Provider string // gen_ai.provider.name (gateway/model hops)
	Model    string // gen_ai.request.model (gateway/model hops)
	Subject  string // SpanName subject (agent/tool/workflow/data-source name)
}

// CallTarget is a workload's resolved downstream dependency (the `calls:` wiring). Exactly
// one of DB/Cache/Service/AI is set per the Kind (AI for any IsAIKind kind).
type CallTarget struct {
	Kind    string  // "db" | "cache" | "service" | one of the AI hop kinds
	DB      *DB     // set when Kind=="db"
	Cache   *Cache  // set when Kind=="cache"
	Service string  // callee workload name, set when Kind=="service"
	AI      *AICall // set when IsAIKind(Kind)
	// ParentHop is the index (into the workload's resolved Calls) of the hop this one
	// nests under; -1 = direct child of the backend SERVER span (the default / `via` unset).
	ParentHop int
}

// Set is the fixture bundle handed to a construct instance at Build. The composition
// root populates only the fields relevant to the instance's placement; everything else
// is nil. Seed is an opaque stable seed for any ADDITIONAL identity the construct must
// derive (hash it via seed.go helpers — never invent identity ad hoc, and never parse
// Seed's contents).
type Set struct {
	Seed    string
	Env     *Env
	Cloud   *Cloud
	Cluster *Cluster
	DB      *DB    // db-scoped constructs (rds, dbo11y_*): the one instance to render
	Cache   *Cache // cache-scoped constructs (elasticache)
	DBs     []*DB  // env/estate-scoped multi-instance views
	Caches  []*Cache
	Host    *Host // host-scoped construct (one instance per declared host)
}
