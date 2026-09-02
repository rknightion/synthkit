// SPDX-License-Identifier: AGPL-3.0-only

//go:build e2e

package e2e

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/rknightion/synthkit/e2e/inventory"
	"github.com/rknightion/synthkit/e2e/receiver"
)

const chartE2ECluster = "synthkit-chart-e2e"

// TestChartE2E deploys the actual chart into a disposable k3d cluster. It uses the
// existing receiver and Schema.Subset contract, then preserves exact receiver-observed
// counter samples over a pod restart to prove I29's clean rate-window behaviour.
func TestChartE2E(t *testing.T) {
	if os.Getenv("SYNTHKIT_CHART_E2E") != "true" {
		t.Skip("set SYNTHKIT_CHART_E2E=true through just chart-e2e")
	}
	if e2eAgentSelected(os.Getenv("SYNTHKIT_E2E_INCLUDE_AGENT")) {
		t.Fatal("chart e2e must not select the agent fixture")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Minute)
	defer cancel()
	for _, tool := range []string{"docker", "k3d", "kubectl", "helm"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Fatalf("chart e2e requires %s: %v", tool, err)
		}
	}
	repoRoot, err := filepath.Abs("..")
	if err != nil {
		t.Fatalf("resolve repository root: %v", err)
	}
	if out := chartCommand(t, ctx, "k3d", "cluster", "list", "-o", "json"); strings.Contains(out, `"name":"`+chartE2ECluster+`"`) {
		t.Fatalf("refusing to reuse an existing k3d cluster named %q", chartE2ECluster)
	}
	chartCommand(t, ctx, "k3d", "cluster", "create", chartE2ECluster, "--wait")
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cleanupCancel()
		chartCommandBestEffort(cleanupCtx, "k3d", "cluster", "delete", chartE2ECluster)
	})
	t.Cleanup(func() {
		if t.Failed() {
			chartDiagnostics(t)
		}
	})

	chartCommand(t, ctx, "docker", "build", "-t", "synthkit-chart-e2e:local", "-f", filepath.Join(repoRoot, "Dockerfile"), repoRoot)
	chartCommand(t, ctx, "docker", "build", "-t", "synthkit-receiver-e2e:local", "-f", filepath.Join(repoRoot, "e2e/receiver/Dockerfile"), repoRoot)
	chartCommand(t, ctx, "k3d", "image", "import", "synthkit-chart-e2e:local", "synthkit-receiver-e2e:local", "-c", chartE2ECluster)

	tlsMaterial := newTestTLS(t)
	chartCommand(t, ctx, "kubectl", "create", "secret", "generic", "receiver-tls", "--from-file=tls.crt="+tlsMaterial.certPath, "--from-file=tls.key="+tlsMaterial.keyPath)
	chartCommand(t, ctx, "kubectl", "create", "configmap", "receiver-ca", "--from-file=ca.crt="+tlsMaterial.caPath)
	chartCommandInput(t, ctx, receiverManifest, "kubectl", "apply", "-f", "-")
	chartCommand(t, ctx, "kubectl", "rollout", "status", "deployment/receiver", "--timeout=3m")
	chartCommand(t, ctx, "kubectl", "create", "secret", "generic", "synthkit-data",
		"--from-literal=GC_TOKEN=e2e",
		"--from-literal=GC_PROM_RW=https://receiver:9099/api/prom/push",
		"--from-literal=GC_PROM_USER=1",
		"--from-literal=GC_OTLP_ENDPOINT=https://receiver:9099/otlp",
		"--from-literal=GC_OTLP_USER=2",
		"--from-literal=GC_LOKI=https://receiver:9099/loki/api/v1/push",
		"--from-literal=GC_LOKI_USER=3")

	values := filepath.Join(t.TempDir(), "chart-e2e-values.yaml")
	if err := os.WriteFile(values, []byte(chartE2EValues), 0o600); err != nil {
		t.Fatalf("write chart values: %v", err)
	}
	chartCommand(t, ctx, "helm", "upgrade", "--install", "synthkit", filepath.Join(repoRoot, "charts/synthkit"), "--values", values)

	port := chartPort(t)
	forwardCtx, stopForward := chartPortForward(t, ctx, port)
	defer stopForward()
	client := &http.Client{Timeout: 10 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: tlsMaterial.roots, ServerName: "receiver"}}}
	defer client.CloseIdleConnections()
	baseURL := fmt.Sprintf("https://127.0.0.1:%d", port)

	expected := dumpSchema(t)
	var before inventory.Schema
	chartEventually(t, forwardCtx, 3*time.Minute, func() error {
		schema, err := chartSchema(client, baseURL)
		if err != nil {
			return err
		}
		if missing := expected.Subset(schema); len(missing) > 0 {
			return fmt.Errorf("declared-but-unreachable lanes: %s", strings.Join(missing, "; "))
		}
		before = schema
		return nil
	})
	if unexpected := before.Subset(expected); len(unexpected) > 0 {
		t.Fatalf("receiver observed undeclared lanes: %s", strings.Join(unexpected, "; "))
	}
	chartCommand(t, ctx, "kubectl", "rollout", "status", "deployment/synthkit", "--timeout=1m")
	beforeCounters := chartCounters(t, client, baseURL)
	if len(beforeCounters) == 0 {
		t.Fatal("receiver observed no producer-declared RW2 counter samples before restart")
	}
	restartBoundary := beforeCounters[0].Timestamp
	for _, sample := range beforeCounters[1:] {
		if sample.Timestamp > restartBoundary {
			restartBoundary = sample.Timestamp
		}
	}

	chartCommand(t, ctx, "kubectl", "rollout", "restart", "deployment/synthkit")
	chartCommand(t, ctx, "kubectl", "rollout", "status", "deployment/synthkit", "--timeout=5m")
	var afterCounters []receiver.CounterSample
	var postRestartCounters []receiver.CounterSample
	var reset counterResetEvidence
	chartEventually(t, forwardCtx, 3*time.Minute, func() error {
		afterCounters = chartCounters(t, client, baseURL)
		postRestartCounters = postRestartCounters[:0]
		for _, sample := range afterCounters {
			if sample.Timestamp > restartBoundary {
				postRestartCounters = append(postRestartCounters, sample)
			}
		}
		if len(postRestartCounters) == 0 {
			return fmt.Errorf("no counter sample newer than restart boundary %d", restartBoundary)
		}
		var err error
		reset, err = assertCleanCounterWindow(beforeCounters, postRestartCounters)
		return err
	})
	t.Logf("counter restart evidence: family=%s before=%g@%d after=%g@%d; total before=%d exact samples, after=%d exact samples",
		reset.Name, reset.BeforeValue, reset.BeforeTimestamp, reset.AfterValue, reset.AfterTimestamp,
		len(beforeCounters), len(postRestartCounters))
}

