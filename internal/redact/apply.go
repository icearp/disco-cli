package redact

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
)

// Placeholder replaces redacted scalar values.
const Placeholder = "[REDACTED]"

// PlaceholderUnparseable marks attributes that a type's rules could not be
// applied to: they did not parse as JSON, or the redacted result did not
// re-marshal.
const PlaceholderUnparseable = "[REDACTED:unparseable]"

// UnparseableAttributes replaces the whole AttributesJSON of a row whose
// attributes could not be redacted. It is a JSON object because readers expect one:
// Postgres stores attributes as JSONB, and Resource.MarshalJSON and the Rego
// input render attributes as an object.
const UnparseableAttributes = `{"redacted":"` + PlaceholderUnparseable + `"}`

// ErrUnparseable is wrapped by the error Apply returns when attributes could
// not be redacted and were replaced with UnparseableAttributes.
var ErrUnparseable = errors.New("attributes could not be redacted")

// judgement is how the value reached so far is to be redacted: the rule's
// mode or its override's, and whether its key name marks it as a likely
// secret (see keySensitive).
type judgement struct {
	mode      Mode
	sensitive bool
}

// apply walks v under segs[0:] and redacts values that land at a terminal
// segment as j says; array elements inherit their array's judgement.
func apply(v any, segs []segment, r compiledRule, j judgement) any {
	if len(segs) == 0 {
		return redactTerminal(v, j)
	}
	s := segs[0]
	rest := segs[1:]
	switch s.kind {
	case segLiteral:
		if m, ok := v.(map[string]any); ok {
			if child, exists := m[s.name]; exists {
				m[s.name] = apply(child, rest, r, judge(m, s.name, r))
			}
		}
	case segMapWildcard:
		if m, ok := v.(map[string]any); ok {
			for k, child := range m {
				m[k] = apply(child, rest, r, judge(m, k, r))
			}
		}
	case segArrayWildcard:
		if a, ok := v.([]any); ok {
			for i, child := range a {
				a[i] = apply(child, rest, r, j)
			}
		}
	}
	return v
}

// judge returns the judgement for the value at m[name].
func judge(m map[string]any, name string, r compiledRule) judgement {
	mode := modeFor(m, r)
	return judgement{mode: mode, sensitive: keySensitive(m, name, mode, r)}
}

// modeFor returns the override's mode when r has one and the override's
// sibling in m is missing, not a string, or one of its values; else r's mode.
func modeFor(m map[string]any, r compiledRule) Mode {
	o := r.override
	if o == nil {
		return r.mode
	}
	v, ok := m[o.Field].(string)
	if !ok || slices.ContainsFunc(o.Values, func(want string) bool { return strings.EqualFold(want, v) }) {
		return o.Mode
	}
	return r.mode
}

// keySensitive reports whether the value at m[name] is to be judged as a
// likely secret, which only RedactUnlessSafe and RedactIfSensitive ask:
// always under KeyIsSensitive; else by the string in the KeyField sibling
// when one is set, a missing or non-string sibling counting as sensitive
// because nothing vouches for the value; else by name.
func keySensitive(m map[string]any, name string, mode Mode, r compiledRule) bool {
	if mode != RedactUnlessSafe && mode != RedactIfSensitive {
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
//   - RedactIfSensitive keeps v unless classifyIfSensitive redacts it.
func redactTerminal(v any, j judgement) any {
	switch j.mode {
	case RedactUnlessSafe:
		return classify(j.sensitive, v)
	case RedactIfSensitive:
		return classifyIfSensitive(j.sensitive, v)
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

// applyJSON parses raw, walks every compiled rule, re-marshals. When raw does
// not parse, or the result does not re-marshal, it fails closed: it returns
// UnparseableAttributes and an error wrapping ErrUnparseable, never raw,
// because raw may hold the very value a rule names.
func applyJSON(raw string, rules []compiledRule) (string, error) {
	if raw == "" || len(rules) == 0 {
		return raw, nil
	}
	var v any
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		return UnparseableAttributes, parseError(err)
	}
	for _, r := range rules {
		v = apply(v, r.segs, r, judgement{mode: r.mode})
	}
	out, err := json.Marshal(v)
	// Unreachable today: v came from Unmarshal and rules only keep its values
	// or replace them with placeholder strings. Kept so that a future rule
	// writing an unmarshalable value still fails closed.
	if err != nil {
		return UnparseableAttributes, fmt.Errorf("%w: re-marshal after redaction: %w", ErrUnparseable, err)
	}
	return string(out), nil
}

// parseError says where raw stopped parsing without quoting it: a
// json.SyntaxError's message names the offending character, which may be
// part of the secret a rule exists to hide.
func parseError(err error) error {
	var se *json.SyntaxError
	if errors.As(err, &se) {
		return fmt.Errorf("%w: invalid JSON at byte offset %d", ErrUnparseable, se.Offset)
	}
	return fmt.Errorf("%w: %w", ErrUnparseable, err)
}
