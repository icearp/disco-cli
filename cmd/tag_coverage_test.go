package cmd

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/icearp/disco-cli/internal/untaggable"
	"github.com/icearp/disco-cli/store"
	"github.com/spf13/pflag"
)

// resetTagCoverageFlags clears flag-attached + package-var state between
// tag-coverage tests; required because rootCmd is shared.
func resetTagCoverageFlags() {
	tagCovProviders, tagCovRegions = nil, nil
	tagCovType = ""
	tagCovExcludeTypes = nil
	tagCovScanID = ""
	tagCovDiscoveredSince.reset()
	tagCovDiscoveredBefore.reset()
	tagCovCreatedSince.reset()
	tagCovCreatedBefore.reset()
	tagCovOutputFmt = ""
	tagCovIncludeManaged = false
	tagCovIncludeUntaggable = false
	tagCovMinCoverage = 0
	// RunE reads Changed("min-coverage"), which a reset variable leaves set.
	tagCoverageCmd.Flags().VisitAll(func(f *pflag.Flag) { f.Changed = false })
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
		{
			Provider: "aws", AccountID: "111", Type: "aws:ec2:instance", NativeID: "i-1",
			AttributesJSON: "{}", TagsJSON: sp(`{"env":"prod"}`), DiscoveredBy: scanID,
		},
		{
			Provider: "aws", AccountID: "111", Type: "aws:ec2:instance", NativeID: "i-2",
			AttributesJSON: "{}", TagsJSON: sp(`{"env":"dev","owner":"alice"}`), DiscoveredBy: scanID,
		},
		{
			Provider: "aws", AccountID: "111", Type: "aws:s3:bucket", NativeID: "b-1",
			AttributesJSON: "{}", TagsJSON: sp(`{"env":"prod"}`), DiscoveredBy: scanID,
		},
		{
			Provider: "aws", AccountID: "111", Type: "aws:s3:bucket", NativeID: "b-2",
			AttributesJSON: "{}", TagsJSON: sp(`{"owner":"bob"}`), DiscoveredBy: scanID,
		},
		{
			Provider: "aws", AccountID: "111", Type: "aws:s3:bucket", NativeID: "b-3",
			AttributesJSON: "{}", DiscoveredBy: scanID,
		},
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

// testUntaggableType is registered untaggable by seedTagCoverageDenominatorDB.
const testUntaggableType = "test:tagcov:untaggable"

// seedTagCoverageDenominatorDB adds, beside seedTestDB's two untagged taggable
// rows, one row per denominator rule: a tagged taggable row, an untagged
// taggable row, an untagged row of a type declared untaggable and an untagged
// provider-managed row.
func seedTagCoverageDenominatorDB(t *testing.T) {
	t.Helper()
	untaggable.Register(testUntaggableType)
	st := seedTestDB(t)
	scanID, err := st.CreateScan([]string{"aws"}, map[string]any{})
	if err != nil {
		t.Fatalf("CreateScan: %v", err)
	}
	rows := []*store.Resource{
		{
			Provider: "aws", AccountID: "111", Type: "aws:ec2:instance", NativeID: "den-tagged",
			AttributesJSON: "{}", TagsJSON: sp(`{"owner":"alice"}`), DiscoveredBy: scanID,
		},
		{
			Provider: "aws", AccountID: "111", Type: "aws:ec2:instance", NativeID: "den-untagged",
			AttributesJSON: "{}", DiscoveredBy: scanID,
		},
		{
			Provider: "aws", AccountID: "111", Type: testUntaggableType, NativeID: "den-untaggable",
			AttributesJSON: "{}", DiscoveredBy: scanID,
		},
		{
			Provider: "aws", AccountID: "111", Type: "aws:ec2:instance", NativeID: "den-managed",
			AttributesJSON: "{}", ManagedByProvider: true, DiscoveredBy: scanID,
		},
	}
	if _, err := st.UpsertResources(rows); err != nil {
		t.Fatalf("upsert: %v", err)
	}
}

// TestTagCoverage_Denominator pins who counts: taggable customer rows, tagged
// or not; an untaggable type's rows and managed rows only when the matching
// --include-* flag asks for them.
func TestTagCoverage_Denominator(t *testing.T) {
	tests := []struct {
		name                  string
		flags                 []string
		wantTagged, wantTotal int
	}{
		// seedTestDB's i-1 and b-1 + den-tagged + den-untagged.
		{name: "default", wantTagged: 1, wantTotal: 4},
		{name: "include untaggable", flags: []string{"--include-untaggable"}, wantTagged: 1, wantTotal: 5},
		{name: "include managed", flags: []string{"--include-managed"}, wantTagged: 1, wantTotal: 5},
		{name: "include both", flags: []string{"--include-untaggable", "--include-managed"}, wantTagged: 1, wantTotal: 6},
		// Only b-1 (aws:s3:bucket) is left.
		{name: "exclude types", flags: []string{"--exclude-types", "aws:ec2:instance"}, wantTagged: 0, wantTotal: 1},
		// b-1 + den-untaggable.
		{
			name:       "exclude types include untaggable",
			flags:      []string{"--exclude-types", "aws:ec2:instance", "--include-untaggable"},
			wantTagged: 0, wantTotal: 2,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			seedTagCoverageDenominatorDB(t)
			resetTagCoverageFlags()

			out, err := captureStdout(t, func() error {
				rootCmd.SetArgs(append([]string{"tag-coverage", "owner", "--output", "json"}, tc.flags...))
				return rootCmd.Execute()
			})
			if err != nil {
				t.Fatalf("tag-coverage %v: %v", tc.flags, err)
			}
			var rep []tagCoverage
			if err := json.Unmarshal([]byte(out), &rep); err != nil {
				t.Fatalf("decode: %v\n%s", err, out)
			}
			if len(rep) != 1 {
				t.Fatalf("tag-coverage %v: got %d rows, want 1: %v", tc.flags, len(rep), rep)
			}
			if rep[0].Tagged != tc.wantTagged || rep[0].Total != tc.wantTotal {
				t.Errorf("tag-coverage %v: owner tagged/total = %d/%d; want %d/%d",
					tc.flags, rep[0].Tagged, rep[0].Total, tc.wantTagged, tc.wantTotal)
			}
		})
	}
}

