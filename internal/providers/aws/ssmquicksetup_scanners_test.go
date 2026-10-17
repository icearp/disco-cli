package aws

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ssmquicksetup"
	qstypes "github.com/aws/aws-sdk-go-v2/service/ssmquicksetup/types"

	"github.com/icearp/disco-cli/store"
)

var (
	qsTestDenied = apiErr("AccessDeniedException", "User: arn:aws:iam::123456789012:user/x is not authorized to perform: ssm-quicksetup:List")
	errQSTest    = errors.New("ssm-quicksetup test failure")
)

const (
	qsMgrARN      = "arn:aws:ssm-quicksetup:us-east-1:123456789012:configuration-manager/11111111-2222-3333-4444-555555555555"
	qsOtherMgrARN = "arn:aws:ssm-quicksetup:us-east-1:123456789012:configuration-manager/99999999-8888-7777-6666-555555555555"
	qsCfg1Native  = qsMgrARN + "/configuration/cfg-1"
	qsCfg2Native  = qsOtherMgrARN + "/configuration/cfg-2"
	qsTargetAcct  = "210987654321"
	qsManagerRows = 1
)

// qsRecorder captures the MaxItems each ListConfigurations call was made with.
type qsRecorder struct {
	mu       sync.Mutex
	maxItems []int32
}

func (r *qsRecorder) record(v *int32) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.maxItems = append(r.maxItems, sdkaws.ToInt32(v))
}

func qsConfig(managerARN, id string) qstypes.ConfigurationSummary {
	return qstypes.ConfigurationSummary{
		ManagerArn: ptrStr(managerARN), Id: ptrStr(id), Account: ptrStr(testAccountID), Region: ptrStr(testRegion),
		Type: ptrStr("AWSQuickSetupType-SSMHostMgmt"), CreatedAt: sdkaws.Time(time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)),
	}
}

// qsDeployed is a configuration of qsMgrARN deployed to account and region,
// either left nil when "".
func qsDeployed(id, account, region string) qstypes.ConfigurationSummary {
	c := qsConfig(qsMgrARN, id)
	c.Account, c.Region = nonEmptyPtr(account), nonEmptyPtr(region)
	return c
}

// qsHandlers answers every ssm-quicksetup op: one manager, and two
// configuration pages, the first holding an element without its Id. The
// second page's configuration names a manager ListConfigurationManagers did
// not return.
func qsHandlers(rec *qsRecorder) map[string]func(any) (any, error) {
	return map[string]func(any) (any, error){
		"ListConfigurationManagers": func(any) (any, error) {
			return &ssmquicksetup.ListConfigurationManagersOutput{ConfigurationManagersList: []qstypes.ConfigurationManagerSummary{
				{ManagerArn: ptrStr(qsMgrARN), Name: ptrStr("host-mgmt")},
			}}, nil
		},
		"GetConfigurationManager": func(any) (any, error) {
			return &ssmquicksetup.GetConfigurationManagerOutput{ManagerArn: ptrStr(qsMgrARN), Name: ptrStr("host-mgmt")}, nil
		},
		"ListConfigurations": func(in any) (any, error) {
			req := in.(*ssmquicksetup.ListConfigurationsInput)
			rec.record(req.MaxItems)
			if req.StartingToken == nil {
				return &ssmquicksetup.ListConfigurationsOutput{
					ConfigurationsList: []qstypes.ConfigurationSummary{qsConfig(qsMgrARN, "cfg-1"), {ManagerArn: ptrStr(qsMgrARN)}},
					NextToken:          ptrStr("p2"),
				}, nil
			}
			return &ssmquicksetup.ListConfigurationsOutput{ConfigurationsList: []qstypes.ConfigurationSummary{qsConfig(qsOtherMgrARN, "cfg-2")}}, nil
		},
	}
}

func scanQSTopLevel(t *testing.T, st *store.Store, handlers map[string]func(any) (any, error)) (int, error) {
	t.Helper()
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(handlers, qsTestDenied)
	total, _, err := scanSSMQuickSetup(context.Background(), acct, testRegion, st, testScanID)
	return total, err
}

