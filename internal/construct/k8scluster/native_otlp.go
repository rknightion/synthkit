// SPDX-License-Identifier: AGPL-3.0-only

package k8scluster

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/fixture"
	"github.com/rknightion/synthkit/internal/sink/otlp"
)

const nativeCollectorVersion = "0.171.0"

var containerStatusReasons = []string{
	"Completed", "ContainerCannotRun", "ContainerCreating", "CrashLoopBackOff",
	"CreateContainerConfigError", "ErrImagePull", "Error", "ImagePullBackOff", "OOMKilled",
}

// nativeOTLPState is deliberately separate from the Prometheus state store. The native receiver
// permutation has its own names and cumulative instruments, and enabling it must not perturb the
// existing promrw state or random draw order. Construct ticks are serialized by the runner.
type nativeOTLPState struct {
	start    time.Time
	counters map[string]float64
}

func newNativeOTLPState() *nativeOTLPState {
	return &nativeOTLPState{counters: map[string]float64{}}
}

func (s *nativeOTLPState) begin(now time.Time) time.Time {
	if s.start.IsZero() {
		s.start = now
	}
	return s.start
}

func (s *nativeOTLPState) add(key string, delta float64) float64 {
	s.counters[key] += delta
	return s.counters[key]
}

type nativePod struct {
	wl        *fixture.Workload
	namespace string
	name      string
	container string
	node      fixture.Node
	ordinal   int
}

func (c *Construct) tickOTLPMetrics(
	ctx context.Context,
	now time.Time,
	cl *fixture.Cluster,
	nodes []fixture.Node,
	replicas int,
	factor, tickSec float64,
	w *core.World,
) error {
	if !c.otelMetrics || w == nil || w.OTLPMetrics == nil {
		return nil
	}
	start := c.otlpState.begin(now)
	resources := make([]otlp.MetricResource, 0)

	for _, pod := range nativePods(cl, nodes, replicas) {
		resources = append(resources,
			c.kubeletPodResource(now, start, pod, factor, tickSec),
			c.clusterPodResource(now, start, cl, pod),
		)
	}
	resources = append(resources, c.kubeletNodeResources(now, start, cl, nodes, factor, tickSec)...)
	resources = append(resources, clusterObjectResources(now, cl, nodes, replicas)...)
	return w.OTLPMetrics.Write(ctx, resources)
}

func nativePods(cl *fixture.Cluster, nodes []fixture.Node, replicas int) []nativePod {
	if len(nodes) == 0 {
		return nil
	}
	byKey := map[string]*fixture.Workload{}
	for i := range cl.Workloads {
		wl := &cl.Workloads[i]
		byKey[wl.Namespace+"\x00"+wl.Name] = wl
	}
	for i := range cl.SubstrateWorkloads {
		wl := &cl.SubstrateWorkloads[i]
		byKey[wl.Namespace+"\x00"+wl.Name] = wl
	}

	var out []nativePod
	for namespace, deployments := range workloadDeployments(cl) {
		for _, deployment := range deployments {
			wl := byKey[namespace+"\x00"+deployment]
			count := substrateReps(wl, replicas, len(nodes))
			for ordinal := 0; ordinal < count; ordinal++ {
				podName := synthPodName(deployment, ordinal)
				nodeIdx := nodeAssignment(namespace, deployment, ordinal, len(nodes))
				container := deployment
				if wl != nil {
					if ordinal < len(wl.PodNames) {
						podName = wl.PodNames[ordinal]
					}
					if ordinal < len(wl.NodeIdx) {
						nodeIdx = wl.NodeIdx[ordinal]
					}
					if wl.Container != "" {
						container = wl.Container
					}
				}
				if nodeIdx < 0 || nodeIdx >= len(nodes) {
					nodeIdx = 0
				}
				out = append(out, nativePod{wl: wl, namespace: namespace, name: podName, container: container, node: nodes[nodeIdx], ordinal: ordinal})
			}
		}
	}
	return out
}

