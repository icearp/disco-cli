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
// that does not import the providers should blank-import codeberg.org/icearp/
// disco/regions/all, the build-tag-gated aggregator that wires the leaf packages
// in (and honours the same `slim` tags as internal/providers/all).
//
// Provider keys are the lower-case wire values "aws", "azure", and "gcp".
package regions

import "slices"

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
