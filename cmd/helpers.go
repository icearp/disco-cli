package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/icearp/disco-cli/internal/providers"
	"github.com/icearp/disco-cli/store"
)

// providerListHint renders the registered provider names for flag help (e.g.
// "aws, azure, gcp"). Provider init()s run before cmd's init() via the
// internal/providers/all blank import, so the registry is populated when help
// is built — a slim build advertises only its compiled providers.
func providerListHint() string { return strings.Join(providers.Names(), ", ") }

// structuredErrorEmitted is set to true after maybeStructuredError writes
// the JSON envelope to stdout. cmd/root.go::Execute reads it and skips the
// duplicate stderr print so a `disco ... -o json` failure produces ONE
// message (the JSON envelope on stdout), not two (envelope + plaintext).
var structuredErrorEmitted bool

// maybeStructuredError emits a JSON error envelope to stdout when format is
// json/jsonl, so pipelines like `disco ... -o json | jq` see a parseable
// signal on failure rather than empty stdout. Sets structuredErrorEmitted so
// root.go can suppress the duplicate plaintext stderr print.
func maybeStructuredError(format string, err error) {
	if err == nil {
		return
	}
	switch format {
	case "json", "jsonl":
		_ = json.NewEncoder(os.Stdout).Encode(struct {
			Err string `json:"error"`
		}{err.Error()})
		structuredErrorEmitted = true
		// "sarif" is deliberately excluded: SARIF is a rigid schema (runs[].
		// results[], tool driver); a bare {"error":"..."} isn't valid SARIF and
		// would break strict consumers (e.g. GitHub code scanning). On error
		// under -o sarif, stdout stays empty and the non-zero exit + plaintext
		// stderr (root.go) carry the failure.
	}
}

// openDB opens the local DB read-only. Every read command (list / graph /
// summary / tag-coverage / scans / coverage / check) routes here so the
// SQLite file descriptor never carries write capability for query paths.
// Defense in depth: even if a future read command accidentally issues an
// UPDATE, SQLite refuses at the driver layer.
//
// Write paths must call openWriteDB explicitly. The global --db-readonly
// flag is honored as a no-op here (already RO) and continues to refuse
// writers up-front (cmd/scan.go, cmd/config.go's `config init`).
//
// First-run UX: when the DB file doesn't exist (e.g. operator ran `disco
// list` before any `disco scan`), the underlying store error names the
// missing path; surface a one-line "run a scan first" hint inline.
func openDB() (*store.Store, error) {
	path := defaultDBPath()
	st, err := store.OpenReadOnly(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, fmt.Errorf("%w (no scans recorded yet — run `disco scan <provider>` first)", err)
		}
		return nil, err
	}
	// Stale-schema gate: store.OpenReadOnly intentionally skips migrate (a RW
	// op). After a binary upgrade that introduces a migration, the on-disk
	// schema can lag the reader's expectations. Probe schema_migrations and
	// reject with a clear hint before queries surface as cryptic SQLite
	// "no such column" / "no such table" errors. See cmd/CLAUDE.md for the
	// trust-boundary rationale of read-only-by-default.
	target, terr := store.TargetSchemaVersion()
	if terr != nil {
		_ = st.Close()
		return nil, fmt.Errorf("probe target schema: %w", terr)
	}
	current, cerr := st.CurrentSchemaVersion()
	if cerr != nil {
		_ = st.Close()
		return nil, fmt.Errorf("probe current schema: %w", cerr)
	}
	if current < target {
		_ = st.Close()
		return nil, fmt.Errorf("disco: on-disk schema is at migration %d, binary expects %d; run `disco scan` to upgrade", current, target)
	}
	return st, nil
}

