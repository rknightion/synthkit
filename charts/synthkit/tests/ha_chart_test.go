// SPDX-License-Identifier: AGPL-3.0-only
package charttest

import (
	"bytes"
	"context"
	"encoding/base64"
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
	"gopkg.in/yaml.v3"
)

func chartPath(t *testing.T) string {
	t.Helper()
	p, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	return p
}
func renderHA(t *testing.T, args ...string) []map[string]any {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "helm", append([]string{"template", "synthkit-test", chartPath(t)}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("helm render: %v\n%s", err, out)
	}
	dec := yaml.NewDecoder(bytes.NewReader(out))
	var objects []map[string]any
	for {
		var obj map[string]any
		err := dec.Decode(&obj)
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if len(obj) > 0 {
			objects = append(objects, obj)
		}
	}
	return objects
}
func object(t *testing.T, objects []map[string]any, kind string) map[string]any {
	t.Helper()
	for _, obj := range objects {
		if obj["kind"] == kind {
			return obj
		}
	}
	t.Fatalf("missing %s", kind)
	return nil
}
func mapping(v any) map[string]any {
	if v == nil {
		return nil
	}
	return v.(map[string]any)
}
func list(v any) []any {
	if v == nil {
		return nil
	}
	return v.([]any)
}
func equal(t *testing.T, label string, got, want any) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("%s: got %#v want %#v", label, got, want)
	}
}

