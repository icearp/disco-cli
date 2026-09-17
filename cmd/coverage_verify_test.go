package cmd

import (
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/viper"

	"github.com/icearp/disco-cli/store"
)

// seedVerifyDB builds a scan whose stored rows disagree with the declared
// emits in every way verify reports: an undeclared type, a declared type with
// rows, a service that errored, an operation that was skipped, and a provider
// outside the scan scope. Returns the scan id.
func seedVerifyDB(t *testing.T, scope map[string]any, extraErrors ...store.ScanErrorEntry) (*store.Store, string) {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "disco.db")
	viper.Set("db", dbPath)
	t.Cleanup(func() { viper.Set("db", "") })
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	scanID, err := st.CreateScanWithID("verifyscan0000000000000000000001", []string{"aws"}, scope)
	if err != nil {
		t.Fatal(err)
	}
	rows := []*store.Resource{
		{Provider: "aws", AccountID: "111", Type: "aws:ec2:instance", NativeID: "i-1", AttributesJSON: "{}", DiscoveredBy: scanID},
		{Provider: "aws", AccountID: "111", Type: "aws:bogus:thing", NativeID: "x-1", AttributesJSON: "{}", DiscoveredBy: scanID},
	}
	if _, err := st.UpsertResources(rows); err != nil {
		t.Fatal(err)
	}
	// The scan runner persists Service as "<provider>:<service>" for errors and
	// "<provider>:<op label>" for warnings; seed the same shapes.
	errs := append([]store.ScanErrorEntry{
		{Service: "aws:s3", Region: "us-east-1", Code: "AccessDenied", Message: "nope"},
		{Service: "aws:sso-admin", Region: "us-east-1", Code: "Throttling", Message: "slow down"},
	}, extraErrors...)
	for _, e := range errs {
		if err := st.AppendScanError(scanID, e); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.AppendScanWarning(scanID, store.ScanWarningEntry{Service: "aws:kms:ListKeys", Region: "us-east-1", Message: "skipped: access denied"}); err != nil {
		t.Fatal(err)
	}
	if err := st.CompleteScan(scanID); err != nil {
		t.Fatal(err)
	}
	return st, scanID
}

func runVerify(t *testing.T, args ...string) ([]verifyRow, error) {
	t.Helper()
	resetCoverageFlags(t)
	out, err := captureStdout(t, func() error {
		cmd := rootCmd
		cmd.SetArgs(append([]string{"coverage", "verify", "--sdk-cache", t.TempDir(), "--source-root=", "-o", "json"}, args...))
		return cmd.Execute()
	})
	var rows []verifyRow
	if out != "" && !strings.HasPrefix(out, "{") {
		if jerr := json.Unmarshal([]byte(out), &rows); jerr != nil {
			t.Fatalf("json: %v\n%s", jerr, out)
		}
	}
	return rows, err
}

func verifyRowByType(rows []verifyRow, typ string) (verifyRow, bool) {
	for _, r := range rows {
		if r.DiscoType == typ {
			return r, true
		}
	}
	return verifyRow{}, false
}

// TestCoverageVerify_Reasons: an undeclared stored type fails the run (exit 1)
// after the rows render; every declared type without rows carries the reason
// the scan record supports — service error, skipped operation joined by
// service prefix (no pairing offline), or plain "no rows" — and a type the
// scan did store is not reported at all.
func TestCoverageVerify_Reasons(t *testing.T) {
	_, scanID := seedVerifyDB(t, map[string]any{"providers": []string{"aws"}})
	rows, err := runVerify(t, "--providers", "aws", "--scan-id", scanID)
	if !errors.Is(err, errCoverageUndeclared) {
		t.Fatalf("want errCoverageUndeclared, got %v", err)
	}
	if len(rows) == 0 || rows[0].Status != verifyEmittedUndeclared || rows[0].DiscoType != "aws:bogus:thing" || rows[0].Service != "bogus" {
		t.Fatalf("first row = %+v", rows[0])
	}
	if _, ok := verifyRowByType(rows, "aws:ec2:instance"); ok {
		t.Error("aws:ec2:instance was stored and must not be reported")
	}
	for typ, want := range map[string]string{
		"aws:s3:bucket":    "scan-error: AccessDenied (us-east-1)",
		"aws:sso:instance": "scan-error: Throttling (us-east-1)", // scanner service aws:sso-admin, declared service sso
		"aws:kms:key":      "warning: kms:ListKeys (us-east-1): skipped: access denied",
		"aws:ec2:volume":   "no rows",
	} {
		r, ok := verifyRowByType(rows, typ)
		if !ok || r.Status != verifyDeclaredNotEmitted || r.Reason != want {
			t.Errorf("%s = %+v; want reason %q", typ, r, want)
		}
	}
}