// openWriteDB opens a writable Store. Reserved for commands whose RunE
// genuinely needs to mutate (scan, config init, check --persist). The global
// --db-readonly flag refuses writers up-front; that check lives at the call
// site (so the error string can name the gate the operator should remove).
//
// When DISCO_PG_DSN is set, writes go to a Postgres-backed Store (the
// scan-worker deployment shape); empty env falls through to the local SQLite
// path for normal CLI and dev use.
func openWriteDB() (*store.Store, error) {
	if s, err := openPostgresFromEnv(); err != nil || s != nil {
		return s, err
	}
	return store.Open(defaultDBPath())
}

// openPostgresFromEnv returns a single-tenant Postgres-backed Store when
// DISCO_PG_DSN is set, or (nil, nil) when it isn't (so callers fall through to
// SQLite). Multi-tenant deployments (disco-saas) layer per-tenant schema +
// RLS GUCs through store.WithAfterConnect on their own side, not here.
func openPostgresFromEnv() (*store.Store, error) {
	dsn := os.Getenv("DISCO_PG_DSN")
	if dsn == "" {
		return nil, nil
	}
	return store.OpenPostgres(context.Background(), dsn)
}

// resolveCheckRunID expands the `latest` shorthand to the most-recent
// check_run's ID; literal IDs pass through after a presence check. Mirrors
// resolveScanID. Used by `disco findings list --check-run-id <...>`.
func resolveCheckRunID(db *store.Store, raw string) (string, error) {
	if raw == "" {
		return "", nil
	}
	if raw == "latest" {
		runs, err := db.ListCheckRuns()
		if err != nil {
			return "", fmt.Errorf("list check_runs: %w", err)
		}
		if len(runs) == 0 {
			return "", fmt.Errorf("no check runs recorded; --check-run-id latest has nothing to resolve")
		}
		return runs[0].ID, nil
	}
	if _, err := db.GetCheckRun(raw); err != nil {
		return "", fmt.Errorf("check run %q not found", raw)
	}
	return raw, nil
}

// loadAllResourcesPaged paginates ListResources and returns every row
// matching base. Callers set IncludeManaged + filter fields on base; this
// helper overrides Limit + Offset to walk the full table. Mirrors the
// store.GraphAll idiom (store/graph.go:451).
func loadAllResourcesPaged(db *store.Store, base store.ResourceFilter) ([]store.Resource, error) {
	const pageSize = uint64(5000)
	var all []store.Resource
	for offset := uint64(0); ; offset += pageSize {
		base.Limit = pageSize
		base.Offset = offset
		page, err := db.ListResources(base)
		if err != nil {
			return nil, err
		}
		all = append(all, page...)
		if uint64(len(page)) < pageSize {
			return all, nil
		}
	}
}

// resolveScanID expands the `latest` shorthand. Returns the most-recent
// scan whose `resource_count > 0` so a re-verify run that touched no new
// rows doesn't silently zero-row the documented drift workflow (F3 fix).
// Falls back to the most-recent scan if none qualify, with a one-line
// stderr note describing the fall-back so auditors don't miss the signal.
//
// Literal IDs pass through unchanged after a presence check. Hex prefixes
// of length 8–31 resolve via unique-prefix match against ListScans so the
// 8-char short form printed by `disco scans` is paste-friendly without
// inviting collisions on the 4–7 char range. Multi-match returns an
// ambiguous-prefix error listing the candidates.
func resolveScanID(db *store.Store, raw string) (string, error) {
	if raw == "" {
		return "", nil
	}
	if raw == "latest" {
		scans, err := db.ListScans()
		if err != nil {
			return "", fmt.Errorf("list scans: %w", err)
		}
		if len(scans) == 0 {
			return "", fmt.Errorf("no scans recorded; --scan-id latest has nothing to resolve")
		}
		for _, s := range scans {
			if s.ResourceCount != nil && *s.ResourceCount > 0 {
				return s.ID, nil
			}
		}
		fmt.Fprintf(os.Stderr, "note: no scan recorded any rows; --scan-id latest fell back to most-recent scan %s (resource_count=0)\n", short(scans[0].ID))
		return scans[0].ID, nil
	}
	if isScanIDPrefix(raw) {
		return resolveScanIDPrefix(db, raw)
	}
	if _, err := db.GetScan(raw); err != nil {
		return "", fmt.Errorf("scan %q not found", raw)
	}
	return raw, nil
}

