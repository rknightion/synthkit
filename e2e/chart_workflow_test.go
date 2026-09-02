// SPDX-License-Identifier: AGPL-3.0-only

package e2e

import (
	"os"
	"strings"
	"testing"
)

// TestScheduledChartE2EWorkflowPins the reachability contract: a chart-e2e test
// that only exists locally is not an acceptance check until a scheduled workflow
// invokes its just target. Keep this deliberately textual so the test proves the
// executable workflow source, rather than an unrelated parsed representation.
func TestScheduledChartE2EWorkflow(t *testing.T) {
	raw, err := os.ReadFile("../.github/workflows/chart-e2e.yml")
	if err != nil {
		t.Fatalf("read scheduled chart workflow: %v", err)
	}
	workflow := string(raw)
	for _, want := range []string{
		"on:\n  schedule:",
		"cron:",
		"workflow_dispatch:",
		"run: just chart-e2e",
		"SYNTHKIT_E2E_INCLUDE_AGENT: 'false'",
	} {
		if !strings.Contains(workflow, want) {
			t.Errorf("scheduled chart workflow missing %q", want)
		}
	}
}