// TestTagCoverage_UntaggableTypeFlag: --type naming an untaggable type is a
// typed error without --include-untaggable, even under --min-coverage (which
// would otherwise fail on an empty report), and a 0/N report with it.
func TestTagCoverage_UntaggableTypeFlag(t *testing.T) {
	for _, flags := range [][]string{nil, {"--min-coverage", "0.5"}} {
		t.Run("rejected"+strings.Join(flags, ""), func(t *testing.T) {
			seedTagCoverageDenominatorDB(t)
			resetTagCoverageFlags()
			t.Cleanup(resetTagCoverageFlags)

			_, err := captureStdout(t, func() error {
				rootCmd.SetArgs(append([]string{"tag-coverage", "owner", "--type", testUntaggableType, "--output", "json"}, flags...))
				return rootCmd.Execute()
			})
			var ue *untaggableTypeError
			if !errors.As(err, &ue) || ue.Type != testUntaggableType {
				t.Fatalf("tag-coverage --type %s %v: err = %v; want *untaggableTypeError naming the type", testUntaggableType, flags, err)
			}
		})
	}
	t.Run("included", func(t *testing.T) {
		seedTagCoverageDenominatorDB(t)
		resetTagCoverageFlags()

		out, err := captureStdout(t, func() error {
			rootCmd.SetArgs([]string{"tag-coverage", "owner", "--type", testUntaggableType, "--include-untaggable", "--output", "json"})
			return rootCmd.Execute()
		})
		if err != nil {
			t.Fatalf("tag-coverage --include-untaggable: %v", err)
		}
		var rep []tagCoverage
		if err := json.Unmarshal([]byte(out), &rep); err != nil {
			t.Fatalf("decode: %v\n%s", err, out)
		}
		if len(rep) != 1 || rep[0].Tagged != 0 || rep[0].Total != 1 {
			t.Errorf("tag-coverage --type %s --include-untaggable = %v; want one owner row 0/1", testUntaggableType, rep)
		}
	})
}
