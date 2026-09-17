// Package coverage measures how much of each cloud's listable resource
// surface disco scans. The denominator is the SDK-derived universe
// (internal/sdkinv); the numerator is the static pairing of scanner SDK calls
// with the types they store (internal/sdkinv/pairing). Each provider
// registers itself via init() — see internal/providers/<p>/<p>_coverage.go.
package coverage

import (
	"context"
	"sort"
	"sync"

	"github.com/icearp/disco-cli/internal/sdkinv"
)

// TypeDecl is one disco resource type declared by a scanner's emits field.
type TypeDecl struct {
	Service   string // disco's service segment (e.g. "ec2", "compute", "microsoft.compute")
	DiscoType string // canonical disco type, e.g. "aws:ec2:instance"
}

// UpstreamType is one entry of a provider's live registry (CloudFormation /
// Service Reference, ARM Providers, the Discovery API list), used only by
// --cross-check.
type UpstreamType struct {
	Key     string // registry identifier, provider-specific shape
	Service string // grouping bucket for rendering
}

// FetchOptions carry per-invocation knobs for the live registry and region
// calls. Each provider reads only the fields it cares about — AWS uses
// Regions/Profile, Azure uses Subscription, GCP ignores all three.
type FetchOptions struct {
	Regions      []string
	Profile      string
	Subscription string
}

// Provider is implemented by each cloud provider package: the name that
// keys the SDK universe and the types its scanners declare.
type Provider interface {
	Name() string
	Emits() []TypeDecl
}

// ServiceMapper is implemented by providers that know which scanner service
// (the name scan records carry in scans.errors / scans.warnings) stores each
// type; `coverage verify` joins failures to types through it.
type ServiceMapper interface {
	TypeServices() map[string][]string
}

// CrossChecker is implemented by providers that can diff the SDK universe
// against a live registry (`coverage services --cross-check`). RegistryKey
// and CanonicalKey map a candidate and a registry key onto one identity so
// the two catalogs compare regardless of spelling.
type CrossChecker interface {
	CrossCheck(ctx context.Context, opts FetchOptions) ([]UpstreamType, error)
	RegistryKey(c sdkinv.Candidate) string
	CanonicalKey(upstreamKey string) string
}

// RegionLister is implemented by Provider impls that can fetch the cloud's
// authoritative region/location list via SDK. Drives `disco coverage regions`.
type RegionLister interface {
	FetchRegions(ctx context.Context, opts FetchOptions) ([]string, error)
}

// ResolverInfo summarises one registered relationship resolver for the
// `disco coverage resolvers` tooling: the resolver function name, the count of
// declared EdgeDecls, and the distinct disco service segments its edges touch.
// EdgeCount==0 marks an unannotated (intentional no-op or pending) resolver.
type ResolverInfo struct {
	Name      string   `json:"name"`
	EdgeCount int      `json:"edgeCount"`
	Services  []string `json:"services,omitempty"`
}

// ResolverAuditor is an optional interface a Provider may implement to expose
// its relationship-resolver registry to `disco coverage resolvers`. Keeping it
// behind the registry (rather than a direct provider-package import in cmd) lets
// cmd stay provider-agnostic, so a slim build that excludes the provider simply
// reports the auditor as unavailable.
type ResolverAuditor interface {
	ListResolvers() []ResolverInfo // one entry per registered resolver, registration order
	ResolverEdgeSources() []string // distinct EdgeDecl.Source disco-types across all resolvers
}

// RegionRow categorises a single region across the static-vs-live diff.
// Status values:
//   - "covered" — region appears in both disco's static RegionNames list
//     and the cloud's live API response.
//   - "stale"   — disco lists it but the live API doesn't return it
//     (region retired or typo in the static list).
//   - "missing" — live API returns it but disco's static list lacks it
//     (refresh internal/providers/<p>/regions.go).
type RegionRow struct {
	Provider string `json:"provider"`
	Region   string `json:"region"`
	Status   string `json:"status"`
}

// Region status string constants.
const (
	RegionCovered = "covered"
	RegionStale   = "stale"
	RegionMissing = "missing"
)

var (
	mu       sync.RWMutex
	registry = map[string]Provider{}
)

// Register adds a Provider to the global registry. Called from each
// provider package's init().
func Register(p Provider) {
	mu.Lock()
	defer mu.Unlock()
	if _, ok := registry[p.Name()]; ok {
		panic("disco: duplicate coverage provider registration: " + p.Name())
	}
	registry[p.Name()] = p
}

// Get returns the registered Provider by name.
func Get(name string) (Provider, bool) {
	mu.RLock()
	defer mu.RUnlock()
	p, ok := registry[name]
	return p, ok
}

// All returns every registered Provider, sorted by Name.
func All() []Provider {
	mu.RLock()
	defer mu.RUnlock()
	out := make([]Provider, 0, len(registry))
	for _, p := range registry {
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name() < out[j].Name() })
	return out
}

// Names returns the sorted names of every registered provider.
func Names() []string {
	mu.RLock()
	defer mu.RUnlock()
	out := make([]string, 0, len(registry))
	for n := range registry {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}
