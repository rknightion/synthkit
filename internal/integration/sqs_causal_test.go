// SPDX-License-Identifier: AGPL-3.0-only

package integration_test

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/blueprint"
	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/core/coretest"
	"github.com/rknightion/synthkit/internal/runner"
	"github.com/rknightion/synthkit/internal/shape"
)

// loadSQSCausal loads the fixture through the real loader and builds every sqs construct, the
// way the composition root does: bindings are validated by the construct's own Build.
func loadSQSCausal(t *testing.T, mutate func(string) string) (*blueprint.Resolved, error) {
	t.Helper()
	data, err := os.ReadFile("../construct/sqs/testdata/queue-dependency-slow.yaml")
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if mutate != nil {
		text = mutate(text)
	}
	reg := runner.Catalog()
	res, err := blueprint.Load([]byte(text), reg)
	if err != nil {
		return nil, err
	}
	for _, ci := range res.Constructs {
		if ci.Kind != "sqs" {
			continue
		}
		cr, _ := reg.Construct(ci.Kind)
		if _, err := cr.Build(ci.Config, ci.Fixtures); err != nil {
			return nil, err
		}
	}
	return res, nil
}

// A queue bound to a declared `queue` resource, loaded through the real loader and catalog, is
// driven by the causal model: a dependency_slow fault on the dependency its consumers hold each
// message across grows the visible backlog and the age of the oldest message while the fault is
// active, and both drain afterwards. The same declaration without the incident stays empty.
func TestSQSBoundQueueBacklogGrowsUnderDependencySlowAndDrains(t *testing.T) {
	res, err := loadSQSCausal(t, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Incidents) != 1 || !strings.HasPrefix(res.Incidents[0], "dependency_slow@2026-10-24T10:30/10m#1@external-validator") {
		t.Fatalf("compiled incidents %v", res.Incidents)
	}

	type minute struct{ visible, age, sent, received, deleted float64 }
	run := func(entries []string) map[int]minute {
		eng := shape.New("UTC", entries)
		reg := runner.Catalog()
		out := map[int]minute{}
		built := 0
		start := time.Date(2026, 10, 24, 10, 0, 0, 0, time.UTC)
		for _, ci := range res.Constructs {
			if ci.Kind != "sqs" {
				continue
			}
			built++
			cr, _ := reg.Construct(ci.Kind)
			c, err := cr.Build(ci.Config, ci.Fixtures)
			if err != nil {
				t.Fatal(err)
			}
			for i := 1; i <= 120; i++ {
				mc := &coretest.MetricCapture{}
				if err := c.Tick(context.Background(), start.Add(time.Duration(i)*time.Minute), &core.World{Shape: eng, Metrics: mc}); err != nil {
					t.Fatal(err)
				}
				var m minute
				for _, s := range mc.All() {
					switch s.Name {
					case "aws_sqs_approximate_number_of_messages_visible_maximum":
						m.visible = s.Value
					case "aws_sqs_approximate_age_of_oldest_message_maximum":
						m.age = s.Value
					case "aws_sqs_number_of_messages_sent_sum":
						m.sent = s.Value
					case "aws_sqs_number_of_messages_received_sum":
						m.received = s.Value
					case "aws_sqs_number_of_messages_deleted_sum":
						m.deleted = s.Value
					}
				}
				out[i] = m
			}
		}
		if built != 1 {
			t.Fatalf("want one sqs construct, built %d", built)
		}
		return out
	}

	healthy := run(nil)
	for i, m := range healthy {
		if m.visible != 0 || m.age != 0 {
			t.Fatalf("no incident, minute %d: backlog %v age %v", i, m.visible, m.age)
		}
		if m.sent < 479 || m.sent > 481 || m.received < 479 || m.received > 481 || m.deleted != m.received {
			t.Fatalf("no incident, minute %d: sent %v received %v deleted %v, want 480 each", i, m.sent, m.received, m.deleted)
		}
	}

	faulted := run(res.Incidents)
	// Minute i covers (10:00+i-1m, 10:00+i m]; the fault is active over [10:30, 10:40), so the
	// first faulted step lies in minute 30 and the last in minute 40.
	for i := 1; i <= 29; i++ {
		if faulted[i].visible != 0 || faulted[i].age != 0 {
			t.Fatalf("minute %d before the fault: backlog %v age %v", i, faulted[i].visible, faulted[i].age)
		}
	}
	for i := 32; i <= 40; i++ {
		if !(faulted[i].visible > faulted[i-1].visible && faulted[i].age > faulted[i-1].age) {
			t.Fatalf("minute %d during the fault: backlog %v -> %v, age %v -> %v; want both growing",
				i, faulted[i-1].visible, faulted[i].visible, faulted[i-1].age, faulted[i].age)
		}
		// Consumers slow to 3.64 msg/s (218/min) against 480/min of arrivals; minute 40 also holds
		// the first healthy step, so it is only checked for growth above.
		if i < 40 && (faulted[i].received < 210 || faulted[i].received > 226) || faulted[i].deleted != faulted[i].received {
			t.Fatalf("minute %d: received %v deleted %v, want about 218 each", i, faulted[i].received, faulted[i].deleted)
		}
		if faulted[i].sent < 479 || faulted[i].sent > 481 {
			t.Fatalf("minute %d: arrivals %v changed under a consumer-side fault", i, faulted[i].sent)
		}
	}
	peak := faulted[40]
	if peak.visible < 2000 || peak.age < 300 {
		t.Fatalf("backlog %v / age %v at the end of the fault, want a few thousand messages and minutes of age", peak.visible, peak.age)
	}
	// After the fault consumers outrun arrivals (10 vs 8 msg/s) and drain the backlog.
	for i := 41; i <= 60; i++ {
		if !(faulted[i].visible < faulted[i-1].visible && faulted[i].age < faulted[i-1].age) {
			t.Fatalf("minute %d after the fault: backlog %v -> %v, age %v -> %v; want both draining",
				i, faulted[i-1].visible, faulted[i].visible, faulted[i-1].age, faulted[i].age)
		}
	}
	if faulted[120].visible != 0 || faulted[120].age != 0 {
		t.Fatalf("backlog %v age %v two hours in, want drained", faulted[120].visible, faulted[120].age)
	}
}

