// SPDX-License-Identifier: AGPL-3.0-only

package webservice

import (
	"strings"
	"time"

	"github.com/rknightion/synthkit/internal/beyla"
	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/fixture"
	"github.com/rknightion/synthkit/internal/genai"
	"github.com/rknightion/synthkit/internal/ledger"
	"github.com/rknightion/synthkit/internal/shape"
	"github.com/rknightion/synthkit/internal/state"
)

// beylaLaneHelper renders this workload's ledger traffic in Beyla's eBPF-observation
// surface (RED + span-metrics/service-graph with source="beyla" + network-flow +
// target_info + avoided_services). It is driven declaratively by beyla.Emission(ctx): a
// helper per Tick, holding the resolved mode/context/feature set + the workload identity.
//
// ⚠ ABSENT-DIM TRAP: unlike the rest of synthkit, Beyla emits absent k8s/host dimensions as
// EMPTY STRING (not omitted). beylaResourceLabels honours that for the full k8s envelope —
// every key in beyla.ResourceLabelKeys(mode) is present, "" when absent (see signals/beyla.md).
type beylaLaneHelper struct {
	w        *Workload
	mode     beyla.Mode
	ctx      beyla.Context
	features map[string]bool
	em       beyla.EmissionSet
}

// beylaLane builds the per-tick Beyla lane helper from the workload's resolved config.
func (w *Workload) beylaLane() *beylaLaneHelper {
	mode := w.cfg.beylaMode()
	ctx := w.cfg.beylaContext()
	feats := map[string]bool{}
	for _, f := range w.cfg.beylaFeatures() {
		feats[f] = true
	}
	return &beylaLaneHelper{
		w:        w,
		mode:     mode,
		ctx:      ctx,
		features: feats,
		em:       beyla.Emission(ctx),
	}
}

// has reports whether a Beyla feature is configured (empty feature set means defaults were
// expanded upstream, so this is exact).
func (l *beylaLaneHelper) has(feature string) bool { return l.features[feature] }

// tick emits the Beyla observation lane for this metric tick into w.st (the caller Collects
// + writes). Volume comes from the traffic config (rps × interval), mirroring the tempo lane.
func (l *beylaLaneHelper) tick(now time.Time, world *core.World) {
	w := l.w
	shp := world.Shape

	intervalSec := interval.Seconds()
	rps := w.m.rpsAt(now, shp)
	totalCalls := ledger.StochasticRound(rps*intervalSec, shp.Float64())
	if totalCalls <= 0 && !w.nonProd {
		totalCalls = 1
	}
	routes := w.activeRoutes(now, world)

	if l.em.RED {
		l.tickRED(totalCalls, routes, shp)
	}
	if l.em.SpanMetrics {
		l.tickSpanMetrics(totalCalls, routes, shp)
	}
	if l.em.ServiceGraph {
		l.tickServiceGraph(totalCalls, shp)
	}
	if l.em.Network {
		l.tickNetwork(totalCalls)
	}
	if l.em.TargetInfo {
		l.tickTargetInfo()
	}
	if l.em.AvoidedService {
		l.tickAvoided()
	}
}

// beylaInstance is the deterministic per-target `instance` label value Beyla stamps on RED,
// span-metrics, and target_info. k8s mode → pod name (the live shape); standalone → host:port
// (the live standalone exposition form). Sourced from the workload identity, never minted.
func (l *beylaLaneHelper) beylaInstance() string {
	if l.mode == beyla.ModeStandalone {
		return l.w.name + "-host:8080"
	}
	return l.w.podName
}

// beylaNodeName returns the k8s node name (Hostname of the node this pod is placed on), or ""
// when no placement/node is resolvable (ABSENT-DIM TRAP — emitted "" not omitted).
func (l *beylaLaneHelper) beylaNodeName() string {
	w := l.w
	if w.b.Cluster == nil {
		return ""
	}
	own := w.ownPlacement()
	if own == nil || len(own.NodeIdx) == 0 {
		return ""
	}
	idx := own.NodeIdx[0]
	if idx < 0 || idx >= len(w.b.Cluster.Nodes) {
		return ""
	}
	return w.b.Cluster.Nodes[idx].Hostname
}

// beylaCloud returns the cluster's cloud identity (provider/account/region), or nil when this
// workload is not placed on a cloud-backed cluster.
func (l *beylaLaneHelper) beylaCloud() *fixture.Cloud {
	if l.w.b.Cluster == nil {
		return nil
	}
	return l.w.b.Cluster.Cloud
}

