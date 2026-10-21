// SPDX-License-Identifier: AGPL-3.0-only

package agento11yprovision

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode"
)

// ClientOptions supplies an existing Grafana instance bearer credential. It
// never discovers credentials or sends caller-supplied tenant/actor headers.
// Zero bounds select 15 seconds and 1 MiB per response. A list shares one
// deadline across all pages. HTTPClient is copied, not modified.
type ClientOptions struct {
	BaseURL, Token, ExpectedTenantID string
	HTTPClient                       *http.Client
	Timeout                          time.Duration
	MaxResponseBytes                 int64
}

// Client accesses only evaluator/hook routes through the Grafana plugin proxy.
// It never retries a request, follows redirects, or certifies ownership/absence.
type Client struct {
	base, token, tenant string
	http                http.Client
	timeout             time.Duration
	maxBytes            int64
}

// HTTPError is a status observation, including plain-text error responses.
// A 404 alone is NOT evidence that a resource has been removed or is unowned.
type HTTPError struct{ StatusCode int }

func (e *HTTPError) Error() string {
	return fmt.Sprintf("agento11y management HTTP status %d", e.StatusCode)
}

// Grafana's plugin proxy supplies the upstream tenant and trusted actor.
const managementPrefix = "/api/plugins/grafana-agento11y-app/resources/eval"

const (
	maxListPages = 100
	maxListItems = 10000
	maxCursorLen = 4096
)

// NewClient validates a credential-free HTTPS base URL (including its base
// path). ExpectedTenantID checks observations, not pre-write attestation.
func NewClient(o ClientOptions) (*Client, error) {
	u, err := url.Parse(o.BaseURL)
	if err != nil || u == nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.RawPath != "" {
		return nil, errors.New("agento11y management requires a plain HTTPS base URL")
	}
	for _, part := range strings.Split(strings.Trim(u.Path, "/"), "/") {
		if part == "." || part == ".." {
			return nil, errors.New("agento11y management base path is invalid")
		}
	}
	if strings.ContainsAny(u.Path, "\\\r\n") || strings.Contains(u.Path, "//") || strings.IndexFunc(u.Path, unicode.IsControl) >= 0 {
		return nil, errors.New("agento11y management base path is invalid")
	}
	if o.Token == "" || strings.IndexFunc(o.Token, func(r rune) bool { return unicode.IsSpace(r) || unicode.IsControl(r) }) >= 0 {
		return nil, errors.New("agento11y management bearer credential is invalid")
	}
	if o.ExpectedTenantID == "" || strings.TrimSpace(o.ExpectedTenantID) != o.ExpectedTenantID || strings.IndexFunc(o.ExpectedTenantID, unicode.IsControl) >= 0 {
		return nil, errors.New("agento11y management expected tenant is invalid")
	}
	if o.Timeout < 0 || o.MaxResponseBytes < 0 || o.MaxResponseBytes > 64<<20 {
		return nil, errors.New("agento11y management bounds are invalid")
	}
	if o.Timeout == 0 {
		o.Timeout = 15 * time.Second
	}
	if o.MaxResponseBytes == 0 {
		o.MaxResponseBytes = 1 << 20
	}
	var hc http.Client
	if o.HTTPClient != nil {
		hc = *o.HTTPClient
	}
	if hc.Timeout <= 0 || hc.Timeout > o.Timeout {
		hc.Timeout = o.Timeout
	}
	hc.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &Client{base: strings.TrimRight(u.String(), "/") + managementPrefix, token: o.Token, tenant: o.ExpectedTenantID,
		http: hc, timeout: o.Timeout, maxBytes: o.MaxResponseBytes}, nil
}

func validID(id string) bool {
	if id == "" || id == "." || id == ".." {
		return false
	}
	for _, r := range id {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' || r == '.' || r == '-') {
			return false
		}
	}
	return true
}

func (c *Client) request(ctx context.Context, method, path string, body any, status int) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	var payload []byte
	if body != nil {
		var err error
		payload, err = json.Marshal(body)
		if err != nil {
			return nil, errors.New("agento11y management request JSON is invalid")
		}
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, bytes.NewReader(payload))
	if err != nil {
		return nil, errors.New("agento11y management request is invalid")
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("agento11y management request interrupted: %w", ctx.Err())
		}
		return nil, errors.New("agento11y management transport failed")
	}
	defer resp.Body.Close()
	// Errors need not be JSON. Do not read, decode or echo an upstream error body.
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, &HTTPError{StatusCode: resp.StatusCode}
	}
	if resp.StatusCode != status {
		return nil, errors.New("agento11y management success status is unexpected")
	}
	if status == http.StatusNoContent {
		return nil, nil
	}
	mediaType, _, err := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		return nil, errors.New("agento11y management response is not JSON")
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, c.maxBytes+1))
	if err != nil {
		return nil, errors.New("agento11y management response read failed")
	}
	if int64(len(data)) > c.maxBytes {
		return nil, errors.New("agento11y management response exceeds byte limit")
	}
	return data, nil
}

