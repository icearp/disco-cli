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
)

// Rule names a path inside AttributesJSON and the redaction mode.
type Rule struct {
	Path string
	Mode Mode
	// KeyField, for RedactUnlessSafe, names the sibling of the path's last
	// named segment whose string value is the terminal value's key name, for
	// an array of name/value objects (OptionSettings[*].Value keyed by
	// OptionName); a missing or non-string sibling counts as a sensitive key.
	// Unset, the key name is the map key or literal that leads to the value.
	KeyField string
	// KeyIsSensitive, for RedactUnlessSafe, judges every value as if its key
	// name were secret-looking, so only references and booleans are kept: for
	// a field that holds either a credential or a pointer to one under a key
	// that does not say so (Authentication.Value).
	KeyIsSensitive bool
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
// Returns the input unchanged when no rules exist or JSON parse fails.
func Apply(resourceType, attributesJSON string) string {
	regMu.RLock()
	rules := registry[resourceType]
	regMu.RUnlock()
	if len(rules) == 0 {
		return attributesJSON
	}
	return applyJSON(attributesJSON, rules)
}

// resetForTest wipes the registry. Test-only.
func resetForTest() {
	regMu.Lock()
	registry = map[string][]compiledRule{}
	regMu.Unlock()
}
