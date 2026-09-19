package coverage

import (
	"slices"
	"sort"
	"strings"

	"github.com/icearp/disco-cli/internal/sdkinv"
	"github.com/icearp/disco-cli/internal/sdkinv/pairing"
)

// Bucket classifies a single matrix row.
type Bucket string

// Bucket values; semantics documented in internal/coverage/CLAUDE.md.
const (
	BucketCovered       Bucket = "covered"        // resource candidate paired with a scanner
	BucketUncovered     Bucket = "uncovered"      // resource candidate no scanner lists
	BucketAttribute     Bucket = "attribute"      // detail read of a parent (Get + id, no collection)
	BucketExcluded      Bucket = "excluded"       // catalog / non-resource / preview-only candidate
	BucketDiscoOnly     Bucket = "disco-only"     // emitted type with no candidate
	BucketRegistryDrift Bucket = "registry-drift" // --cross-check: registry ↔ universe mismatch
)

// Reason values on rows whose bucket alone does not say why.
const (
	ReasonMatchedByName      = "matched-by-name"
	ReasonSidecar            = "sidecar" // listed by a scanner that stores nothing from it
	ReasonUnexplained        = "unexplained"
	ReasonPairingUnavailable = "pairing-unavailable"
	ReasonRegistryOnly       = "registry-only"
	// ReasonMultiType: several types are paired and none of them matches the
	// candidate's identity, so naming one of them as "the" type would be a
	// coin toss. DiscoTypes carries the whole set.
	ReasonMultiType     = "multi-type"
	ReasonCandidateOnly = "candidate-only"
)

// SignalScannerLists marks an excluded row a scanner demonstrably lists and
// stores: a classifier bug to triage, not a settled exclusion.
const SignalScannerLists = "scanner-lists"

// Row is one entry in the coverage matrix.
type Row struct {
	Provider  string `json:"provider"`
	Service   string `json:"service"`
	Key       string `json:"key,omitempty"`       // candidate key; registry key on registry-only drift rows
	DiscoType string `json:"discoType,omitempty"` // the paired (or name-matched) disco type
	// DiscoTypes lists every emitted type the candidate accounts for when one
	// listing stores several (iam:GetAccountAuthorizationDetails → 7 types);
	// DiscoType is the one whose name matches the key.
	DiscoTypes []string `json:"discoTypes,omitempty"`
	Ops        []string `json:"ops,omitempty"` // candidate op labels
	Bucket     Bucket   `json:"bucket"`
	Depth      int      `json:"depth"`
	Parent     string   `json:"parent,omitempty"`
	Scope      string   `json:"scope,omitempty"`
	Reason     string   `json:"reason,omitempty"`
	Signals    []string `json:"signals,omitempty"`
	Refs       []string `json:"refs,omitempty"` // candidate element fields naming other resources
}

// DepthSummary is the covered/uncovered split at one candidate depth.
type DepthSummary struct {
	Depth     int     `json:"depth"`
	Covered   int     `json:"covered"`
	Uncovered int     `json:"uncovered"`
	Percent   float64 `json:"percent"`
}

// Summary is the provider-wide headline.
type Summary struct {
	Covered     int            `json:"covered"`
	Uncovered   int            `json:"uncovered"`
	Attribute   int            `json:"attribute"`
	Excluded    int            `json:"excluded"`
	DiscoOnly   int            `json:"discoOnly"`
	Unexplained int            `json:"unexplained"`
	Percent     float64        `json:"percent"`
	ByDepth     []DepthSummary `json:"byDepth"`
}

// ServiceSummary is the covered/uncovered split for one service.
type ServiceSummary struct {
	Service   string  `json:"service"`
	Covered   int     `json:"covered"`
	Uncovered int     `json:"uncovered"`
	Percent   float64 `json:"percent"`
}

// Matrix is one provider's coverage report. Summary and Services are computed
// over every row before any --filter is applied.
type Matrix struct {
	Provider string            `json:"provider"`
	Pins     map[string]string `json:"pins"`
	Pairing  bool              `json:"pairing"` // false when scanner source was unavailable: name matching only
	Summary  Summary           `json:"summary"`
	Services []ServiceSummary  `json:"services"`
	Rows     []Row             `json:"rows"`
}

