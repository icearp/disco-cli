// Package coverage builds the disco-vs-upstream type coverage matrix for
// every registered cloud provider. Each provider implements Provider and
// registers itself via init() — see internal/providers/<p>/coverage.go.
//
// Coverage truth source = the emits []TypeDecl declared on each scanner's
// serviceEntry, aggregated through the provider's Emits() method. Upstream
// truth source = a live registry call (CFN ListTypes / ARM Providers/List /
// GCP Discovery API) executed at command time. The matching engine reconciles
// the two sets via per-provider alias maps to produce the final matrix.
package coverage

import (
	"context"
	"sort"
	"strings"
	"sync"
)

// TypeDecl is one disco resource type declared by a scanner's emits field.
type TypeDecl struct {
	Service   string // disco's service segment (e.g. "ec2", "compute", "microsoft.compute")
	DiscoType string // canonical disco type, e.g. "aws:ec2:instance"
	// Synthetic marks a fabricated cross-scope stub: a placeholder row a
	// resolver upserts (no SDK call) to anchor a real edge to an account/
	// project that wasn't in scan scope (e.g. aws:iam:foreign-account,
	// gcp:iam:foreign-project). Never catalogable upstream — routes
	// unconditionally to BucketSynthetic. NOT for real resources disco scans;
	// those use Uncatalogued.
	Synthetic bool
	// Uncatalogued marks a real resource disco scans via the SDK that no
	// upstream registry (CFN/SR, ARM, Discovery) lists — e.g. aws:kms:grant,
	// the Azure Entra identities. Suppresses the upstream-missing drift signal
	// on the no-match path; auto-upgrades to covered if the registry later
	// lists it.
	Uncatalogued bool
	// Leaf marks a type as intentionally edge-less: its scanner upserts rows
	// but the resource has no outbound refs to other scanned types
	// (account-level configs, registry policies, leaf catalogue items).
	// `disco coverage --missing-resolvers` filters these out so the orphan
	// list only contains genuinely wire-able candidates.
	Leaf bool
}

// UpstreamType is one entry returned by a provider's live registry Fetch.
type UpstreamType struct {
	Key     string // canonical upstream identifier, provider-specific shape
	Service string // grouping bucket for matrix rendering
}

// FetchOptions carry per-invocation knobs from the cmd. Each provider reads
// only the fields it cares about — AWS uses Regions/Profile, Azure uses
// Subscription, GCP ignores all three.
//
// Regions is a slice so multi-region sweeps can union upstream type lists
// across regions in one call. Empty slice = provider-default (AWS falls back
// to "us-east-1" for CloudFormation ListTypes; Azure ARM types are
// subscription-scoped so a no-op; GCP Discovery is region-agnostic).
type FetchOptions struct {
	Regions      []string
	Profile      string
	Subscription string
}

// Provider is implemented by each cloud provider package. Aggregates emits
// from registeredServices, exposes the live Fetch, and supplies the alias map
// + an algorithmic fallback used when a disco-type has no explicit alias.
type Provider interface {
	Name() string
	Fetch(ctx context.Context, opts FetchOptions) ([]UpstreamType, error)
	Emits() []TypeDecl
	Aliases() map[string]string             // disco-type -> upstream key (overrides)
	AlgorithmicKey(discoType string) string // fallback when no alias entry exists
}

// RegionLister is implemented by Provider impls that can fetch the cloud's
// authoritative region/location list via SDK. Optional — providers without
// an SDK list endpoint don't implement it. Drives `disco coverage --regions`.
type RegionLister interface {
	FetchRegions(ctx context.Context, opts FetchOptions) ([]string, error)
}

// ResolverInfo summarises one registered relationship resolver for the
// `disco coverage resolvers` tooling: the resolver function name, the count of
// declared EdgeDecls, and the distinct disco service segments its edges touch.
// EdgeCount==0 marks an unannotated (intentional no-op or pending) resolver.
type ResolverInfo struct {
	Name      string   `json:"name"`
	EdgeCount int      `json:"edge_count"`
	Services  []string `json:"services,omitempty"`
}

// ResolverAuditor is an optional interface a Provider may implement to expose
// its relationship-resolver registry to `disco coverage resolvers`. Keeping it
// behind the registry (rather than a direct provider-package import in cmd) lets
// cmd stay provider-agnostic, so a slim build that excludes the provider simply
// reports the auditor as unavailable. AWS and Azure implement it.
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

// Bucket classifies a single matrix row.
type Bucket string

// Bucket values; semantics documented in internal/coverage/CLAUDE.md.
const (
	BucketCovered         Bucket = "covered"
	BucketUncovered       Bucket = "uncovered"
	BucketSynthetic       Bucket = "synthetic"
	BucketUncatalogued    Bucket = "uncatalogued"
	BucketUpstreamMissing Bucket = "upstream-missing"
)

// Row is one entry in the coverage matrix.
type Row struct {
	Provider    string `json:"provider"`
	Service     string `json:"service"`
	DiscoType   string `json:"disco_type,omitempty"`   // empty when row is upstream-only (uncovered)
	UpstreamKey string `json:"upstream_key,omitempty"` // empty when row is synthetic
	Bucket      Bucket `json:"bucket"`
}