// isScanIDPrefix reports whether raw is hex of length 8–31 (paste-friendly
// prefix range). Length 32 is the canonical full ID and resolves via
// db.GetScan; lengths < 8 reject up-front to avoid collision risk.
func isScanIDPrefix(raw string) bool {
	if len(raw) < 8 || len(raw) > 31 {
		return false
	}
	for _, r := range raw {
		switch {
		case r >= '0' && r <= '9':
		case r >= 'a' && r <= 'f':
		case r >= 'A' && r <= 'F':
		default:
			return false
		}
	}
	return true
}

// resolveScanIDPrefix scans all recorded runs for IDs starting with prefix
// (case-insensitive). Returns the unique match or an ambiguous-prefix error
// listing the candidates so the caller can paste a longer prefix.
func resolveScanIDPrefix(db *store.Store, prefix string) (string, error) {
	scans, err := db.ListScans()
	if err != nil {
		return "", fmt.Errorf("list scans: %w", err)
	}
	needle := strings.ToLower(prefix)
	var matches []string
	for _, s := range scans {
		if strings.HasPrefix(strings.ToLower(s.ID), needle) {
			matches = append(matches, s.ID)
		}
	}
	switch len(matches) {
	case 0:
		return "", fmt.Errorf("scan %q not found", prefix)
	case 1:
		return matches[0], nil
	default:
		short := make([]string, len(matches))
		for i, m := range matches {
			short[i] = m[:min(16, len(m))]
		}
		return "", fmt.Errorf("scan-id prefix %q is ambiguous: %d matches (%s); use a longer prefix",
			prefix, len(matches), strings.Join(short, ", "))
	}
}

// singleSetString is a pflag.Value that rejects being set more than once.
// Cobra's default StringVar last-wins-silently on `--flag A --flag B`; this
// type errors instead so timestamp-shaped flags (--discovered-since,
// --created-before, etc.) can't be silently overridden in scripted
// invocations. Reset to "" before each test run via the existing reset
// helpers.
type singleSetString struct {
	val string
	set bool
	// flag is the flag name used in the error message ("since", not "--since").
	flag string
}

func (s *singleSetString) String() string { return s.val }
func (s *singleSetString) Type() string   { return "string" }
func (s *singleSetString) Set(v string) error {
	if s.set {
		return fmt.Errorf("--%s: cannot be set more than once", s.flag)
	}
	s.val = v
	s.set = true
	return nil
}

// reset clears Set state so test reset helpers can reuse the same flag var
// across runs without re-registering the cobra flag.
func (s *singleSetString) reset() { s.val, s.set = "", false }

// parseTimeFlag is the shared parser for every time-shaped flag in the
// CLI (--discovered-since, --discovered-before, --created-since,
// --created-before, --run-since). Accepts full RFC3339 or bare YYYY-MM-DD
// (auto-extended to T00:00:00Z UTC). Empty input passes through as a
// no-op so callers can blindly forward the flag value.
// flag is the user-facing name used in error messages.
func parseTimeFlag(flag, raw string) (string, error) {
	if raw == "" {
		return "", nil
	}
	if t, err := time.Parse(time.RFC3339, raw); err == nil {
		return t.UTC().Format(time.RFC3339), nil
	}
	if t, err := time.Parse("2006-01-02", raw); err == nil {
		return t.UTC().Format(time.RFC3339), nil
	}
	return "", fmt.Errorf("%s: %q must be RFC3339 (2026-04-01T00:00:00Z) or bare date (2026-04-01)", flag, raw)
}

// ptrOrDash returns the pointed-to string, or "-" if the pointer is nil.
// Shared by list/diff/graph/check table renderers so missing optional
// fields render uniformly.
func ptrOrDash(p *string) string {
	if p == nil {
		return "-"
	}
	return *p
}