// beylaRegion returns the cloud region for the resource envelope ("" when unknown).
func (l *beylaLaneHelper) beylaRegion() string {
	if c := l.beylaCloud(); c != nil {
		return c.Region
	}
	return ""
}

// beylaAZ returns a deterministic availability zone for the resource envelope. AWS AZs are
// "<region><letter>"; we pin the first zone ("<region>a"), matching the cwinfra convention.
// "" when the region is unknown (ABSENT-DIM TRAP — emitted "" not omitted).
func (l *beylaLaneHelper) beylaAZ() string {
	r := l.beylaRegion()
	if r == "" {
		return ""
	}
	return r + "a"
}

// beylaNode returns the fixture node this pod is placed on, or nil when unresolved.
func (l *beylaLaneHelper) beylaNode() *fixture.Node {
	w := l.w
	if w.b.Cluster == nil {
		return nil
	}
	own := w.ownPlacement()
	if own == nil || len(own.NodeIdx) == 0 {
		return nil
	}
	idx := own.NodeIdx[0]
	if idx < 0 || idx >= len(w.b.Cluster.Nodes) {
		return nil
	}
	return &w.b.Cluster.Nodes[idx]
}

// beylaResourceLabels returns the per-series Beyla resource envelope for the mode. ⚠ Every
// key is PRESENT — absent dims carry "" (the ABSENT-DIM TRAP), distinct from synthkit's
// omit rule. Each call returns a fresh map (no aliasing across series).
func (l *beylaLaneHelper) beylaResourceLabels() map[string]string {
	w := l.w
	m := map[string]string{}
	for _, k := range beyla.ResourceLabelKeys(l.mode) {
		m[k] = "" // default to "" — the trap: absent dims are emitted empty, not dropped
	}
	m[beyla.LabelServiceName] = w.name
	m[beyla.LabelServiceNamespace] = w.namespace
	m[beyla.LabelInstance] = l.beylaInstance()
	if l.mode == beyla.ModeStandalone {
		m[beyla.LabelHostName] = w.name + "-host"
		m[beyla.LabelHostID] = w.podName
		return m
	}
	// k8s envelope: fill what the workload identity carries; the rest stay "".
	m[beyla.LabelK8sClusterName] = w.cluster
	m[beyla.LabelK8sNamespaceName] = w.namespace
	m[beyla.LabelK8sNodeName] = l.beylaNodeName()
	m[beyla.LabelK8sPodName] = w.podName
	m[beyla.LabelK8sDeploymentName] = w.name
	m[beyla.LabelK8sOwnerName] = w.name
	m[beyla.LabelK8sKind] = "Deployment"
	// pod uid / start time / container etc. stay "" (absent in our model).
	return m
}

// tickRED emits the Beyla RED histograms (server + per-hop client) with source="beyla".
func (l *beylaLaneHelper) tickRED(totalCalls int, routes []routeStat, shp *shape.Engine) {
	w := l.w
	if totalCalls <= 0 || len(routes) == 0 {
		return
	}
	perRoute := float64(totalCalls) / float64(len(routes))

	for _, rs := range routes {
		method, route := routeMethod(rs.route), routePath(rs.route)
		okN := boundedN(perRoute * (1 - rs.errorRate))
		errN := boundedN(perRoute * rs.errorRate)
		// server duration (200s)
		okLbls := l.redServerLabels(method, route, "200")
		for range okN {
			w.st.Observe(beyla.MetricHTTPServerDuration, okLbls, beyla.DurationBuckets, state.LEBare, 0.02+shp.Float64()*0.18)
			w.st.Observe(beyla.MetricHTTPServerReqBodySize, l.redServerLabels(method, route, "200"), beyla.SizeBuckets, state.LEBare, 64+shp.Float64()*900)
			w.st.Observe(beyla.MetricHTTPServerRespBodySize, l.redServerLabels(method, route, "200"), beyla.SizeBuckets, state.LEBare, 128+shp.Float64()*1900)
		}
		if errN > 0 {
			errLbls := l.redServerLabels(method, route, "500")
			errLbls[beyla.LabelErrorType] = "500"
			for range errN {
				w.st.Observe(beyla.MetricHTTPServerDuration, errLbls, beyla.DurationBuckets, state.LEBare, 0.05+shp.Float64()*0.5)
			}
		}
	}

	// Per-hop client RED.
	n := boundedN(float64(totalCalls))
	for _, cs := range w.m.calls {
		if cs.AI != nil {
			// gen_ai hops reuse internal/genai (operation duration + token usage).
			l.tickGenAIRED(cs, n, shp)
			continue
		}
		metric := beyla.ClientDurationMetric(cs.Kind)
		lbls := l.redClientLabels(cs.Kind, cs.Target)
		for range n {
			w.st.Observe(metric, lbls, beyla.DurationBuckets, state.LEBare, 0.005+shp.Float64()*0.1)
		}
	}
}

