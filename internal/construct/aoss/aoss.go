// SPDX-License-Identifier: AGPL-3.0-only

// Package aoss emits AWS/AOSS (OpenSearch Serverless) CloudWatch metrics.
//
// Kind:     "aoss"
// Scope:    core.ScopeBlueprint
// Signals:  []core.SignalClass{core.Metrics}
// Interval: 60s
// Config:   Collections []string — one series-set per collection name
//
// Build requires:
//   - fx.Cloud non-nil (account_id, region)
//
// Identity is carried in Config (Collections) + fx.Cloud (account/region).
// NO fx.DB required.
//
// Signal contract (signals/cw.md [slug: cw-aoss]):
//
//	Collection-scoped: aws_aoss_search_request_rate, aws_aoss_search_request_latency,
//	aws_aoss_search_request_errors, aws_aoss_ingestion_request_rate,
//	aws_aoss_ingestion_request_success, aws_aoss_ingestion_request_errors,
//	aws_aoss_ingestion_request_latency, aws_aoss_searchable_documents,
//	aws_aoss_deleted_documents, aws_aoss_storage_used_in_s3,
//	aws_aoss_active_collection, aws_aoss_2xx, aws_aoss_4xx, aws_aoss_5xx.
//	OCU-scoped (account level, NOT per-collection): aws_aoss_search_ocu, aws_aoss_indexing_ocu.
//	All five stat suffixes emitted per base.
//
// ⚠ AOSS OCU metrics are account-level; dimension_ClientId only — NEVER dimension_CollectionId
// (signals/cw.md [slug: cw-aoss] OCU scope deviation).
package aoss

import (
	"context"
	"fmt"
	"time"

	"github.com/rknightion/synthkit/internal/core"
	"github.com/rknightion/synthkit/internal/cw"
	"github.com/rknightion/synthkit/internal/fixture"
	"github.com/rknightion/synthkit/internal/state"
)

// Config is the YAML config struct for the aoss construct.
// Identity is carried here (no fixture.DB — AOSS is not a relational DB).
type Config struct {
	// Collections is the list of OpenSearch Serverless collection names to emit.
	// If empty, one synthetic collection derived from fx.Seed is used.
	Collections []string `yaml:"collections"`
}

// Construct is the AOSS renderer. Not exported; callers use Build.
type Construct struct {
	collections []string
	accountID   string
	region      string
	st          *state.State
}

// Compile-time interface check.
var _ core.Construct = (*Construct)(nil)

// Build validates cfg and fx, resolves collections, and returns a ready core.Construct instance.
func Build(cfgAny any, fx *fixture.Set) (core.Construct, error) {
	c, ok := cfgAny.(*Config)
	if !ok || c == nil {
		return nil, fmt.Errorf("aoss: Build called with %T, want *Config", cfgAny)
	}
	if fx == nil || fx.Cloud == nil {
		return nil, fmt.Errorf("aoss: Build requires a non-nil fixture.Cloud")
	}

	collections := c.Collections
	if len(collections) == 0 {
		// Default: one synthetic collection derived from seed.
		seed := fx.Seed
		if seed == "" {
			seed = "default"
		}
		collections = []string{"collection-" + fixture.HexID(seed, 6, "aoss")}
	}

	return &Construct{
		collections: collections,
		accountID:   fx.Cloud.AccountID,
		region:      fx.Cloud.Region,
		st:          state.NewState(),
	}, nil
}

func (c *Construct) Kind() string                { return "aoss" }
func (c *Construct) Signals() []core.SignalClass { return []core.SignalClass{core.Metrics} }
func (c *Construct) Interval() time.Duration     { return 60 * time.Second }

// Tick renders one 60-second observation window into w.Metrics.
// All series use state.Set (per-period gauges, ARCHITECTURE I5 — NEVER state.Add).
func (c *Construct) Tick(ctx context.Context, now time.Time, w *core.World) error {
	bf := w.Shape.BusinessFactor(now)

	for i, colName := range c.collections {
		colID := fmt.Sprintf("collection/%s-%02d", colName, i+1)

		// Collection-scoped labels (dimension_ClientId + dimension_CollectionId + dimension_CollectionName).
		collLbls := c.baseLabels(map[string]string{
			"dimension_ClientId":       c.accountID,
			"dimension_CollectionId":   colID,
			"dimension_CollectionName": colName,
		})

		// Base names sourced VERBATIM from signals/cw.md [slug: cw-aoss].
		setGauge(c.st, "aws_aoss_search_request_rate", collLbls, 10+90*bf)
		setGauge(c.st, "aws_aoss_search_request_latency", collLbls, 50+150*bf) // ms
		setGauge(c.st, "aws_aoss_search_request_errors", collLbls, bf*0.5)
		setGauge(c.st, "aws_aoss_ingestion_request_rate", collLbls, 5+45*bf)
		setGauge(c.st, "aws_aoss_ingestion_request_success", collLbls, 5+44*bf)
		setGauge(c.st, "aws_aoss_ingestion_request_errors", collLbls, bf*0.1)
		setGauge(c.st, "aws_aoss_ingestion_request_latency", collLbls, 0.1+0.4*bf) // seconds
		setGauge(c.st, "aws_aoss_searchable_documents", collLbls, 1e6+1e5*bf)
		setGauge(c.st, "aws_aoss_deleted_documents", collLbls, bf*100)
		setGauge(c.st, "aws_aoss_storage_used_in_s3", collLbls, 10e9+1e9*bf)
		setGauge(c.st, "aws_aoss_active_collection", collLbls, 1)
		setGauge(c.st, "aws_aoss_2xx", collLbls, (10+90*bf)*60)
		setGauge(c.st, "aws_aoss_4xx", collLbls, bf*30)
		setGauge(c.st, "aws_aoss_5xx", collLbls, bf*5)

		// Info series per collection.
		c.st.Set("aws_aoss_info", collLbls, 1)
	}

	// OCU series: account-level ONLY — dimension_ClientId alone (NOT per-collection).
	// ⚠ signals/cw.md [slug: cw-aoss]: OCU scope deviation — never dimension_CollectionId here.
	ocuLbls := c.baseLabels(map[string]string{
		"dimension_ClientId": c.accountID,
	})
	setGauge(c.st, "aws_aoss_search_ocu", ocuLbls, 2+8*bf)
	setGauge(c.st, "aws_aoss_indexing_ocu", ocuLbls, 1+4*bf)

	return w.Metrics.Write(ctx, c.st.Collect(now))
}

// baseLabels builds the full CloudWatch label set for one series.
func (c *Construct) baseLabels(extra map[string]string) map[string]string {
	m := map[string]string{
		"account_id": c.accountID,
		"region":     c.region,
		"namespace":  "AWS/AOSS",
		"job":        "cloud/aws/aoss",
		"name":       fmt.Sprintf("arn:aws:aoss:%s:%s:collection/all", c.region, c.accountID),
	}
	for k, v := range extra {
		if v != "" { // I13: absent dimension OMITTED
			m[k] = v
		}
	}
	return m
}

// setGauge emits the full five-stat CW family for one AOSS per-period metric.
func setGauge(st *state.State, name string, lbls map[string]string, v float64) {
	cw.EmitStats(st, name, lbls, cw.StatSet{Sum: v, Average: v, Maximum: v, Minimum: v, SampleCount: 60})
}
