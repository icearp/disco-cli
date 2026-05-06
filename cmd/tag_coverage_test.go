package cmd

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"codeberg.org/icearp/disco/internal/store"
)

// resetTagCoverageFlags clears flag-attached + package-var state between
// tag-coverage tests; required because rootCmd is shared.
func resetTagCoverageFlags() {
	tagCovProvider, tagCovType, tagCovRegion = "", "", ""
	tagCovExcludeTypes = nil
	tagCovScanID = ""
	tagCovOutputFmt = ""
	tagCovIncludeManaged = false
}

// seedTagCoverageDB returns a store seeded with five customer rows carrying
// mixed tags: 3 with env, 2 with owner (one shared with env), 0 with the
// "missing-tag" key used in the absent-tag-signal test.
func seedTagCoverageDB(t *testing.T) {
	t.Helper()
	st := seedTestDB(t)
	scanID, err := st.CreateScan([]string{"aws"}, map[string]any{})
	if err != nil {
		t.Fatalf("CreateScan: %v", err)
	}
	rows := []*store.Resource{
		{Provider: "aws", AccountID: "111", Type: "aws:ec2:instance", NativeID: "i-1",
			AttributesJSON: "{}", TagsJSON: sp(`{"env":"prod"}`), DiscoveredBy: scanID},
		{Provider: "aws", AccountID: "111", Type: "aws:ec2:instance", NativeID: "i-2",
			AttributesJSON: "{}", TagsJSON: sp(`{"env":"dev","owner":"alice"}`), DiscoveredBy: scanID},
		{Provider: "aws", AccountID: "111", Type: "aws:s3:bucket", NativeID: "b-1",
			AttributesJSON: "{}", TagsJSON: sp(`{"env":"prod"}`), DiscoveredBy: scanID},
		{Provider: "aws", AccountID: "111", Type: "aws:s3:bucket", NativeID: "b-2",
			AttributesJSON: "{}", TagsJSON: sp(`{"owner":"bob"}`), DiscoveredBy: scanID},
		{Provider: "aws", AccountID: "111", Type: "aws:s3:bucket", NativeID: "b-3",
			AttributesJSON: "{}", DiscoveredBy: scanID},
	}
	if _, err := st.UpsertResources(rows); err != nil {
		t.Fatalf("upsert: %v", err)
	}
}

// sp helper for *string fixtures.
func sp(s string) *string { return &s }

func TestTagCoverage_AllKeys(t *testing.T) {
	seedTagCoverageDB(t)
	resetTagCoverageFlags()

	out, err := captureStdout(t, func() error {
		cmd := rootCmd
		cmd.SetArgs([]string{"tag-coverage"})
		return cmd.Execute()
	})
	if err != nil {
		t.Fatalf("tag-coverage: %v", err)
	}
	// Header + 2 keys (env, owner). seedTestDB also seeds 2 rows with no tags,
	// so total denominator is 7 (5 here + 2 from seedTestDB).
	if !strings.Contains(out, "env") || !strings.Contains(out, "owner") {
		t.Errorf("missing keys: %s", out)
	}
	// env appears 3 times, owner 2 — env should sort first (tagged desc).
	if i := strings.Index(out, "env"); i < 0 || i > strings.Index(out, "owner") {
		t.Errorf("sort order wrong (env should precede owner): %s", out)
	}
}

func TestTagCoverage_SpecificKey_ZeroIncluded(t *testing.T) {
	seedTagCoverageDB(t)
	resetTagCoverageFlags()

	out, err := captureStdout(t, func() error {
		cmd := rootCmd
		cmd.SetArgs([]string{"tag-coverage", "owner", "missing-tag", "--output", "json"})
		return cmd.Execute()
	})
	if err != nil {
		t.Fatalf("tag-coverage: %v", err)
	}
	var rep []tagCoverage
	if err := json.Unmarshal([]byte(out), &rep); err != nil {
		t.Fatalf("decode: %v\n%s", err, out)
	}
	if len(rep) != 2 {
		t.Fatalf("want 2 rows, got %d: %v", len(rep), rep)
	}
	byTag := map[string]tagCoverage{rep[0].Tag: rep[0], rep[1].Tag: rep[1]}
	if byTag["owner"].Tagged != 2 {
		t.Errorf("owner tagged: got %d, want 2", byTag["owner"].Tagged)
	}
	if byTag["missing-tag"].Tagged != 0 {
		t.Errorf("missing-tag tagged: got %d, want 0", byTag["missing-tag"].Tagged)
	}
	if byTag["missing-tag"].Coverage != 0 {
		t.Errorf("missing-tag coverage: got %v, want 0", byTag["missing-tag"].Coverage)
	}
}

func TestTagCoverage_FilterByType(t *testing.T) {
	seedTagCoverageDB(t)
	resetTagCoverageFlags()

	out, err := captureStdout(t, func() error {
		cmd := rootCmd
		cmd.SetArgs([]string{"tag-coverage", "owner", "--type", "aws:ec2:instance", "--output", "csv"})
		return cmd.Execute()
	})
	if err != nil {
		t.Fatalf("tag-coverage: %v", err)
	}
	r := csv.NewReader(bytes.NewReader([]byte(out)))
	rows, err := r.ReadAll()
	if err != nil {
		t.Fatalf("csv: %v\n%s", err, out)
	}
	if len(rows) != 2 {
		t.Fatalf("want header + 1 row, got %d", len(rows))
	}
	// 2 ec2 instances; 1 carries owner.
	if rows[1][0] != "owner" || rows[1][1] != "1" || rows[1][2] != "2" {
		t.Errorf("filtered row: got %v, want [owner 1 2 ...]", rows[1])
	}
}

func TestTagCoverage_UnknownFormat(t *testing.T) {
	seedTagCoverageDB(t)
	resetTagCoverageFlags()

	_, err := captureStdout(t, func() error {
		cmd := rootCmd
		cmd.SetArgs([]string{"tag-coverage", "--output", "xml"})
		return cmd.Execute()
	})
	if err == nil || !strings.Contains(err.Error(), "unknown --output format") {
		t.Errorf("want unknown-format error, got %v", err)
	}
	// Sanity: ratio helper guards div-by-zero.
	if ratio(1, 0) != 0 {
		t.Errorf("ratio div-by-zero guard")
	}
	_ = fmt.Sprintf("%v", tagCoverage{}) // touch struct so unused-field linter stays calm
}
