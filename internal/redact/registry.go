// Package redact applies provider-declared redaction rules to resource
// AttributesJSON at the store boundary.
//
// Provider packages register per-type rules in their init() blocks alongside
// scanner registration. Store.UpsertResources calls Apply once per row before
// insert — providers must not pre-sanitize.
package redact

import "sync"

// Mode picks how a terminal value is redacted.
type Mode int

const (
	// RedactScalar replaces a scalar leaf with Placeholder. Object/array
	// values at the terminal pass through untouched.
	RedactScalar Mode = iota
	// RedactSubtree wholesale-redacts every scalar descendant of the
	// terminal node.
	RedactSubtree
	// RedactUnlessSafe keeps a value judged safe from its shape and its key
	// name and replaces any other with a reason-tagged placeholder
	// (PlaceholderKeyName, PlaceholderValueShape, PlaceholderRule), for a
	// field that only sometimes holds a secret.
	RedactUnlessSafe
	// RedactIfSensitive keeps a value unless its key name marks it as a
	// secret or it contains a credential shape (both judged as under
	// RedactUnlessSafe, with the same placeholders), for a field whose values
	// are settings rather than secrets but whose key names a user may choose.
	RedactIfSensitive
)

// Rule names a path inside AttributesJSON and the redaction mode.
type Rule struct {
	Path string
	Mode Mode
	// KeyField, for RedactUnlessSafe and RedactIfSensitive, names the sibling
	// of the path's last named segment whose string value is the terminal
	// value's key name, for an array of name/value objects
	// (OptionSettings[*].Value keyed by OptionName); a missing or non-string
	// sibling counts as a sensitive key.
	// Unset, the key name is the map key or literal that leads to the value.
	KeyField string
	// KeyIsSensitive, for RedactUnlessSafe and RedactIfSensitive, judges every
	// value as if its key name were secret-looking, so only references and
	// booleans are kept: for a field that holds either a credential or a
	// pointer to one under a key that does not say so (Authentication.Value).
	KeyIsSensitive bool
	// Override, set, switches the rule's mode per object: see SiblingOverride.
	Override *SiblingOverride
}

// SiblingOverride applies Mode in place of the rule's own mode to a value
// whose sibling Field (a sibling of the path's last named segment, as with
// KeyField) holds one of Values, compared case-insensitively. A missing or
// non-string sibling also selects Mode, because nothing vouches for the
// value, so Mode is to be the stricter of the two. Under a map-wildcard
// terminal the sibling read is a key of the iterated map, as with KeyField.
type SiblingOverride struct {
	Field  string
	Values []string
	Mode   Mode
}

// TypeRules bundles every Rule for one resource type.
type TypeRules struct {
	Type       string
	Attributes []Rule
}

type compiledRule struct {
	segs           []segment
	mode           Mode
	keyField       string
	keyIsSensitive bool
	override       *SiblingOverride
}

var (
	regMu    sync.RWMutex
	registry = map[string][]compiledRule{}
)

// Register installs r in the registry. Subsequent Register calls for the same
// Type append rules — useful when scanner + resolver files in the same package
// each declare their own Rules.
func Register(r TypeRules) {
	if r.Type == "" || len(r.Attributes) == 0 {
		return
	}
	compiled := make([]compiledRule, 0, len(r.Attributes))
	for _, rule := range r.Attributes {
		compiled = append(compiled, compiledRule{
			segs:           compilePath(rule.Path),
			mode:           rule.Mode,
			keyField:       rule.KeyField,
			keyIsSensitive: rule.KeyIsSensitive,
			override:       rule.Override,
		})
	}
	regMu.Lock()
	registry[r.Type] = append(registry[r.Type], compiled...)
	regMu.Unlock()
}

// HasRules reports whether any rule is registered for resourceType.
func HasRules(resourceType string) bool {
	regMu.RLock()
	defer regMu.RUnlock()
	_, ok := registry[resourceType]
	return ok
}

// Apply walks attributesJSON under every rule registered for resourceType.
// With no rules for the type there is nothing to redact, so the input comes
// back unchanged, parseable or not. With rules, input that does not parse as
// JSON comes back as UnparseableAttributes with an error wrapping
// ErrUnparseable; the raw value, which may hold a secret a rule names, is
// never returned. The caller keeps the row and reports the error.
func Apply(resourceType, attributesJSON string) (string, error) {
	regMu.RLock()
	rules := registry[resourceType]
	regMu.RUnlock()
	if len(rules) == 0 {
		return attributesJSON, nil
	}
	return applyJSON(attributesJSON, rules)
}

// resetForTest wipes the registry. Test-only.
func resetForTest() {
	regMu.Lock()
	registry = map[string][]compiledRule{}
	regMu.Unlock()
}
