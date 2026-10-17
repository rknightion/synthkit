// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

// These tests build the real synthkit-dash binary once and run it as a subprocess, so flag
// parsing, exit status and the files written by main are what is asserted, not an
// in-process helper. Every distinct generation runs once per test process and is shared
// by the tests that read it (a blueprint's dry cycle dominates the run time).

var (
	binOnce sync.Once
	binPath string
	binErr  error
	binDir  string

	genMu    sync.Mutex
	genCache = map[string]*genRun{}
)

type genRun struct {
	once sync.Once
	dir  string
	res  cliResult
}

func TestMain(m *testing.M) {
	code := m.Run()
	if binDir != "" {
		_ = os.RemoveAll(binDir)
	}
	os.Exit(code)
}

func cliBinary(t *testing.T) string {
	t.Helper()
	binOnce.Do(func() {
		binDir, binErr = os.MkdirTemp("", "synthkit-dash-cli-")
		if binErr != nil {
			return
		}
		binPath = filepath.Join(binDir, "synthkit-dash")
		out, err := exec.Command("go", "build", "-o", binPath, ".").CombinedOutput()
		if err != nil {
			binErr = fmt.Errorf("go build: %v\n%s", err, out)
		}
	})
	if binErr != nil {
		t.Fatal(binErr)
	}
	return binPath
}

const (
	platformBlueprint = "../../blueprints/acme-ai-platform.yaml"
	// netobsBlueprint is the cheap blueprint: its dry cycle takes well under a second, against
	// tens of CPU-seconds for the platform estate, so assertions that do not need the platform's
	// templates (cross-links, rules) run on it.
	netobsBlueprint = "../../blueprints/netobs-global.yaml"
	uidPattern      = `^[A-Za-z0-9_-]{1,40}$`
)

type cliResult struct {
	stdout, stderr string
	code           int
}

func runCLI(t *testing.T, args ...string) cliResult {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, cliBinary(t), args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	res := cliResult{stdout: stdout.String(), stderr: stderr.String()}
	if err != nil {
		exitErr, ok := err.(*exec.ExitError)
		if !ok {
			t.Fatalf("run synthkit-dash: %v", err)
		}
		res.code = exitErr.ExitCode()
	}
	return res
}

func dashArgs(blueprint, out string, extra ...string) []string {
	args := []string{
		"-blueprint", blueprint, "-out", out,
		"-datasource", "prometheus=target-prometheus",
		"-datasource", "loki=target-loki",
		"-datasource", "tempo=target-tempo",
	}
	return append(args, extra...)
}

// generated returns the output directory of one successful generation, running it at most
// once per process. The returned directory must be treated as read-only.
func generated(t *testing.T, blueprint string, extra ...string) string {
	t.Helper()
	key := blueprint + " " + strings.Join(extra, " ")
	genMu.Lock()
	run := genCache[key]
	if run == nil {
		run = &genRun{}
		genCache[key] = run
	}
	genMu.Unlock()
	run.once.Do(func() {
		dir, err := os.MkdirTemp(binDirFor(t), "gen-")
		if err != nil {
			t.Fatal(err)
		}
		run.dir = dir
		run.res = runCLI(t, dashArgs(blueprint, run.dir, extra...)...)
	})
	if run.res.code != 0 {
		t.Fatalf("synthkit-dash %v exited %d\nstdout: %s\nstderr tail: %s", extra, run.res.code, run.res.stdout, tail(run.res.stderr))
	}
	return run.dir
}

func binDirFor(t *testing.T) string {
	cliBinary(t)
	return binDir
}

func tail(s string) string {
	if len(s) > 1500 {
		return s[len(s)-1500:]
	}
	return s
}

func readTree(t *testing.T, dir string) map[string][]byte {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read output dir: %v", err)
	}
	tree := map[string][]byte{}
	for _, e := range entries {
		b, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatal(err)
		}
		tree[e.Name()] = b
	}
	return tree
}

// emitted is one generated output directory, classified by artifact kind.
type emitted struct {
	tree       map[string][]byte
	dashboards map[string]map[string]any // dashboard uid -> parsed manifest
	folderUID  string
	inventory  []invPanel
	rulesFile  string
}

