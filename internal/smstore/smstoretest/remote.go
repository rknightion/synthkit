// SPDX-License-Identifier: AGPL-3.0-only
package smstoretest

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
)

func Token(t *testing.T) string {
	t.Helper()
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(b)
}

// Remote observes real HTTP requests and supplies authoritative ID/Modified fields.
// It never derives or accepts emitter activation; the CLI must persist the evidence.
type Remote struct {
	Mu             sync.Mutex
	Server         *httptest.Server
	Probes, Checks []map[string]any
	Posts          int
}

func NewRemote(t *testing.T) *Remote {
	t.Helper()
	f := &Remote{Probes: []map[string]any{}, Checks: []map[string]any{}}
	f.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.Mu.Lock()
		defer f.Mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		if r.Method == "GET" {
			switch r.URL.Path {
			case "/api/v1/probe/list":
				json.NewEncoder(w).Encode(f.Probes)
			case "/api/v1/check/list":
				json.NewEncoder(w).Encode(f.Checks)
			default:
				t.Errorf("unknown SM inventory %s", r.URL.Path)
				w.WriteHeader(404)
			}
			return
		}
		if r.Method != "POST" {
			t.Errorf("unexpected SM method %s", r.Method)
			w.WriteHeader(400)
			return
		}
		f.Posts++
		var v map[string]any
		if err := json.NewDecoder(r.Body).Decode(&v); err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		switch r.URL.Path {
		case "/api/v1/probe/add":
			v["id"] = 11
			v["modified"] = 10
			f.Probes = append(f.Probes, v)
			json.NewEncoder(w).Encode(map[string]any{"probe": v, "token": []byte(Token(t))})
		case "/api/v1/check/add":
			v["id"] = 12
			v["modified"] = 20
			f.Checks = append(f.Checks, v)
			json.NewEncoder(w).Encode(v)
		case "/api/v1/probe/update":
			v["modified"] = 30
			f.Probes = []map[string]any{v}
			json.NewEncoder(w).Encode(v)
		case "/api/v1/check/update":
			v["modified"] = 40
			f.Checks = []map[string]any{v}
			json.NewEncoder(w).Encode(v)
		default:
			t.Errorf("unknown SM mutation %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	return f
}
func (f *Remote) Close() { f.Server.Close() }
