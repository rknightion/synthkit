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
	if len(calls) != 2+len(liveSeriesSelectors) {
		t.Fatalf("calls=%d, want config check, version, and %d narrow series calls", len(calls), len(liveSeriesSelectors))
	}
	if calls[0] != "gcx config check --context operator-selected" {
		t.Fatalf("first call=%q, want explicit context check", calls[0])
	}
	seriesCall := strings.Join(calls[2:], "\n")
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