type invPanel struct {
	Dashboard string `json:"dashboard"`
	ID        string `json:"id"`
	Queries   []struct {
		RefID string `json:"ref_id"`
	} `json:"queries"`
}

func load(t *testing.T, dir string) emitted {
	t.Helper()
	e := emitted{tree: readTree(t, dir), dashboards: map[string]map[string]any{}}
	for name, raw := range e.tree {
		switch {
		case strings.HasSuffix(name, "-panel-inventory.json"):
			var inv struct {
				Panels []invPanel `json:"panels"`
			}
			if err := json.Unmarshal(raw, &inv); err != nil {
				t.Fatalf("%s: %v", name, err)
			}
			e.inventory = inv.Panels
		case strings.HasSuffix(name, "-rules.json"):
			e.rulesFile = name
		case strings.HasSuffix(name, "-folder.json"):
			e.folderUID = metadataName(t, name, raw)
		default:
			var doc map[string]any
			if err := json.Unmarshal(raw, &doc); err != nil {
				t.Fatalf("%s: %v", name, err)
			}
			e.dashboards[metadataName(t, name, raw)] = doc
		}
	}
	if e.folderUID == "" || len(e.dashboards) == 0 || len(e.inventory) == 0 {
		t.Fatalf("incomplete output: folder=%q dashboards=%d inventory=%d", e.folderUID, len(e.dashboards), len(e.inventory))
	}
	return e
}

