// SPDX-License-Identifier: AGPL-3.0-only

package blueprint

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/rknightion/synthkit/internal/failuremode"
	"github.com/rknightion/synthkit/internal/fixture"

	yaml "gopkg.in/yaml.v3"
)

// replicaMin/replicaMax bound a workload's live-scalable replica dimension. The max is deliberately
// modest: at 50 replicas on a cluster whose node groups have no explicit `desired:` (so node count
// cascades), node count derives to ~7 (max(3, ceil(50/8))), keeping per-node pod density realistic.
// A workload on a cluster with PINNED node groups does not cascade — at replicaMax that is ~12-17
// pods/node on a 3-4 node cluster, still realistic (real nodes hold 30-110 pods). v1 documents this;
// a capacity-aware Max clamp is an optional follow-up.
const replicaMin, replicaMax = 1, 50

// buildTargets enumerates every addressable failure/scaling target across the blueprint, tagging
// each with its axis and (where applicable) live-scaling bounds. v1: ONLY workloads are
// live-scalable (replicas, with a node cascade). Databases/caches/clusters/clouds are addressable
// failure targets but not scalable in v1 (read-replica scaling deferred — no telemetry surface).
func buildTargets(d *Decl) []Target {
	var out []Target
	for _, w := range d.Workloads {
		// An `app` workload (one declaring a `services:` graph) is itself a failure target on
		// AxisWorkload but is NOT workload-level scalable — scaling is per service NODE (design
		// §6.6): each node is a failure + replica-scaling target on AxisService.
		if nodes := appServiceNodes(w); len(nodes) > 0 {
			out = append(out, Target{Name: w.Name, Axis: failuremode.AxisWorkload})
			for _, n := range nodes {
				if n.external {
					continue // remote/managed service (e.g. a SaaS gateway): not a scalable/failure target
				}
				def := n.replicas
				if def <= 0 {
					def = defaultReplicas
				}
				out = append(out, Target{Name: n.name, Axis: failuremode.AxisService,
					Scalable: &ScaleBounds{Dimension: "replicas", Default: def, Min: replicaMin, Max: replicaMax}})
			}
			continue
		}
		def := w.Replicas
		if def <= 0 {
			def = defaultReplicas
		}
		out = append(out, Target{Name: w.Name, Axis: failuremode.AxisWorkload,
			Scalable: &ScaleBounds{Dimension: "replicas", Default: def, Min: replicaMin, Max: replicaMax}})
	}
	for _, e := range d.Environments {
		for _, db := range e.Databases {
			out = append(out, Target{Name: db.Name, Axis: failuremode.AxisDatabase})
		}
		for _, c := range e.Caches {
			out = append(out, Target{Name: c.Name, Axis: failuremode.AxisCache})
		}
		if e.Cluster != nil {
			out = append(out, Target{Name: e.Cluster.Name, Axis: failuremode.AxisCluster})
		}
		if e.Cloud != nil {
			out = append(out, Target{Name: e.Name, Axis: failuremode.AxisCloud})
		}
	}
	return out
}

// targetIndex maps target name → axis and rejects duplicate names across axes (ambiguous). A
// repeated name on the SAME axis is idempotent (not a collision).
func targetIndex(targets []Target) (map[string]failuremode.Axis, error) {
	idx := map[string]failuremode.Axis{}
	for _, t := range targets {
		if existing, dup := idx[t.Name]; dup && existing != t.Axis {
			return nil, fmt.Errorf("target name %q is ambiguous across axes %q and %q", t.Name, existing, t.Axis)
		}
		idx[t.Name] = t.Axis
	}
	return idx, nil
}

// knownAxes is the closed set of valid axis values (for "<axis>:*" wildcard validation).
var knownAxes = map[failuremode.Axis]bool{
	failuremode.AxisWorkload: true, failuremode.AxisCluster: true,
	failuremode.AxisDatabase: true, failuremode.AxisCache: true, failuremode.AxisCloud: true,
	failuremode.AxisService: true, failuremode.AxisNetwork: true,
}

// serviceNodePeek is the wiring facts buildTargets extracts from one declared service-graph node
// without decoding the full app.Config (which doesn't exist until the resolver's workload pass).
type serviceNodePeek struct {
	name     string
	replicas int
	runtime  string // pod language runtime (go|jvm|node|python; "" omitted) — the shared fixture identity
	external bool   // remote/managed service: emits its trace hop but is NOT placed as a k8s deployment on the caller's cluster
	// namespace is the k8s namespace the placed deployment lands in. "" means "default to node name"
	// (back-compat: omitting namespace: in the YAML leaves this empty and the resolver falls back to
	// n.name, exactly as before).
	namespace string
	// resources optionally pins the node's container CPU/memory requests/limits + cAdvisor usage base
	// on the cluster (k8s substrate lane). nil ⇒ k8scluster's per-workload size-class defaults apply.
	resources *fixture.WorkloadResources
	// controller is the k8s controller kind this app service node runs as: "" (⇒ deployment) or
	// "statefulset". "daemonset" is REJECTED at resolve time on an app service node — app nodes are
	// the traced golden-thread lane and DaemonSets do not emit app traces IRL (spec Q4). The blueprint
	// maps this onto fixture.Workload.Controller.
	controller string
	// hpa requests a kube_horizontalpodautoscaler_* family for this node's workload (Deployment/
	// StatefulSet). Opt-in; maps onto fixture.Workload.HasHPA.
	hpa bool
	// volumeClaims are the PVC template names this node mounts; maps onto fixture.Workload.VolumeClaims.
	volumeClaims []string
}

