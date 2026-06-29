// SPDX-License-Identifier: AGPL-3.0-only

package capture

import (
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"testing"
)

// TestCaptureTrustBoundary is the permanent regression guard for the trust boundary: internal/capture
// and cmd/skcapture are shipped in the customer-facing inspector and MUST NOT import any synthkit
// composition/emission internals. The existing TestCatalogImportIsolation scans only construct/workload
// dirs, so capture is covered by nothing else. This walks the non-test Go files of both dirs and fails
// on any forbidden import.
func TestCaptureTrustBoundary(t *testing.T) {
	forbidden := []string{
		"github.com/rknightion/synthkit/internal/blueprint", "github.com/rknightion/synthkit/internal/core", "github.com/rknightion/synthkit/internal/runner",
		"github.com/rknightion/synthkit/internal/bpsource", "github.com/rknightion/synthkit/internal/construct", "github.com/rknightion/synthkit/internal/workload",
	}
	for _, dir := range []string{".", "../../cmd/skcapture"} {
		files, _ := filepath.Glob(filepath.Join(dir, "*.go"))
		for _, f := range files {
			if strings.HasSuffix(f, "_test.go") {
				continue
			}
			af, err := parser.ParseFile(token.NewFileSet(), f, nil, parser.ImportsOnly)
			if err != nil {
				t.Fatalf("parse %s: %v", f, err)
			}
			for _, imp := range af.Imports {
				p := strings.Trim(imp.Path.Value, `"`)
				for _, bad := range forbidden {
					if p == bad || strings.HasPrefix(p, bad+"/") {
						t.Errorf("%s imports forbidden %q (capture is a leaf trust-boundary lib)", f, p)
					}
				}
			}
		}
	}
}
