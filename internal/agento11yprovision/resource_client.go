// SPDX-License-Identifier: AGPL-3.0-only

package agento11yprovision

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"reflect"
	"strconv"
	"strings"
)

// The frozen base decoder uses float64 for open objects. Resource rules/cases
// need lossless numbers for semantic read-back; retain its strict structure and
// trailing-data policy while using json.Number for vendor-owned object values.
func decodeResourceObject(data []byte, out any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	decoder.UseNumber()
	if err := decoder.Decode(out); err != nil {
		return err
	}
	if decoder.Decode(new(any)) != io.EOF {
		return resourceError()
	}
	return nil
}

func resourceError() error { return errors.New("agento11y management resource observation is invalid") }
func resourceIDs(ids ...string) error {
	for _, id := range ids {
		if !validID(id) {
			return errors.New("agento11y management resource identity is invalid")
		}
	}
	return nil
}

func (c *Client) rule(data []byte, id string) (Rule, error) {
	var e Rule
	if err := requiredObject(data, "tenant_id", "rule_id", "enabled", "selector", "match", "sample_rate", "evaluator_ids", "created_by", "updated_by", "created_at", "updated_at"); err != nil {
		return e, err
	}
	if decodeResourceObject(data, &e) != nil || resourceIDs(e.RuleID) != nil || e.TenantID != c.tenant || id != "" && id != e.RuleID || e.Match == nil || e.EvaluatorIDs == nil || e.Selector == "" || e.SampleRate < 0 || e.SampleRate > 1 || e.DeletedAt != nil {
		return Rule{}, resourceError()
	}
	return e, nil
}
func (c *Client) collection(data []byte, id string) (Collection, error) {
	var e Collection
	if err := requiredObject(data, "tenant_id", "collection_id", "name", "created_by", "updated_by", "created_at", "updated_at", "member_count"); err != nil {
		return e, err
	}
	if decodeKnown(data, &e) != nil || resourceIDs(e.CollectionID) != nil || e.TenantID != c.tenant || id != "" && id != e.CollectionID || strings.TrimSpace(e.Name) == "" || e.MemberCount < 0 {
		return Collection{}, resourceError()
	}
	return e, nil
}
func validAction(condition RuleActionCondition, config RuleActionConfig) bool {
	if condition.Kind != "all_evaluators_pass" && condition.Kind != "all_evaluators_fail" {
		return false
	}
	if config.Kind != "add_to_collection" || len(config.CollectionIDs) == 0 {
		return false
	}
	seen := map[string]bool{}
	for _, id := range config.CollectionIDs {
		if !validID(id) || seen[id] {
			return false
		}
		seen[id] = true
	}
	return true
}
func (c *Client) action(data []byte, parent, id string) (RuleAction, error) {
	var e RuleAction
	if err := requiredObject(data, "tenant_id", "action_id", "rule_id", "condition", "action_config", "enabled", "created_by", "updated_by", "created_at", "updated_at"); err != nil {
		return e, err
	}
	if decodeKnown(data, &e) != nil || resourceIDs(e.ActionID, e.RuleID) != nil || e.RuleID != parent || e.TenantID != c.tenant || id != "" && id != e.ActionID || !validAction(e.Condition, e.ActionConfig) || e.DeletedAt != nil {
		return RuleAction{}, resourceError()
	}
	return e, nil
}

// CreateRule posts once. Neither this nor any response establishes ownership.
func (c *Client) CreateRule(ctx context.Context, spec RuleSpec) (Rule, error) {
	if err := resourceIDs(spec.RuleID); err != nil {
		return Rule{}, err
	}
	data, err := c.request(ctx, http.MethodPost, "/rules", spec, http.StatusOK)
	if err != nil {
		return Rule{}, err
	}
	return c.rule(data, spec.RuleID)
}
func (c *Client) GetRule(ctx context.Context, id string) (Rule, error) {
	if err := resourceIDs(id); err != nil {
		return Rule{}, err
	}
	data, err := c.request(ctx, http.MethodGet, "/rules/"+id, nil, http.StatusOK)
	if err != nil {
		return Rule{}, err
	}
	return c.rule(data, id)
}
func (c *Client) ListRules(ctx context.Context) ([]Rule, error) {
	return listEntities(ctx, c, "/rules", c.rule, func(e Rule) string { return e.RuleID })
}

