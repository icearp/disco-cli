package store

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	sq "github.com/Masterminds/squirrel"
)

// hexResourceIDRE matches a full 32-hex-char resource ID (output of ResourceID).
var hexResourceIDRE = regexp.MustCompile(`^[0-9a-f]{32}$`)

// Resource represents a discovered cloud resource.
//
// Wire shape ≠ storage shape: AttributesJSON / TagsJSON live as JSON strings
// in the DB but MarshalJSON / UnmarshalJSON surface them as nested
// `attributes` / `tags` objects on the wire, with snake_case keys matching
// policy.Finding and coverage.Row.
//
// Contract: every key documented under `disco check --help` is always
// present on the wire (never dropped by `,omitempty`). Optional pointer
// fields render as `null` when unset; `tags` always emits as an object
// (possibly empty); `managed_by_provider` always emits its bool. Drift here
// is the F6 paper-cut from focus-group/SUMMARY.md — fix at this struct, not
// in each command's renderer.
type Resource struct {
	ID                string  `db:"id"                  json:"id"`
	Provider          string  `db:"provider"            json:"provider"`
	AccountID         string  `db:"account_id"          json:"account_id"`
	AccountName       *string `db:"account_name"        json:"account_name"`
	Type              string  `db:"type"                json:"type"`
	NativeID          string  `db:"native_id"           json:"native_id"`
	Name              *string `db:"name"                json:"name"`
	Region            *string `db:"region"              json:"region"`
	Zone              *string `db:"zone"                json:"zone"`
	Status            *string `db:"status"              json:"status"`
	TagsJSON          *string `db:"tags"                json:"-"` // surfaced as `tags` via MarshalJSON
	AttributesJSON    string  `db:"attributes"          json:"-"` // surfaced as `attributes` via MarshalJSON
	CreatedAt         *string `db:"created_at"          json:"created_at"`
	DiscoveredAt      string  `db:"discovered_at"       json:"discovered_at"`
	DiscoveredBy      string  `db:"discovered_by"       json:"discovered_by"`
	ManagedByProvider bool    `db:"managed_by_provider" json:"managed_by_provider"`
	WorkspaceID       *string `db:"workspace_id"        json:"-"` // per-workspace RLS discriminator; nil when the disco-saas app.workspace_id GUC was unset

	// Verification + version-chain metadata live on ResourceVersion
	// (resources_versioning.go), not here. Resource is the base type — any
	// field added here cascades to ResourceVersion via its embedded Resource.
}

// resourceWire is the on-wire shape: SDK-shape attributes/tags surfaced as
// nested values, scalar fields inherited from Resource via embedding.
type resourceWire struct {
	resourceAlias
	Attributes json.RawMessage `json:"attributes"`
	Tags       json.RawMessage `json:"tags"`
}

// resourceAlias avoids infinite recursion in MarshalJSON / UnmarshalJSON.
type resourceAlias Resource

// MarshalJSON emits Resource with snake_case keys and nested
// `attributes` / `tags` rather than stringified JSON blobs. Empty / missing /
// malformed blobs render as `{}` so consumers can always traverse
// `input.attributes.X` without a presence check.
func (r Resource) MarshalJSON() ([]byte, error) {
	w := resourceWire{
		resourceAlias: resourceAlias(r),
		Attributes:    json.RawMessage(`{}`),
		Tags:          json.RawMessage(`{}`),
	}
	if r.AttributesJSON != "" && json.Valid([]byte(r.AttributesJSON)) {
		w.Attributes = json.RawMessage(r.AttributesJSON)
	}
	if r.TagsJSON != nil && *r.TagsJSON != "" && json.Valid([]byte(*r.TagsJSON)) {
		w.Tags = json.RawMessage(*r.TagsJSON)
	}
	return json.Marshal(w)
}

// UnmarshalJSON reverses MarshalJSON: nested `attributes` / `tags` objects
// fold back into AttributesJSON / TagsJSON strings so []Resource round-trips
// byte-stably through encode → decode.
func (r *Resource) UnmarshalJSON(data []byte) error {
	w := resourceWire{resourceAlias: resourceAlias(*r)}
	if err := json.Unmarshal(data, &w); err != nil {
		return err
	}
	*r = Resource(w.resourceAlias)
	if len(w.Attributes) > 0 {
		r.AttributesJSON = string(w.Attributes)
	}
	if len(w.Tags) > 0 {
		s := string(w.Tags)
		r.TagsJSON = &s
	}
	return nil
}

// idHashBytes is the number of SHA-256 prefix bytes in a resource ID. 16
// bytes hex-encode to the 32-char IDs documented in store/CLAUDE.md — short
// enough for tabular output (cf. cmd.short()) yet long enough that
// collisions within one account are vanishingly rare.
const idHashBytes = 16

