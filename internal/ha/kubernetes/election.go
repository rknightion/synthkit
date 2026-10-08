// SPDX-License-Identifier: AGPL-3.0-only

// Package kubernetes adapts named, pre-created Lease election to the local HA seam.
package kubernetes

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	coordination "k8s.io/client-go/kubernetes/typed/coordination/v1"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/leaderelection"
	"k8s.io/client-go/tools/leaderelection/resourcelock"
)

var ErrMissing = errors.New("missing pre-created lease")
var ErrSealed = errors.New("lease renewal sealed")

type Options struct {
	Namespace, Name, PodUID                                   string
	LeaseDuration, RenewDeadline, RetryPeriod, RequestTimeout time.Duration
	Started                                                   func(context.Context)
	Stopped                                                   func()
}

type Election struct {
	lock      *namedLock
	leases    coordination.LeaseInterface
	elector   *leaderelection.LeaderElector
	identity  string
	name      string
	namespace string
}

type namedLock struct {
	delegate *resourcelock.LeaseLock
	writes   chan struct{}
	sealed   bool // protected by writes, including every renewal Update
}

func (l *namedLock) Get(ctx context.Context) (*resourcelock.LeaderElectionRecord, []byte, error) {
	return l.delegate.Get(ctx)
}
func (l *namedLock) Identity() string   { return l.delegate.Identity() }
func (l *namedLock) Describe() string   { return l.delegate.Describe() }
func (l *namedLock) RecordEvent(string) {}
func (l *namedLock) Create(context.Context, resourcelock.LeaderElectionRecord) error {
	return ErrMissing
}
func (l *namedLock) Update(ctx context.Context, record resourcelock.LeaderElectionRecord) error {
	select {
	case <-l.writes:
	case <-ctx.Done():
		return ctx.Err()
	}
	defer func() { l.writes <- struct{}{} }()
	if l.sealed {
		return ErrSealed
	}
	return l.delegate.Update(ctx, record)
}
func (l *namedLock) seal(ctx context.Context) error {
	select {
	case <-l.writes:
	case <-ctx.Done():
		return ctx.Err()
	}
	defer func() { l.writes <- struct{}{} }()
	l.sealed = true
	return nil
}
func New(ctx context.Context, opts Options) (*Election, error) {
	cfg, err := rest.InClusterConfig()
	if err != nil {
		return nil, fmt.Errorf("HA in-cluster configuration: %w", err)
	}
	cfg.Timeout = opts.RequestTimeout
	client, err := coordination.NewForConfig(cfg)
	if err != nil {
		return nil, err
	}
	releaseClient, err := coordination.NewForConfig(cfg)
	if err != nil {
		return nil, err
	}
	return newWithClients(ctx, opts, client, releaseClient)
}

