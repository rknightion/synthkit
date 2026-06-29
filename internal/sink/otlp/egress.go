// SPDX-License-Identifier: AGPL-3.0-only

// Package-internal delivery core shared by every OTLP signal sink (traces now; metrics
// next; logs later). It owns gzip + the Alloy-aligned retry policy + the self-obs push
// hook, so each signal sink is just an encoder + a call to post(). This is the seam that
// lets logs reuse the same delivery without rework.
package otlp

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/rknightion/synthkit/internal/pushhook"
	"github.com/rknightion/synthkit/internal/sink/httpretry"
)

type egress struct {
	url      string
	auth     string
	hc       *http.Client
	sinkName string // pushhook attribution: "otlp" (traces) | "otlpmetrics"
}

// newEgress builds the delivery core. signalPath is the OTLP suffix ("/v1/traces",
// "/v1/metrics"); the HTTP client timeout matches Alloy's otlphttp default (30s).
func newEgress(endpoint, signalPath, user, token, sinkName string) egress {
	return egress{
		url:      strings.TrimRight(endpoint, "/") + signalPath,
		auth:     "Basic " + base64.StdEncoding.EncodeToString([]byte(user+":"+token)),
		hc:       &http.Client{Timeout: 30 * time.Second},
		sinkName: sinkName,
	}
}

// post gzips a pre-serialised ExportXServiceRequest body and ships it with the Alloy-aligned
// OTLP retry policy, firing obs once (nil obs ⇒ no hook). items is the signal-item count
// (spans or datapoints); blueprint is recovered by the caller from the first resource.
func (e egress) post(ctx context.Context, body []byte, items int, blueprint string, obs pushhook.Observer) error {
	gz, err := gzipBytes(body)
	if err != nil {
		return fmt.Errorf("otlp %s: gzip: %w", e.sinkName, err)
	}
	var start time.Time
	if obs != nil {
		start = time.Now()
	}
	var lastStatus int
	retryErr := httpretry.OTLPPolicy().Do(ctx, func(rctx context.Context) (int, error) {
		httpReq, err := http.NewRequestWithContext(rctx, http.MethodPost, e.url, bytes.NewReader(gz))
		if err != nil {
			return 0, fmt.Errorf("otlp %s: build request: %w", e.sinkName, err)
		}
		httpReq.Header.Set("Content-Type", "application/x-protobuf")
		httpReq.Header.Set("Content-Encoding", "gzip")
		httpReq.Header.Set("Authorization", e.auth)
		resp, err := e.hc.Do(httpReq)
		if err != nil {
			lastStatus = 0
			return 0, fmt.Errorf("otlp %s: %w", e.sinkName, err)
		}
		defer resp.Body.Close()
		if resp.StatusCode/100 != 2 {
			b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
			lastStatus = resp.StatusCode
			return resp.StatusCode, fmt.Errorf("otlp %s: HTTP %d: %s", e.sinkName, resp.StatusCode, b)
		}
		lastStatus = resp.StatusCode
		return resp.StatusCode, nil
	})
	if obs != nil {
		obs(ctx, pushhook.Event{
			Sink: e.sinkName, Blueprint: blueprint, Items: items,
			Bytes: len(gz), Status: lastStatus, Duration: time.Since(start), DryRun: false, Err: retryErr,
		})
	}
	return retryErr
}
