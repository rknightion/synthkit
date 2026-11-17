// SPDX-License-Identifier: AGPL-3.0-only

package rds_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/construct/rds"
	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/core/coretest"
	"github.com/rknightion/synthkit/internal/fixture"
	"github.com/rknightion/synthkit/internal/shape"
)

// seriesDigest renders every series of every captured batch (name, sorted labels, value,
// timestamp) into one SHA-256, so any change to an unbound instance's output is visible.
func seriesDigest(cap *coretest.MetricCapture) string {
	var lines []string
	for _, s := range cap.All() {
		keys := make([]string, 0, len(s.Labels))
		for k := range s.Labels {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		var b strings.Builder
		b.WriteString(s.Name)
		for _, k := range keys {
			fmt.Fprintf(&b, "|%s=%s", k, s.Labels[k])
		}
		fmt.Fprintf(&b, "|%.12g|%d", s.Value, s.T.UnixMilli())
		lines = append(lines, b.String())
	}
	sort.Strings(lines)
	sum := sha256.Sum256([]byte(strings.Join(lines, "\n")))
	return hex.EncodeToString(sum[:])
}

// unboundIncidents fires the three legacy database modes the construct implements against the
// fixture database, so the digest covers the unbound failure-mode paths too.
var unboundIncidents = []string{
	"connection_saturation@2026-10-24T10:10/10m#1@test-db",
	"lock_contention@2026-10-24T10:25/10m#1@test-db",
	"slow_query_storm@2026-10-24T10:40/10m#1@test-db",
}

func renderUnbound(t *testing.T, engine string, cfg any) string {
	t.Helper()
	fx := &fixture.Set{DB: coretest.DB(engine), Cloud: coretest.Cloud(), Env: coretest.Env(), Seed: "test"}
	c, err := rds.Build(cfg, fx)
	if err != nil {
		t.Fatal(err)
	}
	cap := &coretest.MetricCapture{}
	eng := shape.New("UTC", unboundIncidents)
	start := time.Date(2026, 10, 24, 10, 0, 0, 0, time.UTC)
	for i := 1; i <= 70; i++ {
		if err := c.Tick(context.Background(), start.Add(time.Duration(i)*time.Minute), &core.World{Shape: eng, Metrics: cap}); err != nil {
			t.Fatal(err)
		}
	}
	return seriesDigest(cap)
}

// Digests were recorded from the construct before causal binding existed; an instance that
// names no resource must keep emitting exactly them.
const (
	unboundPostgresDigest = "c33a217b161a3b6c091fdea3a7720213c9170b01af9b7b9a0960d46b6cc3a1a2"
	unboundMySQLDigest    = "209fbea0df25803d9fd11c22eb1c4ac3f61c70e27f45cf699234e05bc2ea1533"
)

func TestUnboundOutputIsByteIdenticalToBeforeBinding(t *testing.T) {
	for engine, want := range map[string]string{"postgres": unboundPostgresDigest, "mysql": unboundMySQLDigest} {
		if got := renderUnbound(t, engine, nil); got != want {
			t.Errorf("%s: unbound digest %s, want %s", engine, got, want)
		}
	}
}