// NewForClient explicitly injects an HTTP client for local embedding/tests. The
// production composition root exclusively calls New (in-cluster credentials).
// This is not an environment-selected URL or KUBECONFIG fallback.
func NewForClient(ctx context.Context, opts Options, endpoint string, hc *http.Client) (*Election, error) {
	cfg := &rest.Config{Host: endpoint, Timeout: opts.RequestTimeout, ContentConfig: rest.ContentConfig{ContentType: "application/json", AcceptContentTypes: "application/json"}}
	client, err := coordination.NewForConfigAndClient(cfg, hc)
	if err != nil {
		return nil, err
	}
	release, err := coordination.NewForConfigAndClient(cfg, hc)
	if err != nil {
		return nil, err
	}
	return newWithClients(ctx, opts, client, release)
}
func newWithClient(ctx context.Context, opts Options, client coordination.CoordinationV1Interface) (*Election, error) {
	return newWithClients(ctx, opts, client, client)
}
func newWithClients(ctx context.Context, opts Options, client, release coordination.CoordinationV1Interface) (*Election, error) {
	if opts.Namespace == "" || opts.Name == "" || opts.PodUID == "" || opts.Started == nil || opts.Stopped == nil {
		return nil, errors.New("HA requires named lease, namespace, pod UID and terminal callbacks")
	}
	if opts.LeaseDuration <= opts.RenewDeadline || opts.LeaseDuration%time.Second != 0 || opts.RetryPeriod <= 0 || float64(opts.RenewDeadline) <= 1.2*float64(opts.RetryPeriod) || opts.RequestTimeout <= 0 || opts.RequestTimeout >= opts.RenewDeadline {
		return nil, errors.New("invalid HA election durations")
	}
	nonce := make([]byte, 16)
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	identity := opts.PodUID + "/" + hex.EncodeToString(nonce)
	leases := client.Leases(opts.Namespace)
	probe, cancel := context.WithTimeout(ctx, opts.RequestTimeout)
	defer cancel()
	lease, err := leases.Get(probe, opts.Name, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("HA named lease lookup: %w", err)
	}
	if lease.Name != opts.Name || lease.Namespace != opts.Namespace || lease.ResourceVersion == "" {
		return nil, errors.New("malformed named lease metadata")
	}
	if lease.Spec.HolderIdentity != nil && *lease.Spec.HolderIdentity != "" && (lease.Spec.LeaseDurationSeconds == nil || *lease.Spec.LeaseDurationSeconds <= 0 || lease.Spec.RenewTime == nil) {
		return nil, errors.New("malformed held lease")
	}
	lock := &namedLock{delegate: &resourcelock.LeaseLock{LeaseMeta: metav1.ObjectMeta{Namespace: opts.Namespace, Name: opts.Name}, Client: client, LockConfig: resourcelock.ResourceLockConfig{Identity: identity}}, writes: make(chan struct{}, 1)}
	lock.writes <- struct{}{}
	elector, err := leaderelection.NewLeaderElector(leaderelection.LeaderElectionConfig{Lock: lock, LeaseDuration: opts.LeaseDuration, RenewDeadline: opts.RenewDeadline, RetryPeriod: opts.RetryPeriod, ReleaseOnCancel: false, Coordinated: false, Callbacks: leaderelection.LeaderCallbacks{OnStartedLeading: opts.Started, OnStoppedLeading: opts.Stopped}})
	if err != nil {
		return nil, err
	}
	return &Election{lock: lock, leases: release.Leases(opts.Namespace), elector: elector, identity: identity, name: opts.Name, namespace: opts.Namespace}, nil
}
func (e *Election) Run(ctx context.Context) { e.elector.Run(ctx) }
func (e *Election) Identity() string        { return e.identity }

// Seal positively joins any outstanding renewal before rejecting all future updates.
func (e *Election) Seal(ctx context.Context) error { return e.lock.seal(ctx) }

// Release uses a separate typed-client path, never LeaseLock's cached Lease.
// Every conflict reloads and rechecks the current holder. Ambiguous responses fail closed.
func (e *Election) Release(ctx context.Context) error {
	select {
	case <-e.lock.writes:
	case <-ctx.Done():
		return ctx.Err()
	}
	sealed := e.lock.sealed
	e.lock.writes <- struct{}{}
	if !sealed {
		return errors.New("release requires sealed renewal")
	}
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		lease, err := e.leases.Get(ctx, e.name, metav1.GetOptions{})
		if err != nil {
			return err
		}
		if lease.Name != e.name || lease.Namespace != e.namespace || lease.ResourceVersion == "" {
			return errors.New("malformed release lease metadata")
		}
		if lease.Spec.HolderIdentity == nil || *lease.Spec.HolderIdentity != e.identity {
			return errors.New("lease holder changed; no release")
		}
		empty := ""
		one := int32(1)
		lease.Spec.HolderIdentity = &empty
		lease.Spec.LeaseDurationSeconds = &one
		_, err = e.leases.Update(ctx, lease, metav1.UpdateOptions{})
		if apierrors.IsConflict(err) {
			continue
		}
		return err
	}
}
