// SPDX-License-Identifier: AGPL-3.0-only
package charttest

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/config"
	"github.com/rknightion/synthkit/internal/smstate"
	"github.com/rknightion/synthkit/internal/smstore"
	"github.com/rknightion/synthkit/internal/smstore/smstoretest"
)

func smFixture(t *testing.T) string {
	t.Helper()
	return filepath.Join(chartPath(t), "tests", "sm-kubernetes-values.yaml")
}

func smJobPod(t *testing.T, objects []map[string]any) map[string]any {
	t.Helper()
	return mapping(mapping(mapping(object(t, objects, "Job")["spec"])["template"])["spec"])
}

func TestSMNamedRBAC(t *testing.T) {
	objects := renderHA(t, "-f", smFixture(t))
	role := object(t, objects, "Role")
	equal(t, "exact SM rules", role["rules"], []any{
		map[string]any{"apiGroups": []any{""}, "resources": []any{"configmaps"}, "resourceNames": []any{"sm"}, "verbs": []any{"get", "update"}},
		map[string]any{"apiGroups": []any{"coordination.k8s.io"}, "resources": []any{"leases"}, "resourceNames": []any{"provision"}, "verbs": []any{"get", "update"}},
	})
	pod := smJobPod(t, objects)
	principal := pod["serviceAccountName"]
	equal(t, "dedicated principal", principal, "synthkit-test-sm-provision")
	equal(t, "projected API credential", pod["automountServiceAccountToken"], true)
	equal(t, "no state volumes", pod["volumes"], nil)
	equal(t, "no co-location", pod["affinity"], nil)
	equal(t, "bounded Job", mapping(object(t, objects, "Job")["spec"])["activeDeadlineSeconds"], 660)
	equal(t, "no retry", mapping(object(t, objects, "Job")["spec"])["backoffLimit"], 0)
	binding := object(t, objects, "RoleBinding")
	equal(t, "only dedicated subject", binding["subjects"], []any{map[string]any{"kind": "ServiceAccount", "name": principal, "namespace": "default"}})
	equal(t, "only dedicated role", binding["roleRef"], map[string]any{"apiGroup": "rbac.authorization.k8s.io", "kind": "Role", "name": principal})
	container := mapping(list(pod["containers"])[0])
	equal(t, "no envFrom", container["envFrom"], nil)
	equal(t, "no mounts", container["volumeMounts"], nil)
	equal(t, "CLI entrypoint", container["command"], []any{"/app/sm-provision"})
	deploymentPod := mapping(mapping(mapping(object(t, objects, "Deployment")["spec"])["template"])["spec"])
	equal(t, "same version-matched image", container["image"], mapping(list(deploymentPod["containers"])[0])["image"])
	for _, obj := range objects {
		if obj["kind"] == "ServiceAccount" && mapping(obj["metadata"])["name"] == principal {
			equal(t, "principal default token off", obj["automountServiceAccountToken"], false)
		}
		if obj["kind"] == "Lease" || (obj["kind"] == "ConfigMap" && mapping(obj["metadata"])["name"] != "synthkit-test") {
			t.Error("chart renders operator-owned state")
		}
	}
	// The narrow SM principal survives a maximum-length release name without colliding
	// with the emitter's account after truncation.
	long := strings.Repeat("a", 63)
	longObjects := renderHA(t, "-f", smFixture(t), "--set", "fullnameOverride="+long)
	longPrincipal := smJobPod(t, longObjects)["serviceAccountName"].(string)
	if longPrincipal == long || len(longPrincipal) > 63 || !strings.HasSuffix(longPrincipal, "-sm-provision") {
		t.Fatalf("unsafe dedicated name %q", longPrincipal)
	}
}

func TestSMInvalidNamedChart(t *testing.T) {
	cases := []string{
		"smProvision.stateConfigMap=", "smProvision.leaseName=", "smProvision.emitterLeaseName=",
		"smProvision.leaseName=emitter", "ha.leaseName=provision", "smProvision.stateConfigMap=synthkit-test",
		"ha.controlConfigMap=sm", "ha.bootConfigMap=sm", "ha.gitSourceConfigMaps.example=sm",
		"smProvision.stateConfigMap=invalid!", "smProvision.stateConfigMap=" + strings.Repeat("a", 64),
		"smProvision.stateBackend=other", "smProvision.createResources=true", "serviceAccount.name=synthkit-test-sm-provision",
		"credentials.sm.keys.GC_SM_TOKEN=null", "credentials.sm.keys.GC_SM_URL=null",
		"extraEnv[0].name=SM_PROVISION_LEASE_NAME,extraEnv[0].value=emitter",
		"extraEnv[0].name=STATE_SM_CONFIGMAP,extraEnv[0].value=other",
	}
	for _, arg := range cases {
		t.Run(arg, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			out, err := exec.CommandContext(ctx, "helm", "template", "synthkit-test", chartPath(t), "-f", smFixture(t), "--set", arg).CombinedOutput()
			if err == nil || (!bytes.Contains(out, []byte("execution error")) && !bytes.Contains(out, []byte("specifications of the schema"))) {
				t.Fatalf("expected values validation rejection: %v\n%s", err, out)
			}
		})
	}
}

