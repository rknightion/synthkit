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
//   - Errors:     any 4xx/5xx → error with method + status + first 512B.

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"
)

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
func (c *Client) post(ctx context.Context, method string, body any) error {
	b, err := json.Marshal(body)
	if err != nil {
		return err
	}
	if c.dryRun {
		log.Printf("fleet [dry-run]: POST %s/collector.v1.CollectorService/%s body=%s", c.base, method, b)
		return nil
	}
	url := fmt.Sprintf("%s/collector.v1.CollectorService/%s", c.base, method) // predecessor line 38
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(b))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.SetBasicAuth(c.stackID, c.token) // predecessor line 44
	resp, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512)) // predecessor lines 51–52
		return fmt.Errorf("FM %s: status %d: %s", method, resp.StatusCode, msg)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	return nil
}

// RegisterCollector posts to RegisterCollector with id, name (=id), and local_attributes.
// Ported from predecessor client.go:58–61. Re-registration is idempotent per API semantics.
func (c *Client) RegisterCollector(ctx context.Context, col Collector) error {
	return c.post(ctx, "RegisterCollector", map[string]any{
		"id":               col.ID,
		"name":             col.ID, // predecessor uses col.Name which equals col.ID
		"local_attributes": col.LocalAttributes(),
	})
}

// GetConfig is the heartbeat call. local_attributes MUST be non-empty or the FM server
// will not record the heartbeat (grafana/fleet-management pkg/collectorutils/shared.go,
// noted in predecessor client.go:64–65).
func (c *Client) GetConfig(ctx context.Context, col Collector) error {
	return c.post(ctx, "GetConfig", map[string]any{
		"id":               col.ID,
		"local_attributes": col.LocalAttributes(),
	})
}

// UnregisterCollector posts to UnregisterCollector with just the collector id.
// Ported from predecessor client.go:72–74.
func (c *Client) UnregisterCollector(ctx context.Context, id string) error {
	return c.post(ctx, "UnregisterCollector", map[string]any{"id": id})
}
