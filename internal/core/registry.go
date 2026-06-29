// SPDX-License-Identifier: AGPL-3.0-only

package core

import (
	"fmt"
	"sort"

	"github.com/rknightion/synthkit/internal/failuremode"
)

// Registry is the explicit catalog of construct and workload kinds. ONE instance is
// assembled by the composition root's catalog wiring file — there is no global
// registry and no init() self-registration (ARCHITECTURE §2). The blueprint loader
// validates every declared kind and config against it.
type Registry struct {
	constructs map[string]ConstructReg
	workloads  map[string]WorkloadReg
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry {
	return &Registry{
		constructs: map[string]ConstructReg{},
		workloads:  map[string]WorkloadReg{},
	}
}

// RegisterConstruct adds a construct kind. A duplicate kind is a wiring bug → panic.
func (r *Registry) RegisterConstruct(reg ConstructReg) {
	if reg.Kind == "" || reg.NewConfig == nil || reg.Build == nil {
		panic(fmt.Sprintf("core: invalid construct registration %+v", reg.Kind))
	}
	if _, dup := r.constructs[reg.Kind]; dup {
		panic(fmt.Sprintf("core: duplicate construct kind %q", reg.Kind))
	}
	r.constructs[reg.Kind] = reg
}

// RegisterWorkload adds a workload kind. A duplicate kind is a wiring bug → panic.
func (r *Registry) RegisterWorkload(reg WorkloadReg) {
	if reg.Kind == "" || reg.NewConfig == nil || reg.Build == nil {
		panic(fmt.Sprintf("core: invalid workload registration %+v", reg.Kind))
	}
	if _, dup := r.workloads[reg.Kind]; dup {
		panic(fmt.Sprintf("core: duplicate workload kind %q", reg.Kind))
	}
	r.workloads[reg.Kind] = reg
}

// Construct looks up a construct kind.
func (r *Registry) Construct(kind string) (ConstructReg, bool) {
	reg, ok := r.constructs[kind]
	return reg, ok
}

// Workload looks up a workload kind.
func (r *Registry) Workload(kind string) (WorkloadReg, bool) {
	reg, ok := r.workloads[kind]
	return reg, ok
}

// ConstructKinds returns the sorted registered construct kinds (for loud errors).
func (r *Registry) ConstructKinds() []string {
	out := make([]string, 0, len(r.constructs))
	for k := range r.constructs {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// WorkloadKinds returns the sorted registered workload kinds.
func (r *Registry) WorkloadKinds() []string {
	out := make([]string, 0, len(r.workloads))
	for k := range r.workloads {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// AllFailureModes unions FailureModes across every registered construct + workload kind. The
// blueprint resolver validates scenario/incident references against this union; the control plane
// enumerates it in the derived schema. Iteration is over the sorted kinds so the output order is
// deterministic.
func (r *Registry) AllFailureModes() []failuremode.Mode {
	var out []failuremode.Mode
	for _, k := range r.ConstructKinds() {
		out = append(out, r.constructs[k].FailureModes...)
	}
	for _, k := range r.WorkloadKinds() {
		out = append(out, r.workloads[k].FailureModes...)
	}
	return out
}
