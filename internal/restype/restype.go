// Package restype defines the unified per-type Descriptor: one declaration
// site for everything disco knows about a resource type, replacing the four
// previously-separate per-type registrations (coverage emit, upstream alias,
// redaction rules, volatile fields) plus the unconditional managed flag.
//
// Registration state and aggregation stay PER-PROVIDER: each provider package
// keeps its own registeredDescriptors slice (next to registeredServices) and
// calls Emit from its registerType helper. This package holds only the struct
// and one stateless forwarder — no global cross-provider registry — so the
// coverage.Provider impl for each provider aggregates only its own types.
//
// Emit fans a Descriptor out into the shared redact/volatile/managed engines
// (which Store consults at the upsert boundary) and returns the coverage
// TypeDecl for the caller to collect. Import graph:
// restype -> {coverage, redact, volatile, managed}.
package restype

import (
	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/internal/managed"
	"codeberg.org/icearp/disco/internal/redact"
	"codeberg.org/icearp/disco/internal/volatile"
)

// Descriptor is the single source of truth for one disco resource type.
type Descriptor struct {
	Type         string // disco type, e.g. "gcp:artifactregistry:repository" — the key
	Service      string // disco service segment, e.g. "artifactregistry"
	Upstream     string // upstream catalog key; "" => rely on AlgorithmicKey fallback
	Uncatalogued bool   // real SDK-scanned type no upstream registry lists
	Leaf         bool   // intentionally edge-less; filtered from --missing-resolvers
	// Managed marks a type as UNCONDITIONALLY provider-managed; the store stamps
	// ManagedByProvider by type. Do NOT set for types whose managed status is a
	// per-row runtime decision — those stay scanner-set.
	Managed  bool
	Redact   []redact.Rule // field redaction rules, forwarded to the redact engine
	Volatile []string      // dot-path volatile fields, forwarded to the volatile engine
}

// Emit forwards d's field-level rules into the shared redact/volatile/managed
// engines and returns the coverage decl for the caller (a provider) to
// aggregate into its own descriptor slice. Stateless — holds nothing itself.
func Emit(d Descriptor) coverage.TypeDecl {
	if len(d.Redact) > 0 {
		redact.Register(redact.TypeRules{Type: d.Type, Attributes: d.Redact})
	}
	if len(d.Volatile) > 0 {
		volatile.Register(volatile.TypeRules{Type: d.Type, Paths: d.Volatile})
	}
	if d.Managed {
		managed.Register(d.Type)
	}
	return coverage.TypeDecl{
		Service:      d.Service,
		DiscoType:    d.Type,
		Uncatalogued: d.Uncatalogued,
		Leaf:         d.Leaf,
	}
}
