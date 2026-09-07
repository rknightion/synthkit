// SPDX-License-Identifier: AGPL-3.0-only

package fleet

// client.go — minimal Fleet Management collector-service client.
//
// API shape:
//   - Transport:  connect-JSON over HTTP POST (same as grafana/fleet-management demo tooling).
//   - URL scheme: <base>/collector.v1.CollectorService/<Method>
//   - Auth:       HTTP Basic — stackID as username, token as password.
//   - Methods:    RegisterCollector, GetConfig, UnregisterCollector.
//   - Bodies:     JSON objects.
//   - Heartbeat:  GetConfig with non-empty local_attributes is REQUIRED.
//   - Errors:     reduced to a closed operational code; response bodies are discarded.

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/rknightion/synthkit/internal/operationalerr"
)

const maxGetConfigResponseBytes = 1 << 20 // bounded before any server-provided config is decoded

// getConfigResponse pins the observed CollectorService response contract to
// github.com/grafana/alloy-remote-config v0.0.12, api/collector/v1/collector.proto:
// content (1), hash (2), not_modified (3). The protocol gives hash no algorithm or bounded
// encoding, so synthkit never retains it; a receipt uses its own fixed SHA-256 content digest.
type getConfigResponse struct {
	Content          string `json:"content"`
	NotModifiedSnake *bool  `json:"not_modified"`
	NotModifiedCamel *bool  `json:"notModified"`
}

func (r getConfigResponse) notModified() (bool, bool) {
	if r.NotModifiedSnake != nil && r.NotModifiedCamel != nil && *r.NotModifiedSnake != *r.NotModifiedCamel {
		return false, false
	}
	if r.NotModifiedCamel != nil {
		return *r.NotModifiedCamel, true
	}
	if r.NotModifiedSnake != nil {
		return *r.NotModifiedSnake, true
	}
	return false, true
}

// Client posts connect-JSON calls to the FM CollectorService on behalf of a stack.
// When dryRun is true no HTTP calls are made; each call logs its intent instead.
type Client struct {
	base    string
	stackID string
	token   string
	http    *http.Client
	dryRun  bool
}

// NewClient returns a live FM client.
// base is the FM API base URL (trailing slash is stripped, predecessor client.go line 26).
// stackID and token are the FM stack credentials (Basic auth: stackID as user, predecessor line 44).
func NewClient(base, stackID, token string) *Client {
	return &Client{
		base:    strings.TrimRight(base, "/"),
		stackID: stackID,
		token:   token,
		http:    &http.Client{Timeout: 15 * time.Second},
	}
}

// NewDryRunClient returns a Client that logs every call without making HTTP requests.
func NewDryRunClient(base, stackID, token string) *Client {
	c := NewClient(base, stackID, token)
	c.dryRun = true
	return c
}

// post marshals body as JSON and POSTs it to <base>/collector.v1.CollectorService/<method>.
// Auth: HTTP Basic with stackID/token (predecessor client.go:44).
// response is decoded only when non-nil; callers must ensure it contains no retained secrets.
func (c *Client) post(ctx context.Context, method string, body, response any) error {
	b, err := json.Marshal(body)
	if err != nil {
		return operationalerr.New(operationalerr.CodeInternal)
	}
	if c.dryRun {
		log.Printf("fleet [dry-run]: %s planned", method)
		return nil
	}
	url := fmt.Sprintf("%s/collector.v1.CollectorService/%s", c.base, method) // predecessor line 38
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(b))
	if err != nil {
		return operationalerr.New(operationalerr.CodeInternal)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.SetBasicAuth(c.stackID, c.token) // predecessor line 44
	resp, err := c.http.Do(req)
	if err != nil {
		return operationalerr.New(operationalerr.CodeOf(err))
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return operationalerr.New(operationalerr.Classify(resp.StatusCode, nil))
	}
	if response != nil {
		// Only GetConfig supplies a response target. Read its complete bounded body before
		// decoding: a single Decode can otherwise accept a valid prefix and leave unbounded
		// trailing data unread.
		payload, err := io.ReadAll(io.LimitReader(resp.Body, maxGetConfigResponseBytes+1))
		if err != nil || len(payload) > maxGetConfigResponseBytes {
			return operationalerr.New(operationalerr.CodeRejected)
		}
		decoder := json.NewDecoder(bytes.NewReader(payload))
		if err := decoder.Decode(response); err != nil && err != io.EOF {
			return operationalerr.New(operationalerr.CodeRejected)
		}
		if err := decoder.Decode(&struct{}{}); err != io.EOF {
			return operationalerr.New(operationalerr.CodeRejected)
		}
	}
	return nil
}

// RegisterCollector posts to RegisterCollector with id, name (=id), and local_attributes.
// Ported from predecessor client.go:58–61. Re-registration is idempotent per API semantics.
func (c *Client) RegisterCollector(ctx context.Context, col Collector) error {
	return c.post(ctx, "RegisterCollector", map[string]any{
		"id":               col.ID,
		"name":             col.ID, // predecessor uses col.Name which equals col.ID
		"local_attributes": col.LocalAttributes(),
	}, nil)
}

// GetConfig is the heartbeat call. local_attributes MUST be non-empty or the FM server
// will not record the heartbeat (grafana/fleet-management pkg/collectorutils/shared.go,
// noted in predecessor client.go:64–65). Its receipt proves delivery only, never parsing or
// execution of the returned Alloy configuration.
func (c *Client) GetConfig(ctx context.Context, col Collector) (Receipt, error) {
	response := getConfigResponse{}
	err := c.post(ctx, "GetConfig", map[string]any{
		"id":               col.ID,
		"local_attributes": col.LocalAttributes(),
	}, &response)
	if err != nil {
		return Receipt{State: ReceiptError}, err
	}
	notModified, valid := response.notModified()
	if !valid {
		return Receipt{State: ReceiptUnavailable}, nil
	}
	if notModified {
		// A not-modified response carries no configuration. It can establish a stale check but
		// cannot become a fresh configuration receipt.
		if response.Content == "" {
			return Receipt{State: ReceiptStale}, nil
		}
		return Receipt{State: ReceiptUnavailable}, nil
	}
	if response.Content == "" {
		return Receipt{State: ReceiptUnavailable}, nil
	}
	sum := sha256.Sum256([]byte(response.Content))
	return Receipt{State: ReceiptReceived, Digest: fmt.Sprintf("%x", sum)}, nil
}

// UnregisterCollector posts to UnregisterCollector with just the collector id.
// Ported from predecessor client.go:72–74.
func (c *Client) UnregisterCollector(ctx context.Context, id string) error {
	return c.post(ctx, "UnregisterCollector", map[string]any{"id": id}, nil)
}
