// SPDX-License-Identifier: AGPL-3.0-only
package kubernetes

import (
	"context"
	"encoding/json"
	"fmt"
	coordinationv1 "k8s.io/api/coordination/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	coordination "k8s.io/client-go/kubernetes/typed/coordination/v1"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/leaderelection/resourcelock"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

type leaseAPI struct {
	mu                         sync.Mutex
	lease                      coordinationv1.Lease
	missing, conflict, replace bool
	methods                    []string
}

func newLeaseAPI(t *testing.T) (*leaseAPI, *httptest.Server, coordination.CoordinationV1Interface) {
	t.Helper()
	a := &leaseAPI{lease: coordinationv1.Lease{TypeMeta: metav1.TypeMeta{APIVersion: "coordination.k8s.io/v1", Kind: "Lease"}, ObjectMeta: metav1.ObjectMeta{Name: "lease", Namespace: "test", ResourceVersion: "1"}}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		a.mu.Lock()
		defer a.mu.Unlock()
		a.methods = append(a.methods, r.Method)
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path != "/apis/coordination.k8s.io/v1/namespaces/test/leases/lease" {
			t.Errorf("unnamed API access %s", r.URL.Path)
			w.WriteHeader(403)
			return
		}
		if a.missing {
			w.WriteHeader(404)
			_ = json.NewEncoder(w).Encode(metav1.Status{Status: "Failure", Reason: metav1.StatusReasonNotFound, Code: 404})
			return
		}
		switch r.Method {
		case "GET":
			_ = json.NewEncoder(w).Encode(a.lease)
		case "PUT":
			var l coordinationv1.Lease
			if err := json.NewDecoder(r.Body).Decode(&l); err != nil {
				t.Error(err)
				return
			}
			if a.conflict {
				a.conflict = false
				if a.replace {
					holder := "another-process"
					a.lease.Spec.HolderIdentity = &holder
				}
				a.lease.ResourceVersion = "2"
				w.WriteHeader(409)
				_ = json.NewEncoder(w).Encode(metav1.Status{Status: "Failure", Reason: metav1.StatusReasonConflict, Code: 409})
				return
			}
			if l.ResourceVersion != a.lease.ResourceVersion {
				t.Error("missing CAS precondition")
			}
			l.ResourceVersion = fmt.Sprint(time.Now().UnixNano())
			a.lease = l
			_ = json.NewEncoder(w).Encode(l)
		default:
			t.Errorf("forbidden API method %s", r.Method)
			w.WriteHeader(403)
		}
	}))
	client, err := coordination.NewForConfig(&rest.Config{Host: srv.URL, Timeout: 100 * time.Millisecond, ContentConfig: rest.ContentConfig{ContentType: "application/json", AcceptContentTypes: "application/json"}})
	if err != nil {
		t.Fatal(err)
	}
	return a, srv, client
}
func testOptions() Options {
	return Options{Namespace: "test", Name: "lease", PodUID: "pod", LeaseDuration: 4 * time.Second, RenewDeadline: 2 * time.Second, RetryPeriod: 100 * time.Millisecond, RequestTimeout: 100 * time.Millisecond, Started: func(context.Context) {}, Stopped: func() {}}
}
func TestNamedOnlyAndNoCreateEvenElectorNotFound(t *testing.T) {
	a, srv, client := newLeaseAPI(t)
	defer srv.Close()
	e, err := newWithClient(context.Background(), testOptions(), client)
	if err != nil {
		t.Fatal(err)
	}
	a.mu.Lock()
	a.missing = true
	a.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
	defer cancel()
	e.Run(ctx)
	a.mu.Lock()
	methods := append([]string{}, a.methods...)
	a.mu.Unlock()
	for _, method := range methods {
		if method != "GET" {
			t.Fatalf("missing resource manufactured through %s", method)
		}
	}
	if _, err := newWithClient(context.Background(), testOptions(), client); err == nil {
		t.Fatal("missing resource accepted")
	}
}
func TestSealReleaseConflictsAndDifferentHolder(t *testing.T) {
	for _, replace := range []bool{false, true} {
		t.Run(fmt.Sprint(replace), func(t *testing.T) {
			a, srv, client := newLeaseAPI(t)
			defer srv.Close()
			e, err := newWithClient(context.Background(), testOptions(), client)
			if err != nil {
				t.Fatal(err)
			}
			a.mu.Lock()
			id := e.Identity()
			a.lease.Spec.HolderIdentity = &id
			a.conflict = true
			a.replace = replace
			a.mu.Unlock()
			if err := e.Seal(context.Background()); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			err = e.Release(ctx)
			if (err != nil) != replace {
				t.Fatalf("release result %v", err)
			}
			a.mu.Lock()
			before := len(a.methods)
			holder := *a.lease.Spec.HolderIdentity
			a.mu.Unlock()
			if replace && holder != "another-process" || !replace && holder != "" {
				t.Fatalf("holder=%q", holder)
			}
			if err := e.lock.Update(context.Background(), resourcelock.LeaderElectionRecord{}); err != ErrSealed {
				t.Fatal("late renewal not sealed", err)
			}
			a.mu.Lock()
			defer a.mu.Unlock()
			if len(a.methods) != before {
				t.Fatal("late renewal overwrote released lease")
			}
		})
	}
}
func TestMalformedNamedLeaseMetadataFailsBeforeWrites(t *testing.T) {
	for _, field := range []string{"name", "namespace", "revision"} {
		t.Run(field, func(t *testing.T) {
			api, srv, client := newLeaseAPI(t)
			defer srv.Close()
			api.mu.Lock()
			switch field {
			case "name":
				api.lease.Name = "different"
			case "namespace":
				api.lease.Namespace = "different"
			case "revision":
				api.lease.ResourceVersion = ""
			}
			api.mu.Unlock()
			if _, err := newWithClient(context.Background(), testOptions(), client); err == nil {
				t.Fatal("malformed named lease accepted")
			}
			api.mu.Lock()
			defer api.mu.Unlock()
			for _, method := range api.methods {
				if method != "GET" {
					t.Fatal("malformed resource caused write", method)
				}
			}
		})
	}
}

func TestProcessNonceDiffersWithinSamePod(t *testing.T) {
	_, srv, client := newLeaseAPI(t)
	defer srv.Close()
	a, err := newWithClient(context.Background(), testOptions(), client)
	if err != nil {
		t.Fatal(err)
	}
	b, err := newWithClient(context.Background(), testOptions(), client)
	if err != nil {
		t.Fatal(err)
	}
	if a.Identity() == b.Identity() {
		t.Fatal("pod identity reused after restart")
	}
}
