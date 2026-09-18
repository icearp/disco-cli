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
	scanID, err := st.CreateScanWithID("a1b2c3d4000000000000000000000001", []string{"aws"}, scope)
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
		"aws:s3:bucket":    "scan-error: AccessDenied (s3) (us-east-1): nope",
		"aws:sso:instance": "scan-error: Throttling (sso-admin) (us-east-1): slow down", // scanner service aws:sso-admin, declared service sso
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
	scanID, err := st.CreateScanWithID("a1b2c3d4000000000000000000000002", []string{"aws"}, map[string]any{"providers": []string{"aws"}})
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
		if r.Reason != "scan-error: Error (load-accounts): login session has expired" {
			t.Fatalf("%s reason = %q", r.DiscoType, r.Reason)
		}
	}
}

// TestLabelIndexExplains: with the pairing present only a label paired to
// the type explains it — a store-level warning labelled by a type
// ("organizations:root … maps to both types") must not explain every
// organizations type by prefix; without the pairing the prefix is all
// there is.
func TestLabelIndexExplains(t *testing.T) {
	services := map[string]bool{"organizations": true}
	paired := labelIndex{
		byType: map[string]map[string]bool{"aws:organizations:account": {"organizations:ListAccounts": true}},
		known:  map[string]bool{"organizations:ListAccounts": true, "organizations:ListRoots": true},
	}
	for _, tc := range []struct {
		name    string
		ix      labelIndex
		label   string
		svcName string // what the scan recorded as the warning's service
		want    bool
	}{
		{"paired label", paired, "organizations:ListAccounts", "", true},
		{"known label of another type", paired, "organizations:ListRoots", "", false},
		{"unknown label with pairing", paired, "organizations:root", "", false},
		{"prefix without pairing", labelIndex{}, "organizations:root", "", true},
		{"other service without pairing", labelIndex{}, "kms:ListKeys", "", false},
		// A warning the scan tagged with its service explains the service's
		// types in both modes; an unknown label alone still does not.
		{"service-tagged with pairing", paired, "armorganizations:Accounts.List", "aws:organizations", true},
		{"service-tagged, other service", paired, "armkms:Keys.List", "aws:kms", false},
		{"service-tagged without pairing", labelIndex{}, "armorganizations:Accounts.List", "organizations", true},
	} {
		w := store.ScanWarningEntry{Service: tc.label, ServiceName: tc.svcName}
		if got := tc.ix.explains(w, tc.label, "aws:organizations:account", services, "aws"); got != tc.want {
			t.Errorf("%s: explains(%q) = %v; want %v", tc.name, tc.label, got, tc.want)
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

// TestCoverageVerify_ScanIDPrefix: the ids `disco scans` prints are 8-char
// prefixes, and every other scan-id-taking command accepts them.
func TestCoverageVerify_ScanIDPrefix(t *testing.T) {
	_, scanID := seedVerifyDB(t, map[string]any{"providers": []string{"aws"}})
	rows, err := runVerify(t, "--providers", "aws", "--scan-id", scanID[:8])
	if !errors.Is(err, errCoverageUndeclared) {
		t.Fatalf("want errCoverageUndeclared, got %v", err)
	}
	if len(rows) == 0 {
		t.Fatal("no rows for an 8-char scan id prefix")
	}
	if _, err := runVerify(t, "--providers", "aws", "--scan-id", "deadbeef"); err == nil {
		t.Error("an unmatched prefix resolved to a scan")
	}
}

// TestCoverageVerify_ExactMatchBeatsStoredNothingFallback: with nothing
// stored, an unlucky error order used to let the first provider-prefixed
// entry answer for every type, hiding the whole-scan failure entirely.
func TestCoverageVerify_ExactMatchBeatsStoredNothingFallback(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "disco.db")
	viper.Set("db", dbPath)
	t.Cleanup(func() { viper.Set("db", "") })
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	scanID, err := st.CreateScanWithID("a1b2c3d4000000000000000000000003", []string{"aws"}, map[string]any{"providers": []string{"aws"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range []store.ScanErrorEntry{
		{Service: "aws:iam", Region: "us-west-2", Code: "AccessDenied", Message: "denied"},
		{Service: "aws:scan", Code: "Canceled", Message: "run aborted"},
	} {
		if err := st.AppendScanError(scanID, e); err != nil {
			t.Fatal(err)
		}
	}
	if err := st.CompleteScan(scanID); err != nil {
		t.Fatal(err)
	}
	rows, err := runVerify(t, "--providers", "aws")
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	r, ok := verifyRowByType(rows, "aws:ec2:instance")
	if !ok {
		t.Fatal("aws:ec2:instance missing")
	}
	if !strings.HasPrefix(r.Reason, "scan-error: Canceled") {
		t.Errorf("reason = %q; want the whole-scan Canceled entry", r.Reason)
	}
	iam, ok := verifyRowByType(rows, "aws:iam:role")
	if !ok {
		t.Fatal("aws:iam:role missing")
	}
	if !strings.HasPrefix(iam.Reason, "scan-error: AccessDenied") {
		t.Errorf("iam reason = %q; want its own service error", iam.Reason)
	}
}

// TestCoverageVerify_Interrupted: Ctrl-C records one entry with no provider
// prefix, and it is the default --scan-id latest target right afterwards.
func TestCoverageVerify_Interrupted(t *testing.T) {
	seedVerifyDB(t, map[string]any{"providers": []string{"aws"}},
		store.ScanErrorEntry{Service: "scan:interrupted", Code: "Canceled", Message: "interrupted by signal"})
	rows, err := runVerify(t, "--providers", "aws")
	if !errors.Is(err, errCoverageUndeclared) {
		t.Fatalf("want errCoverageUndeclared, got %v", err)
	}
	r, ok := verifyRowByType(rows, "aws:ec2:volume")
	if !ok {
		t.Fatal("aws:ec2:volume missing")
	}
	if !strings.HasPrefix(r.Reason, "scan-error: Canceled") {
		t.Errorf("reason = %q; want the interruption", r.Reason)
	}
	if strings.Contains(r.Reason, "("+interruptedService+")") {
		t.Errorf("reason = %q; the whole-scan label is not a service", r.Reason)
	}
}

// TestCoverageVerify_ServiceScope: a --services-filtered scan is not an empty
// account, and the filtered-out types say so.
func TestCoverageVerify_ServiceScope(t *testing.T) {
	seedVerifyDB(t, map[string]any{
		"providers": []string{"aws"},
		"aws":       map[string]any{"services": []string{"aws:ec2"}},
	})
	rows, err := runVerify(t, "--providers", "aws")
	if !errors.Is(err, errCoverageUndeclared) {
		t.Fatalf("want errCoverageUndeclared, got %v", err)
	}
	r, ok := verifyRowByType(rows, "aws:s3:bucket")
	if !ok {
		t.Fatal("aws:s3:bucket missing")
	}
	if r.Reason != "out-of-scope: service not in --services" {
		t.Errorf("s3 reason = %q; want the service scope", r.Reason)
	}
	// A type of the selected service keeps the reason its own service earned.
	vol, ok := verifyRowByType(rows, "aws:ec2:volume")
	if !ok {
		t.Fatal("aws:ec2:volume missing")
	}
	if vol.Reason != "no rows" {
		t.Errorf("ec2 volume reason = %q; want \"no rows\"", vol.Reason)
	}
}

// TestCoverageVerify_NothingStoredNoFailure: "no rows" claims the account has
// none; a provider that stored nothing and recorded no failure has not
// established that.
func TestCoverageVerify_NothingStoredNoFailure(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "disco.db")
	viper.Set("db", dbPath)
	t.Cleanup(func() { viper.Set("db", "") })
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	scanID, err := st.CreateScanWithID("a1b2c3d4000000000000000000000004", []string{"aws"}, map[string]any{"providers": []string{"aws"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.CompleteScan(scanID); err != nil {
		t.Fatal(err)
	}
	rows, err := runVerify(t, "--providers", "aws")
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if len(rows) == 0 {
		t.Fatal("no rows")
	}
	for _, r := range rows {
		if r.Reason != "nothing stored: aws recorded no rows and no failure" {
			t.Fatalf("%s reason = %q", r.DiscoType, r.Reason)
		}
	}
}

// TestCoverageVerify_SubscriptionScopedError: one unreachable subscription is
// not a failure of the provider, so it must not answer for tenant-scoped
// types when other subscriptions stored rows.
func TestCoverageVerify_SubscriptionScopedError(t *testing.T) {
	seedVerifyDB(t, map[string]any{"providers": []string{"aws"}},
		store.ScanErrorEntry{Service: "aws:scan:subscription", Scope: "sub-1", Code: "AuthorizationFailed", Message: "denied"})
	rows, err := runVerify(t, "--providers", "aws")
	if !errors.Is(err, errCoverageUndeclared) {
		t.Fatalf("want errCoverageUndeclared, got %v", err)
	}
	r, ok := verifyRowByType(rows, "aws:ec2:volume")
	if !ok {
		t.Fatal("aws:ec2:volume missing")
	}
	if strings.Contains(r.Reason, "AuthorizationFailed") {
		t.Errorf("reason = %q; a scope-carrying failure must not answer for every type", r.Reason)
	}
}

// TestCoverageVerify_RegionScope: a --regions run says so rather than
// claiming the account holds none, and says it about the scan rather than
// about the type, which may well be global.
func TestCoverageVerify_RegionScope(t *testing.T) {
	seedVerifyDB(t, map[string]any{
		"providers": []string{"aws"},
		"aws":       map[string]any{"regions": []string{"us-east-2"}},
	})
	rows, err := runVerify(t, "--providers", "aws")
	if !errors.Is(err, errCoverageUndeclared) {
		t.Fatalf("want errCoverageUndeclared, got %v", err)
	}
	r, ok := verifyRowByType(rows, "aws:ec2:volume")
	if !ok {
		t.Fatal("aws:ec2:volume missing")
	}
	if r.Reason != "no rows (scan limited to regions: us-east-2)" {
		t.Errorf("reason = %q; want the region-limited form", r.Reason)
	}
}