// tickGenAIRED emits the gen_ai client RED instruments for an AI hop (names from internal/genai).
func (l *beylaLaneHelper) tickGenAIRED(cs callSpec, n int, shp *shape.Engine) {
	w := l.w
	base := map[string]string{genai.LabelOperationName: genaiOp(cs.AI)}
	if cs.AI.Provider != "" {
		base[genai.LabelProviderName] = cs.AI.Provider
	}
	if cs.AI.Model != "" {
		base[genai.LabelRequestModel] = cs.AI.Model
		base[genai.LabelResponseModel] = cs.AI.Model
	}
	l.stampResource(base)
	for range n {
		w.st.Observe(genai.MetricClientOpDuration, cloneStr(base), genai.OpDurationBuckets, state.LEBare, 0.2+shp.Float64()*2.0)
		w.st.Observe(genai.MetricClientTTFC, cloneStr(base), genai.OpDurationBuckets, state.LEBare, 0.05+shp.Float64()*0.5)
		w.st.Observe(genai.MetricClientTimePerOutputChunk, cloneStr(base), genai.OpDurationBuckets, state.LEBare, 0.005+shp.Float64()*0.05)
	}
	if cs.AI.Model != "" {
		in := cloneStr(base)
		in[genai.LabelTokenType] = genai.TokenTypeInput
		out := cloneStr(base)
		out[genai.LabelTokenType] = genai.TokenTypeOutput
		for range n {
			w.st.Observe(genai.MetricClientTokenUsage, in, genai.TokenUsageBuckets, state.LEBare, 200+shp.Float64()*1800)
			w.st.Observe(genai.MetricClientTokenUsage, out, genai.TokenUsageBuckets, state.LEBare, 60+shp.Float64()*600)
		}
	}
}

// redServerLabels: Beyla resource envelope + HTTP server semconv labels. ⚠ No `source` label
// on RED (live ground truth — source is span-metric/service-graph/target_info only).
func (l *beylaLaneHelper) redServerLabels(method, route, status string) map[string]string {
	m := l.beylaResourceLabels()
	m[beyla.LabelHTTPRequestMethod] = method
	m[beyla.LabelHTTPRoute] = route
	m[beyla.LabelHTTPResponseStatusCode] = status
	m[beyla.LabelURLScheme] = "http"
	m[beyla.LabelServerAddress] = l.w.name
	m[beyla.LabelServerPort] = "8080"
	m[beyla.LabelJob] = "integrations/beyla"
	return m
}

// redClientLabels: Beyla resource envelope + per-kind client semconv labels. ⚠ No `source`
// label on RED (live ground truth).
func (l *beylaLaneHelper) redClientLabels(kind, target string) map[string]string {
	m := l.beylaResourceLabels()
	m[beyla.LabelJob] = "integrations/beyla"
	switch kind {
	case "db", "cache":
		m[beyla.LabelDBOperationName] = beylaDBOp(kind)
		m[beyla.LabelDBSystemName] = beylaDBSystem(kind)
	default:
		m[beyla.LabelServerAddress] = target
		m[beyla.LabelServerPort] = "8080"
		m[beyla.LabelHTTPRequestMethod] = "POST"
		m[beyla.LabelHTTPResponseStatusCode] = "200"
	}
	return m
}

// tickSpanMetrics emits the traces_spanmetrics_* families directly with source="beyla". It
// mirrors the tempo lane's label SHAPE but stamps the Beyla source + distro markers. It does
// NOT call metrics.go's spanMetricBase (that is source="tempo", SDK-lane owned).
func (l *beylaLaneHelper) tickSpanMetrics(totalCalls int, routes []routeStat, shp *shape.Engine) {
	w := l.w
	if totalCalls <= 0 || len(routes) == 0 {
		return
	}
	perRoute := float64(totalCalls) / float64(len(routes))
	for _, rs := range routes {
		okFrac := 1 - rs.errorRate
		l.emitSpanCalls(spanKindServer, rs.route, statusCodeOK, perRoute*okFrac)
		l.emitSpanCalls(spanKindServer, rs.route, statusCodeError, perRoute*rs.errorRate)
		l.observeSpanLatency(l.spanLabels(spanKindServer, rs.route, statusCodeOK), perRoute, shp)
	}
	for _, cs := range w.m.calls {
		name := clientSpanName(cs)
		l.emitSpanCalls(spanKindClient, name, statusCodeOK, float64(totalCalls))
		l.observeSpanLatency(l.spanLabels(spanKindClient, name, statusCodeOK), float64(totalCalls), shp)
	}
}

