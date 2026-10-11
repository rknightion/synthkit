// SPDX-License-Identifier: AGPL-3.0-only
// Package smstoretest provides a local named HTTP API for process-edge proofs.
// Fixtures are plain JSON server facts, not an additional Kubernetes adapter.
// The actual typed clients and their serializers remain in the approved adapters.
package smstoretest

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

type typeMeta struct {
	Kind       string `json:"kind"`
	APIVersion string `json:"apiVersion"`
}
type objectMeta struct {
	Name            string            `json:"name"`
	Namespace       string            `json:"namespace"`
	ResourceVersion string            `json:"resourceVersion"`
	Labels          map[string]string `json:"labels,omitempty"`
}
type configMap struct {
	typeMeta
	objectMeta `json:"metadata"`
	Data       map[string]string `json:"data,omitempty"`
	BinaryData map[string][]byte `json:"binaryData,omitempty"`
}
type leaseSpec struct {
	HolderIdentity       *string `json:"holderIdentity,omitempty"`
	LeaseDurationSeconds *int32  `json:"leaseDurationSeconds,omitempty"`
	AcquireTime          *string `json:"acquireTime,omitempty"`
	RenewTime            *string `json:"renewTime,omitempty"`
	LeaseTransitions     *int32  `json:"leaseTransitions,omitempty"`
}
type namedLease struct {
	typeMeta
	objectMeta `json:"metadata"`
	Spec       leaseSpec `json:"spec"`
}
type API struct {
	Mu                                  sync.Mutex
	Maps                                map[string]*configMap
	Lease                               *namedLease
	Requests                            []string
	StateWrites, LeaseWrites, Releases  int
	Allow, LoseLease, LoseStateResponse bool
	ConflictState                       bool
	LoseStateAtWrite                    int
	Acquired                            chan struct{}
	sawAcquisition                      bool
	Server                              *httptest.Server
}

