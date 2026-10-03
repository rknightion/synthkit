// SPDX-License-Identifier: AGPL-3.0-only
package alloyhealth_test

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/blueprint"
	"github.com/rknightion/synthkit/internal/core/coretest"
	"github.com/rknightion/synthkit/internal/runner"
)

// Catches a config that is selectable only in direct Build tests, mislabelled
// Loki instruments, invented OTel parser counters, and non-cumulative intake.
func TestSyslogPublicLoaderHealth(t *testing.T) {
	data, err := os.ReadFile("../../../e2e/fixtures/ai-factory-syslog.yaml")
	if err != nil {
		t.Fatal(err)
	}
	for _, profile := range []string{"loki", "otel", "disabled"} {
		t.Run(profile, func(t *testing.T) {
			text := string(data)
			if profile == "otel" {
				text = strings.Replace(text, "receiver: loki", "receiver: otel\n            receiver_id: syslog", 1)
			}
			if profile == "disabled" {
				text = strings.Replace(text, "          syslog:\n            receiver: loki\n", "", 1)
				text = strings.Replace(text, "          syslog_records_per_min: 1", "", 1)
			}
			reg := runner.Catalog()
			bp, err := blueprint.Load([]byte(text), reg)
			if err != nil {
				t.Fatal(err)
			}
			cap := &coretest.MetricCapture{}
			r := runner.New(runner.Sinks{Metrics: cap, Logs: &coretest.LogCapture{}}, reg, runner.Options{})
			if err = r.AddBlueprint(bp); err != nil {
				t.Fatal(err)
			}
			now := time.Date(2026, 10, 5, 14, 0, 0, 0, time.UTC)
			if err = r.RunOnce(context.Background(), now); err != nil {
				t.Fatal(err)
			}
			count := 0
			for _, s := range cap.All() {
				if !strings.Contains(s.Name, "syslog_") && !strings.Contains(s.Name, "log_records_total") {
					continue
				}
				count++
				if _, ok := s.Labels["blueprint"]; ok {
					t.Fatal("substrate selector")
				}
				if _, ok := s.Labels["transport"]; ok {
					t.Fatal("empty transport")
				}
				if profile == "loki" && len(s.Labels) != 5 {
					t.Fatalf("unexpected instrument labels: %+v", s)
				}
				if profile == "otel" && s.Labels["receiver"] != "syslog" {
					t.Fatalf("missing receiver: %+v", s)
				}
			}
			if profile == "disabled" {
				if count != 0 {
					t.Fatalf("default disabled emits %d syslog series", count)
				}
				return
			}
			if count != 6 {
				t.Fatalf("syslog series=%d want 3 counters x 2 scrape identities", count)
			}
			name := "loki_source_syslog_entries_total"
			if profile == "otel" {
				name = "otelcol_receiver_accepted_log_records_total"
			}
			first := cap.Find(name)
			if err = r.RunOnce(context.Background(), now.Add(time.Minute)); err != nil {
				t.Fatal(err)
			}
			both := cap.Find(name)
			if len(both) != 4 || first[0].Value <= 0 || both[2].Value <= first[0].Value || both[3].Value <= first[1].Value {
				t.Fatalf("intake must accumulate across ticks: %+v", both)
			}
		})
	}
}