// spanMetricBase returns the Beyla span-metric label set (source=beyla). Distinct from
// metrics.go's spanMetricBase (source=tempo). ⚠ The live span-metric set carries NO
// telemetry_distro_name (unlike target_info). ⚠ ABSENT-DIM TRAP: the full live key set is
// SEEDED (absent dims emit "", never omitted — no pruneEmpty here). m7: branches on mode —
// standalone carries host/service identity, never k8s_cluster_name/k8s_namespace_name/
// k8s_node_name (standalone span shape is v:assumed — uncaptured live).
func (l *beylaLaneHelper) spanMetricBase() map[string]string {
	w := l.w
	m := map[string]string{
		"service_name":                w.name,
		"service_namespace":           w.namespace,
		"service_version":             serviceVersion,
		"deployment_environment_name": w.env,
		"instance":                    l.beylaInstance(),
		"job":                         "integrations/beyla",
		"source":                      beyla.SourceValue(),
		"telemetry_sdk_language":      backendSDKLanguage,
		"cloud_availability_zone":     l.beylaAZ(),
		"cloud_region":                l.beylaRegion(),
	}
	if l.mode == beyla.ModeStandalone {
		return m // standalone: host/service identity only, no k8s_* (v:assumed)
	}
	// k8s mode: full live envelope, absent dims seeded "" (NOT omitted).
	m["k8s_cluster_name"] = w.cluster
	m["k8s_namespace_name"] = w.namespace
	m["k8s_node_name"] = l.beylaNodeName()
	return m
}

func (l *beylaLaneHelper) spanLabels(kind, name, status string) map[string]string {
	b := l.spanMetricBase()
	b["span_kind"] = kind
	b["span_name"] = name
	b["status_code"] = status
	return b
}

func (l *beylaLaneHelper) emitSpanCalls(kind, name, status string, delta float64) {
	if delta <= 0 {
		return
	}
	lbls := l.spanLabels(kind, name, status)
	lbls["telemetry_sdk_language"] = backendSDKLanguage
	l.w.st.Add("traces_spanmetrics_calls_total", lbls, delta)
}

func (l *beylaLaneHelper) observeSpanLatency(labels map[string]string, count float64, shp *shape.Engine) {
	n := boundedN(count)
	for range n {
		l.w.st.Observe("traces_spanmetrics_latency", labels, apmLatencyBuckets, state.LEDotZero, 0.02+shp.Float64()*0.18)
	}
}

// tickServiceGraph emits the directed-edge service-graph families with source=beyla and the
// client_k8s_*/server_k8s_* prefixed keys (NOT bare k8s_*; signals/beyla.md).
func (l *beylaLaneHelper) tickServiceGraph(totalReqs int, shp *shape.Engine) {
	w := l.w
	if totalReqs <= 0 {
		return
	}
	for _, cs := range w.m.calls {
		// M3: connection_type ∈ {virtual_node, ""} ONLY (live ground truth). In-cluster edges
		// (db/cache/service hops we model as same-cluster workloads) carry "". virtual_node is
		// reserved for off-cluster/virtual targets — we have none in this model, so "" always.
		connType := beyla.ConnectionTypeEmpty
		client, clientNS := w.name, w.namespace
		if cs.ParentHop >= 0 && cs.ParentHop < len(w.m.calls) {
			p := w.m.calls[cs.ParentHop]
			client, clientNS = p.Target, p.Target
		}
		l.emitSGEdge(client, clientNS, cs.Target, cs.Target, connType, float64(totalReqs), shp)
	}
}

