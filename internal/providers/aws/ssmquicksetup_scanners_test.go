package aws

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
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
	qsMgr2ARN     = "arn:aws:ssm-quicksetup:us-east-1:123456789012:configuration-manager/aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	qsOtherMgrARN = "arn:aws:ssm-quicksetup:us-east-1:123456789012:configuration-manager/99999999-8888-7777-6666-555555555555"
	qsCfg1Native  = qsMgrARN + "/configuration/cfg-1"
	qsCfg2Native  = qsOtherMgrARN + "/configuration/cfg-2"
	qsTargetAcct  = "210987654321"
	qsManagerRows = 2
)

// qsManagerSummary is the ListConfigurationManagers element of the manager
// arn, which carries no tags.
func qsManagerSummary(arn string) qstypes.ConfigurationManagerSummary {
	name := map[string]string{qsMgrARN: "host-mgmt", qsMgr2ARN: "patching"}[arn]
	return qstypes.ConfigurationManagerSummary{ManagerArn: ptrStr(arn), Name: ptrStr(name), Description: ptrStr(name + " summary")}
}

// qsManagerBody is the GetConfigurationManager body of the manager arn: the
// first manager is tagged, the second is not.
func qsManagerBody(arn string) *ssmquicksetup.GetConfigurationManagerOutput {
	out := &ssmquicksetup.GetConfigurationManagerOutput{
		ManagerArn: ptrStr(arn), Name: qsManagerSummary(arn).Name, Description: ptrStr("full body"),
		ConfigurationDefinitions: []qstypes.ConfigurationDefinition{{
			Id: ptrStr("def-1"), Type: ptrStr("AWSQuickSetupType-SSMHostMgmt"), TypeVersion: ptrStr("1.0"),
			Parameters:                           map[string]string{"TargetType": "*", "UpdateSSMAgent": "true"},
			LocalDeploymentAdministrationRoleArn: ptrStr("arn:aws:iam::123456789012:role/AWS-QuickSetup-LocalAdministrationRole"),
		}},
		CreatedAt: sdkaws.Time(time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)),
	}
	if arn == qsMgrARN {
		out.Tags = map[string]string{"env": "prod", "team": "ops"}
	}
	return out
}

// qsManagerListPage answers ListConfigurationManagers: the first manager on
// the first page, the second on the page token "m2" names. It pins MaxItems
// to the model's cap.
func qsManagerListPage(in any) (any, error) {
	req := in.(*ssmquicksetup.ListConfigurationManagersInput)
	if sdkaws.ToInt32(req.MaxItems) != 100 {
		return nil, fmt.Errorf("MaxItems = %v, want 100", req.MaxItems)
	}
	switch sv(req.StartingToken) {
	case "":
		return &ssmquicksetup.ListConfigurationManagersOutput{
			ConfigurationManagersList: []qstypes.ConfigurationManagerSummary{qsManagerSummary(qsMgrARN)}, NextToken: ptrStr("m2"),
		}, nil
	case "m2":
		return &ssmquicksetup.ListConfigurationManagersOutput{ConfigurationManagersList: []qstypes.ConfigurationManagerSummary{qsManagerSummary(qsMgr2ARN)}}, nil
	}
	return nil, fmt.Errorf("unexpected StartingToken %q", sv(req.StartingToken))
}

// qsGetManager answers GetConfigurationManager for the two listed managers,
// failing a manager with its entry in errs.
func qsGetManager(errs map[string]error) func(any) (any, error) {
	return func(in any) (any, error) {
		arn := sv(in.(*ssmquicksetup.GetConfigurationManagerInput).ManagerArn)
		if err := errs[arn]; err != nil {
			return nil, err
		}
		if arn != qsMgrARN && arn != qsMgr2ARN {
			return nil, fmt.Errorf("unexpected ManagerArn %q", arn)
		}
		return qsManagerBody(arn), nil
	}
}

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

