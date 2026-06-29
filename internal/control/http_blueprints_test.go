// SPDX-License-Identifier: AGPL-3.0-only

package control

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// fakeBlueprintAdmin implements BlueprintAdmin for testing.
// Mutation methods record calls and return configurable errors.
type fakeBlueprintAdmin struct {
	staged    []StagedBlueprint
	sources   []SourceView
	pending   PendingChanges
	valResult ValidationResult

	// configurable errors returned by mutation methods
	stageUploadErr  error
	removeUploadErr error
	upsertSourceErr error
	removeSourceErr error
	fetchNowErr     error

	// recorded call arguments
	stageUploadCalled  bool
	stageUploadNS      string
	stageUploadName    string
	stageUploadYAML    []byte
	removeUploadCalled bool
	removeUploadName   string
	upsertSourceCalled bool
	upsertSourceArg    SourceView
	removeSourceCalled bool
	removeSourceID     string
	fetchNowCalled     bool
	fetchNowID         string
}

func (f *fakeBlueprintAdmin) StageUpload(ns, name string, yaml []byte) error {
	f.stageUploadCalled = true
	f.stageUploadNS = ns
	f.stageUploadName = name
	f.stageUploadYAML = yaml
	return f.stageUploadErr
}
func (f *fakeBlueprintAdmin) RemoveUpload(nsName string) error {
	f.removeUploadCalled = true
	f.removeUploadName = nsName
	return f.removeUploadErr
}
func (f *fakeBlueprintAdmin) ListStaged() []StagedBlueprint         { return f.staged }
func (f *fakeBlueprintAdmin) Validate(yaml []byte) ValidationResult { return f.valResult }
func (f *fakeBlueprintAdmin) Pending() PendingChanges               { return f.pending }
func (f *fakeBlueprintAdmin) Sources() []SourceView                 { return f.sources }
func (f *fakeBlueprintAdmin) UpsertSource(sv SourceView) error {
	f.upsertSourceCalled = true
	f.upsertSourceArg = sv
	return f.upsertSourceErr
}
func (f *fakeBlueprintAdmin) RemoveSource(id string) error {
	f.removeSourceCalled = true
	f.removeSourceID = id
	return f.removeSourceErr
}
func (f *fakeBlueprintAdmin) FetchNow(id string) error {
	f.fetchNowCalled = true
	f.fetchNowID = id
	return f.fetchNowErr
}

// newFakeAdmin returns a populated fakeBlueprintAdmin for reuse across tests.
func newFakeAdmin() *fakeBlueprintAdmin {
	return &fakeBlueprintAdmin{
		staged: []StagedBlueprint{
			{Name: "bp-one", Provenance: "upload", SourceID: ""},
		},
		sources: []SourceView{
			{ID: "src1", Name: "My Repo", Namespace: "custom", URL: "https://example.com/repo.git", Ref: "refs/heads/main"},
		},
		pending: PendingChanges{
			Added:   []string{"custom/bp-one"},
			Removed: []string{},
			Changed: []string{},
			Restart: true,
		},
		valResult: ValidationResult{
			OK:          true,
			Name:        "test-blueprint",
			Cardinality: 42,
			Estimated:   false,
			Diagnostics: []string{},
		},
	}
}

// TestBlueprintStagedRoute covers GET /control/blueprints/staged.
func TestBlueprintStagedRoute(t *testing.T) {
	store := NewStore(t.TempDir() + "/state.json")
	admin := newFakeAdmin()
	h := NewHandler(store, nil, "").SetBlueprintAdmin(admin)

	req := httptest.NewRequest("GET", "/control/blueprints/staged", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("GET /control/blueprints/staged: want 200, got %d: %s", rec.Code, rec.Body)
	}
	var got []StagedBlueprint
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got) != 1 || got[0].Name != "bp-one" {
		t.Fatalf("unexpected staged list: %+v", got)
	}
}

