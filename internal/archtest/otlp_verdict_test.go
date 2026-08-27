// SPDX-License-Identifier: AGPL-3.0-only

package archtest

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"testing"

	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/runner"
)

// The three lists below are a mechanical transcription of the verdicts recorded in
// signals/otlp-native-verdicts.md (SKT-0007.01). DO NOT add, remove, or move a kind
// here without first recording (or updating) its verdict in that file — this file is
// a guard on the study, not a second place to decide it. If a kind's correct bucket
// is unclear, that is a question for signals/otlp-native-verdicts.md, not for this
// test.

// otlpNativeAllowed is the OTEL-NATIVE list from signals/otlp-native-verdicts.md: kinds
// that have (or, for the 13 "different namespace" CloudWatch/CSP kinds, may eventually
// get) a real OTel-native metric lane. Membership here does not by itself permit an
// OTLP lane emitting the construct's EXISTING promrw family names — see that file's
// "not permission to re-emit" note.
var otlpNativeAllowed = map[string]bool{
	"k8s_cluster":   true,
	"host":          true,
	"web_service":   true,
	"app":           true,
	"ai_agent":      true,
	"beyla_agent":   true,
	"envoy_gateway": true,
	"cw_infra":      true,
	"ec2":           true,
	"rds":           true,
	"docdb":         true,
	"neptune":       true,
	"elasticache":   true,
	"aoss":          true,
	"mwaa":          true,
	"glue":          true,
	"bedrock":       true,
	"agentcore":     true,
	"csp_azure":     true,
	"csp_gcp":       true,
}

// otlpScrapeOnlyForbidden is the SCRAPE-ONLY list from signals/otlp-native-verdicts.md:
// kinds whose sole real telemetry route is a Prometheus scrape, so an OTLP metrics lane
// would fabricate a shape no real deployment produces. Must NEVER declare
// core.OTLPMetrics.
var otlpScrapeOnlyForbidden = map[string]bool{
	"alloy_health":       true,
	"fleet_management":   true,
	"argocd":             true,
	"cert_manager":       true,
	"cluster_autoscaler": true,

	"load_balancer_controller": true,
	"external_dns":             true,
	"ebs_csi":                  true,
	"vpc_cni":                  true,
	"core_dns":                 true,
	"etcd":                     true,
	"karpenter":                true,
	"ksm_ingress":              true,
	"dbo11y_mysql":             true,
	"dbo11y_postgres":          true,
	"synthetic_monitoring":     true,
	"cloudflare":               true,
	"snowflake":                true,
	"langsmith_platform":       true,
	"langsmith_eval":           true,
	"portkey_poller":           true,
	"qualification_pipeline":   true,
	"network_topology":         true,
	"k8s_profiling":            true,
}

// otlpUnresolved is the UNRESOLVED list from signals/otlp-native-verdicts.md: evidence
// was not obtained, so — per that file — "do not build a lane until [the cantfind.md
// row] resolves". Treated the same as SCRAPE-ONLY for the "must not declare" check
// until the verdict lands; NOT counted as an OTEL-NATIVE allowance.
var otlpUnresolved = map[string]bool{
	"portkey_gateway": true,
}

// sourceDirOfConstruct resolves the filesystem directory that implements a registered
// construct kind by reflecting on its own Build function's compiled location — the
// same directory Registration() lives in, discovered from the actual wiring rather
// than guessed from the kind's snake_case name (which does not match every package's
// Go identifier, e.g. "k8s_cluster" -> package k8scluster).
func sourceDirOfConstruct(t *testing.T, reg core.ConstructReg) string {
	t.Helper()
	pc := reflect.ValueOf(reg.Build).Pointer()
	fn := runtime.FuncForPC(pc)
	if fn == nil {
		t.Fatalf("construct %q: could not resolve Build function", reg.Kind)
	}
	file, _ := fn.FileLine(pc)
	if file == "" {
		t.Fatalf("construct %q: could not resolve Build source file", reg.Kind)
	}
	return filepath.Dir(file)
}

// sourceDirOfWorkload is sourceDirOfConstruct for workloads.
func sourceDirOfWorkload(t *testing.T, reg core.WorkloadReg) string {
	t.Helper()
	pc := reflect.ValueOf(reg.Build).Pointer()
	fn := runtime.FuncForPC(pc)
	if fn == nil {
		t.Fatalf("workload %q: could not resolve Build function", reg.Kind)
	}
	file, _ := fn.FileLine(pc)
	if file == "" {
		t.Fatalf("workload %q: could not resolve Build source file", reg.Kind)
	}
	return filepath.Dir(file)
}

// dirDeclaresOTLPMetrics scans every non-test .go file directly in dir for a reference
// to core.OTLPMetrics. Package directories in this catalog hold exactly one package
// with no subdirectories, so a flat (non-recursive) scan is exhaustive.
func dirDeclaresOTLPMetrics(t *testing.T, dir string) bool {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir %s: %v", dir, err)
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		path := filepath.Join(dir, name)
		// Parse rather than grep. A raw text scan also matches the constant inside a COMMENT,
		// so documenting the constraint in a scrape-only construct ("must never declare
		// core.OTLPMetrics") would fail this guard — the guard punishing the documentation of
		// its own rule. Comments are not expressions, so an AST walk is immune by construction.
		// Parsed without ParseComments for the same reason.
		file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", path, err)
		}
		found := false
		ast.Inspect(file, func(n ast.Node) bool {
			sel, ok := n.(*ast.SelectorExpr)
			if !ok || sel.Sel == nil || sel.Sel.Name != "OTLPMetrics" {
				return true
			}
			pkg, ok := sel.X.(*ast.Ident)
			if !ok || pkg.Name != "core" {
				return true
			}
			found = true
			return false
		})
		if found {
			return true
		}
	}
	return false
}

