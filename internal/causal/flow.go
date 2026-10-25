// SPDX-License-Identifier: AGPL-3.0-only

package causal

import (
	"fmt"
	"slices"
)

// Flow is one workload's declared request tree, so the model can derive how much of a resource's
// demand the workload's upstream path actually admits. Nodes are the services in the order the
// workload walks them (depth-first pre-order of its spanning tree): Nodes[0] is the entry, every
// caller precedes its callees, and a caller's callees appear in call order.
//
// A request meets a node's Uses in order and stops at the first rejection; it then calls the
// node's callees one after another under each call's Policy and stops at the first call that
// fails once its retries are spent. A callee's failure reaches its caller mapped by
// CallerStatus; a rule rejection is never resent. Timeouts and the workload's own legacy error
// draws are not modelled here, so a healthy tree (no resource rejecting) admits every request.
// The tree only gates a resource's demand (reach); resends of the resource's own rejections
// still follow the load's Policy alone.
type Flow struct {
	Nodes []FlowNode
}

// FlowNode is one service of a Flow.
type FlowNode struct {
	// Parent is the caller's index in Nodes, -1 for the entry (Nodes[0] only).
	Parent int
	// Policy is the caller's client policy on the call into this node; zero for the entry.
	Policy CallPolicy
	// Uses are the declared resource names the node meets on arrival, in order.
	Uses []string
}

func validateFlow(f *Flow, byName map[string]Resource) error {
	if len(f.Nodes) == 0 {
		return fmt.Errorf("flow has no nodes")
	}
	for i, n := range f.Nodes {
		switch {
		case i == 0 && n.Parent != -1:
			return fmt.Errorf("flow node 0 is the entry and has no parent")
		case i > 0 && (n.Parent < 0 || n.Parent >= i):
			return fmt.Errorf("flow node %d: parent %d must precede it", i, n.Parent)
		}
		if err := n.Policy.Validate(); err != nil {
			return fmt.Errorf("flow node %d: %w", i, err)
		}
		for j, u := range n.Uses {
			if _, ok := byName[u]; !ok {
				return fmt.Errorf("flow node %d uses unknown resource %q", i, u)
			}
			if slices.Contains(n.Uses[:j], u) {
				return fmt.Errorf("flow node %d uses %q twice", i, u)
			}
		}
	}
	return nil
}

// outcomeKey is how one failure looks to the caller that receives it: class and status, and
// whether a rule rejected it (never resent).
type outcomeKey struct {
	class  FailureClass
	status int
	rule   bool
}

type failShare struct {
	key outcomeKey
	p   float64
}

// dist is one service evaluation's outcome: the share that succeeds and the shares failing by key.
type dist struct {
	ok    float64
	fails []failShare
}

func (d *dist) add(k outcomeKey, p float64) {
	if p <= 0 {
		return
	}
	for i := range d.fails {
		if d.fails[i].key == k {
			d.fails[i].p += p
			return
		}
	}
	d.fails = append(d.fails, failShare{k, p})
}

type flowNode struct {
	policy CallPolicy
	uses   []*compiled
	kids   []int
}

// cflow is a compiled Flow, shared by every load that names it.
type cflow struct {
	nodes []flowNode
}

// gate places one load in its compiled flow: the path from the entry to the node and the index
// of the resource in the node's uses.
type gate struct {
	flow *cflow
	path []int // node indices from the entry to the load's node
	use  int
}

func compileFlow(f *Flow, byName map[string]*compiled) *cflow {
	cf := &cflow{nodes: make([]flowNode, len(f.Nodes))}
	for i, n := range f.Nodes {
		fn := &cf.nodes[i]
		fn.policy = n.Policy
		for _, u := range n.Uses {
			fn.uses = append(fn.uses, byName[u])
		}
		if i > 0 {
			cf.nodes[n.Parent].kids = append(cf.nodes[n.Parent].kids, i)
		}
	}
	return cf
}

func (cf *cflow) gateFor(f *Flow, node int, resource string) *gate {
	g := &gate{flow: cf, use: slices.Index(f.Nodes[node].Uses, resource)}
	for n := node; n >= 0; n = f.Nodes[n].Parent {
		g.path = append(g.path, n)
	}
	slices.Reverse(g.path)
	return g
}