func (c *Construct) kubeletPodResource(now, start time.Time, pod nativePod, factor, tickSec float64) otlp.MetricResource {
	attrs := nativePodAttrs(c.clust.Name, pod, true)
	key := podUID(c.clust.Name, pod.namespace, pod.name)
	cpu := resolveCPUUsageBase(pod.wl, podServiceName(pod)) * factor
	if cpu < 0.001 {
		cpu = 0.001
	}
	memLimit := resolveMemLimit(pod.wl, podServiceName(pod))
	memUsage := memLimit * (0.35 + 0.25*factor)
	fsCapacity := float64(10 * 1024 * 1024 * 1024)
	fsUsage := fsCapacity * (0.15 + 0.1*factor)
	seconds := tickSec
	if seconds <= 0 {
		seconds = interval.Seconds()
	}

	metrics := []otlp.Metric{
		c.counter("container.cpu.time", "s", key, now, start, cpu*seconds, nil),
		gauge("container.cpu.usage", "{cpu}", now, cpu, nil),
		gauge("container.filesystem.available", "By", now, fsCapacity-fsUsage, nil),
		gauge("container.filesystem.capacity", "By", now, fsCapacity, nil),
		gauge("container.filesystem.usage", "By", now, fsUsage, nil),
		gauge("container.memory.available", "By", now, memLimit-memUsage, nil),
		gauge("container.memory.major_page_faults", "1", now, float64(pod.ordinal%3), nil),
		gauge("container.memory.page_faults", "1", now, 100+float64(pod.ordinal*7), nil),
		gauge("container.memory.rss", "By", now, memUsage*0.85, nil),
		gauge("container.memory.usage", "By", now, memUsage, nil),
		gauge("container.memory.working_set", "By", now, memUsage*0.9, nil),
		c.counter("k8s.pod.cpu.time", "s", key, now, start, cpu*seconds, nil),
		gauge("k8s.pod.cpu.usage", "{cpu}", now, cpu, nil),
		gauge("k8s.pod.filesystem.available", "By", now, fsCapacity-fsUsage, nil),
		gauge("k8s.pod.filesystem.capacity", "By", now, fsCapacity, nil),
		gauge("k8s.pod.filesystem.usage", "By", now, fsUsage, nil),
		gauge("k8s.pod.memory.available", "By", now, memLimit-memUsage, nil),
		gauge("k8s.pod.memory.major_page_faults", "1", now, float64(pod.ordinal%3), nil),
		gauge("k8s.pod.memory.page_faults", "1", now, 100+float64(pod.ordinal*7), nil),
		gauge("k8s.pod.memory.rss", "By", now, memUsage*0.85, nil),
		gauge("k8s.pod.memory.usage", "By", now, memUsage, nil),
		gauge("k8s.pod.memory.working_set", "By", now, memUsage*0.9, nil),
		networkCounter(c.otlpState, "k8s.pod.network.io", "By", key, now, start, seconds*1024*factor),
		networkCounter(c.otlpState, "k8s.pod.network.errors", "{error}", key, now, start, 0),
	}
	return otlp.MetricResource{Attrs: attrs, Scope: nativeScope("kubeletstatsreceiver"), Metrics: metrics}
}

func (c *Construct) clusterPodResource(now, start time.Time, cl *fixture.Cluster, pod nativePod) otlp.MetricResource {
	attrs := nativePodAttrs(cl.Name, pod, false)
	attrs["container.id"] = "containerd://" + hex16(cl.Name, pod.namespace, pod.name, pod.container)
	service := podServiceName(pod)
	metrics := []otlp.Metric{
		gauge("k8s.container.cpu_limit", "{cpu}", now, resolveCPULimit(pod.wl, service), nil),
		gauge("k8s.container.cpu_request", "{cpu}", now, resolveCPURequest(pod.wl, service), nil),
		gauge("k8s.container.memory_limit", "By", now, resolveMemLimit(pod.wl, service), nil),
		gauge("k8s.container.memory_request", "By", now, resolveMemRequest(pod.wl, service), nil),
		gauge("k8s.container.ready", "", now, 1, nil),
		gauge("k8s.container.restarts", "{restart}", now, 0, nil),
		gauge("k8s.pod.phase", "", now, 2, nil),
	}
	reasonPoints := make([]otlp.NumberPoint, 0, len(containerStatusReasons))
	for _, reason := range containerStatusReasons {
		reasonPoints = append(reasonPoints, otlp.NumberPoint{
			Attrs: map[string]any{"k8s.container.status.reason": reason},
			Start: start, Time: now, Value: 0,
		})
	}
	metrics = append(metrics, otlp.Metric{
		Name: "k8s.container.status.reason", Unit: "{container}", Kind: otlp.MetricSum,
		Temporality: otlp.TemporalityCumulative, Monotonic: false, Numbers: reasonPoints,
	})
	return otlp.MetricResource{Attrs: attrs, Scope: nativeScope("k8sclusterreceiver"), Metrics: metrics}
}