// TestOTLPVerdictCoverageIsComplete: every kind registered in the catalog (runner.Catalog,
// the composition root's own wiring) must be recorded in exactly one of the three verdict
// buckets above, and every kind named in the buckets must actually be registered. This is
// the guard against the verdict record silently going stale as the catalog grows — a new
// construct or workload ships with NO recorded OTel-native verdict otherwise, and this test
// is what turns that into a build failure instead of a document nobody re-reads.
func TestOTLPVerdictCoverageIsComplete(t *testing.T) {
	reg := runner.Catalog()

	var allKinds []string
	allKinds = append(allKinds, reg.ConstructKinds()...)
	allKinds = append(allKinds, reg.WorkloadKinds()...)
	sort.Strings(allKinds)

	registered := make(map[string]bool, len(allKinds))
	for _, k := range allKinds {
		registered[k] = true
	}

	for _, k := range allKinds {
		buckets := 0
		if otlpNativeAllowed[k] {
			buckets++
		}
		if otlpScrapeOnlyForbidden[k] {
			buckets++
		}
		if otlpUnresolved[k] {
			buckets++
		}
		switch buckets {
		case 0:
			t.Errorf("catalog kind %q has no recorded OTel-native verdict — add one to "+
				"signals/otlp-native-verdicts.md first, then reflect it in "+
				"internal/archtest/otlp_verdict_test.go", k)
		case 1:
			// exactly one bucket: fine.
		default:
			t.Errorf("catalog kind %q is listed in more than one verdict bucket in "+
				"internal/archtest/otlp_verdict_test.go — it must be in exactly one", k)
		}
	}

	checkStale := func(bucket string, m map[string]bool) {
		for k := range m {
			if !registered[k] {
				t.Errorf("%s list names kind %q, which is not registered in runner.Catalog() — "+
					"remove it or fix the kind name (list must track signals/otlp-native-verdicts.md, "+
					"which itself must track the catalog)", bucket, k)
			}
		}
	}
	checkStale("otlpNativeAllowed", otlpNativeAllowed)
	checkStale("otlpScrapeOnlyForbidden", otlpScrapeOnlyForbidden)
	checkStale("otlpUnresolved", otlpUnresolved)

	const wantTotal = 45
	if got := len(allKinds); got != wantTotal {
		t.Errorf("runner.Catalog() registers %d kinds, want %d (signals/otlp-native-verdicts.md "+
			"was measured against 45 catalog packages on 2026-08-27 — if the catalog has genuinely "+
			"grown or shrunk, re-run the SKT-0007.01 study and update both files)", got, wantTotal)
	}
}

// TestScrapeOnlyKindsDoNotDeclareOTLPMetrics: no kind verdicted SCRAPE-ONLY or UNRESOLVED
// in signals/otlp-native-verdicts.md may declare core.OTLPMetrics in its own source. This
// is the obvious direction: a contributor adding an OTLP lane to (e.g.) karpenter or
// ksm_ingress would emit a shape no real deployment produces, which AGENTS.md forbids
// outright — this test fails the build the moment that happens, rather than relying on
// review catching it.
func TestScrapeOnlyKindsDoNotDeclareOTLPMetrics(t *testing.T) {
	reg := runner.Catalog()

	mustNotDeclare := map[string]bool{}
	for k := range otlpScrapeOnlyForbidden {
		mustNotDeclare[k] = true
	}
	for k := range otlpUnresolved {
		mustNotDeclare[k] = true
	}

	for _, k := range reg.ConstructKinds() {
		if !mustNotDeclare[k] {
			continue
		}
		cr, ok := reg.Construct(k)
		if !ok {
			t.Fatalf("construct kind %q reported by ConstructKinds() but not found by Construct()", k)
		}
		dir := sourceDirOfConstruct(t, cr)
		if dirDeclaresOTLPMetrics(t, dir) {
			t.Errorf("construct %q is verdicted SCRAPE-ONLY or UNRESOLVED in "+
				"signals/otlp-native-verdicts.md but its source in %s references core.OTLPMetrics — "+
				"a scrape-only kind must never declare the OTLP metrics lane", k, dir)
		}
	}

	for _, k := range reg.WorkloadKinds() {
		if !mustNotDeclare[k] {
			continue
		}
		wr, ok := reg.Workload(k)
		if !ok {
			t.Fatalf("workload kind %q reported by WorkloadKinds() but not found by Workload()", k)
		}
		dir := sourceDirOfWorkload(t, wr)
		if dirDeclaresOTLPMetrics(t, dir) {
			t.Errorf("workload %q is verdicted SCRAPE-ONLY or UNRESOLVED in "+
				"signals/otlp-native-verdicts.md but its source in %s references core.OTLPMetrics — "+
				"a scrape-only kind must never declare the OTLP metrics lane", k, dir)
		}
	}
}
