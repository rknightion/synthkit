// SPDX-License-Identifier: AGPL-3.0-only

package aiagent

import (
	"hash/fnv"
	"time"

	"github.com/rknightion/synthkit/internal/ledger"
	"github.com/rknightion/synthkit/internal/shape"
)

// turn-gap bounds: a per-turn wall-clock gap (user think-time + model latency) used to size a
// conversation's overall backdated window from its turn count. A multi-turn session of N turns
// spans roughly N * a per-turn gap drawn in [minTurnGap, maxTurnGap].
const (
	minTurnGapSec = 2.0
	maxTurnGapSec = 30.0
)

// minter is the aiagent workload's contribution to the master clock. It mints ONE *ledger.Request
// per arriving CONVERSATION (R-B2): one Request = one conversation = complete-on-arrival, so there
// is no cross-tick conversation state. The single minter mints for ALL declared agents — for each
// AgentDecl it derives expectedVolume = (sessions_per_min/60) * tickSec, StochasticRounds it, and
// mints that many Requests stamped with that agent's identity. ProjectBatch recovers the agent from
// Request.Route (= agent.Name; names are unique within a fleet).
type minter struct {
	workloadName string      // the workload instance name (ledger dispatch key)
	env          string      // environment binding (Request.Env)
	cluster      string      // cluster binding (Request.Cluster)
	agents       []AgentDecl // every agent in the fleet (one volume contribution each)
}

func newMinter(workloadName, env, cluster string, agents []AgentDecl) *minter {
	return &minter{workloadName: workloadName, env: env, cluster: cluster, agents: agents}
}

// Workload implements ledger.Minter — the dispatch key (the workload instance name). It MUST match
// the runner's byWorkload key so this ONE instance projects every minted conversation.
func (m *minter) Workload() string { return m.workloadName }

// expectedVolume is an agent's mean conversation arrivals over a tick of tickSec seconds:
// (sessions_per_min / 60) * tickSec. Exactly linear in tickSec ⇒ cadence-invariant (I10).
func (m *minter) expectedVolume(a AgentDecl, tickSec float64) float64 {
	spm := a.Activity.SessionsPerMin
	if spm <= 0 {
		return 0
	}
	return (spm / 60.0) * tickSec
}

// Mint fabricates this tick's conversation arrivals across every agent. For each agent it mints
// StochasticRound(expectedVolume) Requests, each carrying a fresh conversation correlation and a
// realistic multi-turn session duration.
func (m *minter) Mint(now time.Time, tickSec float64, eng *shape.Engine) []*ledger.Request {
	var out []*ledger.Request
	for i := range m.agents {
		a := m.agents[i]
		n := ledger.StochasticRound(m.expectedVolume(a, tickSec), eng.Float64())
		for range n {
			out = append(out, m.mintOne(a, now, eng))
		}
	}
	return out
}

// mintOne fabricates one conversation Request for agent a. The conversation_id is the
// Correlation.SessionID; Route carries the agent name (the ProjectBatch recovery key); Model is a
// weighted/seeded draw from the agent's models; Duration is a multi-turn session span derived from a
// SessionID-seeded turn count so the last turn ends ≈ now after backdating.
func (m *minter) mintOne(a AgentDecl, now time.Time, eng *shape.Engine) *ledger.Request {
	c := ledger.NewCorrelation()
	r := &ledger.Request{
		Correlation: c,
		Workload:    m.workloadName,
		Env:         m.env,
		Cluster:     m.cluster,
		Route:       a.Name, // agent recovery key (unique within the fleet)
		Model:       drawModel(a, c.SessionID),
		Provider:    a.Provider,
		Start:       now,
		Outcome:     ledger.OutcomeSuccess,
		StatusCode:  200,
	}
	r.Duration = sessionDuration(a, c.SessionID)
	return r
}

// TurnCount derives a deterministic turn count for a conversation from its conversation id, bounded
// by the agent's TurnsP50/P95 (single-shot archetypes are always 1 turn). Exported so the
// conversation builder derives the SAME count from the same id (one source of truth).
func TurnCount(a AgentDecl, convID string) int {
	if a.Archetype == "general_single_shot" {
		return 1
	}
	p50 := a.Activity.TurnsP50
	if p50 < 1 {
		p50 = 1
	}
	p95 := a.Activity.TurnsP95
	if p95 < p50 {
		p95 = p50
	}
	// Map a seeded uniform draw onto [p50, p95] with a long-ish tail toward p95. Using the seeded
	// hash keeps this deterministic per conversation id (no global rand; I12).
	u := seedUnit(convID, "turns")
	// 70% of conversations sit at/below p50, the rest stretch toward p95.
	var n int
	if u < 0.7 {
		// distribute within [1, p50]
		span := p50
		n = 1 + int(seedUnit(convID, "turns_lo")*float64(span))
		if n > p50 {
			n = p50
		}
	} else {
		span := p95 - p50 + 1
		n = p50 + int(seedUnit(convID, "turns_hi")*float64(span))
		if n > p95 {
			n = p95
		}
	}
	if n < 1 {
		n = 1
	}
	return n
}

// sessionDuration sizes a conversation's overall backdated window from its turn count and a
// per-turn gap drawn deterministically from the conversation id. RenderStart backdates the window
// so the LAST turn ends ≈ r.Start (≈ now).
func sessionDuration(a AgentDecl, convID string) time.Duration {
	turns := TurnCount(a, convID)
	// per-turn gap in [minTurnGapSec, maxTurnGapSec]
	gap := minTurnGapSec + seedUnit(convID, "gap")*(maxTurnGapSec-minTurnGapSec)
	secs := float64(turns) * gap
	if secs < minTurnGapSec {
		secs = minTurnGapSec
	}
	return time.Duration(secs * float64(time.Second))
}

// drawModel picks one model from the agent's declared set, deterministically from the conversation
// id (so a conversation pins one model across its turns). Empty model set ⇒ "".
func drawModel(a AgentDecl, convID string) string {
	if len(a.Models) == 0 {
		return ""
	}
	if len(a.Models) == 1 {
		return a.Models[0]
	}
	i := int(seedUnit(convID, "model") * float64(len(a.Models)))
	if i >= len(a.Models) {
		i = len(a.Models) - 1
	}
	return a.Models[i]
}

// seedHash returns a stable FNV-1a hash of (convID, salt) — the deterministic per-conversation
// entropy source (no global rand; I12).
func seedHash(convID, salt string) uint64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(convID))
	_, _ = h.Write([]byte{0})
	_, _ = h.Write([]byte(salt))
	return h.Sum64()
}

// seedUnit maps seedHash to a uniform [0,1).
func seedUnit(convID, salt string) float64 {
	return float64(seedHash(convID, salt)%1_000_000) / 1_000_000.0
}
