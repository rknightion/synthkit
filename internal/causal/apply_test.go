// SPDX-License-Identifier: AGPL-3.0-only

package causal

import (
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Pinned values, cross-checked against an independent Python sha256 implementation of the
// documented formula.
func TestUnitDrawPinned(t *testing.T) {
	d := UnitDraw("app\x00intake\x00prod\x00prod-cluster\x0042\x007")
	for purpose, want := range map[string]float64{
		"intake-api/intake-db-pool/0/fail":   0.7608611306755116,
		"intake-api/intake-db-pool/0/wait":   0.8085832209220751,
		"enrich-fn/partner-gateway/1/jitter": 0.2247444752370219,
		"":                                   0.8146168206163767,
	} {
		if got := d(purpose); got != want {
			t.Errorf("u(%q) = %.17g, want %.17g", purpose, got, want)
		}
	}
	if got, want := d.Scope("intake-api/intake-db-pool/0")("fail"), 0.7608611306755116; got != want {
		t.Errorf("scoped draw = %v, want %v", got, want)
	}
}

// applyAt advances a model one step under clk and applies one arrival with a fixed draw.
func applyAt(t *testing.T, s Set, clk *fakeClock, name string, u float64) Effect {
	t.Helper()
	m := mustCompile(t, s)
	m.Advance(t0, clk)
	return m.Apply(name, t0, fixedDraw(u))
}

func TestApplyRows(t *testing.T) {
	far := t0.Add(time.Hour)
	slowDB := newClock().on(ModeDependencySlow, "records-db", t0, far, 1)
	pool := set(db, poolRes(60))
	reject := 1 - 1/1.11

	// pool, failure: 503, wait T, server-side origin, emergent (no fault on the pool itself).
	e := applyAt(t, pool, slowDB, "orders-db-pool", reject-0.01)
	if !e.Fail || e.Status != 503 || e.Class != ClassHTTP || e.Transport || e.Wait != 3*time.Second ||
		e.Mode != "" || e.ErrorType != "503" || e.Exception != "" || e.Message != "orders-db-pool: connection pool saturated" {
		t.Fatalf("pool failure %+v", e)
	}
	// pool, admitted at saturation: wait = WaitMS x (0.5 + u), capped at T.
	e = applyAt(t, pool, slowDB, "orders-db-pool", 0.9) // admitted; 3000 x 1.4 capped at T
	if e.Fail || e.Wait != 3*time.Second || !e.Slowed() {
		t.Fatalf("pool admission %+v", e)
	}
	// pool between knee and 1: WaitMS 20, u = 0.3 -> 20 x 0.8 = 16 ms.
	e = applyAt(t, set(db, poolRes(450)), newClock(), "orders-db-pool", 0.3)
	if e.Fail || e.Wait != 16*time.Millisecond || e.Message != "orders-db-pool: connection pool saturated" {
		t.Fatalf("pool queueing %+v", e)
	}
	// pool healthy: no-op.
	if e = applyAt(t, pool, newClock(), "orders-db-pool", 0); e.Fail || e.Wait != 0 || e.LatencyFactor != 1 || e.Slowed() || e.Message != "" {
		t.Fatalf("healthy pool %+v", e)
	}
	// pool_exhausted with a template.
	tpl := poolRes(100)
	tpl.Error = &ErrorTemplate{Type: "SQLTransientConnectionException", Message: "orders-db-pool: connection is not available"}
	e = applyAt(t, set(db, tpl), newClock().on(ModePoolExhausted, "orders-db-pool", t0, far, 1), "orders-db-pool", 0.5)
	if !e.Fail || e.Mode != ModePoolExhausted || e.ErrorType != "SQLTransientConnectionException" ||
		e.Exception != "SQLTransientConnectionException" || e.Message != "orders-db-pool: connection is not available" {
		t.Fatalf("templated pool %+v", e)
	}
	e = applyAt(t, set(db, poolRes(100)), newClock().on(ModePoolExhausted, "orders-db-pool", t0, far, 1), "orders-db-pool", 0.5)
	if e.Message != "orders-db-pool: connection pool exhausted (acquire timeout 3000 ms)" {
		t.Fatalf("pool_exhausted message %q", e.Message)
	}

	// concurrency and quota: 429 throttle, no wait.
	fn := Resource{Name: "enrich-fn-concurrency", Kind: KindConcurrency, Size: 20, HoldMS: 900, Demand: constant(15),
		Error: &ErrorTemplate{Type: "TooManyRequestsException", Message: "Rate Exceeded."}}
	e = applyAt(t, set(fn), newClock().on(ModeConcurrencyReduced, fn.Name, t0, far, 0.7), fn.Name, 0.1)
	if !e.Fail || e.Status != 429 || e.Class != ClassThrottle || e.Wait != 0 || e.Transport || e.Message != "Rate Exceeded." {
		t.Fatalf("concurrency %+v", e)
	}
	quota := Resource{Name: "intake-quota", Kind: KindQuota, LimitRPS: 80, Demand: constant(60)}
	e = applyAt(t, set(quota), newClock().on(ModeQuotaReduced, quota.Name, t0, far, 0.5), quota.Name, 0.2)
	if !e.Fail || e.Status != 429 || e.Class != ClassThrottle || e.ErrorType != "429" || e.Message != "intake-quota: rate quota reduced" {
		t.Fatalf("quota %+v", e)
	}
	if e = applyAt(t, set(quota), newClock().on(ModeQuotaReduced, quota.Name, t0, far, 0.5), quota.Name, 0.5); e.Fail {
		t.Fatalf("quota admitted above reject share %+v", e)
	}

	// queue producer side: 503 only for the overflow share of the step.
	q := Resource{Name: "validation-queue", Kind: KindQueue, Size: 4, HoldMS: 400, MaxDepth: 10, Demand: constant(8)}
	e = applyAt(t, set(q), newClock().on(ModeConsumerSlowdown, q.Name, t0, far, 1), q.Name, 0.1) // 40 in, 10 kept: share 0.75
	if !e.Fail || e.Status != 503 || e.Message != "validation-queue: queue full (consumers slowed)" || e.Wait != 0 {
		t.Fatalf("queue overflow %+v", e)
	}
	if e = applyAt(t, set(q), newClock(), q.Name, 0); e.Fail || e.Wait != 0 {
		t.Fatalf("healthy queue %+v", e)
	}

	// dependency: connect failure at the transport, or a latency factor.
	dep := Resource{Name: "partner-api", Kind: KindDependency, SlowFactor: 6}
	e = applyAt(t, set(dep), newClock().on(ModeDependencyUnavailable, dep.Name, t0, far, 0.5), dep.Name, 0.4)
	if !e.Fail || e.Class != ClassConnect || e.Status != 0 || !e.Transport || e.ErrorType != "_OTHER" ||
		e.LatencyFactor != 1 || e.Mode != ModeDependencyUnavailable || e.Message != "partner-api: dependency unavailable (connection failed)" {
		t.Fatalf("dependency unavailable %+v", e)
	}
	e = applyAt(t, set(dep), newClock().on(ModeDependencySlow, dep.Name, t0, far, 1), dep.Name, 0.4)
	if e.Fail || e.LatencyFactor != 6 || !e.Slowed() || e.Mode != ModeDependencySlow || e.Message != "partner-api: dependency slow" {
		t.Fatalf("dependency slow %+v", e)
	}

	// credential: transport origin; 401 for a token.
	cert := Resource{Name: "partner-api-cert", Kind: KindCredential, Of: "partner-api", CredentialType: CredentialServerCertificate,
		Error: &ErrorTemplate{Type: "SSLHandshakeException", Message: "partner-api: PKIX path validation failed: certificate expired"}}
	e = applyAt(t, set(dep, cert), newClock().on(ModeCredentialExpired, cert.Name, t0, far, 1), cert.Name, 0.99)
	if !e.Fail || e.Class != ClassCredential || e.Status != 0 || !e.Transport || e.ErrorType != "SSLHandshakeException" ||
		e.Exception != "SSLHandshakeException" || e.Mode != ModeCredentialExpired {
		t.Fatalf("certificate %+v", e)
	}
	token := Resource{Name: "partner-token", Kind: KindCredential, Of: "partner-api", CredentialType: CredentialToken}
	e = applyAt(t, set(dep, token), newClock().on(ModeCredentialExpired, token.Name, t0, far, 1), token.Name, 0.5)
	if !e.Fail || e.Class != ClassCredential || e.Status != 401 || e.Transport || e.ErrorType != "401" || e.Message != "partner-token: token expired" {
		t.Fatalf("token %+v", e)
	}

	// rule: 422 at the callee; a 500 override (service-local fault) keeps the rule mechanics.
	rule := Resource{Name: "completeness-rule", Kind: KindRule, RejectShare: 0.35}
	e = applyAt(t, set(rule), newClock().on(ModeRuleRegression, rule.Name, t0, far, 1), rule.Name, 0.3)
	if !e.Fail || e.Status != 422 || e.Class != ClassHTTP || e.Transport || e.Message != "completeness-rule: rule regression rejected the record" {
		t.Fatalf("rule %+v", e)
	}
	if e = applyAt(t, set(rule), newClock().on(ModeRuleRegression, rule.Name, t0, far, 1), rule.Name, 0.36); e.Fail {
		t.Fatalf("rule beyond reject share %+v", e)
	}
	rule.Error = &ErrorTemplate{Status: 500}
	if e = applyAt(t, set(rule), newClock().on(ModeRuleRegression, rule.Name, t0, far, 1), rule.Name, 0.3); e.Status != 500 || e.ErrorType != "500" {
		t.Fatalf("rule 500 override %+v", e)
	}

	// Unknown resource or no state: a no-op.
	m := mustCompile(t, set(rule))
	if e := m.Apply("completeness-rule", t0, fixedDraw(0)); e.Fail || e.LatencyFactor != 1 {
		t.Fatalf("apply before advance %+v", e)
	}
	if e := m.Apply("nope", t0, fixedDraw(0)); e.Fail || e.LatencyFactor != 1 {
		t.Fatalf("apply unknown %+v", e)
	}
}

// Exception events come only from a declared exception or a class-like error type.
func TestExceptionRule(t *testing.T) {
	far := t0.Add(time.Hour)
	for typ, want := range map[string]string{
		"SQLTransientConnectionException": "SQLTransientConnectionException",
		"java.net.ConnectException":       "java.net.ConnectException",
		"timeout":                         "",
		"_OTHER":                          "",
		"503":                             "",
		"connection refused":              "",
		"":                                "",
	} {
		rule := Resource{Name: "r", Kind: KindRule, RejectShare: 1, Error: &ErrorTemplate{Type: typ}}
		if e := applyAt(t, set(rule), newClock().on(ModeRuleRegression, "r", t0, far, 1), "r", 0.5); e.Exception != want {
			t.Errorf("type %q: exception %q, want %q", typ, e.Exception, want)
		}
	}
	rule := Resource{Name: "r", Kind: KindRule, RejectShare: 1, Error: &ErrorTemplate{Type: "timeout", Exception: "TimeoutError"}}
	if e := applyAt(t, set(rule), newClock().on(ModeRuleRegression, "r", t0, far, 1), "r", 0.5); e.Exception != "TimeoutError" {
		t.Errorf("declared exception %q", e.Exception)
	}
}

func TestCallerStatusRows(t *testing.T) {
	plain := CallPolicy{}
	prop := CallPolicy{PropagateThrottle: true}
	cases := []struct {
		name   string
		p      CallPolicy
		c      FailureClass
		status int
		want   int
		wantC  FailureClass
	}{
		{"success", plain, ClassNone, 200, 200, ClassNone},
		{"4xx passes through", plain, ClassHTTP, 422, 422, ClassHTTP},
		{"401 passes through", plain, ClassHTTP, 401, 401, ClassHTTP},
		{"429 throttle becomes 503", plain, ClassThrottle, 429, 503, ClassHTTP},
		{"429 propagated", prop, ClassThrottle, 429, 429, ClassThrottle},
		{"429 as http becomes 503", plain, ClassHTTP, 429, 503, ClassHTTP},
		{"5xx becomes 502", plain, ClassHTTP, 503, 502, ClassHTTP},
		{"500 becomes 502", plain, ClassHTTP, 500, 502, ClassHTTP},
		{"connect becomes 502", plain, ClassConnect, 0, 502, ClassHTTP},
		{"credential becomes 502", plain, ClassCredential, 0, 502, ClassHTTP},
		{"token credential becomes 502", plain, ClassCredential, 401, 502, ClassHTTP},
		{"timeout becomes 504", plain, ClassTimeout, 0, 504, ClassHTTP},
	}
	for _, tc := range cases {
		for _, entry := range []bool{false, true} {
			if got, gotC := CallerStatus(tc.p, tc.c, tc.status, entry); got != tc.want || gotC != tc.wantC {
				t.Errorf("%s (entry %v): got %d/%q, want %d/%q", tc.name, entry, got, gotC, tc.want, tc.wantC)
			}
		}
	}
}

func TestCallPolicy(t *testing.T) {
	p := CallPolicy{TimeoutMS: 3000, Retries: 2, BackoffMS: 200, RetryOn: []string{"throttle", "5xx"}}
	if err := p.Validate(); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		c      FailureClass
		status int
		want   bool
	}{
		{ClassThrottle, 429, true}, {ClassHTTP, 503, true}, {ClassHTTP, 500, true}, {ClassHTTP, 422, false},
		{ClassTimeout, 0, false}, {ClassConnect, 0, false}, {ClassCredential, 0, false}, {ClassCredential, 401, false},
	} {
		if got := p.Retryable(tc.c, tc.status); got != tc.want {
			t.Errorf("Retryable(%q, %d) = %v", tc.c, tc.status, got)
		}
	}
	p.RetryOn = []string{"timeout", "connect"}
	if !p.Retryable(ClassTimeout, 0) || !p.Retryable(ClassConnect, 0) || p.Retryable(ClassHTTP, 502) {
		t.Error("timeout/connect retry classes")
	}
	for name, bad := range map[string]CallPolicy{
		"retries in [0, 3]": {Retries: 4},
		"negative retries":  {Retries: -1},
		"timeout_ms":        {TimeoutMS: -1},
		"backoff_ms":        {BackoffMS: -5},
		"must be one of":    {RetryOn: []string{"4xx"}},
		"is listed twice":   {RetryOn: []string{"5xx", "5xx"}},
	} {
		if err := bad.Validate(); err == nil {
			t.Errorf("%s: accepted %+v", name, bad)
		}
	}
}