// TestCoverageVerify_OutOfScopeAndLatest: with no undeclared rows the run
// succeeds; --scan-id latest finds the completed scan without an explicit id;
// a provider named by --providers but absent from scope.providers reports
// out-of-scope instead of per-service reasons, while the default provider
// set is the scan's scope so those rows do not appear unasked.
func TestCoverageVerify_OutOfScopeAndLatest(t *testing.T) {
	st, _ := seedVerifyDB(t, map[string]any{"providers": []string{"aws"}})
	// Remove the undeclared row so the run is clean.
	if _, err := st.DB().Exec(`DELETE FROM resources WHERE type = 'aws:bogus:thing'`); err != nil {
		t.Fatal(err)
	}
	rows, err := runVerify(t, "--providers", "aws,gcp")
	if err != nil {
		t.Fatalf("verify latest: %v", err)
	}
	for _, r := range rows {
		if r.Status == verifyEmittedUndeclared {
			t.Errorf("unexpected undeclared row %+v", r)
		}
		if r.Provider == "gcp" && !strings.HasPrefix(r.Reason, "out-of-scope") {
			t.Errorf("gcp row %+v; want out-of-scope", r)
		}
	}
	if _, ok := verifyRowByType(rows, "gcp:compute:instance"); !ok {
		t.Error("gcp:compute:instance missing from the declared-not-emitted rows")
	}
	rows, err = runVerify(t)
	if err != nil {
		t.Fatalf("verify default providers: %v", err)
	}
	for _, r := range rows {
		if r.Provider != "aws" {
			t.Fatalf("default providers must follow the scan scope; got %+v", r)
		}
	}
	if _, err := runVerify(t, "--scan-id", "nope"); err == nil {
		t.Error("unknown scan id must error")
	}
}

// TestCoverageVerify_WholeScanError: when the provider's Scan itself failed
// the runner records service "scan"; every declared type of that provider
// is then explained by it, including types no service registers (the GCP
// hierarchy types the scanner core stores).
func TestCoverageVerify_WholeScanError(t *testing.T) {
	seedVerifyDB(t, map[string]any{"providers": []string{"aws"}}, store.ScanErrorEntry{Service: "aws:scan", Code: "Unknown", Message: "load accounts: boom"})
	rows, err := runVerify(t, "--providers", "aws")
	if !errors.Is(err, errCoverageUndeclared) {
		t.Fatalf("want errCoverageUndeclared, got %v", err)
	}
	for _, r := range rows {
		if r.Status == verifyDeclaredNotEmitted && !strings.HasPrefix(r.Reason, "scan-error: ") {
			t.Errorf("%s reason = %q; want a scan-error", r.DiscoType, r.Reason)
		}
	}
}

// TestCoverageVerify_NothingStored: a provider that failed before any service
// ran records one error under a label that is no scanner service
// (aws:load-accounts, an expired login) and stores nothing; every declared
// type is explained by that error rather than reading "no rows".
func TestCoverageVerify_NothingStored(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "disco.db")
	viper.Set("db", dbPath)
	t.Cleanup(func() { viper.Set("db", "") })
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	scanID, err := st.CreateScanWithID("verifyscan0000000000000000000002", []string{"aws"}, map[string]any{"providers": []string{"aws"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.AppendScanError(scanID, store.ScanErrorEntry{Service: "aws:load-accounts", Code: "Error", Message: "login session has expired"}); err != nil {
		t.Fatal(err)
	}
	if err := st.CompleteScan(scanID); err != nil {
		t.Fatal(err)
	}
	rows, err := runVerify(t)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if len(rows) == 0 {
		t.Fatal("no rows")
	}
	for _, r := range rows {
		if r.Reason != "scan-error: Error (load-accounts)" {
			t.Fatalf("%s reason = %q", r.DiscoType, r.Reason)
		}
	}
}

// TestCoverageVerify_NoScan: an empty database has nothing to verify and says so.
func TestCoverageVerify_NoScan(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "disco.db")
	viper.Set("db", dbPath)
	t.Cleanup(func() { viper.Set("db", "") })
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	_ = st.Close()
	if _, err := runVerify(t); err == nil || !strings.Contains(err.Error(), "no completed scan") {
		t.Errorf("want no-scan error, got %v", err)
	}
}
