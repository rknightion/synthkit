// SPDX-License-Identifier: AGPL-3.0-only

// events_realism_test.go — realism assertions for the revised events.go.
// Tests the live contract from a live reference cluster (job=integrations/kubernetes/eventhandler):
//   - kubelet events are kind=Pod (NEVER Deployment+kubelet together)
//   - Warning events exist (sparse: BackOff, FailedScheduling)
//   - name is in structured metadata, NOT a stream label
//   - body carries objectRV= and eventRV=
//   - manifests carry action+k8s_kind+k8s_namespace_name labels, NO action="sync"
package k8scluster_test

import (
	"strings"
	"testing"

	"github.com/rknightion/synthkit/internal/core/coretest"
	"github.com/rknightion/synthkit/internal/sink/loki"
)

// evtHandlerStreams returns all eventhandler streams from a full tick.
func evtHandlerStreams(lc *coretest.LogCapture) []loki.Stream {
	var out []loki.Stream
	for _, st := range lc.Streams {
		if st.Labels["job"] == "integrations/kubernetes/eventhandler" {
			out = append(out, st)
		}
	}
	return out
}

// manifestStreams returns all manifests streams from a full tick.
func manifestStreams(lc *coretest.LogCapture) []loki.Stream {
	var out []loki.Stream
	for _, st := range lc.Streams {
		if st.Labels["job"] == "integrations/kubernetes/manifests" {
			out = append(out, st)
		}
	}
	return out
}

func TestEventRealism(t *testing.T) {
	cl := coretest.Cluster()
	c := buildConstruct(t, cl)
	mc := &coretest.MetricCapture{}
	lc := &coretest.LogCapture{}
	tick(t, c, mc, lc)

	// ── (1) No eventhandler stream may have kind=Deployment AND sourcecomponent=kubelet ──
	for _, st := range evtHandlerStreams(lc) {
		for _, ln := range st.Lines {
			if strings.Contains(ln.Body, "kind=Deployment") && strings.Contains(ln.Body, "sourcecomponent=kubelet") {
				t.Errorf("eventhandler: kind=Deployment together with sourcecomponent=kubelet is invalid (kubelet=Pod only); body=%q", ln.Body)
			}
		}
	}

	// ── (2) At least one Warning stream exists ──────────────────────────────────────────
	hadWarning := false
	for _, st := range evtHandlerStreams(lc) {
		if st.Labels["level"] == "Warning" {
			hadWarning = true
			break
		}
	}
	if !hadWarning {
		t.Error("eventhandler: no level=Warning stream emitted (sparse Warnings expected)")
	}

	// ── (3) At least one reason=BackOff stream exists ───────────────────────────────────
	hadBackOff := false
	for _, st := range evtHandlerStreams(lc) {
		if st.Labels["reason"] == "BackOff" {
			hadBackOff = true
			break
		}
	}
	if !hadBackOff {
		t.Error("eventhandler: no reason=BackOff stream emitted")
	}

	// ── (4) name is NOT a stream label — it must be in Line.Meta ────────────────────────
	for _, st := range evtHandlerStreams(lc) {
		if _, hasSL := st.Labels["name"]; hasSL {
			t.Errorf("eventhandler: 'name' must NOT be a stream label (should be in Meta); labels=%v", st.Labels)
		}
		for _, ln := range st.Lines {
			if _, hasMeta := ln.Meta["name"]; !hasMeta {
				t.Errorf("eventhandler: 'name' must be in Line.Meta (structured metadata); meta=%v body=%q", ln.Meta, ln.Body)
			}
		}
	}

	// ── (5) body carries objectRV= and eventRV= ─────────────────────────────────────────
	hadObjectRV := false
	hadEventRV := false
	for _, st := range evtHandlerStreams(lc) {
		for _, ln := range st.Lines {
			if strings.Contains(ln.Body, "objectRV=") {
				hadObjectRV = true
			}
			if strings.Contains(ln.Body, "eventRV=") {
				hadEventRV = true
			}
		}
	}
	if !hadObjectRV {
		t.Error("eventhandler: body must contain objectRV=")
	}
	if !hadEventRV {
		t.Error("eventhandler: body must contain eventRV=")
	}

	// ── (6) manifests carry action+k8s_kind+k8s_namespace_name labels ─────────────────
	mss := manifestStreams(lc)
	if len(mss) == 0 {
		t.Fatal("no manifests stream found")
	}
	for _, st := range mss {
		if st.Labels["action"] == "" {
			t.Errorf("manifests: missing 'action' label; labels=%v", st.Labels)
		}
		if st.Labels["k8s_kind"] == "" {
			t.Errorf("manifests: missing 'k8s_kind' label; labels=%v", st.Labels)
		}
		if st.Labels["k8s_namespace_name"] == "" {
			t.Errorf("manifests: missing 'k8s_namespace_name' label; labels=%v", st.Labels)
		}
		// Must NOT have action="sync"
		if st.Labels["action"] == "sync" {
			t.Errorf("manifests: action='sync' is invalid vocab; labels=%v", st.Labels)
		}
	}

	// ── (7) no action="sync" anywhere in any stream ─────────────────────────────────────
	for _, st := range lc.Streams {
		if st.Labels["action"] == "sync" {
			t.Errorf("stream has action='sync' (invalid vocab); labels=%v", st.Labels)
		}
		for _, ln := range st.Lines {
			if strings.Contains(ln.Body, `"action":"sync"`) {
				t.Errorf("stream body contains '\"action\":\"sync\"' (invalid vocab); body=%q", ln.Body)
			}
		}
	}
}
