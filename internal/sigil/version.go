// SPDX-License-Identifier: AGPL-3.0-only

package sigil

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
)

// EffectiveVersion computes the sigil agent_version digest for a given system prompt, replicating
// the backend agentmeta.go canonical_version=3 envelope (R-m1). The backend trusts an SDK-supplied
// value verbatim, so a stable, deterministic value is what matters. The digest is:
//
//	"sha256:" + hex(sha256(json.Marshal({canonical_version:3, system_prompt:<prompt>})))
//
// The JSON uses the exact field names that the backend persists ("canonical_version",
// "system_prompt") so a value produced here matches what the SDK would produce.
func EffectiveVersion(systemPrompt string) string {
	type envelope struct {
		CanonicalVersion int    `json:"canonical_version"`
		SystemPrompt     string `json:"system_prompt"`
	}
	b, err := json.Marshal(envelope{CanonicalVersion: 3, SystemPrompt: systemPrompt})
	if err != nil {
		// json.Marshal of a plain struct with string fields never errors.
		panic("sigil: EffectiveVersion: " + err.Error())
	}
	sum := sha256.Sum256(b)
	return "sha256:" + hex.EncodeToString(sum[:])
}
