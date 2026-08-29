// SPDX-License-Identifier: AGPL-3.0-only

//go:build e2e

package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/moby/moby/api/types/container"
	dockernetwork "github.com/moby/moby/api/types/network"
	"github.com/testcontainers/testcontainers-go"
	tcnetwork "github.com/testcontainers/testcontainers-go/network"
	"github.com/testcontainers/testcontainers-go/wait"

	e2einventory "github.com/rknightion/synthkit/e2e/inventory"
	"github.com/rknightion/synthkit/internal/inventory"
)

const (
	readinessPath     = "/control/readiness"
	controlStatusPath = "/control/status"
	controlToken      = "e2e-control-token"
	readinessTimeout  = 90 * time.Second
)

type readinessReport struct {
	Running        bool                    `json:"running"`
	HTTPReady      bool                    `json:"http_ready"`
	Ready          bool                    `json:"ready"`
	LiveReady      bool                    `json:"live_ready"`
	Blueprints     readinessBlueprints     `json:"blueprints"`
	PersistedState readinessPersistedState `json:"persisted_state"`
	Lanes          []readinessLane         `json:"lanes"`
	Reasons        []string                `json:"reasons"`
}

type readinessBlueprints struct {
	Loaded  int `json:"loaded"`
	Skipped int `json:"skipped"`
	Active  int `json:"active"`
}

type readinessPersistedState struct {
	Writable bool   `json:"writable"`
	Error    string `json:"error"`
}

type readinessLane struct {
	Name       string `json:"name"`
	Configured bool   `json:"configured"`
	Disabled   bool   `json:"disabled"`
	Attempted  bool   `json:"attempted"`
	State      string `json:"state"`
	LiveReady  bool   `json:"live_ready"`
}

type readinessStatus struct {
	Readiness readinessReport `json:"readiness"`
}

func TestDockerReadinessFreshDryRunReturns503(t *testing.T) {
	ctx := context.Background()
	synth, baseURL, client := startReadinessSynthkit(t, ctx, nil, nil, map[string]string{
		"DRY_RUN":              "true",
		"CONFIG_SNAPSHOT_PATH": "/tmp/control-state.json",
	})
	defer printContainerLogs(t, ctx, synth, "synthkit-readiness-dry-run")

	status, report, body, err := getReadiness(ctx, client, baseURL)
	if status != http.StatusServiceUnavailable {
		t.Fatalf("dry-run readiness HTTP status = %d, want 503; body=%s", status, body)
	}
	if err != nil {
		t.Fatalf("decode dry-run %s response: %v; body=%s", readinessPath, err, body)
	}
	if report.Ready || report.LiveReady {
		t.Fatalf("dry-run reported live readiness: %+v", report)
	}
	assertBootstrapReady(t, report)
	assertPublicReadinessSanitized(t, report, body)
	assertDetailedReadinessRequiresAuth(t, ctx, client, baseURL)
	detailed := getDetailedReadiness(t, ctx, client, baseURL)
	assertBootstrapReady(t, detailed)
	assertRequiredReadinessLanes(t, detailed.Lanes)
	if !allLanesDisabled(detailed.Lanes) {
		t.Fatalf("dry-run lanes are not explicitly disabled: %+v", detailed.Lanes)
	}
	if !containsReadinessReason(detailed.Reasons, "live delivery is disabled") {
		t.Fatalf("dry-run reason missing from %v", detailed.Reasons)
	}
}

func TestDockerReadinessUnwritablePersistedStateReturns503(t *testing.T) {
	ctx := context.Background()
	net, receiverURL, _, testTLS := startReadinessReceiver(t, ctx)

	synth, baseURL, client := startReadinessSynthkit(t, ctx, net, &readinessSink{
		baseURL: receiverURL,
		caPath:  testTLS.caPath,
	}, map[string]string{
		"CONFIG_SNAPSHOT_PATH": "/app/control-state.json",
	})
	defer printContainerLogs(t, ctx, synth, "synthkit-readiness-unwritable")

	report := waitForDetailedReadiness(t, ctx, client, baseURL, func(report readinessReport) bool {
		return allLiveLanesReady(report.Lanes)
	})
	status, public, body, err := getReadiness(ctx, client, baseURL)
	if err != nil {
		t.Fatalf("decode unwritable-state %s response: %v; body=%s", readinessPath, err, body)
	}
	if status != http.StatusServiceUnavailable {
		t.Fatalf("unwritable-state readiness HTTP status = %d, want 503; body=%s", status, body)
	}
	assertPublicReadinessSanitized(t, public, body)
	if report.PersistedState.Writable || report.PersistedState.Error == "" {
		t.Fatalf("unwritable state probe not reported: %+v", report.PersistedState)
	}
	if report.Ready || !report.LiveReady {
		t.Fatalf("state probe should be the remaining readiness gate: %+v", report)
	}
	assertRequiredReadinessLanes(t, report.Lanes)
	if len(report.Reasons) != 1 || !containsReadinessReason(report.Reasons, "persisted control state is not writable") {
		t.Fatalf("unwritable state should be the sole readiness reason, got %v", report.Reasons)
	}
}

