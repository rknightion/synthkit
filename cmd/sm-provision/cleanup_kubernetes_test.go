// SPDX-License-Identifier: AGPL-3.0-only
package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/rknightion/synthkit/internal/config"
	"github.com/rknightion/synthkit/internal/control"
	"github.com/rknightion/synthkit/internal/ha"
	lease "github.com/rknightion/synthkit/internal/ha/kubernetes"
	"github.com/rknightion/synthkit/internal/smstate"
	"github.com/rknightion/synthkit/internal/smstore"
	"github.com/rknightion/synthkit/internal/smstore/smstoretest"
)

// TestKubernetesCleanupProcessHelper is the diskless CLI with injected local named Kubernetes and
// SM HTTP endpoints; the arguments go through the same parser as the real command line.
func TestKubernetesCleanupProcessHelper(t *testing.T) {
	path := os.Getenv("SYNTHKIT_SM_CLEANUP_PROOF_CONFIG")
	if path == "" {
		return
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var cfg config.SMProvisionConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatal(err)
	}
	var args []string
	if err := json.Unmarshal([]byte(os.Getenv("SYNTHKIT_SM_CLEANUP_PROOF_ARGS")), &args); err != nil {
		t.Fatal(err)
	}
	inv, err := parseInvocation(args)
	if err != nil {
		t.Fatal(err)
	}
	endpoint := os.Getenv("SYNTHKIT_SM_CLEANUP_PROOF_API")
	deps := productionProvisionDependencies()
	deps.cleanup = inv.cleanup
	observer := &operationObserver{}
	deps.HTTPClient = &http.Client{Transport: observedTransport{observer: observer}}
	deps.Backend = func(g ha.LeaderGate) (control.StateBackend, error) {
		return control.NewKubernetesBackendForClient(control.KubernetesBackendOptions{Gate: g, Namespace: cfg.Namespace, Objects: map[control.Key]string{control.SMState: cfg.StateSMConfigMap}, RequestTimeout: cfg.KubeRequestTimeout, MaxDocumentBytes: control.MaxStateDocumentBytes}, endpoint, &http.Client{})
	}
	deps.Election = func(ctx context.Context, o lease.Options) (provisionElection, error) {
		e, err := lease.NewForClient(ctx, o, endpoint, &http.Client{})
		if err != nil {
			return nil, err
		}
		return observedElection{e, observer}, nil
	}
	if err := runWithDependencies(context.Background(), cfg, deps); err != nil {
		os.Stderr.WriteString(err.Error() + "\n")
		os.Exit(2)
	}
	os.Exit(0)
}

func startKubeCleanup(t *testing.T, a *smstoretest.API, cfg config.SMProvisionConfig, args ...string) *provisionProcess {
	t.Helper()
	dir, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(cfg)
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(args)
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	p := &provisionProcess{done: make(chan error, 1), cancel: cancel}
	p.cmd = exec.CommandContext(ctx, exe, "-test.run=^TestKubernetesCleanupProcessHelper$")
	p.cmd.Env = append(os.Environ(), "SYNTHKIT_SM_CLEANUP_PROOF_CONFIG="+path, "SYNTHKIT_SM_CLEANUP_PROOF_API="+a.Server.URL,
		"SYNTHKIT_SM_CLEANUP_PROOF_ARGS="+string(encoded), "GORACE="+os.Getenv("GORACE")+" atexit_sleep_ms=0")
	p.cmd.Stdout, p.cmd.Stderr = &p.output, &p.output
	if err := p.cmd.Start(); err != nil {
		cancel()
		t.Fatal(err)
	}
	go func() { p.done <- p.cmd.Wait() }()
	t.Cleanup(cancel)
	return p
}

func namedDoc(t *testing.T, a *smstoretest.API) smstore.Document {
	t.Helper()
	var d smstore.Document
	if err := json.Unmarshal(a.Data("sm"), &d); err != nil {
		t.Fatal(err)
	}
	return d
}

func leaseReads(a *smstoretest.API) int {
	a.Mu.Lock()
	defer a.Mu.Unlock()
	n := 0
	for _, r := range a.Requests {
		if strings.Contains(r, "GET /apis/coordination") {
			n++
		}
	}
	return n
}

