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
)

// Rule names a path inside AttributesJSON and the redaction mode.
type Rule struct {
	Path string
	Mode Mode
}

// TypeRules bundles every Rule for one resource type.
type TypeRules struct {
	Type       string
	Attributes []Rule
}

type compiledRule struct {
	segs []segment
	mode Mode
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
			segs: compilePath(rule.Path),
			mode: rule.Mode,
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
