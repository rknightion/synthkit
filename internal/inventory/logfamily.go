// SPDX-License-Identifier: AGPL-3.0-only

// logfamily.go — the one rule every inventory producer uses to name the log family a stream
// or log resource belongs to.
//
// A `source` label is a lane's own declaration and only some lanes carry one: SK-20
// live-verified that a real pod-log stream carries NO `source`, and reserves the label for the
// journal and kubernetes-events lanes. Keying a projection on that label therefore files every
// pod-log stream under the empty source, fuses it with every other source-less lane, and leaves
// the comparator with a union label set that matches no real stream. The family has to come
// from SHAPE instead — and never from stamping the missing label onto the wire, which would be
// correcting the capture to the synth.

package inventory

// LogFamilyPodLogs is the shape-derived family name for a Kubernetes pod-log stream, on either
// transport. It matches the family name the reality corpus records.
const LogFamilyPodLogs = "k8s_pod_logs"

// podLogIdentitySpellings are the namespace/pod/container triple in each spelling a real
// pod-log pipeline puts on the wire. All three name the same identity:
//
//   - dotted OTel, as podLogsViaOpenTelemetry sends it to /v1/logs;
//   - underscore-sanitised, as the destination promotes those same attributes and as the
//     addon lanes push them straight to Loki;
//   - the classic Alloy podLogsViaLoki spelling.
//
// A stream carrying a full triple in any one spelling is a pod-log stream: no other lane in
// the catalogue identifies an individual container inside an individual pod.
var podLogIdentitySpellings = [][3]string{
	{"k8s.namespace.name", "k8s.pod.name", "k8s.container.name"},
	{"k8s_namespace_name", "k8s_pod_name", "k8s_container_name"},
	{"namespace", "pod", "container"},
}

// ShapeLogFamily names the family a recorded log entry's SHAPE proves it belongs to. The
// second result is false when no shape rule recognises it, which is not a defect: most lanes
// declare their own `source` and need no shape rule. It reads label KEYS only, so it works
// identically on a live stream and on a corpus document whose values are elided.
func ShapeLogFamily(log Log) (string, bool) {
	present := make(map[string]struct{}, len(log.StreamLabels))
	for _, label := range log.StreamLabels {
		present[label.Key] = struct{}{}
	}
	return shapeLogFamily(func(key string) bool {
		_, ok := present[key]
		return ok
	})
}

// ClassifyLogSource names the family one stream or log resource belongs to, for a producer
// holding the raw attribute map. Shape decides first, because it is the objective evidence; a
// declared `source` label names the lane when no shape rule recognises it. No recognised shape
// collides with a source-declaring lane — a pod-log stream has no `source` and the lanes that
// do carry one identify no container inside a pod.
func ClassifyLogSource(attributes map[string]string) string {
	if family, ok := shapeLogFamily(func(key string) bool {
		value, present := attributes[key]
		return present && value != ""
	}); ok {
		return family
	}
	return attributes["source"]
}

func shapeLogFamily(has func(string) bool) (string, bool) {
	for _, spelling := range podLogIdentitySpellings {
		if has(spelling[0]) && has(spelling[1]) && has(spelling[2]) {
			return LogFamilyPodLogs, true
		}
	}
	return "", false
}