func TestSMFileJobUnchanged(t *testing.T) {
	objects := renderHA(t, "-f", filepath.Join(chartPath(t), "ci", "08-sm-provision-values.yaml"))
	pod := smJobPod(t, objects)
	equal(t, "file token remains off", pod["automountServiceAccountToken"], false)
	equal(t, "file shared principal", pod["serviceAccountName"], "synthkit-test")
	if pod["affinity"] == nil || len(list(pod["volumes"])) != 2 {
		t.Fatal("file shared claim/co-location changed")
	}
	for _, obj := range objects {
		if obj["kind"] == "Role" || obj["kind"] == "RoleBinding" {
			t.Error("file mode acquired Kubernetes API authority")
		}
	}
}

// Resolve exactly the rendered Job environment at the real CLI parser. Only local
// synthetic Secret values and downward fields are injected, never ambient credentials.
func renderedSMConfig(t *testing.T, remoteURL, token string, apply bool) config.SMProvisionConfig {
	t.Helper()
	objects := renderHA(t, "-f", smFixture(t), "--set", fmt.Sprintf("smProvision.apply=%t", apply))
	container := mapping(list(smJobPod(t, objects)["containers"])[0])
	want := map[string]string{
		"STATE_BACKEND": "kubernetes", "STATE_SM_CONFIGMAP": "sm", "SM_PROVISION_LEASE_NAME": "provision", "HA_LEASE_NAME": "emitter",
		"HA_NAMESPACE": "test", "POD_UID": "job-pod", "HA_LEASE_DURATION": "30s", "HA_RENEW_DEADLINE": "15s", "HA_RETRY_PERIOD": "2s",
		"HA_KUBE_REQUEST_TIMEOUT": "2s", "HA_HTTP_TIMEOUT": "5s", "HA_FLUSH_TIMEOUT": "8s", "HA_FENCE_MARGIN": "2s", "HA_RELEASE_TIMEOUT": "2s",
		"STATE_CAS_MAX_ATTEMPTS": "5", "SM_PROVISION_APPLY": fmt.Sprint(apply), "SM_PROVISION_ADOPT_LEGACY": "false", "SM_PROVISION_MIGRATE_TARGET": "false",
		"GC_SM_URL": remoteURL, "GC_SM_TOKEN": token,
	}
	got := map[string]string{}
	for _, entry := range list(container["env"]) {
		e := mapping(entry)
		name := e["name"].(string)
		if value, ok := e["value"].(string); ok {
			got[name] = value
			continue
		}
		from := mapping(e["valueFrom"])
		if from["secretKeyRef"] != nil {
			ref := mapping(from["secretKeyRef"])
			equal(t, "only SM Secret", ref["name"], "synthkit-sm")
			equal(t, "exact Secret key", ref["key"], name)
			if name != "GC_SM_URL" && name != "GC_SM_TOKEN" {
				t.Fatalf("unrelated credential %s", name)
			}
			got[name] = want[name]
		} else {
			field := mapping(from["fieldRef"])["fieldPath"]
			switch name {
			case "HA_NAMESPACE":
				equal(t, "namespace field", field, "metadata.namespace")
			case "POD_UID":
				equal(t, "identity field", field, "metadata.uid")
			default:
				t.Fatalf("unexpected downward field %s", name)
			}
			got[name] = want[name]
		}
	}
	if !reflect.DeepEqual(got, want) {
		// Do not log credentials, including generated fixture tokens.
		t.Fatal("rendered environment differs from the exact allowlist")
	}
	t.Setenv("CONFIG_SNAPSHOT_PATH", "/dev/null/no-file-state.json")
	for key, value := range got {
		t.Setenv(key, value)
	}
	cfg, err := config.LoadSMProvision()
	if err != nil {
		t.Fatal(err)
	}
	return *cfg
}