// qsHandlers answers every ssm-quicksetup op: two managers on two pages, and
// two configuration pages. The
// second page's configuration names a manager ListConfigurationManagers did
// not return.
func qsHandlers(rec *qsRecorder) map[string]func(any) (any, error) {
	return map[string]func(any) (any, error){
		"ListConfigurationManagers": qsManagerListPage,
		"GetConfigurationManager":   qsGetManager(nil),
		"ListConfigurations": func(in any) (any, error) {
			req := in.(*ssmquicksetup.ListConfigurationsInput)
			rec.record(req.MaxItems)
			if req.StartingToken == nil {
				return &ssmquicksetup.ListConfigurationsOutput{
					ConfigurationsList: []qstypes.ConfigurationSummary{qsConfig(qsMgrARN, "cfg-1")},
					NextToken:          ptrStr("p2"),
				}, nil
			}
			return &ssmquicksetup.ListConfigurationsOutput{ConfigurationsList: []qstypes.ConfigurationSummary{qsConfig(qsOtherMgrARN, "cfg-2")}}, nil
		},
	}
}

func scanQSTopLevel(t *testing.T, st *store.Store, handlers map[string]func(any) (any, error)) (int, error) {
	t.Helper()
	return scanQSCtx(context.Background(), t, st, handlers)
}