func TestValidateRejects(t *testing.T) {
	ok := []Resource{
		{Name: "records-db", Kind: KindDependency, LatencyMS: 5},
		{Name: "pool", Kind: KindPool, Size: 10, HoldMS: 10, AcquireTimeoutMS: 100, HeldAcross: "records-db", Demand: constant(5)},
	}
	if err := Validate(set(ok...)); err != nil {
		t.Fatalf("valid set rejected: %v", err)
	}
	with := func(f func(*Resource)) Set {
		r := ok[1]
		f(&r)
		return set(ok[0], r)
	}
	cases := map[string]Set{
		"unknown kind":                   with(func(r *Resource) { r.Kind = "cache" }),
		"must match":                     with(func(r *Resource) { r.Name = "bad name" }),
		"demand is required":             with(func(r *Resource) { r.Demand = nil }),
		"peak_rps must be > 0":           with(func(r *Resource) { r.Demand = &Demand{} }),
		"off_peak_rps must be in":        with(func(r *Resource) { r.Demand = &Demand{OffPeakRPS: 9, PeakRPS: 5} }),
		"business or constant":           with(func(r *Resource) { r.Demand = &Demand{PeakRPS: 5, Load: "spiky"} }),
		"size must be":                   with(func(r *Resource) { r.Size = 0 }),
		"hold_ms must be > 0":            with(func(r *Resource) { r.HoldMS = 0 }),
		"names no declared resource":     with(func(r *Resource) { r.HeldAcross = "nope" }),
		"not a dependency":               with(func(r *Resource) { r.HeldAcross = "pool" }),
		"acquire_timeout_ms is required": with(func(r *Resource) { r.AcquireTimeoutMS = 0 }),
		"knee must be in (0,1)":          with(func(r *Resource) { r.Knee = 1 }),
		"limit_rps is not a pool field":  with(func(r *Resource) { r.LimitRPS = 3 }),
		"error.status 302 must be in":    with(func(r *Resource) { r.Error = &ErrorTemplate{Status: 302} }),
		"unique across the blueprint":    {Envs: []Declaration{{Env: "a", Resources: ok}, {Env: "b", Resources: ok[:1]}}},
		"limit_rps is required":          set(Resource{Name: "q", Kind: KindQuota, Demand: constant(1)}),
		"max_depth must be >= 0":         set(Resource{Name: "q", Kind: KindQueue, Size: 1, HoldMS: 1, MaxDepth: -1, Demand: constant(1)}),
		"slow_factor must be >= 1":       set(Resource{Name: "d", Kind: KindDependency, SlowFactor: 0.5}),
		"not allowed on a dependency":    set(Resource{Name: "d", Kind: KindDependency, Error: &ErrorTemplate{Status: 503}}),
		"of is required":                 set(Resource{Name: "c", Kind: KindCredential, CredentialType: "token"}),
		"credential_type":                set(ok[0], Resource{Name: "c", Kind: KindCredential, Of: "records-db", CredentialType: "password"}),
		"only for credential_type token": set(ok[0], Resource{Name: "c", Kind: KindCredential, Of: "records-db", CredentialType: "server_certificate", Error: &ErrorTemplate{Status: 495}}),
		"reject_share is required":       set(Resource{Name: "r", Kind: KindRule, RejectShare: 1.5}),
		"demand is not a rule field":     set(Resource{Name: "r", Kind: KindRule, RejectShare: 1, Demand: constant(1)}),
		"size is not a dependency field": set(Resource{Name: "d", Kind: KindDependency, Size: 2}),
	}
	for want, s := range cases {
		err := Validate(s)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: got %v", want, err)
		}
		if _, cerr := Compile(s); cerr == nil {
			t.Errorf("%s: Compile accepted an invalid set", want)
		}
	}
	// A token credential may carry a status override; a rule may carry 500.
	if err := Validate(set(ok[0], Resource{Name: "c", Kind: KindCredential, Of: "records-db", CredentialType: "token", Error: &ErrorTemplate{Status: 403}})); err != nil {
		t.Error(err)
	}
}

