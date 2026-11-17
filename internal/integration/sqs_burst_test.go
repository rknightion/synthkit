// SPDX-License-Identifier: AGPL-3.0-only

package integration_test

import (
	"context"
	"crypto/sha256"
	"fmt"
	"math"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/blueprint"
	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/core/coretest"
	"github.com/rknightion/synthkit/internal/runner"
	"github.com/rknightion/synthkit/internal/shape"
)

const burstFixture = "../construct/sqs/testdata/burst-arrivals.yaml"
const burstDecl = "            burst: {period: 30m, duty: 0.25}\n"

// loadSQSBurst loads the burst fixture through the real loader and builds its sqs construct.
func loadSQSBurst(t *testing.T, fixture string, mutate func(string) string) (core.Construct, error) {
	t.Helper()
	data, err := os.ReadFile(fixture)
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
		return cr.Build(ci.Config, ci.Fixtures)
	}
	t.Fatal("no sqs construct in the fixture")
	return nil, nil
}

type burstMinute struct{ sent, received, visible float64 }

// runSQSBurst ticks the construct once a minute for the given number of minutes from an
// epoch-aligned half hour; minute i covers (start+(i-1)m, start+i m].
func runSQSBurst(t *testing.T, c core.Construct, minutes int) (map[int]burstMinute, string) {
	t.Helper()
	eng := shape.New("UTC", nil)
	start := time.Date(2026, 10, 24, 10, 0, 0, 0, time.UTC)
	out := map[int]burstMinute{}
	h := sha256.New()
	for i := 1; i <= minutes; i++ {
		mc := &coretest.MetricCapture{}
		if err := c.Tick(context.Background(), start.Add(time.Duration(i)*time.Minute), &core.World{Shape: eng, Metrics: mc}); err != nil {
			t.Fatal(err)
		}
		var lines []string
		var m burstMinute
		for _, s := range mc.All() {
			lines = append(lines, fmt.Sprintf("%d %s %v %v", i, s.Name, s.Labels, s.Value))
			switch s.Name {
			case "aws_sqs_number_of_messages_sent_sum":
				m.sent = s.Value
			case "aws_sqs_number_of_messages_received_sum":
				m.received = s.Value
			case "aws_sqs_approximate_number_of_messages_visible_maximum":
				m.visible = s.Value
			}
		}
		sort.Strings(lines)
		fmt.Fprintln(h, strings.Join(lines, "\n"))
		out[i] = m
	}
	return out, fmt.Sprintf("%x", h.Sum(nil))
}

// A queue that declares a burst, loaded through the real loader and catalog, takes the same mean
// arrival volume as the unbursted queue but delivers it inside the declared on-window of each
// epoch-aligned period: the visible backlog rises during the burst, drains afterwards, and the
// per-period totals match the unbursted mean.
func TestSQSBurstConcentratesArrivalsAndFormsBacklog(t *testing.T) {
	c, err := loadSQSBurst(t, burstFixture, nil)
	if err != nil {
		t.Fatal(err)
	}
	m, _ := runSQSBurst(t, c, 180)

	for p := 0; p < 6; p++ {
		base := p * 30 // minutes 1..7 are fully inside the 7.5 minute on-window, 8 is half inside
		var total float64
		for i := 1; i <= 30; i++ {
			sent := m[base+i].sent
			total += sent
			switch {
			case i <= 7:
				if sent < 479 || sent > 481 {
					t.Fatalf("period %d minute %d: sent %v, want 8 msg/s x 60 s", p, i, sent)
				}
			case i == 8:
				if sent < 239 || sent > 241 {
					t.Fatalf("period %d minute %d: sent %v, want half a minute at 8 msg/s", p, i, sent)
				}
			default:
				if sent != 0 {
					t.Fatalf("period %d minute %d: sent %v outside the on-window", p, i, sent)
				}
			}
		}
		if math.Abs(total-3600) > 1 {
			t.Fatalf("period %d: sent %v, want the unbursted mean 2 msg/s x 1800 s = 3600", p, total)
		}
		// Arrivals (8/s) beat receives (4/s) in the burst: the backlog rises every minute until the
		// on-window ends (peak 450 s x 4/s = 1800), then drains at 4/s and is empty by 900 s.
		for i := 2; i <= 8; i++ {
			if !(m[base+i].visible > m[base+i-1].visible) {
				t.Fatalf("period %d minute %d: backlog %v -> %v, want rising during the burst", p, i, m[base+i-1].visible, m[base+i].visible)
			}
		}
		if peak := m[base+8].visible; peak < 1700 || peak > 1801 {
			t.Fatalf("period %d: backlog peak %v, want about 1800", p, peak)
		}
		for i := 9; i <= 15; i++ {
			if !(m[base+i].visible < m[base+i-1].visible) {
				t.Fatalf("period %d minute %d: backlog %v -> %v, want draining after the burst", p, i, m[base+i-1].visible, m[base+i].visible)
			}
		}
		for i := 16; i <= 30; i++ {
			if m[base+i].visible != 0 {
				t.Fatalf("period %d minute %d: backlog %v, want drained", p, i, m[base+i].visible)
			}
		}
	}
}

