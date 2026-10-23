package aws

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/inspector2"
	i2types "github.com/aws/aws-sdk-go-v2/service/inspector2/types"
	"github.com/icearp/disco-cli/store"
)

// fakeInspector2 serves ListConnectors and ListDelegatedAdminAccounts pages
// keyed by NextToken ("" is the first page); pageErrs / adminErrs[token]
// fails that page. adminMaxResults records each admin call's page size.
type fakeInspector2 struct {
	inspector2API
	connectors      map[string]*inspector2.ListConnectorsOutput
	pageErrs        map[string]error
	admins          map[string]*inspector2.ListDelegatedAdminAccountsOutput
	adminErrs       map[string]error
	adminMaxResults *[]int32
}

func (f fakeInspector2) ListDelegatedAdminAccounts(_ context.Context, in *inspector2.ListDelegatedAdminAccountsInput, _ ...func(*inspector2.Options)) (*inspector2.ListDelegatedAdminAccountsOutput, error) {
	if f.adminMaxResults != nil {
		*f.adminMaxResults = append(*f.adminMaxResults, sdkaws.ToInt32(in.MaxResults))
	}
	if err := f.adminErrs[sv(in.NextToken)]; err != nil {
		return nil, err
	}
	if out := f.admins[sv(in.NextToken)]; out != nil {
		return out, nil
	}
	return &inspector2.ListDelegatedAdminAccountsOutput{}, nil
}

func (f fakeInspector2) ListConnectors(_ context.Context, in *inspector2.ListConnectorsInput, _ ...func(*inspector2.Options)) (*inspector2.ListConnectorsOutput, error) {
	if err := f.pageErrs[sv(in.NextToken)]; err != nil {
		return nil, err
	}
	if out := f.connectors[sv(in.NextToken)]; out != nil {
		return out, nil
	}
	return &inspector2.ListConnectorsOutput{}, nil
}

func i2ConnectorARN(id string) string {
	return "arn:aws:inspector2:" + testRegion + ":" + testAccountID + ":connector/" + id
}

func i2Connector(id string) i2types.Connector {
	created := time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
	return i2types.Connector{
		ConnectorArn:     sdkaws.String(i2ConnectorARN(id)),
		Name:             sdkaws.String("conn-" + id),
		Provider:         i2types.ConnectorCloudProviderAzure,
		EnablementStatus: i2types.EnablementStatusEnabled,
		CreatedAt:        &created,
		UpdatedAt:        &created,
		Tags:             map[string]string{"env": "prod"},
	}
}

func TestScanInspector2Connectors_PaginatesAndStores(t *testing.T) {
	st := newTestStore(t)
	fake := fakeInspector2{connectors: map[string]*inspector2.ListConnectorsOutput{
		"":   {Items: []i2types.Connector{i2Connector("c1"), {Name: sdkaws.String("no-arn")}}, NextToken: sdkaws.String("p2")},
		"p2": {Items: []i2types.Connector{i2Connector("c2")}},
	}}
	total, _, err := scanInspector2Connectors(context.Background(), fake, newTestAccount(testAccountID), testRegion, st, testScanID)
	if err != nil {
		t.Fatalf("scanInspector2Connectors: %v", err)
	}
	if total != 2 {
		t.Errorf("total = %d; want 2 (empty-ARN connector skipped)", total)
	}
	rows := qsRows(t, st, TypeInspector2Connector)
	if len(rows) != 2 {
		t.Fatalf("stored %d connectors; want 2", len(rows))
	}
	r, ok := rows[i2ConnectorARN("c2")]
	if !ok {
		t.Fatalf("page-2 connector %s not stored; rows=%v", i2ConnectorARN("c2"), rows)
	}
	if r.Type != TypeInspector2Connector {
		t.Errorf("Type = %q; want %q", r.Type, TypeInspector2Connector)
	}
	if sv(r.Region) != testRegion {
		t.Errorf("Region = %q; want %q", sv(r.Region), testRegion)
	}
	if sv(r.Name) != "conn-c2" {
		t.Errorf("Name = %q; want conn-c2", sv(r.Name))
	}
	if sv(r.Status) != "ENABLED" {
		t.Errorf("Status = %q; want ENABLED", sv(r.Status))
	}
	if sv(r.CreatedAt) != "2026-09-01T12:00:00Z" {
		t.Errorf("CreatedAt = %q; want 2026-09-01T12:00:00Z", sv(r.CreatedAt))
	}
	if sv(r.TagsJSON) != `{"env":"prod"}` {
		t.Errorf("TagsJSON = %q; want {\"env\":\"prod\"}", sv(r.TagsJSON))
	}
	var attrs struct{ Provider string }
	if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
		t.Fatalf("attrs: %v", err)
	}
	if attrs.Provider != "AZURE" {
		t.Errorf("attrs Provider = %q; want AZURE", attrs.Provider)
	}
}

