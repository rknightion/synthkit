// SPDX-License-Identifier: AGPL-3.0-only

// Package synthkit polls a SynthKit control plane's GET /control/state and reports the rising
// and falling edges of one scenario id in active_scenarios, and switches that scenario on or
// off for the operator's fire and heal commands. It decodes its own two-field view of
// the state, so it is uncoupled from internal/control at compile time.
package synthkit

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/rknightion/synthkit/internal/chatsim/engine"
)

// Poller watches one scenario id.
type Poller struct {
	BaseURL  string // control plane root, e.g. http://127.0.0.1:8088
	Token    string // CONTROL_TOKEN; sent as HTTP Basic control:<token>
	Scenario string // "<blueprint>/<name>"
	Interval time.Duration
	Client   *http.Client
	// Logf reports poll errors; nil discards.
	Logf func(format string, a ...any)
}

type state struct {
	ActiveScenarios []string `json:"active_scenarios"`
}

// Active fetches the state once and reports whether the scenario is active.
func (p *Poller) Active(ctx context.Context) (bool, error) {
	u := strings.TrimRight(p.BaseURL, "/") + "/control/state"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return false, err
	}
	if p.Token != "" {
		req.SetBasicAuth("control", p.Token)
	}
	hc := p.Client
	if hc == nil {
		hc = &http.Client{Timeout: 5 * time.Second}
	}
	resp, err := hc.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return false, fmt.Errorf("GET /control/state: HTTP %d", resp.StatusCode)
	}
	var s state
	if err := json.NewDecoder(io.LimitReader(resp.Body, 8<<20)).Decode(&s); err != nil {
		return false, fmt.Errorf("decode /control/state: %w", err)
	}
	return slices.Contains(s.ActiveScenarios, p.Scenario), nil
}

// Run polls until ctx ends. The first successful poll is the baseline: a scenario that is
// already active (SynthKit persists active scenarios across restarts) never fires. After that,
// inactive to active sends EvSynthkitActivated and active to inactive EvSynthkitDeactivated.
func (p *Poller) Run(ctx context.Context, out chan<- engine.Event) error {
	if p.Scenario == "" {
		return errors.New("synthkit trigger: scenario id is empty")
	}
	iv := p.Interval
	if iv <= 0 {
		iv = time.Second
	}
	known, prev := false, false
	tick := time.NewTicker(iv)
	defer tick.Stop()
	for {
		active, err := p.Active(ctx)
		switch {
		case err != nil:
			if p.Logf != nil && ctx.Err() == nil {
				p.Logf("synthkit trigger: %v", err)
			}
		case !known:
			known, prev = true, active
			if p.Logf != nil {
				p.Logf("synthkit trigger: baseline %s active=%t (an already-active scenario does not fire)", p.Scenario, active)
			}
		case active != prev:
			prev = active
			kind := engine.EvSynthkitDeactivated
			if active {
				kind = engine.EvSynthkitActivated
			}
			select {
			case out <- engine.Event{Kind: kind, Text: p.Scenario}:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-tick.C:
		}
	}
}

// SetActive implements engine.Activator: POST /control/scenarios/activate (or deactivate) for
// the watched scenario. The poller then sees the edge like any other change.
func (p *Poller) SetActive(ctx context.Context, active bool) error {
	verb := "deactivate"
	if active {
		verb = "activate"
	}
	body, err := json.Marshal(map[string]string{"scenario": p.Scenario})
	if err != nil {
		return err
	}
	u := strings.TrimRight(p.BaseURL, "/") + "/control/scenarios/" + verb
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if p.Token != "" {
		req.SetBasicAuth("control", p.Token)
	}
	hc := p.Client
	if hc == nil {
		hc = &http.Client{Timeout: 10 * time.Second}
	}
	resp, err := hc.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("POST /control/scenarios/%s: HTTP %d: %s", verb, resp.StatusCode, strings.TrimSpace(string(msg)))
	}
	return nil
}
