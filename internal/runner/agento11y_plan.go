// SPDX-License-Identifier: AGPL-3.0-only

package runner

import (
	"fmt"
	"slices"
	"sort"

	p "github.com/rknightion/synthkit/internal/agento11yprovision"
	"github.com/rknightion/synthkit/internal/blueprint"
	"github.com/rknightion/synthkit/internal/core"
)

// registrationAgentDeclarations discovers the FULL selected structural roster.
// It neither builds workloads nor filters by producer mode, activity, sinks or
// control state. These declarations are not custody or runtime admission.
func registrationAgentDeclarations(reg *core.Registry, set []*blueprint.Resolved) ([]p.DeclarationInput, error) {
	if reg == nil {
		return nil, fmt.Errorf("agent declarations: registry missing")
	}
	ordered := slices.Clone(set)
	for _, res := range ordered {
		if res == nil || res.Name == "" {
			return nil, fmt.Errorf("agent declarations: blueprint identity missing")
		}
	}
	sort.SliceStable(ordered, func(i, j int) bool { return ordered[i].Name < ordered[j].Name })
	var out []p.DeclarationInput
	for i, res := range ordered {
		if i > 0 && ordered[i-1].Name == res.Name {
			return nil, fmt.Errorf("agent declarations: duplicate blueprint identity")
		}
		instances := map[string]bool{}
		for _, wi := range res.Workloads {
			if wi.Name == "" || instances[wi.Name] {
				return nil, fmt.Errorf("agent declarations: ambiguous workload identity")
			}
			instances[wi.Name] = true
			wr, ok := reg.Workload(wi.Kind)
			if !ok || wr.AgentDeclarations == nil {
				return nil, fmt.Errorf("agent declarations: blueprint %q workload %q has no declaration callback", res.Name, wi.Name)
			}
			declarations, err := wr.AgentDeclarations(wi.Config, structuralWorkloadBinding(res, wi))
			if err != nil {
				return nil, fmt.Errorf("agent declarations: blueprint %q workload %q: %w", res.Name, wi.Name, err)
			}
			if uint64(len(declarations)) > uint64(^uint32(0))+1 {
				return nil, fmt.Errorf("agent declarations: declaration index overflow")
			}
			for j, d := range declarations {
				// Even a callback that reuses its own buffers cannot alias the result. Do
				// not flatten children or deduplicate actual declarations sharing a name.
				d.Models = slices.Clone(d.Models)
				d.ProjectedChildNames = slices.Clone(d.ProjectedChildNames)
				d.HookPolicy.Phases = slices.Clone(d.HookPolicy.Phases)
				out = append(out, p.DeclarationInput{Origin: p.Origin{Blueprint: res.Name, Instance: wi.Name, Declaration: uint32(j)}, Declaration: d})
			}
		}
	}
	return out, nil
}

// structuralWorkloadBinding is the shared identity/wiring used by both pure
// discovery and Build. Runtime collaborators are added separately by Build.
func structuralWorkloadBinding(res *blueprint.Resolved, wi blueprint.WorkloadInstance) core.Binding {
	return core.Binding{Name: wi.Name, Seed: res.Name + ":" + wi.Name, Replicas: wi.Replicas, Env: wi.Env, Cluster: wi.Cluster, Calls: wi.Calls, Databases: wi.Databases}
}