func TestScanInspector2Connectors_ErrorShapes(t *testing.T) {
	cases := []struct {
		name      string
		token     string
		err       error
		wantOther bool
		wantRows  int
		wantWarns int
	}{
		{"access denied on page 2 warns and keeps page 1", "p2", apiErr("AccessDeniedException", "User: x is not authorized to perform: inspector2:ListConnectors"), false, 1, 1},
		{"account not enabled on page 2 warns and keeps page 1", "p2", apiErr("AccessDeniedException", "Invoking account is not enabled"), false, 1, 1},
		{"other error propagates", "p2", apiErr("InternalServerException", "boom"), true, 0, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			var warned []string
			st.OnWarn = func(w store.ScanWarning) { warned = append(warned, w.Service) }
			fake := fakeInspector2{
				connectors: map[string]*inspector2.ListConnectorsOutput{"": {Items: []i2types.Connector{i2Connector("c1")}, NextToken: sdkaws.String("p2")}},
				pageErrs:   map[string]error{tc.token: tc.err},
			}
			_, _, err := scanInspector2Connectors(context.Background(), fake, newTestAccount(testAccountID), testRegion, st, testScanID)
			if tc.wantOther {
				if !isAPIErrorCode(err, "InternalServerException") {
					t.Fatalf("err = %v; want the InternalServerException propagated", err)
				}
			} else if err != nil {
				t.Fatalf("err = %v; want nil", err)
			}
			if got := len(qsRows(t, st, TypeInspector2Connector)); got != tc.wantRows {
				t.Errorf("stored %d rows; want %d", got, tc.wantRows)
			}
			if len(warned) != tc.wantWarns {
				t.Errorf("recorded %d warnings; want %d", len(warned), tc.wantWarns)
			}
			for _, svc := range warned {
				if svc != "inspector2:ListConnectors" {
					t.Errorf("warning names %q; want inspector2:ListConnectors", svc)
				}
			}
		})
	}
}

func TestScanInspector2Connectors_Empty(t *testing.T) {
	st := newTestStore(t)
	total, _, err := scanInspector2Connectors(context.Background(), fakeInspector2{}, newTestAccount(testAccountID), testRegion, st, testScanID)
	if err != nil || total != 0 {
		t.Errorf("total=%d err=%v; want 0, nil", total, err)
	}
}

// inspector2ServiceStub serves every op scanInspector2 calls, one row per
// phase; override replaces an op's queue.
func inspector2ServiceStub(t *testing.T, override map[string][]stubCall) sdkaws.Config {
	t.Helper()
	one := func(out any) []stubCall { return []stubCall{{Output: out}} }
	arn := func(kind string) *string {
		return sdkaws.String("arn:aws:inspector2:" + testRegion + ":" + testAccountID + ":owner/" + testAccountID + "/" + kind + "/x")
	}
	calls := map[string][]stubCall{
		"ListFilters": one(&inspector2.ListFiltersOutput{Filters: []i2types.Filter{{Arn: arn("filter")}}}),
		"ListMembers": one(&inspector2.ListMembersOutput{Members: []i2types.Member{{AccountId: sdkaws.String("222222222222")}}}),
		"ListCisScanConfigurations": one(&inspector2.ListCisScanConfigurationsOutput{
			ScanConfigurations: []i2types.CisScanConfiguration{{ScanConfigurationArn: arn("cis-configuration")}},
		}),
		"ListCodeSecurityIntegrations": one(&inspector2.ListCodeSecurityIntegrationsOutput{
			Integrations: []i2types.CodeSecurityIntegrationSummary{{IntegrationArn: arn("codesecurity-integration")}},
		}),
		"ListCodeSecurityScanConfigurations": one(&inspector2.ListCodeSecurityScanConfigurationsOutput{
			Configurations: []i2types.CodeSecurityScanConfigurationSummary{{ScanConfigurationArn: arn("codesecurity-configuration")}},
		}),
		"ListConnectors": one(&inspector2.ListConnectorsOutput{Items: []i2types.Connector{i2Connector("c1")}}),
		"ListDelegatedAdminAccounts": one(&inspector2.ListDelegatedAdminAccountsOutput{
			DelegatedAdminAccounts: []i2types.DelegatedAdminAccount{i2DelegatedAdmin(testI2Admin)},
		}),
	}
	for op, c := range override {
		calls[op] = c
	}
	return cfgWithStub(stubResponses(t, calls), testRegion)
}

