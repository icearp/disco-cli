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

// Skipper is an optional interface a Provider may implement to declare upstream
// resource keys it deliberately does not scan — CFN sub-resource/association
// types with no standalone List API, ephemeral task/quote/report records,
// preview services with no public SDK, or duplicates of an already-scanned type.
// Each entry maps the upstream key to a one-line rationale. Build reclassifies
// matching leftover-upstream rows from BucketUncovered to BucketNotScannable so
// the uncovered bucket reflects genuine, actionable gaps only.
type Skipper interface {
	Skips() map[string]string // upstream-key -> rationale
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
	BucketNotScannable    Bucket = "not-scannable"
	BucketUncatalogued    Bucket = "uncatalogued"
	BucketUpstreamMissing Bucket = "upstream-missing"
)

// Row is one entry in the coverage matrix.
type Row struct {
	Provider    string `json:"provider"`
	Service     string `json:"service"`
	DiscoType   string `json:"disco_type,omitempty"`   // empty when row is upstream-only (uncovered/not-scannable)
	UpstreamKey string `json:"upstream_key,omitempty"` // empty when row is uncatalogued
	Bucket      Bucket `json:"bucket"`
	Reason      string `json:"reason,omitempty"` // populated only for not-scannable rows
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
// matching. A disco-type whose alias resolves to a live upstream key is
// covered; one whose key is absent falls through to BucketUncatalogued when it
// is flagged Uncatalogued (an expected registry gap for a scanned resource),
// otherwise to BucketUpstreamMissing — the drift signal called out in ROADMAP
// G5. Because Uncatalogued is checked only after a failed match, such a type
// auto-upgrades to covered if the registry later lists it.
// skips maps a (case-insensitive) upstream key to the reason disco deliberately
// does not scan it; matching leftover-upstream entries become BucketNotScannable
// instead of BucketUncovered. Pass nil when the provider declares no skips.
func Build(providerName string, emits []TypeDecl, aliases map[string]string, algorithmic func(string) string, upstream []UpstreamType, skips map[string]string) Matrix {
	if algorithmic == nil {
		algorithmic = AlgorithmicUpstreamKey
	}
	// Normalise skip keys to the lowercased namespace Build matches in.
	skipByKey := make(map[string]string, len(skips))
	for k, reason := range skips {
		skipByKey[strings.ToLower(k)] = reason
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

	// Leftover upstream entries become uncovered rows, unless the provider has
	// declared the key deliberately not-scannable.
	for k, u := range upstreamByKey {
		if matched[k] {
			continue
		}
		if reason, skip := skipByKey[k]; skip {
			rows = append(rows, Row{
				Provider:    providerName,
				Service:     u.Service,
				UpstreamKey: u.Key,
				Bucket:      BucketNotScannable,
				Reason:      reason,
			})
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
		BucketCovered: 0, BucketUncovered: 1, BucketNotScannable: 2, BucketUncatalogued: 3, BucketUpstreamMissing: 4,
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