func TestHAChart(t *testing.T) {
	fixture := filepath.Join(chartPath(t), "ci", "11-ha-values.yaml")
	objects := renderHA(t, "-f", fixture, "--kube-version", "1.31.0")
	deployment := object(t, objects, "Deployment")
	spec := mapping(deployment["spec"])
	equal(t, "replicas", spec["replicas"], 2)
	equal(t, "rollout", spec["strategy"], map[string]any{"type": "RollingUpdate", "rollingUpdate": map[string]any{"maxSurge": 1, "maxUnavailable": 0}})
	pod := mapping(mapping(spec["template"])["spec"])
	equal(t, "mounted API token", pod["automountServiceAccountToken"], true)
	spread := list(pod["topologySpreadConstraints"])
	if len(spread) == 0 {
		t.Fatal("missing hostname topology spread")
	}
	equal(t, "spread topology", mapping(spread[0])["topologyKey"], "kubernetes.io/hostname")
	equal(t, "spread skew", mapping(spread[0])["maxSkew"], 1)
	equal(t, "spread scheduling", mapping(spread[0])["whenUnsatisfiable"], "ScheduleAnyway")
	for _, vol := range list(pod["volumes"]) {
		v := mapping(vol)
		if v["name"] == "data" || v["persistentVolumeClaim"] != nil {
			t.Error("HA has a state-volume dependency")
		}
	}
	for _, obj := range objects {
		if obj["kind"] == "PersistentVolumeClaim" {
			t.Error("HA renders PVC")
		}
	}
	pdb := mapping(object(t, objects, "PodDisruptionBudget")["spec"])
	equal(t, "PDB minimum", pdb["minAvailable"], 1)
	equal(t, "PDB unhealthy eviction", pdb["unhealthyPodEvictionPolicy"], "AlwaysAllow")
	role := object(t, objects, "Role")
	for _, rule := range list(role["rules"]) {
		r := mapping(rule)
		equal(t, "RBAC verbs", r["verbs"], []any{"get", "update", "patch"})
		resources := list(r["resources"])
		equal(t, "single resource type", len(resources), 1)
		switch resources[0] {
		case "leases":
			equal(t, "Lease scope", r["resourceNames"], []any{"synthkit-election"})
		case "configmaps":
			equal(t, "state scope", r["resourceNames"], []any{"synthkit-control", "synthkit-boot", "synthkit-source"})
		default:
			t.Errorf("unexpected RBAC resource %v", resources)
		}
	}
	env := map[string]any{}
	for _, obj := range objects {
		if obj["kind"] == "ConfigMap" && mapping(obj["metadata"])["name"] == "synthkit-test" {
			for k, v := range mapping(obj["data"]) {
				env[k] = v
			}
		}
	}
	for _, entry := range list(mapping(list(pod["containers"])[0])["env"]) {
		e := mapping(entry)
		if e["value"] != nil {
			env[e["name"].(string)] = e["value"]
		}
		if e["name"] == "POD_UID" || e["name"] == "HA_NAMESPACE" {
			field := mapping(mapping(e["valueFrom"])["fieldRef"])["fieldPath"]
			want := "metadata.uid"
			if e["name"] == "HA_NAMESPACE" {
				want = "metadata.namespace"
			}
			equal(t, "downward API", field, want)
			env[e["name"].(string)] = field
		}
	}
	expected := map[string]string{"HA_MODE": "lease", "HA_LEASE_NAME": "synthkit-election", "HA_NAMESPACE": "metadata.namespace", "POD_UID": "metadata.uid", "HA_LEASE_DURATION": "30s", "HA_RENEW_DEADLINE": "15s", "HA_RETRY_PERIOD": "2s", "HA_KUBE_REQUEST_TIMEOUT": "2s", "HA_HTTP_TIMEOUT": "5s", "HA_RETRY_MAX_ELAPSED": "3s", "HA_FLUSH_TIMEOUT": "8s", "HA_FENCE_MARGIN": "2s", "HA_RELEASE_TIMEOUT": "2s", "STATE_BACKEND": "kubernetes", "STATE_CONTROL_CONFIGMAP": "synthkit-control", "STATE_BOOT_CONFIGMAP": "synthkit-boot", "STATE_GIT_SOURCE_CONFIGMAPS": "{\"sample_source\":\"synthkit-source\"}", "STATE_GIT_SOURCE_MAX_BYTES": "786432", "STATE_CAS_MAX_ATTEMPTS": "5", "SEND_DRAIN_DEADLINE": "10s"}
	for key, want := range expected {
		equal(t, key, env[key], want)
		if value, ok := env[key].(string); ok {
			t.Setenv(key, value)
		}
	}
	// Exercise the actual runtime parser with the rendered values, not a source-text proxy.
	t.Setenv("HA_NAMESPACE", "default")
	t.Setenv("POD_UID", "fixture-pod")
	t.Setenv("DRY_RUN", "true")
	t.Setenv("SELFOBS_ENABLED", "false")
	cfg, err := config.Load("/dev/null")
	if err != nil {
		t.Fatalf("rendered environment rejected by runtime: %v", err)
	}
	equal(t, "runtime HA mode", cfg.HAMode, "lease")
	equal(t, "runtime state backend", cfg.StateBackend, "kubernetes")
	equal(t, "runtime source slots", cfg.StateGitSourceConfigMaps, map[string]string{"sample_source": "synthkit-source"})
	equal(t, "runtime document cap", cfg.StateGitSourceMaxBytes, 786432)
	equal(t, "runtime CAS attempts", cfg.StateCASMaxAttempts, 5)
	equal(t, "runtime whole-flush deadline", cfg.HAFlushTimeout, 8*time.Second)
	for key := range env {
		if (strings.HasPrefix(key, "HA_") || strings.HasPrefix(key, "STATE_")) && expected[key] == "" {
			t.Errorf("unexpected HA/state env %s", key)
		}
	}
	// Operator-owned state must be absent from both hooks and managed manifests.
	// This also keeps it out of stored manifests replayed by rollback.
	for _, obj := range objects {
		if obj["kind"] == "Lease" || (obj["kind"] == "ConfigMap" && mapping(obj["metadata"])["name"] != "synthkit-test") {
			t.Errorf("install renders operator-owned mutable state: %v", mapping(obj["metadata"])["name"])
		}
	}
	upgrade := renderHA(t, "-f", fixture, "--kube-version", "1.31.0", "--is-upgrade")
	for _, obj := range upgrade {
		if obj["kind"] == "Lease" || (obj["kind"] == "ConfigMap" && mapping(obj["metadata"])["name"] != "synthkit-test") {
			t.Error("upgrade renders mutable state")
		}
	}
	policy := mapping(object(t, objects, "NetworkPolicy")["spec"])
	for _, kind := range list(policy["policyTypes"]) {
		if kind == "Egress" {
			t.Error("chart must not guess an API-server egress allowlist")
		}
	}
}

