package store

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	sq "github.com/Masterminds/squirrel"

	"codeberg.org/icearp/disco/internal/redact"
)

// hexResourceIDRE matches a full 32-hex-char resource ID (output of ResourceID).
var hexResourceIDRE = regexp.MustCompile(`^[0-9a-f]{32}$`)

// Resource represents a discovered cloud resource.
//
// Wire shape ≠ storage shape: AttributesJSON / TagsJSON live as JSON strings
// in the DB but MarshalJSON / UnmarshalJSON surface them as nested
// `attributes` / `tags` objects on the wire. JSON keys are snake_case to
// match policy.Finding and coverage.Row.
//
// Contract: every key documented under `disco check --help` is always
// present on the wire (never dropped by `,omitempty`). Optional pointer
// fields render as `null` when unset; `tags` always emits as an object
// (possibly empty); `managed_by_provider` always emits its bool. Drift
// here is the F6 paper-cut from focus-group/SUMMARY.md — fix at this
// struct, not in each command's renderer.
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
	VerifiedAt        *string `db:"verified_at"         json:"verified_at"`
	VerifiedBy        *string `db:"verified_by"         json:"verified_by"`
	ManagedByProvider bool    `db:"managed_by_provider" json:"managed_by_provider"`
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

// idHashBytes is the number of SHA-256 prefix bytes used in a resource ID.
// 16 bytes hex-encode to the 32-char IDs documented in store/CLAUDE.md;
// the prefix is short enough to fit in tabular output (cf. cmd.short()) yet
// long enough that collisions within a single account are vanishingly rare.
const idHashBytes = 16

// ResourceID computes a stable deterministic ID for a resource.
func ResourceID(provider, accountID, resourceType, nativeID string) string {
	h := sha256.Sum256([]byte(provider + "|" + accountID + "|" + resourceType + "|" + nativeID))
	return fmt.Sprintf("%x", h[:idHashBytes])
}

// UpsertResource inserts or replaces a single resource. Delegates to UpsertResources.
// Returns the number of newly inserted resources (0 or 1).
func (s *Store) UpsertResource(r *Resource) (int, error) {
	return s.UpsertResources([]*Resource{r})
}

// UpsertResources bulk-upserts resources in a single transaction.
// Returns the number of resources that were newly inserted (not previously in the DB).
func (s *Store) UpsertResources(resources []*Resource) (inserted int, err error) {
	now := time.Now().UTC().Format(time.RFC3339)

	// Assign IDs before the pre-query so we know which keys to check.
	for _, r := range resources {
		if r.ID == "" {
			r.ID = ResourceID(r.Provider, r.AccountID, r.Type, r.NativeID)
		}
		if r.DiscoveredAt == "" {
			r.DiscoveredAt = now
		}
		r.VerifiedAt = &now
		r.VerifiedBy = &r.DiscoveredBy
		r.AttributesJSON = redact.Apply(r.Type, r.AttributesJSON)
	}

	// Count how many of these IDs already exist in the DB.
	// inserted = len(resources) - existing.
	ids := make([]string, len(resources))
	for i, r := range resources {
		ids[i] = r.ID
	}
	q, args, _ := sq.Select("COUNT(*)").From("resources").
		Where(sq.Eq{"id": ids}).PlaceholderFormat(sq.Question).ToSql()
	var existing int
	if err := s.db.Get(&existing, q, args...); err != nil {
		return 0, fmt.Errorf("count existing resources: %w", err)
	}
	inserted = len(resources) - existing

	tx, err := s.db.Beginx()
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()
	for _, r := range resources {
		if _, err := tx.NamedExec(`
			INSERT INTO resources
				(id, provider, account_id, account_name, type, native_id, name,
				 region, zone, status, tags, attributes, created_at, discovered_at, discovered_by, verified_at, verified_by, managed_by_provider)
			VALUES
				(:id, :provider, :account_id, :account_name, :type, :native_id, :name,
				 :region, :zone, :status, :tags, :attributes, :created_at, :discovered_at, :discovered_by, :verified_at, :verified_by, :managed_by_provider)
			ON CONFLICT(id) DO UPDATE SET
				name                = excluded.name,
				status              = excluded.status,
				tags                = excluded.tags,
				attributes          = excluded.attributes,
				verified_at         = excluded.verified_at,
				verified_by         = excluded.verified_by,
				managed_by_provider = excluded.managed_by_provider`, r); err != nil {
			return 0, fmt.Errorf("upsert resource %s: %w", r.ID, err)
		}
	}
	return inserted, tx.Commit()
}