func metadataName(t *testing.T, file string, raw []byte) string {
	t.Helper()
	var doc struct {
		Metadata struct {
			Name string `json:"name"`
		} `json:"metadata"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil || doc.Metadata.Name == "" {
		t.Fatalf("%s has no metadata.name (%v)", file, err)
	}
	return doc.Metadata.Name
}

func (e emitted) dashboardUIDs() []string {
	uids := make([]string, 0, len(e.dashboards))
	for uid := range e.dashboards {
		uids = append(uids, uid)
	}
	sort.Strings(uids)
	return uids
}

// strings returns every JSON string value in a parsed document.
func jsonStrings(v any, visit func(path, s string)) {
	var walk func(any, string)
	walk = func(n any, path string) {
		switch node := n.(type) {
		case map[string]any:
			for k, c := range node {
				walk(c, path+"/"+k)
			}
		case []any:
			for _, c := range node {
				walk(c, path+"[]")
			}
		case string:
			visit(path, node)
		}
	}
	walk(v, "")
}

// navTags are the tags a "dashboards" link selects on.
func (e emitted) navTags() map[string]bool {
	tags := map[string]bool{}
	for _, doc := range e.dashboards {
		spec, _ := doc["spec"].(map[string]any)
		links, _ := spec["links"].([]any)
		for _, l := range links {
			link, _ := l.(map[string]any)
			if link["type"] != "dashboards" {
				continue
			}
			list, _ := link["tags"].([]any)
			for _, tag := range list {
				tags[tag.(string)] = true
			}
		}
	}
	return tags
}

func (e emitted) dashboardTags() map[string]bool {
	tags := map[string]bool{}
	for _, doc := range e.dashboards {
		spec, _ := doc["spec"].(map[string]any)
		list, _ := spec["tags"].([]any)
		for _, tag := range list {
			tags[tag.(string)] = true
		}
	}
	return tags
}

var dashLinkPattern = regexp.MustCompile(`/d/([A-Za-z0-9_-]+)`)

// legacyTokenLeft reports files in which a legacy identity still appears as a whole token (a
// maximal run of UID characters). One tokenising pass per file keeps this cheap under -race.
func legacyTokenLeft(tree map[string][]byte, legacy []string) []string {
	want := make(map[string]bool, len(legacy))
	for _, id := range legacy {
		want[id] = true
	}
	tokens := regexp.MustCompile(`[A-Za-z0-9_-]+`)
	var left []string
	for name, raw := range tree {
		found := map[string]bool{}
		for _, tok := range tokens.FindAll(raw, -1) {
			if want[string(tok)] {
				found[string(tok)] = true
			}
		}
		for id := range found {
			left = append(left, name+": "+id)
		}
	}
	sort.Strings(left)
	return left
}

func TestLegacyOutputIsByteIdentical(t *testing.T) {
	t.Parallel()
	tree := readTree(t, generated(t, trimmedPlatformBlueprint(t)))
	names := make([]string, 0, len(tree))
	for name := range tree {
		names = append(names, name)
	}
	sort.Strings(names)
	var got strings.Builder
	for _, name := range names {
		sum := sha256.Sum256(tree[name])
		fmt.Fprintf(&got, "%s  %s\n", hex.EncodeToString(sum[:]), name)
	}
	golden := filepath.Join("testdata", "legacy-acme-ai-platform-2env.sha256")
	if os.Getenv("UPDATE_GOLDEN") == "1" {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(golden, []byte(got.String()), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("read golden (UPDATE_GOLDEN=1 records it from the default output): %v", err)
	}
	if got.String() != string(want) {
		t.Fatalf("default (no -namespace) output changed\n--- golden\n%s--- got\n%s", want, got.String())
	}
}

func TestNamespaceRepeatedRunsAreByteIdentical(t *testing.T) {
	t.Parallel()
	// "-namespace=alpha" and "-namespace alpha" are the same request, run as two separate processes.
	a := readTree(t, generated(t, netobsBlueprint, "-namespace", "alpha"))
	b := readTree(t, generated(t, netobsBlueprint, "-namespace=alpha"))
	if len(a) == 0 || len(a) != len(b) {
		t.Fatalf("file counts differ: %d vs %d", len(a), len(b))
	}
	for name, raw := range a {
		if !bytes.Equal(raw, b[name]) {
			t.Errorf("%s differs between identical runs", name)
		}
	}
}

func TestNamespacesDoNotCollide(t *testing.T) {
	t.Parallel()
	legacy := load(t, generated(t, netobsBlueprint))
	alpha := load(t, generated(t, netobsBlueprint, "-namespace", "alpha"))
	beta := load(t, generated(t, netobsBlueprint, "-namespace", "beta"))
	seen := map[string]string{}
	claim := func(kind, id, owner string) {
		t.Helper()
		if prev, dup := seen[kind+":"+id]; dup {
			t.Errorf("%s %q claimed by both %s and %s", kind, id, prev, owner)
		}
		seen[kind+":"+id] = owner
	}
	for owner, e := range map[string]emitted{"legacy": legacy, "alpha": alpha, "beta": beta} {
		for _, uid := range e.dashboardUIDs() {
			claim("dashboard", uid, owner)
		}
		claim("folder", e.folderUID, owner)
		for tag := range e.navTags() {
			claim("navtag", tag, owner)
		}
	}
	if len(alpha.dashboards) != len(legacy.dashboards) || len(beta.dashboards) != len(legacy.dashboards) {
		t.Fatalf("namespaced runs emitted %d/%d dashboards, legacy %d", len(alpha.dashboards), len(beta.dashboards), len(legacy.dashboards))
	}
	// File names are disjoint too, so both namespaces can share one output directory (and
	// the same push loop) without one run overwriting the other.
	for name := range alpha.tree {
		if _, clash := beta.tree[name]; clash {
			t.Errorf("file %s is written by both namespaces", name)
		}
		if _, clash := legacy.tree[name]; clash {
			t.Errorf("file %s is written by both the namespace and the legacy run", name)
		}
	}
}

func TestNamespaceInternalLinksResolveInsideTheNamespace(t *testing.T) {
	t.Parallel()
	legacy := load(t, generated(t, trimmedPlatformBlueprint(t)))
	alpha := load(t, generated(t, trimmedPlatformBlueprint(t), "-namespace", "alpha"))

	// The legacy output really does carry internal cross-links and nav tags (otherwise this
	// test would assert nothing).
	legacyLinks := 0
	for _, doc := range legacy.dashboards {
		jsonStrings(doc, func(_, s string) { legacyLinks += len(dashLinkPattern.FindAllString(s, -1)) })
	}
	if legacyLinks < 5 || len(legacy.navTags()) == 0 {
		t.Fatalf("legacy output has %d /d/ links and %d nav tags; expected both", legacyLinks, len(legacy.navTags()))
	}

	links := 0
	for uid, doc := range alpha.dashboards {
		jsonStrings(doc, func(path, s string) {
			for _, m := range dashLinkPattern.FindAllStringSubmatch(s, -1) {
				links++
				if _, ok := alpha.dashboards[m[1]]; !ok {
					t.Errorf("%s %s links to /d/%s, which is not a dashboard in this namespace", uid, path, m[1])
				}
			}
		})
		annotations, _ := doc["metadata"].(map[string]any)["annotations"].(map[string]any)
		if got := annotations["grafana.app/folder"]; got != alpha.folderUID {
			t.Errorf("%s is placed in folder %v, want the namespaced folder %q", uid, got, alpha.folderUID)
		}
	}
	if links != legacyLinks {
		t.Errorf("namespaced output has %d /d/ links, legacy has %d", links, legacyLinks)
	}
	// Tag-selected dropdown links must select only this namespace's dashboards.
	dashTags := alpha.dashboardTags()
	for tag := range alpha.navTags() {
		if !dashTags[tag] {
			t.Errorf("nav dropdown selects tag %q that no dashboard in the namespace carries", tag)
		}
		if legacy.navTags()[tag] {
			t.Errorf("nav dropdown still selects the shared legacy tag %q", tag)
		}
	}
	ids := append(legacy.dashboardUIDs(), legacy.folderUID)
	if left := legacyTokenLeft(alpha.tree, ids); len(left) > 0 {
		t.Errorf("legacy identities leaked into namespaced output: %v", left)
	}
	// Integration deep links to vendor dashboards are outside the generated set and must not be rewritten.
	integ := filepath.Join(t.TempDir(), "integrations.yaml")
	if err := os.WriteFile(integ, []byte("targets:\n  k8s:\n    dashboard_uid: vendor-k8s-overview\n    title: Vendor k8s\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	withInteg := generated(t, "../../blueprints/k8s-full-stack.yaml", "-namespace", "alpha", "-integrations", integ)
	idxRaw := ""
	for name, raw := range readTree(t, withInteg) {
		if strings.HasSuffix(name, "-index.json") {
			idxRaw = string(raw)
		}
	}
	if !strings.Contains(idxRaw, "/d/vendor-k8s-overview?") {
		t.Errorf("vendor deep link was rewritten or missing in the namespaced index")
	}
}

func TestNamespaceInventoryAndVerificationCarryEffectiveUIDs(t *testing.T) {
	t.Parallel()
	legacy := load(t, generated(t, trimmedPlatformBlueprint(t)))
	alphaDir := generated(t, trimmedPlatformBlueprint(t), "-namespace", "alpha")
	alpha := load(t, alphaDir)

	inInventory := map[string]bool{}
	for _, p := range alpha.inventory {
		inInventory[p.Dashboard] = true
		if _, ok := alpha.dashboards[p.Dashboard]; !ok {
			t.Errorf("inventory panel %s/%s names a dashboard that was not emitted", p.Dashboard, p.ID)
		}
	}
	for uid := range alpha.dashboards {
		if !inInventory[uid] {
			t.Errorf("emitted dashboard %s has no inventory panels", uid)
		}
	}
	inventoryPath := filepath.Join(alphaDir, "acme-ai-platform-alpha-panel-inventory.json")
	if _, ok := alpha.tree[filepath.Base(inventoryPath)]; !ok {
		t.Fatalf("namespaced inventory file %s missing; have %v", filepath.Base(inventoryPath), keys(alpha.tree))
	}

	writeObservations := func(inv []invPanel, dir string) string {
		type obs struct {
			Dashboard string `json:"dashboard"`
			PanelID   string `json:"panel_id"`
			RefID     string `json:"ref_id"`
			State     string `json:"state"`
			Evidence  string `json:"evidence"`
		}
		var list []obs
		for _, p := range inv {
			for _, q := range p.Queries {
				list = append(list, obs{p.Dashboard, p.ID, q.RefID, "rendered", "series returned"})
			}
		}
		if len(list) == 0 {
			t.Fatal("inventory has no queries to observe")
		}
		raw, _ := json.Marshal(map[string]any{"observations": list})
		path := filepath.Join(dir, "observations.json")
		if err := os.WriteFile(path, raw, 0o644); err != nil {
			t.Fatal(err)
		}
		return path
	}

	obsDir := t.TempDir()
	ok := runCLI(t, "-verify-inventory", inventoryPath, "-observations", writeObservations(alpha.inventory, obsDir))
	if ok.code != 0 {
		t.Fatalf("verification of namespaced observations failed (%d): %s%s", ok.code, ok.stdout, tail(ok.stderr))
	}
	var report struct {
		Panels []struct {
			Dashboard   string `json:"dashboard"`
			Disposition string `json:"disposition"`
		} `json:"panels"`
	}
	if err := json.Unmarshal([]byte(ok.stdout), &report); err != nil || len(report.Panels) != len(alpha.inventory) {
		t.Fatalf("verification report: %v (%d panels, want %d)", err, len(report.Panels), len(alpha.inventory))
	}
	for _, p := range report.Panels {
		if _, found := alpha.dashboards[p.Dashboard]; !found || p.Disposition != "rendered" {
			t.Errorf("report row %s/%s is not a namespaced rendered panel", p.Dashboard, p.Disposition)
		}
	}

	// Observations recorded against the legacy identities do not verify the namespaced inventory.
	staleDir := t.TempDir()
	stale := runCLI(t, "-verify-inventory", inventoryPath, "-observations", writeObservations(legacy.inventory, staleDir))
	if stale.code == 0 || !strings.Contains(stale.stderr, "missing observation") {
		t.Errorf("legacy-identity observations verified a namespaced inventory (exit %d): %s", stale.code, tail(stale.stderr))
	}
}

func keys(m map[string][]byte) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func TestNamespaceRulesCarryEffectiveIdentities(t *testing.T) {
	t.Parallel()
	legacy := load(t, generated(t, trimmedPlatformBlueprint(t)))
	alpha := load(t, generated(t, trimmedPlatformBlueprint(t), "-namespace", "alpha"))
	if legacy.rulesFile != "acme-ai-platform-rules.json" || alpha.rulesFile != "acme-ai-platform-alpha-rules.json" {
		t.Fatalf("rules files: legacy %q, alpha %q", legacy.rulesFile, alpha.rulesFile)
	}
	idPattern := regexp.MustCompile(`"(?:folderUID|uid)":\s*"([^"]+)"`)
	legacyIDs, alphaIDs := idPattern.FindAllStringSubmatch(string(legacy.tree[legacy.rulesFile]), -1), idPattern.FindAllStringSubmatch(string(alpha.tree[alpha.rulesFile]), -1)
	if len(legacyIDs) == 0 || len(legacyIDs) != len(alphaIDs) {
		t.Fatalf("rules identities: legacy %d, alpha %d", len(legacyIDs), len(alphaIDs))
	}
	for i := range alphaIDs {
		if alphaIDs[i][1] == legacyIDs[i][1] || !regexp.MustCompile(uidPattern).MatchString(alphaIDs[i][1]) {
			t.Errorf("rule identity %q was not namespaced into a valid UID (legacy %q)", alphaIDs[i][1], legacyIDs[i][1])
		}
	}
}

