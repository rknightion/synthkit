// SPDX-License-Identifier: AGPL-3.0-only

package selfobs

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log"
	"sort"
	"sync"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// Delivery-manifest export names. Instruments follow the existing synthkit.<area>.<thing> form;
// point dimensions reuse the bare sink key and add a bare state key (as outcome is bare), while
// the provenance key is synthkit-namespaced like the session resource attributes.
const (
	MetricManifestLane    = "synthkit.delivery.manifest.lane"
	MetricManifestAdopted = "synthkit.delivery.manifest.adopted_timestamp"
	AttrManifestRevision  = "synthkit.delivery.manifest.revision"

	// maxManifestLanes caps exported lane points per collection. The composition root supplies
	// at most one lane per fixed sink queue (eight today); the cap holds even if that grows.
	maxManifestLanes = 16
)

// Closed vocabularies. A lane outside them is not exported (and is logged once per revision), so
// a caller can never widen label cardinality.
var (
	manifestSinks     = map[string]bool{"promrw": true, "loki": true, "otlp": true, "pyroscope": true, "faro": true, "otlpmetrics": true, "otlplogs": true, "sigil": true}
	manifestStates    = map[string]bool{"expected": true, "disabled": true, "unsupported": true, "standby": true, "dry_run": true}
	manifestFreshness = map[string]bool{"scheduled": true, "request_driven": true}
)

// processNonce is 128 random bits drawn once per process and never exported. It is hashed into
// every revision, so a restart that reuses hostname and PID (and, with HA off, has no session)
// still publishes new provenance. No session is inferred from it.
var processNonce = sync.OnceValue(func() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		panic("selfobs: crypto/rand: " + err.Error()) // rand.Read does not fail on supported platforms
	}
	return hex.EncodeToString(b)
})

// ManifestLane is one intended delivery lane as the composition root reports it. Sink is a fixed
// queue name, State and Freshness closed words, FreshAfter the lane's bound (a missing-evidence
// bound only when Freshness is scheduled).
type ManifestLane struct {
	Sink       string
	State      string
	Freshness  string
	FreshAfter time.Duration
}

// DeliveryManifest is the composition root's bounded expectation of what the running set
// intends to deliver. Fingerprint digests the effective configuration it was derived from.
type DeliveryManifest struct {
	Fingerprint string
	Lanes       []ManifestLane
}

type manifestState struct {
	revision string
	adopted  int64
}

// manifestRevision binds a manifest to this process (its unexported nonce), its session (the
// exported session ID) and its effective configuration (the fingerprint and the exported lanes).
// It is the first 64 bits of a SHA-256, hex encoded, and carries none of its inputs.
func manifestRevision(nonce, session string, m DeliveryManifest) string {
	h := sha256.New()
	_, _ = fmt.Fprintf(h, "synthkit.delivery.manifest.v2\x00%s\x00%s\x00%s\x00", nonce, session, m.Fingerprint)
	for _, l := range m.Lanes {
		_, _ = fmt.Fprintf(h, "%s\x00%s\x00%s\x00%d\x00", l.Sink, l.State, l.Freshness, l.FreshAfter)
	}
	return hex.EncodeToString(h.Sum(nil)[:8])
}

// boundManifest keeps one in-vocabulary lane per sink in sink order, caps the point count, and
// returns how many lanes it rejected.
func boundManifest(m DeliveryManifest) (DeliveryManifest, int) {
	seen := map[string]bool{}
	rejected := 0
	lanes := make([]ManifestLane, 0, len(m.Lanes))
	for _, l := range m.Lanes {
		if !manifestSinks[l.Sink] || !manifestStates[l.State] || !manifestFreshness[l.Freshness] || seen[l.Sink] {
			rejected++
			continue
		}
		seen[l.Sink] = true
		lanes = append(lanes, l)
	}
	sort.Slice(lanes, func(i, j int) bool { return lanes[i].Sink < lanes[j].Sink })
	if len(lanes) > maxManifestLanes {
		rejected += len(lanes) - maxManifestLanes
		lanes = lanes[:maxManifestLanes]
	}
	m.Lanes = lanes
	return m, rejected
}

// registerManifest exports the manifest as two observable gauges read in one callback, so lane
// points and the adoption point always carry the same revision. An empty manifest still exports
// the adoption point: "no lanes intended" is distinct from "no manifest".
func (s *SelfObs) registerManifest(m metric.Meter, source func() DeliveryManifest) error {
	lane, err := m.Float64ObservableGauge(MetricManifestLane, metric.WithUnit("s"),
		metric.WithDescription("intended delivery lanes of the running set by sink, state and freshness; value is the slowest declared cadence plus the batch deadline, a missing-evidence bound only for scheduled lanes"))
	if err != nil {
		return err
	}
	adopted, err := m.Int64ObservableGauge(MetricManifestAdopted, metric.WithUnit("s"),
		metric.WithDescription("Unix time at which this process first exported the current delivery-manifest revision"))
	if err != nil {
		return err
	}
	_, err = m.RegisterCallback(func(_ context.Context, o metric.Observer) error {
		manifest, rejected := boundManifest(source())
		session := s.sessionID
		if session == "" {
			session = SessionUnknown
		}
		revision := manifestRevision(processNonce(), session, manifest)
		s.manifestMu.Lock()
		changed := s.manifest.revision != revision
		if changed {
			s.manifest = manifestState{revision: revision, adopted: time.Now().Unix()}
		}
		since := s.manifest.adopted
		s.manifestMu.Unlock()
		if changed && rejected > 0 {
			log.Printf("selfobs: delivery manifest revision %s rejected %d lane(s) outside the closed sink/state/freshness sets", revision, rejected)
		}
		rev := attribute.String(AttrManifestRevision, revision)
		for _, l := range manifest.Lanes {
			o.ObserveFloat64(lane, l.FreshAfter.Seconds(), metric.WithAttributes(
				attribute.String("sink", l.Sink), attribute.String("state", l.State),
				attribute.String("freshness", l.Freshness), rev))
		}
		o.ObserveInt64(adopted, since, metric.WithAttributes(rev))
		return nil
	}, lane, adopted)
	return err
}