func TestScanInspector2_StoresConnectorsAndDelegatedAdmins(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	acct.cfg = inspector2ServiceStub(t, nil)
	total, _, err := scanInspector2(context.Background(), acct, testRegion, st, testScanID)
	if err != nil {
		t.Fatalf("scanInspector2: %v", err)
	}
	if total != 7 {
		t.Errorf("total = %d; want 7 (one row per phase)", total)
	}
	if _, ok := qsRows(t, st, TypeInspector2Connector)[i2ConnectorARN("c1")]; !ok {
		t.Errorf("connector %s not stored", i2ConnectorARN("c1"))
	}
	if _, ok := qsRows(t, st, TypeInspector2DelegatedAdminAccount)[i2AdminNativeID(testI2Admin)]; !ok {
		t.Errorf("delegated admin %s not stored", i2AdminNativeID(testI2Admin))
	}
}

// TestScanInspector2_ConnectorErrorAfterExistingPhases pins that the connector
// phase runs after every existing phase, that its hard error reaches the
// service result, and that the delegated-admin phase still runs.
func TestScanInspector2_ConnectorErrorAfterExistingPhases(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	acct.cfg = inspector2ServiceStub(t, map[string][]stubCall{
		"ListConnectors": {{Err: apiErr("ValidationException", "boom")}},
	})
	total, _, err := scanInspector2(context.Background(), acct, testRegion, st, testScanID)
	if !isAPIErrorCode(err, "ValidationException") {
		t.Fatalf("err = %v; want the ListConnectors ValidationException", err)
	}
	if total != 6 {
		t.Errorf("total = %d; want 6 (every phase but connectors)", total)
	}
	for _, typ := range []string{
		TypeInspector2Filter, TypeInspector2Member, TypeInspector2CisScanConfiguration,
		TypeInspector2CodeSecurityIntegration, TypeInspector2CodeSecurityScanConfiguration,
		TypeInspector2DelegatedAdminAccount,
	} {
		if got := len(qsRows(t, st, typ)); got != 1 {
			t.Errorf("stored %d %s rows; want 1", got, typ)
		}
	}
}

// TestScanInspector2_ConnectorNotEnabledWarns pins that the "Invoking account
// is not enabled" denial on ListConnectors, reachable only after the earlier
// phases succeeded, is one visible warning and not a service-disabled sentinel
// that would report the region as disabled with 0 resources.
func TestScanInspector2_ConnectorNotEnabledWarns(t *testing.T) {
	st := newTestStore(t)
	var warned []string
	st.OnWarn = func(w store.ScanWarning) { warned = append(warned, w.Service) }
	acct := newTestAccount(testAccountID)
	acct.cfg = inspector2ServiceStub(t, map[string][]stubCall{
		"ListConnectors": {{Err: apiErr("AccessDeniedException", "Invoking account is not enabled")}},
	})
	total, _, err := scanInspector2(context.Background(), acct, testRegion, st, testScanID)
	if err != nil {
		t.Fatalf("scanInspector2: %v; want nil", err)
	}
	if total != 6 {
		t.Errorf("total = %d; want 6 (one row per phase other than connectors)", total)
	}
	if len(warned) != 1 || warned[0] != "inspector2:ListConnectors" {
		t.Errorf("warnings = %v; want exactly one naming inspector2:ListConnectors", warned)
	}
}

const testI2Admin = "555555555555"

func i2AdminNativeID(adminID string) string {
	return "arn:aws:inspector2:" + testRegion + ":" + testAccountID + ":delegated-admin/" + adminID
}