func TestMaximumNamespaceStaysWithinGrafanaUIDLimit(t *testing.T) {
	// Grafana UIDs are at most 40 characters of [A-Za-z0-9_-]
	// (https://grafana.com/docs/grafana/latest/whatsnew/whats-new-in-v10-1/). A maximum-length
	// namespace on a long folder UID pushes it past that, which forces the shortened (hashed) form.
	t.Parallel()
	const longFolder = "netobs-global-dashboards-extra-long"
	e := load(t, generated(t, netobsBlueprint, "-namespace", "abcdefghijkl", "-folder", longFolder))
	valid := regexp.MustCompile(uidPattern)
	all := append(e.dashboardUIDs(), e.folderUID)
	distinct := map[string]bool{}
	shortened := 0
	for _, uid := range all {
		if !valid.MatchString(uid) {
			t.Errorf("%q is not a valid Grafana UID", uid)
		}
		if distinct[uid] {
			t.Errorf("duplicate UID %q", uid)
		}
		distinct[uid] = true
		if uid == e.folderUID && len(uid) == 40 {
			shortened++
		}
	}
	if shortened != 1 {
		t.Errorf("the long folder UID should have taken the shortened 40-character form: %q", e.folderUID)
	}
	for _, p := range e.inventory {
		if !distinct[p.Dashboard] {
			t.Errorf("inventory dashboard %q is not an emitted UID", p.Dashboard)
		}
	}
	// The shortened form is deterministic too.
	again := readTree(t, generated(t, netobsBlueprint, "-namespace=abcdefghijkl", "-folder="+longFolder))
	for name, raw := range e.tree {
		if !bytes.Equal(raw, again[name]) {
			t.Errorf("%s differs between identical runs", name)
		}
	}
}