// Inputs bundle one provider's sources for BuildInventory.
type Inputs struct {
	Provider string
	Emits    []TypeDecl
	Universe *sdkinv.Universe
	// Pairings and Unpaired come from pairing.Scan; both nil when the scanner
	// source is unavailable, in which case only name matching applies.
	Pairings []pairing.Pairing
	Unpaired map[string]string
	// Paired records that the walk ran, which a nil Pairings slice does not:
	// a source tree that parses but anchors nothing would otherwise be
	// indistinguishable from no source tree, and every gate keys on the
	// difference.
	Paired bool
}

// pairingKinds are the pairing kinds that prove a scanner lists a candidate.
// "label" (a label with no SDK call) and "other"/"skew" (no candidate) do not.
var pairingKinds = map[string]bool{"emits": true, "sidecar": true, "derived": true}

// BuildInventory classifies every candidate of the universe and every emitted
// type into buckets. The unit of coverage is the candidate: one op storing
// seven types counts once, seven ops storing one type mark seven candidates.
func BuildInventory(in Inputs) Matrix {
	m := Matrix{Provider: in.Provider, Pins: in.Universe.Pins, Pairing: in.Paired}
	types := map[string]map[string]bool{} // candidate key -> paired disco types
	for _, p := range in.Pairings {
		if p.Key == "" || !pairingKinds[p.Kind] {
			continue
		}
		if types[p.Key] == nil {
			types[p.Key] = map[string]bool{} // present with no types: a listing nothing stores (sidecar)
		}
		for _, t := range p.Types {
			types[p.Key][t] = true
		}
	}
	byIdent := map[string]string{} // canonical type identity -> disco type
	for _, e := range in.Emits {
		for _, id := range typeIdents(e.DiscoType) {
			if _, dup := byIdent[id]; !dup {
				byIdent[id] = e.DiscoType
			}
		}
	}
	accounted := map[string]bool{}
	for _, c := range in.Universe.Candidates {
		row := Row{Provider: in.Provider, Service: c.Service, Key: c.Key, Depth: c.Depth, Parent: c.Parent, Signals: c.Signals, Refs: c.Refs}
		row.Ops, row.Scope = opCells(c.Ops)
		paired, listed := types[c.Key]
		switch {
		case len(paired) > 0:
			assignPairedTypes(&row, paired, c, accounted)
		case listed:
			row.Reason = ReasonSidecar
		default:
			// A name match must not mask the regression signal: when the
			// pairing ran and explicitly could not explain the type, the
			// disco-only row it would suppress is the only thing
			// --check-strict exits on. other-op and skew matches stand.
			if t, ok := byIdent[candidateIdent(c)]; ok && !contradicted(in, t) {
				row.DiscoType, row.Reason = t, ReasonMatchedByName
				accounted[t] = true
			}
		}
		switch {
		case hasSignal(c, "preview-only"):
			row.Bucket, row.Reason = BucketExcluded, "preview-only"
		case c.Class == sdkinv.ClassAttribute:
			row.Bucket = BucketAttribute
		case c.Class == sdkinv.ClassCatalog, c.Class == sdkinv.ClassNonResource:
			row.Bucket, row.Reason = BucketExcluded, string(c.Class)
			// The class rule wins, but a scanner that provably lists and
			// stores the candidate is the strongest evidence the rule is
			// wrong. Keep it visible instead of discarding it with the reason.
			if row.DiscoType != "" || len(row.DiscoTypes) > 0 {
				row.Signals = append(append([]string(nil), row.Signals...), SignalScannerLists)
			}
		case len(paired) > 0 || listed || row.DiscoType != "":
			row.Bucket = BucketCovered
		default:
			row.Bucket = BucketUncovered
		}
		m.Rows = append(m.Rows, row)
	}
	for _, e := range in.Emits {
		if accounted[e.DiscoType] {
			continue
		}
		row := Row{Provider: in.Provider, Service: e.Service, DiscoType: e.DiscoType, Bucket: BucketDiscoOnly}
		switch reason, ok := in.Unpaired[e.DiscoType]; {
		case !in.Paired:
			row.Reason = ReasonPairingUnavailable
		case ok && reason != ReasonUnexplained:
			row.Reason = "explained: " + reason
		default:
			row.Reason = ReasonUnexplained
		}
		m.Rows = append(m.Rows, row)
	}
	sortRows(m.Rows)
	m.Summary, m.Services = summarize(m.Rows)
	return m
}

