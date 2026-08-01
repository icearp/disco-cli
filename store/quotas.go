package store

import (
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	sq "github.com/Masterminds/squirrel"
	"github.com/google/uuid"
)

// Quota is one service quota limit: a per-account ceiling the provider
// enforces, not a thing the customer provisioned.
//
// Quotas live in their own table rather than in resources because they differ
// on shape, access pattern and volume all three. The limit is a scalar with a
// unit, so `value` is a real NUMERIC column and questions like "which limits am
// I close to" or "which have I raised" are expressible in SQL rather than
// trapped inside an attribute blob. They carry no graph edges. And they
// outnumber real inventory by roughly nine to one, so keeping them here stops
// the provider's catalogue size from dictating how the inventory read path
// performs.
//
// Wire shape differs from storage shape the same way [Resource]'s does:
// AttributesJSON is a JSON string in the DB and surfaces as a nested
// `attributes` object via MarshalJSON.
//
// The identity is (provider, account, region, service code, quota code). ID
// carries the deterministic [QuotaID] hash of exactly that tuple and is shared
// by every row in the version chain, matching how Resource.ID carries root_id.
type Quota struct {
	ID             string   `db:"id"             json:"id"`
	Provider       string   `db:"provider"       json:"provider"`
	AccountID      string   `db:"account_id"     json:"accountId"`
	AccountName    *string  `db:"account_name"   json:"accountName"`
	Region         string   `db:"region"         json:"region"`
	ServiceCode    string   `db:"service_code"   json:"serviceCode"`
	ServiceName    *string  `db:"service_name"   json:"serviceName"`
	QuotaCode      string   `db:"quota_code"     json:"quotaCode"`
	Name           string   `db:"name"           json:"name"`
	Description    *string  `db:"description"    json:"description"`
	Unit           *string  `db:"unit"           json:"unit"`
	Value          *float64 `db:"value"          json:"value"`
	DefaultValue   *float64 `db:"default_value"  json:"defaultValue"`
	Adjustable     bool     `db:"adjustable"     json:"adjustable"`
	GlobalQuota    bool     `db:"global_quota"   json:"globalQuota"`
	AppliedLevel   *string  `db:"applied_level"  json:"appliedLevel"`
	AttributesJSON string   `db:"attributes"     json:"-"` // surfaced as `attributes` via MarshalJSON
	DiscoveredAt   string   `db:"discovered_at"  json:"discoveredAt"`
	DiscoveredBy   string   `db:"discovered_by"  json:"discoveredBy"`
	WorkspaceID    *string  `db:"workspace_id"   json:"-"` // per-workspace RLS discriminator; nil when the disco-saas app.workspace_id GUC was unset
}

// quotaWire is the on-wire shape: attributes surfaced as a nested value,
// scalars inherited from Quota via embedding.
type quotaWire struct {
	quotaAlias
	Attributes json.RawMessage `json:"attributes"`
}

// quotaAlias avoids infinite recursion in MarshalJSON / UnmarshalJSON.
type quotaAlias Quota

// MarshalJSON emits Quota with a nested `attributes` object rather than a
// stringified JSON blob. An empty or malformed blob renders as `{}` so
// consumers can always traverse it without a presence check.
func (q Quota) MarshalJSON() ([]byte, error) {
	w := quotaWire{quotaAlias: quotaAlias(q), Attributes: json.RawMessage(`{}`)}
	if q.AttributesJSON != "" && json.Valid([]byte(q.AttributesJSON)) {
		w.Attributes = json.RawMessage(q.AttributesJSON)
	}
	return json.Marshal(w)
}

// UnmarshalJSON reverses MarshalJSON so []Quota round-trips byte-stably
// through encode then decode.
func (q *Quota) UnmarshalJSON(data []byte) error {
	w := quotaWire{quotaAlias: quotaAlias(*q)}
	if err := json.Unmarshal(data, &w); err != nil {
		return err
	}
	*q = Quota(w.quotaAlias)
	if len(w.Attributes) > 0 {
		q.AttributesJSON = string(w.Attributes)
	}
	return nil
}