// The shortened form is a pure function of (namespace, legacy UID): dashboard-sized inputs
// exercise it in-process, since the shipped blueprints' dashboard UIDs rarely need it.
func TestDeriveShortensDeterministicallyWithinTheLimit(t *testing.T) {
	ns, err := newNamespace("abcdefghijkl")
	if err != nil {
		t.Fatal(err)
	}
	valid := regexp.MustCompile(uidPattern)
	seen := map[string]string{}
	for _, legacy := range []string{"acme-ai-eval-langsmith-platform", "acme-ai-eval-langsmith-platforn", "short", strings.Repeat("a-", 20), strings.Repeat("a", 40)} {
		first, err := ns.derive(legacy)
		if err != nil {
			t.Fatal(err)
		}
		if again, _ := ns.derive(legacy); again != first {
			t.Errorf("derive(%q) is not stable: %q then %q", legacy, first, again)
		}
		if !valid.MatchString(first) {
			t.Errorf("derive(%q) = %q is not a valid UID", legacy, first)
		}
		if other, dup := seen[first]; dup {
			t.Errorf("derive(%q) and derive(%q) both give %q", legacy, other, first)
		}
		seen[first] = legacy
	}
	if got, _ := ns.derive("short"); got != "abcdefghijkl-short" {
		t.Errorf("a UID that fits keeps the readable form, got %q", got)
	}
}

