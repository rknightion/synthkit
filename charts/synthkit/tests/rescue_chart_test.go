// SPDX-License-Identifier: AGPL-3.0-only
package charttest

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

// The reusable is trusted by origin and by immutability, not by one SHA: the
// exact commit is a routine dependency-update surface. Only repository, path,
// a full 40-hex commit and its release comment are contract.
var (
	reusableUses    = regexp.MustCompile(`^rknightion/\.github/\.github/workflows/helm-validate\.yml@([0-9a-f]{40})$`)
	reusableVersion = regexp.MustCompile(`^# v[0-9]+\.[0-9]+\.[0-9]+$`)
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
	for _, p := range ciWorkflowProblems(t, root, data, true) {
		t.Error(p)
	}
}

// ciWorkflowProblems checks the CI schema legs in workflow and returns every
// contract violation. render also runs helm template for each assigned fixture.
func ciWorkflowProblems(t *testing.T, root string, data []byte, render bool) []string {
	t.Helper()
	var problems []string
	fail := func(format string, args ...any) { problems = append(problems, fmt.Sprintf(format, args...)) }
	var workflow map[string]any
	if err := yaml.Unmarshal(data, &workflow); err != nil {
		t.Fatal(err)
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		t.Fatal(err)
	}
	jobs := mapping(workflow["jobs"])
	// The reusable's concurrency group is shared by sibling calls. A dependency
	// prevents the HA invocation from cancelling the still-running non-HA one.
	if !reflect.DeepEqual(mapping(jobs["helm-validate-ha"])["needs"], []any{"helm-validate"}) {
		fail("schema invocations serialized: helm-validate-ha needs %#v, want [helm-validate]", mapping(jobs["helm-validate-ha"])["needs"])
	}
	covered := map[string]int{}
	needs := list(mapping(jobs["ci-success"])["needs"])
	pins := map[string]bool{}
	for name, raw := range jobs {
		if !strings.HasPrefix(name, "helm-validate") {
			continue
		}
		job := mapping(raw)
		inputs := mapping(job["with"])
		uses, _ := job["uses"].(string)
		if m := reusableUses.FindStringSubmatch(uses); m == nil {
			fail("%s: reusable %q is not rknightion/.github helm-validate.yml pinned to a full 40-hex commit", name, uses)
		} else {
			pins[m[1]] = true
		}
		if c := usesComment(&doc, name); !reusableVersion.MatchString(c) {
			fail("%s: reusable pin comment %q is not a release version like \"# v1.2.3\"", name, c)
		}
		found := false
		for _, n := range needs {
			if n == name {
				found = true
			}
		}
		if !found {
			fail("aggregator omits %s", name)
		}
		if job["if"] != nil || job["continue-on-error"] != nil {
			fail("schema job may not skip or tolerate errors: %s", name)
		}
		glob, _ := inputs["values-glob"].(string)
		files, err := filepath.Glob(filepath.Join(root, glob))
		if glob == "" || err != nil || len(files) == 0 {
			fail("%s: empty/invalid fixture glob %q: %v", name, glob, err)
			continue
		}
		version, _ := inputs["kubernetes-versions"].(string)
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
			if version != floor {
				fail("%s API floor: got %q want %q", f, version, floor)
			}
			if !render {
				continue
			}
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			cmd := exec.CommandContext(ctx, "helm", "template", "synthkit", filepath.Join(root, "charts/synthkit"), "-f", f, "--kube-version", version)
			out, err := cmd.CombinedOutput()
			cancel()
			if err != nil {
				fail("CI input fails for %s: %v\n%s", f, err, out)
			}
		}
	}
	// Both invocations share one concurrency group and one release; a split pin
	// means a partial update.
	if len(pins) > 1 {
		fail("schema invocations use different reusable commits: %v", pins)
	}
	for _, dir := range []string{"ci", "ci-ha"} {
		files, err := filepath.Glob(filepath.Join(root, "charts/synthkit", dir, "*-values.yaml"))
		if err != nil {
			t.Fatal(err)
		}
		for _, f := range files {
			if covered[f] != 1 {
				fail("CI coverage %s: assigned %d times, want 1", f, covered[f])
			}
		}
	}
	return problems
}