func TestSetLookupResolve(t *testing.T) {
	s := Set{Envs: []Declaration{
		{Env: "PROD", Resources: []Resource{{Name: "store-prod", Kind: KindDependency}, {Name: "store", Kind: KindDependency}}},
		{Env: "dev", Resources: []Resource{{Name: "cache", Kind: KindDependency}}},
	}}
	if s.Empty() || !(Set{Envs: []Declaration{{Env: "x"}}}).Empty() {
		t.Fatal("Empty")
	}
	if r, d, ok := s.Resolve("store", "PROD"); !ok || r.Name != "store-prod" || d.Env != "PROD" {
		t.Fatalf("Resolve per-env: %v %v %v", r.Name, d.Env, ok)
	}
	if r, _, ok := s.Resolve("store", "dev"); !ok || r.Name != "store" {
		t.Fatalf("Resolve exact fallback: %v", r.Name)
	}
	if _, _, ok := s.Lookup("nope"); ok {
		t.Fatal("Lookup unknown")
	}
}

func TestFailureModesOnResourceAxis(t *testing.T) {
	if len(FailureModes) != 8 {
		t.Fatalf("%d modes", len(FailureModes))
	}
	for _, m := range FailureModes {
		if m.Axis != AxisResource || m.Help == "" {
			t.Errorf("mode %+v", m)
		}
	}
}

