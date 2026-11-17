// SPDX-License-Identifier: AGPL-3.0-only

package runner

import (
	"fmt"
	"slices"
	"sort"
	"strings"

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

// RegistrationPlan composes retained authored objects with the entire pure
// declaration set. No construction, inventory, minter or runtime collaborator is
// invoked. C remains the typed normalization and management admission boundary.
func RegistrationPlan(reg *core.Registry, set []*blueprint.Resolved, binding p.Binding) (p.PlanInput, error) {
	if err := blueprint.ValidateSet(set); err != nil {
		return p.PlanInput{}, err
	}
	declarations, err := registrationAgentDeclarations(reg, set)
	if err != nil {
		return p.PlanInput{}, err
	}
	in := p.PlanInput{Binding: binding, Mode: p.ModeApply, Declarations: declarations}
	ordered := slices.Clone(set)
	sort.SliceStable(ordered, func(i, j int) bool { return ordered[i].Name < ordered[j].Name })
	keys := map[string]bool{}
	targets := map[p.Origin]bool{}
	for _, res := range ordered {
		if res.AgentObservability == nil {
			continue
		}
		authored := res.AgentObservability.AuthoredSpecs()
		for _, closure := range res.AgentObservability.Closures {
			var owner *p.DeclarationInput
			for i := range declarations {
				d := &declarations[i]
				if d.Origin.Blueprint == res.Name && d.Origin.Instance == closure.Agent.Instance && d.Declaration.Name == closure.Agent.AgentName {
					if owner != nil {
						return p.PlanInput{}, p.ErrConflict
					}
					owner = d
				}
			}
			if owner == nil || owner.Declaration.ProducerMode != core.AgentProducerReal || targets[owner.Origin] {
				return p.PlanInput{}, p.ErrInvalid
			}
			targets[owner.Origin] = true
			in.Targets = append(in.Targets, owner.Origin)
			names := append([]string{owner.Declaration.Name}, owner.Declaration.ProjectedChildNames...)
			for i := range names {
				names[i] = strings.ToLower(strings.TrimSpace(names[i]))
			}
			sort.Strings(names)
			add := func(key string, co p.Coordinate, versionFrom string, deps []string) error {
				if key == "" || keys[key] {
					return p.ErrConflict
				}
				spec, ok := authored[key]
				if !ok {
					return p.ErrInvalid
				}
				keys[key] = true
				in.Resources = append(in.Resources, p.PlannedResource{Key: key, Origin: owner.Origin, Coordinate: co, VersionFrom: versionFrom, Spec: spec, Dependencies: slices.Clone(deps), Namespaces: slices.Clone(names)})
				return nil
			}
			for _, v := range closure.Evaluators {
				if err := add(v.Key, p.Coordinate{Kind: p.KindEvaluator, ID: v.Spec.EvaluatorID, Version: v.Spec.Version}, "", nil); err != nil {
					return p.PlanInput{}, err
				}
			}
			for _, v := range closure.Collections {
				if err := add(v.Key, p.Coordinate{Kind: p.KindCollection, Unassigned: true}, "", nil); err != nil {
					return p.PlanInput{}, err
				}
			}
			for _, v := range closure.Rules {
				if err := add(v.Key, p.Coordinate{Kind: p.KindRule, ID: v.Spec.RuleID}, "", v.Spec.EvaluatorIDs); err != nil {
					return p.PlanInput{}, err
				}
			}
			for _, v := range closure.Hooks {
				if err := add(v.Key, p.Coordinate{Kind: p.KindHook, ID: v.Spec.RuleID}, "", v.Spec.EvaluatorIDs); err != nil {
					return p.PlanInput{}, err
				}
			}
			for _, v := range closure.Actions {
				parent := ""
				for _, rule := range closure.Rules {
					if rule.Key == v.ParentRule {
						parent = rule.Spec.RuleID
					}
				}
				if parent == "" {
					return p.PlanInput{}, p.ErrInvalid
				}
				deps := append([]string{v.ParentRule}, v.Spec.ActionConfig.CollectionIDs...)
				if err := add(v.Key, p.Coordinate{Kind: p.KindAction, ParentID: parent, Unassigned: true}, "", deps); err != nil {
					return p.PlanInput{}, err
				}
			}
			for _, v := range closure.Suites {
				if err := add(v.Key, p.Coordinate{Kind: p.KindSuite, ID: v.Spec.SuiteID}, "", nil); err != nil {
					return p.PlanInput{}, err
				}
				d := v.Draft
				if err := add(d.Key, p.Coordinate{Kind: p.KindDraft, ParentID: v.Spec.SuiteID, Unassigned: true}, "", []string{v.Key}); err != nil {
					return p.PlanInput{}, err
				}
				base := []string{v.Key, d.Key}
				pubDeps := slices.Clone(base)
				for _, cs := range d.Cases {
					if err := add(cs.Key, p.Coordinate{Kind: p.KindCase, ID: cs.Spec.TestCaseID, ParentID: v.Spec.SuiteID}, d.Key, base); err != nil {
						return p.PlanInput{}, err
					}
					pubDeps = append(pubDeps, cs.Key)
				}
				bindingDeps := slices.Clone(base)
				for _, b := range d.Bindings.Bindings {
					bindingDeps = append(bindingDeps, b.EvaluatorID)
				}
				if err := add(d.Bindings.Key, p.Coordinate{Kind: p.KindBindings, ParentID: v.Spec.SuiteID}, d.Key, bindingDeps); err != nil {
					return p.PlanInput{}, err
				}
				pubDeps = append(pubDeps, d.Bindings.Key)
				for _, b := range d.Publication.Spec.Bindings {
					pubDeps = append(pubDeps, b.EvaluatorID)
				}
				if err := add(d.Publication.Key, p.Coordinate{Kind: p.KindPublication, ParentID: v.Spec.SuiteID}, d.Key, pubDeps); err != nil {
					return p.PlanInput{}, err
				}
			}
			// In this initial authoring subset every reference stays in its closure.
			local := map[string]bool{}
			for _, r := range in.Resources {
				if r.Origin == owner.Origin {
					local[r.Key] = true
				}
			}
			for _, r := range in.Resources {
				if r.Origin == owner.Origin {
					for _, dep := range r.Dependencies {
						if !local[dep] {
							return p.PlanInput{}, p.ErrInvalid
						}
					}
				}
			}
		}
	}
	// Global phases, not one phase restart per suite/closure. Stable ordering
	// within each phase preserves the operator's authored declaration order.
	for _, phase := range [][]p.ResourceKind{{p.KindEvaluator}, {p.KindCollection}, {p.KindSuite}, {p.KindDraft}, {p.KindCase}, {p.KindBindings}, {p.KindPublication}, {p.KindRule, p.KindHook}, {p.KindAction}} {
		for _, r := range in.Resources {
			if !slices.Contains(phase, r.Coordinate.Kind) {
				continue
			}
			op := p.OpCreate
			if r.Coordinate.Kind == p.KindBindings {
				op = p.OpInitialBindings
			}
			if r.Coordinate.Kind == p.KindPublication {
				op = p.OpPublish
			}
			in.Steps = append(in.Steps, p.PlannedStep{Number: uint32(len(in.Steps) + 1), ResourceKey: r.Key, Operation: op})
		}
	}
	if _, err := p.Prepare(in); err != nil {
		return p.PlanInput{}, err
	}
	return in, nil
}

// RegistrationHistoryPlan consumes only an already validated locked snapshot.
// Recovery returns the original immutable plan, NEVER acquired-coordinate
// reconstruction or a ModeRecover plan. A new cleanup is a distinct operation.
func RegistrationHistoryPlan(doc p.Document, constructionID string, mode p.Mode) (p.PlanInput, error) {
	var selected *p.Construction
	for i := range doc.Constructions {
		if doc.Constructions[i].ID == constructionID {
			if selected != nil {
				return p.PlanInput{}, p.ErrConflict
			}
			selected = &doc.Constructions[i]
		}
	}
	if selected == nil || selected.Plan.Binding != doc.Binding {
		return p.PlanInput{}, p.ErrConflict
	}
	prepared, err := p.Prepare(selected.Plan)
	if err != nil || prepared.Digest() != selected.Input || selected.Mode != selected.Plan.Mode {
		return p.PlanInput{}, p.ErrConflict
	}
	if mode == p.ModeRecover {
		if selected.Complete || doc.Pending != nil && (doc.Pending.ConstructionID != selected.ID || doc.Pending.Input != selected.Input) {
			return p.PlanInput{}, p.ErrConflict
		}
		return copyRegistrationPlan(selected.Plan), nil
	}
	if mode != p.ModeDeactivate && mode != p.ModeTeardown || doc.Pending != nil {
		return p.PlanInput{}, p.ErrConflict
	}
	in := copyRegistrationPlan(selected.Plan)
	in.Mode = mode
	in.Steps = nil
	for i, r := range in.Resources {
		var record *p.ResourceRecord
		for j := range doc.Resources {
			if doc.Resources[j].Key == r.Key {
				if record != nil {
					return p.PlanInput{}, p.ErrConflict
				}
				record = &doc.Resources[j]
			}
		}
		if record == nil || len(record.Acquisition) == 0 || record.Origin != r.Origin || record.Authored.Digest() != r.Spec.Digest() || !slices.Equal(record.Dependencies, r.Dependencies) || !slices.Equal(record.Namespaces, r.Namespaces) || record.Coordinate.Kind != r.Coordinate.Kind || record.Coordinate.ParentID != r.Coordinate.ParentID || record.Coordinate.Unassigned {
			return p.PlanInput{}, p.ErrConflict
		}
		if !r.Coordinate.Unassigned && r.Coordinate.ID != record.Coordinate.ID {
			return p.PlanInput{}, p.ErrConflict
		}
		if r.VersionFrom != "" {
			var draft *p.ResourceRecord
			for j := range doc.Resources {
				v := &doc.Resources[j]
				if v.Key == r.VersionFrom {
					draft = v
				}
			}
			if draft == nil || len(draft.Acquisition) == 0 || draft.Coordinate.Kind != p.KindDraft || draft.Coordinate.ParentID != r.Coordinate.ParentID || draft.Coordinate.ID != draft.Coordinate.Version || draft.Coordinate.Version == "" || record.Coordinate.Version != draft.Coordinate.Version {
				return p.PlanInput{}, p.ErrConflict
			}
		} else if !r.Coordinate.Unassigned && r.Coordinate.Version != record.Coordinate.Version {
			return p.PlanInput{}, p.ErrConflict
		}
		in.Resources[i].Coordinate = record.Coordinate
		in.Resources[i].VersionFrom = ""
	}
	// Reject a selected closure that omits any historically acquired overlapping
	// consumer. Never turn deselection or a narrower old plan into empty success.
	for _, old := range doc.Resources {
		if len(old.Acquisition) == 0 || !registrationConsumer(old.Coordinate.Kind) {
			continue
		}
		overlaps := false
		for _, r := range in.Resources {
			for _, n := range r.Namespaces {
				if slices.Contains(old.Namespaces, n) {
					overlaps = true
				}
			}
		}
		if overlaps && !slices.ContainsFunc(in.Resources, func(r p.PlannedResource) bool { return r.Key == old.Key }) {
			return p.PlanInput{}, p.ErrConflict
		}
	}
	add := func(key string, op p.Operation) {
		in.Steps = append(in.Steps, p.PlannedStep{Number: uint32(len(in.Steps) + 1), ResourceKey: key, Operation: op})
	}
	for _, r := range in.Resources {
		if registrationConsumer(r.Coordinate.Kind) {
			add(r.Key, p.OpDisable)
		}
	}
	if mode == p.ModeTeardown {
		// Supported deletes only, dependent-first. Permanent/unknown-use members
		// remain represented and are classified by C, not silently dropped.
		for _, kind := range []p.ResourceKind{p.KindAction, p.KindHook, p.KindRule} {
			for _, r := range in.Resources {
				if r.Coordinate.Kind == kind {
					add(r.Key, p.OpDelete)
				}
			}
		}
	}
	if _, err := p.Prepare(in); err != nil {
		return p.PlanInput{}, err
	}
	return in, nil
}
func copyRegistrationPlan(in p.PlanInput) p.PlanInput {
	in.Declarations = slices.Clone(in.Declarations)
	for i := range in.Declarations {
		d := &in.Declarations[i].Declaration
		d.Models = slices.Clone(d.Models)
		d.ProjectedChildNames = slices.Clone(d.ProjectedChildNames)
		d.HookPolicy.Phases = slices.Clone(d.HookPolicy.Phases)
	}
	in.Targets = slices.Clone(in.Targets)
	in.Resources = slices.Clone(in.Resources)
	for i := range in.Resources {
		in.Resources[i].Dependencies = slices.Clone(in.Resources[i].Dependencies)
		in.Resources[i].Namespaces = slices.Clone(in.Resources[i].Namespaces)
	}
	in.Steps = slices.Clone(in.Steps)
	return in
}

func registrationConsumer(k p.ResourceKind) bool {
	return k == p.KindHook || k == p.KindRule || k == p.KindAction
}

// structuralWorkloadBinding is the shared identity/wiring used by both pure
// discovery and Build. Runtime collaborators are added separately by Build.
func structuralWorkloadBinding(res *blueprint.Resolved, wi blueprint.WorkloadInstance) core.Binding {
	return core.Binding{Name: wi.Name, Seed: res.Name + ":" + wi.Name, Replicas: wi.Replicas, Env: wi.Env, Cluster: wi.Cluster, Calls: wi.Calls, Databases: wi.Databases}
}