func (c *Construct) kubeletNodeResources(now, start time.Time, cl *fixture.Cluster, nodes []fixture.Node, factor, tickSec float64) []otlp.MetricResource {
	seconds := tickSec
	if seconds <= 0 {
		seconds = interval.Seconds()
	}
	out := make([]otlp.MetricResource, 0, len(nodes))
	for idx, node := range nodes {
		osType := node.OS
		if osType == "" {
			osType = "linux"
		}
		attrs := map[string]any{"k8s.cluster.name": cl.Name, "k8s.node.name": node.Hostname, "host.name": node.Hostname, "os.type": osType}
		cpu := float64(vcpusForNode(node)) * nodeCPUPercent(idx, factor) / 100
		memory := memBytesForNode(node)
		memoryUsage := memory * (0.45 + 0.2*factor)
		fsCapacity := float64(100 * 1024 * 1024 * 1024)
		fsUsage := fsCapacity * (0.25 + 0.1*factor)
		key := node.Hostname
		metrics := []otlp.Metric{
			c.counter("k8s.node.cpu.time", "s", key, now, start, cpu*seconds, nil),
			gauge("k8s.node.cpu.usage", "{cpu}", now, cpu, nil),
			gauge("k8s.node.filesystem.available", "By", now, fsCapacity-fsUsage, nil),
			gauge("k8s.node.filesystem.capacity", "By", now, fsCapacity, nil),
			gauge("k8s.node.filesystem.usage", "By", now, fsUsage, nil),
			gauge("k8s.node.memory.available", "By", now, memory-memoryUsage, nil),
			gauge("k8s.node.memory.major_page_faults", "1", now, float64(idx%3), nil),
			gauge("k8s.node.memory.page_faults", "1", now, 500+float64(idx*17), nil),
			gauge("k8s.node.memory.rss", "By", now, memoryUsage*0.85, nil),
			gauge("k8s.node.memory.usage", "By", now, memoryUsage, nil),
			gauge("k8s.node.memory.working_set", "By", now, memoryUsage*0.9, nil),
			networkCounter(c.otlpState, "k8s.node.network.io", "By", key, now, start, seconds*4096*factor),
			networkCounter(c.otlpState, "k8s.node.network.errors", "{error}", key, now, start, 0),
		}
		out = append(out, otlp.MetricResource{Attrs: attrs, Scope: nativeScope("kubeletstatsreceiver"), Metrics: metrics})
	}
	return out
}

func clusterObjectResources(now time.Time, cl *fixture.Cluster, nodes []fixture.Node, replicas int) []otlp.MetricResource {
	base := map[string]any{"k8s.cluster.name": cl.Name}
	var out []otlp.MetricResource
	namespaces := map[string]bool{}
	byKey := map[string]*fixture.Workload{}
	for i := range cl.Workloads {
		wl := &cl.Workloads[i]
		byKey[wl.Namespace+"\x00"+wl.Name] = wl
	}
	for i := range cl.SubstrateWorkloads {
		wl := &cl.SubstrateWorkloads[i]
		byKey[wl.Namespace+"\x00"+wl.Name] = wl
	}
	for namespace, deployments := range workloadDeployments(cl) {
		namespaces[namespace] = true
		for _, name := range deployments {
			wl := byKey[namespace+"\x00"+name]
			count := substrateReps(wl, replicas, len(nodes))
			controller := "deployment"
			if wl != nil && wl.Controller != "" {
				controller = wl.Controller
			}
			switch controller {
			case "daemonset":
				attrs := objectAttrs(base, namespace, "k8s.daemonset", name)
				out = append(out, clusterResource(attrs,
					gauge("k8s.daemonset.current_scheduled_nodes", "{node}", now, float64(count), nil),
					gauge("k8s.daemonset.desired_scheduled_nodes", "{node}", now, float64(len(nodes)), nil),
					gauge("k8s.daemonset.misscheduled_nodes", "{node}", now, 0, nil),
					gauge("k8s.daemonset.ready_nodes", "{node}", now, float64(count), nil),
				))
			case "deployment":
				attrs := objectAttrs(base, namespace, "k8s.deployment", name)
				out = append(out, clusterResource(attrs,
					gauge("k8s.deployment.available", "{pod}", now, float64(count), nil),
					gauge("k8s.deployment.desired", "{pod}", now, float64(count), nil),
				))
				rs := replicaSetName(name)
				rsAttrs := objectAttrs(base, namespace, "k8s.replicaset", rs)
				out = append(out, clusterResource(rsAttrs,
					gauge("k8s.replicaset.available", "{pod}", now, float64(count), nil),
					gauge("k8s.replicaset.desired", "{pod}", now, float64(count), nil),
				))
			}
		}
	}
	for _, namespace := range extraNamespaces {
		namespaces[namespace] = true
	}
	nsNames := make([]string, 0, len(namespaces))
	for namespace := range namespaces {
		nsNames = append(nsNames, namespace)
	}
	sort.Strings(nsNames)
	for _, namespace := range nsNames {
		attrs := cloneAny(base)
		attrs["k8s.namespace.name"] = namespace
		attrs["k8s.namespace.uid"] = podUID(cl.Name, "", namespace)
		attrs["service.namespace"] = namespace
		out = append(out, clusterResource(attrs, gauge("k8s.namespace.phase", "", now, 1, nil)))
	}
	for _, node := range nodes {
		attrs := cloneAny(base)
		attrs["k8s.node.name"] = node.Hostname
		attrs["k8s.node.uid"] = node.UID
		attrs["host.name"] = node.Hostname
		out = append(out, clusterResource(attrs, gauge("k8s.node.condition_ready", "", now, 1, nil)))
	}
	return out
}