// TestBlueprintSourcesRoute covers GET /control/blueprints/sources.
func TestBlueprintSourcesRoute(t *testing.T) {
	store := NewStore(t.TempDir() + "/state.json")
	admin := newFakeAdmin()
	h := NewHandler(store, nil, "").SetBlueprintAdmin(admin)

	req := httptest.NewRequest("GET", "/control/blueprints/sources", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("GET /control/blueprints/sources: want 200, got %d: %s", rec.Code, rec.Body)
	}
	var got []SourceView
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(got) != 1 || got[0].ID != "src1" {
		t.Fatalf("unexpected sources: %+v", got)
	}
}

// TestBlueprintPendingRoute covers GET /control/blueprints/pending.
func TestBlueprintPendingRoute(t *testing.T) {
	store := NewStore(t.TempDir() + "/state.json")
	admin := newFakeAdmin()
	h := NewHandler(store, nil, "").SetBlueprintAdmin(admin)

	req := httptest.NewRequest("GET", "/control/blueprints/pending", nil)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("GET /control/blueprints/pending: want 200, got %d: %s", rec.Code, rec.Body)
	}
	var got PendingChanges
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !got.Restart || len(got.Added) != 1 || got.Added[0] != "custom/bp-one" {
		t.Fatalf("unexpected pending: %+v", got)
	}
}

// TestBlueprintValidateRoute covers POST /control/blueprints/validate (guarded).
func TestBlueprintValidateRoute(t *testing.T) {
	store := NewStore(t.TempDir() + "/state.json")
	admin := newFakeAdmin()
	token := "secret-tok"
	h := NewHandler(store, nil, token).SetBlueprintAdmin(admin)
	srv := httptest.NewServer(h)
	defer srv.Close()

	yamlBody := `{"yaml": "name: test-blueprint\nworkloads: []"}`
	req, _ := http.NewRequest("POST", srv.URL+"/control/blueprints/validate", strings.NewReader(yamlBody))
	req.Header.Set("Content-Type", "application/json")
	req.SetBasicAuth("control", token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("POST /control/blueprints/validate: want 200, got %d", resp.StatusCode)
	}
	var got ValidationResult
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if !got.OK || got.Name != "test-blueprint" || got.Cardinality != 42 {
		t.Fatalf("unexpected validation result: %+v", got)
	}
}

// TestBlueprintValidateRequiresToken covers 401 when CONTROL_TOKEN is missing.
func TestBlueprintValidateRequiresToken(t *testing.T) {
	store := NewStore(t.TempDir() + "/state.json")
	admin := newFakeAdmin()
	token := "secret-tok"
	h := NewHandler(store, nil, token).SetBlueprintAdmin(admin)
	srv := httptest.NewServer(h)
	defer srv.Close()

	req, _ := http.NewRequest("POST", srv.URL+"/control/blueprints/validate", strings.NewReader(`{"yaml":"name: x"}`))
	req.Header.Set("Content-Type", "application/json")
	// No auth header.

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("want 401 without token, got %d", resp.StatusCode)
	}
}

// TestBlueprintAdminNil404 covers the nil bpadmin → 404 path for all GET routes.
func TestBlueprintAdminNil404(t *testing.T) {
	store := NewStore(t.TempDir() + "/state.json")
	// No SetBlueprintAdmin — h.bpadmin is nil.
	h := NewHandler(store, nil, "")

	routes := []string{
		"/control/blueprints/staged",
		"/control/blueprints/sources",
		"/control/blueprints/pending",
	}
	for _, route := range routes {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest("GET", route, nil))
		if rec.Code != http.StatusNotFound {
			t.Errorf("GET %s with nil bpadmin: want 404, got %d", route, rec.Code)
		}
	}
}

// ── POST /control/blueprints/custom ─────────────────────────────────────────

