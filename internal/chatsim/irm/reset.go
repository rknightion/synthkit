// SPDX-License-Identifier: AGPL-3.0-only

// Package irm returns a Grafana IRM stack to a clean slate for a drill: every open OnCall alert
// group and every open incident is resolved. Neither can be deleted for good (an alert group
// delete is accepted but the group stays listed), so resolved ones stay in IRM's history.
package irm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Config points the reset at one stack.
type Config struct {
	// GrafanaURL is the stack root, e.g. https://example.grafana.net.
	GrafanaURL string
	// Token is a Grafana service account token with IRM admin rights.
	Token string
	// OnCallURL overrides the OnCall API root (default: the IRM app's onCallApiUrl setting).
	OnCallURL string
	DryRun    bool
	Progress  func(string)
	HTTP      *http.Client
}

// Report says what a reset did.
type Report struct {
	AlertGroupsResolved, IncidentsResolved int
	Problems                               []string
}

func (r Report) String() string {
	return fmt.Sprintf("%d alert groups and %d incidents resolved", r.AlertGroupsResolved, r.IncidentsResolved)
}

type client struct {
	cfg Config
	h   *http.Client
}

func (c *client) do(ctx context.Context, method, url string, body, out any) error {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, url, rd)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.cfg.Token)
	req.Header.Set("X-Grafana-URL", c.cfg.GrafanaURL) // the OnCall API resolves the stack from it
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.h.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode/100 != 2 {
		return fmt.Errorf("%s %s: HTTP %d: %s", method, url, resp.StatusCode, strings.TrimSpace(string(data[:min(len(data), 300)])))
	}
	if out != nil && len(data) > 0 {
		return json.Unmarshal(data, out)
	}
	return nil
}

// Reset resolves every open alert group and incident on the stack.
func Reset(ctx context.Context, cfg Config) (Report, error) {
	var rep Report
	if cfg.GrafanaURL == "" || cfg.Token == "" {
		return rep, errors.New("irm reset needs the Grafana URL and a service account token")
	}
	cfg.GrafanaURL = strings.TrimRight(cfg.GrafanaURL, "/")
	if cfg.Progress == nil {
		cfg.Progress = func(string) {}
	}
	h := cfg.HTTP
	if h == nil {
		h = &http.Client{Timeout: 30 * time.Second}
	}
	c := &client{cfg: cfg, h: h}
	app := cfg.GrafanaURL + "/api/plugins/grafana-irm-app"
	oncall := strings.TrimRight(cfg.OnCallURL, "/")
	if oncall == "" {
		var s struct {
			JSONData struct {
				OnCallAPIURL string `json:"onCallApiUrl"`
			} `json:"jsonData"`
		}
		if err := c.do(ctx, http.MethodGet, app+"/settings", nil, &s); err != nil {
			return rep, fmt.Errorf("irm app settings: %w", err)
		}
		if oncall = strings.TrimRight(s.JSONData.OnCallAPIURL, "/"); oncall == "" {
			return rep, errors.New("irm app settings carry no onCallApiUrl")
		}
	}

	// Incidents first: resolving one can post to its channel, which the Slack wipe then clears.
	cursor := ""
	for {
		// Fresh per page: Unmarshal keeps fields a page omits, which would re-query one cursor forever.
		var q struct {
			Incidents []struct {
				IncidentID string `json:"incidentID"`
				Status     string `json:"status"`
				Title      string `json:"title"`
			} `json:"incidents"`
			Cursor struct {
				HasMore   bool   `json:"hasMore"`
				NextValue string `json:"nextValue"`
			} `json:"cursor"`
		}
		body := map[string]any{"query": map[string]any{"limit": 100, "orderDirection": "DESC"}}
		if cursor != "" {
			body["cursor"] = map[string]any{"nextValue": cursor}
		}
		if err := c.do(ctx, http.MethodPost, app+"/resources/api/v1/IncidentsService.QueryIncidents", body, &q); err != nil {
			return rep, fmt.Errorf("query incidents: %w", err)
		}
		for _, in := range q.Incidents {
			if in.Status == "resolved" {
				continue
			}
			if !cfg.DryRun {
				err := c.do(ctx, http.MethodPost, app+"/resources/api/v1/IncidentsService.UpdateStatus",
					map[string]any{"incidentID": in.IncidentID, "status": "resolved"}, nil)
				if err != nil {
					rep.Problems = append(rep.Problems, fmt.Sprintf("resolve incident %s: %v", in.IncidentID, err))
					continue
				}
			}
			rep.IncidentsResolved++
			cfg.Progress(fmt.Sprintf("resolved incident %q", in.Title))
		}
		if !q.Cursor.HasMore || q.Cursor.NextValue == "" {
			break
		}
		cursor = q.Cursor.NextValue
	}

	next := oncall + "/api/v1/alert_groups/?perpage=100"
	var ids []string
	for next != "" {
		var page struct {
			Next    *string `json:"next"`
			Results []struct {
				ID    string `json:"id"`
				State string `json:"state"`
			} `json:"results"`
		}
		if err := c.do(ctx, http.MethodGet, next, nil, &page); err != nil {
			return rep, fmt.Errorf("list alert groups: %w", err)
		}
		for _, g := range page.Results {
			if g.State != "resolved" {
				ids = append(ids, g.ID)
			}
		}
		next = ""
		if page.Next != nil {
			next = *page.Next
		}
	}
	for _, id := range ids {
		if !cfg.DryRun {
			if err := c.do(ctx, http.MethodPost, oncall+"/api/v1/alert_groups/"+id+"/resolve/", nil, nil); err != nil {
				rep.Problems = append(rep.Problems, fmt.Sprintf("resolve alert group %s: %v", id, err))
				continue
			}
		}
		rep.AlertGroupsResolved++
	}
	cfg.Progress(fmt.Sprintf("resolved %d alert groups", rep.AlertGroupsResolved))
	return rep, nil
}