// CrossCheck appends registry-drift rows: registry keys with no candidate and
// resource candidates with no registry key. Identities come from the
// provider's CrossChecker so the two spellings compare.
func CrossCheck(m *Matrix, u *sdkinv.Universe, registry []UpstreamType, cc CrossChecker) {
	// registry-only is judged against every candidate class and only within
	// services the SDK universe knows: a registry's catalog/operation nodes
	// and the APIs the universe excludes by rule are not drift.
	candidates := map[string]sdkinv.Candidate{}
	known := map[string]bool{}
	services := map[string]bool{}
	for _, c := range u.Candidates {
		known[cc.RegistryKey(c)] = true
		services[strings.ToLower(c.Service)] = true
		if c.Class == sdkinv.ClassResource {
			candidates[cc.RegistryKey(c)] = c
		}
	}
	seen := map[string]bool{}
	for _, r := range registry {
		id := cc.CanonicalKey(r.Key)
		if seen[id] {
			continue
		}
		seen[id] = true
		if known[id] || !services[strings.ToLower(r.Service)] {
			continue
		}
		m.Rows = append(m.Rows, Row{Provider: m.Provider, Service: r.Service, Key: r.Key, Bucket: BucketRegistryDrift, Reason: ReasonRegistryOnly})
	}
	for id, c := range candidates {
		if seen[id] {
			continue
		}
		m.Rows = append(m.Rows, Row{Provider: m.Provider, Service: c.Service, Key: c.Key, Depth: c.Depth, Bucket: BucketRegistryDrift, Reason: ReasonCandidateOnly})
	}
	sortRows(m.Rows)
}

// Filter keeps the rows matching filter ("all", a bucket, or "gaps" =
// uncovered ∪ unexplained disco-only) and, when services is non-empty, one of
// the named services. Summaries are untouched: percent is computed before
// filtering.
func Filter(rows []Row, filter string, services []string) []Row {
	allowed := map[string]bool{}
	for _, s := range services {
		allowed[strings.ToLower(s)] = true
	}
	out := make([]Row, 0, len(rows))
	for _, r := range rows {
		switch filter {
		case "all":
		case "gaps":
			if r.Bucket != BucketUncovered && (r.Bucket != BucketDiscoOnly || r.Reason != ReasonUnexplained) {
				continue
			}
		case FilterScannerLists:
			if !slices.Contains(r.Signals, SignalScannerLists) {
				continue
			}
		default:
			if string(r.Bucket) != filter {
				continue
			}
		}
		if len(allowed) > 0 && !allowed[strings.ToLower(r.Service)] {
			continue
		}
		out = append(out, r)
	}
	return out
}

// Filters lists the accepted --filter values.
var Filters = []string{"all", "covered", "uncovered", "attribute", "excluded", "disco-only", "gaps", "registry-drift", FilterScannerLists}

// FilterScannerLists narrows to excluded rows a scanner provably lists — the
// worklist for fixing the per-provider class rules.
const FilterScannerLists = "scanner-lists"

func summarize(rows []Row) (Summary, []ServiceSummary) {
	var s Summary
	depth := map[int]*DepthSummary{}
	svc := map[string]*ServiceSummary{}
	for _, r := range rows {
		switch r.Bucket {
		case BucketCovered, BucketUncovered:
			d := depth[r.Depth]
			if d == nil {
				d = &DepthSummary{Depth: r.Depth}
				depth[r.Depth] = d
			}
			v := svc[r.Service]
			if v == nil {
				v = &ServiceSummary{Service: r.Service}
				svc[r.Service] = v
			}
			if r.Bucket == BucketCovered {
				s.Covered++
				d.Covered++
				v.Covered++
			} else {
				s.Uncovered++
				d.Uncovered++
				v.Uncovered++
			}
		case BucketAttribute:
			s.Attribute++
		case BucketExcluded:
			s.Excluded++
		case BucketDiscoOnly:
			s.DiscoOnly++
			if r.Reason == ReasonUnexplained {
				s.Unexplained++
			}
		}
	}
	s.Percent = percent(s.Covered, s.Uncovered)
	for _, d := range depth {
		d.Percent = percent(d.Covered, d.Uncovered)
		s.ByDepth = append(s.ByDepth, *d)
	}
	sort.Slice(s.ByDepth, func(i, j int) bool { return s.ByDepth[i].Depth < s.ByDepth[j].Depth })
	services := make([]ServiceSummary, 0, len(svc))
	for _, v := range svc {
		v.Percent = percent(v.Covered, v.Uncovered)
		services = append(services, *v)
	}
	// Largest gaps first: the list is the work queue.
	sort.Slice(services, func(i, j int) bool {
		if services[i].Uncovered != services[j].Uncovered {
			return services[i].Uncovered > services[j].Uncovered
		}
		return services[i].Service < services[j].Service
	})
	return s, services
}