func TestMalformedNamespaceIsRefusedBeforeAnyOutput(t *testing.T) {
	for _, ns := range []string{"Alpha", "has-hyphen", "abcdefghijklm", "a b", "-lead", "_lead", "naïve", "a/b", "a.b", "", "x\ny"} {
		t.Run(fmt.Sprintf("%q", ns), func(t *testing.T) {
			out := filepath.Join(t.TempDir(), "out")
			res := runCLI(t, dashArgs(netobsBlueprint, out, "-namespace="+ns)...)
			if res.code == 0 {
				t.Fatalf("namespace %q was accepted", ns)
			}
			if !strings.Contains(res.stderr, "invalid -namespace") || strings.Contains(res.stderr, "flag provided but not defined") {
				t.Errorf("refusal is not a namespace validation error: %s", tail(res.stderr))
			}
			if _, err := os.Stat(out); !os.IsNotExist(err) {
				t.Errorf("output directory was created for refused namespace %q (stat err %v)", ns, err)
			}
		})
	}
}

func TestNamespaceRefusalsLeaveNoPartialOutput(t *testing.T) {
	type refusal struct {
		args func(out string) []string
		want string
	}
	cases := map[string]refusal{
		"missing datasource mapping": {func(out string) []string {
			return []string{"-blueprint", netobsBlueprint, "-out", out, "-datasource", "prometheus=p", "-namespace", "alpha"}
		}, "missing explicit datasource mapping"},
		"folder with invalid chars": {func(out string) []string {
			return dashArgs(netobsBlueprint, out, "-namespace", "alpha", "-folder", "not a uid!")
		}, "-folder:"},
		"folder with a path separator": {func(out string) []string {
			return dashArgs(netobsBlueprint, out, "-namespace", "alpha", "-folder", "team/folder")
		}, "-folder:"},
		"verify mode": {func(out string) []string {
			return []string{"-verify-inventory", "x.json", "-observations", "y.json", "-namespace", "alpha"}
		}, "applies to generation only"},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			out := filepath.Join(t.TempDir(), "out")
			res := runCLI(t, c.args(out)...)
			if res.code == 0 {
				t.Fatalf("expected refusal, got success: %s", res.stdout)
			}
			if !strings.Contains(res.stderr, c.want) {
				t.Errorf("refusal does not mention %q: %s", c.want, tail(res.stderr))
			}
			if _, err := os.Stat(out); !os.IsNotExist(err) {
				entries, _ := os.ReadDir(out)
				t.Errorf("refusal left output behind (stat err %v, %d entries)", err, len(entries))
			}
		})
	}
}
