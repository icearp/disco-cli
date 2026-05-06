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

func TestParseSince_NonUTCNormalisesToUTC(t *testing.T) {
	out, err := parseSince("2026-04-01T05:00:00-05:00")
	if err != nil {
		t.Fatalf("parseSince: %v", err)
	}
	if out != "2026-04-01T10:00:00Z" {
		t.Errorf("got %q, want UTC normalization to 2026-04-01T10:00:00Z", out)
	}
}