func i2DelegatedAdmin(adminID string) i2types.DelegatedAdminAccount {
	return i2types.DelegatedAdminAccount{AccountId: sdkaws.String(adminID), Status: i2types.DelegatedAdminStatusEnabled}
}

func TestScanInspector2DelegatedAdmins_PaginatesAndStores(t *testing.T) {
	st := newTestStore(t)
	var pageSizes []int32
	fake := fakeInspector2{
		admins: map[string]*inspector2.ListDelegatedAdminAccountsOutput{
			"": {DelegatedAdminAccounts: []i2types.DelegatedAdminAccount{
				i2DelegatedAdmin("111111111111"),
				{Status: i2types.DelegatedAdminStatusEnabled},
			}, NextToken: sdkaws.String("p2")},
			"p2": {DelegatedAdminAccounts: []i2types.DelegatedAdminAccount{
				{AccountId: sdkaws.String(testI2Admin), Status: i2types.DelegatedAdminStatusDisableInProgress},
				{AccountId: sdkaws.String("666666666666")},
			}},
		},
		adminMaxResults: &pageSizes,
	}
	total, inserted, err := scanInspector2DelegatedAdmins(context.Background(), fake, newTestAccount(testAccountID), testRegion, st, testScanID)
	if err != nil {
		t.Fatalf("scanInspector2DelegatedAdmins: %v", err)
	}
	if total != 3 || inserted != 3 {
		t.Errorf("total, inserted = %d, %d; want 3, 3 (empty-AccountId admin skipped)", total, inserted)
	}
	if len(pageSizes) != 2 || pageSizes[0] != 5 || pageSizes[1] != 5 {
		t.Errorf("MaxResults per call = %v; want [5 5] (the model's page cap)", pageSizes)
	}
	rows := qsRows(t, st, TypeInspector2DelegatedAdminAccount)
	if len(rows) != 3 {
		t.Fatalf("stored %d delegated admins; want 3", len(rows))
	}
	r, ok := rows[i2AdminNativeID(testI2Admin)]
	if !ok {
		t.Fatalf("page-2 admin %s not stored; rows=%v", i2AdminNativeID(testI2Admin), rows)
	}
	if r.Type != TypeInspector2DelegatedAdminAccount {
		t.Errorf("Type = %q; want %q", r.Type, TypeInspector2DelegatedAdminAccount)
	}
	if sv(r.Region) != testRegion {
		t.Errorf("Region = %q; want %q", sv(r.Region), testRegion)
	}
	if sv(r.Name) != testI2Admin {
		t.Errorf("Name = %q; want %s", sv(r.Name), testI2Admin)
	}
	if sv(r.Status) != "DISABLE_IN_PROGRESS" {
		t.Errorf("Status = %q; want DISABLE_IN_PROGRESS", sv(r.Status))
	}
	var attrs struct {
		AccountID string `json:"AccountId"`
		Status    string
	}
	if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
		t.Fatalf("attrs: %v", err)
	}
	if attrs.AccountID != testI2Admin || attrs.Status != "DISABLE_IN_PROGRESS" {
		t.Errorf("attrs = %+v; want AccountId %s, Status DISABLE_IN_PROGRESS", attrs, testI2Admin)
	}
	if noStatus := rows[i2AdminNativeID("666666666666")]; noStatus.Status != nil {
		t.Errorf("Status = %q for an admin with no status; want unset", sv(noStatus.Status))
	}
}