// short returns the first 8 chars of a resource ID for compact table display.
// Resource IDs are 32-hex-char SHA-256 prefixes; the leading 8 are unique
// enough for human disambiguation in table output.
func short(id string) string {
	if len(id) <= 8 {
		return id
	}
	return id[:8]
}

// messageRow is the shared shape used by renderMessages to print scan
// errors and warnings as a single column-aligned block. ScanError and
// ScanWarning live in store and have identical fields; flattening
// to messageRow lets one renderer serve both.
type messageRow struct {
	provider, service, scope, message string
}

// renderMessages prints a grouped, column-aligned block of messages with
// deterministic sort order (provider, scope, service). Used by scan.go to
// render the trailing "Errors:" and "Warnings:" sections.
func renderMessages(w io.Writer, label string, rows []messageRow, quiet bool) {
	if quiet || len(rows) == 0 {
		return
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].provider != rows[j].provider {
			return rows[i].provider < rows[j].provider
		}
		if rows[i].scope != rows[j].scope {
			return rows[i].scope < rows[j].scope
		}
		return rows[i].service < rows[j].service
	})
	provW, svcW, scopeW := 0, 0, 0
	for _, r := range rows {
		if len(r.provider) > provW {
			provW = len(r.provider)
		}
		if len(r.service) > svcW {
			svcW = len(r.service)
		}
		if len(r.scope) > scopeW {
			scopeW = len(r.scope)
		}
	}
	_, _ = fmt.Fprintf(w, "\n%s (%d):\n", label, len(rows))
	for _, r := range rows {
		_, _ = fmt.Fprintf(w, "  %-*s  %-*s  %-*s  %s\n",
			provW, r.provider, svcW, r.service, scopeW, r.scope, r.message)
	}
}

// sanitizeMarkdownCell makes an arbitrary value safe to drop into a GFM table
// cell: escape `|` (else it splits the column) and fold newlines to spaces (md
// tables can't span lines). Callers — including the raw-JSON ones (scope/tags
// blobs) — stay safe by construction.
func sanitizeMarkdownCell(s string) string {
	if !strings.ContainsAny(s, "|\n\r") {
		return s
	}
	s = strings.ReplaceAll(s, "|", "\\|")
	s = strings.ReplaceAll(s, "\r\n", " ")
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\r", " ")
	return s
}

// renderMarkdownTable writes a GitHub-flavoured markdown table to w. Returns
// nil when headers is empty. Cell values are sanitized internally
// (sanitizeMarkdownCell) — `|` is escaped and newlines folded to spaces — so
// callers may pass raw values (incl. JSON blobs) without pre-escaping.
//
// Output shape:
//
//	| H1 | H2 |
//	| --- | --- |
//	| a | b |
//
// Used by every `disco <cmd> -o markdown` path so the table syntax is
// byte-stable across renderers.
func renderMarkdownTable(w io.Writer, headers []string, rows [][]string) error {
	if len(headers) == 0 {
		return nil
	}
	hdr := make([]string, len(headers))
	for i, h := range headers {
		hdr[i] = sanitizeMarkdownCell(h)
	}
	if _, err := fmt.Fprintf(w, "| %s |\n", strings.Join(hdr, " | ")); err != nil {
		return err
	}
	sep := make([]string, len(headers))
	for i := range sep {
		sep[i] = "---"
	}
	if _, err := fmt.Fprintf(w, "| %s |\n", strings.Join(sep, " | ")); err != nil {
		return err
	}
	for _, row := range rows {
		// Pad short rows with empty cells so the column count matches headers.
		cells := make([]string, len(headers))
		for i := range cells {
			if i < len(row) {
				cells[i] = sanitizeMarkdownCell(row[i])
			}
		}
		if _, err := fmt.Fprintf(w, "| %s |\n", strings.Join(cells, " | ")); err != nil {
			return err
		}
	}
	return nil
}