func TestDockerReadinessTransitionsAfterFakeSinkDelivery(t *testing.T) {
	ctx := context.Background()
	net, receiverURL, _, testTLS := startReadinessReceiver(t, ctx)

	synth, baseURL, client := startReadinessSynthkit(t, ctx, net, &readinessSink{
		baseURL: receiverURL,
		caPath:  testTLS.caPath,
	}, map[string]string{
		"CONFIG_SNAPSHOT_PATH": "/tmp/control-state.json",
	})
	defer printContainerLogs(t, ctx, synth, "synthkit-readiness-live")

	status, fresh, body, err := getReadiness(ctx, client, baseURL)
	if status != http.StatusServiceUnavailable {
		t.Fatalf("fresh live readiness HTTP status = %d, want 503; body=%s", status, body)
	}
	if err != nil {
		t.Fatalf("decode fresh %s response: %v; body=%s", readinessPath, err, body)
	}
	assertBootstrapReady(t, fresh)
	assertPublicReadinessSanitized(t, fresh, body)
	detailedFresh := getDetailedReadiness(t, ctx, client, baseURL)
	assertRequiredReadinessLanes(t, detailedFresh.Lanes)
	if !hasNotAttemptedLane(detailedFresh.Lanes) {
		t.Fatalf("fresh live readiness did not expose a not-attempted lane: %+v", detailedFresh.Lanes)
	}

	ready := waitForDetailedReadiness(t, ctx, client, baseURL, func(report readinessReport) bool {
		return report.Ready
	})
	status, publicReady, body, err := getReadiness(ctx, client, baseURL)
	if err != nil {
		t.Fatalf("decode ready %s response: %v; body=%s", readinessPath, err, body)
	}
	if status != http.StatusOK || !ready.Ready || !ready.LiveReady {
		t.Fatalf("post-delivery readiness did not become green: status=%d report=%+v body=%s", status, ready, body)
	}
	assertPublicReadinessSanitized(t, publicReady, body)
	if !allLiveLanesReady(ready.Lanes) {
		t.Fatalf("readiness became green before every configured lane delivered: %+v", ready.Lanes)
	}
	if len(ready.Reasons) != 0 {
		t.Fatalf("ready report retained failure reasons: %v", ready.Reasons)
	}
}

func TestDockerReadinessObservesOTLPLogsLiveDelivery(t *testing.T) {
	ctx := context.Background()
	net, receiverURL, receiver, testTLS := startReadinessReceiver(t, ctx)

	synth, baseURL, client := startReadinessSynthkitForBlueprint(t, ctx, "k8s-full-stack", net, &readinessSink{
		baseURL: receiverURL,
		caPath:  testTLS.caPath,
	}, map[string]string{
		"CONFIG_SNAPSHOT_PATH": "/tmp/control-state.json",
	})
	defer printContainerLogs(t, ctx, synth, "synthkit-readiness-otlp-logs")

	report := waitForDetailedReadiness(t, ctx, client, baseURL, func(report readinessReport) bool {
		for _, lane := range report.Lanes {
			if lane.Name == "otlplogs" {
				return lane.Attempted && lane.State == "success" && lane.LiveReady && report.Ready && report.LiveReady
			}
		}
		return false
	})
	if !report.Ready || !report.LiveReady {
		t.Fatalf("OTLP-logs delivery should satisfy the live-delivery gate: %+v", report)
	}
	received := fetchPublishedInventory(t, ctx, receiver, testTLS)
	if got := e2einventory.ReceiptCount(received, inventory.TransportOTLPLogs); got <= 0 {
		t.Fatalf("receiver OTLP-log receipt count = %d, want > 0", got)
	}
	foundLog := false
	for _, log := range received.Logs {
		if log.Transport == inventory.TransportOTLPLogs {
			foundLog = true
			break
		}
	}
	if !foundLog {
		t.Fatalf("receiver decoded no OTLP log record: %+v", received.Logs)
	}
}