// ResourceFilter defines optional filters for ListResources.
type ResourceFilter struct {
	Provider     string
	AccountID    string
	Types        []string
	ExcludeTypes []string
	Regions      []string
	Status       string
	DiscoveredBy string
	// ScanAs picks which scan-FK column DiscoveredBy filters on:
	//   "discovered" — `discovered_by = ?` (the scan that first inserted)
	//   "verified"   — `verified_by = ?`   (the scan that last re-verified)
	//   "" or "any"  — either column matches
	// Default ("any") matches the persona expectation that
	// `--scan-id <id>` returns rows the named scan touched, period — fix
	// for the F3 silent-zero-row drift workflow. Ignored when DiscoveredBy
	// is empty. Field name mirrors the CLI flag `--scan-as` (renamed from
	// `--scan-role` to avoid IAM-role cognitive collision).
	ScanAs string
	// DiscoveredSince filters rows whose discovered_at >= this RFC3339
	// timestamp. Stored timestamps sort lexicographically the same as
	// chronologically, so plain string comparison suffices. Pairs with
	// DiscoveredBefore for half-open `[since, before)` interval queries.
	DiscoveredSince string
	// DiscoveredBefore filters rows whose discovered_at < this RFC3339
	// timestamp (strict). Used for "stale" hygiene queries and as the
	// upper half of a half-open closed interval with DiscoveredSince.
	DiscoveredBefore string
	// CreatedBefore filters rows whose created_at < this RFC3339 timestamp.
	// Anchored on the resource's intrinsic CreateDate (lifted from the SDK
	// at scan time), NOT discovered_at. Rows with NULL created_at are
	// excluded from the result set; not every scanner lifts the SDK
	// timestamp yet (see EBS volume precedent in commit 8e61c52).
	CreatedBefore string
	// CreatedSince filters rows whose created_at >= this RFC3339 timestamp.
	// Pairs with CreatedBefore for half-open closed-interval queries on
	// intrinsic age. Same NULL caveat as CreatedBefore.
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
	// `disco list --skip-globals` and friends to opt out of the default
	// "include globals when filtering by --regions" behaviour.
	SkipGlobals bool
}

// ListResources returns resources matching the given filters.
func (s *Store) ListResources(f ResourceFilter) ([]Resource, error) {
	q := sq.Select("*").From("resources")

	if f.Provider != "" {
		q = q.Where(sq.Eq{"provider": f.Provider})
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
		// `--regions us-east-1` is intuitively "show me what's scoped to
		// us-east-1". Global resources (Region="global") sit logically in
		// every region; folding them in by default matches that mental
		// model. `--skip-globals` opts out for callers that want strictly
		// region-scoped rows.
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
		switch f.ScanAs {
		case "discovered":
			q = q.Where(sq.Eq{"discovered_by": f.DiscoveredBy})
		case "verified":
			q = q.Where(sq.Eq{"verified_by": f.DiscoveredBy})
		default: // "" / "any"
			q = q.Where(sq.Or{
				sq.Eq{"discovered_by": f.DiscoveredBy},
				sq.Eq{"verified_by": f.DiscoveredBy},
			})
		}
	}
	if f.ID != "" {
		q = q.Where(sq.Eq{"id": f.ID})
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
		q = q.Where("json_extract(tags, ?) = ?", "$."+f.TagKey, f.TagValue)
	case f.TagKey != "":
		q = q.Where("json_extract(tags, ?) IS NOT NULL", "$."+f.TagKey)
	case f.TagValue != "":
		// Value-only match: any tag whose value matches, regardless of key.
		// json_each yields one row per tag entry so EXISTS short-circuits.
		q = q.Where("EXISTS (SELECT 1 FROM json_each(tags) WHERE json_each.value = ?)", f.TagValue)
	}
	if !f.IncludeManaged {
		q = q.Where(sq.Eq{"managed_by_provider": false})
	}

	limit := f.Limit
	if limit == 0 {
		limit = 500
	}
	q = q.Limit(limit).Offset(f.Offset).OrderBy("provider", "type", "name")

	query, args, err := q.PlaceholderFormat(sq.Question).ToSql()
	if err != nil {
		return nil, fmt.Errorf("build query: %w", err)
	}

	var results []Resource
	if err := s.db.Select(&results, query, args...); err != nil {
		return nil, fmt.Errorf("list resources: %w", err)
	}
	return results, nil
}