// scanQSCtx runs the top-level scan with ctx and returns its total, failing
// the test unless inserted equals it: every test starts from an empty store,
// so every row stored is new.
func scanQSCtx(ctx context.Context, t *testing.T, st *store.Store, handlers map[string]func(any) (any, error)) (int, error) {
	t.Helper()
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(handlers, qsTestDenied)
	total, inserted, err := scanSSMQuickSetup(ctx, acct, testRegion, st, testScanID)
	if inserted != total {
		t.Errorf("inserted = %d, want total %d", inserted, total)
	}
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

// A configuration lacking its ManagerArn, Id, Account or Region is not
// stored, and one warning names it by the identifying fields it has; the
// other configurations are stored.
func TestScanSSMQuickSetup_ConfigurationWithoutIdentifierWarned(t *testing.T) {
	cases := []struct {
		name     string
		bad      []qstypes.ConfigurationSummary
		warnKeys []string
	}{
		{"no account", []qstypes.ConfigurationSummary{qsDeployed("cfg-noacct", "", testRegion)}, []string{qsMgrARN + "/configuration/cfg-noacct"}},
		{"no region", []qstypes.ConfigurationSummary{qsDeployed("cfg-noregion", testAccountID, "")}, []string{qsMgrARN + "/configuration/cfg-noregion"}},
		{"no id", []qstypes.ConfigurationSummary{qsConfig(qsMgrARN, "")}, []string{qsMgrARN}},
		{"no manager", []qstypes.ConfigurationSummary{qsConfig("", "cfg-nomgr")}, []string{"cfg-nomgr"}},
		{"no manager and no id", []qstypes.ConfigurationSummary{qsConfig("", "")}, []string{"(a row with no ManagerArn and no Id)"}},
		// One warning per cause, so a row of either cause is not counted
		// under the other.
		{
			"no id and no account",
			[]qstypes.ConfigurationSummary{qsConfig(qsMgrARN, ""), qsDeployed("cfg-noacct", "", testRegion)},
			[]string{qsMgrARN, qsMgrARN + "/configuration/cfg-noacct"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := recordWarnings(st)
			h := qsHandlers(&qsRecorder{})
			h["ListConfigurations"] = func(any) (any, error) {
				return &ssmquicksetup.ListConfigurationsOutput{ConfigurationsList: append([]qstypes.ConfigurationSummary{qsConfig(qsMgrARN, "cfg-1")}, c.bad...)}, nil
			}
			total, err := scanQSTopLevel(t, st, h)
			if err != nil {
				t.Fatalf("err = %v", err)
			}
			if total != qsManagerRows+1 {
				t.Errorf("total = %d, want %d", total, qsManagerRows+1)
			}
			if rows := rowsByNativeID(t, st, TypeSSMQuickSetupConfiguration); len(rows) != 1 || rows[qsCfg1Native].NativeID == "" {
				t.Errorf("configuration rows = %v, want only %s", slices.Sorted(maps.Keys(rows)), qsCfg1Native)
			}
			got := qsOpWarnings(*warns, ssmQuickSetupConfigurationsOp)
			if len(got) != len(c.warnKeys) {
				t.Fatalf("warnings = %+v, want %d naming %v", got, len(c.warnKeys), c.warnKeys)
			}
			for i, key := range c.warnKeys {
				assertQSWarning(t, got[i:i+1], key)
			}
		})
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

// A page that repeats the token it was asked with ends the listing with
// errRepeatedNextToken, after the configurations read before it are stored.
func TestScanSSMQuickSetup_ConfigurationsRepeatedTokenStops(t *testing.T) {
	st := newTestStore(t)
	h := qsHandlers(&qsRecorder{})
	calls := 0
	h["ListConfigurations"] = func(in any) (any, error) {
		calls++
		token := in.(*ssmquicksetup.ListConfigurationsInput).StartingToken
		if calls > 2 || (calls == 1) != (token == nil) {
			return nil, fmt.Errorf("call %d with StartingToken %v", calls, sv(token))
		}
		id := []string{"", "cfg-a", "cfg-b"}[calls]
		return &ssmquicksetup.ListConfigurationsOutput{
			ConfigurationsList: []qstypes.ConfigurationSummary{qsConfig(qsMgrARN, id)},
			NextToken:          ptrStr("same"),
		}, nil
	}
	total, err := scanQSTopLevel(t, st, h)
	if !errors.Is(err, errRepeatedNextToken) {
		t.Fatalf("err = %v, want errRepeatedNextToken", err)
	}
	if calls != 2 {
		t.Errorf("calls = %d, want 2", calls)
	}
	if total != qsManagerRows+2 {
		t.Errorf("total = %d, want %d", total, qsManagerRows+2)
	}
	rows := rowsByNativeID(t, st, TypeSSMQuickSetupConfiguration)
	for _, n := range []string{qsMgrARN + "/configuration/cfg-a", qsMgrARN + "/configuration/cfg-b"} {
		if _, ok := rows[n]; !ok {
			t.Errorf("no row %s in %v", n, rows)
		}
	}
	if len(rows) != 2 {
		t.Errorf("configuration rows = %d, want 2", len(rows))
	}
}

// A phase's transient error is demoted to a warning under its op instead of
// buried in the other phase's hard error.
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

// qsManagerWant is how a manager row is expected to be stored: from its
// GetConfigurationManager body, or from its list summary when the read failed.
type qsManagerWant int

const (
	qsFromBody qsManagerWant = iota
	qsFromSummary
)

// assertQSManagers checks that the stored manager rows are exactly want, each
// with its Name, Region, full attributes and tags: a body row carries the
// body's tags, a summary row none.
func assertQSManagers(t *testing.T, st *store.Store, want map[string]qsManagerWant) {
	t.Helper()
	rows := rowsByNativeID(t, st, TypeSSMQuickSetupConfigurationManager)
	if len(rows) != len(want) {
		t.Errorf("manager rows = %v, want %v", slices.Sorted(maps.Keys(rows)), want)
	}
	for arn, from := range want {
		r, ok := rows[arn]
		if !ok {
			t.Errorf("no manager row %s", arn)
			continue
		}
		wantName := sv(qsManagerSummary(arn).Name)
		if sv(r.Name) != wantName || sv(r.Region) != testRegion || r.Status != nil {
			t.Errorf("%s: name %q region %q status %v; want %q, %q, nil", arn, sv(r.Name), sv(r.Region), r.Status, wantName, testRegion)
		}
		body := qsManagerBody(arn)
		wantAttrs, wantTags := mustJSON(body), body.Tags
		if from == qsFromSummary {
			wantAttrs, wantTags = mustJSON(qsManagerSummary(arn)), nil
		}
		if r.AttributesJSON != wantAttrs {
			t.Errorf("%s attrs = %s, want %s", arn, r.AttributesJSON, wantAttrs)
		}
		if wantTags == nil {
			if r.TagsJSON != nil {
				t.Errorf("%s tags = %s, want none", arn, *r.TagsJSON)
			}
			continue
		}
		assertTagsMap(t, r.TagsJSON, wantTags)
	}
}

// qsOpWarnings is the warnings recorded for op.
func qsOpWarnings(warns []store.ScanWarning, op string) []store.ScanWarning {
	var got []store.ScanWarning
	for _, w := range warns {
		if w.Service == op {
			got = append(got, w)
		}
	}
	return got
}

// assertQSWarning checks that warns is one warning naming key, scoped to the
// scanned account and region.
func assertQSWarning(t *testing.T, warns []store.ScanWarning, key string) {
	t.Helper()
	if len(warns) != 1 {
		t.Fatalf("warnings = %+v, want one naming %s", warns, key)
	}
	if w := warns[0]; !strings.Contains(w.Message, key) || w.Scope != testAccountID+"/"+testRegion {
		t.Errorf("warning = %+v, want one naming %s scoped %s/%s", w, key, testAccountID, testRegion)
	}
}

// Every listed manager is stored from its GetConfigurationManager body, with
// the body's tags; an untagged manager stores none.
func TestScanSSMQuickSetup_ManagersStoredFromBodyWithTags(t *testing.T) {
	st := newTestStore(t)
	warns := recordWarnings(st)
	total, err := scanQSTopLevel(t, st, qsHandlers(&qsRecorder{}))
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if total != qsManagerRows+2 {
		t.Errorf("total = %d, want %d", total, qsManagerRows+2)
	}
	assertQSManagers(t, st, map[string]qsManagerWant{qsMgrARN: qsFromBody, qsMgr2ARN: qsFromBody})
	if len(*warns) != 0 {
		t.Errorf("warnings = %+v, want none", *warns)
	}
}

// A failed GetConfigurationManager costs no other manager its body. A manager
// the read no longer finds is not stored, silently; any other failure keeps
// its list summary: a denial warns once naming the first manager, a lone
// transient failure is returned as transient, a transient failure beside a
// hard one is demoted to a warning, and any other error is returned.
func TestScanSSMQuickSetup_ManagerReadFailures(t *testing.T) {
	throttle := apiErr("ThrottlingException", "Rate exceeded")
	conflict := apiErr("ConflictException", "busy")
	cases := []struct {
		name        string
		errs        map[string]error
		want        map[string]qsManagerWant
		wantOutcome serviceOutcome
		wantErr     bool
		warnKey     string
		wantCode    string
	}{
		{
			name: "not found", errs: map[string]error{qsMgrARN: apiErr("ResourceNotFoundException", "gone")},
			want: map[string]qsManagerWant{qsMgr2ARN: qsFromBody},
		},
		{
			name: "denied", errs: map[string]error{qsMgrARN: qsTestDenied},
			want:    map[string]qsManagerWant{qsMgrARN: qsFromSummary, qsMgr2ARN: qsFromBody},
			warnKey: qsMgrARN,
		},
		{
			name: "transient", errs: map[string]error{qsMgrARN: throttle},
			want:    map[string]qsManagerWant{qsMgrARN: qsFromSummary, qsMgr2ARN: qsFromBody},
			wantErr: true, wantOutcome: outcomeTransient, wantCode: "ThrottlingException",
		},
		{
			name: "other error", errs: map[string]error{qsMgrARN: conflict},
			want:    map[string]qsManagerWant{qsMgrARN: qsFromSummary, qsMgr2ARN: qsFromBody},
			wantErr: true, wantOutcome: outcomeError, wantCode: "ConflictException",
		},
		{
			name: "transient beside other error", errs: map[string]error{qsMgrARN: throttle, qsMgr2ARN: conflict},
			want:    map[string]qsManagerWant{qsMgrARN: qsFromSummary, qsMgr2ARN: qsFromSummary},
			wantErr: true, wantOutcome: outcomeError, wantCode: "ConflictException",
			warnKey: qsMgrARN,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := recordWarnings(st)
			h := qsHandlers(&qsRecorder{})
			h["GetConfigurationManager"] = qsGetManager(c.errs)
			total, err := scanQSTopLevel(t, st, h)
			if !c.wantErr && err != nil {
				t.Fatalf("err = %v, want nil", err)
			}
			if c.wantErr && (err == nil || classifyServiceError(err) != c.wantOutcome || !isAPIErrorCode(err, c.wantCode)) {
				t.Fatalf("err = %v, want outcome %v with code %s", err, c.wantOutcome, c.wantCode)
			}
			if c.warnKey != "" && isAPIErrorCode(err, "ThrottlingException") {
				t.Errorf("err = %v, want the throttle demoted to a warning", err)
			}
			if want := len(c.want) + 2; total != want {
				t.Errorf("total = %d, want %d", total, want)
			}
			assertQSManagers(t, st, c.want)
			// A manager stored from its list summary still contains its
			// configurations.
			if _, ok := c.want[qsMgrARN]; ok {
				assertContainsNative(t, st, qsMgrARN, qsCfg1Native)
			}
			if c.warnKey == "" {
				if len(*warns) != 0 {
					t.Errorf("warnings = %+v, want none", *warns)
				}
				return
			}
			assertQSWarning(t, qsOpWarnings(*warns, ssmQuickSetupGetManagerOp), c.warnKey)
		})
	}
}

// A failed ListConfigurationManagers page keeps the managers of the pages
// before it: a denial warns once, a repeated token or any other error is
// returned. A listed manager without an ARN is not stored and is warned about
// by name.
func TestScanSSMQuickSetup_ManagerListFailures(t *testing.T) {
	cases := []struct {
		name    string
		page1   []qstypes.ConfigurationManagerSummary
		page2   func(any) (any, error)
		want    map[string]qsManagerWant
		wantErr error
		warnKey string
	}{
		{
			name:    "later page fails",
			page2:   func(any) (any, error) { return nil, errQSTest },
			want:    map[string]qsManagerWant{qsMgrARN: qsFromBody},
			wantErr: errQSTest,
		},
		{
			name:    "later page denied",
			page2:   func(any) (any, error) { return nil, qsTestDenied },
			want:    map[string]qsManagerWant{qsMgrARN: qsFromBody},
			warnKey: "ListConfigurationManagers",
		},
		{
			name: "repeated token",
			page2: func(any) (any, error) {
				return &ssmquicksetup.ListConfigurationManagersOutput{
					ConfigurationManagersList: []qstypes.ConfigurationManagerSummary{qsManagerSummary(qsMgr2ARN)}, NextToken: ptrStr("m2"),
				}, nil
			},
			want:    map[string]qsManagerWant{qsMgrARN: qsFromBody, qsMgr2ARN: qsFromBody},
			wantErr: errRepeatedNextToken,
		},
		{
			name:    "manager without ARN",
			page1:   []qstypes.ConfigurationManagerSummary{qsManagerSummary(qsMgrARN), {Name: ptrStr("arnless-manager")}},
			want:    map[string]qsManagerWant{qsMgrARN: qsFromBody, qsMgr2ARN: qsFromBody},
			warnKey: "arnless-manager",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := recordWarnings(st)
			h := qsHandlers(&qsRecorder{})
			h["ListConfigurationManagers"] = func(in any) (any, error) {
				first := in.(*ssmquicksetup.ListConfigurationManagersInput).StartingToken == nil
				out, err := qsManagerListPage(in)
				switch {
				case !first && c.page2 != nil:
					return c.page2(in)
				case first && c.page1 != nil:
					out.(*ssmquicksetup.ListConfigurationManagersOutput).ConfigurationManagersList = c.page1
				}
				return out, err
			}
			total, err := scanQSTopLevel(t, st, h)
			if (c.wantErr == nil) != (err == nil) || c.wantErr != nil && !errors.Is(err, c.wantErr) {
				t.Fatalf("err = %v, want %v", err, c.wantErr)
			}
			if want := len(c.want) + 2; total != want {
				t.Errorf("total = %d, want %d", total, want)
			}
			assertQSManagers(t, st, c.want)
			if c.warnKey == "" {
				if len(*warns) != 0 {
					t.Errorf("warnings = %+v, want none", *warns)
				}
				return
			}
			assertQSWarning(t, qsOpWarnings(*warns, ssmQuickSetupManagersOp), c.warnKey)
		})
	}
}

// Once ctx ends no manager is read: the listed managers keep their list
// summaries and the stop is returned.
func TestScanSSMQuickSetup_ManagerContextStop(t *testing.T) {
	st := newTestStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	h := qsHandlers(&qsRecorder{})
	var gets atomic.Int32
	h["ListConfigurationManagers"] = func(in any) (any, error) {
		out, err := qsManagerListPage(in)
		if in.(*ssmquicksetup.ListConfigurationManagersInput).StartingToken != nil {
			cancel()
		}
		return out, err
	}
	h["GetConfigurationManager"] = func(in any) (any, error) {
		gets.Add(1)
		return qsGetManager(nil)(in)
	}
	h["ListConfigurations"] = func(any) (any, error) { return &ssmquicksetup.ListConfigurationsOutput{}, nil }
	total, err := scanQSCtx(ctx, t, st, h)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want the cancellation", err)
	}
	if m := joinedMembers(err); len(m) != 1 {
		t.Errorf("err members = %q, want the cancellation alone", m)
	}
	if n := gets.Load(); n != 0 {
		t.Errorf("GetConfigurationManager calls = %d, want 0", n)
	}
	if total != qsManagerRows {
		t.Errorf("total = %d, want %d", total, qsManagerRows)
	}
	assertQSManagers(t, st, map[string]qsManagerWant{qsMgrARN: qsFromSummary, qsMgr2ARN: qsFromSummary})
}