// TestStageUploadHappy covers POST /control/blueprints/custom (happy path).
func TestStageUploadHappy(t *testing.T) {
	store := NewStore(t.TempDir() + "/state.json")
	admin := newFakeAdmin()
	token := "tok"
	h := NewHandler(store, nil, token).SetBlueprintAdmin(admin)
	srv := httptest.NewServer(h)
	defer srv.Close()

	body := `{"namespace":"custom","name":"my-bp","yaml":"name: my-bp\nworkloads: []"}`
	req, _ := http.NewRequest("POST", srv.URL+"/control/blueprints/custom", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.SetBasicAuth("control", token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200, got %d", resp.StatusCode)
	}
	var got map[string]string
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if got["status"] != "staged" {
		t.Fatalf("want status=staged, got %+v", got)
	}
	if !admin.stageUploadCalled {
		t.Fatal("StageUpload not called")
	}
	if admin.stageUploadNS != "custom" || admin.stageUploadName != "my-bp" {
		t.Fatalf("StageUpload args: ns=%q name=%q", admin.stageUploadNS, admin.stageUploadName)
	}
	if string(admin.stageUploadYAML) != "name: my-bp\nworkloads: []" {
		t.Fatalf("StageUpload yaml: %q", string(admin.stageUploadYAML))
	}
}

// TestStageUploadRejected covers POST /control/blueprints/custom when StageUpload returns an error.
func TestStageUploadRejected(t *testing.T) {
	store := NewStore(t.TempDir() + "/state.json")
	admin := newFakeAdmin()
	admin.stageUploadErr = errors.New("invalid blueprint: missing name")
	token := "tok"
	h := NewHandler(store, nil, token).SetBlueprintAdmin(admin)
	srv := httptest.NewServer(h)
	defer srv.Close()

	body := `{"namespace":"custom","name":"bad-bp","yaml":"invalid yaml"}`
	req, _ := http.NewRequest("POST", srv.URL+"/control/blueprints/custom", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.SetBasicAuth("control", token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", resp.StatusCode)
	}
}

// TestStageUploadRequiresToken covers 401 when CONTROL_TOKEN missing.
func TestStageUploadRequiresToken(t *testing.T) {
	store := NewStore(t.TempDir() + "/state.json")
	admin := newFakeAdmin()
	h := NewHandler(store, nil, "tok").SetBlueprintAdmin(admin)
	srv := httptest.NewServer(h)
	defer srv.Close()

	req, _ := http.NewRequest("POST", srv.URL+"/control/blueprints/custom", strings.NewReader(`{"namespace":"x","name":"y","yaml":"z"}`))
	req.Header.Set("Content-Type", "application/json")
	// No auth.

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("want 401 without token, got %d", resp.StatusCode)
	}
}

// ── DELETE /control/blueprints/custom ───────────────────────────────────────

// TestDeleteUploadHappy covers DELETE /control/blueprints/custom?name=<ns/name>.
func TestDeleteUploadHappy(t *testing.T) {
	store := NewStore(t.TempDir() + "/state.json")
	admin := newFakeAdmin()
	token := "tok"
	h := NewHandler(store, nil, token).SetBlueprintAdmin(admin)
	srv := httptest.NewServer(h)
	defer srv.Close()

	req, _ := http.NewRequest("DELETE", srv.URL+"/control/blueprints/custom?name=custom/my-bp", nil)
	req.SetBasicAuth("control", token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("DELETE: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200, got %d", resp.StatusCode)
	}
	if !admin.removeUploadCalled {
		t.Fatal("RemoveUpload not called")
	}
	if admin.removeUploadName != "custom/my-bp" {
		t.Fatalf("RemoveUpload name: got %q, want %q", admin.removeUploadName, "custom/my-bp")
	}
}

// TestDeleteUploadRequiresToken covers 401 when CONTROL_TOKEN missing.
func TestDeleteUploadRequiresToken(t *testing.T) {
	store := NewStore(t.TempDir() + "/state.json")
	admin := newFakeAdmin()
	h := NewHandler(store, nil, "tok").SetBlueprintAdmin(admin)
	srv := httptest.NewServer(h)
	defer srv.Close()

	req, _ := http.NewRequest("DELETE", srv.URL+"/control/blueprints/custom?name=custom/my-bp", nil)
	// No auth.

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("DELETE: %v", err)
	}
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("want 401 without token, got %d", resp.StatusCode)
	}
}

