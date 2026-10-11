// SPDX-License-Identifier: AGPL-3.0-only
package control

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/ha"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes/scheme"
	coreclient "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/rest"
)

type namedMemory struct {
	object       *corev1.ConfigMap
	writes       int
	lose         bool
	beforeUpdate func()
	afterUpdate  func()
}

func (m *namedMemory) Get(ctx context.Context, name string, _ metav1.GetOptions) (*corev1.ConfigMap, error) {
	return m.object.DeepCopy(), ctx.Err()
}
func (m *namedMemory) Update(ctx context.Context, obj *corev1.ConfigMap, _ metav1.UpdateOptions) (*corev1.ConfigMap, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if m.beforeUpdate != nil {
		hook := m.beforeUpdate
		m.beforeUpdate = nil
		hook()
	}
	if obj.ResourceVersion != m.object.ResourceVersion {
		return nil, apierrors.NewConflict(schema.GroupResource{Resource: "configmaps"}, obj.Name, errors.New("stale"))
	}
	m.writes++
	m.object = obj.DeepCopy()
	m.object.ResourceVersion = m.object.ResourceVersion + "x"
	if m.afterUpdate != nil {
		hook := m.afterUpdate
		m.afterUpdate = nil
		hook()
	}
	if m.lose {
		m.lose = false
		return nil, errors.New("response lost after commit")
	}
	return m.object.DeepCopy(), nil
}
func testNamedBackend(t *testing.T, gate ha.LeaderGate) (StateBackend, *namedMemory) {
	t.Helper()
	m := &namedMemory{object: &corev1.ConfigMap{ObjectMeta: metav1.ObjectMeta{Name: "control", ResourceVersion: "v"}, Data: map[string]string{"unrelated": "retained"}}}
	b, err := newKubernetesBackend(KubernetesBackendOptions{Gate: gate, Namespace: "ns", Objects: map[Key]string{Control: "control"}, RequestTimeout: time.Second, MaxDocumentBytes: 786432}, m)
	if err != nil {
		t.Fatal(err)
	}
	return b, m
}
func TestNamedBackendCASAndStandby(t *testing.T) {
	ctx := context.Background()
	b, m := testNamedBackend(t, ha.AlwaysLeader{})
	snap, err := b.Load(ctx, Control)
	if err != nil {
		t.Fatal(err)
	}
	rev, err := b.CompareAndSwap(ctx, Control, snap.Revision, []byte(`{"volume_multiplier":2}`))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = b.CompareAndSwap(ctx, Control, snap.Revision, []byte(`{}`)); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale CAS: %v", err)
	}
	got, err := b.Load(ctx, Control)
	if err != nil || got.Revision != rev || string(got.Data) != `{"volume_multiplier":2}` {
		t.Fatalf("roundtrip: %+v %v", got, err)
	}
	if m.object.Data["unrelated"] != "retained" || m.writes != 1 {
		t.Fatal("metadata lost or stale write sent")
	}
	standby, sm := testNamedBackend(t, ha.NewGate())
	s, err := standby.Load(ctx, Control)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = standby.CompareAndSwap(ctx, Control, s.Revision, []byte(`{}`)); !errors.Is(err, ha.ErrNotLeader) || sm.writes != 0 {
		t.Fatalf("standby: %v writes=%d", err, sm.writes)
	}
}
func TestBackendOutcomeUnknownHTTPAndReadiness(t *testing.T) {
	ctx := context.Background()
	b, m := testNamedBackend(t, ha.AlwaysLeader{})
	store, err := NewBackendStore(ctx, b, ha.AlwaysLeader{}, 5)
	if err != nil {
		t.Fatal(err)
	}
	m.lose = true
	m.afterUpdate = func() {
		doc, err := decodeControl(m.object.BinaryData["document"])
		if err != nil {
			t.Fatal(err)
		}
		doc.Sequence += 300
		doc.ReceiptFloorSequence = doc.Sequence - 256
		doc.Receipts = nil
		data, err := json.Marshal(doc)
		if err != nil {
			t.Fatal(err)
		}
		m.object.BinaryData["document"] = data
		m.object.ResourceVersion += "evicted"
	}
	events := 0
	handler := NewHandler(store, func(State) { events++ }, "")
	server := httptest.NewServer(handler)
	defer server.Close()
	resp, err := http.Post(server.URL+"/control/reset", "application/json", strings.NewReader(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var body map[string]json.RawMessage
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 503 || string(body["code"]) != `"state_outcome_unknown"` || events != 0 {
		t.Fatalf("unknown acknowledged: status=%d body=%v events=%d", resp.StatusCode, body, events)
	}
	health := store.PersistHealth()
	report := EvaluateReadiness(ReadinessInput{ProcessRunning: true, HTTPServing: true, Blueprints: BlueprintReadiness{Active: 1}, PersistedState: PersistedStateReadiness{Writable: health.LastError == "", Error: health.LastError}})
	if report.Ready || health.LastError == "" {
		t.Fatal("unknown outcome remained ready")
	}
}

func TestBackendHTTPConflictAndLostIncidentReceipt(t *testing.T) {
	ctx := context.Background()
	b, m := testNamedBackend(t, ha.AlwaysLeader{})
	a, err := NewBackendStore(ctx, b, ha.AlwaysLeader{}, 5)
	if err != nil {
		t.Fatal(err)
	}
	other, err := NewBackendStore(ctx, b, ha.AlwaysLeader{}, 5)
	if err != nil {
		t.Fatal(err)
	}
	// Force the API conflict after the consumer has loaded its expected revision.
	m.beforeUpdate = func() {
		if _, err := other.UpdateContext(ctx, func(s *State) { s.Scaling["api"] = 9 }); err != nil {
			t.Fatal(err)
		}
		m.lose = true
	}
	applied := []State{}
	events := 0
	h := NewHandler(a, func(s State) { applied = append(applied, s) }, "", fakeIncidentSrc{}).SetChangeObserver(func(State) { events++ })
	srv := httptest.NewServer(h)
	defer srv.Close()
	resp, err := http.Post(srv.URL+"/control/incidents", "application/json", strings.NewReader(`{"blueprint":"mini","mode":"failure","for":"1m"}`))
	if err != nil {
		t.Fatal(err)
	}
	var got State
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 || got.Scaling["api"] != 9 || len(got.RuntimeIncidents) != 1 || events != 1 || len(applied) != 1 {
		t.Fatalf("consumer convergence: status=%d state=%+v events=%d applied=%d", resp.StatusCode, got, events, len(applied))
	}
	doc, err := decodeControl(m.object.BinaryData["document"])
	if err != nil {
		t.Fatal(err)
	}
	if doc.State.RuntimeIncidents[0].ID == "" || doc.State.RuntimeIncidents[0].ID != got.RuntimeIncidents[0].ID || m.writes != 2 {
		t.Fatal("incident replayed or identity changed")
	}
	_, err = a.UpdateContext(ctx, func(s *State) { s.BlueprintSources = []SourceView{{ID: "source"}} })
	if err != nil {
		t.Fatal(err)
	}
	out, err := a.ResetContext(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(out.BlueprintSources) != 0 || len(out.RuntimeIncidents) != 0 {
		t.Fatal("reset compatibility lost")
	}
	doc, err = decodeControl(m.object.BinaryData["document"])
	if err != nil || len(doc.Receipts) < 4 {
		t.Fatal("reset erased receipts", err)
	}
}

func TestKubernetesHTTPCommittedResponseLost(t *testing.T) {
	object := &corev1.ConfigMap{TypeMeta: metav1.TypeMeta{Kind: "ConfigMap", APIVersion: "v1"}, ObjectMeta: metav1.ObjectMeta{Name: "control", Namespace: "test", ResourceVersion: "1"}}
	writes := 0
	var mu sync.Mutex
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		if r.URL.Path != "/api/v1/namespaces/test/configmaps/control" {
			t.Errorf("unnamed API: %s", r.URL.Path)
			w.WriteHeader(403)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.Method {
		case "GET":
			json.NewEncoder(w).Encode(object)
		case "PUT":
			// Typed core clients prefer protobuf. Decode the real negotiated wire
			// format rather than failing before this fixture reaches its commit.
			mediaType := r.Header.Get("Content-Type")
			if mediaType != "application/vnd.kubernetes.protobuf" && mediaType != "application/json" {
				t.Errorf("unexpected request media type %q", mediaType)
				w.WriteHeader(415)
				return
			}
			body, err := io.ReadAll(r.Body)
			if err != nil {
				t.Error(err)
				w.WriteHeader(400)
				return
			}
			var next corev1.ConfigMap
			if _, _, err := scheme.Codecs.UniversalDeserializer().Decode(body, nil, &next); err != nil {
				t.Error(err)
				w.WriteHeader(400)
				return
			}
			if next.ResourceVersion != object.ResourceVersion {
				w.WriteHeader(409)
				w.Write([]byte(`{"kind":"Status","apiVersion":"v1","reason":"Conflict","code":409}`))
				return
			}
			writes++
			next.ResourceVersion = next.ResourceVersion + "x"
			object = &next
			// Actually commit on the API edge, then lose the response at the socket.
			conn, _, err := w.(http.Hijacker).Hijack()
			if err != nil {
				t.Error(err)
				return
			}
			conn.Close()
		default:
			t.Errorf("forbidden API verb %s", r.Method)
			w.WriteHeader(403)
		}
	}))
	defer server.Close()
	client, err := coreclient.NewForConfigAndClient(&rest.Config{Host: server.URL}, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	backend, err := newKubernetesBackend(KubernetesBackendOptions{Gate: ha.AlwaysLeader{}, Namespace: "test", Objects: map[Key]string{Control: "control"}, RequestTimeout: time.Second, MaxDocumentBytes: MaxStateDocumentBytes}, client.ConfigMaps("test"))
	if err != nil {
		t.Fatal(err)
	}
	store, err := NewBackendStore(context.Background(), backend, ha.AlwaysLeader{}, 5)
	if err != nil {
		t.Fatal(err)
	}
	var applied []State
	events := 0
	handler := NewHandler(store, func(s State) { applied = append(applied, s) }, "", fakeIncidentSrc{}).
		SetChangeObserver(func(State) { events++ })
	api := httptest.NewServer(handler)
	defer api.Close()
	resp, err := http.Post(api.URL+"/control/incidents", "application/json", strings.NewReader(`{"blueprint":"mini","mode":"failure","for":"1m"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var out State
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if resp.StatusCode != 200 || len(out.RuntimeIncidents) != 1 || writes != 1 || events != 1 || len(applied) != 1 {
		t.Fatalf("HTTP lost response: status=%d state=%+v writes=%d events=%d applies=%d", resp.StatusCode, out, writes, events, len(applied))
	}
	doc, err := decodeControl(object.BinaryData["document"])
	if err != nil || len(doc.Receipts) != 1 || len(doc.State.RuntimeIncidents) != 1 {
		t.Fatal("committed incident/receipt missing", err)
	}
	want, _ := json.Marshal(doc.State.RuntimeIncidents)
	got, _ := json.Marshal(out.RuntimeIncidents)
	appliedIncidents, _ := json.Marshal(applied[0].RuntimeIncidents)
	if string(want) != string(got) || string(want) != string(appliedIncidents) || out.RuntimeIncidents[0].ID == "" {
		t.Fatal("committed, returned and applied incident differ")
	}
}

func TestBackendReceiptReconciliationAfterInterveningWrite(t *testing.T) {
	for _, mismatch := range []bool{false, true} {
		name := "reset-keeps-later-write"
		if mismatch {
			name = "same-id-different-fingerprint"
		}
		t.Run(name, func(t *testing.T) {
			ctx := context.Background()
			b, m := testNamedBackend(t, ha.AlwaysLeader{})
			store, err := NewBackendStore(ctx, b, ha.AlwaysLeader{}, 5)
			if err != nil {
				t.Fatal(err)
			}
			other, err := NewBackendStore(ctx, b, ha.AlwaysLeader{}, 5)
			if err != nil {
				t.Fatal(err)
			}
			m.afterUpdate = func() {
				if mismatch {
					doc, err := decodeControl(m.object.BinaryData["document"])
					if err != nil {
						t.Fatal(err)
					}
					doc.Receipts[0].Fingerprint = "different-intent"
					data, err := json.Marshal(doc)
					if err != nil {
						t.Fatal(err)
					}
					m.object.BinaryData["document"] = data
					m.object.ResourceVersion += "changed"
				} else {
					if _, err := other.UpdateContext(ctx, func(s *State) { s.Scaling["later"] = 7 }); err != nil {
						t.Fatal(err)
					}
				}
				m.lose = true
			}
			applies, events := 0, 0
			h := NewHandler(store, func(State) { applies++ }, "").SetChangeObserver(func(State) { events++ })
			srv := httptest.NewServer(h)
			defer srv.Close()
			resp, err := http.Post(srv.URL+"/control/reset", "application/json", strings.NewReader(`{}`))
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			if mismatch {
				var body map[string]string
				if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
					t.Fatal(err)
				}
				if resp.StatusCode != 409 || body["code"] != "state_conflict" || m.writes != 1 || applies != 0 || events != 0 {
					t.Fatalf("mismatched receipt accepted/replayed: status=%d writes=%d applies=%d events=%d", resp.StatusCode, m.writes, applies, events)
				}
				return
			}
			var out State
			if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
				t.Fatal(err)
			}
			if resp.StatusCode != 200 || out.Scaling["later"] != 7 || m.writes != 2 || applies != 1 || events != 1 {
				t.Fatalf("reset replay erased later commit: status=%d state=%+v writes=%d applies=%d events=%d", resp.StatusCode, out, m.writes, applies, events)
			}
		})
	}
}

func TestNamedBackendCapsAndCorruption(t *testing.T) {
	b, m := testNamedBackend(t, ha.AlwaysLeader{})
	ctx := context.Background()
	snap, _ := b.Load(ctx, Control)
	if _, err := b.CompareAndSwap(ctx, Control, snap.Revision, make([]byte, MaxStateDocumentBytes+1)); err == nil || m.writes != 0 {
		t.Fatal("oversize write reached API")
	}
	m.object.Data["large"] = strings.Repeat("a", 1<<20)
	if _, err := b.Load(ctx, Control); err == nil {
		t.Fatal("aggregate cap not enforced")
	}
	delete(m.object.Data, "large")
	m.object.BinaryData = map[string][]byte{"document": []byte(`{"schema_version":19}`)}
	if _, err := NewBackendStore(ctx, b, ha.AlwaysLeader{}, 5); err == nil {
		t.Fatal("unsupported control schema loaded")
	}
}

func TestBackendControlLostResponseAndDisjointWriters(t *testing.T) {
	ctx := context.Background()
	b, m := testNamedBackend(t, ha.AlwaysLeader{})
	a, err := NewBackendStore(ctx, b, ha.AlwaysLeader{}, 5)
	if err != nil {
		t.Fatal(err)
	}
	other, err := NewBackendStore(ctx, b, ha.AlwaysLeader{}, 5)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = other.UpdateContext(ctx, func(s *State) { s.Scaling["api"] = 7 }); err != nil {
		t.Fatal(err)
	}
	m.lose = true
	out, err := a.UpdateContext(ctx, func(s *State) {
		s.RuntimeIncidents = append(s.RuntimeIncidents, RuntimeIncident{ID: "stable", Mode: "failure"})
	})
	if err != nil {
		t.Fatal(err)
	}
	if out.Scaling["api"] != 7 || len(out.RuntimeIncidents) != 1 {
		t.Fatalf("lost edit or duplicate incident: %+v", out)
	}
	if m.writes != 2 {
		t.Fatalf("ambiguous commit replayed: writes=%d", m.writes)
	}
}
