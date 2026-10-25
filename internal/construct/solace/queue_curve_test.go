// SPDX-License-Identifier: AGPL-3.0-only

package solace_test

// queue_curve_test.go - loader-level proof that each queue can declare its own rate scale and
// load profile. The blueprint is decoded from YAML exactly as the loader decodes a construct
// config (unknown fields rejected), then built and ticked.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math"
	"sort"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/rknightion/synthkit/internal/construct/solace"
	"github.com/rknightion/synthkit/internal/core/coretest"
)

// curveYAML builds a one-broker config whose queues share one publish rate, so any divergence
// between them comes from the per-queue declarations. extra[queue] is the YAML appended to that
// queue's mapping.
func curveYAML(extra map[string]string) string {
	var b strings.Builder
	b.WriteString("targets: [QueueDetails, QueueStats]\nbrokers:\n  - name: broker-a\n    vpns:\n      - name: vpn-1\n        queues:\n")
	for _, q := range []string{"q-plain", "q-scaled", "q-tokyo", "q-flat", "q-idle"} {
		b.WriteString("          - name: " + q + "\n            publish_msgs_per_sec: 2\n")
		b.WriteString(extra[q])
	}
	return b.String()
}

func decodeStrict(t *testing.T, src string) *solace.Config {
	t.Helper()
	dec := yaml.NewDecoder(strings.NewReader(src))
	dec.KnownFields(true)
	var cfg solace.Config
	if err := dec.Decode(&cfg); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return &cfg
}

// spooledAt ticks a fresh build of src for n minutes from start and returns each queue's total
// solace_queue_msg_spooled.
func spooledAt(t *testing.T, src string, start time.Time, n int) map[string]float64 {
	t.Helper()
	c, err := solace.Build(decodeStrict(t, src), nil)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	mc := &coretest.MetricCapture{}
	w := coretest.World(mc, nil, nil)
	for i := range n {
		if err := c.Tick(context.Background(), start.Add(time.Duration(i)*time.Minute), w); err != nil {
			t.Fatalf("Tick %d: %v", i, err)
		}
	}
	out := map[string]float64{}
	for _, s := range mc.Find("solace_queue_msg_spooled") {
		out[s.Labels["queue_name"]] = s.Value
	}
	return out
}

func TestQueuesDeclareTheirOwnCurve(t *testing.T) {
	// 12:00 UTC on a Friday is 14:00 in the engine's Zurich zone (plateau) and 21:00 in Tokyo
	// (past the default ramp down, so at the floor).
	start := time.Date(2026, 6, 12, 12, 0, 0, 0, time.UTC)
	const ticks = 30

	base := spooledAt(t, curveYAML(nil), start, ticks)
	got := spooledAt(t, curveYAML(map[string]string{
		"q-scaled": "            rate_scale: 3\n",
		"q-tokyo":  "            shape_profile:\n              timezone: Asia/Tokyo\n",
		"q-flat":   "            shape_profile:\n              floor: 1\n              ramp_up_start_hour: 0\n              plateau_start_hour: 0\n              plateau_end_hour: 24\n              ramp_down_end_hour: 24\n",
		"q-idle":   "            rate_scale: 0\n",
	}), start, ticks)

	for _, q := range []string{"q-plain", "q-scaled", "q-tokyo", "q-flat", "q-idle"} {
		if base[q] <= 0 {
			t.Fatalf("base %s spooled %v, want > 0", q, base[q])
		}
	}

	// An undeclared queue is untouched by its neighbours' declarations.
	if got["q-plain"] != base["q-plain"] {
		t.Errorf("q-plain = %v, want %v (undeclared queue changed)", got["q-plain"], base["q-plain"])
	}
	// rate_scale 3 triples the volume (whole-message rounding carries over, so allow one message).
	if want := 3 * base["q-scaled"]; math.Abs(got["q-scaled"]-want) > 1.5 {
		t.Errorf("q-scaled = %v, want about %v (3x base)", got["q-scaled"], want)
	}
	// A Tokyo-zone profile reads 21:00 local: the floor (0.1) against the plateau (1.0).
	if r := got["q-tokyo"] / base["q-tokyo"]; r < 0.05 || r > 0.2 {
		t.Errorf("q-tokyo ratio = %v, want about 0.1", r)
	}
	// A flat-top profile carries the plateau all day, so a plateau-time run is unchanged.
	if math.Abs(got["q-flat"]-base["q-flat"]) > 1.5 {
		t.Errorf("q-flat = %v, want %v (plateau either way)", got["q-flat"], base["q-flat"])
	}
	if got["q-idle"] != 0 {
		t.Errorf("q-idle = %v, want 0", got["q-idle"])
	}

	// At 02:00 Zurich the blueprint curve is at its floor; the flat profile is not.
	night := time.Date(2026, 6, 12, 0, 0, 0, 0, time.UTC)
	nb := spooledAt(t, curveYAML(nil), night, ticks)
	ng := spooledAt(t, curveYAML(map[string]string{
		"q-flat": "            shape_profile:\n              floor: 1\n              ramp_up_start_hour: 0\n              plateau_start_hour: 0\n              plateau_end_hour: 24\n              ramp_down_end_hour: 24\n",
	}), night, ticks)
	if r := ng["q-flat"] / nb["q-flat"]; r < 5 {
		t.Errorf("night q-flat ratio = %v, want about 10 (flat profile vs floor)", r)
	}
	if ng["q-plain"] != nb["q-plain"] {
		t.Errorf("night q-plain = %v, want %v", ng["q-plain"], nb["q-plain"])
	}
}