func nativePodAttrs(cluster string, pod nativePod, includeOS bool) map[string]any {
	service := podServiceName(pod)
	attrs := map[string]any{
		"k8s.cluster.name":     cluster,
		"k8s.cluster.uid":      podUID(cluster, "", cluster),
		"k8s.namespace.name":   pod.namespace,
		"k8s.node.name":        pod.node.Hostname,
		"k8s.pod.name":         pod.name,
		"k8s.pod.uid":          podUID(cluster, pod.namespace, pod.name),
		"k8s.pod.start_time":   time.Unix(clusterCreatedUnix, 0).UTC().Format(time.RFC3339),
		"k8s.container.name":   pod.container,
		"container.image.name": imageRepo(service),
		"container.image.tag":  "latest",
		"service.name":         service,
		"service.namespace":    pod.namespace,
		"service.instance.id":  pod.name,
		"service.version":      "1.0.0",
		"host.name":            pod.node.Hostname,
	}
	if includeOS {
		osType := pod.node.OS
		if osType == "" {
			osType = "linux"
		}
		attrs["os.type"] = osType
	}
	controller := "deployment"
	if pod.wl != nil && pod.wl.Controller != "" {
		controller = pod.wl.Controller
	}
	switch controller {
	case "daemonset":
		attrs["k8s.daemonset.name"] = service
		attrs["k8s.daemonset.uid"] = podUID(cluster, pod.namespace, service)
	case "deployment":
		attrs["k8s.deployment.name"] = service
		rs := replicaSetName(service)
		attrs["k8s.replicaset.name"] = rs
		attrs["k8s.replicaset.uid"] = podUID(cluster, pod.namespace, rs)
	}
	return attrs
}

func podServiceName(pod nativePod) string {
	if pod.wl != nil && pod.wl.Name != "" {
		return pod.wl.Name
	}
	return pod.container
}

func objectAttrs(base map[string]any, namespace, prefix, name string) map[string]any {
	attrs := cloneAny(base)
	attrs[prefix+".name"] = name
	attrs[prefix+".uid"] = podUID(fmt.Sprint(base["k8s.cluster.name"]), namespace, name)
	attrs["k8s.namespace.name"] = namespace
	attrs["service.namespace"] = namespace
	return attrs
}

func clusterResource(attrs map[string]any, metrics ...otlp.Metric) otlp.MetricResource {
	return otlp.MetricResource{Attrs: attrs, Scope: nativeScope("k8sclusterreceiver"), Metrics: metrics}
}

func nativeScope(receiver string) otlp.Scope {
	return otlp.Scope{
		Name:    "github.com/open-telemetry/opentelemetry-collector-contrib/receiver/" + receiver,
		Version: nativeCollectorVersion,
	}
}

func gauge(name, unit string, now time.Time, value float64, attrs map[string]any) otlp.Metric {
	return otlp.Metric{Name: name, Unit: unit, Kind: otlp.MetricGauge, Numbers: []otlp.NumberPoint{{Attrs: attrs, Time: now, Value: value}}}
}

func (c *Construct) counter(name, unit, identity string, now, start time.Time, delta float64, attrs map[string]any) otlp.Metric {
	value := c.otlpState.add(name+"\x00"+identity+"\x00"+fmt.Sprint(attrs), delta)
	return otlp.Metric{
		Name: name, Unit: unit, Kind: otlp.MetricSum, Monotonic: true, Temporality: otlp.TemporalityCumulative,
		Numbers: []otlp.NumberPoint{{Attrs: attrs, Start: start, Time: now, Value: value}},
	}
}

func networkCounter(state *nativeOTLPState, name, unit, identity string, now, start time.Time, delta float64) otlp.Metric {
	points := make([]otlp.NumberPoint, 0, 2)
	for _, direction := range []string{"receive", "transmit"} {
		attrs := map[string]any{"interface": "eth0", "direction": direction}
		value := state.add(name+"\x00"+identity+"\x00"+direction, delta)
		points = append(points, otlp.NumberPoint{Attrs: attrs, Start: start, Time: now, Value: value})
	}
	return otlp.Metric{Name: name, Unit: unit, Kind: otlp.MetricSum, Monotonic: true, Temporality: otlp.TemporalityCumulative, Numbers: points}
}

func cloneAny(in map[string]any) map[string]any {
	out := make(map[string]any, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}