// Matrix groups rows by provider for rendering.
type Matrix struct {
	Provider string `json:"provider"`
	Rows     []Row  `json:"rows"`
}

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

// Build assembles the coverage matrix for one provider. emits and upstream
// are deduped by their canonical keys (DiscoType / UpstreamKey) before
// matching. A disco-type marked Synthetic short-circuits to BucketSynthetic
// regardless of upstream presence (a fabricated stub is never catalogable). A
// non-synthetic disco-type whose alias resolves to a live upstream key is
// covered; one whose key is absent falls through to BucketUncatalogued when it
// is flagged Uncatalogued (an expected registry gap for a scanned resource),
// otherwise to BucketUpstreamMissing — the drift signal called out in ROADMAP
// G5. Because Uncatalogued is checked only after a failed match, such a type
// auto-upgrades to covered if the registry later lists it.
func Build(providerName string, emits []TypeDecl, aliases map[string]string, algorithmic func(string) string, upstream []UpstreamType) Matrix {
	if algorithmic == nil {
		algorithmic = AlgorithmicUpstreamKey
	}
	// De-dupe emits by DiscoType, preserving the first occurrence's Service.
	dedupedEmits := make([]TypeDecl, 0, len(emits))
	seen := make(map[string]int, len(emits))
	for _, t := range emits {
		if _, ok := seen[t.DiscoType]; ok {
			continue
		}
		seen[t.DiscoType] = len(dedupedEmits)
		dedupedEmits = append(dedupedEmits, t)
	}

	// Index upstream by key (lowercased for case-insensitive lookup; Azure
	// stores ARM IDs case-insensitively, AWS CFN names are case-sensitive but
	// alias maps may carry typo'd casing — lowercasing is forgiving without
	// loss because every provider has a unique key namespace).
	upstreamByKey := make(map[string]UpstreamType, len(upstream))
	for _, u := range upstream {
		upstreamByKey[strings.ToLower(u.Key)] = u
	}

	// Track which upstream keys were matched so we can emit the leftover
	// "uncovered" rows after walking emits.
	matched := make(map[string]bool, len(upstream))

	rows := make([]Row, 0, len(dedupedEmits)+len(upstream))

	for _, t := range dedupedEmits {
		if t.Synthetic {
			rows = append(rows, Row{
				Provider:  providerName,
				Service:   t.Service,
				DiscoType: t.DiscoType,
				Bucket:    BucketSynthetic,
			})
			continue
		}

		upstreamKey, ok := aliases[t.DiscoType]
		if !ok {
			upstreamKey = algorithmic(t.DiscoType)
		}
		lookup := strings.ToLower(upstreamKey)
		if u, hit := upstreamByKey[lookup]; hit {
			matched[lookup] = true
			rows = append(rows, Row{
				Provider:    providerName,
				Service:     t.Service,
				DiscoType:   t.DiscoType,
				UpstreamKey: u.Key,
				Bucket:      BucketCovered,
			})
			continue
		}
		if t.Uncatalogued {
			rows = append(rows, Row{
				Provider:  providerName,
				Service:   t.Service,
				DiscoType: t.DiscoType,
				Bucket:    BucketUncatalogued,
			})
			continue
		}
		rows = append(rows, Row{
			Provider:    providerName,
			Service:     t.Service,
			DiscoType:   t.DiscoType,
			UpstreamKey: upstreamKey,
			Bucket:      BucketUpstreamMissing,
		})
	}

	// Leftover upstream entries become uncovered rows.
	for k, u := range upstreamByKey {
		if matched[k] {
			continue
		}
		rows = append(rows, Row{
			Provider:    providerName,
			Service:     u.Service,
			UpstreamKey: u.Key,
			Bucket:      BucketUncovered,
		})
	}

	// Stable sort: bucket order, then service, then disco / upstream key.
	bucketOrder := map[Bucket]int{
		BucketCovered: 0, BucketUncovered: 1, BucketSynthetic: 2, BucketUncatalogued: 3, BucketUpstreamMissing: 4,
	}
	sort.SliceStable(rows, func(i, j int) bool {
		if bucketOrder[rows[i].Bucket] != bucketOrder[rows[j].Bucket] {
			return bucketOrder[rows[i].Bucket] < bucketOrder[rows[j].Bucket]
		}
		if rows[i].Service != rows[j].Service {
			return rows[i].Service < rows[j].Service
		}
		ki, kj := rows[i].DiscoType, rows[j].DiscoType
		if ki == "" {
			ki = rows[i].UpstreamKey
		}
		if kj == "" {
			kj = rows[j].UpstreamKey
		}
		return ki < kj
	})

	return Matrix{Provider: providerName, Rows: rows}
}

// AlgorithmicUpstreamKey is the fallback mapping used when no alias is
// registered for a given disco-type. Today this is just the lowercased
// disco-type itself — alias maps are the source of truth for accurate
// matching, and the fallback exists only so that providers whose
// upstream-key shape happens to equal the disco-type can omit alias entries.
//
// Per-provider algorithmic conversions (CFN PascalCase, ARM camelCase, GCP
// resource-collection forms) live in the provider's coverage.go alias-map
// builder, where the disco<->upstream rules are visible alongside the
// provider's other quirks.
func AlgorithmicUpstreamKey(discoType string) string {
	return strings.ToLower(discoType)
}
