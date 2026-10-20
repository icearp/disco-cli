package redact

import "encoding/json"

// Placeholder replaces redacted scalar values.
const Placeholder = "[REDACTED]"

// apply walks v under segs[0:] and redacts values that land at a terminal
// segment. sensitive says whether the key name of the value reached so far
// marks it as a likely secret (see keySensitive); array elements inherit
// their array's.
func apply(v any, segs []segment, r compiledRule, sensitive bool) any {
	if len(segs) == 0 {
		return redactTerminal(v, r.mode, sensitive)
	}
	s := segs[0]
	rest := segs[1:]
	switch s.kind {
	case segLiteral:
		if m, ok := v.(map[string]any); ok {
			if child, exists := m[s.name]; exists {
				m[s.name] = apply(child, rest, r, keySensitive(m, s.name, r))
			}
		}
	case segMapWildcard:
		if m, ok := v.(map[string]any); ok {
			for k, child := range m {
				m[k] = apply(child, rest, r, keySensitive(m, k, r))
			}
		}
	case segArrayWildcard:
		if a, ok := v.([]any); ok {
			for i, child := range a {
				a[i] = apply(child, rest, r, sensitive)
			}
		}
	}
	return v
}

// keySensitive reports whether the value at m[name] is to be judged as a
// likely secret, which only RedactUnlessSafe asks: always under
// KeyIsSensitive; else by the string in the
// KeyField sibling when one is set, a missing or non-string sibling counting
// as sensitive because nothing vouches for the value; else by name.
func keySensitive(m map[string]any, name string, r compiledRule) bool {
	if r.mode != RedactUnlessSafe {
		return false
	}
	if r.keyIsSensitive {
		return true
	}
	if r.keyField == "" {
		return isSensitiveKey(name)
	}
	k, ok := m[r.keyField].(string)
	return !ok || isSensitiveKey(k)
}

// redactTerminal handles the value sitting at the end of a rule path.
//
//   - RedactSubtree wholesale-redacts every scalar descendant.
//   - RedactScalar replaces a scalar with Placeholder; objects/arrays under
//     RedactScalar pass through untouched (use RedactSubtree to nuke a tree).
//   - RedactUnlessSafe keeps v only when classify allows its shape.
func redactTerminal(v any, mode Mode, sensitive bool) any {
	switch mode {
	case RedactUnlessSafe:
		return classify(sensitive, v)
	case RedactSubtree:
		return redactAllScalars(v)
	case RedactScalar:
		switch v.(type) {
		case map[string]any, []any, nil:
			return v
		default:
			return Placeholder
		}
	}
	return v
}

// redactAllScalars walks v and replaces every scalar (non-map, non-slice,
// non-nil) value with Placeholder.
func redactAllScalars(v any) any { return redactAllScalarsWith(v, Placeholder) }

func redactAllScalarsWith(v any, placeholder string) any {
	switch t := v.(type) {
	case map[string]any:
		for k, child := range t {
			t[k] = redactAllScalarsWith(child, placeholder)
		}
		return t
	case []any:
		for i, child := range t {
			t[i] = redactAllScalarsWith(child, placeholder)
		}
		return t
	case nil:
		return nil
	default:
		return placeholder
	}
}

// applyJSON parses raw, walks every compiled rule, re-marshals.
// Returns raw unchanged on parse/marshal failure — callers never want a
// silently dropped row.
func applyJSON(raw string, rules []compiledRule) string {
	if raw == "" || len(rules) == 0 {
		return raw
	}
	var v any
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		return raw
	}
	for _, r := range rules {
		v = apply(v, r.segs, r, false)
	}
	out, err := json.Marshal(v)
	if err != nil {
		return raw
	}
	return string(out)
}