// QuotaVersion is the read shape carrying verification and version-chain
// metadata. It embeds Quota, so a field added there cascades here.
//
//   - VersionRowID is the per-row UUIDv7 primary key in the quotas table.
//   - RootID is the deterministic [QuotaID] hash shared by every row in this
//     quota's chain. Quota.ID (embedded) carries the same hash, because reads
//     alias `root_id AS id`.
//   - PreviousVersionID points at the immediate predecessor (NULL on the root).
//   - SupersededBy points at the successor that replaced this version (NULL on
//     the current row of every chain).
type QuotaVersion struct {
	Quota
	VerifiedAt        *string `db:"verified_at"         json:"verifiedAt"`
	VerifiedBy        *string `db:"verified_by"         json:"verifiedBy"`
	RootID            string  `db:"root_id"             json:"rootId"`
	PreviousVersionID *string `db:"previous_version_id" json:"previousVersionId"`
	SupersededBy      *string `db:"superseded_by"       json:"supersededBy"`
	VersionRowID      string  `db:"version_row_id"      json:"versionRowId"`
}

// QuotaID computes the stable identity hash for a quota. Unlike [ResourceID]
// the region is part of the key: a limit is applied per region, and the same
// service and quota code legitimately carry different values in each one.
func QuotaID(provider, accountID, region, serviceCode, quotaCode string) string {
	h := sha256.Sum256([]byte(provider + "|" + accountID + "|" + region + "|" + serviceCode + "|" + quotaCode))
	return fmt.Sprintf("%x", h[:idHashBytes])
}

// quotaSelectColumns is the projection for reads targeting *Quota. root_id is
// aliased to id so a Quota projection always carries the stable cross-version
// identity rather than the per-version row id, matching resourceSelectColumns.
func quotaSelectColumns() []string {
	return []string{
		"root_id AS id",
		"provider",
		"account_id",
		"account_name",
		"region",
		"service_code",
		"service_name",
		"quota_code",
		"name",
		"description",
		"unit",
		"value",
		"default_value",
		"adjustable",
		"global_quota",
		"applied_level",
		"attributes",
		"discovered_at",
		"discovered_by",
		"workspace_id",
	}
}

// quotaVersionColumns is the projection for reads targeting *QuotaVersion. It
// carries both `id AS version_row_id` and `root_id`, so chain metadata is fully
// populated.
func quotaVersionColumns() []string {
	return append(quotaSelectColumns()[1:],
		"id AS version_row_id",
		"root_id",
		"previous_version_id",
		"superseded_by",
		"verified_at",
		"verified_by",
	)
}

// QuotaFilter narrows [Store.ListQuotas]. A zero filter returns every current
// quota. Empty slices mean "not filtering on this axis", so a caller deriving
// one from a permission grant must decide separately whether an empty grant
// means everything or nothing — this type cannot tell those apart, exactly as
// [ResourceFilter] documents.
type QuotaFilter struct {
	Providers    []string
	AccountIDs   []string
	Regions      []string
	ServiceCodes []string
	// Adjustable, when non-nil, keeps only quotas whose adjustable flag
	// matches. The interesting setting is false: a non-adjustable limit
	// changes only when the provider changes it, with no customer request and
	// no notification.
	Adjustable *bool
	// RaisedOnly keeps only quotas whose applied value differs from the
	// provider default. On an adjustable quota that divergence is a limit
	// increase the customer requested; on a non-adjustable one it means the
	// provider moved a hard ceiling.
	RaisedOnly bool
	Limit      int
	Offset     int
}

