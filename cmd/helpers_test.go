package cmd

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/viper"
)

func TestParseSince_RFC3339(t *testing.T) {
	out, err := parseTimeFlag("--discovered-since", "2026-04-01T00:00:00Z")
	if err != nil {
		t.Fatalf("parseTimeFlag: %v", err)
	}
	if out != "2026-04-01T00:00:00Z" {
		t.Errorf("got %q, want round-trip", out)
	}
}

func TestParseSince_BareDate(t *testing.T) {
	out, err := parseTimeFlag("--discovered-since", "2026-04-01")
	if err != nil {
		t.Fatalf("parseTimeFlag: %v", err)
	}
	if out != "2026-04-01T00:00:00Z" {
		t.Errorf("got %q, want bare-date auto-extend", out)
	}
}

func TestParseSince_Empty(t *testing.T) {
	out, err := parseTimeFlag("--discovered-since", "")
	if err != nil {
		t.Errorf("parseSince empty: %v", err)
	}
	if out != "" {
		t.Errorf("empty input: got %q, want empty no-op", out)
	}
}

func TestParseSince_Invalid(t *testing.T) {
	for _, in := range []string{"7d", "last week", "April 1", "2026/04/01"} {
		_, err := parseTimeFlag("--discovered-since", in)
		if err == nil || !strings.Contains(err.Error(), "must be RFC3339") {
			t.Errorf("parseTimeFlag(%q): want error mentioning RFC3339, got %v", in, err)
		}
	}
}

func TestIsScanIDPrefix(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"", false},
		{"latest", false},
		{"abc", false},     // < 8
		{"abcdef1", false}, // 7
		{"abcdef12", true}, // 8
		{"ABCDEF12", true}, // case-insensitive
		{"29cdb17389eccf8506148306a815ed5d", false}, // 32 = full ID, not prefix
		{"29cdb17389eccf8506148306a815ed5", true},   // 31
		{"29cdb17g", false},                         // non-hex
		{"29cdb17!", false},                         // non-hex
	}
	for _, c := range cases {
		if got := isScanIDPrefix(c.in); got != c.want {
			t.Errorf("isScanIDPrefix(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestResolveScanIDPrefix_Lookup(t *testing.T) {
	st := seedTestDB(t)
	idA, err := st.ListScans()
	if err != nil || len(idA) != 1 {
		t.Fatalf("seed produced %d scans, want 1: err=%v", len(idA), err)
	}
	full := idA[0].ID

	got, err := resolveScanID(st, full[:8])
	if err != nil {
		t.Fatalf("resolveScanID(8-char prefix): %v", err)
	}
	if got != full {
		t.Errorf("got %q, want %q", got, full)
	}

	if _, err := resolveScanID(st, full[:7]); err == nil {
		t.Errorf("7-char input should not match — fell through to GetScan and reported not-found")
	}
}

func TestParseSince_NonUTCNormalisesToUTC(t *testing.T) {
	out, err := parseTimeFlag("--discovered-since", "2026-04-01T05:00:00-05:00")
	if err != nil {
		t.Fatalf("parseTimeFlag: %v", err)
	}
	if out != "2026-04-01T10:00:00Z" {
		t.Errorf("got %q, want UTC normalization to 2026-04-01T10:00:00Z", out)
	}
}

func TestOpenDB_MissingFile_HintsScan(t *testing.T) {
	dir := t.TempDir()
	missing := filepath.Join(dir, "no-such.db")
	viper.Set("db", missing)
	t.Cleanup(func() { viper.Set("db", "") })

	_, err := openDB()
	if err == nil {
		t.Fatalf("expected error opening missing DB, got nil")
	}
	if !strings.Contains(err.Error(), "disco scan") {
		t.Errorf("missing-DB error should hint `disco scan`, got %q", err)
	}
}

func TestOpenDB_StaleSchema_HintsScan(t *testing.T) {
	st := seedTestDB(t)
	// Roll back schema_migrations one step so the on-disk schema looks
	// stale relative to the embedded migration set.
	if _, err := st.DB().Exec("DELETE FROM schema_migrations WHERE version = (SELECT MAX(version) FROM schema_migrations)"); err != nil {
		t.Fatalf("roll back schema_migrations: %v", err)
	}
	_ = st.Close()

	_, err := openDB()
	if err == nil {
		t.Fatalf("expected stale-schema error, got nil")
	}
	if !strings.Contains(err.Error(), "schema") || !strings.Contains(err.Error(), "disco scan") {
		t.Errorf("stale-schema error should mention 'schema' and 'disco scan', got %q", err)
	}
}
