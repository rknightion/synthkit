// SPDX-License-Identifier: AGPL-3.0-only
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/pem"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/golang/snappy"
	"github.com/rknightion/synthkit/internal/config"
	"github.com/rknightion/synthkit/internal/control"
	"github.com/rknightion/synthkit/internal/ha"
	lease "github.com/rknightion/synthkit/internal/ha/kubernetes"
	"github.com/rknightion/synthkit/internal/sink/promrw/writev2"
	"github.com/rknightion/synthkit/internal/smstate"
	"github.com/rknightion/synthkit/internal/smstore"
	"github.com/rknightion/synthkit/internal/smstore/smstoretest"
	"google.golang.org/protobuf/proto"
	"gopkg.in/yaml.v3"
)

// Only Kubernetes transport is replaced: election, named CAS, handoff, runner and
// remote-write delivery use their production implementations.
func TestSMChartEmitterProcessHelper(t *testing.T) {
	p := os.Getenv("SM_CHART_CONFIG")
	if p == "" {
		return
	}
	data, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	var cfg config.Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatal(err)
	}
	endpoint := os.Getenv("SM_CHART_API")
	deps := productionHADependencies()
	deps.StateBackend = func(g ha.LeaderGate) control.StateBackend {
		b, err := control.NewKubernetesBackendForClient(control.KubernetesBackendOptions{Gate: g, Namespace: cfg.HANamespace, Objects: map[control.Key]string{control.Control: cfg.StateControlConfigMap, control.BootManifest: cfg.StateBootConfigMap, control.SMState: cfg.StateSMConfigMap}, RequestTimeout: cfg.HAKubeRequestTimeout, MaxDocumentBytes: control.MaxStateDocumentBytes}, endpoint, &http.Client{Timeout: cfg.HAKubeRequestTimeout})
		if err != nil {
			t.Fatal(err)
		}
		return b
	}
	deps.Election = func(ctx context.Context, o lease.Options) (leaseElection, error) {
		return lease.NewForClient(ctx, o, endpoint, &http.Client{Timeout: o.RequestTimeout})
	}
	if err := runHALifecycle(&cfg, true, make(chan struct{}), deps); err != nil {
		t.Fatal(err)
	}
}