// usesComment returns the trailing comment on a job's uses value.
func usesComment(doc *yaml.Node, job string) string {
	if len(doc.Content) == 0 {
		return ""
	}
	child := func(n *yaml.Node, key string) *yaml.Node {
		if n == nil || n.Kind != yaml.MappingNode {
			return nil
		}
		for i := 0; i+1 < len(n.Content); i += 2 {
			if n.Content[i].Value == key {
				return n.Content[i+1]
			}
		}
		return nil
	}
	if u := child(child(child(doc.Content[0], "jobs"), job), "uses"); u != nil {
		return u.LineComment
	}
	return ""
}

func TestCIReusablePinContract(t *testing.T) {
	root := filepath.Clean(filepath.Join(chartPath(t), "../.."))
	data, err := os.ReadFile(filepath.Join(root, ".github/workflows/ci.yml"))
	if err != nil {
		t.Fatal(err)
	}
	// Derive the live pin from the workflow so a routine bump never edits this
	// test; every mutation below is relative to whatever is currently pinned.
	pins := regexp.MustCompile(`rknightion/\.github/\.github/workflows/helm-validate\.yml@([0-9a-f]{40}) # (v[0-9]+\.[0-9]+\.[0-9]+)`).FindAllStringSubmatch(string(data), -1)
	if len(pins) != 2 || pins[0][1] != pins[1][1] || pins[0][2] != pins[1][2] {
		t.Fatalf("want both schema calls on one pinned reusable with a version comment, got %v", pins)
	}
	sha, version := pins[0][1], pins[0][2]
	other := "0" + sha[1:] // a different, equally valid commit
	if other == sha {
		other = "1" + sha[1:]
	}
	pinned := " # " + version
	workflow := string(data)
	mutate := func(from, to string) []byte { return []byte(strings.ReplaceAll(workflow, from, to)) }
	const reusable = "rknightion/.github/.github/workflows/helm-validate.yml@"
	for _, tc := range []struct {
		name    string
		data    []byte
		wantErr string // empty: no problem expected
	}{
		{"routine pin bump", mutate(sha+pinned, other+" # v99.0.0"), ""},
		{"branch ref", mutate(reusable+sha+pinned, reusable+"main"+pinned), "40-hex"},
		{"tag ref", mutate(reusable+sha+pinned, reusable+version), "40-hex"},
		{"short sha", mutate(reusable+sha+pinned, reusable+sha[:7]+pinned), "40-hex"},
		{"uppercase sha", mutate(reusable+sha+pinned, reusable+strings.ToUpper(sha)+pinned), "40-hex"},
		{"foreign repo", mutate("rknightion/.github/.github/workflows/helm-validate.yml@"+sha, "other/.github/.github/workflows/helm-validate.yml@"+sha), "40-hex"},
		{"wrong workflow path", mutate("workflows/helm-validate.yml@"+sha, "workflows/other.yml@"+sha), "40-hex"},
		{"missing version comment", mutate(pinned, ""), "release version"},
		{"split pin", []byte(strings.Replace(workflow, sha, other, 1)), "different reusable commits"},
		{"parallel HA call", mutate("    needs: [helm-validate]\n    permissions:", "    permissions:"), "serialized"},
		{"aggregator omits HA", mutate("helm-validate, helm-validate-ha]", "helm-validate]"), "aggregator omits helm-validate-ha"},
		{"wrong non-HA floor", mutate(`kubernetes-versions: "1.25.0"`, `kubernetes-versions: "1.31.0"`), "API floor"},
		{"wrong HA floor", mutate(`kubernetes-versions: "1.31.0"`, `kubernetes-versions: "1.25.0"`), "API floor"},
		{"unassigned fixtures", mutate("charts/synthkit/ci-ha/*-values.yaml", "charts/synthkit/ci-ha/none-*-values.yaml"), "invalid fixture glob"},
		{"fixtures assigned twice", mutate("charts/synthkit/ci-ha/*-values.yaml", "charts/synthkit/ci/*-values.yaml"), "assigned 2 times"},
		{"tolerated errors", mutate("  helm-validate-ha:\n", "  helm-validate-ha:\n    continue-on-error: true\n"), "may not skip or tolerate"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.name != "routine pin bump" && string(tc.data) == workflow {
				t.Fatal("mutation did not change the workflow")
			}
			problems := strings.Join(ciWorkflowProblems(t, root, tc.data, false), "\n")
			if tc.wantErr == "" && problems != "" {
				t.Fatalf("unexpected problems:\n%s", problems)
			}
			if tc.wantErr != "" && !strings.Contains(problems, tc.wantErr) {
				t.Fatalf("want a problem containing %q, got:\n%s", tc.wantErr, problems)
			}
		})
	}
}