func TestScanSSMQuickSetup_StoresConfigurations(t *testing.T) {
	st := newTestStore(t)
	rec := &qsRecorder{}
	total, err := scanQSTopLevel(t, st, qsHandlers(rec))
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if total != qsManagerRows+2 {
		t.Errorf("total = %d, want %d", total, qsManagerRows+2)
	}
	if !slices.Equal(rec.maxItems, []int32{100, 100}) {
		t.Errorf("MaxItems = %v, want [100 100]", rec.maxItems)
	}
	rows := rowsByNativeID(t, st, TypeSSMQuickSetupConfiguration)
	if len(rows) != 2 {
		t.Fatalf("configuration rows = %v, want 2", rows)
	}
	r, ok := rows[qsCfg1Native]
	if !ok {
		t.Fatalf("no row %s in %v", qsCfg1Native, rows)
	}
	if r.Type != TypeSSMQuickSetupConfiguration || r.AccountID != testAccountID || sv(r.Region) != testRegion || sv(r.Name) != "cfg-1" {
		t.Errorf("row = type %q account %q region %q name %q", r.Type, r.AccountID, sv(r.Region), sv(r.Name))
	}
	var attrs struct {
		Account, Region string
		CreatedAt       time.Time
	}
	if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
		t.Fatalf("attrs %s: %v", r.AttributesJSON, err)
	}
	if attrs.Account != testAccountID || attrs.Region != testRegion || !attrs.CreatedAt.Equal(time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)) {
		t.Errorf("attrs = %s, want account %s, region %s, CreatedAt 2026-01-02T03:04:05Z", r.AttributesJSON, testAccountID, testRegion)
	}
	if _, ok := rows[qsCfg2Native]; !ok {
		t.Errorf("no row %s in %v", qsCfg2Native, rows)
	}
	assertContainsNative(t, st, qsMgrARN, qsCfg1Native)
}

// A configuration naming a manager ListConfigurationManagers did not return is
// stored unlinked, with no pair recorded for the store to warn about.
func TestScanSSMQuickSetup_ConfigurationOfUnlistedManagerUnlinked(t *testing.T) {
	st := newTestStore(t)
	var warnings []store.ScanWarning
	st.OnWarn = func(w store.ScanWarning) { warnings = append(warnings, w) }
	if _, err := scanQSTopLevel(t, st, qsHandlers(&qsRecorder{})); err != nil {
		t.Fatalf("err = %v", err)
	}
	if len(warnings) != 0 {
		t.Errorf("warnings = %+v, want none", warnings)
	}
	rels, err := st.RelationshipsTo(store.ResourceID("aws", testAccountID, qsCfg2Native), store.RelContains)
	if err != nil {
		t.Fatalf("RelationshipsTo: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("contains edges to %s = %+v, want none", qsCfg2Native, rels)
	}
}

// Configurations deployed to another account or region are not stored, and
// however many there are, one warning reports them; local ones are stored.
func TestScanSSMQuickSetup_ForeignDeploymentsWarnOnce(t *testing.T) {
	cases := []struct {
		name      string
		foreign   []qstypes.ConfigurationSummary
		wantWarns int
	}{
		{"all local", nil, 0},
		{"foreign region", []qstypes.ConfigurationSummary{qsDeployed("cfg-r", testAccountID, "eu-west-1")}, 1},
		{"foreign account", []qstypes.ConfigurationSummary{qsDeployed("cfg-a", qsTargetAcct, testRegion)}, 1},
		{"both", []qstypes.ConfigurationSummary{
			qsDeployed("cfg-r", testAccountID, "eu-west-1"), qsDeployed("cfg-a", qsTargetAcct, testRegion),
		}, 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			h := qsHandlers(&qsRecorder{})
			h["ListConfigurations"] = func(any) (any, error) {
				return &ssmquicksetup.ListConfigurationsOutput{
					ConfigurationsList: append([]qstypes.ConfigurationSummary{qsConfig(qsMgrARN, "cfg-1")}, c.foreign...),
				}, nil
			}
			warnings := countOpWarnings(st, ssmQuickSetupConfigurationsOp)
			total, err := scanQSTopLevel(t, st, h)
			if err != nil {
				t.Fatalf("err = %v", err)
			}
			if *warnings != c.wantWarns {
				t.Errorf("warnings = %d, want %d", *warnings, c.wantWarns)
			}
			if total != qsManagerRows+1 {
				t.Errorf("total = %d, want %d", total, qsManagerRows+1)
			}
			rows := rowsByNativeID(t, st, TypeSSMQuickSetupConfiguration)
			if _, ok := rows[qsCfg1Native]; !ok || len(rows) != 1 {
				t.Errorf("configuration rows = %v, want only %s", rows, qsCfg1Native)
			}
		})
	}
}