func TestSMChartRenderedHAProvisionAndDelivery(t *testing.T) {
	remote := smstoretest.NewRemote(t)
	defer remote.Close()
	var mu sync.Mutex
	receipts := []map[string]string{}
	receiver := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/prom/push" && r.Method == "POST" {
			data, err := io.ReadAll(r.Body)
			if err != nil {
				t.Error(err)
				w.WriteHeader(400)
				return
			}
			raw, err := snappy.Decode(nil, data)
			if err != nil {
				t.Error(err)
				w.WriteHeader(400)
				return
			}
			var req writev2.Request
			if err := proto.Unmarshal(raw, &req); err != nil {
				t.Error(err)
				w.WriteHeader(400)
				return
			}
			mu.Lock()
			defer mu.Unlock()
			for _, series := range req.Timeseries {
				labels := map[string]string{}
				for i := 0; i < len(series.LabelsRefs); i += 2 {
					labels[req.Symbols[series.LabelsRefs[i]]] = req.Symbols[series.LabelsRefs[i+1]]
				}
				if labels["__name__"] == "sm_check_info" && len(series.Samples) > 0 {
					receipts = append(receipts, labels)
				}
			}
		}
		w.WriteHeader(200)
	}))
	defer receiver.Close()
	certPath := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: receiver.Certificate().Raw}), 0600); err != nil {
		t.Fatal(err)
	}
	chart, err := filepath.Abs("../../charts/synthkit")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	args := []string{"template", "synthkit-test", chart, "--namespace", "test", "--kube-version", "1.31.0", "-f", filepath.Join(chart, "tests", "sm-kubernetes-values.yaml"), "--set", "ha.enabled=true,ha.leaseName=emitter,ha.controlConfigMap=control,ha.bootConfigMap=boot,config.blueprintNames=sm-test,smProvision.apply=true"}
	out, err := exec.CommandContext(ctx, "helm", args...).CombinedOutput()
	if err != nil {
		t.Fatalf("render: %v %s", err, out)
	}
	decoder := yaml.NewDecoder(bytes.NewReader(out))
	var objects []map[string]any
	for {
		var o map[string]any
		err := decoder.Decode(&o)
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		objects = append(objects, o)
	}
	secrets := map[string]string{"GC_SM_URL": remote.Server.URL, "GC_SM_TOKEN": smstoretest.Token(t)}
	resolve := func(env any, uid string) {
		for _, entry := range env.([]any) {
			e := entry.(map[string]any)
			name := e["name"].(string)
			if v, ok := e["value"].(string); ok {
				t.Setenv(name, v)
				continue
			}
			from := e["valueFrom"].(map[string]any)
			if from["secretKeyRef"] != nil {
				v, ok := secrets[name]
				if !ok {
					t.Fatalf("unexpected credential %s", name)
				}
				t.Setenv(name, v)
			} else {
				field := from["fieldRef"].(map[string]any)["fieldPath"]
				switch field {
				case "metadata.namespace":
					t.Setenv(name, "test")
				case "metadata.uid":
					t.Setenv(name, uid)
				default:
					t.Fatalf("unknown field %v", field)
				}
			}
		}
	}
	var emitterEnv, jobEnv any
	for _, o := range objects {
		if o["kind"] == "ConfigMap" {
			for k, v := range o["data"].(map[string]any) {
				t.Setenv(k, v.(string))
			}
		}
		if o["kind"] == "Deployment" || o["kind"] == "Job" {
			pod := o["spec"].(map[string]any)["template"].(map[string]any)["spec"].(map[string]any)
			env := pod["containers"].([]any)[0].(map[string]any)["env"]
			if o["kind"] == "Deployment" {
				emitterEnv = env
			} else {
				jobEnv = env
			}
		}
	}
	resolve(emitterEnv, "emitter-pod")
	cfg, err := config.Load("/dev/null")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.StateSMConfigMap != "sm" || cfg.SMProvisionLeaseName != "provision" {
		t.Fatal("rendered emitter lacks named SM contract")
	}
	api := smstoretest.New(t, cfg.StateSMConfigMap, cfg.StateControlConfigMap, cfg.StateBootConfigMap)
	defer api.Close()
	root := t.TempDir()
	cfg.BlueprintsDir = filepath.Join(root, "baked")
	cfg.BlueprintDataDir = filepath.Join(root, "absent-data")
	cfg.SnapshotPath = "/dev/null/no-shared-file.json"
	if err := os.Mkdir(cfg.BlueprintsDir, 0700); err != nil {
		t.Fatal(err)
	}
	fixture := []byte("name: sm-test\nhosts: [{name: sample, os: linux}]\nfeatures:\n  synthetic_monitoring:\n    enabled: true\n    checks:\n      - {name: api-health, target: 'https://api.example/health', labels: {team: platform}}\n")
	if err := os.WriteFile(filepath.Join(cfg.BlueprintsDir, "sm.yaml"), fixture, 0600); err != nil {
		t.Fatal(err)
	}
	// Local sink endpoints replace deployment destinations; state identity stays rendered.
	cfg.SelfObsEnabled = false
	cfg.FMURL, cfg.SigilEndpoint, cfg.FaroCollector, cfg.ProfilesURL = "", "", "", ""
	cfg.DryRun = false
	cfg.PromRWURL = receiver.URL + "/api/prom/push"
	cfg.PromUser = "1"
	cfg.Token = smstoretest.Token(t)
	cfg.LokiURL = receiver.URL + "/loki/api/v1/push"
	cfg.LokiUser = "1"
	cfg.OTLPEndpoint = receiver.URL + "/otlp"
	cfg.OTLPUser = "1"
	runEmitter := func() {
		t.Helper()
		api.ReplaceLease(cfg.HALeaseName)
		api.Permit()
		l, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		cfg.HTTPAddr = l.Addr().String()
		l.Close()
		p := filepath.Join(t.TempDir(), "config.json")
		writeHAJSON(t, p, cfg)
		exe, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, exe, "-test.run=^TestSMChartEmitterProcessHelper$")
		cmd.Env = []string{"SM_CHART_CONFIG=" + p, "SM_CHART_API=" + api.Server.URL, "SSL_CERT_FILE=" + certPath, "GORACE=atexit_sleep_ms=0"}
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("emitter: %v %s", err, out)
		}
	}
	runEmitter()
	mu.Lock()
	before := len(receipts)
	mu.Unlock()
	if before != 0 {
		t.Fatal("unregistered emitter delivered SM")
	}
	resolve(jobEnv, "job-pod")
	pc, err := config.LoadSMProvision()
	if err != nil {
		t.Fatal(err)
	}
	if pc.StateSMConfigMap != cfg.StateSMConfigMap || pc.ProvisionLeaseName != cfg.SMProvisionLeaseName || pc.EmitterLeaseName != cfg.HALeaseName {
		t.Fatal("rendered Job/emitter identities differ")
	}
	binary := filepath.Join(t.TempDir(), "provision")
	ctx2, cancel2 := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel2()
	build := exec.CommandContext(ctx2, "go", "test", "-c", "-o", binary, "../sm-provision")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build provisioner %v %s", err, out)
	}
	data, err := json.Marshal(pc)
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(p, data, 0600); err != nil {
		t.Fatal(err)
	}
	api.ReplaceLease(pc.ProvisionLeaseName)
	api.Permit()
	ctx3, cancel3 := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel3()
	cmd := exec.CommandContext(ctx3, binary, "-test.run=^TestKubernetesProvisionProcessHelper$")
	cmd.Env = []string{"SYNTHKIT_SM_PROOF_CONFIG=" + p, "SYNTHKIT_SM_PROOF_API=" + api.Server.URL, "GORACE=atexit_sleep_ms=0"}
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("provisioner %v %s", err, out)
	}
	var doc smstore.Document
	if err := json.Unmarshal(api.Data(cfg.StateSMConfigMap), &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Snapshot == nil || doc.Provision.Registration == nil {
		t.Fatal("no persisted registration")
	}
	if err := smstate.ValidateRegistration(*doc.Snapshot, *doc.Provision.Registration); err != nil {
		t.Fatal(err)
	}
	runEmitter()
	mu.Lock()
	defer mu.Unlock()
	if len(receipts) == 0 {
		t.Fatal("no real RW2 SM receipt")
	}
	expected := ""
	for _, r := range doc.Provision.Registration.Resources {
		if r.Kind == "check" {
			expected = r.ConfigVersion
		}
	}
	for _, labels := range receipts {
		if expected == "" || labels["config_version"] != expected || labels["job"] != doc.Snapshot.Checks[0].Job || labels["instance"] != doc.Snapshot.Checks[0].Target {
			t.Fatalf("receipt config_version=%q expected=%q", labels["config_version"], expected)
		}
	}
	if _, err := os.Stat(cfg.BlueprintDataDir); !os.IsNotExist(err) {
		t.Fatal("diskless runtime materialized state")
	}
	t.Logf("rendered Job/emitter -> real parsers -> production HA snapshot -> fake SM applied check -> named state -> restarted HA emitter -> %d decoded RW2 receipts config_version=%s", len(receipts), expected)
}