// Phase moves the on-window within the period; the volume and the shape are unchanged.
func TestSQSBurstPhaseShiftsTheOnWindow(t *testing.T) {
	c, err := loadSQSBurst(t, burstFixture, func(s string) string {
		return strings.Replace(s, "duty: 0.25}", "duty: 0.25, phase: 10m}", 1)
	})
	if err != nil {
		t.Fatal(err)
	}
	m, _ := runSQSBurst(t, c, 60)
	var total float64
	for i := 1; i <= 60; i++ {
		total += m[i].sent
		switch {
		case i <= 10 || i >= 19 && i <= 40 || i >= 49:
			if m[i].sent != 0 {
				t.Fatalf("minute %d: sent %v outside the on-window", i, m[i].sent)
			}
		case i >= 11 && i <= 17 || i >= 41 && i <= 47:
			if m[i].sent < 479 || m[i].sent > 481 {
				t.Fatalf("minute %d: sent %v, want 8 msg/s x 60 s", i, m[i].sent)
			}
		}
	}
	if math.Abs(total-7200) > 1 {
		t.Fatalf("sent %v over two periods, want 7200", total)
	}
}

// An undeclared burst leaves the queue's output exactly what it was before the key existed, and a
// declared duty of 1 is a continuous stream, byte-identical to the undeclared queue.
func TestSQSUndeclaredBurstOutputIsUnchanged(t *testing.T) {
	strip := func(s string) string { return strings.Replace(s, burstDecl, "", 1) }
	c, err := loadSQSBurst(t, burstFixture, strip)
	if err != nil {
		t.Fatal(err)
	}
	m, hash := runSQSBurst(t, c, 90)
	for i := 1; i <= 90; i++ {
		if m[i].sent != 120 || m[i].visible != 0 {
			t.Fatalf("minute %d: sent %v visible %v, want the constant 2 msg/s and no backlog", i, m[i].sent, m[i].visible)
		}
	}
	// Recorded from the unmodified base commit (before the burst key existed).
	const wantBaseHash = "b1769f353e6eba60aed86086fd048de6458807ee9f04821a92a7930b81b0b870"
	if hash != wantBaseHash {
		t.Fatalf("undeclared output hash %s, want %s", hash, wantBaseHash)
	}

	full, err := loadSQSBurst(t, burstFixture, func(s string) string {
		return strings.Replace(s, "duty: 0.25}", "duty: 1}", 1)
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, got := runSQSBurst(t, full, 90); got != hash {
		t.Fatalf("duty 1 output %s differs from the undeclared output %s", got, hash)
	}
}

// The loader rejects a burst that cannot be honoured, and a bound queue, whose arrivals the
// causal resource owns, cannot declare one.
func TestSQSBurstDeclarationRejections(t *testing.T) {
	replace := func(old, repl string) func(string) string {
		return func(s string) string { return strings.Replace(s, old, repl, 1) }
	}
	for name, tc := range map[string]struct {
		fixture string
		mutate  func(string) string
		want    string
	}{
		"duty zero":        {burstFixture, replace("duty: 0.25", "duty: 0"), "burst.duty"},
		"duty above one":   {burstFixture, replace("duty: 0.25", "duty: 1.5"), "burst.duty"},
		"period too short": {burstFixture, replace("period: 30m", "period: 500ms"), "burst.period"},
		"period zero":      {burstFixture, replace("period: 30m", "period: 0s"), "burst.period"},
		"phase at period":  {burstFixture, replace("duty: 0.25}", "duty: 0.25, phase: 30m}"), "burst.phase"},
		"negative phase":   {burstFixture, replace("duty: 0.25}", "duty: 0.25, phase: -1m}"), "burst.phase"},
		"no arrivals":      {burstFixture, replace("arrivals_per_second: 2", "arrivals_per_second: 0"), "arrivals_per_second"},
		"unknown key":      {burstFixture, replace("duty: 0.25}", "duty: 0.25, width: 5m}"), "width"},
		"on-window rate over the cap": {burstFixture, func(s string) string {
			return strings.Replace(strings.Replace(s, "arrivals_per_second: 2", "arrivals_per_second: 900000", 1), "duty: 0.25", "duty: 0.5", 1)
		}, "1000000"},
		"bound queue": {"../construct/sqs/testdata/queue-dependency-slow.yaml",
			replace("            resource: validation-queue\n", "            resource: validation-queue\n"+burstDecl), "burst"},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := loadSQSBurst(t, tc.fixture, tc.mutate)
			if err == nil {
				t.Fatal("accepted the declaration")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %q does not name %q", err, tc.want)
			}
		})
	}
}
