// SPDX-License-Identifier: AGPL-3.0-only

package core

import "time"

// AgentProducerMode identifies the declared telemetry producer path, not a
// fallback policy or proof of runtime admission.
type AgentProducerMode string

const (
	AgentProducerSynthetic AgentProducerMode = "synthetic"
	AgentProducerReal      AgentProducerMode = "real"
)

// AgentServiceIdentity is the actual modeled service/resource identity.
type AgentServiceIdentity struct {
	Name, Namespace string
}

// AgentModelPair identifies a request model, not a rule's judge model.
type AgentModelPair struct {
	Provider, Model string
}

// AgentHookPolicy contains resolved producer-owned policy. The first runtime
// slice admits preflight only; Timeout is the resolved positive bound.
type AgentHookPolicy struct {
	Enabled  bool
	Phases   []string
	FailOpen bool
	Timeout  time.Duration
}

// AgentDeclaration is a neutral, resolved structural claim. A tools-only source
// may be real with no Models; real does not imply a model generation. Effective
// projected child names inherit service, mode and models, with hooks disabled.
// Destination, credentials, receipts and wire configuration do not belong here.
type AgentDeclaration struct {
	Name                string
	Service             AgentServiceIdentity
	ProducerMode        AgentProducerMode
	HookPolicy          AgentHookPolicy
	Models              []AgentModelPair
	ProjectedChildNames []string
}