// ListQuotas returns current quota rows matching the filter, ordered by
// service then name so paging is stable.
func (s *Store) ListQuotas(f QuotaFilter) ([]Quota, error) {
	q := sq.Select(quotaSelectColumns()...).From("quotas").
		Where("superseded_by IS NULL")

	if len(f.Providers) > 0 {
		q = q.Where(sq.Eq{"provider": f.Providers})
	}
	if len(f.AccountIDs) > 0 {
		q = q.Where(sq.Eq{"account_id": f.AccountIDs})
	}
	if len(f.Regions) > 0 {
		q = q.Where(sq.Eq{"region": f.Regions})
	}
	if len(f.ServiceCodes) > 0 {
		q = q.Where(sq.Eq{"service_code": f.ServiceCodes})
	}
	if f.Adjustable != nil {
		q = q.Where(sq.Eq{"adjustable": *f.Adjustable})
	}
	if f.RaisedOnly {
		q = q.Where("default_value IS NOT NULL AND value IS NOT NULL AND value <> default_value")
	}
	q = q.OrderBy("provider ASC", "account_id ASC", "service_code ASC", "name ASC", "region ASC")
	if f.Limit > 0 {
		q = q.Limit(uint64(f.Limit))
	}
	if f.Offset > 0 {
		q = q.Offset(uint64(f.Offset))
	}

	query, args, err := q.PlaceholderFormat(s.placeholder()).ToSql()
	if err != nil {
		return nil, fmt.Errorf("build query: %w", err)
	}
	var out []Quota
	if err := s.selectAll(&out, query, args...); err != nil {
		return nil, fmt.Errorf("list quotas: %w", err)
	}
	return out, nil
}

// GetQuota returns the current version of one quota by its stable [QuotaID]
// hash. Returns nil without error when no such quota exists.
func (s *Store) GetQuota(rootID string) (*Quota, error) {
	q := sq.Select(quotaSelectColumns()...).From("quotas").
		Where(sq.Eq{"root_id": rootID}).
		Where("superseded_by IS NULL").
		PlaceholderFormat(s.placeholder())
	query, args, err := q.ToSql()
	if err != nil {
		return nil, fmt.Errorf("build query: %w", err)
	}
	var out Quota
	if err := s.get(&out, query, args...); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, nil
		}
		return nil, fmt.Errorf("get quota: %w", err)
	}
	return &out, nil
}

// ResolveQuota finds one current quota from whatever the user pasted: a
// [QuotaID] hash, a quota code, a name, or a short-id prefix. It mirrors
// [Store.ResolveResource] so `disco history` accepts a quota id the same way it
// accepts a resource id.
//
// Returns an error naming the ambiguity when more than one quota matches,
// rather than picking one — two regions legitimately share a quota code, and
// silently choosing between them would show the wrong limit's history.
func (s *Store) ResolveQuota(arg string) (*Quota, error) {
	if hexResourceIDRE.MatchString(arg) {
		q, err := s.GetQuota(arg)
		if err != nil {
			return nil, err
		}
		if q == nil {
			return nil, fmt.Errorf("no quota with id %q", arg)
		}
		return q, nil
	}

	rows, err := s.resolveQuotaQuery(sq.Or{sq.Eq{"quota_code": arg}, sq.Eq{"name": arg}})
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 && isLikelyHexPrefix(arg) {
		if rows, err = s.resolveQuotaQuery(sq.Like{"root_id": arg + "%"}); err != nil {
			return nil, err
		}
	}
	if len(rows) == 0 {
		if rows, err = s.resolveQuotaQuery(sq.Like{"name": "%" + arg + "%"}); err != nil {
			return nil, err
		}
	}

	switch len(rows) {
	case 0:
		return nil, fmt.Errorf("no quota matching %q (id-prefix, quota code, or name)", arg)
	case 1:
		return &rows[0], nil
	default:
		lines := make([]string, 0, len(rows))
		for _, q := range rows {
			lines = append(lines, fmt.Sprintf("  %s  %s  %s  %s  %s",
				q.Provider, q.AccountID, q.Region, q.ServiceCode+"/"+q.QuotaCode, q.ID))
		}
		return nil, fmt.Errorf("ambiguous quota identifier %q (%d matches):\n%s",
			arg, len(rows), strings.Join(lines, "\n"))
	}
}

