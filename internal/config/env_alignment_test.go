// SPDX-License-Identifier: AGPL-3.0-only

package config

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

// TestEnvSurfaceAligned is the guardrail that keeps the env-var surface from drifting across the
// places it lives: the Go consumers (config.go + any os.Getenv in cmd/internal), the committed
// `.env.example` template, the `docker-compose.yml` interpolations, and — when present — the real
// gitignored `.env`. It runs in the mandatory gate (`go test ./...`), so adding a config var and
// forgetting to document/provision it CANNOT pass green. The single rule: every env var goes into
// the `.env` files. See CLAUDE.md "Build & verify".
//
// How the surface is extracted (all reads use string-literal keys, by construction — see config.go):
//   - consumed: get("X")/getInt("X") literals in config.go + os.Getenv/LookupEnv("X") anywhere in cmd/+internal/
//   - documented: KEY= lines in .env.example
//   - interpolated: ${KEY} in docker-compose.yml
//   - provisioned: KEY= lines in the real .env (only when it exists locally)
func TestEnvSurfaceAligned(t *testing.T) {
	var (
		getLiteralRe = regexp.MustCompile(`get(?:Int)?\(\s*"([A-Z][A-Z0-9_]*)"`)
		getenvRe     = regexp.MustCompile(`os\.(?:Getenv|LookupEnv)\(\s*"([A-Z][A-Z0-9_]*)"`)
		assignRe     = regexp.MustCompile(`(?m)^\s*([A-Z][A-Z0-9_]*)=`)
		interpRe     = regexp.MustCompile(`\$\{([A-Z][A-Z0-9_]*)`)
	)

	// operatorAllowlist: documented in .env.example for the operator / deploy tooling but not read by
	// any scanned Go source. Keep tiny + justified. The GC_FM_* triplet USED to live here; Fleet
	// Management registration is now wired (config.go reads GC_FM_URL/GC_FM_STACK_ID/GC_FM_TOKEN →
	// runner.Options.Fleet), so those keys are now consumed-and-documented, not operator-only.
	operatorAllowlist := map[string]bool{}

	root := func(rel string) string { return filepath.Join("..", "..", rel) }
	read := func(p string) string {
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatalf("read %s: %v", p, err)
		}
		return string(b)
	}
	keys := func(re *regexp.Regexp, text string) map[string]bool {
		out := map[string]bool{}
		for _, m := range re.FindAllStringSubmatch(text, -1) {
			out[m[1]] = true
		}
		return out
	}
	sortedMissing := func(want map[string]bool, have ...map[string]bool) []string {
		var miss []string
	next:
		for k := range want {
			for _, h := range have {
				if h[k] {
					continue next
				}
			}
			miss = append(miss, k)
		}
		sort.Strings(miss)
		return miss
	}

	// consumed = config.go get/getInt literals ∪ os.Getenv/LookupEnv literals across cmd/ + internal/.
	consumed := keys(getLiteralRe, read("config.go"))
	for _, treeRel := range []string{"cmd", "internal"} {
		tree := root(treeRel)
		err := filepath.WalkDir(tree, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() || !strings.HasSuffix(p, ".go") || strings.HasSuffix(p, "_test.go") {
				return nil
			}
			for k := range keys(getenvRe, read(p)) {
				consumed[k] = true
			}
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", tree, err)
		}
	}

	example := keys(assignRe, read(root(".env.example")))
	compose := read(root("docker-compose.yml"))
	interpolated := keys(interpRe, compose)

	// 1. Every Go-consumed var is documented in .env.example.
	if miss := sortedMissing(consumed, example); len(miss) > 0 {
		t.Errorf("env vars read by Go but missing from .env.example: %v\n→ add them to .env.example (and your .env)", miss)
	}
	// 2. Every docker-compose.yml ${interpolation} is documented in .env.example.
	if miss := sortedMissing(interpolated, example); len(miss) > 0 {
		t.Errorf("vars interpolated in docker-compose.yml but missing from .env.example: %v", miss)
	}
	// 3. Nothing stale in .env.example: every key is consumed, interpolated, or an explicit operator var.
	if miss := sortedMissing(example, consumed, interpolated, operatorAllowlist); len(miss) > 0 {
		t.Errorf(".env.example documents vars nothing consumes: %v\n→ remove them, or add to operatorAllowlist if intentional", miss)
	}
	// 4. docker-compose.yml must pass .env through (so every documented var actually flows to the container).
	if !strings.Contains(compose, "env_file") || !regexp.MustCompile(`env_file:\s*\.env`).MatchString(compose) {
		t.Error("docker-compose.yml must keep `env_file: .env` so .env vars flow to the container")
	}

	// 5. When the real .env exists locally, it must provision every documented var (keep .env aligned
	// with .env.example). Skipped on a clean clone / CI where .env is absent — the committed-file
	// checks above still run there.
	if envText, err := os.ReadFile(root(".env")); err == nil {
		provisioned := keys(assignRe, string(envText))
		if miss := sortedMissing(example, provisioned); len(miss) > 0 {
			t.Errorf("your local .env is missing vars documented in .env.example: %v\n→ add them to .env (copy from .env.example)", miss)
		}
	}
}