func TestHAInvalidChart(t *testing.T) {
	fixture := filepath.Join(chartPath(t), "ci", "11-ha-values.yaml")
	cases := [][]string{{"--set", "ha.createResources=true"}, {"--kube-version", "1.30.0"}, {"--set", "ha.mode=off"}, {"--set", "ha.stateBackend=file"}, {"--set", "ha.replicas=3"}, {"--set", "persistence.enabled=true"}, {"--set", "persistence.existingClaim=state"}, {"--set", "ha.bootConfigMap=synthkit-control"}, {"--set", "ha.gitSourceConfigMaps.other=synthkit-source"}, {"--set", "ha.gitSourceConfigMaps.bad__id=source"}, {"--set", "ha.controlConfigMap="}, {"--set", "ha.unknownField=true"}, {"--set", "smProvision.enabled=true"}, {"--set", "extraEnv[0].name=HA_MODE,extraEnv[0].value=off"}, {"--set", "extraEnv[0].name=STATE_BACKEND,extraEnv[0].value=file"}}
	for _, args := range cases {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, "helm", append([]string{"template", "synthkit-test", chartPath(t), "-f", fixture}, args...)...)
			out, err := cmd.CombinedOutput()
			if err == nil {
				t.Fatalf("invalid render succeeded\n%s", out)
			}
			if !bytes.Contains(out, []byte("execution error")) && !bytes.Contains(out, []byte("specifications of the schema")) {
				t.Fatalf("unexpected failure: %v\n%s", err, out)
			}
		})
	}
	// The non-HA floor and persistence/default rollout remain unchanged.
	objects := renderHA(t, "--kube-version", "1.25.0")
	spec := mapping(object(t, objects, "Deployment")["spec"])
	equal(t, "default replicas", spec["replicas"], 1)
	equal(t, "default rollout", mapping(spec["strategy"])["type"], "Recreate")
	object(t, objects, "PersistentVolumeClaim")
	for _, obj := range objects {
		if obj["kind"] == "Role" || obj["kind"] == "Lease" || obj["kind"] == "PodDisruptionBudget" {
			t.Errorf("default unexpected %v", obj["kind"])
		}
	}
}

func TestHAOperatorOwnedState(t *testing.T) {
	for _, race := range []bool{false, true} {
		for _, upgrade := range []bool{false, true} {
			t.Run(fmt.Sprintf("precreation-race=%t/upgrade=%t", race, upgrade), func(t *testing.T) {
				verifyOperatorOwnedState(t, race, upgrade)
			})
		}
	}
}