// A configuration lacking its Account or Region is skipped without a warning.
func TestScanSSMQuickSetup_MissingDeploymentTargetSkipped(t *testing.T) {
	st := newTestStore(t)
	h := qsHandlers(&qsRecorder{})
	h["ListConfigurations"] = func(any) (any, error) {
		return &ssmquicksetup.ListConfigurationsOutput{ConfigurationsList: []qstypes.ConfigurationSummary{
			qsDeployed("cfg-noacct", "", testRegion), qsDeployed("cfg-noregion", testAccountID, ""),
		}}, nil
	}
	warnings := countOpWarnings(st, ssmQuickSetupConfigurationsOp)
	if _, err := scanQSTopLevel(t, st, h); err != nil {
		t.Fatalf("err = %v", err)
	}
	if *warnings != 0 {
		t.Errorf("warnings = %d, want 0", *warnings)
	}
	if n := len(rowsByNativeID(t, st, TypeSSMQuickSetupConfiguration)); n != 0 {
		t.Errorf("configuration rows = %d, want 0", n)
	}
}

func TestScanSSMQuickSetup_EmptyConfigurations(t *testing.T) {
	st := newTestStore(t)
	h := qsHandlers(&qsRecorder{})
	h["ListConfigurations"] = func(any) (any, error) { return &ssmquicksetup.ListConfigurationsOutput{}, nil }
	total, err := scanQSTopLevel(t, st, h)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if total != qsManagerRows {
		t.Errorf("total = %d, want %d (manager rows only)", total, qsManagerRows)
	}
	if n := len(rowsByNativeID(t, st, TypeSSMQuickSetupConfiguration)); n != 0 {
		t.Errorf("configuration rows = %d, want 0", n)
	}
}

// A denial warns once, keeps the rows of the pages before it, and costs the
// manager phase nothing.
func TestScanSSMQuickSetup_ConfigurationsDenied(t *testing.T) {
	cases := []struct {
		name     string
		deniedAt int
		wantRows []string
	}{
		{"first page", 1, nil},
		{"second page", 2, []string{qsCfg1Native}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			h := qsHandlers(&qsRecorder{})
			first := h["ListConfigurations"]
			calls := 0
			h["ListConfigurations"] = func(in any) (any, error) {
				calls++
				if calls == c.deniedAt {
					return nil, qsTestDenied
				}
				return first(in)
			}
			warnings := countOpWarnings(st, ssmQuickSetupConfigurationsOp)
			total, err := scanQSTopLevel(t, st, h)
			if err != nil {
				t.Fatalf("err = %v, want nil", err)
			}
			if *warnings != 1 {
				t.Errorf("warnings = %d, want 1", *warnings)
			}
			if total != qsManagerRows+len(c.wantRows) {
				t.Errorf("total = %d, want %d", total, qsManagerRows+len(c.wantRows))
			}
			rows := rowsByNativeID(t, st, TypeSSMQuickSetupConfiguration)
			if len(rows) != len(c.wantRows) {
				t.Errorf("configuration rows = %v, want %v", rows, c.wantRows)
			}
			for _, n := range c.wantRows {
				if _, ok := rows[n]; !ok {
					t.Errorf("missing row %s", n)
				}
			}
			if n := len(rowsByNativeID(t, st, TypeSSMQuickSetupConfigurationManager)); n != qsManagerRows {
				t.Errorf("manager rows = %d, want %d", n, qsManagerRows)
			}
		})
	}
}

// A hard error in either phase is returned, and the other phase's rows are
// stored: the configuration phase runs after the manager phase, and runs even
// when it fails (its rows are then stored unlinked).
func TestScanSSMQuickSetup_PhaseErrorPropagates(t *testing.T) {
	cases := []struct {
		op           string
		wantTotal    int
		wantConfigs  int
		wantManagers int
	}{
		{"ListConfigurationManagers", 2, 2, 0},
		{"ListConfigurations", qsManagerRows, 0, qsManagerRows},
	}
	for _, c := range cases {
		t.Run(c.op, func(t *testing.T) {
			st := newTestStore(t)
			h := qsHandlers(&qsRecorder{})
			h[c.op] = func(any) (any, error) { return nil, errQSTest }
			total, err := scanQSTopLevel(t, st, h)
			if !errors.Is(err, errQSTest) {
				t.Fatalf("err = %v, want the %s error", err, c.op)
			}
			if total != c.wantTotal {
				t.Errorf("total = %d, want %d", total, c.wantTotal)
			}
			if n := len(rowsByNativeID(t, st, TypeSSMQuickSetupConfiguration)); n != c.wantConfigs {
				t.Errorf("configuration rows = %d, want %d", n, c.wantConfigs)
			}
			if n := len(rowsByNativeID(t, st, TypeSSMQuickSetupConfigurationManager)); n != c.wantManagers {
				t.Errorf("manager rows = %d, want %d", n, c.wantManagers)
			}
		})
	}
}