// A failed manager write returns the store error alone, warns about nothing
// (not the denial it read, nor the throttle it would demote beside the write
// failure) and stops the configuration phase.
func TestScanSSMQuickSetup_ManagerStoreWriteStops(t *testing.T) {
	st := newTestStore(t)
	warns := recordWarnings(st)
	refuseStoreWrites(t, st, TypeSSMQuickSetupConfigurationManager)
	h := qsHandlers(&qsRecorder{})
	h["GetConfigurationManager"] = qsGetManager(map[string]error{qsMgrARN: qsTestDenied, qsMgr2ARN: apiErr("ThrottlingException", "Rate exceeded")})
	listed := false
	h["ListConfigurations"] = func(any) (any, error) {
		listed = true
		return &ssmquicksetup.ListConfigurationsOutput{}, nil
	}
	total, err := scanQSTopLevel(t, st, h)
	if !errors.Is(err, store.ErrStoreWrite) || isAPIErrorCode(err, "ThrottlingException") {
		t.Fatalf("err = %v, want the store write failure alone", err)
	}
	if total != 0 {
		t.Errorf("total = %d, want 0", total)
	}
	if listed {
		t.Error("ListConfigurations was called after the manager write failed")
	}
	if len(*warns) != 0 {
		t.Errorf("warnings = %+v, want none", *warns)
	}
}

