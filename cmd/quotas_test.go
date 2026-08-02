package cmd

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/icearp/disco-cli/store"
)

// seedQuotas adds three quotas to the seeded DB: one raised adjustable limit,
// one non-adjustable limit sitting at its default, and one in another region.
func seedQuotas(t *testing.T, st *store.Store) {
	t.Helper()
	scanID, err := st.CreateScan([]string{"aws"}, map[string]any{})
	if err != nil {
		t.Fatalf("CreateScan: %v", err)
	}
	f := func(v float64) *float64 { return &v }
	unit := "None"
	quotas := []*store.Quota{
		{
			Provider: "aws", AccountID: "111", Region: "us-east-1",
			ServiceCode: "ec2", QuotaCode: "L-RAISED", Name: "VPCs per Region",
			Unit: &unit, Value: f(64), DefaultValue: f(5), Adjustable: true,
			AttributesJSON: `{}`, DiscoveredBy: scanID,
		},
		{
			Provider: "aws", AccountID: "111", Region: "us-east-1",
			ServiceCode: "lambda", QuotaCode: "L-FIXED", Name: "Function timeout",
			Unit: &unit, Value: f(900), DefaultValue: f(900), Adjustable: false,
			AttributesJSON: `{}`, DiscoveredBy: scanID,
		},
		{
			Provider: "aws", AccountID: "111", Region: "eu-west-1",
			ServiceCode: "s3", QuotaCode: "L-BUCKETS", Name: "Buckets",
			Value: f(100), Adjustable: true,
			AttributesJSON: `{}`, DiscoveredBy: scanID,
		},
	}
	if _, err := st.UpsertQuotas(quotas); err != nil {
		t.Fatalf("UpsertQuotas: %v", err)
	}
}

// runQuotas executes `disco quotas` with the given args and decodes the JSON.
func runQuotas(t *testing.T, args ...string) []store.Quota {
	t.Helper()
	quotasOutputFmt = "table"
	quotasAdjustable = ""
	quotasRaisedOnly = false
	quotasChangedOnly = false
	quotasProviders, quotasAccounts, quotasRegions, quotasServiceCodes = nil, nil, nil, nil

	out, err := captureStdout(t, func() error {
		rootCmd.SetArgs(append([]string{"quotas"}, append(args, "-o", "json")...))
		return rootCmd.Execute()
	})
	if err != nil {
		t.Fatalf("quotas %v: %v", args, err)
	}
	var got []store.Quota
	if jerr := json.Unmarshal([]byte(out), &got); jerr != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", jerr, out)
	}
	return got
}

func TestQuotasCmd_ListsAndFilters(t *testing.T) {
	st := seedTestDB(t)
	seedQuotas(t, st)

	if got := runQuotas(t); len(got) != 3 {
		t.Fatalf("unfiltered: got %d quotas, want 3", len(got))
	}

	// The whole point of recording non-adjustable limits is being able to ask
	// for exactly them, so this filter is the one that matters most.
	fixed := runQuotas(t, "--adjustable=false")
	if len(fixed) != 1 || fixed[0].QuotaCode != "L-FIXED" {
		t.Fatalf("--adjustable=false returned %d rows: %+v", len(fixed), fixed)
	}

	raised := runQuotas(t, "--raised")
	if len(raised) != 1 || raised[0].QuotaCode != "L-RAISED" {
		t.Fatalf("--raised returned %d rows: %+v", len(raised), raised)
	}

	byService := runQuotas(t, "--service", "ec2")
	if len(byService) != 1 || byService[0].ServiceCode != "ec2" {
		t.Fatalf("--service ec2 returned %d rows: %+v", len(byService), byService)
	}

	byRegion := runQuotas(t, "--regions", "eu-west-1")
	if len(byRegion) != 1 || byRegion[0].Region != "eu-west-1" {
		t.Fatalf("--regions eu-west-1 returned %d rows: %+v", len(byRegion), byRegion)
	}
}

// A plain `disco quotas` must not silently behave like --adjustable=false. The
// tristate flag exists precisely because a bool flag's zero value and an
// explicit false are indistinguishable.
func TestQuotasCmd_AdjustableUnsetDoesNotFilter(t *testing.T) {
	st := seedTestDB(t)
	seedQuotas(t, st)

	all := runQuotas(t)
	adjustable := 0
	for _, q := range all {
		if q.Adjustable {
			adjustable++
		}
	}
	if adjustable != 2 {
		t.Fatalf("unset --adjustable returned %d adjustable rows, want both", adjustable)
	}
}

func TestQuotasCmd_RejectsNonBooleanAdjustable(t *testing.T) {
	st := seedTestDB(t)
	seedQuotas(t, st)

	quotasOutputFmt = "table"
	quotasAdjustable = ""
	_, err := captureStdout(t, func() error {
		rootCmd.SetArgs([]string{"quotas", "--adjustable", "maybe"})
		return rootCmd.Execute()
	})
	if err == nil || !strings.Contains(err.Error(), "not a boolean") {
		t.Errorf("expected a boolean-parse error, got %v", err)
	}
}

// `disco quotas` on a database that never ran a quota scan is a valid outcome,
// not a failure — and the note has to name the flag that would fix it.
func TestQuotasCmd_EmptyDatabase(t *testing.T) {
	_ = seedTestDB(t)

	quotasOutputFmt = "table"
	quotasAdjustable = ""
	out, err := captureStdout(t, func() error {
		rootCmd.SetArgs([]string{"quotas"})
		return rootCmd.Execute()
	})
	if err != nil {
		t.Fatalf("quotas on an empty table: %v", err)
	}
	if strings.TrimSpace(out) != "" {
		t.Errorf("expected no stdout rows, got %q", out)
	}
}