// provisionKube performs a real diskless apply and returns the cleanup preview plan.
func provisionKube(t *testing.T) (*smstoretest.API, *cleanupSM, config.SMProvisionConfig, string) {
	t.Helper()
	a := smstoretest.New(t, "sm")
	t.Cleanup(a.Close)
	cs := newCleanupSM()
	server := httptest.NewServer(cs)
	t.Cleanup(server.Close)
	cfg := kubeProvisionConfig(t, server.URL)
	seedSM(t, a, cfg)
	a.Permit()
	waitProvision(t, startProvision(t, a, cfg), true)
	cfg.Apply = false
	preview := receiptOf(t, waitProvision(t, startKubeCleanup(t, a, cfg, "cleanup"), true))
	if len(preview.Resources) != 2 {
		t.Fatalf("preview = %+v", preview)
	}
	cfg.Apply = true
	return a, cs, cfg, preview.Plan
}

func TestKubernetesCleanupActsOnlyUnderProvisionLeaseWithDurableIntent(t *testing.T) {
	a, cs, cfg, plan := provisionKube(t)
	intentBeforeDelete := 0
	cs.set(func(c *cleanupSM) {
		c.onDelete = func(http.ResponseWriter, *http.Request, string, int64) bool {
			d := namedDoc(t, a)
			if d.Provision.Pending != nil && d.Provision.Pending.Operation == smstate.DeleteOperation && d.Provision.Registration == nil {
				intentBeforeDelete++
			} else {
				t.Error("DELETE before durable intent and activation removal")
			}
			return false
		}
	})
	a.ReplaceLease("provision") // another holder owns the provisioning Lease
	reads := leaseReads(a)
	a.Mu.Lock()
	writes, releases := a.StateWrites, a.Releases
	a.Mu.Unlock()
	_, requests := cs.observed()
	p := startKubeCleanup(t, a, cfg, "cleanup", "-confirm="+plan)
	awaitNamedLeaseRead(t, a, reads)
	a.Mu.Lock()
	preWrites := a.StateWrites
	a.Mu.Unlock()
	if _, now := cs.observed(); now != requests || preWrites != writes {
		t.Fatalf("cleanup acted before Lease acquisition: SM=%d state=%d", now-requests, preWrites-writes)
	}
	a.Permit()
	out := waitProvision(t, p, true)
	if !strings.Contains(out, "joined-before-release=true") {
		t.Fatalf("no joined release proof:\n%s", out)
	}
	receipt := receiptOf(t, out)
	intents := 0
	cs.set(func(*cleanupSM) { intents = intentBeforeDelete }) // joins the handler's writes
	if receipt.Status != "complete" || intents != 2 {
		t.Fatalf("receipt=%+v intents=%d", receipt, intents)
	}
	d := namedDoc(t, a)
	if d.Provision.Pending != nil || d.Provision.Registration != nil || d.Provision.Ownership == nil || len(d.Provision.Ownership.Resources) != 0 || d.Snapshot == nil {
		t.Fatalf("final custody = %+v", d.Provision)
	}
	a.Mu.Lock()
	defer a.Mu.Unlock()
	if a.Releases != releases+1 {
		t.Fatal("planned cleanup completion did not release the Lease")
	}
}

func TestKubernetesCleanupEmitterRepublicationStopsBeforeDelete(t *testing.T) {
	a, cs, cfg, plan := provisionKube(t)
	var republished smstate.Snapshot
	cs.set(func(c *cleanupSM) {
		c.checkLists = 0
		c.onList = func(n int) {
			if n != 2 { // republish between the plan inventory and the pre-delete revalidation
				return
			}
			d := namedDoc(t, a)
			checks := append([]smstate.CheckSpec(nil), d.Snapshot.Checks...)
			checks[0].FrequencyMS = 30000
			next, err := smstate.NewSnapshot(d.Snapshot.Probes, checks, cfg.SMURL, cfg.SMToken, version, time.Now())
			if err != nil {
				t.Error(err)
				return
			}
			republished, d.Snapshot = next, &next
			b, _ := json.Marshal(d)
			a.Set("sm", b)
		}
	})
	out := waitProvision(t, startKubeCleanup(t, a, cfg, "cleanup", "-confirm="+plan), false)
	if !strings.Contains(out, "no deletion was attempted") {
		t.Fatalf("output:\n%s", out)
	}
	if deletes, _ := cs.observed(); len(deletes) != 0 {
		t.Fatalf("deleted across a changed emitter snapshot: %v", deletes)
	}
	var republishedHash string
	cs.set(func(*cleanupSM) { republishedHash = republished.SnapshotHash })
	d := namedDoc(t, a)
	if republishedHash == "" || d.Provision.Pending != nil || len(d.Provision.Ownership.Resources) != 2 || d.Snapshot == nil || d.Snapshot.SnapshotHash != republishedHash {
		t.Fatalf("custody or emitter snapshot changed: %+v", d)
	}
}