func TestSMRenderedCLIUsesNamedState(t *testing.T) {
	root := filepath.Clean(filepath.Join(chartPath(t), "../.."))
	binary := filepath.Join(t.TempDir(), "sm-provision.test")
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	build := exec.CommandContext(ctx, "go", "test", "-c", "-o", binary, "./cmd/sm-provision")
	build.Dir = root
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build accepted CLI helper: %v\n%s", err, out)
	}
	for _, lost := range []bool{false, true} {
		t.Run(fmt.Sprintf("lost-check-response=%t", lost), func(t *testing.T) {
			api := smstoretest.New(t, "sm")
			defer api.Close()
			remote := smstoretest.NewRemote(t)
			defer remote.Close()
			var mu sync.Mutex
			observedPosts := 0
			var pendingObserved bool
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Method == "POST" {
					var d smstore.Document
					if err := json.Unmarshal(api.Data("sm"), &d); err != nil {
						t.Error(err)
					}
					mu.Lock()
					observedPosts++
					pendingObserved = d.Provision.Pending != nil && d.Provision.Registration == nil
					mu.Unlock()
					if d.Provision.Pending == nil || d.Provision.Registration != nil {
						t.Error("remote mutation before durable pending and activation removal")
					}
				}
				if lost && r.URL.Path == "/api/v1/check/add" {
					// The real fake SM handler commits the create, then its response is lost.
					// Subprocess completion joins this observation; no sleep is a receipt.
					remote.Server.Config.Handler.ServeHTTP(httptest.NewRecorder(), r)
					conn, _, err := w.(http.Hijacker).Hijack()
					if err != nil {
						t.Error(err)
						return
					}
					conn.Close()
					return
				}
				remote.Server.Config.Handler.ServeHTTP(w, r)
			}))
			defer server.Close()
			cfg := renderedSMConfig(t, server.URL, smstoretest.Token(t), true)
			probe := smstate.ProbeSpec{Name: "private", Region: "EMEA", Key: smstate.ProbeKey("private", "EMEA"), Latitude: 50, Longitude: 8}
			check := smstate.CheckSpec{Job: "health", Target: "https://api.example/health", Key: smstate.CheckKey("health", "https://api.example/health"), Source: "sample", ProbeKey: probe.Key, FrequencyMS: 60000, TimeoutMS: 3000}
			snapshot, err := smstate.NewSnapshot([]smstate.ProbeSpec{probe}, []smstate.CheckSpec{check}, cfg.SMURL, cfg.SMToken, "dev", time.Now())
			if err != nil {
				t.Fatal(err)
			}
			data, err := json.Marshal(smstore.Document{SchemaVersion: 1, ProvisionLeaseName: cfg.ProvisionLeaseName, Snapshot: &snapshot})
			if err != nil {
				t.Fatal(err)
			}
			api.Set("sm", data)
			api.Permit()
			run := func(cfg config.SMProvisionConfig, success bool) string {
				t.Helper()
				path := filepath.Join(t.TempDir(), "config.json")
				data, err := json.Marshal(cfg)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, data, 0600); err != nil {
					t.Fatal(err)
				}
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				cmd := exec.CommandContext(ctx, binary, "-test.run=^TestKubernetesProvisionProcessHelper$")
				cmd.Env = []string{"SYNTHKIT_SM_PROOF_CONFIG=" + path, "SYNTHKIT_SM_PROOF_API=" + api.Server.URL, "GORACE=atexit_sleep_ms=0"}
				out, err := cmd.CombinedOutput()
				t.Logf("CLI subprocess output (expected success=%t):\n%s", success, out)
				if (err == nil) != success || ctx.Err() != nil {
					t.Fatalf("CLI exit %v; want success=%t\n%s", err, success, out)
				}
				return string(out)
			}
			out := run(cfg, !lost)
			if !strings.Contains(out, "joined-before-release=true") {
				t.Fatal("CLI did not join operations before release")
			}
			var doc smstore.Document
			if err := json.Unmarshal(api.Data("sm"), &doc); err != nil {
				t.Fatal(err)
			}
			if lost {
				if doc.Provision.Pending == nil || doc.Provision.Registration != nil || doc.Provision.Ownership == nil {
					t.Fatal("lost response did not retain pending/ownership and remove activation")
				}
				before := api.Data("sm")
				blocked := run(cfg, false)
				if !strings.Contains(blocked, "pending") || !bytes.Equal(before, api.Data("sm")) {
					t.Fatal("pending retry did not fail closed with unchanged evidence")
				}
			} else {
				if doc.Provision.Pending != nil || doc.Provision.Registration == nil || doc.Provision.Ownership == nil {
					t.Fatal("missing authoritative apply evidence")
				}
				if err := smstate.ValidateRegistration(snapshot, *doc.Provision.Registration); err != nil {
					t.Fatal(err)
				}
				before := api.Data("sm")
				cfg = renderedSMConfig(t, server.URL, cfg.SMToken, false)
				run(cfg, true)
				if !bytes.Equal(before, api.Data("sm")) {
					t.Fatal("preview changed aggregate state")
				}
			}
			mu.Lock()
			posts, pending := observedPosts, pendingObserved
			mu.Unlock()
			remote.Mu.Lock()
			remotePosts, checks := remote.Posts, len(remote.Checks)
			remote.Mu.Unlock()
			equal(t, "exact creates, no replay", posts, 2)
			equal(t, "SM server observed creates", remotePosts, 2)
			equal(t, "real check committed", checks, 1)
			equal(t, "durable pending observed at remote boundary", pending, true)
			api.Mu.Lock()
			requests := append([]string(nil), api.Requests...)
			writes, releases := api.StateWrites, api.Releases
			api.Mu.Unlock()
			seen := map[string]bool{}
			for _, request := range requests {
				seen[request] = true
				if request != "GET /api/v1/namespaces/test/configmaps/sm" && request != "PUT /api/v1/namespaces/test/configmaps/sm" && request != "GET /apis/coordination.k8s.io/v1/namespaces/test/leases/provision" && request != "PUT /apis/coordination.k8s.io/v1/namespaces/test/leases/provision" {
					t.Fatalf("foreign API authority %s", request)
				}
			}
			if len(seen) != 4 || writes == 0 || releases != 2 {
				t.Fatalf("missing actual named GET/PUT/release observations: %v writes=%d releases=%d", seen, writes, releases)
			}
			t.Logf("rendered Job -> actual CLI parser -> accepted production CLI subprocess; named transcript=%v; creates=%d; pending-retained=%t; releases=%d; joined-before-release=true", requests, posts, lost, releases)
		})
	}
}