// Refuse unknown structural fields rather than silently dropping configuration
// this deliberately narrow DTO surface cannot represent (such as policy_service).
// Kind-specific config/match JSON objects remain vendor-defined open objects.
func decodeKnown(data []byte, out any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(out); err != nil {
		return err
	}
	if decoder.Decode(new(any)) != io.EOF {
		return errors.New("invalid trailing JSON")
	}
	return nil
}

func requiredObject(data []byte, fields ...string) error {
	var object map[string]json.RawMessage
	if json.Unmarshal(data, &object) != nil || object == nil {
		return errors.New("agento11y management entity JSON is invalid")
	}
	for _, field := range fields {
		value, ok := object[field]
		if !ok || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return errors.New("agento11y management entity is incomplete")
		}
	}
	return nil
}

func (c *Client) evaluator(data []byte, id string) (Evaluator, error) {
	var e Evaluator
	if err := requiredObject(data, "tenant_id", "evaluator_id", "version", "kind", "config", "output_keys"); err != nil {
		return e, err
	}
	if decodeKnown(data, &e) != nil || !validID(e.EvaluatorID) || e.TenantID != c.tenant || id != "" && e.EvaluatorID != id ||
		strings.TrimSpace(e.Version) == "" || strings.TrimSpace(e.Kind) == "" || e.Config == nil || len(e.OutputKeys) != 1 || e.OutputKeys[0].Key == "" || e.OutputKeys[0].Type == "" || e.DeletedAt != nil {
		return Evaluator{}, errors.New("agento11y management evaluator observation is invalid")
	}
	return e, nil
}

func (c *Client) hook(data []byte, id string) (HookRule, error) {
	var h HookRule
	if err := requiredObject(data, "tenant_id", "rule_id", "enabled", "phase", "priority", "selector", "match", "evaluator_ids", "action_on_fail", "short_circuit"); err != nil {
		return h, err
	}
	if decodeKnown(data, &h) != nil || !validID(h.RuleID) || h.TenantID != c.tenant || id != "" && h.RuleID != id ||
		h.Phase == "" || h.Selector == "" || h.ActionOnFail == "" || h.Match == nil || h.EvaluatorIDs == nil || h.DeletedAt != nil {
		return HookRule{}, errors.New("agento11y management hook observation is invalid")
	}
	return h, nil
}

// CreateEvaluator POSTs one explicit version, without retries or lifecycle policy.
func (c *Client) CreateEvaluator(ctx context.Context, spec EvaluatorSpec) (Evaluator, error) {
	if !validID(spec.EvaluatorID) {
		return Evaluator{}, errors.New("agento11y management evaluator identity is invalid")
	}
	data, err := c.request(ctx, http.MethodPost, "/evaluators", spec, http.StatusOK)
	if err != nil {
		return Evaluator{}, err
	}
	evaluator, err := c.evaluator(data, spec.EvaluatorID)
	if err != nil {
		return Evaluator{}, err
	}
	if evaluator.Version != strings.TrimSpace(spec.Version) {
		return Evaluator{}, errors.New("agento11y management evaluator version observation mismatches request")
	}
	return evaluator, nil
}

// GetEvaluator observes the latest active definition; 404 is only an observation.
func (c *Client) GetEvaluator(ctx context.Context, id string) (Evaluator, error) {
	if !validID(id) {
		return Evaluator{}, errors.New("agento11y management evaluator identity is invalid")
	}
	data, err := c.request(ctx, http.MethodGet, "/evaluators/"+url.PathEscape(id), nil, http.StatusOK)
	if err != nil {
		return Evaluator{}, err
	}
	return c.evaluator(data, id)
}

// DeleteEvaluator soft-deletes an identity; 204 does not certify durable absence.
func (c *Client) DeleteEvaluator(ctx context.Context, id string) error {
	if !validID(id) {
		return errors.New("agento11y management evaluator identity is invalid")
	}
	_, err := c.request(ctx, http.MethodDelete, "/evaluators/"+url.PathEscape(id), nil, http.StatusNoContent)
	return err
}