// DisableRule sends only enabled:false and verifies the returned configuration.
// It does not drain previously queued or in-flight work.
func (c *Client) DisableRule(ctx context.Context, id string) (Rule, error) {
	if err := resourceIDs(id); err != nil {
		return Rule{}, err
	}
	data, err := c.request(ctx, http.MethodPatch, "/rules/"+id, struct {
		Enabled bool `json:"enabled"`
	}{false}, http.StatusOK)
	if err != nil {
		return Rule{}, err
	}
	e, err := c.rule(data, id)
	if err != nil {
		return Rule{}, err
	}
	if e.Enabled {
		return Rule{}, resourceError()
	}
	return e, nil
}
func (c *Client) DeleteRule(ctx context.Context, id string) error {
	if err := resourceIDs(id); err != nil {
		return err
	}
	_, err := c.request(ctx, http.MethodDelete, "/rules/"+id, nil, http.StatusNoContent)
	return err
}
func (c *Client) CreateCollection(ctx context.Context, spec CollectionSpec) (Collection, error) {
	data, err := c.request(ctx, http.MethodPost, "/collections", spec, http.StatusOK)
	if err != nil {
		return Collection{}, err
	}
	return c.collection(data, "")
}
func (c *Client) GetCollection(ctx context.Context, id string) (Collection, error) {
	if err := resourceIDs(id); err != nil {
		return Collection{}, err
	}
	data, err := c.request(ctx, http.MethodGet, "/collections/"+id, nil, http.StatusOK)
	if err != nil {
		return Collection{}, err
	}
	return c.collection(data, id)
}
func (c *Client) ListCollections(ctx context.Context) ([]Collection, error) {
	return listEntities(ctx, c, "/collections", c.collection, func(e Collection) string { return e.CollectionID })
}
func (c *Client) DeleteCollection(ctx context.Context, id string) error {
	if err := resourceIDs(id); err != nil {
		return err
	}
	_, err := c.request(ctx, http.MethodDelete, "/collections/"+id, nil, http.StatusNoContent)
	return err
}
func (c *Client) CreateRuleAction(ctx context.Context, parentRuleID string, spec RuleActionSpec) (RuleAction, error) {
	if err := resourceIDs(parentRuleID); err != nil {
		return RuleAction{}, err
	}
	if !validAction(spec.Condition, spec.ActionConfig) {
		return RuleAction{}, resourceError()
	}
	data, err := c.request(ctx, http.MethodPost, "/rules/"+parentRuleID+"/actions", spec, http.StatusOK)
	if err != nil {
		return RuleAction{}, err
	}
	return c.action(data, parentRuleID, "")
}
func (c *Client) GetRuleAction(ctx context.Context, parentRuleID, id string) (RuleAction, error) {
	if err := resourceIDs(parentRuleID, id); err != nil {
		return RuleAction{}, err
	}
	data, err := c.request(ctx, http.MethodGet, "/rules/"+parentRuleID+"/actions/"+id, nil, http.StatusOK)
	if err != nil {
		return RuleAction{}, err
	}
	return c.action(data, parentRuleID, id)
}