// sgLabels: the live service-graph label set (client/server endpoints + client_k8s_*/server_k8s_*
// prefixed keys; NO bare k8s_*; NO job — M1). ⚠ ABSENT-DIM TRAP: the full live key set is SEEDED,
// absent dims emit "" (NOT omitted — no pruneEmpty). m7: standalone carries no k8s_* prefixed keys.
func (l *beylaLaneHelper) sgLabels(client, clientNS, server, serverNS, connType string) map[string]string {
	w := l.w
	m := map[string]string{
		"client":          client,
		"server":          server,
		"connection_type": connType,
		"source":          beyla.SourceValue(),
	}
	if l.mode == beyla.ModeStandalone {
		// standalone: service-namespace endpoints only, no k8s_* prefixed keys (v:assumed).
		m["client_service_namespace"] = clientNS
		m["server_service_namespace"] = serverNS
		return m
	}
	m["client_k8s_cluster_name"] = w.cluster
	m["client_k8s_namespace_name"] = clientNS
	m["client_service_namespace"] = clientNS
	m["server_k8s_cluster_name"] = w.cluster
	m["server_k8s_namespace_name"] = serverNS
	m["server_service_namespace"] = serverNS
	return m
}

func (l *beylaLaneHelper) emitSGEdge(client, clientNS, server, serverNS, connType string, reqs float64, shp *shape.Engine) {
	lbls := l.sgLabels(client, clientNS, server, serverNS, connType)
	l.w.st.Add("traces_service_graph_request_total", lbls, reqs)
	if failed := reqs * 0.01; failed > 0 {
		l.w.st.Add("traces_service_graph_request_failed_total", lbls, failed)
	}
	n := boundedN(reqs)
	for range n {
		serverSec := 0.02 + shp.Float64()*0.18
		clientSec := serverSec + (0.001 + shp.Float64()*0.019)
		l.w.st.Observe("traces_service_graph_request_server_seconds", lbls, apmLatencyBuckets, state.LEDotZero, serverSec)
		l.w.st.Observe("traces_service_graph_request_client_seconds", lbls, apmLatencyBuckets, state.LEDotZero, clientSec)
	}
}

// tickNetwork emits beyla_network_flow_bytes_total per service→hop edge, both directions.
// Never gated (emitted in both contexts). k8s mode carries the chart-curated src/dst owner
// allow-list; standalone uses src_name/dst_name.
func (l *beylaLaneHelper) tickNetwork(totalCalls int) {
	w := l.w
	if totalCalls <= 0 {
		return
	}
	for _, cs := range w.m.calls {
		for _, dir := range []string{beyla.DirectionRequest, beyla.DirectionResponse} {
			bytes := float64(totalCalls) * 1024
			if dir == beyla.DirectionResponse {
				bytes *= 3
			}
			w.st.Add(beyla.MetricNetworkFlowBytes, l.netLabels(dir, cs.Target), bytes)
		}
	}
}

func (l *beylaLaneHelper) netLabels(dir, dst string) map[string]string {
	w := l.w
	if l.mode == beyla.ModeStandalone {
		return map[string]string{
			beyla.LabelDirection: dir,
			beyla.LabelSrcName:   w.name,
			beyla.LabelDstName:   dst,
		}
	}
	// k8s mode: chart-curated allow-list. dst owner/namespace may be "" for virtual targets
	// (ABSENT-DIM TRAP) — we model the hop target as an in-namespace workload.
	return map[string]string{
		beyla.LabelDirection:       dir,
		beyla.LabelK8sClusterName:  w.cluster,
		beyla.LabelK8sSrcName:      w.podName,
		beyla.LabelK8sSrcNamespace: w.namespace,
		beyla.LabelK8sSrcOwnerName: w.name,
		beyla.LabelK8sSrcOwnerType: "Deployment",
		beyla.LabelK8sDstName:      dst,
		beyla.LabelK8sDstNamespace: w.namespace,
		beyla.LabelK8sDstOwnerName: dst,
		beyla.LabelK8sDstOwnerType: "Deployment",
	}
}

// tickTargetInfo emits target_info + traces_target_info=1 with the Beyla origin markers.
func (l *beylaLaneHelper) tickTargetInfo() {
	lbls := l.targetInfoLabels()
	l.w.st.Set("target_info", lbls, 1)
	l.w.st.Set("traces_target_info", cloneStr(lbls), 1)
}