func TestScanInspector2DelegatedAdmins_ErrorShapes(t *testing.T) {
	cases := []struct {
		name      string
		err       error
		wantOther bool
		wantRows  int
		wantWarns int
	}{
		{"access denied on page 2 warns and keeps page 1", apiErr("AccessDeniedException", "User: x is not authorized to perform: inspector2:ListDelegatedAdminAccounts"), false, 1, 1},
		{"account not enabled on page 2 is silent and keeps page 1", apiErr("AccessDeniedException", "Invoking account is not enabled"), false, 1, 0},
		{"other error propagates", apiErr("ValidationException", "boom"), true, 0, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			var warned []string
			st.OnWarn = func(w store.ScanWarning) { warned = append(warned, w.Service) }
			fake := fakeInspector2{
				admins: map[string]*inspector2.ListDelegatedAdminAccountsOutput{"": {
					DelegatedAdminAccounts: []i2types.DelegatedAdminAccount{i2DelegatedAdmin(testI2Admin)}, NextToken: sdkaws.String("p2"),
				}},
				adminErrs: map[string]error{"p2": tc.err},
			}
			total, _, err := scanInspector2DelegatedAdmins(context.Background(), fake, newTestAccount(testAccountID), testRegion, st, testScanID)
			if tc.wantOther {
				if !isAPIErrorCode(err, "ValidationException") {
					t.Fatalf("err = %v; want the ValidationException propagated", err)
				}
			} else if err != nil {
				t.Fatalf("err = %v; want nil", err)
			}
			if total != tc.wantRows {
				t.Errorf("total = %d; want %d", total, tc.wantRows)
			}
			if got := len(qsRows(t, st, TypeInspector2DelegatedAdminAccount)); got != tc.wantRows {
				t.Errorf("stored %d rows; want %d", got, tc.wantRows)
			}
			if len(warned) != tc.wantWarns {
				t.Errorf("recorded %d warnings; want %d", len(warned), tc.wantWarns)
			}
			for _, svc := range warned {
				if svc != "inspector2:ListDelegatedAdminAccounts" {
					t.Errorf("warning names %q; want inspector2:ListDelegatedAdminAccounts", svc)
				}
			}
		})
	}
}

func TestScanInspector2DelegatedAdmins_Empty(t *testing.T) {
	st := newTestStore(t)
	total, inserted, err := scanInspector2DelegatedAdmins(context.Background(), fakeInspector2{}, newTestAccount(testAccountID), testRegion, st, testScanID)
	if err != nil || total != 0 || inserted != 0 {
		t.Errorf("total=%d inserted=%d err=%v; want 0, 0, nil", total, inserted, err)
	}
}

// TestScanInspector2_DelegatedAdminErrorAfterExistingPhases pins that the
// delegated-admin phase's hard error reaches the service result with every
// other phase's rows and counts kept.
func TestScanInspector2_DelegatedAdminErrorAfterExistingPhases(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	acct.cfg = inspector2ServiceStub(t, map[string][]stubCall{
		"ListDelegatedAdminAccounts": {{Err: apiErr("ValidationException", "boom")}},
	})
	total, _, err := scanInspector2(context.Background(), acct, testRegion, st, testScanID)
	if !isAPIErrorCode(err, "ValidationException") {
		t.Fatalf("err = %v; want the ListDelegatedAdminAccounts ValidationException", err)
	}
	if total != 6 {
		t.Errorf("total = %d; want 6 (every phase but the delegated admin)", total)
	}
	for _, typ := range []string{
		TypeInspector2Filter, TypeInspector2Member, TypeInspector2CisScanConfiguration,
		TypeInspector2CodeSecurityIntegration, TypeInspector2CodeSecurityScanConfiguration,
		TypeInspector2Connector,
	} {
		if got := len(qsRows(t, st, typ)); got != 1 {
			t.Errorf("stored %d %s rows; want 1", got, typ)
		}
	}
}

// notEnabledInspector2Stub is a management account that has not enabled
// Inspector itself: the given filters, no members, and the CIS phase answers
// with the not-enabled denial that scanInspector2CisConfigs turns into the
// sentinel.
func notEnabledInspector2Stub(t *testing.T, filters []i2types.Filter, admins []stubCall) sdkaws.Config {
	t.Helper()
	return inspector2ServiceStub(t, map[string][]stubCall{
		"ListFilters":                {{Output: &inspector2.ListFiltersOutput{Filters: filters}}},
		"ListMembers":                {{Output: &inspector2.ListMembersOutput{}}},
		"ListCisScanConfigurations":  {{Err: apiErr("AccessDeniedException", "Invoking account is not enabled")}},
		"ListDelegatedAdminAccounts": admins,
	})
}