// ResourceID computes a stable deterministic ID for a resource. type is
// deliberately excluded — a resource's identity is (provider, account, native_id);
// its type is a versioned attribute (a type change supersedes, it does not fork).
func ResourceID(provider, accountID, nativeID string) string {
	h := sha256.Sum256([]byte(provider + "|" + accountID + "|" + nativeID))
	return fmt.Sprintf("%x", h[:idHashBytes])
}

// UpsertResource inserts or replaces a single resource. Delegates to
// UpsertResources, whose version-chain logic lives in resources_upsert.go.
func (s *Store) UpsertResource(r *Resource) (int, error) {
	return s.UpsertResources([]*Resource{r})
}

// ResourceFilter defines optional filters for ListResources.
type ResourceFilter struct {
	Providers    []string
	AccountID    string
	Types        []string
	ExcludeTypes []string
	Regions      []string
	Status       string
	// DiscoveredBy, when set, restricts results to rows inserted by the named
	// scan id (matches `discovered_by`). The paid build adds a verified_by
	// branch via ResourceFilterPaid + ListResourcesPaid; the OSS-shared filter
	// only knows about discovery.
	DiscoveredBy string
	// DiscoveredSince filters rows whose discovered_at >= this RFC3339
	// timestamp. Stored timestamps sort lexicographically same as
	// chronologically, so plain string comparison works. Pairs with
	// DiscoveredBefore for half-open `[since, before)` queries.
	DiscoveredSince string
	// DiscoveredBefore filters rows whose discovered_at < this RFC3339
	// timestamp (strict). Used for "stale" hygiene queries and as the upper
	// half of a half-open interval with DiscoveredSince.
	DiscoveredBefore string
	// CreatedBefore filters rows whose created_at < this RFC3339 timestamp.
	// Anchored on the resource's intrinsic CreateDate (lifted from the SDK at
	// scan time), NOT discovered_at. Rows with NULL created_at are excluded;
	// not every scanner lifts the SDK timestamp yet (see EBS volume precedent
	// in commit 8e61c52).
	CreatedBefore string
	// CreatedSince filters rows whose created_at >= this RFC3339 timestamp.
	// Pairs with CreatedBefore for half-open interval queries on intrinsic
	// age. Same NULL caveat as CreatedBefore.
	CreatedSince string
	TagKey       string
	TagValue     string
	Limit        uint64
	Offset       uint64
	// ID, when set, restricts the result to a single row by primary key.
	// Mirrors a `WHERE id = ?` short-circuit.
	ID string
	// IncludeManaged when false hides provider-managed resources (built-in
	// roles, AWS-owned prefix lists, etc.). Defaults false at the SQL layer.
	IncludeManaged bool
	// SkipGlobals, when true, excludes rows whose region = "global". Used by
	// `disco resources --exclude-global-region` and friends to opt out of the
	// default "include globals when filtering by --regions" behaviour.
	SkipGlobals bool
}

// ListResources returns resources matching the given filters.
func (s *Store) ListResources(f ResourceFilter) ([]Resource, error) {
	q := applyCurrentVersionPredicate(sq.Select(resourceSelectColumns()...).From("resources"))

	if len(f.Providers) > 0 {
		q = q.Where(sq.Eq{"provider": f.Providers})
	}
	if f.AccountID != "" {
		q = q.Where(sq.Eq{"account_id": f.AccountID})
	}
	if len(f.Types) > 0 {
		q = q.Where(sq.Eq{"type": f.Types})
	}
	if len(f.ExcludeTypes) > 0 {
		q = q.Where(sq.NotEq{"type": f.ExcludeTypes})
	}
	if len(f.Regions) > 0 {
		// `--regions us-east-1` intuitively means "show me what's scoped to
		// us-east-1". Global resources (Region="global") sit logically in
		// every region, so folding them in by default matches that mental
		// model. `--exclude-global-region` opts out for callers wanting
		// strictly region-scoped rows.
		if f.SkipGlobals {
			q = q.Where(sq.Eq{"region": f.Regions})
		} else {
			q = q.Where(sq.Or{
				sq.Eq{"region": f.Regions},
				sq.Eq{"region": "global"},
			})
		}
	} else if f.SkipGlobals {
		q = q.Where(sq.NotEq{"region": "global"})
	}
	if f.Status != "" {
		q = q.Where(sq.Eq{"status": f.Status})
	}
	if f.DiscoveredBy != "" {
		q = q.Where(sq.Eq{"discovered_by": f.DiscoveredBy})
	}
	if f.ID != "" {
		q = q.Where(sq.Eq{resourceIDColumn(): f.ID})
	}
	if f.DiscoveredSince != "" {
		q = q.Where(sq.GtOrEq{"discovered_at": f.DiscoveredSince})
	}
	if f.DiscoveredBefore != "" {
		q = q.Where(sq.Lt{"discovered_at": f.DiscoveredBefore})
	}
	if f.CreatedSince != "" {
		q = q.Where(sq.GtOrEq{"created_at": f.CreatedSince})
	}
	if f.CreatedBefore != "" {
		q = q.Where(sq.Lt{"created_at": f.CreatedBefore})
	}
	switch {
	case f.TagKey != "" && f.TagValue != "":
		frag, arg := s.tagJSONFilter(f.TagKey)
		q = q.Where(frag+" = ?", arg, f.TagValue)
	case f.TagKey != "":
		frag, arg := s.tagJSONFilter(f.TagKey)
		q = q.Where(frag+" IS NOT NULL", arg)
	case f.TagValue != "":
		// Value-only match: any tag whose value matches, regardless of key.
		q = q.Where(s.tagJSONValueExists(), f.TagValue)
	}
	if !f.IncludeManaged {
		q = q.Where(sq.Eq{"managed_by_provider": false})
	}

	limit := f.Limit
	if limit == 0 {
		limit = 500
	}
	q = q.Limit(limit).Offset(f.Offset).OrderBy("provider", "type", "name")

	query, args, err := q.PlaceholderFormat(s.placeholder()).ToSql()
	if err != nil {
		return nil, fmt.Errorf("build query: %w", err)
	}

	var results []Resource
	if err := s.selectAll(&results, query, args...); err != nil {
		return nil, fmt.Errorf("list resources: %w", err)
	}
	return results, nil
}

