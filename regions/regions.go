// Package regions is the dependency-free public view of the cloud regions
// disco's scanners support, per provider. The per-provider lists are the
// source of truth and live next to each provider's scanner
// (internal/providers/<p>/<p>regions); each of those SDK-free leaf packages
// self-registers its list here via init(), so this package links no cloud SDK
// and names no provider — external callers (e.g. the SaaS control plane) can
// validate scan regions without either.
//
// Registration is triggered by importing the leaf packages: the disco binary
// gets it for free (each provider package imports its own <p>regions leaf, and
// cmd pulls the providers in via internal/providers/all). A standalone consumer
// that does not import the providers should blank-import github.com/icearp/
// disco-cli/regions/all, the build-tag-gated aggregator that wires the leaf packages
// in (and honours the same `slim` tags as internal/providers/all).
//
// Provider keys are the lower-case wire values "aws", "azure", and "gcp".
//
// Alongside the provider → regions view, this package carries a per-SERVICE
// view: [RegisterServices] / [ServiceRegions] / [ServiceAvailable] answer "which
// regions does this provider offer this service in", which is what lets a
// scanner skip a dormant (service × region) cell instead of waiting for it to
// time out. Only providers whose services have a region axis register a table;
// the rest report no opinion and callers fail open.
package regions

import (
	"slices"
	"strings"
)

// registry maps provider name → supported region list. Populated by each
// <p>regions leaf package's init() via Register; init() is single-threaded so a
// plain map needs no guard.
var registry = map[string][]string{}

// Register records a provider's supported region list. Leaf packages call this
// from their init(). Later calls for the same provider overwrite earlier ones.
func Register(provider string, list []string) {
	registry[provider] = list
}

// For returns the supported regions for provider in sorted order, or nil for an
// unknown provider. The returned slice is a copy the caller may retain or
// mutate.
func For(provider string) []string {
	src, ok := registry[provider]
	if !ok {
		return nil
	}
	out := slices.Clone(src)
	slices.Sort(out)
	return out
}

// Valid reports whether region is a supported region for provider. It is an
// exact, case-sensitive membership test.
func Valid(provider, region string) bool {
	return slices.Contains(registry[provider], region)
}

// serviceRegistry maps provider name → service name → the regions that provider
// offers the service in. Populated by each <p>regions leaf package's init() via
// RegisterServices; init() is single-threaded so a plain map needs no guard.
//
// Service names are provider-qualified ("aws:cassandra"), so the provider level
// is redundant for lookup — it exists so a provider's table can be replaced
// wholesale without leaving another provider's stale keys behind.
var serviceRegistry = map[string]map[string][]string{}

// RegisterServices records the regions provider offers each of its services in,
// keyed by the provider-qualified service name ("aws:cassandra"). Leaf packages
// call this from their init(). Later calls for the same provider replace earlier
// ones.
//
// It panics if provider is empty or any key lacks the provider's own "<provider>:"
// prefix. A mis-prefixed key would register a table no lookup can ever reach, so
// failing at init beats silently scanning everything forever.
func RegisterServices(provider string, m map[string][]string) {
	if provider == "" {
		panic("regions: RegisterServices called with an empty provider")
	}
	prefix := provider + ":"
	for service := range m {
		if !strings.HasPrefix(service, prefix) {
			panic("regions: RegisterServices(" + provider + ") got service key " + service + " without the " + prefix + " prefix")
		}
	}
	serviceRegistry[provider] = m
}

// ServiceRegions returns the regions the provider offers service in, sorted, or
// nil when nothing is registered for it. service is the provider-qualified name
// ("aws:cassandra"). The returned slice is a copy the caller may retain or mutate.
func ServiceRegions(service string) []string {
	src := serviceRegionsFor(service)
	if len(src) == 0 {
		return nil
	}
	out := slices.Clone(src)
	slices.Sort(out)
	return out
}

// ServiceAvailable reports whether the provider offers service in region.
//
// known distinguishes "not offered there" from "no data": it is false when
// nothing is registered for service, in which case avail is meaningless and the
// caller MUST fail open and scan anyway. Registration is best-effort per
// provider — azure and gcp register nothing today — so an unknown service is the
// normal case, not an error.
func ServiceAvailable(service, region string) (known, avail bool) {
	src := serviceRegionsFor(service)
	if len(src) == 0 {
		return false, false
	}
	return true, slices.Contains(src, region)
}

// serviceRegionsFor resolves the registered region list for a
// provider-qualified service name, deriving the provider from the name's prefix.
func serviceRegionsFor(service string) []string {
	provider, _, ok := strings.Cut(service, ":")
	if !ok || provider == "" {
		return nil
	}
	return serviceRegistry[provider][service]
}