type readinessSink struct {
	baseURL string
	caPath  string
}

func startReadinessSynthkit(
	t *testing.T,
	ctx context.Context,
	net *testcontainers.DockerNetwork,
	sink *readinessSink,
	overrides map[string]string,
) (testcontainers.Container, string, *http.Client) {
	t.Helper()
	return startReadinessSynthkitForBlueprint(t, ctx, e2eBlueprint, net, sink, overrides)
}

func startReadinessSynthkitForBlueprint(
	t *testing.T,
	ctx context.Context,
	blueprintName string,
	net *testcontainers.DockerNetwork,
	sink *readinessSink,
	overrides map[string]string,
) (testcontainers.Container, string, *http.Client) {
	t.Helper()

	blueprintPath, err := filepath.Abs("../blueprints/" + blueprintName + ".yaml")
	if err != nil {
		t.Fatalf("resolve readiness blueprint: %v", err)
	}
	if _, err := os.Stat(blueprintPath); err != nil {
		t.Fatalf("readiness blueprint not found at %s: %v", blueprintPath, err)
	}

	env := map[string]string{
		"DRY_RUN":             "false",
		"BLUEPRINT_NAMES":     blueprintName,
		"JSON_HTTP_ADDR":      "0.0.0.0:8088",
		"SYNTHKIT_BIND":       "127.0.0.1",
		"CONTROL_TOKEN":       controlToken,
		"BLUEPRINTS":          "/app/blueprints-readiness",
		"BLUEPRINT_DATA_DIR":  "/tmp/blueprints-readiness",
		"TICK_DEFAULT":        "10s",
		"SEND_BATCH_DEADLINE": "250ms",
	}
	files := []testcontainers.ContainerFile{{
		HostFilePath:      blueprintPath,
		ContainerFilePath: "/app/blueprints-readiness/" + blueprintName + ".yaml",
		FileMode:          0o644,
	}}
	var networks []string
	if net != nil {
		networks = []string{net.Name}
	}
	if sink != nil {
		env["GC_TOKEN"] = "e2e"
		env["GC_PROM_RW"] = sink.baseURL + "/api/prom/push"
		env["GC_PROM_USER"] = "1"
		env["GC_OTLP_ENDPOINT"] = sink.baseURL + "/otlp"
		env["GC_OTLP_USER"] = "2"
		env["GC_LOKI"] = sink.baseURL + "/loki/api/v1/push"
		env["GC_LOKI_USER"] = "3"
		env["SSL_CERT_FILE"] = "/tmp/e2e-ca.crt"
		files = append(files, testcontainers.ContainerFile{
			HostFilePath:      sink.caPath,
			ContainerFilePath: "/tmp/e2e-ca.crt",
			FileMode:          0o644,
		})
	}
	for key, value := range overrides {
		env[key] = value
	}

	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			FromDockerfile: testcontainers.FromDockerfile{
				Context:    "..",
				Dockerfile: "Dockerfile",
				Repo:       "synthkit-readiness-e2e",
				Tag:        "latest",
				KeepImage:  true,
			},
			ExposedPorts: []string{"8088/tcp"},
			HostConfigModifier: func(hostConfig *container.HostConfig) {
				hostConfig.PortBindings = dockernetwork.PortMap{
					dockernetwork.MustParsePort("8088/tcp"): {{
						HostIP:   netip.MustParseAddr("127.0.0.1"),
						HostPort: "0",
					}},
				}
			},
			Env:      env,
			Files:    files,
			Networks: networks,
			// A listening-port probe only proves Docker's published port accepts a
			// dial, which it does before the control listener serves. Wait on a real
			// HTTP answer instead, accepting 503: fresh readiness is legitimately red.
			WaitingFor: wait.ForHTTP(readinessPath).
				WithPort("8088/tcp").
				WithStatusCodeMatcher(func(int) bool { return true }).
				WithStartupTimeout(2 * time.Minute),
		},
		Started: true,
	})
	testcontainers.CleanupContainer(t, c)
	if err != nil {
		if c != nil {
			printContainerLogs(t, ctx, c, "synthkit-readiness-startup")
		}
		t.Fatalf("start readiness synthkit: %v", err)
	}

	host, err := c.Host(ctx)
	if err != nil {
		t.Fatalf("readiness synthkit host: %v", err)
	}
	port, err := c.MappedPort(ctx, "8088/tcp")
	if err != nil {
		t.Fatalf("readiness synthkit mapped port: %v", err)
	}
	client := &http.Client{Timeout: 10 * time.Second}
	t.Cleanup(client.CloseIdleConnections)
	return c, fmt.Sprintf("http://%s:%s", host, port.Port()), client
}

