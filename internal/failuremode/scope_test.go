// SPDX-License-Identifier: AGPL-3.0-only

package failuremode

import (
	"regexp"
	"strings"
	"testing"
)

func appConsumer(service, env string) Consumer {
	return Consumer{Kind: ConsumerApp, Identity: map[string]string{"service": service, "deployment_environment_name": env}}
}

func assertionOf(t *testing.T, mode string) Assertion {
	t.Helper()
	a, ok := AssertionFor([]string{mode})
	if !ok {
		t.Fatalf("no assertion for %s", mode)
	}
	return a
}

// The resource modes whose query can filter on a consumer's identity carry a scope; the others
// read the caller's side of a failure, which the schema does not name, and stay blueprint-wide.
func TestOnlyFilterableResourceModesCarryAScope(t *testing.T) {
	scoped := map[string]bool{"dependency_slow": true, "pool_exhausted": true, "quota_reduced": true, "concurrency_reduced": true, "consumer_slowdown": true}
	for _, mode := range resourceAxisModes {
		a := assertionOf(t, mode)
		if scoped[mode] && len(a.Scopes) == 0 {
			t.Errorf("%s declares no consumer scope", mode)
		}
		if !scoped[mode] && len(a.Scopes) != 0 {
			t.Errorf("%s declares a consumer scope %+v, but its series carry no consumer identity", mode, a.Scopes)
		}
	}
	for _, a := range Assertions() {
		if len(a.Scopes) != 0 && !strings.HasPrefix(a.Mode, "") {
			continue
		}
		for _, s := range a.Scopes {
			for _, ph := range regexp.MustCompile(`\{\{[^}]*\}\}`).FindAllString(s.Query, -1) {
				if !strings.HasPrefix(ph, "{{consumers.") && ph != "{{blueprint}}" {
					t.Errorf("%s scope %q uses placeholder %s the harness does not fill", a.Mode, s.Query, ph)
				}
			}
		}
	}
}

func TestScopeNarrowsLatencyToConsumersAndTheirCallers(t *testing.T) {
	a := assertionOf(t, "dependency_slow")
	scoped, n := a.Scope([]Consumer{appConsumer("api", "prod"), appConsumer("partner-gateway", "prod")})
	if n != 2 {
		t.Fatalf("matched %d consumers, want 2", n)
	}
	for _, want := range []string{`service=~"api",deployment_environment_name=~"prod"`, `service=~"partner-gateway",deployment_environment_name=~"prod"`, `blueprint="{{blueprint}}"`, "histogram_avg", "> 0.05"} {
		if !strings.Contains(scoped.Query, want) {
			t.Errorf("scoped query %q missing %s", scoped.Query, want)
		}
	}
	if strings.Contains(scoped.Query, "{{consumers.") {
		t.Errorf("scoped query keeps a placeholder: %q", scoped.Query)
	}
	if !scoped.AnySeries || scoped.Direction != a.Direction || scoped.MinActive != a.MinActive || scoped.Mode != a.Mode {
		t.Errorf("scoping changed more than the query: %+v vs %+v", scoped, a)
	}
	if a.Query == scoped.Query {
		t.Error("scoping left the query unchanged")
	}
}