// Server dry-run exercises actual Helm discovery/read behavior. Operator resources
// are read back from the API store and compared byte-for-byte, not source-grepped.
func TestSMOperatorOwnedResourcesByteIdentical(t *testing.T) {
	for _, upgrade := range []bool{false, true} {
		t.Run(fmt.Sprintf("upgrade=%t", upgrade), func(t *testing.T) {
			api := smstoretest.New(t, "sm")
			defer api.Close()
			api.Set("sm", []byte(`{"schema_version":1,"provision_lease_name":"provision","sentinel":"retain"}`))
			cmBytes, _ := json.Marshal(api.Maps["sm"])
			leaseBytes, _ := json.Marshal(api.Lease)
			stored := map[string][]byte{
				"/api/v1/namespaces/test/configmaps/sm":                         cmBytes,
				"/apis/coordination.k8s.io/v1/namespaces/test/leases/provision": leaseBytes,
				"/apis/coordination.k8s.io/v1/namespaces/test/leases/emitter":   []byte(`{"kind":"Lease","apiVersion":"coordination.k8s.io/v1","metadata":{"name":"emitter","namespace":"test","resourceVersion":"23","annotations":{"external":"retain"}},"spec":{"holderIdentity":"other/process","leaseTransitions":9,"leaseDurationSeconds":30}}`),
			}
			want := map[string][]byte{}
			for path, body := range stored {
				want[path] = bytes.Clone(body)
			}
			resources := map[string][]map[string]any{
				"v1":                           {{"name": "configmaps", "kind": "ConfigMap", "namespaced": true, "verbs": []string{"get"}}, {"name": "serviceaccounts", "kind": "ServiceAccount", "namespaced": true, "verbs": []string{"get"}}},
				"apps/v1":                      {{"name": "deployments", "kind": "Deployment", "namespaced": true, "verbs": []string{"get"}}},
				"batch/v1":                     {{"name": "jobs", "kind": "Job", "namespaced": true, "verbs": []string{"get"}}},
				"networking.k8s.io/v1":         {{"name": "networkpolicies", "kind": "NetworkPolicy", "namespaced": true, "verbs": []string{"get"}}},
				"rbac.authorization.k8s.io/v1": {{"name": "roles", "kind": "Role", "namespaced": true, "verbs": []string{"get"}}, {"name": "rolebindings", "kind": "RoleBinding", "namespaced": true, "verbs": []string{"get"}}},
				"coordination.k8s.io/v1":       {{"name": "leases", "kind": "Lease", "namespaced": true, "verbs": []string{"get"}}},
			}
			groups := []any{}
			discovery := map[string]any{}
			for gv, entries := range resources {
				path := "/api/" + gv
				if gv != "v1" {
					group, version, _ := strings.Cut(gv, "/")
					v := map[string]any{"groupVersion": gv, "version": version}
					groups = append(groups, map[string]any{"name": group, "versions": []any{v}, "preferredVersion": v})
					path = "/apis/" + gv
				}
				discovery[path] = map[string]any{"kind": "APIResourceList", "apiVersion": "v1", "groupVersion": gv, "resources": entries}
			}
			discovery["/apis"] = map[string]any{"kind": "APIGroupList", "apiVersion": "v1", "groups": groups}
			var mu sync.Mutex
			calls := []string{}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				defer mu.Unlock()
				calls = append(calls, r.Method+" "+r.URL.Path)
				w.Header().Set("Content-Type", "application/json")
				if r.Method != "GET" {
					t.Errorf("Helm attempted mutation %s %s", r.Method, r.URL.Path)
					w.WriteHeader(405)
					return
				}
				if body, ok := stored[r.URL.Path]; ok {
					w.Write(body)
					return
				}
				switch r.URL.Path {
				case "/version":
					fmt.Fprint(w, `{"major":"1","minor":"31","gitVersion":"v1.31.0"}`)
				case "/api":
					fmt.Fprint(w, `{"kind":"APIVersions","apiVersion":"v1","versions":["v1"]}`)
				default:
					if response, ok := discovery[r.URL.Path]; ok {
						json.NewEncoder(w).Encode(response)
						return
					}
					known := false
					for gv, entries := range resources {
						prefix := "/apis/" + gv
						if gv == "v1" {
							prefix = "/api/v1"
						}
						for _, entry := range entries {
							start := prefix + "/namespaces/test/" + entry["name"].(string) + "/"
							if strings.HasPrefix(r.URL.Path, start) && !strings.Contains(strings.TrimPrefix(r.URL.Path, start), "/") {
								known = true
							}
						}
					}
					if !known {
						t.Errorf("unknown Helm API read %s", r.URL.Path)
					}
					w.WriteHeader(404)
					fmt.Fprint(w, `{"kind":"Status","apiVersion":"v1","status":"Failure","reason":"NotFound","code":404}`)
				}
			}))
			defer server.Close()
			kubeconfig := filepath.Join(t.TempDir(), "kubeconfig")
			text := fmt.Sprintf("apiVersion: v1\nkind: Config\nclusters:\n- name: fixture\n  cluster:\n    server: %s\ncontexts:\n- name: fixture\n  context:\n    cluster: fixture\n    user: fixture\n    namespace: test\ncurrent-context: fixture\nusers:\n- name: fixture\n  user: {}\n", server.URL)
			if err := os.WriteFile(kubeconfig, []byte(text), 0600); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			version, err := exec.CommandContext(ctx, "helm", "version", "--template", "{{.Version}}").CombinedOutput()
			if err != nil {
				t.Fatal(err)
			}
			args := []string{"-f", smFixture(t), "--namespace", "test", "--kubeconfig", kubeconfig, "--dry-run=server", "--disable-openapi-validation"}
			if strings.HasPrefix(string(version), "v3.") {
				args = append(args, "--validate")
			} else if !strings.HasPrefix(string(version), "v4.") {
				t.Fatalf("unverified Helm %s", version)
			}
			if upgrade {
				args = append(args, "--is-upgrade")
			}
			rendered := renderHA(t, args...)
			for _, obj := range rendered {
				meta := mapping(obj["metadata"])
				if obj["kind"] == "Lease" || (obj["kind"] == "ConfigMap" && meta["name"] != "synthkit-test") || mapping(meta["annotations"])["helm.sh/hook"] != nil {
					t.Errorf("operator state/hook in managed manifest: %s %v", obj["kind"], meta["name"])
				}
			}
			mu.Lock()
			transcript := append([]string(nil), calls...)
			mu.Unlock()
			if len(transcript) == 0 {
				t.Fatal("no actual Helm discovery observation")
			}
			for _, call := range transcript {
				for path := range want {
					if strings.HasSuffix(call, " "+path) {
						t.Errorf("Helm accessed external state: %s", call)
					}
				}
			}
			client := server.Client()
			client.Timeout = 5 * time.Second
			for path, expected := range want {
				response, err := client.Get(server.URL + path)
				if err != nil {
					t.Fatal(err)
				}
				body, err := io.ReadAll(response.Body)
				response.Body.Close()
				if err != nil {
					t.Fatal(err)
				}
				equal(t, "state readback status", response.StatusCode, http.StatusOK)
				equal(t, "byte-identical API object "+path, body, expected)
			}
			t.Logf("actual Helm API transcript=%v; SM aggregate + provisioner Lease + emitter Lease byte-identical on readback", transcript)
		})
	}
}