// CreateHookRule POSTs one rule, without retries, ownership or activation policy.
func (c *Client) CreateHookRule(ctx context.Context, spec HookRuleSpec) (HookRule, error) {
	if !validID(spec.RuleID) {
		return HookRule{}, errors.New("agento11y management hook identity is invalid")
	}
	data, err := c.request(ctx, http.MethodPost, "/hook-rules", spec, http.StatusOK)
	if err != nil {
		return HookRule{}, err
	}
	return c.hook(data, spec.RuleID)
}

// GetHookRule observes configuration; it does not establish ownership.
func (c *Client) GetHookRule(ctx context.Context, id string) (HookRule, error) {
	if !validID(id) {
		return HookRule{}, errors.New("agento11y management hook identity is invalid")
	}
	data, err := c.request(ctx, http.MethodGet, "/hook-rules/"+url.PathEscape(id), nil, http.StatusOK)
	if err != nil {
		return HookRule{}, err
	}
	return c.hook(data, id)
}

// ReplaceHookRule PUTs a full replacement (the backend can upsert). A supplied
// spec.RuleID must match id; it may be omitted for this path-addressed request.
func (c *Client) ReplaceHookRule(ctx context.Context, id string, spec HookRuleSpec) (HookRule, error) {
	if !validID(id) || spec.RuleID != "" && spec.RuleID != id {
		return HookRule{}, errors.New("agento11y management hook identity is invalid")
	}
	data, err := c.request(ctx, http.MethodPut, "/hook-rules/"+url.PathEscape(id), spec, http.StatusOK)
	if err != nil {
		return HookRule{}, err
	}
	return c.hook(data, id)
}

// DeleteHookRule observes protocol acceptance, not verified absence.
func (c *Client) DeleteHookRule(ctx context.Context, id string) error {
	if !validID(id) {
		return errors.New("agento11y management hook identity is invalid")
	}
	_, err := c.request(ctx, http.MethodDelete, "/hook-rules/"+url.PathEscape(id), nil, http.StatusNoContent)
	return err
}

func listEntities[T any, K comparable](ctx context.Context, c *Client, path string, decode func([]byte, string) (T, error), identity func(T) K) ([]T, error) {
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	all := make([]T, 0)
	seenCursors := map[string]bool{}
	seenIDs := map[K]bool{}
	cursor := ""
	for page := 0; page < maxListPages; page++ {
		query := url.Values{"limit": {"100"}}
		if cursor != "" {
			query.Set("cursor", cursor)
		}
		data, err := c.request(ctx, http.MethodGet, path+"?"+query.Encode(), nil, http.StatusOK)
		if err != nil {
			return nil, err
		}
		var envelope struct {
			Items      *[]json.RawMessage `json:"items"`
			NextCursor *string            `json:"next_cursor"`
		}
		if decodeKnown(data, &envelope) != nil || envelope.Items == nil || envelope.NextCursor == nil {
			return nil, errors.New("agento11y management list envelope is invalid")
		}
		if len(all)+len(*envelope.Items) > maxListItems {
			return nil, errors.New("agento11y management list exceeds item limit")
		}
		for _, raw := range *envelope.Items {
			entity, err := decode(raw, "")
			if err != nil {
				return nil, err
			}
			id := identity(entity)
			if seenIDs[id] {
				return nil, errors.New("agento11y management list repeats an identity")
			}
			seenIDs[id] = true
			all = append(all, entity)
		}
		cursor = *envelope.NextCursor
		if cursor == "" {
			return all, nil
		}
		if len(cursor) > maxCursorLen || seenCursors[cursor] {
			return nil, errors.New("agento11y management list cursor is invalid")
		}
		seenCursors[cursor] = true
	}
	return nil, errors.New("agento11y management list exceeds page limit")
}

// ListEvaluators returns every nondeleted version row in backend page order,
// not latest-per-ID. Exact (ID, version) duplicates reject the entire inventory.
func (c *Client) ListEvaluators(ctx context.Context) ([]Evaluator, error) {
	return listEntities(ctx, c, "/evaluators", c.evaluator, func(e Evaluator) [2]string {
		return [2]string{e.EvaluatorID, e.Version}
	})
}

// ListHookRules returns only a complete bounded inventory, not absence certification.
func (c *Client) ListHookRules(ctx context.Context) ([]HookRule, error) {
	return listEntities(ctx, c, "/hook-rules", c.hook, func(h HookRule) string { return h.RuleID })
}