// Unrelated-series proof at the query level: the service that is not a consumer is not selected,
// and a pattern that would match it by prefix or substring does not.
func TestScopeSelectsExactlyTheDeclaredIdentities(t *testing.T) {
	a := assertionOf(t, "pool_exhausted")
	scoped, _ := a.Scope([]Consumer{appConsumer("api", "prod"), appConsumer("a.b", "prod")})
	matches := regexp.MustCompile(`service=~"([^"]*)"`).FindAllStringSubmatch(scoped.Query, -1)
	if len(matches) == 0 {
		t.Fatalf("no service matcher in %q", scoped.Query)
	}
	// Prometheus anchors a regex matcher on both ends; the query string unescapes \\ to \.
	selected := func(svc string) bool {
		for _, m := range matches {
			if regexp.MustCompile("^(?:" + strings.ReplaceAll(m[1], `\\`, `\`) + ")$").MatchString(svc) {
				return true
			}
		}
		return false
	}
	for svc, want := range map[string]bool{"api": true, "a.b": true, "apix": false, "xapi": false, "aXb": false, "unrelated": false, "": false} {
		if selected(svc) != want {
			t.Errorf("service %q selected = %v, want %v (query %s)", svc, !want, want, scoped.Query)
		}
	}
}

func TestScopeEscapesIdentityValues(t *testing.T) {
	a := assertionOf(t, "concurrency_reduced")
	scoped, n := a.Scope([]Consumer{{Kind: ConsumerLambda, Identity: map[string]string{"dimension_FunctionName": `we"ird\fn`}}})
	if n != 1 {
		t.Fatalf("matched %d", n)
	}
	if !strings.Contains(scoped.Query, `dimension_FunctionName=~"we\"ird\\\\fn"`) {
		t.Errorf("identity not escaped for a PromQL string holding a regex: %q", scoped.Query)
	}
}

func TestScopeKeepsFloorsAndZeroFallbackForSums(t *testing.T) {
	a := assertionOf(t, "concurrency_reduced")
	scoped, n := a.Scope([]Consumer{
		{Kind: ConsumerLambda, Identity: map[string]string{"dimension_FunctionName": "enrich-fn"}},
		{Kind: ConsumerAgentcore, Identity: map[string]string{"name": "arn:aws:bedrock-agentcore:eu-west-1:111122223333:runtime/planner"}},
	})
	if n != 2 || scoped.MinActive != throttleFloor {
		t.Fatalf("n=%d floor=%v, want 2 consumers and the %d floor kept", n, scoped.MinActive, throttleFloor)
	}
	for _, want := range []string{`aws_lambda_throttles_sum{dimension_FunctionName=~"enrich-fn"}`, `aws_bedrock_agentcore_throttles_sum{name=~"arn:aws:bedrock-agentcore:eu-west-1:111122223333:runtime/planner"}`, "or vector(0)"} {
		if !strings.Contains(scoped.Query, want) {
			t.Errorf("scoped query %q missing %s", scoped.Query, want)
		}
	}
	if scoped.AnySeries {
		t.Error("concurrency_reduced is a single aggregate and must stay one")
	}
	// One kind alone scopes to that kind alone.
	onlyLambda, n := a.Scope([]Consumer{{Kind: ConsumerLambda, Identity: map[string]string{"dimension_FunctionName": "enrich-fn"}}})
	if n != 1 || strings.Contains(onlyLambda.Query, "agentcore") {
		t.Errorf("lambda-only scope = %q (n=%d)", onlyLambda.Query, n)
	}
}

func TestScopeQueueSlowdownCoversBothBrokers(t *testing.T) {
	a := assertionOf(t, "consumer_slowdown")
	scoped, n := a.Scope([]Consumer{
		{Kind: ConsumerSQS, Identity: map[string]string{"dimension_QueueName": "validation-queue", "name": "validation-collector"}},
		{Kind: ConsumerSolace, Identity: map[string]string{"instance": "broker-a:9628", "vpn_name": "vpn-1", "queue_name": "orders-q"}},
	})
	if n != 2 || !scoped.AnySeries || scoped.MinActive != backlogFloor {
		t.Fatalf("n=%d anySeries=%v floor=%v", n, scoped.AnySeries, scoped.MinActive)
	}
	for _, want := range []string{`aws_sqs_approximate_age_of_oldest_message_maximum{dimension_QueueName=~"validation-queue"}`, `solace_queue_spool_usage_msgs{`, `queue_name=~"orders-q"`, `vpn_name=~"vpn-1"`, `instance=~"broker-a:9628"`, " or "} {
		if !strings.Contains(scoped.Query, want) {
			t.Errorf("scoped query %q missing %s", scoped.Query, want)
		}
	}
}

func TestScopeQuotaReadsTheEdgeIntoTheConsumer(t *testing.T) {
	a := assertionOf(t, "quota_reduced")
	scoped, n := a.Scope([]Consumer{appConsumer("api", "prod")})
	if n != 1 {
		t.Fatalf("n=%d", n)
	}
	if got := strings.Count(scoped.Query, `server=~"api"`); got != 2 {
		t.Errorf("both blueprint-label forms must be narrowed to the server, got %d in %q", got, scoped.Query)
	}
}

// No consumer of a kind the assertion can filter on: the assertion is returned unchanged, so the
// caller keeps the blueprint-wide observation it had before.
func TestScopeWithoutAFilterableConsumerIsUnchanged(t *testing.T) {
	a := assertionOf(t, "dependency_slow")
	for name, cs := range map[string][]Consumer{
		"none":            nil,
		"construct only":  {{Kind: ConsumerRDS, Identity: map[string]string{"dimension_DBInstanceIdentifier": "orders-db"}}},
		"missing a label": {{Kind: ConsumerApp, Identity: map[string]string{"service": "api"}}},
	} {
		got, n := a.Scope(cs)
		if n != 0 || got.Query != a.Query || len(got.Scopes) != len(a.Scopes) {
			t.Errorf("%s: n=%d query=%q, want the assertion unchanged", name, n, got.Query)
		}
	}
	for _, mode := range []string{"rule_regression", "dependency_unavailable", "credential_expired", "error_spike"} {
		b := assertionOf(t, mode)
		if got, n := b.Scope([]Consumer{appConsumer("api", "prod")}); n != 0 || got.Query != b.Query {
			t.Errorf("%s: scoped to %q (n=%d) although it declares no scope", mode, got.Query, n)
		}
	}
}

// Assertions() hands out copies: scoping one must not rewrite the catalogue.
func TestScopeDoesNotMutateTheCatalogue(t *testing.T) {
	before := assertionOf(t, "dependency_slow")
	scoped, _ := before.Scope([]Consumer{appConsumer("api", "prod")})
	scoped.Query = "x"
	if after := assertionOf(t, "dependency_slow"); after.Query != before.Query || len(after.Scopes) == 0 {
		t.Fatalf("catalogue changed: %q", after.Query)
	}
}

// A scope that filters on several labels selects each consumer's own combination: per-label
// alternations would also match a series pairing one consumer's vpn with another's queue.
func TestMultiLabelScopeSelectsEachConsumersOwnCombination(t *testing.T) {
	a := assertionOf(t, "consumer_slowdown")
	scoped, n := a.Scope([]Consumer{
		{Kind: ConsumerSolace, Identity: map[string]string{"instance": "broker-a:9628", "vpn_name": "vpn-1", "queue_name": "q1"}},
		{Kind: ConsumerSolace, Identity: map[string]string{"instance": "broker-b:9628", "vpn_name": "vpn-2", "queue_name": "q2"}},
	})
	if n != 2 {
		t.Fatalf("n=%d", n)
	}
	for _, want := range []string{
		`solace_queue_spool_usage_msgs{instance=~"broker-a:9628",vpn_name=~"vpn-1",queue_name=~"q1"}`,
		`solace_queue_spool_usage_msgs{instance=~"broker-b:9628",vpn_name=~"vpn-2",queue_name=~"q2"}`,
	} {
		if !strings.Contains(scoped.Query, want) {
			t.Errorf("scoped query %q missing %s", scoped.Query, want)
		}
	}
	if strings.Contains(scoped.Query, "|") {
		t.Errorf("scoped query pairs labels across consumers: %q", scoped.Query)
	}
	// The same pair of environments for one service name must not select the other environment's.
	l := assertionOf(t, "pool_exhausted")
	ls, _ := l.Scope([]Consumer{appConsumer("a", "prod"), appConsumer("b", "dev")})
	if strings.Contains(ls.Query, `service=~"a|b"`) || !strings.Contains(ls.Query, `service=~"a",deployment_environment_name=~"prod"`) || !strings.Contains(ls.Query, `service=~"b",deployment_environment_name=~"dev"`) {
		t.Errorf("latency scope pairs service and environment across consumers: %q", ls.Query)
	}
	// Identical identities collapse to one selector.
	dup, n := a.Scope([]Consumer{
		{Kind: ConsumerSolace, Identity: map[string]string{"instance": "i", "vpn_name": "v", "queue_name": "q"}},
		{Kind: ConsumerSolace, Identity: map[string]string{"instance": "i", "vpn_name": "v", "queue_name": "q"}},
	})
	if strings.Count(dup.Query, "solace_queue_spool_usage_msgs") != 1 {
		t.Errorf("duplicate identities repeated: %q", dup.Query)
	}
}
