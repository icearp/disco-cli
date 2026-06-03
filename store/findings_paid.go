//go:build paid

package store

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	sq "github.com/Masterminds/squirrel"
)

// CheckRun records one invocation of `disco check --persist`. ID shape
// mirrors scans.id (32-hex-char crypto/rand). Timestamps are RFC3339 UTC.
type CheckRun struct {
	ID             string   `db:"id"`
	StartedAt      string   `db:"started_at"`
	FinishedAt     *string  `db:"finished_at"`
	RulesPathsJSON string   `db:"rules_paths"`
	RulesPaths     []string `db:"-"`
	PacksJSON      string   `db:"packs"`
	Packs          []string `db:"-"`
	SeverityFilter *string  `db:"severity_filter"`
	ResourceCount  *int     `db:"resource_count"`
	FindingCount   *int     `db:"finding_count"`
	// WorkspaceID is the per-workspace RLS discriminator; carried so
	// SELECT * round-trips the column. nil when the app.workspace_id GUC
	// was unset at insert time.
	WorkspaceID *string `db:"workspace_id"`
}

// StoredFinding is the on-disk shape of a policy.Finding. Pointer fields
// allow NULL in the DB so optional Rego output keys round-trip cleanly.
type StoredFinding struct {
	RowID       int64   `db:"id"`
	CheckRunID  string  `db:"check_run_id"`
	FindingID   string  `db:"finding_id"`
	ResourceID  string  `db:"resource_id"`
	Severity    string  `db:"severity"`
	Message     string  `db:"message"`
	Provider    *string `db:"provider"`
	Type        *string `db:"type"`
	Name        *string `db:"name"`
	Region      *string `db:"region"`
	Category    *string `db:"category"`
	Remediation *string `db:"remediation"`
	RefURL      *string `db:"ref_url"`
	TagsJSON    *string `db:"tags"`
	WorkspaceID *string `db:"workspace_id"` // per-workspace RLS discriminator; nil when the app.workspace_id GUC was unset
}

// FindingFilter shapes ListFindings queries. Empty fields skip the clause.
// Since is RFC3339; the SQL JOIN compares against check_runs.started_at.
type FindingFilter struct {
	CheckRunID string
	FindingID  string
	Severity   string
	Category   string
	Provider   string
	Type       string
	ResourceID string
	Since      string
	Limit      uint64
	Offset     uint64
}