func startReadinessReceiver(t *testing.T, ctx context.Context) (*testcontainers.DockerNetwork, string, testcontainers.Container, generatedTLS) {
	t.Helper()
	testTLS := newTestTLS(t)

	net, err := tcnetwork.New(ctx)
	if err != nil {
		t.Fatalf("create readiness Docker network: %v", err)
	}
	testcontainers.CleanupNetwork(t, net)

	receiverC, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			FromDockerfile: testcontainers.FromDockerfile{
				Context:    "..",
				Dockerfile: "e2e/receiver/Dockerfile",
				KeepImage:  true,
			},
			ExposedPorts: []string{"9099/tcp"},
			Env: map[string]string{
				"RECEIVER_TLS_CERT_FILE": "/tmp/receiver.crt",
				"RECEIVER_TLS_KEY_FILE":  "/tmp/receiver.key",
			},
			Files: []testcontainers.ContainerFile{
				{HostFilePath: testTLS.certPath, ContainerFilePath: "/tmp/receiver.crt", FileMode: 0o644},
				{HostFilePath: testTLS.keyPath, ContainerFilePath: "/tmp/receiver.key", FileMode: 0o604},
			},
			Networks: []string{net.Name},
			NetworkAliases: map[string][]string{
				net.Name: {"receiver"},
			},
			WaitingFor: wait.ForListeningPort("9099/tcp").WithStartupTimeout(2 * time.Minute),
		},
		Started: true,
	})
	testcontainers.CleanupContainer(t, receiverC)
	if err != nil {
		t.Fatalf("start readiness receiver: %v", err)
	}
	return net, "https://receiver:9099", receiverC, testTLS
}

func getReadiness(ctx context.Context, client *http.Client, baseURL string) (int, readinessReport, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+readinessPath, nil)
	if err != nil {
		return 0, readinessReport{}, "", err
	}
	resp, err := client.Do(req)
	if err != nil {
		return 0, readinessReport{}, "", err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return resp.StatusCode, readinessReport{}, "", err
	}
	var report readinessReport
	if err := json.Unmarshal(body, &report); err != nil {
		return resp.StatusCode, readinessReport{}, string(body), fmt.Errorf("decode readiness response: %w", err)
	}
	return resp.StatusCode, report, string(body), nil
}

// tryDetailedReadiness fetches the authenticated status without failing the test, so a caller
// polling towards a deadline can absorb a transient blip instead of ending the run on it.
func tryDetailedReadiness(ctx context.Context, client *http.Client, baseURL string) (readinessReport, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+controlStatusPath, nil)
	if err != nil {
		return readinessReport{}, fmt.Errorf("create authenticated status request: %w", err)
	}
	req.SetBasicAuth("control", controlToken)
	resp, err := client.Do(req)
	if err != nil {
		return readinessReport{}, fmt.Errorf("get authenticated status: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return readinessReport{}, fmt.Errorf("read authenticated status: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return readinessReport{}, fmt.Errorf("authenticated status HTTP status = %d, want 200; body=%s", resp.StatusCode, body)
	}
	var status readinessStatus
	if err := json.Unmarshal(body, &status); err != nil {
		return readinessReport{}, fmt.Errorf("decode authenticated status: %w; body=%s", err, body)
	}
	return status.Readiness, nil
}

func getDetailedReadiness(t *testing.T, ctx context.Context, client *http.Client, baseURL string) readinessReport {
	t.Helper()
	report, err := tryDetailedReadiness(ctx, client, baseURL)
	if err != nil {
		t.Fatalf("%v", err)
	}
	return report
}