// GetResource retrieves a single resource by its caller-facing ID (the
// deterministic ResourceID hash). Under paid, resolves to the current
// version row of the chain whose root_id matches.
func (s *Store) GetResource(id string) (*Resource, error) {
	q := applyCurrentVersionPredicate(
		sq.Select(resourceSelectColumns()...).From("resources").
			Where(sq.Eq{resourceIDColumn(): id}),
	)
	query, args, err := q.PlaceholderFormat(s.placeholder()).ToSql()
	if err != nil {
		return nil, fmt.Errorf("build query: %w", err)
	}
	var r Resource
	if err := s.get(&r, query, args...); err != nil {
		return nil, fmt.Errorf("get resource %s: %w", id, err)
	}
	return &r, nil
}

// UnmarshalAttributes unmarshals the resource's JSON attributes into v.
func (r *Resource) UnmarshalAttributes(v any) error {
	return json.Unmarshal([]byte(r.AttributesJSON), v)
}

// CountResourcesByScan returns the number of resources recorded under a scan ID.
func (s *Store) CountResourcesByScan(scanID string) (int, error) {
	var n int
	err := s.get(&n, "SELECT COUNT(*) FROM resources WHERE discovered_by = ?", scanID)
	return n, err
}

// CountManaged returns the count of provider-managed rows in resources —
// the population a customer-only query (IncludeManaged=false) hides. Used by
// `disco check` to print the excluded-managed count alongside the evaluated
// count so SecEng/Compliance personas don't misread the small denominator as
// "no resources".
func (s *Store) CountManaged() (int, error) {
	var n int
	err := s.get(&n, "SELECT COUNT(*) FROM resources WHERE managed_by_provider = 1")
	return n, err
}

// Tags unmarshals the resource's JSON tags into a string map.
func (r *Resource) Tags() (map[string]string, error) {
	if r.TagsJSON == nil {
		return nil, nil
	}
	var tags map[string]string
	return tags, json.Unmarshal([]byte(*r.TagsJSON), &tags)
}