// `disco history` accepts a quota id and prints the limit's value history.
// A user pasting an id should not have to know which table it came from.
func TestHistoryCmd_QuotaID(t *testing.T) {
	st := seedTestDB(t)
	seedQuotas(t, st)

	// Raise the fixed limit — the provider moving a hard ceiling, which is the
	// case this history view exists for.
	scanB, err := st.CreateScan([]string{"aws"}, map[string]any{})
	if err != nil {
		t.Fatalf("CreateScan: %v", err)
	}
	moved := 1800.0
	unit := "None"
	def := 1800.0
	if _, err := st.UpsertQuotas([]*store.Quota{{
		Provider: "aws", AccountID: "111", Region: "us-east-1",
		ServiceCode: "lambda", QuotaCode: "L-FIXED", Name: "Function timeout",
		Unit: &unit, Value: &moved, DefaultValue: &def, Adjustable: false,
		AttributesJSON: `{}`, DiscoveredBy: scanB,
	}}); err != nil {
		t.Fatalf("UpsertQuotas: %v", err)
	}

	rootID := store.QuotaID("aws", "111", "us-east-1", "lambda", "L-FIXED")
	historyOutputFmt = "table"
	out, err := captureStdout(t, func() error {
		rootCmd.SetArgs([]string{"history", rootID, "-o", "json"})
		return rootCmd.Execute()
	})
	if err != nil {
		t.Fatalf("history on a quota id: %v", err)
	}

	var entries []quotaHistoryEntry
	if jerr := json.Unmarshal([]byte(out), &entries); jerr != nil {
		t.Fatalf("output is not valid JSON: %v\n%s", jerr, out)
	}
	if len(entries) != 2 {
		t.Fatalf("want 2 versions, got %d: %s", len(entries), out)
	}
	if entries[0].Value == nil || *entries[0].Value != 900 {
		t.Errorf("v1 value: got %v, want 900", entries[0].Value)
	}
	if entries[1].Value == nil || *entries[1].Value != 1800 {
		t.Errorf("v2 value: got %v, want 1800", entries[1].Value)
	}
	if entries[0].Current || !entries[1].Current {
		t.Error("current flag: only the newest version may be current")
	}
	if entries[1].Adjustable {
		t.Error("adjustable flag did not survive into the history view")
	}
	if entries[1].ID != rootID {
		t.Errorf("id: got %s, want root %s", entries[1].ID, rootID)
	}
}

// An argument matching neither a resource nor a quota must surface the resource
// error, not a confusing quota one — resources are what the command is mostly for.
func TestHistoryCmd_UnknownIDPrefersResourceError(t *testing.T) {
	st := seedTestDB(t)
	seedQuotas(t, st)

	historyOutputFmt = "table"
	_, err := captureStdout(t, func() error {
		rootCmd.SetArgs([]string{"history", "no-such-thing-anywhere"})
		return rootCmd.Execute()
	})
	if err == nil || !strings.Contains(err.Error(), "no resource matching") {
		t.Errorf("expected the resource-not-found error, got %v", err)
	}
}

// TestQuotasChangedFlag pins --changed against the case it is easy to conflate
// with --raised.
//
// L-FIXED sits exactly on its default and has moved, so --raised must drop it
// and --changed must keep it. That pair is the whole point of the second flag:
// "the value differs from the default" is a state, "the value has moved" is an
// event, and a hard ceiling the provider moved back onto its default is
// invisible to the first question.
func TestQuotasCmd_ChangedFlag(t *testing.T) {
	st := seedTestDB(t)
	seedQuotas(t, st)

	// Move the non-adjustable limit, which splits its version chain.
	scanID, err := st.CreateScan([]string{"aws"}, map[string]any{})
	if err != nil {
		t.Fatalf("CreateScan: %v", err)
	}
	f := func(v float64) *float64 { return &v }
	unit := "None"
	if _, err := st.UpsertQuotas([]*store.Quota{{
		Provider: "aws", AccountID: "111", Region: "us-east-1",
		ServiceCode: "lambda", QuotaCode: "L-FIXED", Name: "Function timeout",
		Unit: &unit, Value: f(300), DefaultValue: f(900), Adjustable: false,
		AttributesJSON: `{}`, DiscoveredBy: scanID,
	}}); err != nil {
		t.Fatalf("UpsertQuotas: %v", err)
	}

	changed := runQuotas(t, "--changed")
	if len(changed) != 1 || changed[0].QuotaCode != "L-FIXED" {
		t.Fatalf("--changed returned %d rows: %+v", len(changed), changed)
	}

	// Rescanning the same value must not manufacture a change, or every limit
	// would qualify after two scans and the filter would mean nothing.
	all := runQuotas(t)
	if len(all) != 3 {
		t.Fatalf("expected 3 current quotas, got %d", len(all))
	}
	if got := runQuotas(t, "--changed", "--adjustable=false"); len(got) != 1 {
		t.Fatalf("--changed --adjustable=false returned %d rows", len(got))
	}
	if got := runQuotas(t, "--changed", "--adjustable=true"); len(got) != 0 {
		t.Fatalf("no adjustable limit has moved, got %d rows", len(got))
	}
}