func New(t *testing.T, names ...string) *API {
	t.Helper()
	holder := "other"
	duration := int32(3600)
	now := time.Now().UTC().Format("2006-01-02T15:04:05.000000Z07:00")
	a := &API{Maps: map[string]*configMap{}, Lease: &namedLease{typeMeta: typeMeta{Kind: "Lease", APIVersion: "coordination.k8s.io/v1"}, objectMeta: objectMeta{Name: "provision", Namespace: "test", ResourceVersion: "1"}, Spec: leaseSpec{HolderIdentity: &holder, LeaseDurationSeconds: &duration, RenewTime: &now}}, Acquired: make(chan struct{})}
	for _, n := range names {
		a.Maps[n] = &configMap{typeMeta: typeMeta{Kind: "ConfigMap", APIVersion: "v1"}, objectMeta: objectMeta{Name: n, Namespace: "test", ResourceVersion: "1", Labels: map[string]string{"preserved": "yes"}}, Data: map[string]string{"unrelated": "preserved"}, BinaryData: map[string][]byte{}}
	}
	a.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		a.Mu.Lock()
		defer a.Mu.Unlock()
		a.Requests = append(a.Requests, r.Method+" "+r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		prefix := "/api/v1/namespaces/test/configmaps/"
		if strings.HasPrefix(r.URL.Path, prefix) {
			name := strings.TrimPrefix(r.URL.Path, prefix)
			obj := a.Maps[name]
			if obj == nil {
				status(w, 404, "NotFound")
				return
			}
			switch r.Method {
			case "GET":
				json.NewEncoder(w).Encode(obj)
			case "PUT":
				// The explicit embedding constructor negotiates JSON, as the Lease seam
				// does. These bytes are emitted by the real core typed client, not a fake
				// StateBackend. Production protobuf response-loss coverage stays unchanged
				// in the owning backend package's existing socket-level fixture.
				if r.Header.Get("Content-Type") != "application/json" {
					t.Errorf("unexpected negotiated media type %q", r.Header.Get("Content-Type"))
					status(w, 415, "UnsupportedMediaType")
					return
				}
				var next configMap
				if err := json.NewDecoder(r.Body).Decode(&next); err != nil {
					t.Error(err)
					status(w, 400, "Invalid")
					return
				}
				if a.ConflictState || next.ResourceVersion != obj.ResourceVersion {
					status(w, 409, "Conflict")
					return
				}
				if next.Name != obj.Name || next.Namespace != obj.Namespace || next.Data["unrelated"] != "preserved" || next.Labels["preserved"] != "yes" {
					t.Error("named metadata/data changed")
					status(w, 400, "Invalid")
					return
				}
				next.ResourceVersion += "x"
				a.Maps[name] = &next
				a.StateWrites++
				if a.LoseStateResponse || a.LoseStateAtWrite == a.StateWrites {
					a.LoseStateResponse = false
					conn, _, err := w.(http.Hijacker).Hijack()
					if err != nil {
						t.Error(err)
						return
					}
					conn.Close()
					return
				}
				json.NewEncoder(w).Encode(&next)
			default:
				t.Errorf("forbidden state method %s", r.Method)
				status(w, 403, "Forbidden")
			}
			return
		}
		if r.URL.Path != "/apis/coordination.k8s.io/v1/namespaces/test/leases/"+a.Lease.Name {
			t.Errorf("unnamed or foreign API request %s", r.URL.Path)
			status(w, 403, "Forbidden")
			return
		}
		if a.LoseLease {
			status(w, 403, "Forbidden")
			return
		}
		switch r.Method {
		case "GET":
			if a.Allow && !a.sawAcquisition {
				empty := ""
				a.Lease.Spec.HolderIdentity = &empty
			}
			json.NewEncoder(w).Encode(a.Lease)
		case "PUT":
			var next namedLease
			if err := json.NewDecoder(r.Body).Decode(&next); err != nil {
				t.Error(err)
				status(w, 400, "Invalid")
				return
			}
			if next.ResourceVersion != a.Lease.ResourceVersion {
				status(w, 409, "Conflict")
				return
			}
			next.ResourceVersion += "x"
			a.Lease = &next
			a.LeaseWrites++
			if next.Spec.HolderIdentity != nil && *next.Spec.HolderIdentity == "" {
				a.Releases++
			} else if !a.sawAcquisition {
				a.sawAcquisition = true
				close(a.Acquired)
			}
			json.NewEncoder(w).Encode(&next)
		default:
			t.Errorf("forbidden Lease method %s", r.Method)
			status(w, 403, "Forbidden")
		}
	}))
	return a
}
func status(w http.ResponseWriter, code int, reason string) {
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]any{"kind": "Status", "apiVersion": "v1", "status": "Failure", "reason": reason, "code": code})
}
func (a *API) Set(name string, data []byte) {
	a.Mu.Lock()
	defer a.Mu.Unlock()
	obj := a.Maps[name]
	obj.BinaryData["document"] = append([]byte(nil), data...)
	obj.ResourceVersion += "x"
}
func (a *API) Data(name string) []byte {
	a.Mu.Lock()
	defer a.Mu.Unlock()
	return append([]byte(nil), a.Maps[name].BinaryData["document"]...)
}
func (a *API) ReplaceLease(name string) {
	a.Mu.Lock()
	defer a.Mu.Unlock()
	a.Lease.Name = name
	a.Allow = false
	a.sawAcquisition = false
	a.Acquired = make(chan struct{})
	holder := "other"
	duration := int32(3600)
	now := time.Now().UTC().Format("2006-01-02T15:04:05.000000Z07:00")
	a.Lease.Spec.HolderIdentity = &holder
	a.Lease.Spec.LeaseDurationSeconds = &duration
	a.Lease.Spec.RenewTime = &now
}
func (a *API) Permit() { a.Mu.Lock(); a.Allow = true; a.Mu.Unlock() }
func (a *API) Close()  { a.Server.Close() }