// A manager read still throttled after retries, beside a hard error of the
// configuration phase, is demoted to one warning under GetConfigurationManager
// naming the manager, while the hard error is returned.
func TestScanSSMQuickSetup_ManagerReadThrottleBesideConfigurationError(t *testing.T) {
	st := newTestStore(t)
	warns := recordWarnings(st)
	h := qsHandlers(&qsRecorder{})
	h["GetConfigurationManager"] = qsGetManager(map[string]error{qsMgrARN: apiErr("ThrottlingException", "Rate exceeded")})
	h["ListConfigurations"] = func(any) (any, error) { return nil, errQSTest }
	total, err := scanQSTopLevel(t, st, h)
	if !errors.Is(err, errQSTest) || classifyServiceError(err) != outcomeError || isAPIErrorCode(err, "ThrottlingException") {
		t.Fatalf("err = %v, want the configuration error alone", err)
	}
	if total != qsManagerRows {
		t.Errorf("total = %d, want %d", total, qsManagerRows)
	}
	assertQSManagers(t, st, map[string]qsManagerWant{qsMgrARN: qsFromSummary, qsMgr2ARN: qsFromBody})
	assertQSWarning(t, qsOpWarnings(*warns, ssmQuickSetupGetManagerOp), qsMgrARN)
	if n := len(qsOpWarnings(*warns, ssmQuickSetupManagersOp)); n != 0 {
		t.Errorf("ListConfigurationManagers warnings = %d, want 0", n)
	}
}