// The loader rejects a bound queue that carries a field the model owns or one the queue
// resource cannot express on SQS.
func TestSQSBoundQueueDeclarationRejections(t *testing.T) {
	for name, tc := range map[string]struct {
		mutate func(string) string
		want   string
	}{
		"max_depth on the queue resource": {
			func(s string) string {
				return strings.Replace(s, "        size: 4\n", "        size: 4\n        max_depth: 100\n", 1)
			},
			"max_depth",
		},
		"arrivals_per_second on the bound queue": {
			func(s string) string {
				return strings.Replace(s, "            message_bytes: 2048\n", "            message_bytes: 2048\n            arrivals_per_second: 1\n", 1)
			},
			"arrivals_per_second",
		},
		"receives_per_second on the bound queue": {
			func(s string) string {
				return strings.Replace(s, "            message_bytes: 2048\n", "            message_bytes: 2048\n            receives_per_second: 1\n", 1)
			},
			"receives_per_second",
		},
		"processing_seconds on the bound queue": {
			func(s string) string {
				return strings.Replace(s, "            message_bytes: 2048\n", "            message_bytes: 2048\n            processing_seconds: 1\n", 1)
			},
			"processing_seconds",
		},
		"resource that is not declared": {
			func(s string) string {
				return strings.Replace(s, "            resource: validation-queue\n", "            resource: no-such-resource\n", 1)
			},
			"no-such-resource",
		},
		"resource of the wrong kind": {
			func(s string) string {
				return strings.Replace(s, "            resource: validation-queue\n", "            resource: external-validator\n", 1)
			},
			"queue",
		},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := loadSQSCausal(t, tc.mutate); err == nil {
				t.Fatal("loader accepted the declaration")
			} else if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %q does not name %q", err, tc.want)
			}
		})
	}
	if _, err := loadSQSCausal(t, nil); err != nil {
		t.Fatalf("the unmutated declaration must load: %v", err)
	}
}
