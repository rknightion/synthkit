// SPDX-License-Identifier: AGPL-3.0-only

package sigil

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	"github.com/rknightion/synthkit/internal/operationalerr"
	"github.com/rknightion/synthkit/internal/pushhook"
	nativesigil "github.com/rknightion/synthkit/internal/sigil"
	"github.com/rknightion/synthkit/internal/sink/httpretry"
	sigilv1 "github.com/rknightion/synthkit/internal/sink/sigil/v1"
)

// Sink POSTs sigil ingest batches to the three native ingest endpoints via
// HTTP+protojson + HTTP Basic auth. No gRPC; no OTel SDK.
type Sink struct {
	endpoint string
	auth     string // "Basic <base64(tenantID:token)>"
	hc       *http.Client
	policy   httpretry.Policy
	dryRun   bool

	// Observe, when non-nil, receives one sanitized aggregate outcome per non-empty Write.
	Observe pushhook.Observer

	invGenerations   atomic.Int64
	invWorkflowSteps atomic.Int64
	invScores        atomic.Int64

	// invOpMu guards invOpNames; only written in dry-run.
	invOpMu    sync.Mutex
	invOpNames map[string]bool // distinct generation operation_name values
}

var jsonMarshal = protojson.MarshalOptions{UseProtoNames: true}

// New creates a sigil Sink that POSTs to endpoint, authenticating with
// HTTP Basic auth using tenantID:token. dryRun=true short-circuits the POST
// and bumps Inventory counters instead.
func New(endpoint, tenantID, token string, dryRun bool) (*Sink, error) {
	// In DRY_RUN the sink never POSTs, so empty creds are fine (mirrors promrw/otlp): this lets
	// `-once -dump` surface the sigil inventory with no GC_SIGIL_* configured. Live mode requires it.
	if endpoint == "" && !dryRun {
		return nil, fmt.Errorf("sigil: endpoint must not be empty")
	}
	auth := "Basic " + base64.StdEncoding.EncodeToString([]byte(tenantID+":"+token))
	return &Sink{
		endpoint:   endpoint,
		auth:       auth,
		hc:         &http.Client{Timeout: 15 * time.Second},
		policy:     httpretry.EmitOncePolicy(),
		dryRun:     dryRun,
		invOpNames: map[string]bool{},
	}, nil
}

// Write implements core.SigilWriter. For each Export it POSTs non-empty
// generation/workflow-step/score slices to the three ingest endpoints.
// In DRY_RUN mode it skips the POST and increments Inventory instead.
func (s *Sink) Write(ctx context.Context, batches []nativesigil.Export) error {
	var (
		allGens   []*sigilv1.Generation
		allSteps  []*sigilv1.WorkflowStep
		allScores []*sigilv1.ScoreItem
	)

	for _, b := range batches {
		allGens = append(allGens, toProtoGenerations(b.Generations)...)
		allSteps = append(allSteps, toProtoWorkflowSteps(b.WorkflowSteps)...)
		allScores = append(allScores, toProtoScores(b.Scores)...)
	}
	items := len(allGens) + len(allSteps) + len(allScores)
	if items == 0 {
		return nil
	}

	if s.dryRun {
		s.invGenerations.Add(int64(len(allGens)))
		s.invWorkflowSteps.Add(int64(len(allSteps)))
		s.invScores.Add(int64(len(allScores)))
		if len(allGens) > 0 {
			s.invOpMu.Lock()
			for _, g := range allGens {
				if op := g.GetOperationName(); op != "" {
					s.invOpNames[op] = true
				}
			}
			s.invOpMu.Unlock()
		}
		s.observe(ctx, items, 0, 0, true, nil)
		return nil
	}
	start := time.Now()
	status := 0
	delivered := 0

	if len(allGens) > 0 {
		req := &sigilv1.ExportGenerationsRequest{Generations: allGens}
		var err error
		status, err = s.postProto(ctx, "/api/v1/generations:export", req)
		if err != nil {
			s.observe(ctx, delivered, status, time.Since(start), false, err)
			return operationalerr.New(operationalerr.Classify(status, err))
		}
		delivered += len(allGens)
	}
	if len(allSteps) > 0 {
		req := &sigilv1.ExportWorkflowStepsRequest{WorkflowSteps: allSteps}
		var err error
		status, err = s.postProto(ctx, "/api/v1/workflow-steps:export", req)
		if err != nil {
			s.observe(ctx, delivered, status, time.Since(start), false, err)
			return operationalerr.New(operationalerr.Classify(status, err))
		}
		delivered += len(allSteps)
	}
	if len(allScores) > 0 {
		req := &sigilv1.ExportScoresRequest{Scores: allScores}
		var err error
		status, err = s.postProto(ctx, "/api/v1/scores:export", req)
		if err != nil {
			s.observe(ctx, delivered, status, time.Since(start), false, err)
			return operationalerr.New(operationalerr.Classify(status, err))
		}
		delivered += len(allScores)
	}

	s.observe(ctx, delivered, status, time.Since(start), false, nil)
	return nil
}

func (s *Sink) observe(ctx context.Context, items, status int, duration time.Duration, dryRun bool, err error) {
	if s.Observe == nil {
		return
	}
	s.Observe(ctx, pushhook.Event{
		Sink: "sigil", Items: items, Status: status, Duration: duration, DryRun: dryRun,
		ErrorCode: operationalerr.Classify(status, err),
	})
}

// Inventory returns the dry-run counters and operation names. Only meaningful when dryRun=true.
func (s *Sink) Inventory() Inventory {
	s.invOpMu.Lock()
	ops := make([]string, 0, len(s.invOpNames))
	for op := range s.invOpNames {
		ops = append(ops, op)
	}
	s.invOpMu.Unlock()
	sort.Strings(ops)
	return Inventory{
		Generations:    s.invGenerations.Load(),
		WorkflowSteps:  s.invWorkflowSteps.Load(),
		Scores:         s.invScores.Load(),
		OperationNames: ops,
	}
}

// postProto marshals msg as protojson and POSTs it to s.endpoint+path.
// The body is materialized once and reused across retry attempts.
func (s *Sink) postProto(ctx context.Context, path string, msg proto.Message) (int, error) {
	body, err := jsonMarshal.Marshal(msg)
	if err != nil {
		return 0, fmt.Errorf("protojson marshal: %w", err)
	}

	url := s.endpoint + path
	lastStatus := 0

	err = s.policy.Do(ctx, func(ctx context.Context) (int, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
		if err != nil {
			return 0, err
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", s.auth)

		resp, err := s.hc.Do(req)
		if err != nil {
			return 0, err
		}
		defer func() { _ = resp.Body.Close() }()
		_, _ = io.Copy(io.Discard, resp.Body)
		lastStatus = resp.StatusCode

		if resp.StatusCode >= 300 {
			return resp.StatusCode, fmt.Errorf("sigil ingest returned HTTP %d", resp.StatusCode)
		}
		return resp.StatusCode, nil
	})
	return lastStatus, err
}