// targetInfoLabels: the full live target_info envelope — resource labels (incl. instance) +
// source/job + telemetry markers + the cloud_*/host_*/os_type envelope (M4). Cloud/host values
// come from the cluster's cloud identity + the placed node where available; deterministic
// placeholders otherwise (ABSENT-DIM TRAP — present, "" only when truly unresolved).
func (l *beylaLaneHelper) targetInfoLabels() map[string]string {
	m := l.beylaResourceLabels()
	m[beyla.LabelSource] = beyla.SourceValue()
	m[beyla.LabelJob] = "integrations/beyla"
	m[beyla.LabelServiceVersion] = serviceVersion
	m[beyla.LabelTelemetrySDKLanguage] = backendSDKLanguage
	m[beyla.LabelTelemetrySDKName] = beyla.SDKNameBeyla
	m[beyla.LabelTelemetrySDKVersion] = "v1.43.0"
	m[beyla.LabelTelemetryDistroName] = beyla.DistroName
	m[beyla.LabelTelemetryDistroVer] = beyla.DistroVersionUnset
	l.stampCloudHostEnvelope(m)
	return m
}

// stampCloudHostEnvelope folds the cloud_*/host_*/os_type identity onto a target_info map.
// cloud_platform=aws_ec2 / cloud_provider=aws / os_type=linux are the modelled substrate;
// account/region/zone come from the cluster cloud, host_*/host_type from the placed node.
func (l *beylaLaneHelper) stampCloudHostEnvelope(m map[string]string) {
	m[beyla.LabelCloudPlatform] = "aws_ec2"
	m[beyla.LabelCloudProvider] = "aws"
	m[beyla.LabelOSType] = "linux"
	m[beyla.LabelCloudRegion] = l.beylaRegion()
	m[beyla.LabelCloudAvailabilityZone] = l.beylaAZ()
	if c := l.beylaCloud(); c != nil {
		m[beyla.LabelCloudAccountID] = c.AccountID
	} else {
		m[beyla.LabelCloudAccountID] = ""
	}
	// host identity from the placed node (host_id = EC2 instance id, host_name = node hostname,
	// host_type = instance type). "" when the workload is not placed on a resolvable node.
	if n := l.beylaNode(); n != nil {
		m[beyla.LabelHostID] = n.InstanceID
		m[beyla.LabelHostName] = n.Hostname
		m[beyla.LabelHostType] = n.InstanceType
		m[beyla.LabelHostImageID] = beylaAMI(n.InstanceID)
	} else {
		m[beyla.LabelHostID] = ""
		m[beyla.LabelHostName] = ""
		m[beyla.LabelHostType] = ""
		m[beyla.LabelHostImageID] = ""
	}
}

// beylaAMI derives a deterministic host_image_id (AMI) placeholder from the instance id. Beyla
// surfaces the node's AMI here; we have no AMI in the fixture, so we mint a stable "ami-…"
// from the instance id suffix (deterministic, never request-scoped).
func beylaAMI(instanceID string) string {
	if instanceID == "" {
		return ""
	}
	suffix := instanceID
	if len(suffix) > 17 {
		suffix = suffix[len(suffix)-17:]
	}
	suffix = strings.TrimPrefix(suffix, "i-")
	return "ami-" + suffix
}

// tickAvoided emits beyla_avoided_services=1 for the observed service (coexist_sdk only).
func (l *beylaLaneHelper) tickAvoided() {
	w := l.w
	l.w.st.Set(beyla.MetricAvoidedServices, map[string]string{
		beyla.LabelServiceName:       w.name,
		beyla.LabelServiceNamespace:  w.namespace,
		beyla.LabelServiceInstanceID: w.podName,
		beyla.LabelTelemetryType:     "traces",
	}, 1)
}

// stampResource folds the Beyla resource envelope into a metric label map (in place).
func (l *beylaLaneHelper) stampResource(m map[string]string) {
	for k, v := range l.beylaResourceLabels() {
		m[k] = v
	}
	m[beyla.LabelSource] = beyla.SourceValue()
	m[beyla.LabelJob] = "integrations/beyla"
}

// boundedN converts a fractional volume to a bounded per-tick observation budget.
func boundedN(count float64) int {
	n := int(count)
	if n <= 0 {
		return 0
	}
	if n > 200 {
		n = 200
	}
	return n
}

// cloneStr returns an independent copy of a label map (per-series state must not alias).
func cloneStr(m map[string]string) map[string]string {
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// beylaDBOp / beylaDBSystem are content-free db-hop descriptors for the client RED labels.
func beylaDBOp(kind string) string {
	if kind == "cache" {
		return "GET"
	}
	return "SELECT"
}

func beylaDBSystem(kind string) string {
	if kind == "cache" {
		return "redis"
	}
	return "postgresql"
}
