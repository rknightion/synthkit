// SPDX-License-Identifier: AGPL-3.0-only

//go:build e2e

package e2e

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/network"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/rknightion/synthkit/e2e/inventory"
)

var publishedImageRefRE = regexp.MustCompile(`^ghcr\.io/rknightion/synthkit@sha256:[0-9a-f]{64}$`)
var publishedRevisionRE = regexp.MustCompile(`^[0-9a-f]{40}$`)

const publishedStateHelper = "node:24-alpine@sha256:d32cdf619f63fe0471182d08996dd516c6275bb5fd31ae06e55a570bd9e1ad43"

// TestPublishedCompose exercises the committed Compose artifact with a published immutable
// image. It is intentionally opt-in: the ordinary e2e test builds locally, while this test is
// used by a release gate after the exact published digest and expected metadata are known.
func TestPublishedCompose(t *testing.T) {
	imageRef := os.Getenv("SYNTHKIT_PUBLISHED_IMAGE_REF")
	if imageRef == "" {
		t.Skip("SYNTHKIT_PUBLISHED_IMAGE_REF is not set")
	}
	if !publishedImageRefRE.MatchString(imageRef) {
		t.Fatalf("SYNTHKIT_PUBLISHED_IMAGE_REF must be an immutable GHCR digest reference, got %q", imageRef)
	}
	expectedVersion := os.Getenv("SYNTHKIT_EXPECTED_VERSION")
	expectedRevision := os.Getenv("SYNTHKIT_EXPECTED_REVISION")
	if expectedVersion == "" || !publishedRevisionRE.MatchString(expectedRevision) {
		t.Fatalf("SYNTHKIT_EXPECTED_VERSION and a 40-hex SYNTHKIT_EXPECTED_REVISION are required")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	tlsFiles := newTestTLS(t)
	net, receiverURL, receiver := startPublishedReceiver(t, ctx, tlsFiles)

	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatalf("resolve checkout: %v", err)
	}
	tmp := t.TempDir()
	stateDir := filepath.Join(tmp, "state")
	if err := os.Mkdir(stateDir, 0o700); err != nil {
		t.Fatalf("create isolated state directory: %v", err)
	}
	// GitHub-hosted runners cannot chown the bind directory directly. Initialize it through a
	// pinned helper container so the production distroless uid can exercise real writable state.
	runDocker(t, ctx, root, "run", "--rm", "--volume", stateDir+":/data", "--entrypoint", "/bin/sh", publishedStateHelper, "-ceu", "chown 65532:65532 /data; chmod 0700 /data")
	envFile := filepath.Join(tmp, "fake.env")
	if err := os.WriteFile(envFile, []byte(publishedComposeEnv(receiverURL)), 0o600); err != nil {
		t.Fatalf("write fake env file: %v", err)
	}
	override := filepath.Join(tmp, "published.override.yml")
	if err := os.WriteFile(override, []byte(publishedComposeOverride(imageRef, net.Name, stateDir, tlsFiles.caPath)), 0o600); err != nil {
		t.Fatalf("write Compose override: %v", err)
	}

	project := fmt.Sprintf("synthkit-published-%d", time.Now().UnixNano())
	compose := func(args ...string) []byte {
		base := []string{"compose", "--project-name", project, "--env-file", envFile, "-f", filepath.Join(root, "docker-compose.yml"), "-f", override}
		cmd := exec.CommandContext(ctx, "docker", append(base, args...)...)
		cmd.Dir = root
		cmd.Env = append(os.Environ(), "SYNTHKIT_ENV_FILE="+envFile, "SYNTHKIT_IMAGE_REF="+imageRef)
		var stdout, stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		err := cmd.Run()
		if err != nil {
			t.Fatalf("docker %s: %v\nstdout:\n%s\nstderr:\n%s", strings.Join(args, " "), err, stdout.Bytes(), stderr.Bytes())
		}
		return stdout.Bytes()
	}
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cleanupCancel()
		cmd := exec.CommandContext(cleanupCtx, "docker", "compose", "--project-name", project, "--env-file", envFile, "-f", filepath.Join(root, "docker-compose.yml"), "-f", override, "down", "--volumes", "--remove-orphans")
		cmd.Dir = root
		cmd.Env = append(os.Environ(), "SYNTHKIT_ENV_FILE="+envFile, "SYNTHKIT_IMAGE_REF="+imageRef)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Errorf("published Compose cleanup: %v\n%s", err, out)
		}
		cmd = exec.CommandContext(cleanupCtx, "docker", "run", "--rm", "--volume", stateDir+":/data", "--entrypoint", "/bin/sh", publishedStateHelper, "-ceu", "rm -rf /data/* /data/.[!.]* /data/..?*; chmod 0777 /data")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Errorf("published state container cleanup: %v\n%s", err, out)
			return
		}
		if err := os.RemoveAll(stateDir); err != nil {
			t.Errorf("published state host cleanup: %v", err)
		}
	})

	compose("config", "--quiet")
	images := strings.Fields(string(compose("--profile", "sm-provision", "config", "--images")))
	if len(images) != 2 || images[0] != imageRef || images[1] != imageRef {
		t.Fatalf("Compose image identities = %v, want the exact digest for both services", images)
	}
	compose("up", "-d", "--wait")
	containerID := strings.TrimSpace(string(compose("ps", "-q", "synthkit")))
	if containerID == "" {
		t.Fatal("Compose returned no synthkit container")
	}
	configured := strings.TrimSpace(string(runDocker(t, ctx, root, "inspect", "-f", "{{.Config.Image}}", containerID)))
	if configured != imageRef {
		t.Fatalf("running image %q does not equal requested digest %q", configured, imageRef)
	}
	health := strings.TrimSpace(string(runDocker(t, ctx, root, "inspect", "-f", "{{.State.Health.Status}}", containerID)))
	if health != "healthy" {
		t.Fatalf("running Compose container health = %q, want healthy", health)
	}

	versionRaw := compose("exec", "-T", "synthkit", "/app/synthkit", "-version")
	var got struct {
		Version  string `json:"version"`
		Revision string `json:"revision"`
	}
	if err := json.Unmarshal(versionRaw, &got); err != nil {
		t.Fatalf("decode -version JSON %q: %v", versionRaw, err)
	}
	if got.Version != expectedVersion || got.Revision != expectedRevision {
		t.Fatalf("running version metadata = version %q revision %q, want %q %q", got.Version, got.Revision, expectedVersion, expectedRevision)
	}

	port := strings.TrimSpace(string(compose("port", "synthkit", "8088")))
	port = strings.TrimSpace(strings.TrimPrefix(port, "127.0.0.1:"))
	client := &http.Client{Timeout: 15 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12}}}
	resp, err := client.Get("http://127.0.0.1:" + port + "/control/readiness")
	if err != nil {
		t.Fatalf("readiness request: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("readiness status = %d, want 200", resp.StatusCode)
	}
	var readiness struct {
		Ready          bool `json:"ready"`
		LiveReady      bool `json:"live_ready"`
		PersistedState struct {
			Writable bool `json:"writable"`
		} `json:"persisted_state"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&readiness); err != nil {
		t.Fatalf("decode readiness: %v", err)
	}
	if !readiness.Ready || !readiness.LiveReady || !readiness.PersistedState.Writable {
		t.Fatalf("published Compose readiness/state not green: %+v", readiness)
	}

	// Continuous operation phase-spreads each instance's first emission across its declared
	// interval. After proving the committed service healthcheck and writable-state contract,
	// quiesce it and use the same Compose service for one complete deterministic emission.
	compose("stop", "synthkit")
	compose("run", "--rm", "--no-deps", "synthkit", "-once")

	received := fetchPublishedInventory(t, ctx, receiver, tlsFiles)
	expected := dumpSchema(t)
	if missing := expected.Subset(received); len(missing) != 0 {
		t.Fatalf("published Compose image missing intended receipts (%d):\n  %s", len(missing), strings.Join(missing, "\n  "))
	}
	if len(received.Metrics) == 0 || len(received.LogSources) == 0 || len(received.Traces) == 0 || len(received.Sigil) == 0 {
		t.Fatalf("receiver did not positively decode every configured telemetry lane: metrics=%d logs=%d traces=%d sigil=%d", len(received.Metrics), len(received.LogSources), len(received.Traces), len(received.Sigil))
	}
	for _, protocol := range []string{"promrw", "otlp_metrics", "otlp_traces", "loki", "sigil_generations", "sigil_workflow_steps", "sigil_scores"} {
		if received.Receipts[protocol] == 0 {
			t.Fatalf("receiver has no successfully decoded non-empty %s receipt: %v", protocol, received.Receipts)
		}
	}
}

func startPublishedReceiver(t *testing.T, ctx context.Context, files generatedTLS) (*testcontainers.DockerNetwork, string, testcontainers.Container) {
	t.Helper()
	net, err := network.New(ctx)
	if err != nil {
		t.Fatalf("create receiver network: %v", err)
	}
	testcontainers.CleanupNetwork(t, net)
	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{ContainerRequest: testcontainers.ContainerRequest{
		FromDockerfile: testcontainers.FromDockerfile{Context: "..", Dockerfile: "e2e/receiver/Dockerfile", KeepImage: true},
		ExposedPorts:   []string{"9099/tcp"},
		Env:            map[string]string{"RECEIVER_TLS_CERT_FILE": "/tmp/receiver.crt", "RECEIVER_TLS_KEY_FILE": "/tmp/receiver.key"},
		Files:          []testcontainers.ContainerFile{{HostFilePath: files.certPath, ContainerFilePath: "/tmp/receiver.crt", FileMode: 0o644}, {HostFilePath: files.keyPath, ContainerFilePath: "/tmp/receiver.key", FileMode: 0o604}},
		Networks:       []string{net.Name}, NetworkAliases: map[string][]string{net.Name: {"receiver"}},
		WaitingFor: wait.ForListeningPort("9099/tcp").WithStartupTimeout(2 * time.Minute),
	}, Started: true})
	if err != nil {
		t.Fatalf("start receiver: %v", err)
	}
	testcontainers.CleanupContainer(t, c)
	return net, "https://receiver:9099", c
}

func fetchPublishedInventory(t *testing.T, ctx context.Context, receiver testcontainers.Container, files generatedTLS) inventory.Schema {
	t.Helper()
	host, err := receiver.Host(ctx)
	if err != nil {
		t.Fatalf("receiver host: %v", err)
	}
	port, err := receiver.MappedPort(ctx, "9099/tcp")
	if err != nil {
		t.Fatalf("receiver port: %v", err)
	}
	client := &http.Client{Timeout: 30 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: files.roots, ServerName: "receiver"}}}
	resp, err := client.Get(fmt.Sprintf("https://%s:%s/__inventory", host, port.Port()))
	if err != nil {
		t.Fatalf("receiver inventory: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("receiver inventory status = %d", resp.StatusCode)
	}
	var got inventory.Schema
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode receiver inventory: %v", err)
	}
	return got
}

func runDocker(t *testing.T, ctx context.Context, dir string, args ...string) []byte {
	t.Helper()
	cmd := exec.CommandContext(ctx, "docker", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("docker %s: %v\n%s", strings.Join(args, " "), err, out)
	}
	return out
}

func publishedComposeEnv(receiverURL string) string {
	return "DRY_RUN=false\nBLUEPRINT_NAMES=otlp-native\nJSON_HTTP_ADDR=0.0.0.0:8088\nSYNTHKIT_BIND=127.0.0.1\nSSL_CERT_FILE=/tmp/e2e-ca.crt\nGC_TOKEN=e2e\nGC_PROM_RW=" + receiverURL + "/api/prom/push\nGC_PROM_USER=1\nGC_OTLP_ENDPOINT=" + receiverURL + "/otlp\nGC_OTLP_USER=2\nGC_LOKI=" + receiverURL + "/loki/api/v1/push\nGC_LOKI_USER=3\nGC_SIGIL_ENDPOINT=" + receiverURL + "\nGC_SIGIL_TENANT_ID=e2e\nGC_SIGIL_TOKEN=e2e\n"
}

func publishedComposeOverride(image, networkName, stateDir, caPath string) string {
	return fmt.Sprintf("services:\n  synthkit:\n    image: %s\n    networks: [published-test]\n    ports: !override [\"127.0.0.1::8088\"]\n    volumes:\n      - %s:/data\n      - %s:/tmp/e2e-ca.crt:ro\nnetworks:\n  published-test:\n    external: true\n    name: %s\n", image, stateDir, caPath, networkName)
}
