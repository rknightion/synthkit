// SPDX-License-Identifier: AGPL-3.0-only

package inventory

import "testing"

func TestShapeLogFamilyRecognisesEveryPodLogSpelling(t *testing.T) {
	t.Parallel()
	for name, keys := range map[string][]string{
		// OTLP wire, exactly as captured at collector egress (podLogsViaOpenTelemetry).
		"otlp": {
			"cluster", "k8s.cluster.name", "k8s.namespace.name", "k8s.pod.name",
			"k8s.container.name", "service.instance.id", "service.name", "service.namespace",
		},
		// The same stream after Loki sanitises the promoted resource attributes.
		"loki_sanitised": {
			"cluster", "k8s_cluster_name", "k8s_namespace_name", "k8s_pod_name",
			"k8s_container_name", "service_name", "detected_level", "log_iostream",
		},
		// Loki-native push (podLogsViaLoki), the classic Alloy shape.
		"loki_classic": {
			"cluster", "k8s_cluster_name", "namespace", "pod", "container", "job",
			"service_namespace", "service_instance_id", "stream", "detected_level",
		},
	} {
		family, ok := ShapeLogFamily(logWithKeys("", keys))
		if !ok || family != LogFamilyPodLogs {
			t.Fatalf("%s: ShapeLogFamily = (%q, %v), want (%q, true)", name, family, ok, LogFamilyPodLogs)
		}
	}
}

func TestShapeLogFamilyLeavesOtherLanesUnclassified(t *testing.T) {
	t.Parallel()
	for name, keys := range map[string][]string{
		// Reserves `source` for itself (SK-20) and carries no pod identity.
		"journal":           {"cluster", "instance", "job", "k8s_cluster_name", "level", "service_name", "source", "unit"},
		"kubernetes_events": {"cluster", "job", "k8s_cluster_name", "level", "namespace", "reason", "service_name", "source"},
		// A namespace alone is not a pod identity.
		"app": {"blueprint", "cluster", "env", "job", "level", "namespace", "service_name", "source"},
		// Two thirds of a triple is not a pod identity either.
		"partial": {"cluster", "k8s_namespace_name", "k8s_pod_name"},
	} {
		if family, ok := ShapeLogFamily(logWithKeys("", keys)); ok {
			t.Fatalf("%s: ShapeLogFamily classified as %q; only a full pod identity is a pod-log stream", name, family)
		}
	}
}

func TestClassifyLogSourceNamesThePodLogFamilyWithoutASourceLabel(t *testing.T) {
	t.Parallel()
	podLog := map[string]string{
		"k8s.namespace.name": "default", "k8s.pod.name": "api-0", "k8s.container.name": "api",
		"cluster": "lab",
	}
	if got := ClassifyLogSource(podLog); got != LogFamilyPodLogs {
		t.Fatalf("ClassifyLogSource(pod log)=%q, want %q derived from shape", got, LogFamilyPodLogs)
	}
	if _, stamped := podLog["source"]; stamped {
		t.Fatal("classification must never add a source label to the wire shape it reads")
	}
	if got := ClassifyLogSource(map[string]string{"source": "journal", "unit": "kubelet.service"}); got != "journal" {
		t.Fatalf("ClassifyLogSource(journal)=%q, want the lane's own declared source", got)
	}
	if got := ClassifyLogSource(map[string]string{"topic": "hub", "instance": "eventhub"}); got != "" {
		t.Fatalf("ClassifyLogSource(unrecognised)=%q, want the empty source it already had", got)
	}
}

func logWithKeys(source string, keys []string) Log {
	log := Log{Source: source, StreamLabels: make([]Attribute, 0, len(keys)), StructuredMetadataKeys: []string{}}
	for _, key := range keys {
		log.StreamLabels = append(log.StreamLabels, Attribute{Key: key, Values: []string{}})
	}
	return log
}
