// SPDX-License-Identifier: AGPL-3.0-only

package aiagent

import (
	"fmt"

	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/sigil"
)

// agentDeclarations describes the complete configured roster, including inactive
// agents. It must not build conversations or consult runtime collaborators.
func agentDeclarations(cfg any, _ core.Binding) ([]core.AgentDeclaration, error) {
	c, ok := cfg.(*Config)
	if !ok || c == nil {
		return nil, fmt.Errorf("ai_agent: invalid declaration config")
	}
	out := make([]core.AgentDeclaration, 0, len(c.Agents))
	names := make(map[string]bool, len(c.Agents))
	for _, a := range c.Agents {
		if a.Name == "" || names[a.Name] {
			return nil, fmt.Errorf("ai_agent: empty or ambiguous agent declaration")
		}
		names[a.Name] = true
		switch a.Archetype {
		case archCodingClaudeCode, archCodingCodex, archGeneralOrchestration, archGeneralAutonomousLoop, archGeneralMultiturn, archGeneralSingleShot:
		default:
			return nil, fmt.Errorf("ai_agent: unsupported declaration archetype")
		}
		service := c.Resource.ServiceName
		// Match resourceAttrs: general agents have no service-name fallback.
		if service == "" && sigil.IsCoding(a.Archetype) {
			service = "agento11y"
		}
		d := core.AgentDeclaration{Name: a.Name, Service: core.AgentServiceIdentity{Name: service, Namespace: c.Resource.ServiceNamespace}, ProducerMode: core.AgentProducerSynthetic}
		for _, model := range a.Models {
			d.Models = append(d.Models, core.AgentModelPair{Provider: a.Provider, Model: model})
		}
		// These are the ONLY two fanout consumers in the current emitter. In
		// particular coding_codex ignores Subagents rather than prefixing them.
		children := map[string]bool{a.Name: true}
		if a.Archetype == archGeneralOrchestration || a.Archetype == archCodingClaudeCode {
			for _, peer := range a.Subagents {
				if peer == "" {
					return nil, fmt.Errorf("ai_agent: empty projected child")
				}
				child := peer
				if a.Archetype == archCodingClaudeCode {
					child = a.Name + "/" + peer
				}
				if children[child] {
					return nil, fmt.Errorf("ai_agent: ambiguous projected child")
				}
				children[child] = true
				d.ProjectedChildNames = append(d.ProjectedChildNames, child)
			}
		}
		out = append(out, d)
	}
	return out, nil
}