// rejectFn is a resource's reject share for one evaluation: the resource being solved is
// overridden with its iterate, every other resource reads its latest record (this step when it is
// already integrated, else the previous step; zero before the first step).
type rejectFn func(*compiled) float64

func latestReject(self *compiled, selfReject float64) rejectFn {
	return func(c *compiled) float64 {
		if c == self {
			return selfReject
		}
		if !c.last.valid {
			return 0
		}
		return c.last.st.RejectShare
	}
}

// serve is the outcome of one evaluation of node n: its uses in order, then its calls in order.
func (cf *cflow) serve(n int, rej rejectFn) dist {
	fn := &cf.nodes[n]
	out := dist{ok: 1}
	for _, u := range fn.uses {
		if p := rej(u); p > 0 {
			out.add(u.key, out.ok*p)
			out.ok *= 1 - p
		}
	}
	for _, k := range fn.kids {
		e := cf.edge(k, rej)
		for _, f := range e.fails {
			status, class := CallerStatus(cf.nodes[k].policy, f.key.class, f.key.status, n == 0)
			out.add(outcomeKey{class: class, status: status, rule: f.key.rule}, out.ok*f.p)
		}
		out.ok *= e.ok
	}
	return out
}

// edge is the outcome of the call into node k under its policy, as the caller sees it before
// status mapping. Attempts are independent: a retryable failure (share r) is resent up to Retries
// times, so the call succeeds with ok x sum_{j<=Retries} r^j.
func (cf *cflow) edge(k int, rej rejectFn) dist {
	pol := cf.nodes[k].policy
	a := cf.serve(k, rej)
	r := 0.0
	for _, f := range a.fails {
		if !f.key.rule && pol.Retryable(f.key.class, f.key.status) {
			r += f.p
		}
	}
	attempts, rk, last := 0.0, 1.0, 1.0 // last ends as r^Retries, the share reaching the final attempt
	for range pol.Retries + 1 {
		last = rk
		attempts += rk
		rk *= r
	}
	out := dist{ok: a.ok * attempts}
	for _, f := range a.fails {
		if !f.key.rule && pol.Retryable(f.key.class, f.key.status) {
			out.add(f.key, f.p*last)
		} else {
			out.add(f.key, f.p*attempts)
		}
	}
	return out
}

// reach is the share of entry requests whose first attempt meets the gated resource: every use of
// every caller on the path admits it, every earlier call of those callers succeeds (retries
// included), and the node's own earlier uses admit it. Exactly 1 when nothing on the tree rejects.
//
// It gates; it does not resend. The resource's own rejections are resent under the load's Policy
// (the closed loop of design §3.3, unchanged), and a failure elsewhere that a caller on the path
// resends does not add arrivals here (v1: such a resend would need the hold of a failing call,
// which the model does not shorten).
func (g *gate) reach(rej rejectFn) float64 {
	cf := g.flow
	v := 1.0
	for i, n := range g.path {
		fn := &cf.nodes[n]
		if i == len(g.path)-1 {
			for _, u := range fn.uses[:g.use] {
				v *= 1 - rej(u)
			}
			return v
		}
		for _, u := range fn.uses {
			v *= 1 - rej(u)
		}
		next := g.path[i+1]
		for _, k := range fn.kids {
			if k == next {
				break
			}
			v *= cf.edge(k, rej).ok
		}
	}
	return v
}

// touched reports the resources whose reject share reach reads (the solved resource excluded by
// the caller): the uses on the path and the subtrees of earlier calls.
func (g *gate) touched(visit func(*compiled)) {
	cf := g.flow
	var subtree func(n int)
	subtree = func(n int) {
		for _, u := range cf.nodes[n].uses {
			visit(u)
		}
		for _, k := range cf.nodes[n].kids {
			subtree(k)
		}
	}
	for i, n := range g.path {
		fn := &cf.nodes[n]
		if i == len(g.path)-1 {
			for _, u := range fn.uses[:g.use] {
				visit(u)
			}
			return
		}
		for _, u := range fn.uses {
			visit(u)
		}
		next := g.path[i+1]
		for _, k := range fn.kids {
			if k == next {
				break
			}
			subtree(k)
		}
	}
}

// failKey is the outcome key of this resource's rejection, exactly as Apply reports it.
func (c *compiled) failKey() outcomeKey {
	e := Effect{}
	c.failArrival(&e)
	return outcomeKey{class: e.Class, status: e.Status, rule: c.r.Kind == KindRule}
}
