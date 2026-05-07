package cmd

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"strings"
	"testing"

	"codeberg.org/icearp/disco/internal/store"
)

// resetSummaryFlags clears flag-attached + package-var state between tests;
// rootCmd is shared so flags leak otherwise.
func resetSummaryFlags() {
	summaryProvider, summaryRegion = "", ""
	summaryExcludeTypes = nil
	summaryScanID = ""
	summaryDiscoveredSince.reset()
	summaryOutputFmt = ""
	summaryTopTypes = 10
	summaryIncludeManaged = false
}

// seedSummaryDB seeds 6 customer rows: 3 log-streams (noisy), 2 ec2
// instances, 1 s3 bucket — across two regions plus one global. seedTestDB
// itself plants 2 more (web + db) for a total of 8.
func seedSummaryDB(t *testing.T) {
	t.Helper()
	st := seedTestDB(t)
	scanID, err := st.CreateScan([]string{"aws"}, map[string]any{})
	if err != nil {
		t.Fatalf("CreateScan: %v", err)
	}
	r := func(t string) string { return t }
	regUE2 := r("us-east-2")
	regUE1 := r("us-east-1")
	rows := []*store.Resource{
		{Provider: "aws", AccountID: "111", Type: "aws:logs:log-stream", NativeID: "ls-1",
			Region: &regUE2, AttributesJSON: "{}", DiscoveredBy: scanID},
		{Provider: "aws", AccountID: "111", Type: "aws:logs:log-stream", NativeID: "ls-2",
			Region: &regUE2, AttributesJSON: "{}", DiscoveredBy: scanID},
		{Provider: "aws", AccountID: "111", Type: "aws:logs:log-stream", NativeID: "ls-3",
			Region: &regUE2, AttributesJSON: "{}", DiscoveredBy: scanID},
		{Provider: "aws", AccountID: "111", Type: "aws:ec2:instance", NativeID: "i-9",
			Region: &regUE1, AttributesJSON: "{}", DiscoveredBy: scanID},
		{Provider: "aws", AccountID: "111", Type: "aws:ec2:instance", NativeID: "i-10",
			Region: &regUE2, AttributesJSON: "{}", DiscoveredBy: scanID},
		// Global (nil region) — should bucket as "(global)".
		{Provider: "aws", AccountID: "111", Type: "aws:iam:role", NativeID: "role-x",
			AttributesJSON: "{}", DiscoveredBy: scanID},
	}
	if _, err := st.UpsertResources(rows); err != nil {
		t.Fatalf("upsert: %v", err)
	}
}

func TestSummary_AllSections(t *testing.T) {
	seedSummaryDB(t)
	resetSummaryFlags()

	out, err := captureStdout(t, func() error {
		cmd := rootCmd
		cmd.SetArgs([]string{"summary"})
		return cmd.Execute()
	})
	if err != nil {
		t.Fatalf("summary: %v", err)
	}
	for _, want := range []string{"BY PROVIDER", "BY REGION", "BY TYPE", "(global)", "aws:logs:log-stream"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in output:\n%s", want, out)
		}
	}
}

func TestSummary_FilterByProvider(t *testing.T) {
	seedSummaryDB(t)
	resetSummaryFlags()

	out, err := captureStdout(t, func() error {
		cmd := rootCmd
		cmd.SetArgs([]string{"summary", "--provider", "azure", "-o", "json"})
		return cmd.Execute()
	})
	if err != nil {
		t.Fatalf("summary: %v", err)
	}
	var rep summaryReport
	if jerr := json.Unmarshal([]byte(out), &rep); jerr != nil {
		t.Fatalf("decode: %v\n%s", jerr, out)
	}
	if rep.Total != 0 {
		t.Errorf("provider=azure total: got %d, want 0", rep.Total)
	}
}

func TestSummary_ExcludeTypes(t *testing.T) {
	seedSummaryDB(t)
	resetSummaryFlags()

	out, err := captureStdout(t, func() error {
		cmd := rootCmd
		cmd.SetArgs([]string{"summary", "--exclude-types", "aws:logs:log-stream", "-o", "json"})
		return cmd.Execute()
	})
	if err != nil {
		t.Fatalf("summary: %v", err)
	}
	var rep summaryReport
	if jerr := json.Unmarshal([]byte(out), &rep); jerr != nil {
		t.Fatalf("decode: %v\n%s", jerr, out)
	}
	for _, b := range rep.ByType {
		if b.Type == "aws:logs:log-stream" {
			t.Errorf("excluded type leaked into by_type: %+v", b)
		}
	}
	// seedTestDB rows (2) + seedSummaryDB non-log rows (3) = 5 expected.
	if rep.Total != 5 {
		t.Errorf("denominator: got %d, want 5", rep.Total)
	}
}

func TestSummary_JSON(t *testing.T) {
	seedSummaryDB(t)
	resetSummaryFlags()

	out, err := captureStdout(t, func() error {
		cmd := rootCmd
		cmd.SetArgs([]string{"summary", "-o", "json"})
		return cmd.Execute()
	})
	if err != nil {
		t.Fatalf("summary: %v", err)
	}
	var rep summaryReport
	if jerr := json.Unmarshal([]byte(out), &rep); jerr != nil {
		t.Fatalf("decode: %v\n%s", jerr, out)
	}
	if rep.AsOf == "" {
		t.Errorf("as_of empty (expected non-empty from CreateScan)")
	}
	if rep.Total != 8 {
		t.Errorf("total: got %d, want 8", rep.Total)
	}
	if len(rep.ByProvider) == 0 || rep.ByProvider[0].Provider != "aws" {
		t.Errorf("by_provider top: %+v", rep.ByProvider)
	}
	// log-stream should be tied for top with count 3 (ec2:instance also has 3
	// because seedTestDB seeds one). Sort breaks ties by name ascending.
	found := false
	for _, b := range rep.ByType {
		if b.Type == "aws:logs:log-stream" {
			if b.Count != 3 {
				t.Errorf("log-stream count: got %d, want 3", b.Count)
			}
			found = true
		}
	}
	if !found {
		t.Errorf("log-stream missing from by_type: %+v", rep.ByType)
	}
}

func TestSummary_CSV(t *testing.T) {
	seedSummaryDB(t)
	resetSummaryFlags()

	out, err := captureStdout(t, func() error {
		cmd := rootCmd
		cmd.SetArgs([]string{"summary", "-o", "csv"})
		return cmd.Execute()
	})
	if err != nil {
		t.Fatalf("summary: %v", err)
	}
	r := csv.NewReader(bytes.NewReader([]byte(out)))
	rows, err := r.ReadAll()
	if err != nil {
		t.Fatalf("csv: %v\n%s", err, out)
	}
	if len(rows) < 2 || rows[0][0] != "dimension" {
		t.Fatalf("want header 'dimension,...', got %v", rows[0])
	}
	for _, row := range rows[1:] {
		switch row[0] {
		case "provider", "account", "region", "type":
		default:
			t.Errorf("unexpected dimension: %v", row)
		}
	}
}

func TestSummary_UnknownFormat(t *testing.T) {
	seedSummaryDB(t)
	resetSummaryFlags()

	_, err := captureStdout(t, func() error {
		cmd := rootCmd
		cmd.SetArgs([]string{"summary", "-o", "xml"})
		return cmd.Execute()
	})
	if err == nil || !strings.Contains(err.Error(), "unknown --output format") {
		t.Errorf("want unknown-format error, got %v", err)
	}
}