func chartSchema(client *http.Client, baseURL string) (inventory.Schema, error) {
	resp, err := client.Get(baseURL + "/__inventory") //nolint:noctx
	if err != nil {
		return inventory.Schema{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return inventory.Schema{}, fmt.Errorf("inventory status %d: %s", resp.StatusCode, body)
	}
	var schema inventory.Schema
	return schema, json.NewDecoder(resp.Body).Decode(&schema)
}

func chartCounters(t *testing.T, client *http.Client, baseURL string) []receiver.CounterSample {
	t.Helper()
	resp, err := client.Get(baseURL + "/__counter_samples") //nolint:noctx
	if err != nil {
		t.Fatalf("GET counter samples: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("counter-sample status %d: %s", resp.StatusCode, body)
	}
	var samples []receiver.CounterSample
	if err := json.NewDecoder(resp.Body).Decode(&samples); err != nil {
		t.Fatalf("decode counter samples: %v", err)
	}
	return samples
}

// assertCleanCounterWindow requires a real reset: a non-zero pre-restart counter
// must reappear after the restart with a newer timestamp and a lower exact value.
// Zero-valued counters cannot prove a reset and therefore do not satisfy this check.
type counterResetEvidence struct {
	Name            string
	BeforeValue     float64
	BeforeTimestamp int64
	AfterValue      float64
	AfterTimestamp  int64
}

func assertCleanCounterWindow(before, after []receiver.CounterSample) (counterResetEvidence, error) {
	last := map[string]receiver.CounterSample{}
	for _, sample := range before {
		key := counterIdentity(sample)
		if current, ok := last[key]; !ok || sample.Timestamp > current.Timestamp {
			last[key] = sample
		}
	}
	first := map[string]receiver.CounterSample{}
	for _, sample := range after {
		key := counterIdentity(sample)
		if current, ok := first[key]; !ok || sample.Timestamp < current.Timestamp {
			first[key] = sample
		}
	}
	for key, pre := range last {
		post, ok := first[key]
		if !ok || pre.Value == 0 {
			continue
		}
		if post.Timestamp <= pre.Timestamp {
			return counterResetEvidence{}, fmt.Errorf("counter %s did not advance timestamp across restart", pre.Name)
		}
		if post.Value >= pre.Value {
			return counterResetEvidence{}, fmt.Errorf("counter %s did not reset: before=%g@%d after=%g@%d", pre.Name, pre.Value, pre.Timestamp, post.Value, post.Timestamp)
		}
		return counterResetEvidence{
			Name: pre.Name, BeforeValue: pre.Value, BeforeTimestamp: pre.Timestamp,
			AfterValue: post.Value, AfterTimestamp: post.Timestamp,
		}, nil
	}
	return counterResetEvidence{}, fmt.Errorf("no non-zero counter series proved a clean post-restart rate window")
}

func counterIdentity(sample receiver.CounterSample) string {
	keys := make([]string, 0, len(sample.Labels))
	for key := range sample.Labels {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var b strings.Builder
	b.WriteString(sample.Name)
	for _, key := range keys {
		b.WriteString("\x00" + key + "=" + sample.Labels[key])
	}
	return b.String()
}

func chartEventually(t *testing.T, ctx context.Context, timeout time.Duration, check func() error) {
	t.Helper()
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	tick := time.NewTicker(2 * time.Second)
	defer tick.Stop()
	var last error
	for {
		if err := check(); err == nil {
			return
		} else {
			last = err
		}
		select {
		case <-ctx.Done():
			t.Fatalf("chart e2e context ended: %v (last check: %v)", ctx.Err(), last)
		case <-deadline.C:
			t.Fatalf("chart e2e evidence timeout after %s: %v", timeout, last)
		case <-tick.C:
		}
	}
}

func chartCommand(t *testing.T, ctx context.Context, name string, args ...string) string {
	t.Helper()
	cmd := exec.CommandContext(ctx, name, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %s: %v\n%s", name, strings.Join(args, " "), err, out)
	}
	return string(out)
}

func chartCommandInput(t *testing.T, ctx context.Context, input, name string, args ...string) {
	t.Helper()
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdin = strings.NewReader(input)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %s: %v\n%s", name, strings.Join(args, " "), err, out)
	}
}

func chartCommandBestEffort(ctx context.Context, name string, args ...string) {
	_ = exec.CommandContext(ctx, name, args...).Run()
}

func chartDiagnostics(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	for _, command := range [][]string{
		{"kubectl", "get", "pods", "-o", "wide"},
		{"kubectl", "describe", "deployment/synthkit"},
		{"kubectl", "logs", "deployment/synthkit", "--all-containers", "--tail=200"},
		{"kubectl", "logs", "deployment/receiver", "--all-containers", "--tail=100"},
	} {
		out, err := exec.CommandContext(ctx, command[0], command[1:]...).CombinedOutput()
		t.Logf("diagnostic %s %s: err=%v\n%s", command[0], strings.Join(command[1:], " "), err, out)
	}
}

func chartPort(t *testing.T) int {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("allocate receiver port: %v", err)
	}
	defer listener.Close()
	return listener.Addr().(*net.TCPAddr).Port
}

func chartPortForward(t *testing.T, parent context.Context, port int) (context.Context, func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(parent)
	cmd := exec.CommandContext(ctx, "kubectl", "port-forward", "--address", "127.0.0.1", "service/receiver", fmt.Sprintf("%d:9099", port))
	if err := cmd.Start(); err != nil {
		cancel()
		t.Fatalf("start receiver port-forward: %v", err)
	}
	return ctx, func() { cancel(); _ = cmd.Wait() }
}

const receiverManifest = `apiVersion: apps/v1
kind: Deployment
metadata:
  name: receiver
spec:
  replicas: 1
  selector: {matchLabels: {app: receiver}}
  template:
    metadata: {labels: {app: receiver}}
    spec:
      containers:
        - name: receiver
          image: synthkit-receiver-e2e:local
          imagePullPolicy: Never
          env:
            - {name: RECEIVER_TLS_CERT_FILE, value: /tls/tls.crt}
            - {name: RECEIVER_TLS_KEY_FILE, value: /tls/tls.key}
          volumeMounts: [{name: tls, mountPath: /tls, readOnly: true}]
      volumes: [{name: tls, secret: {secretName: receiver-tls}}]
---
apiVersion: v1
kind: Service
metadata: {name: receiver}
spec:
  selector: {app: receiver}
  ports: [{port: 9099, targetPort: 9099}]
`

const chartE2EValues = `image:
  repository: synthkit-chart-e2e
  tag: local
  pullPolicy: Never
config:
  dryRun: false
  blueprintNames: otlp-native
  tickDefault: 1s
persistence:
  enabled: false
resources:
  requests: {cpu: 50m, memory: 768Mi}
  limits: {memory: 1024Mi}
credentials:
  data:
    existingSecret: synthkit-data
extraEnv:
  - name: SSL_CERT_FILE
    value: /receiver-ca/ca.crt
extraVolumes:
  - name: receiver-ca
    configMap: {name: receiver-ca}
extraVolumeMounts:
  - name: receiver-ca
    mountPath: /receiver-ca
    readOnly: true
`