func verifyOperatorOwnedState(t *testing.T, race, upgrade bool) {
	t.Helper()
	// Real Helm server dry-run against a bounded local API, not a live cluster.
	// For the race, state starts absent. An old chart lookup receives NotFound
	// immediately before the operator populates that object. With no lookup,
	// precreation happens on Helm's first ordinary release-object read, after
	// rendering and before any execution could begin. Neither path may put state
	// in a hook or managed manifest. Every attempted Helm mutation is recorded.
	document := base64.StdEncoding.EncodeToString([]byte(`{"schema_version":1,"state":{"blueprint_sources":[]},"sequence":7}`))
	objects := map[string]map[string]any{}
	for _, name := range []string{"synthkit-control", "synthkit-boot", "synthkit-source"} {
		objects["/api/v1/namespaces/default/configmaps/"+name] = map[string]any{"apiVersion": "v1", "kind": "ConfigMap", "metadata": map[string]any{"name": name, "namespace": "default", "resourceVersion": "17"}, "binaryData": map[string]any{"document": document}, "data": map[string]any{"unrelated": "retain"}}
	}
	objects["/apis/coordination.k8s.io/v1/namespaces/default/leases/synthkit-election"] = map[string]any{"apiVersion": "coordination.k8s.io/v1", "kind": "Lease", "metadata": map[string]any{"name": "synthkit-election", "namespace": "default", "resourceVersion": "23"}, "spec": map[string]any{"holderIdentity": "pod/process", "leaseTransitions": 9, "leaseDurationSeconds": 30}}
	// Discovery must describe every rendered kind: server dry-run builds the entire
	// release, not just the objects accessed by lookup. Keep this fixture read-only.
	resources := map[string][]map[string]any{
		"v1": {
			{"name": "configmaps", "singularName": "configmap", "namespaced": true, "kind": "ConfigMap", "verbs": []string{"get"}},
			{"name": "serviceaccounts", "singularName": "serviceaccount", "namespaced": true, "kind": "ServiceAccount", "verbs": []string{"get"}},
		},
		"coordination.k8s.io/v1": {{"name": "leases", "singularName": "lease", "namespaced": true, "kind": "Lease", "verbs": []string{"get"}}},
		"networking.k8s.io/v1":   {{"name": "networkpolicies", "singularName": "networkpolicy", "namespaced": true, "kind": "NetworkPolicy", "verbs": []string{"get"}}},
		"policy/v1":              {{"name": "poddisruptionbudgets", "singularName": "poddisruptionbudget", "namespaced": true, "kind": "PodDisruptionBudget", "verbs": []string{"get"}}},
		"apps/v1":                {{"name": "deployments", "singularName": "deployment", "namespaced": true, "kind": "Deployment", "verbs": []string{"get"}}},
		"rbac.authorization.k8s.io/v1": {
			{"name": "roles", "singularName": "role", "namespaced": true, "kind": "Role", "verbs": []string{"get"}},
			{"name": "rolebindings", "singularName": "rolebinding", "namespaced": true, "kind": "RoleBinding", "verbs": []string{"get"}},
		},
	}
	groups := []map[string]any{}
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
	want := map[string][]byte{}
	stored := map[string][]byte{}
	for path, obj := range objects {
		metadata := mapping(obj["metadata"])
		metadata["uid"] = "operator-owned-" + metadata["name"].(string)
		metadata["labels"] = map[string]any{"owner": "external-operator"}
		metadata["annotations"] = map[string]any{"unrelated": "retain-exactly"}
		encoded, err := json.Marshal(obj)
		if err != nil {
			t.Fatal(err)
		}
		want[path] = encoded
		if !race {
			stored[path] = bytes.Clone(encoded)
		}
	}
	operations := []string{}
	precreated := []string{}
	var apiMu sync.Mutex
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		apiMu.Lock()
		defer apiMu.Unlock()
		operations = append(operations, r.Method+" "+r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		if r.Method != "GET" {
			t.Errorf("Helm attempted mutation %s %s", r.Method, r.URL.Path)
			w.WriteHeader(405)
			return
		}
		if initial, namedState := want[r.URL.Path]; namedState {
			if current, exists := stored[r.URL.Path]; exists {
				_, _ = w.Write(current)
				return
			}
			// Snapshot the NotFound response, then simulate the external operator
			// racing the lookup before Helm can execute its rendered hooks.
			w.WriteHeader(404)
			_, _ = fmt.Fprint(w, `{"kind":"Status","apiVersion":"v1","status":"Failure","reason":"NotFound","code":404}`)
			stored[r.URL.Path] = bytes.Clone(initial)
			precreated = append(precreated, "after NotFound: "+r.URL.Path)
			return
		}
		switch r.URL.Path {
		case "/version":
			_, _ = fmt.Fprint(w, `{"major":"1","minor":"31","gitVersion":"v1.31.0"}`)
		case "/api":
			_, _ = fmt.Fprint(w, `{"kind":"APIVersions","apiVersion":"v1","versions":["v1"]}`)
		case "/apis":
			// Helm's upgrade render does not perform installation ownership
			// reads, so precreate new slots during discovery in that case.
			if race && upgrade {
				for path, initial := range want {
					stored[path] = bytes.Clone(initial)
					precreated = append(precreated, "during discovery: "+path)
				}
			}
			_ = json.NewEncoder(w).Encode(discovery[r.URL.Path])
		default:
			if response, ok := discovery[r.URL.Path]; ok {
				_ = json.NewEncoder(w).Encode(response)
				return
			}
			// Named reads for the ordinary release objects return NotFound,
			// as they would before installation. Never claim that an unknown
			// discovery endpoint succeeded with an empty resource list.
			knownRead := false
			for path, response := range discovery {
				resourceList, ok := response.(map[string]any)
				if !ok || resourceList["kind"] != "APIResourceList" {
					continue
				}
				for _, resource := range resourceList["resources"].([]map[string]any) {
					prefix := path + "/namespaces/default/" + resource["name"].(string) + "/"
					if strings.HasPrefix(r.URL.Path, prefix) && !strings.Contains(strings.TrimPrefix(r.URL.Path, prefix), "/") {
						knownRead = true
					}
				}
			}
			if !knownRead {
				t.Errorf("unexpected API read %s", r.URL.Path)
			} else if race {
				// The new chart never looks state up. Precreate it while Helm
				// checks ordinary release objects after rendering instead.
				for path, initial := range want {
					if _, exists := stored[path]; !exists {
						stored[path] = bytes.Clone(initial)
						precreated = append(precreated, "during "+r.URL.Path+": "+path)
					}
				}
			}
			w.WriteHeader(404)
			_, _ = fmt.Fprint(w, `{"kind":"Status","apiVersion":"v1","status":"Failure","reason":"NotFound","code":404}`)
		}
	}))
	defer server.Close()
	config := filepath.Join(t.TempDir(), "kubeconfig")
	text := fmt.Sprintf("apiVersion: v1\nkind: Config\nclusters:\n- name: fixture\n  cluster:\n    server: %s\ncontexts:\n- name: fixture\n  context:\n    cluster: fixture\n    user: fixture\ncurrent-context: fixture\nusers:\n- name: fixture\n  user: {}\n", server.URL)
	if err := os.WriteFile(config, []byte(text), 0600); err != nil {
		t.Fatal(err)
	}
	fixture := filepath.Join(chartPath(t), "ci", "11-ha-values.yaml")
	// Discovery only, not an OpenAPI service. render_test.sh independently validates
	// the output against real 1.31 schemas. Dry-run does not execute hooks: safety
	// requires their complete absence as well as unchanged API state, not merely
	// observing no writes from a dry-run command.
	args := []string{"-f", fixture, "--kubeconfig", config, "--dry-run=server", "--disable-openapi-validation"}
	if upgrade {
		args = append(args, "--is-upgrade")
	}
	rendered := renderHA(t, args...)
	for _, obj := range rendered {
		if obj["kind"] == "Lease" || (obj["kind"] == "ConfigMap" && mapping(obj["metadata"])["name"] != "synthkit-test") {
			t.Errorf("operator-owned state would be recreated: %v", mapping(obj["metadata"])["name"])
		}
	}
	apiMu.Lock()
	helmcalls := append([]string(nil), operations...)
	populatedCount := len(stored)
	creations := append([]string(nil), precreated...)
	apiMu.Unlock()
	equal(t, "operator populated all objects before Helm returned", populatedCount, len(want))
	if race {
		equal(t, "operator precreation count", len(creations), len(want))
	}
	for _, call := range helmcalls {
		for path := range want {
			if strings.HasSuffix(call, " "+path) {
				t.Errorf("Helm must not access operator state: %s", call)
			}
		}
	}
	// Read back the actual API store, including binary bytes, unrelated keys,
	// metadata, resourceVersions and the populated Lease holder/transitions.
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
		equal(t, "preserved bytes for "+path, string(body), string(expected))
	}
	t.Logf("Helm API operations: %v; external precreation: %v; all %d state objects byte-identical", helmcalls, creations, len(want))
}