// fixedInventory handles source routes with items only (not paginated routes).
func fixedInventory[T any](data []byte, decode func([]byte, string) (T, error), identity func(T) string) ([]T, error) {
	var envelope struct {
		Items *[]json.RawMessage `json:"items"`
	}
	if decodeKnown(data, &envelope) != nil || envelope.Items == nil || len(*envelope.Items) > maxListItems {
		return nil, resourceError()
	}
	all := make([]T, 0, len(*envelope.Items))
	seen := map[string]bool{}
	for _, raw := range *envelope.Items {
		e, err := decode(raw, "")
		if err != nil {
			return nil, err
		}
		id := identity(e)
		if seen[id] {
			return nil, resourceError()
		}
		seen[id] = true
		all = append(all, e)
	}
	return all, nil
}
func (c *Client) ListRuleActions(ctx context.Context, parentRuleID string) ([]RuleAction, error) {
	if err := resourceIDs(parentRuleID); err != nil {
		return nil, err
	}
	data, err := c.request(ctx, http.MethodGet, "/rules/"+parentRuleID+"/actions", nil, http.StatusOK)
	if err != nil {
		return nil, err
	}
	return fixedInventory(data, func(raw []byte, id string) (RuleAction, error) { return c.action(raw, parentRuleID, id) }, func(e RuleAction) string { return e.ActionID })
}

// DisableRuleAction does not update its condition, collection set or parent.
func (c *Client) DisableRuleAction(ctx context.Context, parentRuleID, id string) (RuleAction, error) {
	if err := resourceIDs(parentRuleID, id); err != nil {
		return RuleAction{}, err
	}
	data, err := c.request(ctx, http.MethodPatch, "/rules/"+parentRuleID+"/actions/"+id, struct {
		Enabled bool `json:"enabled"`
	}{false}, http.StatusOK)
	if err != nil {
		return RuleAction{}, err
	}
	e, err := c.action(data, parentRuleID, id)
	if err != nil {
		return RuleAction{}, err
	}
	if e.Enabled {
		return RuleAction{}, resourceError()
	}
	return e, nil
}
func (c *Client) DeleteRuleAction(ctx context.Context, parentRuleID, id string) error {
	if err := resourceIDs(parentRuleID, id); err != nil {
		return err
	}
	_, err := c.request(ctx, http.MethodDelete, "/rules/"+parentRuleID+"/actions/"+id, nil, http.StatusNoContent)
	return err
}

func validSuiteVersion(v string) bool {
	if len(v) < 2 || v[0] != 'v' || v[1] == '0' {
		return false
	}
	n, err := strconv.ParseUint(v[1:], 10, 64)
	return err == nil && n > 0 && "v"+strconv.FormatUint(n, 10) == v
}

// normalizeBinding mirrors pinned eval/suite.go NormalizeTestSuiteBindings.
// Allocate mappings so receipt comparison never mutates the caller's review.
func normalizeBinding(binding SuiteBinding) SuiteBinding {
	binding.BindingID = strings.TrimSpace(binding.BindingID)
	binding.EvaluatorID = strings.TrimSpace(binding.EvaluatorID)
	binding.EvaluatorVersion = strings.TrimSpace(binding.EvaluatorVersion)
	binding.ReportRole = strings.TrimSpace(binding.ReportRole)
	mappings := make(map[string]string, len(binding.InputMappings))
	for name, selector := range binding.InputMappings {
		mappings[strings.TrimSpace(name)] = strings.TrimSpace(selector)
	}
	binding.InputMappings = mappings
	return binding
}

// Compare the complete normalized values by ID, allowing the server's sort but
// not same-ID evaluator/version/mapping/report-role substitution or omission.
func sameBindingContent(wanted, observed []SuiteBinding) bool {
	if len(wanted) != len(observed) {
		return false
	}
	byID := make(map[string]SuiteBinding, len(wanted))
	for _, binding := range wanted {
		b := normalizeBinding(binding)
		if _, exists := byID[b.BindingID]; exists {
			return false
		}
		byID[b.BindingID] = b
	}
	for _, binding := range observed {
		b := normalizeBinding(binding)
		expected, ok := byID[b.BindingID]
		if !ok || !reflect.DeepEqual(expected, b) {
			return false
		}
		delete(byID, b.BindingID)
	}
	return len(byID) == 0
}