// resolveQuotaQuery runs one ResolveQuota pass with the given match predicate.
func (s *Store) resolveQuotaQuery(match sq.Sqlizer) ([]Quota, error) {
	q := sq.Select(quotaSelectColumns()...).From("quotas").
		Where("superseded_by IS NULL").Where(match).
		Limit(resolveQuotaMatchLimit).
		PlaceholderFormat(s.placeholder())
	query, args, err := q.ToSql()
	if err != nil {
		return nil, fmt.Errorf("build query: %w", err)
	}
	var out []Quota
	if err := s.selectAll(&out, query, args...); err != nil {
		return nil, fmt.Errorf("resolve quota: %w", err)
	}
	return out, nil
}

// resolveQuotaMatchLimit bounds the ambiguity report. A substring match against
// a catalogue this size can hit thousands of rows, and the caller only needs
// enough of them to know the identifier was not specific enough.
const resolveQuotaMatchLimit = 20

// GetQuotaVersions walks the full version chain for one quota and returns
// every row in chronological order, root first. This is the change history the
// separate table exists to preserve: each row is a limit the provider reported
// at a point in time, and a row whose adjustable flag is false records a
// ceiling the provider moved on its own.
func (s *Store) GetQuotaVersions(rootID string) ([]QuotaVersion, error) {
	q := sq.Select(quotaVersionColumns()...).From("quotas").
		Where(sq.Eq{"root_id": rootID}).
		OrderBy("discovered_at ASC", "id ASC").
		PlaceholderFormat(s.placeholder())
	query, args, err := q.ToSql()
	if err != nil {
		return nil, fmt.Errorf("build query: %w", err)
	}
	var out []QuotaVersion
	if err := s.selectAll(&out, query, args...); err != nil {
		return nil, fmt.Errorf("list quota versions: %w", err)
	}
	return out, nil
}

// UpsertQuotas bulk-upserts quotas under the same version-chain model
// [Store.UpsertResources] uses. Per quota:
//
//   - First discovery: INSERT a row with id = UUIDv7, root_id = the
//     deterministic [QuotaID] hash, verified_at = now.
//   - Unchanged rescan: UPDATE verified_at / verified_by in place. Display-only
//     columns (name, service_name, account_name) update with it, so a provider
//     rewording a quota's label does not manufacture a version. No new row.
//   - Changed rescan: INSERT a new row inheriting discovered_at /
//     discovered_by from the chain root, then point the predecessor's
//     superseded_by at it.
//
// What counts as changed is the limit itself — value, default_value,
// adjustable, global_quota, unit, applied_level and the attributes remainder.
// Both providers report limits only, with no usage figure, etag or timestamp
// in the payload, so an unchanged quota produces no new row. That property is
// what makes it safe to scan a catalogue of this size repeatedly, and it is
// structural here rather than dependent on JSON serialization order: the
// fields that decide it are typed columns.
//
// Unlike UpsertResources this runs on [Store.ext], so it works both on a pool
// and inside a caller-owned transaction from [WrapTx] — where s.db is nil and
// any s.db.Begin would nil-panic.
//
// Returns the count of rows inserted: first discoveries plus version splits.
// Verify-only updates do not count.
func (s *Store) UpsertQuotas(quotas []*Quota) (inserted int, err error) {
	now := time.Now().UTC().Format(time.RFC3339)

	for _, q := range quotas {
		if q.ID == "" {
			q.ID = QuotaID(q.Provider, q.AccountID, q.Region, q.ServiceCode, q.QuotaCode)
		}
		if q.AttributesJSON == "" {
			q.AttributesJSON = "{}"
		}
	}

	err = s.withWriteRetry("upsert quotas", func() error {
		var txErr error
		inserted, txErr = s.upsertQuotasTx(quotas, now)
		return txErr
	})
	if err != nil {
		return 0, err
	}
	return inserted, nil
}

