package cmd

import (
	"strings"
	"testing"
)

func TestParseSince_RFC3339(t *testing.T) {
	out, err := parseSince("2026-04-01T00:00:00Z")
	if err != nil {
		t.Fatalf("parseSince: %v", err)
	}
	if out != "2026-04-01T00:00:00Z" {
		t.Errorf("got %q, want round-trip", out)
	}
}

func TestParseSince_BareDate(t *testing.T) {
	out, err := parseSince("2026-04-01")
	if err != nil {
		t.Fatalf("parseSince: %v", err)
	}
	if out != "2026-04-01T00:00:00Z" {
		t.Errorf("got %q, want bare-date auto-extend", out)
	}
}

func TestParseSince_Empty(t *testing.T) {
	out, err := parseSince("")
	if err != nil {
		t.Errorf("parseSince empty: %v", err)
	}
	if out != "" {
		t.Errorf("empty input: got %q, want empty no-op", out)
	}
}

func TestParseSince_Invalid(t *testing.T) {
	for _, in := range []string{"7d", "last week", "April 1", "2026/04/01"} {
		_, err := parseSince(in)
		if err == nil || !strings.Contains(err.Error(), "must be RFC3339") {
			t.Errorf("parseSince(%q): want error mentioning RFC3339, got %v", in, err)
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
	out, err := parseSince("2026-04-01T05:00:00-05:00")
	if err != nil {
		t.Fatalf("parseSince: %v", err)
	}
	if out != "2026-04-01T10:00:00Z" {
		t.Errorf("got %q, want UTC normalization to 2026-04-01T10:00:00Z", out)
	}
}
