// SPDX-License-Identifier: AGPL-3.0-only

package app

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// A source belongs to the declaration, not to a metric/label invented by the catalog.
func TestCustomMetricsRequireSource(t *testing.T) {
	for _, source := range []string{"", "source: '   '"} {
		var cfg Config
		err := yaml.Unmarshal([]byte(`services:
  - name: api
    type: web
    custom_metrics:
      - name: declared_requests_total
        instrument: counter
        value: {const: 2}
        `+source+"\n"), &cfg)
		if err == nil {
			_, err = buildGraph(cfg.Services)
		}
		if err == nil || !strings.Contains(err.Error(), "source") {
			t.Fatalf("missing source must be rejected, got %v", err)
		}
	}
}
