// SPDX-License-Identifier: AGPL-3.0-only

package aiagent

import (
	"fmt"
	"strings"
)

// evalTagName follows worker.SanitizeTagKey at backend bc963fb09. The
// explicit allowlist is separate from SDK client/context tag dimensions.
func evalTagName(raw string) string {
	key := strings.TrimSpace(raw)
	for _, prefix := range []string{"agento11y.tag.", "sigil.tag."} {
		if rest, ok := strings.CutPrefix(key, prefix); ok {
			key = rest
			break
		}
	}
	var b strings.Builder
	previousUnderscore := false
	for _, r := range key {
		valid := r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9'
		if !valid {
			if !previousUnderscore {
				b.WriteByte('_')
			}
			previousUnderscore = true
			continue
		}
		b.WriteRune(r)
		previousUnderscore = false
	}
	key = strings.TrimRight(b.String(), "_")
	if key == "" {
		key = "empty"
	}
	return "agento11y_eval_tag_" + key
}

func evalTagLabels(keys []string, tags map[string]string) map[string]string {
	labels := map[string]string{}
	for _, raw := range keys {
		key := strings.TrimSpace(raw)
		for _, prefix := range []string{"agento11y.tag.", "sigil.tag."} {
			if rest, ok := strings.CutPrefix(key, prefix); ok {
				key = rest
				break
			}
		}
		for _, stored := range []string{key, "agento11y.tag." + key, "sigil.tag." + key} {
			if value, ok := tags[stored]; ok {
				// Keep the catalogue's no-empty-dimension invariant even though the
				// server's dynamic vec uses an empty placeholder for absent tags.
				if value != "" {
					runes := []rune(strings.ToValidUTF8(value, "\uFFFD"))
					if len(runes) > 128 {
						runes = runes[:128]
					}
					labels[evalTagName(raw)] = string(runes)
				}
				break
			}
		}
	}
	return labels
}

func evaluatorRole(rule RuleDecl, name string) string {
	if rule.ExecutionMode == "sequential" {
		for i, id := range rule.Evaluators {
			if id == name && i < len(rule.Evaluators)-1 {
				return "gate"
			}
		}
	}
	return "outcome"
}

func validateEvalRules(evaluators []EvalDecl, rules []RuleDecl) error {
	byName := make(map[string]EvalDecl, len(evaluators))
	for _, ev := range evaluators {
		byName[ev.Name] = ev
	}
	for _, r := range rules {
		if r.ExecutionMode != "" && r.ExecutionMode != "parallel" && r.ExecutionMode != "sequential" {
			return fmt.Errorf("ai_agent rule %q: invalid execution_mode", r.Name)
		}
		// Backend bc963fb09 validateSequentialGates requires every nonterminal
		// evaluator to exist and have an explicit pass verdict. The legacy
		// synthetic model implements numeric threshold and bool-detection
		// policies; it has no string pass_value/pass_match condition.
		if r.ExecutionMode == "sequential" && len(r.Evaluators) > 1 {
			for _, name := range r.Evaluators[:len(r.Evaluators)-1] {
				ev, ok := byName[name]
				if !ok {
					return fmt.Errorf("ai_agent rule %q: sequential gate %q was not found", r.Name, name)
				}
				if ev.ValueType != "number" && ev.ValueType != "bool" {
					return fmt.Errorf("ai_agent rule %q: sequential gate %q requires a numeric threshold or bool verdict", r.Name, name)
				}
			}
		}
		if len(r.FilterableTagKeys) > 10 {
			return fmt.Errorf("ai_agent rule %q: filterable_tag_keys exceeds 10", r.Name)
		}
		seen := map[string]string{}
		for _, key := range r.FilterableTagKeys {
			if strings.TrimSpace(key) == "" {
				return fmt.Errorf("ai_agent rule %q: empty filterable tag key", r.Name)
			}
			name := evalTagName(key)
			if prev, ok := seen[name]; ok {
				return fmt.Errorf("ai_agent rule %q: filterable tag keys %q and %q collide", r.Name, prev, key)
			}
			seen[name] = key
		}
	}
	return nil
}