// ── POST /control/blueprints/sources ────────────────────────────────────────

// TestUpsertSourceHappy covers POST /control/blueprints/sources.
func TestUpsertSourceHappy(t *testing.T) {
	store := NewStore(t.TempDir() + "/state.json")
	admin := newFakeAdmin()
	token := "tok"
	h := NewHandler(store, nil, token).SetBlueprintAdmin(admin)
	srv := httptest.NewServer(h)
	defer srv.Close()

	body := `{"id":"src2","name":"Other Repo","namespace":"ext","url":"https://example.com/other.git","ref":"refs/heads/main","subpath":"","token_env_var":"","last_sha":"","last_fetch_ms":0,"last_err":""}`
	req, _ := http.NewRequest("POST", srv.URL+"/control/blueprints/sources", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.SetBasicAuth("control", token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200, got %d", resp.StatusCode)
	}
	if !admin.upsertSourceCalled {
		t.Fatal("UpsertSource not called")
	}
	if admin.upsertSourceArg.ID != "src2" || admin.upsertSourceArg.URL != "https://example.com/other.git" {
		t.Fatalf("UpsertSource arg: %+v", admin.upsertSourceArg)
	}
	// Verify the response body does NOT contain a raw token field.
	var respBody map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&respBody); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if _, hasToken := respBody["token"]; hasToken {
		t.Fatal("response must not echo a raw token field")
	}
}

// TestUpsertSourceRequiresToken covers 401 when CONTROL_TOKEN missing.
func TestUpsertSourceRequiresToken(t *testing.T) {
	store := NewStore(t.TempDir() + "/state.json")
	admin := newFakeAdmin()
	h := NewHandler(store, nil, "tok").SetBlueprintAdmin(admin)
	srv := httptest.NewServer(h)
	defer srv.Close()

	body := `{"id":"src2","name":"Other Repo","namespace":"ext","url":"https://example.com/other.git","ref":"refs/heads/main","subpath":"","token_env_var":"","last_sha":"","last_fetch_ms":0,"last_err":""}`
	req, _ := http.NewRequest("POST", srv.URL+"/control/blueprints/sources", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	// No auth.

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("want 401 without token, got %d", resp.StatusCode)
	}
}

// ── DELETE /control/blueprints/sources ──────────────────────────────────────

// TestDeleteSourceHappy covers DELETE /control/blueprints/sources?id=<id>.
func TestDeleteSourceHappy(t *testing.T) {
	store := NewStore(t.TempDir() + "/state.json")
	admin := newFakeAdmin()
	token := "tok"
	h := NewHandler(store, nil, token).SetBlueprintAdmin(admin)
	srv := httptest.NewServer(h)
	defer srv.Close()

	req, _ := http.NewRequest("DELETE", srv.URL+"/control/blueprints/sources?id=src1", nil)
	req.SetBasicAuth("control", token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("DELETE: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200, got %d", resp.StatusCode)
	}
	if !admin.removeSourceCalled {
		t.Fatal("RemoveSource not called")
	}
	if admin.removeSourceID != "src1" {
		t.Fatalf("RemoveSource id: got %q, want %q", admin.removeSourceID, "src1")
	}
}

// TestDeleteSourceRequiresToken covers 401 when CONTROL_TOKEN missing.
func TestDeleteSourceRequiresToken(t *testing.T) {
	store := NewStore(t.TempDir() + "/state.json")
	admin := newFakeAdmin()
	h := NewHandler(store, nil, "tok").SetBlueprintAdmin(admin)
	srv := httptest.NewServer(h)
	defer srv.Close()

	req, _ := http.NewRequest("DELETE", srv.URL+"/control/blueprints/sources?id=src1", nil)
	// No auth.

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("DELETE: %v", err)
	}
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("want 401 without token, got %d", resp.StatusCode)
	}
}