// undeclaredDigest is the sha256 of every series (name, sorted labels, value, timestamp) the
// undeclared five-queue blueprint emits over 90 ticks, captured on the base commit before the
// per-queue fields existed. A change to any undeclared output changes the digest.
const undeclaredDigest = "7e3906e69af17c2f979fbc6d2eec98083e7645ccd76af29842f1ac71f19219df"

func digest(t *testing.T, src string) string {
	t.Helper()
	c, err := solace.Build(decodeStrict(t, src), nil)
	if err != nil {
		t.Fatal(err)
	}
	mc := &coretest.MetricCapture{}
	w := coretest.World(mc, nil, nil)
	start := time.Date(2026, 6, 12, 0, 0, 0, 0, time.UTC)
	for i := range 90 {
		if err := c.Tick(context.Background(), start.Add(time.Duration(i)*20*time.Minute), w); err != nil {
			t.Fatal(err)
		}
	}
	// Series order within a batch follows a map; sort the lines so the digest is order-free.
	var lines []string
	for _, s := range mc.All() {
		keys := make([]string, 0, len(s.Labels))
		for k := range s.Labels {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		var line strings.Builder
		fmt.Fprintf(&line, "%s|%d|%x", s.Name, s.Kind, math.Float64bits(s.Value))
		for _, k := range keys {
			fmt.Fprintf(&line, "|%s=%s", k, s.Labels[k])
		}
		fmt.Fprintf(&line, "|%d", s.T.UnixNano())
		lines = append(lines, line.String())
	}
	sort.Strings(lines)
	h := sha256.New()
	for _, l := range lines {
		fmt.Fprintln(h, l)
	}
	return hex.EncodeToString(h.Sum(nil))
}

func TestUndeclaredCurveIsByteIdenticalToBase(t *testing.T) {
	src := strings.Replace(curveYAML(nil), "targets: [QueueDetails, QueueStats]", "targets: [Vpn, VpnStats, VpnSpool, QueueDetails, QueueStats, QueueRates, GlobalStats, GlobalSystemInfo, Spool]", 1)
	if got := digest(t, src); got != undeclaredDigest {
		t.Errorf("undeclared output digest = %s, want %s (base)", got, undeclaredDigest)
	}
	// A declared scale of exactly 1 is the same curve.
	scaled := strings.Replace(src, "          - name: q-scaled\n            publish_msgs_per_sec: 2\n", "          - name: q-scaled\n            publish_msgs_per_sec: 2\n            rate_scale: 1\n", 1)
	if got := digest(t, scaled); got != undeclaredDigest {
		t.Errorf("rate_scale: 1 digest = %s, want %s", got, undeclaredDigest)
	}
}

func TestQueueCurveValidation(t *testing.T) {
	cases := map[string]string{
		"negative scale":   "            rate_scale: -1\n",
		"nan scale":        "            rate_scale: .nan\n",
		"overflow scale":   "            rate_scale: 1e308\n",
		"bad floor":        "            shape_profile:\n              floor: 2\n",
		"bad hour order":   "            shape_profile:\n              plateau_start_hour: 18\n",
		"bad timezone":     "            shape_profile:\n              timezone: Not/AZone\n",
		"negative weekend": "            shape_profile:\n              weekend_factor: -1\n",
	}
	for name, extra := range cases {
		t.Run(name, func(t *testing.T) {
			cfg := decodeStrict(t, curveYAML(map[string]string{"q-plain": extra}))
			if _, err := solace.Build(cfg, nil); err == nil {
				t.Fatalf("Build accepted %s", name)
			}
		})
	}
}
