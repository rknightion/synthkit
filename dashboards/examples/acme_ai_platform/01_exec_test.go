// SPDX-License-Identifier: AGPL-3.0-only

package acme_ai_platform

import (
	"os"
	"strings"
	"testing"
)

func TestExecSpanMetricSuccessDenominatorIncludesUnset(t *testing.T) {
	source, err := os.ReadFile("01_exec.go")
	if err != nil {
		t.Fatalf("read dashboard source: %v", err)
	}
	text := string(source)
	if !strings.Contains(text, `status_code!="STATUS_CODE_ERROR"`) {
		t.Fatal("span-metric success denominator must select every non-error status")
	}
	if strings.Contains(text, `status_code!="STATUS_CODE_UNSET"`) {
		t.Fatal("span-metric success denominator must not exclude STATUS_CODE_UNSET")
	}
}