func percent(covered, uncovered int) float64 {
	if covered+uncovered == 0 {
		return 0
	}
	return 100 * float64(covered) / float64(covered+uncovered)
}

var bucketOrder = map[Bucket]int{
	BucketCovered: 0, BucketUncovered: 1, BucketAttribute: 2, BucketExcluded: 3, BucketDiscoOnly: 4, BucketRegistryDrift: 5,
}

func sortRows(rows []Row) {
	sort.SliceStable(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		if bucketOrder[a.Bucket] != bucketOrder[b.Bucket] {
			return bucketOrder[a.Bucket] < bucketOrder[b.Bucket]
		}
		if a.Service != b.Service {
			return a.Service < b.Service
		}
		if a.Key != b.Key {
			return a.Key < b.Key
		}
		return a.DiscoType < b.DiscoType
	})
}

func hasSignal(c sdkinv.Candidate, s string) bool {
	for _, v := range c.Signals {
		if v == s {
			return true
		}
	}
	return false
}

// candidateIdent is the candidate's name identity: canonical service plus the
// canonical singular of every key segment ("microsoft.compute/virtualmachines/
// extensions" → "microsoftcompute/virtualmachine/extension").
func candidateIdent(c sdkinv.Candidate) string {
	segs := strings.Split(strings.TrimPrefix(c.Key, c.Service+"/"), "/")
	for i, s := range segs {
		segs[i] = sdkinv.Ident(s)
	}
	return sdkinv.Canon(c.Service) + "/" + strings.Join(segs, "/")
}

// typeIdents are the identities a disco type answers to: its full path and,
// for a flat type naming a nested collection ("gcp:cloudkms:crypto-key" for
// cloudkms/keyrings/cryptokeys), the leaf alone is matched by bestType.
func typeIdents(discoType string) []string {
	parts := strings.SplitN(discoType, ":", 3)
	if len(parts) != 3 {
		return nil
	}
	segs := strings.Split(parts[2], ":")
	for i, s := range segs {
		segs[i] = sdkinv.Ident(s)
	}
	return []string{sdkinv.Canon(parts[1]) + "/" + strings.Join(segs, "/")}
}

// assignPairedTypes records which emitted types a candidate is credited with:
// the one to display, the whole set when there is more than one, and every one
// of them as accounted for.
func assignPairedTypes(row *Row, paired map[string]bool, c sdkinv.Candidate, accounted map[string]bool) {
	row.DiscoType = bestType(paired, c)
	if row.DiscoType == "" {
		row.Reason = ReasonMultiType
	}
	for t := range paired {
		accounted[t] = true
		if t != row.DiscoType {
			row.DiscoTypes = append(row.DiscoTypes, t)
		}
	}
	if len(paired) > 1 {
		if row.DiscoType != "" {
			row.DiscoTypes = append(row.DiscoTypes, row.DiscoType)
		}
		sort.Strings(row.DiscoTypes)
	}
}

