// SPDX-License-Identifier: AGPL-3.0-only

// Package main — SM API client types and low-level HTTP plumbing.
// Call shapes and endpoint paths match the Grafana Cloud Synthetic Monitoring API.
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// smClient is a thin wrapper around the Grafana Cloud Synthetic Monitoring API.
// Auth: Bearer token in the Authorization header (predecessor line 33).
type smClient struct {
	base  string
	token string
	hc    *http.Client
}

// do executes one SM API call. body is JSON-encoded when non-nil; out is
// JSON-decoded from the response body when non-nil. Any HTTP status >= 300 is
// returned as an error containing the status code and raw body.
func (c *smClient) do(method, path string, body, out any) error {
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("sm api: marshal %s %s: %w", method, path, err)
		}
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, c.base+path, r)
	if err != nil {
		return fmt.Errorf("sm api: build request %s %s: %w", method, path, err)
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.hc.Do(req)
	if err != nil {
		return fmt.Errorf("sm api: %s %s: %w", method, path, err)
	}
	defer resp.Body.Close()
	rb, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return fmt.Errorf("sm api: %s %s -> %d: %s", method, path, resp.StatusCode, string(rb))
	}
	if out != nil && len(rb) > 0 {
		if err := json.Unmarshal(rb, out); err != nil {
			return fmt.Errorf("sm api: decode response %s %s: %w", method, path, err)
		}
	}
	return nil
}

// ── Wire types (predecessor lines 51–77) ──────────────────────────────────────────

// smProbe is the SM API probe object. ID is omitted on add (server assigns it).
type smProbe struct {
	ID        int     `json:"id,omitempty"`
	Name      string  `json:"name"`
	Public    bool    `json:"public"`
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`
	Region    string  `json:"region"`
}

// smLabel is a key/value pair attached to an SM check (predecessor label struct).
type smLabel struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

// smCheck is the SM API check object (predecessor lines 65–77). ID is omitted on add.
type smCheck struct {
	ID               int            `json:"id,omitempty"`
	Job              string         `json:"job"`
	Target           string         `json:"target"`
	Frequency        int            `json:"frequency"`
	Timeout          int            `json:"timeout"`
	Enabled          bool           `json:"enabled"`
	Probes           []int          `json:"probes"`
	Labels           []smLabel      `json:"labels"`
	AlertSensitivity string         `json:"alertSensitivity"`
	BasicMetricsOnly bool           `json:"basicMetricsOnly"`
	Settings         map[string]any `json:"settings"`
}

// ── Probe operations ──────────────────────────────────────────────────────────

// listProbes calls GET /api/v1/probe/list (predecessor line 89).
func (c *smClient) listProbes() ([]smProbe, error) {
	var out []smProbe
	if err := c.do("GET", "/api/v1/probe/list", nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// addProbeResponse is the response envelope for POST /api/v1/probe/add
// (predecessor lines 100–103).
type addProbeResponse struct {
	Probe smProbe `json:"probe"`
}

// addProbe calls POST /api/v1/probe/add and returns the created probe with its
// server-assigned ID (predecessor lines 100–108).
func (c *smClient) addProbe(p smProbe) (smProbe, error) {
	var res addProbeResponse
	if err := c.do("POST", "/api/v1/probe/add", p, &res); err != nil {
		return smProbe{}, err
	}
	return res.Probe, nil
}

// ── Check operations ──────────────────────────────────────────────────────────

// listChecks calls GET /api/v1/check/list (predecessor line 115).
func (c *smClient) listChecks() ([]smCheck, error) {
	var out []smCheck
	if err := c.do("GET", "/api/v1/check/list", nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// addCheck calls POST /api/v1/check/add (predecessor line 152).
func (c *smClient) addCheck(ch smCheck) error {
	return c.do("POST", "/api/v1/check/add", ch, nil)
}

// updateCheck calls POST /api/v1/check/update (predecessor line 147).
// The check must carry a non-zero ID.
func (c *smClient) updateCheck(ch smCheck) error {
	return c.do("POST", "/api/v1/check/update", ch, nil)
}
