// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"bytes"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rknightion/synthkit/internal/inventory"
)

func TestRunRequiresExplicitContextBeforeCallingGCX(t *testing.T) {
	t.Parallel()
	called := false
	runner := func(name string, args ...string) ([]byte, error) {
		called = true
		return nil, nil
	}
	err := run([]string{"-corpus", t.TempDir()}, &bytes.Buffer{}, runner)
	if err == nil || !strings.Contains(err.Error(), "-context is required") {
		t.Fatalf("run error=%v, want explicit context requirement", err)
	}
	if called {
		t.Fatal("gcx runner called without an explicit context")
	}
}

func TestRunChecksContextThenReadsAndMergesOnlyInScopeSeries(t *testing.T) {
	t.Parallel()
	corpusDir := t.TempDir()
	var calls []string
	runner := func(name string, args ...string) ([]byte, error) {
		call := name + " " + strings.Join(args, " ")
		calls = append(calls, call)
		switch {
		case strings.HasPrefix(call, "gcx config check "):
			return []byte("configuration valid\nconnectivity online\n"), nil
		case call == "gcx version":
			return []byte("FIELD VALUE\nVersion 1.1.1\n"), nil
		case strings.HasPrefix(call, "gcx metrics metadata "):
			// The stack answers for the CloudWatch family and holds nothing for the
			// remote-write-ingested Kubernetes families.
			return []byte(`{"status":"success","data":{
                    "aws_ec2_cpu_credit_usage_sum":[{"type":"gauge","help":""}]
                }}`), nil
		case strings.HasPrefix(call, "gcx metrics series "):
			return []byte(`{"status":"success","data":[
                    {"__name__":"kube_node_info","cluster":"deployment-cluster","provider_id":"aws:///zone/i-id","kubelet_version":"v1.35.2-eks-build","job":"integrations/kubernetes/kube-state-metrics"},
                    {"__name__":"awscni_eni_allocated","cluster":"deployment-cluster"},
                    {"__name__":"aws_ec2_cpu_credit_usage_sum","aws_account_id":"account-id","region":"region"}
                ]}`), nil
		default:
			return nil, errors.New("unexpected command: " + call)
		}
	}

	var output bytes.Buffer
	err := run([]string{
		"-context", "operator-selected",
		"-corpus", corpusDir,
		"-captured-on", "2026-08-25",
		"-since", "24h",
	}, &output, runner)
	if err != nil {
		t.Fatal(err)
	}
	if len(calls) != 3+len(liveSeriesSelectors) {
		t.Fatalf("calls=%d, want config check, version, %d narrow series calls, and one metadata call", len(calls), len(liveSeriesSelectors))
	}
	if calls[0] != "gcx config check --context operator-selected" {
		t.Fatalf("first call=%q, want explicit context check", calls[0])
	}
	seriesCall := strings.Join(calls[2:2+len(liveSeriesSelectors)], "\n")
	for _, want := range []string{"gcx metrics series", "--context operator-selected", "--since 24h", "--match", "awscni_", "kubeproxy_", "aws_ec2_"} {
		if !strings.Contains(seriesCall, want) {
			t.Fatalf("series call missing %q: %s", want, seriesCall)
		}
	}
	for _, excluded := range []string{"bedrock", "appflow", "genai", "llm"} {
		if strings.Contains(strings.ToLower(seriesCall), excluded) {
			t.Fatalf("series call includes excluded AI/LLM selector %q: %s", excluded, seriesCall)
		}
	}

	if got := calls[len(calls)-1]; got != "gcx metrics metadata --context operator-selected -o json" {
		t.Fatalf("last call=%q, want the metric metadata read-back", got)
	}

	documents, err := inventory.LoadCorpusDir(corpusDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(documents) != 3 {
		t.Fatalf("documents=%d, want k8s, k8s-addons and cw", len(documents))
	}
	for _, area := range []string{"cw", "k8s", "k8s-addons"} {
		path := filepath.Join(corpusDir, area, "eks-live-readback.json")
		if !strings.Contains(output.String(), path) {
			t.Fatalf("output missing written path %q: %s", path, output.String())
		}
	}
	for _, want := range []struct {
		area       string
		metric     string
		instrument string
	}{
		{"cw", "aws_ec2_cpu_credit_usage_sum", inventory.InstrumentGauge},
		{"k8s", "kube_node_info", inventory.InstrumentUnknown},
		{"k8s-addons", "awscni_eni_allocated", inventory.InstrumentUnknown},
	} {
		metric := findCorpusMetric(t, documents, want.area, want.metric)
		if len(metric.InstrumentTypes) != 1 || metric.InstrumentTypes[0] != want.instrument {
			t.Errorf("%s/%s instrument_types=%v, want [%s]", want.area, want.metric, metric.InstrumentTypes, want.instrument)
		}
	}
}

func TestRunReturnsClearContextAuthenticationFailure(t *testing.T) {
	t.Parallel()
	runner := func(name string, args ...string) ([]byte, error) {
		return []byte("authentication required"), errors.New("exit status 1")
	}
	err := run([]string{"-context", "operator-selected", "-corpus", t.TempDir()}, &bytes.Buffer{}, runner)
	if err == nil || !strings.Contains(err.Error(), "gcx context check failed") || !strings.Contains(err.Error(), "authentication required") {
		t.Fatalf("run error=%v, want clear context/authentication failure", err)
	}
}

func findCorpusMetric(t *testing.T, documents []inventory.CorpusDocument, area, name string) inventory.Metric {
	t.Helper()
	for _, document := range documents {
		if document.Area != area {
			continue
		}
		for _, metric := range document.Inventory.Metrics {
			if metric.Name == name {
				return metric
			}
		}
	}
	t.Fatalf("metric %q missing from area %q", name, area)
	return inventory.Metric{}
}

func TestParseGCXVersionAcceptsBothReportedShapes(t *testing.T) {
	t.Parallel()
	for name, data := range map[string]string{
		"table": "FIELD VALUE\nVersion 1.1.1\n",
		"json":  `{"version":"1.1.1","commit":"Homebrew","os":"darwin"}`,
	} {
		got, err := parseGCXVersion([]byte(data))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if got != "1.1.1" {
			t.Errorf("%s version=%q, want 1.1.1", name, got)
		}
	}
	if _, err := parseGCXVersion([]byte("no version here")); err == nil {
		t.Error("unversioned output must fail rather than record an empty collector version")
	}
}
