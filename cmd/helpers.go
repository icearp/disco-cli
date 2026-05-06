package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"time"

	"codeberg.org/icearp/disco/internal/store"
)

// maybeStructuredError emits a JSON error envelope to stdout when the caller's
// selected output format is structured (json/jsonl). Lets pipelines such as
// `disco ... -o json | jq` see a parseable signal on failure rather than
// empty stdout. Stderr message + exit code unchanged — call this from a
// deferred wrapper in RunE; cmd/root.go still prints err to stderr.
func maybeStructuredError(format string, err error) {
	if err == nil {
		return
	}
	switch format {
	case "json", "jsonl":
		_ = json.NewEncoder(os.Stdout).Encode(struct {
			Err string `json:"error"`
		}{err.Error()})
	}
}

// openDB opens the local DB, honoring the global --db-readonly flag.
// Read commands (list / graph / check / coverage / diff) call this; scan
// opens directly via store.Open after rejecting --db-readonly up-front.
func openDB() (*store.Store, error) {
	if dbReadOnly {
		return store.OpenReadOnly(defaultDBPath())
	}
	return store.Open(defaultDBPath())
}

// loadAllResourcesPaged paginates ListResources and returns every row
// matching base. Callers set IncludeManaged + filter fields on base; this
// helper overrides Limit + Offset to walk the full table. Mirrors the
// store.GraphAll idiom (internal/store/graph.go:451).
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

// resolveScanID expands the `latest` shorthand to the most-recent scan's
// ID; literal IDs pass through unchanged after a presence check. Used by
// list / summary / tag-coverage / scans show — auditor-facing surface.
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
		return scans[0].ID, nil
	}
	if _, err := db.GetScan(raw); err != nil {
		return "", fmt.Errorf("scan %q not found", raw)
	}
	return raw, nil
}

// singleSetString is a pflag.Value that rejects being set more than once.
// Cobra's default StringVar last-wins-silently on `--flag A --flag B`; this
// type errors instead so timestamp-shaped flags (--since today) can't be
// silently overridden in scripted invocations. Reset to "" before each test
// run via the existing reset helpers.
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

// parseSince normalises a user-supplied --since value into the RFC3339 UTC
// shape ListResources stores discovered_at in. Accepts full RFC3339 or bare
// YYYY-MM-DD (auto-extended to T00:00:00Z UTC). Empty input passes through
// as a no-op so callers can blindly forward the flag.
func parseSince(raw string) (string, error) {
	if raw == "" {
		return "", nil
	}
	if t, err := time.Parse(time.RFC3339, raw); err == nil {
		return t.UTC().Format(time.RFC3339), nil
	}
	if t, err := time.Parse("2006-01-02", raw); err == nil {
		return t.UTC().Format(time.RFC3339), nil
	}
	return "", fmt.Errorf("--since: %q must be RFC3339 (2026-04-01T00:00:00Z) or bare date (2026-04-01)", raw)
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
// ScanWarning live in internal/store and have identical fields; flattening
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