func validBindings(bindings []SuiteBinding) bool {
	if len(bindings) > maxListItems {
		return false
	}
	seen := map[string]bool{}
	for _, binding := range bindings {
		b := normalizeBinding(binding)
		if resourceIDs(b.BindingID, b.EvaluatorID) != nil || b.EvaluatorVersion == "" || binding.InputMappings == nil || len(b.InputMappings) != len(binding.InputMappings) || seen[b.BindingID] {
			return false
		}
		seen[b.BindingID] = true
	}
	return true
}
func (c *Client) version(data []byte, suiteID, version string) (SuiteVersion, error) {
	var e SuiteVersion
	if err := requiredObject(data, "suite_id", "version", "test_case_count", "published", "created_at"); err != nil {
		return e, err
	}
	if decodeKnown(data, &e) != nil || e.SuiteID != suiteID || !validSuiteVersion(e.Version) || version != "" && e.Version != version || e.TenantID != "" && e.TenantID != c.tenant || e.TestCaseCount < 0 || !validBindings(e.Bindings) {
		return SuiteVersion{}, resourceError()
	}
	// Tenant is source-optional, but an explicit null/empty tenant is not a
	// contradictory-observation escape hatch.
	if !optionalIdentity(data, "tenant_id", c.tenant) {
		return SuiteVersion{}, resourceError()
	}
	return e, nil
}
func optionalIdentity(data []byte, key, want string) bool {
	var object map[string]json.RawMessage
	if json.Unmarshal(data, &object) != nil {
		return false
	}
	raw, ok := object[key]
	if !ok {
		return true
	}
	var got string
	return json.Unmarshal(raw, &got) == nil && got == want
}
func (c *Client) suite(data []byte, id string) (Suite, error) {
	var e Suite
	if err := requiredObject(data, "tenant_id", "suite_id", "name", "created_at", "updated_at"); err != nil {
		return e, err
	}
	if decodeKnown(data, &e) != nil || resourceIDs(e.SuiteID) != nil || id != "" && e.SuiteID != id || e.TenantID != c.tenant || strings.TrimSpace(e.Name) == "" || len(e.Versions) > maxListItems {
		return Suite{}, resourceError()
	}
	var raw struct {
		Versions []json.RawMessage `json:"versions"`
	}
	if json.Unmarshal(data, &raw) != nil {
		return Suite{}, resourceError()
	}
	seen := map[string]bool{}
	for _, v := range raw.Versions {
		observed, err := c.version(v, e.SuiteID, "")
		if err != nil {
			return Suite{}, err
		}
		if seen[observed.Version] {
			return Suite{}, resourceError()
		}
		seen[observed.Version] = true
	}
	if e.LatestVersion != "" && !validSuiteVersion(e.LatestVersion) {
		return Suite{}, resourceError()
	}
	return e, nil
}
func (c *Client) CreateSuite(ctx context.Context, spec SuiteSpec) (Suite, error) {
	if err := resourceIDs(spec.SuiteID); err != nil {
		return Suite{}, err
	}
	data, err := c.request(ctx, http.MethodPost, "/test-suites", spec, http.StatusOK)
	if err != nil {
		return Suite{}, err
	}
	return c.suite(data, spec.SuiteID)
}