// A hard error on a later page is returned, and the rows of the page before
// it are stored and linked.
func TestScanSSMQuickSetup_ConfigurationErrorOnSecondPageKeepsFirst(t *testing.T) {
	st := newTestStore(t)
	h := qsHandlers(&qsRecorder{})
	first := h["ListConfigurations"]
	h["ListConfigurations"] = func(in any) (any, error) {
		if in.(*ssmquicksetup.ListConfigurationsInput).StartingToken != nil {
			return nil, errQSTest
		}
		return first(in)
	}
	total, err := scanQSTopLevel(t, st, h)
	if !errors.Is(err, errQSTest) {
		t.Fatalf("err = %v, want the configurations error", err)
	}
	if total != qsManagerRows+1 {
		t.Errorf("total = %d, want %d", total, qsManagerRows+1)
	}
	rows := rowsByNativeID(t, st, TypeSSMQuickSetupConfiguration)
	if _, ok := rows[qsCfg1Native]; !ok || len(rows) != 1 {
		t.Errorf("configuration rows = %v, want only %s", rows, qsCfg1Native)
	}
	assertContainsNative(t, st, qsMgrARN, qsCfg1Native)
}

// A page that repeats the token it was asked with ends the listing.
func TestScanSSMQuickSetup_ConfigurationsRepeatedTokenStops(t *testing.T) {
	st := newTestStore(t)
	h := qsHandlers(&qsRecorder{})
	calls := 0
	h["ListConfigurations"] = func(any) (any, error) {
		calls++
		if calls > 3 {
			return nil, errQSTest
		}
		id := []string{"", "cfg-a", "cfg-b", "cfg-c"}[calls]
		return &ssmquicksetup.ListConfigurationsOutput{
			ConfigurationsList: []qstypes.ConfigurationSummary{qsConfig(qsMgrARN, id)},
			NextToken:          ptrStr("same"),
		}, nil
	}
	if _, err := scanQSTopLevel(t, st, h); err != nil {
		t.Fatalf("err = %v", err)
	}
	if calls != 2 {
		t.Errorf("calls = %d, want 2", calls)
	}
	if n := len(rowsByNativeID(t, st, TypeSSMQuickSetupConfiguration)); n != 2 {
		t.Errorf("configuration rows = %d, want 2", n)
	}
}

// A phase's transient error is demoted to a warning, so it cannot make the
// dispatcher read the other phase's hard error as transient.
func TestScanSSMQuickSetup_TransientPhaseDoesNotMaskHardError(t *testing.T) {
	throttle := apiErr("ThrottlingException", "Rate exceeded")
	cases := []struct {
		name, transientOp, hardOp, warnOp string
	}{
		{"throttled manager phase", "ListConfigurationManagers", "ListConfigurations", "ssm-quicksetup:ListConfigurationManagers"},
		{"throttled configuration phase", "ListConfigurations", "ListConfigurationManagers", ssmQuickSetupConfigurationsOp},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			h := qsHandlers(&qsRecorder{})
			h[c.transientOp] = func(any) (any, error) { return nil, throttle }
			h[c.hardOp] = func(any) (any, error) { return nil, errQSTest }
			warnings := countOpWarnings(st, c.warnOp)
			_, err := scanQSTopLevel(t, st, h)
			if !errors.Is(err, errQSTest) {
				t.Fatalf("err = %v, want the %s error", err, c.hardOp)
			}
			if got := classifyServiceError(err); got != outcomeError {
				t.Errorf("classifyServiceError(%v) = %v, want outcomeError", err, got)
			}
			if *warnings != 1 {
				t.Errorf("%s warnings = %d, want 1", c.warnOp, *warnings)
			}
		})
	}
}
