// SPDX-License-Identifier: AGPL-3.0-only

package bpsource

import "testing"

func TestSeamTypesCompile(t *testing.T) {
	var _ GitClient = (*fakeGit)(nil)
	var _ SourceConfig = (*fakeConfig)(nil)
	if ProvGit != "git" || ProvUpload != "upload" || ProvBuiltin != "builtin" {
		t.Fatal("provenance constants drifted")
	}
}