// upsertQuotasTx runs one attempt of UpsertQuotas atomically. A caller-owned
// transaction is already atomic, so it is used directly; on the pool path a
// transaction is opened here and the work runs against a [WrapTx] view of it.
func (s *Store) upsertQuotasTx(quotas []*Quota, now string) (int, error) {
	if s.tx != nil {
		return s.upsertQuotasOn(quotas, now)
	}
	tx, err := s.db.Beginx()
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()
	n, err := WrapTx(tx, Driver(s.driver)).upsertQuotasOn(quotas, now)
	if err != nil {
		return 0, err
	}
	return n, tx.Commit()
}

// upsertQuotasOn does the row-by-row work against whatever [Store.ext]
// resolves to. It must never reach for s.db.
func (s *Store) upsertQuotasOn(quotas []*Quota, now string) (int, error) {
	inserted := 0
	for _, q := range quotas {
		existing, err := s.currentQuota(q)
		switch {
		case errors.Is(err, sql.ErrNoRows):
			n, err := s.insertFirstQuota(q, now)
			if err != nil {
				return 0, err
			}
			inserted += n

		case err != nil:
			return 0, fmt.Errorf("lookup current version for quota %s: %w", q.ID, err)

		case existing.unchanged(q):
			// Verify-only. Display columns still move so a reworded label or a
			// renamed account propagates without manufacturing a version.
			if _, err := s.exec(`
				UPDATE quotas
				   SET verified_at  = ?,
				       verified_by  = ?,
				       name         = ?,
				       description  = ?,
				       service_name = ?,
				       account_name = ?
				 WHERE id = ?`,
				now, q.DiscoveredBy, q.Name, q.Description, q.ServiceName, q.AccountName, existing.VersionRowID,
			); err != nil {
				return 0, fmt.Errorf("verify quota %s: %w", q.ID, err)
			}

		default:
			if err := s.splitQuota(q, existing, now); err != nil {
				return 0, err
			}
			inserted++
		}
	}
	return inserted, nil
}

// currentQuotaRow is the lookup projection: chain metadata plus the fields that
// decide whether the limit changed.
type currentQuotaRow struct {
	VersionRowID   string   `db:"version_row_id"`
	RootID         string   `db:"root_id"`
	Unit           *string  `db:"unit"`
	Value          *float64 `db:"value"`
	DefaultValue   *float64 `db:"default_value"`
	Adjustable     bool     `db:"adjustable"`
	GlobalQuota    bool     `db:"global_quota"`
	AppliedLevel   *string  `db:"applied_level"`
	AttributesJSON string   `db:"attributes"`
	DiscoveredAt   string   `db:"discovered_at"`
	DiscoveredBy   string   `db:"discovered_by"`
}

// unchanged reports whether the incoming quota carries the same limit as the
// stored row. Display-only columns are deliberately excluded — they update in
// place instead of splitting the chain.
func (c currentQuotaRow) unchanged(q *Quota) bool {
	return floatEqual(c.Value, q.Value) &&
		floatEqual(c.DefaultValue, q.DefaultValue) &&
		c.Adjustable == q.Adjustable &&
		c.GlobalQuota == q.GlobalQuota &&
		derefStr(c.Unit) == derefStr(q.Unit) &&
		derefStr(c.AppliedLevel) == derefStr(q.AppliedLevel) &&
		jsonEqual(c.AttributesJSON, q.AttributesJSON)
}

// floatEqual compares two optional limits, treating NULL as distinct from any
// value. Both sides originate as float64 from the provider SDKs and round-trip
// through NUMERIC unchanged, so exact equality is the right comparison: a
// tolerance would silently swallow a real limit change.
func floatEqual(a, b *float64) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

