package redact

import "encoding/json"

// Placeholder replaces redacted scalar values.
const Placeholder = "[REDACTED]"

// apply walks v under segs[0:] and redacts values that land at a terminal
// segment.
func apply(v any, segs []segment, mode Mode) any {
	if len(segs) == 0 {
		return redactTerminal(v, mode)
	}
	s := segs[0]
	rest := segs[1:]
	switch s.kind {
	case segLiteral:
		if m, ok := v.(map[string]any); ok {
			if child, exists := m[s.name]; exists {
				m[s.name] = apply(child, rest, mode)
			}
		}
	case segMapWildcard:
		if m, ok := v.(map[string]any); ok {
			for k, child := range m {
				m[k] = apply(child, rest, mode)
			}
		}
	case segArrayWildcard:
		if a, ok := v.([]any); ok {
			for i, child := range a {
				a[i] = apply(child, rest, mode)
			}
		}
	}
	return v
}

// redactTerminal handles the value sitting at the end of a rule path.
//
//   - RedactSubtree wholesale-redacts every scalar descendant.
//   - RedactScalar replaces a scalar with Placeholder; objects/arrays under
//     RedactScalar pass through untouched (use RedactSubtree to nuke a tree).
func redactTerminal(v any, mode Mode) any {
	switch mode {
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
func redactAllScalars(v any) any {
	switch t := v.(type) {
	case map[string]any:
		for k, child := range t {
			t[k] = redactAllScalars(child)
		}
		return t
	case []any:
		for i, child := range t {
			t[i] = redactAllScalars(child)
		}
		return t
	case nil:
		return nil
	default:
		return Placeholder
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
		v = apply(v, r.segs, r.mode)
	}
	out, err := json.Marshal(v)
	if err != nil {
		return raw
	}
	return string(out)
}