// scopeSpecificity ranks a candidate's scopes narrowest-first. A GCP
// collection listable per project and per organization is scanned per project,
// and ops sort by label, so "billingAccounts." and "folders." outranked
// "projects." on 117 rows. An unranked scope is widest.
var scopeSpecificity = map[string]int{
	string(sdkinv.ScopeProject): 8, string(sdkinv.ScopeResourceGroup): 7, string(sdkinv.ScopeSubscription): 7,
	string(sdkinv.ScopeAccount): 6, string(sdkinv.ScopeRegion): 6, string(sdkinv.ScopeOrg): 5,
	string(sdkinv.ScopeFolder): 4, string(sdkinv.ScopeBillingAccount): 3, string(sdkinv.ScopeManagementGroup): 3,
	string(sdkinv.ScopeTenant): 2, string(sdkinv.ScopeExtension): 2, string(sdkinv.ScopeGlobal): 1,
}

// opCells lists a candidate's op labels and its narrowest scope. Sibling
// models and per-version GCP documents repeat a label, so 930 rows rendered a
// duplicated ops cell; Ops is never read back, so the fold is presentation only.
func opCells(ops []sdkinv.Operation) ([]string, string) {
	var labels []string
	scope := ""
	for _, op := range ops {
		if !slices.Contains(labels, op.Label) {
			labels = append(labels, op.Label)
		}
		if s := string(op.Scope); s != "" && (scope == "" || scopeSpecificity[s] > scopeSpecificity[scope]) {
			scope = s
		}
	}
	return labels, scope
}

// contradicted reports that the pairing ran and could not explain the type.
// A name match then has to yield: the disco-only row it would suppress is the
// only thing --check-strict exits on.
func contradicted(in Inputs, discoType string) bool {
	return in.Pairings != nil && in.Unpaired[discoType] == ReasonUnexplained
}

// bestType picks the paired type to display for a candidate: the one whose
// identity matches the key, else the one sharing its leaf. With several types
// paired and neither tier matching, there is no answer — the alphabetically
// first is a coin toss that showed the diagnostic-settings dispatcher as
// "azure:microsoft.apimanagement:service" — so it returns "" and the caller
// records ReasonMultiType. A lone paired type is always the answer.
func bestType(paired map[string]bool, c sdkinv.Candidate) string {
	names := make([]string, 0, len(paired))
	for t := range paired {
		names = append(names, t)
	}
	sort.Strings(names)
	want := candidateIdent(c)
	leaf := want[strings.LastIndex(want, "/")+1:]
	for _, t := range names {
		for _, id := range typeIdents(t) {
			if id == want {
				return t
			}
		}
	}
	for _, t := range names {
		for _, id := range typeIdents(t) {
			if id[strings.LastIndex(id, "/")+1:] == leaf {
				return t
			}
		}
	}
	if len(names) == 1 {
		return names[0]
	}
	return ""
}

// TypeRefs maps paired disco types to the union of their candidates' Refs:
// the element fields a resolver for that type could follow. A type with no
// refs is a hint that it may be a derived leaf, never proof of one.
//
// The primary type takes the row's refs; a secondary type from Row.DiscoTypes
// takes them only when it matches the candidate's identity or shares its leaf.
// That narrowness is the point: the five aws:docdb:* orphans do share
// rds/dbinstance's leaf and were starved of its 44 refs, while the types a
// dispatcher's derived pairing sweeps up span a whole service and must not
// inherit the application gateway's 280 fields.
func TypeRefs(m Matrix) map[string][]string {
	sets := map[string]map[string]bool{}
	add := func(t string, refs []string) {
		if t == "" {
			return
		}
		if sets[t] == nil {
			sets[t] = map[string]bool{}
		}
		for _, r := range refs {
			sets[t][r] = true
		}
	}
	for _, r := range m.Rows {
		add(r.DiscoType, r.Refs)
		if len(r.Refs) == 0 || len(r.DiscoTypes) == 0 {
			continue
		}
		want := candidateIdent(sdkinv.Candidate{Service: r.Service, Key: r.Key})
		leaf := want[strings.LastIndex(want, "/")+1:]
		for _, t := range r.DiscoTypes {
			if t == r.DiscoType {
				continue
			}
			for _, id := range typeIdents(t) {
				if id == want || id[strings.LastIndex(id, "/")+1:] == leaf {
					add(t, r.Refs)
					break
				}
			}
		}
	}
	out := make(map[string][]string, len(sets))
	for t, set := range sets {
		refs := make([]string, 0, len(set))
		for r := range set {
			refs = append(refs, r)
		}
		sort.Strings(refs)
		out[t] = refs
	}
	return out
}
