// SPDX-License-Identifier: AGPL-3.0-only

package cw

// streamTableCWInfra owns the CloudWatch infrastructure namespaces emitted by cwinfra.
// Entries remain absent until their exact AWS name, namespace, unit, and dimensions are cited.
func streamTableCWInfra() streamTable {
	return streamTable{
		entries:    map[string]StreamEntry{},
		dimensions: map[string]map[string]string{},
	}
}