// A refused configuration write returns the store error and sends neither
// the foreign-deployment nor the not-stored warning: both would describe a
// listing whose local rows were lost.
func TestScanSSMQuickSetup_ConfigurationStoreWriteRefusedNoForeignWarning(t *testing.T) {
	st := newTestStore(t)
	refuseStoreWrites(t, st, TypeSSMQuickSetupConfiguration)
	h := qsHandlers(&qsRecorder{})
	h["ListConfigurations"] = func(any) (any, error) {
		return &ssmquicksetup.ListConfigurationsOutput{ConfigurationsList: []qstypes.ConfigurationSummary{
			qsConfig(qsMgrARN, "cfg-1"), qsDeployed("cfg-r", testAccountID, "eu-west-1"), qsConfig(qsMgrARN, ""),
		}}, nil
	}
	warnings := countOpWarnings(st, ssmQuickSetupConfigurationsOp)
	total, err := scanQSTopLevel(t, st, h)
	if !errors.Is(err, store.ErrStoreWrite) {
		t.Fatalf("err = %v, want the store write failure", err)
	}
	if total != qsManagerRows {
		t.Errorf("total = %d, want %d (manager rows only)", total, qsManagerRows)
	}
	if *warnings != 0 {
		t.Errorf("%s warnings = %d, want 0", ssmQuickSetupConfigurationsOp, *warnings)
	}
	if n := len(rowsByNativeID(t, st, TypeSSMQuickSetupConfiguration)); n != 0 {
		t.Errorf("configuration rows = %d, want 0", n)
	}
}