// currentQuota finds the live row for a quota's natural key. The partial unique
// index idx_quotas_current_by_natural_key makes this a single index lookup.
func (s *Store) currentQuota(q *Quota) (currentQuotaRow, error) {
	var out currentQuotaRow
	err := s.get(&out, `
		SELECT id AS version_row_id, root_id, unit, value, default_value,
		       adjustable, global_quota, applied_level, attributes,
		       discovered_at, discovered_by
		  FROM quotas
		 WHERE provider = ? AND account_id = ? AND service_code = ?
		   AND quota_code = ? AND region = ?
		   AND superseded_by IS NULL`,
		q.Provider, q.AccountID, q.ServiceCode, q.QuotaCode, q.Region)
	return out, err
}

// insertFirstQuota records a quota seen for the first time and returns how many
// rows it wrote.
//
// Scanners upsert concurrently, so a sibling goroutine can take this natural
// key between the lookup and this insert. ON CONFLICT DO NOTHING against the
// current-by-natural-key partial index turns that race into a no-op rather than
// a 23505 that would abort the whole batch — the sibling recorded the same
// limit from the same point-in-time scan, so nothing is lost.
func (s *Store) insertFirstQuota(q *Quota, now string) (int, error) {
	if q.DiscoveredAt == "" {
		q.DiscoveredAt = now
	}
	res, err := s.exec(`
		INSERT INTO quotas
			(id, root_id, provider, account_id, account_name, region,
			 service_code, service_name, quota_code, name, description, unit,
			 value, default_value, adjustable, global_quota, applied_level,
			 attributes, discovered_at, discovered_by, verified_at, verified_by)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (provider, account_id, service_code, quota_code, region)
		    WHERE superseded_by IS NULL
		DO NOTHING`,
		uuid.Must(uuid.NewV7()).String(), q.ID,
		q.Provider, q.AccountID, q.AccountName, q.Region,
		q.ServiceCode, q.ServiceName, q.QuotaCode, q.Name, q.Description, q.Unit,
		q.Value, q.DefaultValue, q.Adjustable, q.GlobalQuota, q.AppliedLevel,
		q.AttributesJSON, q.DiscoveredAt, q.DiscoveredBy, now, q.DiscoveredBy,
	)
	if err != nil {
		return 0, fmt.Errorf("insert quota %s: %w", q.ID, err)
	}
	n, _ := res.RowsAffected()
	if n <= 0 {
		return 0, nil
	}
	return 1, nil
}

// splitQuota records a changed limit as a new version.
//
// Order matters: the predecessor is marked superseded BEFORE the successor is
// inserted. idx_quotas_current_by_natural_key forbids two live rows on one
// natural key, so pointing superseded_by at the new row id (computed upfront)
// first lets the successor take the current-version slot atomically.
func (s *Store) splitQuota(q *Quota, existing currentQuotaRow, now string) error {
	newRowID := uuid.Must(uuid.NewV7()).String()
	if _, err := s.exec(
		`UPDATE quotas SET superseded_by = ? WHERE id = ?`,
		newRowID, existing.VersionRowID,
	); err != nil {
		return fmt.Errorf("mark superseded for quota %s: %w", q.ID, err)
	}
	if _, err := s.exec(`
		INSERT INTO quotas
			(id, root_id, previous_version_id, provider, account_id, account_name, region,
			 service_code, service_name, quota_code, name, description, unit,
			 value, default_value, adjustable, global_quota, applied_level,
			 attributes, discovered_at, discovered_by, verified_at, verified_by)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		newRowID, existing.RootID, existing.VersionRowID,
		q.Provider, q.AccountID, q.AccountName, q.Region,
		q.ServiceCode, q.ServiceName, q.QuotaCode, q.Name, q.Description, q.Unit,
		q.Value, q.DefaultValue, q.Adjustable, q.GlobalQuota, q.AppliedLevel,
		q.AttributesJSON, existing.DiscoveredAt, existing.DiscoveredBy, now, q.DiscoveredBy,
	); err != nil {
		return fmt.Errorf("insert new version of quota %s: %w", q.ID, err)
	}
	return nil
}