// ResolveResource finds a resource by its 32-hex ID, an 8+-char hex prefix
// of an ID, an exact native_id/name, or a substring of native_id/name.
// Disambiguation flags (provider/rtype/account) narrow the result set;
// ambiguity surfaces as a multi-candidate error.
//
// Two-pass matcher: exact native_id/name first, then prefix-on-id and
// substring-on-native_id/name, so paste-the-short-ID workflows ("the ticket
// says 8895a0bd") work the same as full IDs. F12 fix from
// focus-group/SUMMARY.md.
func (s *Store) ResolveResource(arg, provider, rtype, account string) (*Resource, error) {
	if hexResourceIDRE.MatchString(arg) {
		return s.GetResource(arg)
	}

	// Pass 1: exact match on native_id or name. Most callers pass full values
	// from `disco resources` output and want a deterministic hit before
	// falling back to fuzzy matching.
	rows, err := s.resolveQuery(
		sq.Or{sq.Eq{"native_id": arg}, sq.Eq{"name": arg}},
		provider, rtype, account,
	)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 && isLikelyHexPrefix(arg) {
		// Pass 2a: ID-prefix match for paste-the-short-ID flows. Anchored at
		// the start of `id` so middle-of-hex collisions don't surface;
		// minimum length 4 hex keeps noise out.
		rows, err = s.resolveQuery(
			sq.Like{resourceIDColumn(): arg + "%"},
			provider, rtype, account,
		)
		if err != nil {
			return nil, err
		}
	}
	if len(rows) == 0 {
		// Pass 2b: substring on native_id or name. Useful for ARN tails,
		// partial bucket names, etc. Wildcard both sides — small DBs won't
		// notice; large fleets should pass --provider / --type to narrow.
		rows, err = s.resolveQuery(
			sq.Or{sq.Like{"native_id": "%" + arg + "%"}, sq.Like{"name": "%" + arg + "%"}},
			provider, rtype, account,
		)
		if err != nil {
			return nil, err
		}
	}

	switch len(rows) {
	case 0:
		return nil, fmt.Errorf("no resource matching %q (id-prefix, native_id, or name)%s", arg, resolveFilterSuffix(provider, rtype, account))
	case 1:
		return &rows[0], nil
	default:
		var lines []string
		for _, r := range rows {
			region := ""
			if r.Region != nil {
				region = *r.Region
			}
			lines = append(lines, fmt.Sprintf("  %s  %s  %s  %s", r.Type, r.AccountID, region, r.ID))
		}
		return nil, fmt.Errorf(
			"ambiguous identifier %q (%d matches) — add --provider / --type / --account:\n%s",
			arg, len(rows), strings.Join(lines, "\n"),
		)
	}
}

// resolveQuery runs a single ResolveResource pass with the given match
// predicate, applying provider/rtype/account narrowing and returning the
// raw rows. Caller decides what to do with empty/single/multi results.
func (s *Store) resolveQuery(match sq.Sqlizer, provider, rtype, account string) ([]Resource, error) {
	q := applyCurrentVersionPredicate(sq.Select(resourceSelectColumns()...).From("resources")).Where(match)
	if provider != "" {
		q = q.Where(sq.Eq{"provider": provider})
	}
	if rtype != "" {
		q = q.Where(sq.Eq{"type": rtype})
	}
	if account != "" {
		q = q.Where(sq.Eq{"account_id": account})
	}
	q = q.Limit(50)
	query, args, err := q.PlaceholderFormat(s.placeholder()).ToSql()
	if err != nil {
		return nil, err
	}
	var rows []Resource
	if err := s.selectAll(&rows, query, args...); err != nil {
		return nil, fmt.Errorf("resolve resource: %w", err)
	}
	return rows, nil
}

// isLikelyHexPrefix returns true when arg is 4-31 lowercase-hex chars —
// the shape of a `disco resources` short-ID paste. Avoids triggering the
// id-prefix fallback for arbitrary short strings.
func isLikelyHexPrefix(arg string) bool {
	if len(arg) < 4 || len(arg) >= 32 {
		return false
	}
	for _, c := range arg {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

// resolveFilterSuffix formats active disambiguation filters for error messages.
func resolveFilterSuffix(provider, rtype, account string) string {
	var parts []string
	if provider != "" {
		parts = append(parts, "provider="+provider)
	}
	if rtype != "" {
		parts = append(parts, "type="+rtype)
	}
	if account != "" {
		parts = append(parts, "account="+account)
	}
	if len(parts) == 0 {
		return ""
	}
	return " in " + strings.Join(parts, ", ")
}

// DescendantsOf returns all resources descended from parentID (any depth).
// Caller passes a deterministic ResourceID hash; hierarchy_closure stores
// hashes, so the join key on r is resourceIDColumn() — `id` in OSS, `root_id`
// in paid.
func (s *Store) DescendantsOf(parentID string, f ResourceFilter) ([]Resource, error) {
	q := applyCurrentVersionPredicate(
		sq.Select(resourceSelectColumnsPrefixed("r")...).
			From("resources r").
			Join("hierarchy_closure hc ON hc.descendant_id = r." + resourceIDColumn()).
			Where(sq.Eq{"hc.ancestor_id": parentID}).
			Where(sq.Gt{"hc.depth": 0}),
	)

	if len(f.Providers) > 0 {
		q = q.Where(sq.Eq{"r.provider": f.Providers})
	}
	if len(f.Types) > 0 {
		q = q.Where(sq.Eq{"r.type": f.Types})
	}
	if f.Status != "" {
		q = q.Where(sq.Eq{"r.status": f.Status})
	}

	query, args, err := q.PlaceholderFormat(s.placeholder()).ToSql()
	if err != nil {
		return nil, err
	}
	var results []Resource
	return results, s.selectAll(&results, query, args...)
}