// A failed manager write is returned joined with the hard read errors of the
// managers it would have stored, so those are not lost.
func TestScanSSMQuickSetup_ManagerStoreWriteKeepsReadErrors(t *testing.T) {
	st := newTestStore(t)
	refuseStoreWrites(t, st, TypeSSMQuickSetupConfigurationManager)
	h := qsHandlers(&qsRecorder{})
	h["GetConfigurationManager"] = qsGetManager(map[string]error{qsMgrARN: apiErr("ConflictException", "busy")})
	total, err := scanQSTopLevel(t, st, h)
	if !errors.Is(err, store.ErrStoreWrite) || !isAPIErrorCode(err, "ConflictException") {
		t.Fatalf("err = %v, want the store write failure and the ConflictException", err)
	}
	if total != 0 {
		t.Errorf("total = %d, want 0", total)
	}
}

// A transient ListConfigurationManagers page beside a hard manager read error
// is demoted to one warning under ListConfigurationManagers, and the read
// error alone is returned: the listing's and the reads' errors are kept
// apart, or the throttle would be buried in the read error.
func TestScanSSMQuickSetup_ManagerListThrottleBesideReadError(t *testing.T) {
	st := newTestStore(t)
	warns := recordWarnings(st)
	h := qsHandlers(&qsRecorder{})
	h["ListConfigurationManagers"] = func(in any) (any, error) {
		if in.(*ssmquicksetup.ListConfigurationManagersInput).StartingToken != nil {
			return nil, apiErr("ThrottlingException", "Rate exceeded")
		}
		return qsManagerListPage(in)
	}
	h["GetConfigurationManager"] = qsGetManager(map[string]error{qsMgrARN: apiErr("ConflictException", "busy")})
	total, err := scanQSTopLevel(t, st, h)
	if !isAPIErrorCode(err, "ConflictException") || isAPIErrorCode(err, "ThrottlingException") || classifyServiceError(err) != outcomeError {
		t.Fatalf("err = %v, want the ConflictException alone", err)
	}
	if total != 1+2 {
		t.Errorf("total = %d, want 3", total)
	}
	assertQSManagers(t, st, map[string]qsManagerWant{qsMgrARN: qsFromSummary})
	assertQSWarning(t, qsOpWarnings(*warns, ssmQuickSetupManagersOp), "ThrottlingException")
	if n := len(qsOpWarnings(*warns, ssmQuickSetupGetManagerOp)); n != 0 {
		t.Errorf("GetConfigurationManager warnings = %d, want 0", n)
	}
}