// ── POST /control/blueprints/sources/fetch ───────────────────────────────────

// TestFetchNowHappy covers POST /control/blueprints/sources/fetch?id=<id> (happy path).
func TestFetchNowHappy(t *testing.T) {
	store := NewStore(t.TempDir() + "/state.json")
	admin := newFakeAdmin()
	token := "tok"
	h := NewHandler(store, nil, token).SetBlueprintAdmin(admin)
	srv := httptest.NewServer(h)
	defer srv.Close()

	req, _ := http.NewRequest("POST", srv.URL+"/control/blueprints/sources/fetch?id=src1", nil)
	req.SetBasicAuth("control", token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200, got %d", resp.StatusCode)
	}
	if !admin.fetchNowCalled {
		t.Fatal("FetchNow not called")
	}
	if admin.fetchNowID != "src1" {
		t.Fatalf("FetchNow id: got %q, want %q", admin.fetchNowID, "src1")
	}
}

// TestFetchNowError covers POST /control/blueprints/sources/fetch when FetchNow returns an error.
func TestFetchNowError(t *testing.T) {
	store := NewStore(t.TempDir() + "/state.json")
	admin := newFakeAdmin()
	admin.fetchNowErr = errors.New("remote: authentication required")
	token := "tok"
	h := NewHandler(store, nil, token).SetBlueprintAdmin(admin)
	srv := httptest.NewServer(h)
	defer srv.Close()

	req, _ := http.NewRequest("POST", srv.URL+"/control/blueprints/sources/fetch?id=src1", nil)
	req.SetBasicAuth("control", token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("want 502, got %d", resp.StatusCode)
	}
}

// TestFetchNowRequiresToken covers 401 when CONTROL_TOKEN missing.
func TestFetchNowRequiresToken(t *testing.T) {
	store := NewStore(t.TempDir() + "/state.json")
	admin := newFakeAdmin()
	h := NewHandler(store, nil, "tok").SetBlueprintAdmin(admin)
	srv := httptest.NewServer(h)
	defer srv.Close()

	req, _ := http.NewRequest("POST", srv.URL+"/control/blueprints/sources/fetch?id=src1", nil)
	// No auth.

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("want 401 without token, got %d", resp.StatusCode)
	}
}

// ── nil bpadmin → 404 for mutation routes ────────────────────────────────────

// TestBlueprintMutationAdminNil404 covers the nil bpadmin → 404 path for all mutation routes.
func TestBlueprintMutationAdminNil404(t *testing.T) {
	store := NewStore(t.TempDir() + "/state.json")
	// No SetBlueprintAdmin — h.bpadmin is nil; no token so guard is a no-op.
	h := NewHandler(store, nil, "")

	cases := []struct {
		method string
		path   string
		body   string
	}{
		{"POST", "/control/blueprints/custom", `{"namespace":"x","name":"y","yaml":"z"}`},
		{"DELETE", "/control/blueprints/custom?name=x/y", ""},
		{"POST", "/control/blueprints/sources", `{"id":"x","name":"y","namespace":"z","url":"https://example.com","ref":"refs/heads/main","subpath":"","token_env_var":"","last_sha":"","last_fetch_ms":0,"last_err":""}`},
		{"DELETE", "/control/blueprints/sources?id=x", ""},
		{"POST", "/control/blueprints/sources/fetch?id=x", ""},
	}
	for _, c := range cases {
		var bodyReader *strings.Reader
		if c.body != "" {
			bodyReader = strings.NewReader(c.body)
		} else {
			bodyReader = strings.NewReader("")
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(c.method, c.path, bodyReader))
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s %s with nil bpadmin: want 404, got %d", c.method, c.path, rec.Code)
		}
	}
}