// appServiceNodes peeks a workload's raw config node for a `services:` graph and returns each
// node's name + replica default. It mirrors rumDeclared (resolve.go): it extracts a wiring fact
// from the raw yaml.Node WITHOUT importing internal/workload/app, keeping internal/blueprint free
// of a workload-package import. Only the `app` kind declares `services:` (strict decoding rejects
// it on any other kind, which runs before buildTargets), so this is gated by shape, not a kind
// literal — generic and free of kind-name coupling. Returns nil for workloads with no service graph.
func appServiceNodes(w WorkloadDecl) []serviceNodePeek {
	cfg := w.Config
	if cfg.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(cfg.Content); i += 2 {
		if cfg.Content[i].Value != "services" {
			continue
		}
		seq := cfg.Content[i+1]
		if seq.Kind != yaml.SequenceNode {
			return nil
		}
		var out []serviceNodePeek
		for _, node := range seq.Content {
			if node.Kind != yaml.MappingNode {
				continue
			}
			var p serviceNodePeek
			for j := 0; j+1 < len(node.Content); j += 2 {
				switch node.Content[j].Value {
				case "name":
					_ = node.Content[j+1].Decode(&p.name)
				case "replicas":
					_ = node.Content[j+1].Decode(&p.replicas)
				case "runtime":
					_ = node.Content[j+1].Decode(&p.runtime)
				case "external":
					_ = node.Content[j+1].Decode(&p.external)
				case "namespace":
					_ = node.Content[j+1].Decode(&p.namespace)
				case "controller":
					_ = node.Content[j+1].Decode(&p.controller)
				case "hpa":
					_ = node.Content[j+1].Decode(&p.hpa)
				case "volume_claims":
					_ = node.Content[j+1].Decode(&p.volumeClaims)
				case "resources":
					var r fixture.WorkloadResources
					if err := node.Content[j+1].Decode(&r); err == nil && r != (fixture.WorkloadResources{}) {
						p.resources = &r
					}
				}
			}
			if p.name != "" {
				out = append(out, p)
			}
		}
		return out
	}
	return nil
}

// modeOnAxis reports whether vocab declares mode on axis.
func modeOnAxis(vocab []failuremode.Mode, mode string, axis failuremode.Axis) bool {
	for _, m := range vocab {
		if m.Name == mode && m.Axis == axis {
			return true
		}
	}
	return false
}

// soleAxis returns the single axis a mode is declared on (ok=false if zero or multiple).
func soleAxis(vocab []failuremode.Mode, mode string) (failuremode.Axis, bool) {
	var found failuremode.Axis
	n := 0
	for _, m := range vocab {
		if m.Name == mode {
			found = m.Axis
			n++
		}
	}
	return found, n == 1
}

// validateEffect checks one (mode, target, intensity) against the target index + vocabulary.
func validateEffect(e EffectDecl, axes map[string]failuremode.Axis, vocab []failuremode.Mode, multi map[string]bool) error {
	if e.Intensity < 0 || e.Intensity > 1 {
		return fmt.Errorf("effect %q: intensity %v out of [0,1]", e.Mode, e.Intensity)
	}
	switch {
	case e.Target == "":
		if multi[e.Mode] {
			return fmt.Errorf("effect %q: empty target is ambiguous (mode is multi-axis); name a target or <axis>:*", e.Mode)
		}
		if _, ok := soleAxis(vocab, e.Mode); !ok {
			return fmt.Errorf("effect %q: unknown mode", e.Mode)
		}
		return nil
	case strings.HasSuffix(e.Target, ":*"):
		axis := failuremode.Axis(strings.TrimSuffix(e.Target, ":*"))
		if !knownAxes[axis] {
			return fmt.Errorf("effect %q: unknown axis wildcard %q", e.Mode, e.Target)
		}
		if !modeOnAxis(vocab, e.Mode, axis) {
			return fmt.Errorf("effect %q: mode not declared on axis %q", e.Mode, axis)
		}
		return nil
	default:
		axis, ok := axes[e.Target]
		if !ok {
			return fmt.Errorf("effect %q: unknown target %q", e.Mode, e.Target)
		}
		if !modeOnAxis(vocab, e.Mode, axis) {
			return fmt.Errorf("effect %q: mode not declared on target %q's axis %q", e.Mode, e.Target, axis)
		}
		return nil
	}
}

// scheduleEntry builds a shape-engine schedule entry: kind@at/for[#intensity][@scope].
func scheduleEntry(kind, at, forDur string, intensity float64, scope string) string {
	entry := kind + "@" + at + "/" + forDur
	if intensity > 0 {
		entry += "#" + strconv.FormatFloat(intensity, 'g', -1, 64)
	}
	if scope != "" {
		entry += "@" + scope
	}
	return entry
}

// expandScopes turns a target into the concrete scope(s) for SCHEDULED entries: "" → [""] (the
// engine's un-scoped match), "<axis>:*" → every target name of that axis, name → [name]. (Live
// expansion is the runner's mirror of this against bp.targets.)
func expandScopes(target string, targets []Target) []string {
	if target == "" {
		return []string{""}
	}
	if strings.HasSuffix(target, ":*") {
		axis := failuremode.Axis(strings.TrimSuffix(target, ":*"))
		var out []string
		for _, t := range targets {
			if t.Axis == axis {
				out = append(out, t.Name)
			}
		}
		return out
	}
	return []string{target}
}