// GetSuite is the supported version observation route. No lone-version GET exists.
func (c *Client) GetSuite(ctx context.Context, id string) (Suite, error) {
	if err := resourceIDs(id); err != nil {
		return Suite{}, err
	}
	data, err := c.request(ctx, http.MethodGet, "/test-suites/"+id, nil, http.StatusOK)
	if err != nil {
		return Suite{}, err
	}
	return c.suite(data, id)
}
func (c *Client) ListSuites(ctx context.Context) ([]Suite, error) {
	return listEntities(ctx, c, "/test-suites", c.suite, func(e Suite) string { return e.SuiteID })
}
func suiteVersionPath(suiteID, version string) (string, error) {
	if err := resourceIDs(suiteID); err != nil {
		return "", err
	}
	if !validSuiteVersion(version) {
		return "", errors.New("agento11y management suite version is invalid")
	}
	return "/test-suites/" + suiteID + "/versions/" + version, nil
}
func (c *Client) CreateDraft(ctx context.Context, suiteID string, spec DraftSpec) (SuiteVersion, error) {
	if err := resourceIDs(suiteID); err != nil {
		return SuiteVersion{}, err
	}
	body := struct {
		DraftSpec
		EmptyDraft bool `json:"empty_draft"`
	}{spec, true}
	data, err := c.request(ctx, http.MethodPost, "/test-suites/"+suiteID+"/versions", body, http.StatusOK)
	if err != nil {
		return SuiteVersion{}, err
	}
	e, err := c.version(data, suiteID, "")
	if err != nil {
		return SuiteVersion{}, err
	}
	if e.Published || e.TestCaseCount != 0 || len(e.Bindings) != 0 {
		return SuiteVersion{}, resourceError()
	}
	return e, nil
}
func (c *Client) suiteCase(data []byte, suiteID, version, id string) (SuiteCase, error) {
	var e SuiteCase
	if err := requiredObject(data, "test_case_id", "input"); err != nil {
		return e, err
	}
	if decodeResourceObject(data, &e) != nil || resourceIDs(e.TestCaseID) != nil || id != "" && e.TestCaseID != id || len(e.Input) == 0 || !optionalIdentity(data, "tenant_id", c.tenant) || !optionalIdentity(data, "suite_id", suiteID) || !optionalIdentity(data, "suite_version", version) {
		return SuiteCase{}, resourceError()
	}
	return e, nil
}
func (c *Client) CreateCase(ctx context.Context, suiteID, version string, spec CaseSpec) (SuiteCase, error) {
	path, err := suiteVersionPath(suiteID, version)
	if err != nil {
		return SuiteCase{}, err
	}
	if resourceIDs(spec.TestCaseID) != nil || len(spec.Input) == 0 || spec.ArtifactRefs != nil && len(*spec.ArtifactRefs) > 0 {
		return SuiteCase{}, resourceError()
	}
	body := struct {
		CaseSpec
		CreateOnly bool `json:"create_only"`
	}{spec, true}
	data, err := c.request(ctx, http.MethodPost, path+"/test-cases", body, http.StatusOK)
	if err != nil {
		return SuiteCase{}, err
	}
	return c.suiteCase(data, suiteID, version, spec.TestCaseID)
}
func (c *Client) GetCase(ctx context.Context, suiteID, version, id string) (SuiteCase, error) {
	path, err := suiteVersionPath(suiteID, version)
	if err != nil {
		return SuiteCase{}, err
	}
	if err := resourceIDs(id); err != nil {
		return SuiteCase{}, err
	}
	data, err := c.request(ctx, http.MethodGet, path+"/test-cases/"+id, nil, http.StatusOK)
	if err != nil {
		return SuiteCase{}, err
	}
	return c.suiteCase(data, suiteID, version, id)
}
func (c *Client) ListCases(ctx context.Context, suiteID, version string) ([]SuiteCase, error) {
	path, err := suiteVersionPath(suiteID, version)
	if err != nil {
		return nil, err
	}
	return listEntities(ctx, c, path+"/test-cases", func(raw []byte, id string) (SuiteCase, error) { return c.suiteCase(raw, suiteID, version, id) }, func(e SuiteCase) string { return e.TestCaseID })
}
func bindingInventory(data []byte) ([]SuiteBinding, error) {
	bindings, err := fixedInventory(data, func(raw []byte, _ string) (SuiteBinding, error) {
		var b SuiteBinding
		if requiredObject(raw, "binding_id", "evaluator_id", "evaluator_version", "input_mappings") != nil || decodeKnown(raw, &b) != nil || !validBindings([]SuiteBinding{b}) {
			return b, resourceError()
		}
		return b, nil
	}, func(b SuiteBinding) string { return strings.TrimSpace(b.BindingID) })
	return bindings, err
}
func (c *Client) GetBindings(ctx context.Context, suiteID, version string) ([]SuiteBinding, error) {
	path, err := suiteVersionPath(suiteID, version)
	if err != nil {
		return nil, err
	}
	data, err := c.request(ctx, http.MethodGet, path+"/bindings", nil, http.StatusOK)
	if err != nil {
		return nil, err
	}
	return bindingInventory(data)
}

