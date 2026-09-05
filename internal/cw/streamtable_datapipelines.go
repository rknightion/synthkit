// SPDX-License-Identifier: AGPL-3.0-only

package cw

// streamTableDataPipelines owns the AWS/MWAA, AmazonMWAA, and Glue namespaces.
// Entries remain absent until their exact AWS name, namespace, unit, and dimensions are cited.
func streamTableDataPipelines() streamTable {
	return streamTable{
		entries:    map[string]StreamEntry{},
		dimensions: map[string]map[string]string{},
	}
}