func TestKubernetesCleanupTerminationDuringDeleteRetainsRecoveryCustody(t *testing.T) {
	a, cs, cfg, plan := provisionKube(t)
	reached := make(chan struct{})
	cs.set(func(c *cleanupSM) {
		c.onDelete = func(_ http.ResponseWriter, r *http.Request, kind string, _ int64) bool {
			if kind != "check" {
				return false
			}
			close(reached)
			c.onDelete = nil
			c.mu.Unlock()
			<-r.Context().Done()
			c.mu.Lock()
			return true
		}
	})
	a.Mu.Lock()
	releases := a.Releases
	a.Mu.Unlock()
	p := startKubeCleanup(t, a, cfg, "cleanup", "-confirm="+plan)
	select {
	case <-reached:
	case <-time.After(8 * time.Second):
		t.Fatal("no real DELETE observed")
	}
	if err := p.cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	out := waitProvision(t, p, false)
	if receipt := receiptOf(t, out); receipt.Status != "unknown" {
		t.Fatalf("receipt = %+v", receipt)
	}
	d := namedDoc(t, a)
	if d.Provision.Pending == nil || d.Provision.Pending.Operation != smstate.DeleteOperation || d.Provision.Pending.Kind != "check" || d.Provision.Registration != nil {
		t.Fatalf("interrupted cleanup lost custody: %+v", d.Provision)
	}
	a.Mu.Lock()
	if a.Releases != releases+1 {
		a.Mu.Unlock()
		t.Fatal("planned termination did not join and release")
	}
	a.Mu.Unlock()
	cfg.Apply = false
	recovery := receiptOf(t, waitProvision(t, startKubeCleanup(t, a, cfg, "cleanup"), true))
	cfg.Apply = true
	final := receiptOf(t, waitProvision(t, startKubeCleanup(t, a, cfg, "cleanup", "-confirm="+recovery.Plan), true))
	if final.Status != "complete" {
		t.Fatalf("recovery = %+v", final)
	}
	deletes, _ := cs.observed()
	if len(deletes) != 3 || !strings.HasPrefix(deletes[0], "check/") || deletes[0] != deletes[1] {
		t.Fatalf("deletes = %v", deletes)
	}
	if d := namedDoc(t, a); d.Provision.Pending != nil || len(d.Provision.Ownership.Resources) != 0 {
		t.Fatalf("recovered custody = %+v", d.Provision)
	}
}

func TestKubernetesCleanupLeaseLossDuringDeleteHardExitsWithCustody(t *testing.T) {
	a, cs, cfg, plan := provisionKube(t)
	// The same loss budget as the provisioning loss proof: the operation plus margin fits in the gap.
	cfg.LeaseDuration, cfg.RenewDeadline, cfg.RetryPeriod = 4*time.Second, 300*time.Millisecond, 50*time.Millisecond
	cfg.KubeRequestTimeout, cfg.HTTPTimeout, cfg.OperationTimeout, cfg.FenceMargin = 100*time.Millisecond, time.Second, time.Second, 200*time.Millisecond
	reached := make(chan struct{})
	cs.set(func(c *cleanupSM) {
		c.onDelete = func(_ http.ResponseWriter, r *http.Request, kind string, _ int64) bool {
			close(reached)
			c.onDelete = nil
			c.mu.Unlock()
			<-r.Context().Done()
			c.mu.Lock()
			return true
		}
	})
	a.Mu.Lock()
	releases := a.Releases
	a.Mu.Unlock()
	p := startKubeCleanup(t, a, cfg, "cleanup", "-confirm="+plan)
	select {
	case <-reached:
	case <-time.After(8 * time.Second):
		t.Fatal("no real DELETE observed")
	}
	a.Mu.Lock()
	a.LoseLease = true
	a.Mu.Unlock()
	out := waitProvision(t, p, false)
	if strings.Contains(out, `"status":"complete"`) {
		t.Fatalf("lease loss reported completion:\n%s", out)
	}
	d := namedDoc(t, a)
	if d.Provision.Pending == nil || d.Provision.Pending.Operation != smstate.DeleteOperation || d.Provision.Registration != nil || len(d.Provision.Ownership.Resources) != 2 {
		t.Fatalf("lease loss lost custody: %+v", d.Provision)
	}
	deletes, _ := cs.observed()
	a.Mu.Lock()
	defer a.Mu.Unlock()
	if a.Releases != releases || len(deletes) != 1 {
		t.Fatalf("lost authority released or admitted more work: releases=%d deletes=%v", a.Releases-releases, deletes)
	}
}