// GetResource retrieves a single resource by ID.
func (s *Store) GetResource(id string) (*Resource, error) {
	var r Resource
	if err := s.db.Get(&r, "SELECT * FROM resources WHERE id = ?", id); err != nil {
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
	err := s.db.Get(&n, "SELECT COUNT(*) FROM resources WHERE discovered_by = ?", scanID)
	return n, err
}

// CountManaged returns the count of provider-managed rows in the resources
// table — the population a customer-only query (IncludeManaged=false) hides.
// Used by `disco check` to print the excluded-managed count alongside the
// evaluated count so SecEng/Compliance personas don't misread the small
// denominator as "no resources".
func (s *Store) CountManaged() (int, error) {
	var n int
	err := s.db.Get(&n, "SELECT COUNT(*) FROM resources WHERE managed_by_provider = 1")
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

// ResolveResource finds a resource by either its 32-hex ID, an 8+-char
// hex prefix of an ID, an exact native_id/name, or a substring of
// native_id/name. Disambiguation flags (provider/rtype/account) narrow
// the result set; ambiguity surfaces as a multi-candidate error.
//
// Two-pass matcher: try exact native_id/name first, then fall back to
// prefix-on-id and substring-on-native_id/name so paste-the-short-ID
// workflows ("the ticket says 8895a0bd") work the same as full-IDs. F12
// fix from focus-group/SUMMARY.md.
func (s *Store) ResolveResource(arg, provider, rtype, account string) (*Resource, error) {
	if hexResourceIDRE.MatchString(arg) {
		return s.GetResource(arg)
	}

	// Pass 1: exact match on native_id or name. Most callers pass full
	// values from `disco list` output and want a deterministic hit before
	// falling back to fuzzy matching.
	rows, err := s.resolveQuery(
		sq.Or{sq.Eq{"native_id": arg}, sq.Eq{"name": arg}},
		provider, rtype, account,
	)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 && isLikelyHexPrefix(arg) {
		// Pass 2a: ID-prefix match for paste-the-short-ID flows. Anchored
		// at the start of `id` so unrelated middle-of-hex collisions don't
		// surface; minimum length 4 hex to keep noise out of substring.
		rows, err = s.resolveQuery(
			sq.Like{"id": arg + "%"},
			provider, rtype, account,
		)
		if err != nil {
			return nil, err
		}
	}
	if len(rows) == 0 {
		// Pass 2b: substring on native_id or name. Useful for ARN tails,
		// partial bucket names, etc. Wildcard both sides — small DBs
		// won't notice; large fleets should pass --provider / --type
		// to narrow.
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
	q := sq.Select("*").From("resources").Where(match)
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
	query, args, err := q.PlaceholderFormat(sq.Question).ToSql()
	if err != nil {
		return nil, err
	}
	var rows []Resource
	if err := s.db.Select(&rows, query, args...); err != nil {
		return nil, fmt.Errorf("resolve resource: %w", err)
	}
	return rows, nil
}

// isLikelyHexPrefix returns true when arg is 4-31 lowercase-hex chars —
// the shape of a `disco list` short-ID paste. Avoids triggering the
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

// DescendantsOf returns all resources that are descendants of parentID (any depth).
func (s *Store) DescendantsOf(parentID string, f ResourceFilter) ([]Resource, error) {
	q := sq.Select("r.*").
		From("resources r").
		Join("hierarchy_closure hc ON hc.descendant_id = r.id").
		Where(sq.Eq{"hc.ancestor_id": parentID}).
		Where(sq.Gt{"hc.depth": 0})

	if f.Provider != "" {
		q = q.Where(sq.Eq{"r.provider": f.Provider})
	}
	if len(f.Types) > 0 {
		q = q.Where(sq.Eq{"r.type": f.Types})
	}
	if f.Status != "" {
		q = q.Where(sq.Eq{"r.status": f.Status})
	}

	query, args, err := q.PlaceholderFormat(sq.Question).ToSql()
	if err != nil {
		return nil, err
	}
	var results []Resource
	return results, s.db.Select(&results, query, args...)
}
