// SPDX-License-Identifier: AGPL-3.0-only

// Package openrouter is a minimal net/http client for OpenRouter's OpenAI-compatible chat
// completions endpoint, shaped for the director: a strict json_schema response format, provider
// preferences that keep the request on endpoints supporting it (require_parameters) and off
// data-retaining ones (data_collection deny, optional zdr), and token accounting.
package openrouter

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/rknightion/synthkit/internal/chatsim/director"
)

// DefaultBaseURL is OpenRouter's API root.
const DefaultBaseURL = "https://openrouter.ai/api/v1"

// Config configures a client. APIKey is required.
type Config struct {
	APIKey          string
	BaseURL         string
	Model           string
	Temperature     *float64
	MaxTokens       int
	Seed            *int64
	ReasoningEffort string // none|low|medium|high; "omit" or "" sends no reasoning block
	ProviderOrder   []string
	ZDR             bool
	Timeout         time.Duration
	HTTPClient      *http.Client
	AppTitle        string
}

// Usage is the running token total.
type Usage struct {
	Calls, PromptTokens, CompletionTokens int
}

// Client implements director.LLM.
type Client struct {
	cfg   Config
	http  *http.Client
	mu    sync.Mutex
	usage Usage
}

// New validates cfg and returns a client.
func New(cfg Config) (*Client, error) {
	if strings.TrimSpace(cfg.APIKey) == "" {
		return nil, errors.New("openrouter: API key is empty")
	}
	if cfg.Model == "" {
		return nil, errors.New("openrouter: model is empty")
	}
	if cfg.BaseURL == "" {
		cfg.BaseURL = DefaultBaseURL
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = 20 * time.Second
	}
	hc := cfg.HTTPClient
	if hc == nil {
		hc = &http.Client{Timeout: cfg.Timeout}
	}
	return &Client{cfg: cfg, http: hc}, nil
}

// Usage returns the token totals so far.
func (c *Client) Usage() Usage {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.usage
}

type message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type provider struct {
	RequireParameters bool     `json:"require_parameters"`
	DataCollection    string   `json:"data_collection"`
	ZDR               bool     `json:"zdr,omitempty"`
	Order             []string `json:"order,omitempty"`
	AllowFallbacks    bool     `json:"allow_fallbacks"`
}

type request struct {
	Model          string         `json:"model"`
	Messages       []message      `json:"messages"`
	ResponseFormat map[string]any `json:"response_format"`
	Provider       provider       `json:"provider"`
	Reasoning      map[string]any `json:"reasoning,omitempty"`
	Temperature    *float64       `json:"temperature,omitempty"`
	MaxTokens      int            `json:"max_tokens,omitempty"`
	Seed           *int64         `json:"seed,omitempty"`
}

type response struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
	} `json:"usage"`
	Error *struct {
		Code    any    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// Body builds the request body for a prompt (exported for tests and dry runs).
func (c *Client) Body(p director.Prompt) ([]byte, error) {
	r := request{
		Model:    c.cfg.Model,
		Messages: []message{{Role: "system", Content: p.System}, {Role: "user", Content: p.User}},
		ResponseFormat: map[string]any{
			"type":        "json_schema",
			"json_schema": map[string]any{"name": p.SchemaName, "strict": true, "schema": p.Schema},
		},
		Provider:    provider{RequireParameters: true, DataCollection: "deny", ZDR: c.cfg.ZDR, Order: c.cfg.ProviderOrder, AllowFallbacks: true},
		Temperature: c.cfg.Temperature,
		MaxTokens:   c.cfg.MaxTokens,
		Seed:        c.cfg.Seed,
	}
	if e := c.cfg.ReasoningEffort; e != "" && e != "omit" {
		r.Reasoning = map[string]any{"effort": e}
	}
	return json.Marshal(r)
}

// retryable marks a failure worth one more attempt.
type retryable struct {
	err   error
	after time.Duration
}

func (r *retryable) Error() string { return r.err.Error() }
func (r *retryable) Unwrap() error { return r.err }

// Complete implements director.LLM: one request, retried once on 429, 5xx or a transport error.
func (c *Client) Complete(ctx context.Context, p director.Prompt) (director.Completion, error) {
	body, err := c.Body(p)
	if err != nil {
		return director.Completion{}, err
	}
	out, err := c.once(ctx, body)
	var re *retryable
	if errors.As(err, &re) {
		wait := min(max(re.after, 500*time.Millisecond), 5*time.Second)
		select {
		case <-ctx.Done():
			return director.Completion{}, ctx.Err()
		case <-time.After(wait):
		}
		out, err = c.once(ctx, body)
	}
	return out, err
}

func (c *Client) once(ctx context.Context, body []byte) (director.Completion, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(c.cfg.BaseURL, "/")+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return director.Completion{}, err
	}
	req.Header.Set("Authorization", "Bearer "+c.cfg.APIKey)
	req.Header.Set("Content-Type", "application/json")
	if c.cfg.AppTitle != "" {
		req.Header.Set("X-Title", c.cfg.AppTitle)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return director.Completion{}, ctx.Err()
		}
		return director.Completion{}, &retryable{err: fmt.Errorf("openrouter: %w", err)}
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return director.Completion{}, &retryable{err: fmt.Errorf("openrouter: read body: %w", err)}
	}
	if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
		after, _ := strconv.Atoi(resp.Header.Get("Retry-After"))
		return director.Completion{}, &retryable{err: fmt.Errorf("openrouter: HTTP %d: %s", resp.StatusCode, snippet(raw)), after: time.Duration(after) * time.Second}
	}
	if resp.StatusCode != http.StatusOK {
		return director.Completion{}, fmt.Errorf("openrouter: HTTP %d: %s", resp.StatusCode, snippet(raw))
	}
	var r response
	if err := json.Unmarshal(raw, &r); err != nil {
		return director.Completion{}, fmt.Errorf("openrouter: decode: %w", err)
	}
	if r.Error != nil {
		return director.Completion{}, fmt.Errorf("openrouter: error %v: %s", r.Error.Code, r.Error.Message)
	}
	if len(r.Choices) == 0 {
		return director.Completion{}, errors.New("openrouter: response has no choices")
	}
	c.mu.Lock()
	c.usage.Calls++
	c.usage.PromptTokens += r.Usage.PromptTokens
	c.usage.CompletionTokens += r.Usage.CompletionTokens
	c.mu.Unlock()
	return director.Completion{
		Content:          r.Choices[0].Message.Content,
		PromptTokens:     r.Usage.PromptTokens,
		CompletionTokens: r.Usage.CompletionTokens,
	}, nil
}

func snippet(b []byte) string {
	s := strings.TrimSpace(string(b))
	if len(s) > 300 {
		s = s[:300] + "..."
	}
	return s
}
