// SPDX-License-Identifier: AGPL-3.0-only
package main

import (
	"context"
	"errors"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/rknightion/synthkit/internal/config"
	"github.com/rknightion/synthkit/internal/control"
	"github.com/rknightion/synthkit/internal/ha"
	lease "github.com/rknightion/synthkit/internal/ha/kubernetes"
	"github.com/rknightion/synthkit/internal/smstate"
	"github.com/rknightion/synthkit/internal/smstore"
)

const provisionAcquireTimeout = 2 * time.Minute
const provisionInvocationTimeout = 10 * time.Minute

type provisionElection interface {
	Run(context.Context)
	Seal(context.Context) error
	Release(context.Context) error
}
type provisionDependencies struct {
	Backend    func(ha.LeaderGate) (control.StateBackend, error)
	Election   func(context.Context, lease.Options) (provisionElection, error)
	HTTPClient *http.Client
	Exit       func(int)
	Clock      func() time.Time
	client     *smClient
}

func productionProvisionDependencies() provisionDependencies {
	return provisionDependencies{Election: func(ctx context.Context, o lease.Options) (provisionElection, error) { return lease.New(ctx, o) }, Exit: os.Exit, Clock: time.Now}
}
func runWithDependencies(ctx context.Context, cfg config.SMProvisionConfig, deps provisionDependencies) (err error) {
	if err := cfg.Validate(); err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()
	ctx, cancel := context.WithTimeout(ctx, provisionInvocationTimeout)
	defer cancel()
	if deps.Clock == nil {
		deps.Clock = time.Now
	}
	if deps.Exit == nil {
		deps.Exit = os.Exit
	}
	timeout := 30 * time.Second
	if cfg.StateBackend == "kubernetes" {
		timeout = cfg.HTTPTimeout
	}
	hc := deps.HTTPClient
	if hc == nil {
		hc = &http.Client{Timeout: timeout}
	} else {
		copy := *hc
		copy.Timeout = timeout
		hc = &copy
	}
	deps.client = &smClient{base: strings.TrimRight(cfg.SMURL, "/"), token: cfg.SMToken, hc: hc}
	if cfg.StateBackend == "file" {
		dir := smstate.RuntimeDir(cfg.SnapshotPath)
		store, release, acquireErr := smstore.AcquireFileStore(dir)
		if acquireErr != nil {
			return errors.New("private state lock is unavailable")
		}
		defer func() { err = errors.Join(err, release()) }()
		return executeStored(ctx, cfg, store, dir, deps)
	}
	gate := ha.NewGate()
	crash := func() { gate.Revoke(); deps.Exit(1); panic("SM provisioning terminal exit returned") }
	bound := ha.Bounded{Gate: gate, Timeout: cfg.OperationTimeout, Margin: cfg.FenceMargin, Crash: crash}
	deps.client.bound = &bound
	backendFactory := deps.Backend
	if backendFactory == nil {
		backendFactory = func(g ha.LeaderGate) (control.StateBackend, error) {
			return control.NewKubernetesBackend(control.KubernetesBackendOptions{Gate: g, Namespace: cfg.Namespace, Objects: map[control.Key]string{control.SMState: cfg.StateSMConfigMap}, RequestTimeout: cfg.KubeRequestTimeout, MaxDocumentBytes: control.MaxStateDocumentBytes})
		}
	}
	backend, err := backendFactory(gate)
	if err != nil {
		return errors.New("named SM state backend is unavailable")
	}
	store, err := smstore.NewBackendStore(backend, cfg.CASAttempts, cfg.ProvisionLeaseName)
	if err != nil {
		return err
	}
	// Named state verification is read-only; no SM operation before acquired authority.
	if _, err := store.Load(ctx); err != nil {
		return errors.New("named SM state is unavailable or invalid")
	}
	producerCtx, producerCancel := context.WithCancel(ctx)
	defer producerCancel()
	var mu sync.Mutex
	closing, started := false, false
	acquired := make(chan struct{})
	workDone := make(chan error, 1)
	var planned atomic.Bool
	electionFactory := deps.Election
	if electionFactory == nil {
		electionFactory = productionProvisionDependencies().Election
	}
	election, err := electionFactory(ctx, lease.Options{Namespace: cfg.Namespace, Name: cfg.ProvisionLeaseName, PodUID: cfg.PodUID, LeaseDuration: cfg.LeaseDuration, RenewDeadline: cfg.RenewDeadline, RetryPeriod: cfg.RetryPeriod, RequestTimeout: cfg.KubeRequestTimeout,
		Stopped: func() {
			if !planned.Load() {
				crash()
			}
		},
		Started: func(leadCtx context.Context) {
			mu.Lock()
			if closing {
				mu.Unlock()
				return
			}
			started = true
			close(acquired)
			mu.Unlock()
			if err := gate.Activate(leadCtx, func(context.Context) error { return producerCtx.Err() }); err != nil {
				workDone <- err
				return
			}
			// Epoch cancellation is enforced on every real HTTP operation and state CAS.
			stateBound := ha.Bounded{Gate: gate, Timeout: 2 * time.Second, Margin: cfg.FenceMargin, Crash: crash}
			workDone <- executeStored(producerCtx, cfg, boundedProvisionStore{store, stateBound}, "", deps)
		}})
	if err != nil {
		return errors.New("dedicated SM provisioning Lease is unavailable")
	}
	electionCtx, electionCancel := context.WithCancel(context.Background())
	defer electionCancel()
	electionDone := make(chan struct{})
	go func() { defer close(electionDone); election.Run(electionCtx) }()
	timer := time.NewTimer(provisionAcquireTimeout)
	defer timer.Stop()
	joined := false
	select {
	case <-acquired:
		select {
		case err = <-workDone:
			joined = true
		case <-ctx.Done():
			err = ctx.Err()
		}
	case <-timer.C:
		err = errors.New("SM provisioning Lease acquisition timed out")
	case <-ctx.Done():
		err = ctx.Err()
	}
	mu.Lock()
	closing = true
	didStart := started
	mu.Unlock()
	producerCancel()
	if didStart {
		// workDone has already been consumed only on ordinary completion.
		// On interruption, positively join the complete operation, not cancellation.
		if !joined {
			join := time.NewTimer(cfg.OperationTimeout + cfg.FenceMargin)
			select {
			case <-workDone:
			case <-join.C:
				crash()
			}
			join.Stop()
		}
		gate.Quiesce()
		joinCtx, joinCancel := context.WithTimeout(context.Background(), cfg.OperationTimeout+cfg.FenceMargin)
		if gate.Wait(joinCtx) != nil {
			joinCancel()
			crash()
		}
		joinCancel()
	}
	gate.Revoke() // no operations admitted after positive worker join
	planned.Store(true)
	finalCtx, finalCancel := context.WithTimeout(context.Background(), cfg.ReleaseTimeout)
	defer finalCancel()
	if didStart {
		if sealErr := election.Seal(finalCtx); sealErr != nil {
			crash()
		}
	}
	electionCancel()
	select {
	case <-electionDone:
	case <-finalCtx.Done():
		crash()
	}
	if didStart {
		if releaseErr := election.Release(finalCtx); releaseErr != nil {
			return errors.New("SM provisioning Lease release did not complete")
		}
	}
	return err
}