// PersistCheckRun writes one check_runs row plus N findings rows in a
// single transaction. Returns the generated check_run ID. Caller pre-
// builds []StoredFinding (CheckRunID is filled in by this method) — keeps
// the store package free of a policy import which would cycle.
func (s *Store) PersistCheckRun(rulesPaths, packs []string, severityFilter string, resourceCount int, findings []StoredFinding) (string, error) {
	id, err := newCheckRunID()
	if err != nil {
		return "", err
	}
	now := time.Now().UTC().Format(time.RFC3339)
	rulesJSON, _ := json.Marshal(rulesPaths)
	packsJSON, _ := json.Marshal(packs)

	tx, err := s.db.Beginx()
	if err != nil {
		return "", err
	}
	defer func() { _ = tx.Rollback() }()

	var sevPtr *string
	if severityFilter != "" {
		sevPtr = &severityFilter
	}
	findingCount := len(findings)
	_, err = tx.Exec(
		`
		INSERT INTO check_runs
			(id, started_at, finished_at, rules_paths, packs, severity_filter, resource_count, finding_count)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		id, now, now, string(rulesJSON), string(packsJSON), sevPtr, resourceCount, findingCount,
	)
	if err != nil {
		return "", fmt.Errorf("insert check_run: %w", err)
	}

	for _, f := range findings {
		row := f
		row.CheckRunID = id
		_, err = tx.NamedExec(`
			INSERT INTO findings
				(check_run_id, finding_id, resource_id, severity, message,
				 provider, type, name, region, category, remediation, ref_url, tags)
			VALUES
				(:check_run_id, :finding_id, :resource_id, :severity, :message,
				 :provider, :type, :name, :region, :category, :remediation, :ref_url, :tags)`, row)
		if err != nil {
			return "", fmt.Errorf("insert finding %s: %w", f.FindingID, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return "", err
	}
	return id, nil
}

// ListCheckRuns returns every check_run, newest first. Tie-break on rowid
// DESC so two runs created within the same SQLite-second order
// deterministically (mirrors ListScans).
func (s *Store) ListCheckRuns() ([]CheckRun, error) {
	var runs []CheckRun
	if err := s.selectAll(&runs, "SELECT * FROM check_runs ORDER BY started_at DESC, rowid DESC"); err != nil {
		return nil, err
	}
	for i := range runs {
		if err := decodeRunSlices(&runs[i]); err != nil {
			return nil, err
		}
	}
	return runs, nil
}

// GetCheckRun retrieves one check_run by ID. Decodes RulesPaths + Packs.
func (s *Store) GetCheckRun(id string) (*CheckRun, error) {
	var r CheckRun
	if err := s.get(&r, "SELECT * FROM check_runs WHERE id = ?", id); err != nil {
		return nil, fmt.Errorf("get check_run %s: %w", id, err)
	}
	if err := decodeRunSlices(&r); err != nil {
		return nil, err
	}
	return &r, nil
}

// ListFindings returns findings matching the filter. Sorted by severity
// rank (critical → low) then finding_id then resource_id for stable
// reporting.
func (s *Store) ListFindings(f FindingFilter) ([]StoredFinding, error) {
	q := sq.Select("findings.*").From("findings")
	if f.Since != "" {
		q = q.Join("check_runs ON check_runs.id = findings.check_run_id").
			Where(sq.GtOrEq{"check_runs.started_at": f.Since})
	}
	if f.CheckRunID != "" {
		q = q.Where(sq.Eq{"findings.check_run_id": f.CheckRunID})
	}
	if f.FindingID != "" {
		q = q.Where(sq.Eq{"findings.finding_id": f.FindingID})
	}
	if f.Severity != "" {
		q = q.Where(sq.Eq{"findings.severity": f.Severity})
	}
	if f.Category != "" {
		q = q.Where(sq.Eq{"findings.category": f.Category})
	}
	if f.Provider != "" {
		q = q.Where(sq.Eq{"findings.provider": f.Provider})
	}
	if f.Type != "" {
		q = q.Where(sq.Eq{"findings.type": f.Type})
	}
	if f.ResourceID != "" {
		q = q.Where(sq.Eq{"findings.resource_id": f.ResourceID})
	}
	q = q.OrderBy(`
		CASE findings.severity
			WHEN 'critical' THEN 0
			WHEN 'high' THEN 1
			WHEN 'medium' THEN 2
			WHEN 'low' THEN 3
			ELSE 4
		END`, "findings.finding_id", "findings.resource_id")
	if f.Limit > 0 {
		q = q.Limit(f.Limit).Offset(f.Offset)
	}
	query, args, err := q.PlaceholderFormat(s.placeholder()).ToSql()
	if err != nil {
		return nil, fmt.Errorf("build query: %w", err)
	}
	var out []StoredFinding
	if err := s.selectAll(&out, query, args...); err != nil {
		return nil, fmt.Errorf("list findings: %w", err)
	}
	return out, nil
}

// DeleteCheckRun removes the run and (via FK CASCADE) every finding
// row owned by it. Used by future retention-pruning paths.
func (s *Store) DeleteCheckRun(id string) error {
	_, err := s.exec("DELETE FROM check_runs WHERE id = ?", id)
	return err
}

func decodeRunSlices(r *CheckRun) error {
	if r.RulesPathsJSON != "" {
		if err := json.Unmarshal([]byte(r.RulesPathsJSON), &r.RulesPaths); err != nil {
			return fmt.Errorf("decode rules_paths: %w", err)
		}
	}
	if r.PacksJSON != "" {
		if err := json.Unmarshal([]byte(r.PacksJSON), &r.Packs); err != nil {
			return fmt.Errorf("decode packs: %w", err)
		}
	}
	return nil
}

func newCheckRunID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate check_run id: %w", err)
	}
	return hex.EncodeToString(b), nil
}