// PutInitialBindings refuses an observed populated or published draft. This is
// NOT a server conditional write, freshness/ownership proof, or lifecycle lock.
// Its caller still needs exclusive authority over a fresh suite and checkpoint.
// A contradictory receipt can follow a committed write; error never permits retry.
func (c *Client) PutInitialBindings(ctx context.Context, suiteID, version string, bindings []SuiteBinding) ([]SuiteBinding, error) {
	path, err := suiteVersionPath(suiteID, version)
	if err != nil {
		return nil, err
	}
	if bindings == nil || !validBindings(bindings) {
		return nil, resourceError()
	}
	ctx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	suite, err := c.GetSuite(ctx, suiteID)
	if err != nil {
		return nil, err
	}
	found := false
	for _, v := range suite.Versions {
		if v.Version == version {
			found = true
			if v.Published || len(v.Bindings) > 0 {
				return nil, resourceError()
			}
		}
	}
	if !found {
		return nil, resourceError()
	}
	current, err := c.GetBindings(ctx, suiteID, version)
	if err != nil {
		return nil, err
	}
	if len(current) > 0 {
		return nil, resourceError()
	}
	data, err := c.request(ctx, http.MethodPut, path+"/bindings", struct {
		Bindings []SuiteBinding `json:"bindings"`
	}{bindings}, http.StatusOK)
	if err != nil {
		return nil, err
	}
	observed, err := bindingInventory(data)
	if err != nil {
		return nil, err
	}
	if !sameBindingContent(bindings, observed) {
		return nil, resourceError()
	}
	return observed, nil
}

// Publish sends an explicit complete reviewed checkpoint, never a legacy empty
// assertion. Backend sanitization/conflict errors remain failures, not retries.
// Receipt checks observe count/bindings only, not case content or custody. A
// contradictory response after POST remains ambiguous and never permits retry.
func (c *Client) Publish(ctx context.Context, suiteID, version string, review PublicationReview) (SuiteVersion, error) {
	path, err := suiteVersionPath(suiteID, version)
	if err != nil {
		return SuiteVersion{}, err
	}
	if review.Cases == nil || review.Bindings == nil || len(review.Cases) > maxListItems || !validBindings(review.Bindings) {
		return SuiteVersion{}, resourceError()
	}
	seen := map[string]bool{}
	for _, testCase := range review.Cases {
		if resourceIDs(testCase.TestCaseID) != nil || seen[testCase.TestCaseID] || len(testCase.Input) == 0 || testCase.SuiteID != "" && testCase.SuiteID != suiteID || testCase.SuiteVersion != "" && testCase.SuiteVersion != version || len(testCase.ArtifactRefs) > 0 {
			return SuiteVersion{}, resourceError()
		}
		seen[testCase.TestCaseID] = true
	}
	data, err := c.request(ctx, http.MethodPost, path+":publish", struct {
		Review PublicationReview `json:"reviewed_publication"`
	}{review}, http.StatusOK)
	if err != nil {
		return SuiteVersion{}, err
	}
	e, err := c.version(data, suiteID, version)
	if err != nil {
		return SuiteVersion{}, err
	}
	if !e.Published || e.TestCaseCount != len(review.Cases) || !sameBindingContent(review.Bindings, e.Bindings) {
		return SuiteVersion{}, resourceError()
	}
	return e, nil
}
