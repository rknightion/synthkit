// SPDX-License-Identifier: AGPL-3.0-only

package cloudwatchlogs

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sort"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/shape"
	"github.com/rknightion/synthkit/internal/sink/promrw"
)

// Fixed-input predecessor equivalence, pinned before adding subscription filters.
// Sort only map iteration order; retain exact timestamps, kinds, values and labels.
func TestLegacyIntakeFixedInput(t *testing.T) {
	cfg := config(1.0 / 128)
	cfg.LogGroups = append(cfg.LogGroups, LogGroup{Name: "fast", EventsPerSecond: ptr(2), UncompressedBytesPerEvent: ptr(65536)}, LogGroup{Name: "idle", EventsPerSecond: ptr(0), UncompressedBytesPerEvent: ptr(1)})
	c := build(t, cfg)
	eng := &shape.Engine{}
	now := time.Unix(1800000000, 0)
	var observations [][]promrw.Series
	for _, step := range []struct {
		offset time.Duration
		volume float64
	}{
		{0, 1}, {time.Second, 2}, {time.Minute, 1}, {68 * time.Second, 1}, {68 * time.Second, 1}, {time.Second, 1}, {24 * time.Hour, 1},
		{25 * time.Hour, 0}, {26 * time.Hour, 2}, {26*time.Hour + time.Minute, 2}, {26*time.Hour + 68*time.Second, 1},
	} {
		eng.SetVolumeMultiplier(step.volume)
		batch := tick(t, c, now.Add(step.offset), eng).batch
		sort.Slice(batch, func(i, j int) bool {
			a, _ := json.Marshal(batch[i])
			b, _ := json.Marshal(batch[j])
			return string(a) < string(b)
		})
		observations = append(observations, batch)
	}
	b, e := json.Marshal(observations)
	if e != nil {
		t.Fatal(e)
	}
	got := fmt.Sprintf("%x", sha256.Sum256(b))
	const predecessorDigest = "b82e752378c194afa06df2b71c4b6778a2eacdfc0228292c4f23582f45ebabea"
	if got != predecessorDigest {
		t.Fatalf("legacy intake digest %s, want %s", got, predecessorDigest)
	}
}
