// SPDX-License-Identifier: AGPL-3.0-only

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/rknightion/synthkit/internal/operationalerr"
)

type smClient struct {
	base  string
	token string
	hc    *http.Client
}

type apiError struct {
	code      operationalerr.Code
	ambiguous bool
}

func (e apiError) Error() string                        { return operationalerr.Message(e.code) }
func (e apiError) OperationalCode() operationalerr.Code { return e.code }
func ambiguousAPIError(err error) bool {
	var target apiError
	return errors.As(err, &target) && target.ambiguous
}

func (c *smClient) do(ctx context.Context, method, path string, body, out any) error {
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			return apiError{code: operationalerr.CodeInternal}
		}
		reader = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, reader)
	if err != nil {
		return apiError{code: operationalerr.CodeInternal}
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.hc.Do(req)
	if err != nil {
		return apiError{code: operationalerr.CodeOf(err), ambiguous: method != http.MethodGet}
	}
	defer func() { _ = resp.Body.Close() }()
	response, readErr := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if readErr != nil {
		return apiError{code: operationalerr.CodeOf(readErr), ambiguous: method != http.MethodGet && resp.StatusCode < 300}
	}
	if resp.StatusCode >= 300 {
		return apiError{
			code:      operationalerr.Classify(resp.StatusCode, nil),
			ambiguous: method != http.MethodGet && resp.StatusCode >= 500,
		}
	}
	if out != nil && (len(response) == 0 || json.Unmarshal(response, out) != nil) {
		return apiError{code: operationalerr.CodeInternal, ambiguous: method != http.MethodGet}
	}
	return nil
}

type smProbe struct {
	ID        int64   `json:"id,omitempty"`
	Name      string  `json:"name"`
	Public    bool    `json:"public"`
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`
	Region    string  `json:"region"`
	Modified  float64 `json:"modified,omitempty"`
}

type smLabel struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type smCheck struct {
	ID               int64          `json:"id,omitempty"`
	Job              string         `json:"job"`
	Target           string         `json:"target"`
	Frequency        int            `json:"frequency"`
	Timeout          int            `json:"timeout"`
	Enabled          bool           `json:"enabled"`
	Probes           []int64        `json:"probes"`
	Labels           []smLabel      `json:"labels"`
	AlertSensitivity string         `json:"alertSensitivity"`
	BasicMetricsOnly bool           `json:"basicMetricsOnly"`
	Settings         map[string]any `json:"settings"`
	Modified         float64        `json:"modified,omitempty"`
}

type addProbeResponse struct {
	Probe smProbe `json:"probe"`
	Token []byte  `json:"token,omitempty"`
}

func (c *smClient) listProbes(ctx context.Context) ([]smProbe, error) {
	var out []smProbe
	if err := c.do(ctx, http.MethodGet, "/api/v1/probe/list", nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func (c *smClient) addProbe(ctx context.Context, probe smProbe) (smProbe, error) {
	var out addProbeResponse
	if err := c.do(ctx, http.MethodPost, "/api/v1/probe/add", probe, &out); err != nil {
		return smProbe{}, err
	}
	if out.Probe.ID <= 0 || out.Probe.Modified <= 0 {
		return smProbe{}, apiError{code: operationalerr.CodeInternal, ambiguous: true}
	}
	return out.Probe, nil
}

func (c *smClient) updateProbe(ctx context.Context, probe smProbe) (smProbe, error) {
	var out smProbe
	if err := c.do(ctx, http.MethodPost, "/api/v1/probe/update", probe, &out); err != nil {
		return smProbe{}, err
	}
	if out.ID <= 0 || out.Modified <= 0 {
		return smProbe{}, apiError{code: operationalerr.CodeInternal, ambiguous: true}
	}
	return out, nil
}

func (c *smClient) listChecks(ctx context.Context) ([]smCheck, error) {
	var out []smCheck
	if err := c.do(ctx, http.MethodGet, "/api/v1/check/list", nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

func (c *smClient) addCheck(ctx context.Context, check smCheck) (smCheck, error) {
	var out smCheck
	if err := c.do(ctx, http.MethodPost, "/api/v1/check/add", check, &out); err != nil {
		return smCheck{}, err
	}
	if out.ID <= 0 || out.Modified <= 0 {
		return smCheck{}, apiError{code: operationalerr.CodeInternal, ambiguous: true}
	}
	return out, nil
}

func (c *smClient) updateCheck(ctx context.Context, check smCheck) (smCheck, error) {
	var out smCheck
	if err := c.do(ctx, http.MethodPost, "/api/v1/check/update", check, &out); err != nil {
		return smCheck{}, err
	}
	if out.ID <= 0 || out.Modified <= 0 {
		return smCheck{}, apiError{code: operationalerr.CodeInternal, ambiguous: true}
	}
	return out, nil
}
