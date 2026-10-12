// SPDX-License-Identifier: AGPL-3.0-only
package charttest

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/bpsource"
)

func sourceValues(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "values.yaml")
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}
func TestDeclarativeSourcesChart(t *testing.T) {
	values := sourceValues(t, `blueprintSources:
  enabled: true
  sources:
    - id: example
      name: Example
      namespace: example
      url: https://example.invalid/repository
      ref: refs/heads/main
`)
	objects := renderHA(t, "-f", values)
	deployment := object(t, objects, "Deployment")
	pod := mapping(mapping(mapping(deployment["spec"])["template"])["spec"])
	container := mapping(list(pod["containers"])[0])
	found := false
	for _, env := range list(container["env"]) {
		e := mapping(env)
		if e["name"] == "GIT_SOURCES_FILE" {
			equal(t, "input path", e["value"], "/etc/synthkit/git-sources/sources.json")
			found = true
		}
	}
	if !found {
		t.Fatal("missing declarative input env")
	}
	found = false
	for _, mount := range list(container["volumeMounts"]) {
		m := mapping(mount)
		if m["name"] == "git-source-input" {
			found = true
			equal(t, "input mount", m["mountPath"], "/etc/synthkit/git-sources")
			equal(t, "read-only", m["readOnly"], true)
			if m["subPath"] != nil {
				t.Fatal("subPath prevents projected volume updates")
			}
		}
	}
	if !found {
		t.Fatal("missing input directory mount")
	}
	found = false
	for _, obj := range objects {
		if obj["kind"] == "ConfigMap" && mapping(obj["metadata"])["name"] == "synthkit-test-git-sources" {
			data := mapping(obj["data"])["sources.json"].(string)
			path := filepath.Join(t.TempDir(), "sources.json")
			if err := os.WriteFile(path, []byte(data), 0600); err != nil {
				t.Fatal(err)
			}
			desired, err := bpsource.ReadDeclarations(path, bpsource.SourcePolicy{})
			if err != nil || len(desired) != 1 || desired[0].ID != "example" {
				t.Fatalf("rendered input not accepted by real decoder: %+v %v", desired, err)
			}
			found = true
		}
	}
	if !found {
		t.Fatal("missing declarative input ConfigMap")
	}
	// Altering only desired source definitions must not change the Deployment/restart pods.
	changed := sourceValues(t, strings.ReplaceAll(string(mustRead(t, values)), "name: Example", "name: Changed"))
	other := object(t, renderHA(t, "-f", changed), "Deployment")
	if !reflect.DeepEqual(deployment, other) {
		t.Fatal("desired-set changes force a pod restart")
	}
	external := renderHA(t, "--set", "blueprintSources.enabled=true", "--set", "blueprintSources.existingConfigMap=operator-input")
	for _, obj := range external {
		if obj["kind"] == "ConfigMap" && mapping(obj["metadata"])["name"] == "operator-input" {
			t.Fatal("chart took ownership of external declarative input")
		}
	}
	defaults := renderHA(t)
	raw, _ := json.Marshal(object(t, defaults, "Deployment"))
	if strings.Contains(string(raw), "GIT_SOURCES_FILE") || strings.Contains(string(raw), "git-source-input") {
		t.Fatal("default file mode changed")
	}
}
func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}
func TestDeclarativeSourcesChartRejectsInvalidInput(t *testing.T) {
	for _, content := range []string{
		"blueprintSources:\n  enabled: true\n  sources: wrong\n",
		"blueprintSources:\n  enabled: true\n  key: ../sources.json\n",
		"blueprintSources:\n  existingConfigMap: input\n",
		"blueprintSources:\n  enabled: true\n  sources:\n    - id: example\n      name: Example\n      namespace: example\n      url: https://user:token@example.invalid/repository\n      ref: main\n",
		"blueprintSources:\n  enabled: true\n  sources:\n    - id: example\n      name: Example\n      namespace: example\n      url: https://example.invalid/repository\n      ref: main\n      fetched_sha: forged\n",
	} {
		path := sourceValues(t, content)
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		out, err := exec.CommandContext(ctx, "helm", "template", "synthkit-test", chartPath(t), "-f", path).CombinedOutput()
		cancel()
		if err == nil || !strings.Contains(string(out), "blueprintSources") {
			t.Fatalf("invalid input not rejected by source contract: %v\n%s", err, out)
		}
	}
}