func assertDetailedReadinessRequiresAuth(t *testing.T, ctx context.Context, client *http.Client, baseURL string) {
	t.Helper()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+controlStatusPath, nil)
	if err != nil {
		t.Fatalf("create unauthenticated status request: %v", err)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("get unauthenticated status: %v", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read unauthenticated status: %v", err)
	}
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthenticated status HTTP status = %d, want 401; body=%s", resp.StatusCode, body)
	}
	for _, forbidden := range []string{`"readiness"`, `"lanes"`, `"reasons"`, `"error"`} {
		if strings.Contains(string(body), forbidden) {
			t.Fatalf("unauthenticated status exposed %s: %s", forbidden, body)
		}
	}
}

func waitForDetailedReadiness(
	t *testing.T,
	ctx context.Context,
	client *http.Client,
	baseURL string,
	done func(readinessReport) bool,
) readinessReport {
	t.Helper()
	deadline := time.Now().Add(readinessTimeout)
	var lastReport readinessReport
	var lastErr error
	for time.Now().Before(deadline) {
		report, err := tryDetailedReadiness(ctx, client, baseURL)
		if err != nil {
			// A transient error during container warm-up is expected; only a
			// persistent one reaches the deadline below, carrying its reason.
			lastErr = err
		} else {
			lastErr = nil
			lastReport = report
			if done(lastReport) {
				return lastReport
			}
		}
		select {
		case <-ctx.Done():
			t.Fatalf("wait for readiness: %v", ctx.Err())
		case <-time.After(500 * time.Millisecond):
		}
	}
	if lastErr != nil {
		t.Fatalf("detailed readiness never became readable within %s: %v", readinessTimeout, lastErr)
	}
	t.Fatalf("detailed readiness condition not met within %s: report=%+v", readinessTimeout, lastReport)
	return readinessReport{}
}

func assertBootstrapReady(t *testing.T, report readinessReport) {
	t.Helper()
	if !report.Running || !report.HTTPReady {
		t.Fatalf("process/HTTP bootstrap not ready: %+v", report)
	}
	if report.Blueprints.Loaded != 1 || report.Blueprints.Skipped != 0 || report.Blueprints.Active != 1 {
		t.Fatalf("unexpected blueprint readiness counts: %+v", report.Blueprints)
	}
	if !report.PersistedState.Writable {
		t.Fatalf("writable state path reported unavailable: %+v", report.PersistedState)
	}
}

func assertPublicReadinessSanitized(t *testing.T, report readinessReport, body string) {
	t.Helper()
	if len(report.Lanes) != 0 || len(report.Reasons) != 0 || report.PersistedState.Error != "" {
		t.Fatalf("public readiness exposed operational details: report=%+v body=%s", report, body)
	}
	for _, forbidden := range []string{`"lanes"`, `"reasons"`, `"error"`} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("public readiness exposed %s: %s", forbidden, body)
		}
	}
}

func assertRequiredReadinessLanes(t *testing.T, lanes []readinessLane) {
	t.Helper()
	want := []string{"loki", "otlp", "otlpmetrics", "promrw"}
	configured := make(map[string]bool, len(lanes))
	for _, lane := range lanes {
		if lane.Configured {
			configured[lane.Name] = true
		}
	}
	var missing []string
	for _, name := range want {
		if !configured[name] {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		t.Fatalf("required readiness lanes are absent or unconfigured: missing=%v lanes=%+v", missing, lanes)
	}
}

func allLanesDisabled(lanes []readinessLane) bool {
	if len(lanes) == 0 {
		return false
	}
	for _, lane := range lanes {
		if !lane.Configured || !lane.Disabled || lane.State != "disabled" || lane.LiveReady {
			return false
		}
	}
	return true
}

func hasNotAttemptedLane(lanes []readinessLane) bool {
	for _, lane := range lanes {
		if lane.Configured && !lane.Disabled && !lane.Attempted && lane.State == "not_attempted" {
			return true
		}
	}
	return false
}

func allLiveLanesReady(lanes []readinessLane) bool {
	live := 0
	for _, lane := range lanes {
		if !lane.Configured || lane.Disabled {
			continue
		}
		live++
		if !lane.Attempted || lane.State != "success" || !lane.LiveReady {
			return false
		}
	}
	return live > 0
}

func containsReadinessReason(reasons []string, want string) bool {
	for _, reason := range reasons {
		if strings.Contains(reason, want) {
			return true
		}
	}
	return false
}
