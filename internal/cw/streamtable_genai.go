// SPDX-License-Identifier: AGPL-3.0-only

package cw

// streamTableGenAI owns the AWS/Bedrock and AWS/Bedrock-AgentCore namespaces.
// Entries remain absent until their exact AWS name, namespace, unit, and dimensions are cited.
func streamTableGenAI() streamTable {
	return streamTable{
		entries:    map[string]StreamEntry{},
		dimensions: map[string]map[string]string{},
	}
}