// The package imports only the standard library and internal/failuremode.
func TestImportsStdlibAndFailuremodeOnly(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	for _, f := range files {
		src, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		af, err := parser.ParseFile(fset, f, src, parser.ImportsOnly)
		if err != nil {
			t.Fatal(err)
		}
		for _, imp := range af.Imports {
			path := strings.Trim(imp.Path.Value, `"`)
			first := strings.SplitN(path, "/", 2)[0]
			if path == "github.com/rknightion/synthkit/internal/failuremode" || !strings.Contains(first, ".") {
				continue
			}
			t.Errorf("%s imports %q: internal/causal may import only the standard library and internal/failuremode", f, path)
		}
	}
}

// held_across needs a dependency baseline latency to stretch: a hold that includes it is at
// least that long, and a zero latency would make dependency_slow stretch nothing.
func TestValidateHeldAcrossLatency(t *testing.T) {
	pool := func(holdMS float64, dep Resource) Set {
		return set(dep, Resource{Name: "pool", Kind: KindPool, Size: 10, HoldMS: holdMS, AcquireTimeoutMS: 100, HeldAcross: dep.Name, Demand: constant(5)})
	}
	dep := func(latency float64) Resource {
		return Resource{Name: "records-db", Kind: KindDependency, LatencyMS: latency}
	}
	for name, tc := range map[string]struct {
		s    Set
		want string // "" = valid
	}{
		"latency equals hold":      {pool(15, dep(15)), ""},
		"latency below hold":       {pool(20, dep(15)), ""},
		"zero latency":             {pool(20, dep(0)), "latency_ms must be > 0"},
		"hold below latency":       {pool(10, dep(15)), "hold_ms 10 must be >= the held_across"},
		"queue hold below latency": {set(dep(15), Resource{Name: "q", Kind: KindQueue, Size: 1, HoldMS: 14, HeldAcross: "records-db", Demand: constant(1)}), "hold_ms 14 must be >= the held_across"},
	} {
		err := Validate(tc.s)
		switch {
		case tc.want == "" && err != nil:
			t.Errorf("%s: valid set rejected: %v", name, err)
		case tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)):
			t.Errorf("%s: got %v, want %q", name, err, tc.want)
		}
	}
}