func TestScanInspector2_NotEnabledSentinel(t *testing.T) {
	cases := []struct {
		name         string
		filters      []i2types.Filter
		admins       []stubCall
		wantNilErr   bool
		wantOutcome  serviceOutcome
		wantTotal    int
		wantAdminRow bool
	}{
		{
			name: "delegated admin stored drops the sentinel and keeps counts",
			admins: []stubCall{{Output: &inspector2.ListDelegatedAdminAccountsOutput{
				DelegatedAdminAccounts: []i2types.DelegatedAdminAccount{i2DelegatedAdmin(testI2Admin)},
			}}},
			wantNilErr: true, wantTotal: 1, wantAdminRow: true,
		},
		{
			name:       "an older phase's row drops the sentinel and keeps counts",
			filters:    []i2types.Filter{{Arn: sdkaws.String("arn:aws:inspector2:" + testRegion + ":" + testAccountID + ":owner/" + testAccountID + "/filter/f1")}},
			admins:     []stubCall{{Output: &inspector2.ListDelegatedAdminAccountsOutput{}}},
			wantNilErr: true, wantTotal: 1,
		},
		{
			name:        "no delegated admin keeps the sentinel",
			admins:      []stubCall{{Output: &inspector2.ListDelegatedAdminAccountsOutput{}}},
			wantOutcome: outcomeDisabled,
		},
		{
			name:        "delegated-admin hard error is not hidden behind the sentinel",
			admins:      []stubCall{{Err: apiErr("ValidationException", "boom")}},
			wantOutcome: outcomeError,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			acct := newTestAccount(testAccountID)
			acct.cfg = notEnabledInspector2Stub(t, tc.filters, tc.admins)
			total, inserted, err := scanInspector2(context.Background(), acct, testRegion, st, testScanID)
			if tc.wantNilErr {
				if err != nil {
					t.Fatalf("err = %v; want nil", err)
				}
			} else if got := classifyServiceError(err); got != tc.wantOutcome {
				t.Fatalf("classifyServiceError(%v) = %v; want %v", err, got, tc.wantOutcome)
			}
			if total != tc.wantTotal || inserted != tc.wantTotal {
				t.Errorf("total, inserted = %d, %d; want %d, %d", total, inserted, tc.wantTotal, tc.wantTotal)
			}
			_, stored := qsRows(t, st, TypeInspector2DelegatedAdminAccount)[i2AdminNativeID(testI2Admin)]
			if stored != tc.wantAdminRow {
				t.Errorf("delegated admin stored = %v; want %v", stored, tc.wantAdminRow)
			}
		})
	}
}

// TestScanInspector2_TransientDoesNotMaskHardError pins that a transient error
// on either side of the join is demoted to one warning, so the other side's
// hard error still classifies as a hard error.
func TestScanInspector2_TransientDoesNotMaskHardError(t *testing.T) {
	throttle := apiErr("ThrottlingException", "slow down")
	hard := apiErr("ValidationException", "boom")
	cases := []struct {
		name, warnOp string
		override     map[string][]stubCall
	}{
		{"filters throttled, CIS fails", "inspector2:ListFilters", map[string][]stubCall{
			"ListFilters":               {{Err: throttle}},
			"ListCisScanConfigurations": {{Err: hard}},
		}},
		{"CIS throttled, filters fail", "inspector2:ListCisScanConfigurations", map[string][]stubCall{
			"ListFilters":               {{Err: hard}},
			"ListCisScanConfigurations": {{Err: throttle}},
		}},
		{"older phase throttled, delegated admin fails", "inspector2:ListConnectors", map[string][]stubCall{
			"ListConnectors":             {{Err: throttle}},
			"ListDelegatedAdminAccounts": {{Err: hard}},
		}},
		{"delegated admin throttled, older phase fails", "inspector2:ListDelegatedAdminAccounts", map[string][]stubCall{
			"ListConnectors":             {{Err: hard}},
			"ListDelegatedAdminAccounts": {{Err: throttle}},
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := countOpWarnings(st, tc.warnOp)
			acct := newTestAccount(testAccountID)
			acct.cfg = inspector2ServiceStub(t, tc.override)
			_, _, err := scanInspector2(context.Background(), acct, testRegion, st, testScanID)
			if got := classifyServiceError(err); got != outcomeError {
				t.Errorf("classifyServiceError(%v) = %v; want outcomeError", err, got)
			}
			if !isAPIErrorCode(err, "ValidationException") {
				t.Errorf("err = %v; want the ValidationException", err)
			}
			if *warns != 1 {
				t.Errorf("%s warnings = %d; want 1 for the demoted transient", tc.warnOp, *warns)
			}
		})
	}
}
