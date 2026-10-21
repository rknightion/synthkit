// SPDX-License-Identifier: AGPL-3.0-only
package charttest

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

// These identities come from the public multi-platform index and both platform
// configs, not from the source checkout used to run these tests.
const publishedHAImage = "ghcr.io/rknightion/synthkit@sha256:e20d0a7c4730ef86f653026fc2d7a4c23693cec2ac6a5301ab0cea28dc02e154"

func TestPublishedHAImageSelection(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"default", nil, publishedHAImage},
		{"release fallback", []string{"--set-string", "image.digest=", "--set-string", "image.tag="}, "ghcr.io/rknightion/synthkit:1.6.0-rc.160"},
		{"explicit tag", []string{"--set-string", "image.digest=", "--set-string", "image.tag=custom-build"}, "ghcr.io/rknightion/synthkit:custom-build"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			objects := renderHA(t, tc.args...)
			pod := mapping(mapping(mapping(object(t, objects, "Deployment")["spec"])["template"])["spec"])
			equal(t, "published image", mapping(list(pod["containers"])[0])["image"], tc.want)
		})
	}
}

// Exercise the real reusable-workflow inputs, not a second hand-maintained list
// of fixtures: every positive fixture must be assigned exactly once at its floor.
func TestCISchemaFixtureFloors(t *testing.T) {
	root := filepath.Clean(filepath.Join(chartPath(t), "../.."))
	data, err := os.ReadFile(filepath.Join(root, ".github/workflows/ci.yml"))
	if err != nil {
		t.Fatal(err)
	}
	var workflow map[string]any
	if err = yaml.Unmarshal(data, &workflow); err != nil {
		t.Fatal(err)
	}
	jobs := mapping(workflow["jobs"])
	// The reusable's concurrency group is shared by sibling calls. A dependency
	// prevents the HA invocation from cancelling the still-running non-HA one.
	equal(t, "schema invocations serialized", mapping(jobs["helm-validate-ha"])["needs"], []any{"helm-validate"})
	covered := map[string]int{}
	needs := list(mapping(jobs["ci-success"])["needs"])
	for name, raw := range jobs {
		if !strings.HasPrefix(name, "helm-validate") {
			continue
		}
		job := mapping(raw)
		inputs := mapping(job["with"])
		equal(t, "pinned reusable", job["uses"], "rknightion/.github/.github/workflows/helm-validate.yml@c36bf7ded21407b0a5169ecb2502d6a3a385be30")
		found := false
		for _, n := range needs {
			if n == name {
				found = true
			}
		}
		if !found {
			t.Errorf("aggregator omits %s", name)
		}
		if job["if"] != nil || job["continue-on-error"] != nil {
			t.Errorf("schema job may not skip or tolerate errors: %s", name)
		}
		files, err := filepath.Glob(filepath.Join(root, inputs["values-glob"].(string)))
		if err != nil || len(files) == 0 {
			t.Fatalf("empty/invalid fixture glob: %v", err)
		}
		for _, f := range files {
			covered[f]++
			values, err := os.ReadFile(f)
			if err != nil {
				t.Fatal(err)
			}
			var v map[string]any
			if err = yaml.Unmarshal(values, &v); err != nil {
				t.Fatal(err)
			}
			floor := "1.25.0"
			if mapping(v["ha"])["enabled"] == true {
				floor = "1.31.0"
			}
			equal(t, f+" API floor", inputs["kubernetes-versions"], floor)
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			cmd := exec.CommandContext(ctx, "helm", "template", "synthkit", chartPath(t), "-f", f, "--kube-version", inputs["kubernetes-versions"].(string))
			out, err := cmd.CombinedOutput()
			cancel()
			if err != nil {
				t.Errorf("CI input fails for %s: %v\n%s", f, err, out)
			}
		}
	}
	for _, dir := range []string{"ci", "ci-ha"} {
		files, err := filepath.Glob(filepath.Join(chartPath(t), dir, "*-values.yaml"))
		if err != nil {
			t.Fatal(err)
		}
		for _, f := range files {
			equal(t, "CI coverage "+f, covered[f], 1)
		}
	}
}
