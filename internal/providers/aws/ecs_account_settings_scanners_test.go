package aws

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ecs"
	ecstypes "github.com/aws/aws-sdk-go-v2/service/ecs/types"
	"github.com/icearp/disco-cli/internal/util"
	"github.com/icearp/disco-cli/store"
)

var (
	ecsDenied         = apiErr("AccessDeniedException", "User: arn:aws:iam::123456789012:user/x is not authorized to perform: ecs:List")
	ecsTestRoleARN    = "arn:aws:iam::123456789012:role/scanner"
	ecsTestClusterARN = fmt.Sprintf("arn:aws:ecs:%s:%s:cluster/cl-1", testRegion, testAccountID)
)

// ecsAccountSettingsHandler answers ListAccountSettings from pages; a page
// other than the last carries NextToken "p<next index>". errs[i] fails page i.
// A call that does not ask for effective settings at the model's page cap
// fails, so both inputs are pinned.
func ecsAccountSettingsHandler(pages [][]ecstypes.Setting, errs map[int]error) func(any) (any, error) {
	return func(in any) (any, error) {
		req := in.(*ecs.ListAccountSettingsInput)
		if !req.EffectiveSettings || req.MaxResults != ecsAccountSettingsPageLimit {
			return nil, fmt.Errorf("EffectiveSettings, MaxResults = %v, %d; want true, %d", req.EffectiveSettings, req.MaxResults, ecsAccountSettingsPageLimit)
		}
		page := 0
		if tok := sdkaws.ToString(req.NextToken); tok != "" {
			if _, err := fmt.Sscanf(tok, "p%d", &page); err != nil {
				return nil, err
			}
		}
		if err := errs[page]; err != nil {
			return nil, err
		}
		out := &ecs.ListAccountSettingsOutput{}
		if page < len(pages) {
			out.Settings = pages[page]
		}
		if page+1 < len(pages) {
			out.NextToken = sdkaws.String(fmt.Sprintf("p%d", page+1))
		}
		return out, nil
	}
}

func ecsSetting(name ecstypes.SettingName, value string, typ ecstypes.SettingType) ecstypes.Setting {
	return ecstypes.Setting{Name: name, Value: sdkaws.String(value), PrincipalArn: sdkaws.String(ecsTestRoleARN), Type: typ}
}

func scanECSAccountSettingsWith(t *testing.T, st *store.Store, handler func(any) (any, error)) (int, int, error) {
	t.Helper()
	client := ecs.NewFromConfig(opStubConfig(map[string]func(any) (any, error){"ListAccountSettings": handler}, ecsDenied))
	return scanECSAccountSettings(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID)
}

func TestScanECSAccountSettings_PagesAndFields(t *testing.T) {
	st := newTestStore(t)
	pages := [][]ecstypes.Setting{
		{ecsSetting(ecstypes.SettingNameContainerInsights, "enabled", ecstypes.SettingTypeUser)},
		{
			ecsSetting(ecstypes.SettingNameGuardDutyActivate, "on", ecstypes.SettingTypeAwsManaged),
			{Value: sdkaws.String("enabled")},
		},
	}
	total, inserted, err := scanECSAccountSettingsWith(t, st, ecsAccountSettingsHandler(pages, nil))
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != 2 || inserted != 2 {
		t.Errorf("total, inserted = %d, %d; want 2, 2 (the nameless setting skipped)", total, inserted)
	}
	rows := rowsByNativeID(t, st, TypeECSAccountSetting)
	if len(rows) != 2 {
		t.Fatalf("rows = %v; want 2", rows)
	}
	// Every row is provider-managed, whatever its Type: the descriptor
	// marks the per-(account, region) singleton config type Managed.
	cases := []struct {
		name    string
		managed bool
		value   string
	}{
		{"containerInsights", true, "enabled"},
		{"guardDutyActivate", true, "on"},
	}
	for _, tc := range cases {
		id := fmt.Sprintf("arn:aws:ecs:%s:%s:account-setting/%s", testRegion, testAccountID, tc.name)
		r, ok := rows[id]
		if !ok {
			t.Errorf("row %s missing; rows = %v", id, rows)
			continue
		}
		if r.Type != TypeECSAccountSetting || sv(r.Region) != testRegion || sv(r.Name) != tc.name || r.ManagedByProvider != tc.managed {
			t.Errorf("row %s = type %s region %s name %s managed %v; want %s %s %s %v",
				id, r.Type, sv(r.Region), sv(r.Name), r.ManagedByProvider, TypeECSAccountSetting, testRegion, tc.name, tc.managed)
		}
		var attrs ecstypes.Setting
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			t.Fatalf("decode %s attrs: %v", id, err)
		}
		if sv(attrs.Value) != tc.value || sv(attrs.PrincipalArn) != ecsTestRoleARN {
			t.Errorf("row %s attrs Value, PrincipalArn = %q, %q; want %q, %q", id, sv(attrs.Value), sv(attrs.PrincipalArn), tc.value, ecsTestRoleARN)
		}
	}
}

func TestScanECSAccountSettings_Empty(t *testing.T) {
	st := newTestStore(t)
	total, inserted, err := scanECSAccountSettingsWith(t, st, ecsAccountSettingsHandler(nil, nil))
	if err != nil || total != 0 || inserted != 0 {
		t.Errorf("total, inserted, err = %d, %d, %v; want 0, 0, nil", total, inserted, err)
	}
	if rows := rowsByNativeID(t, st, TypeECSAccountSetting); len(rows) != 0 {
		t.Errorf("rows = %v; want none", rows)
	}
}

// A denial warns once and keeps the settings listed before it; any other
// error is returned and also keeps them.
func TestScanECSAccountSettings_ListErrors(t *testing.T) {
	pages := [][]ecstypes.Setting{
		{ecsSetting(ecstypes.SettingNameContainerInsights, "enabled", ecstypes.SettingTypeUser)},
		{ecsSetting(ecstypes.SettingNameTaskLongArnFormat, "enabled", ecstypes.SettingTypeUser)},
	}
	cases := []struct {
		name      string
		errs      map[int]error
		wantRows  int
		wantWarns int
		wantCode  string
	}{
		{"denied on first page", map[int]error{0: ecsDenied}, 0, 1, ""},
		{"denied on second page", map[int]error{1: ecsDenied}, 1, 1, ""},
		{"server error on second page", map[int]error{1: apiErr("ServerException", "boom")}, 1, 0, "ServerException"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := countOpWarnings(st, ecsListAccountSettingsOp)
			total, _, err := scanECSAccountSettingsWith(t, st, ecsAccountSettingsHandler(pages, tc.errs))
			if tc.wantCode == "" && err != nil {
				t.Fatalf("err = %v; want nil", err)
			}
			if tc.wantCode != "" && !isAPIErrorCode(err, tc.wantCode) {
				t.Fatalf("err = %v; want %s", err, tc.wantCode)
			}
			if *warns != tc.wantWarns {
				t.Errorf("warnings = %d; want %d", *warns, tc.wantWarns)
			}
			if rows := rowsByNativeID(t, st, TypeECSAccountSetting); len(rows) != tc.wantRows || total != tc.wantRows {
				t.Errorf("rows, total = %d, %d; want %d", len(rows), total, tc.wantRows)
			}
		})
	}
}

// ecsTopLevelHandlers serves one cluster with no services, and one account
// setting; every other op is denied.
func ecsTopLevelHandlers() map[string]func(any) (any, error) {
	return map[string]func(any) (any, error){
		"ListClusters": func(any) (any, error) {
			return &ecs.ListClustersOutput{ClusterArns: []string{ecsTestClusterARN}}, nil
		},
		"DescribeClusters": func(any) (any, error) {
			return &ecs.DescribeClustersOutput{Clusters: []ecstypes.Cluster{{ClusterArn: sdkaws.String(ecsTestClusterARN), ClusterName: sdkaws.String("cl-1")}}}, nil
		},
		"ListServices": func(any) (any, error) { return &ecs.ListServicesOutput{}, nil },
		"ListAccountSettings": ecsAccountSettingsHandler([][]ecstypes.Setting{
			{ecsSetting(ecstypes.SettingNameContainerInsights, "enabled", ecstypes.SettingTypeUser)},
		}, nil),
	}
}

// storedRowCount counts every row in st, managed ones included.
func storedRowCount(t *testing.T, st *store.Store) int {
	t.Helper()
	rows, err := st.ListResources(store.ResourceFilter{IncludeManaged: true, Limit: util.AllResources})
	if err != nil {
		t.Fatalf("ListResources: %v", err)
	}
	return len(rows)
}

var ecsTestSettingID = ecsAccountSettingNativeID(testRegion, testAccountID, ecstypes.SettingNameContainerInsights)

func TestScanECS_StoresSettingsAndClusters(t *testing.T) {
	st := newTestStore(t)
	acct := &account{ID: testAccountID, Name: "Test Account", cfg: opStubConfig(ecsTopLevelHandlers(), ecsDenied)}
	total, inserted, err := scanECS(context.Background(), acct, testRegion, st, testScanID)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if stored := storedRowCount(t, st); total != stored || inserted != stored {
		t.Errorf("total, inserted = %d, %d; want both %d (every stored row)", total, inserted, stored)
	}
	if clusters := rowsByNativeID(t, st, TypeECSCluster); len(clusters) != 1 || clusters[ecsTestClusterARN].NativeID == "" {
		t.Errorf("cluster rows = %v; want %s only", clusters, ecsTestClusterARN)
	}
	if settings := rowsByNativeID(t, st, TypeECSAccountSetting); len(settings) != 1 || settings[ecsTestSettingID].NativeID == "" {
		t.Errorf("account-setting rows = %v; want %s only", settings, ecsTestSettingID)
	}
}

// The settings phase runs before the cluster phases, so a cluster side that
// runs into the deadline costs no setting rows.
func TestScanECS_SettingsBeforeClusterDeadline(t *testing.T) {
	st := newTestStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	h := ignoreCtx(ecsTopLevelHandlers())
	h["ListClusters"] = func(context.Context, any) (any, error) {
		cancel()
		return nil, context.DeadlineExceeded
	}
	settings := h["ListAccountSettings"]
	h["ListAccountSettings"] = func(ctx context.Context, in any) (any, error) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		return settings(ctx, in)
	}
	acct := &account{ID: testAccountID, Name: "Test Account", cfg: opStubConfigCtx(h, ecsDenied)}
	_, _, err := scanECS(ctx, acct, testRegion, st, testScanID)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("err = %v; want the cluster side's deadline", err)
	}
	if _, ok := rowsByNativeID(t, st, TypeECSAccountSetting)[ecsTestSettingID]; !ok {
		t.Error("account-setting row missing: settings ran after the cluster side")
	}
}

// A hard error on either side is returned, and the other side's rows are
// still stored.
func TestScanECS_PhaseErrorKeepsOtherSide(t *testing.T) {
	cases := []struct {
		name    string
		failOp  string
		wantRow string
		rowType string
	}{
		{"cluster phases fail", "ListClusters", ecsTestSettingID, TypeECSAccountSetting},
		{"account settings fail", "ListAccountSettings", ecsTestClusterARN, TypeECSCluster},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			h := ecsTopLevelHandlers()
			h[tc.failOp] = func(any) (any, error) { return nil, apiErr("ClientException", "bad") }
			acct := &account{ID: testAccountID, Name: "Test Account", cfg: opStubConfig(h, ecsDenied)}
			total, _, err := scanECS(context.Background(), acct, testRegion, st, testScanID)
			if !isAPIErrorCode(err, "ClientException") || classifyServiceError(err) != outcomeError {
				t.Errorf("err = %v; want the ClientException classified outcomeError", err)
			}
			if stored := storedRowCount(t, st); total != stored {
				t.Errorf("total = %d; want %d (every stored row)", total, stored)
			}
			if _, ok := rowsByNativeID(t, st, tc.rowType)[tc.wantRow]; !ok {
				t.Errorf("row %s missing", tc.wantRow)
			}
		})
	}
}

// A transient error on one side is demoted to one warning, so the other
// side's hard error still classifies as an error.
func TestScanECS_TransientSideDemoted(t *testing.T) {
	cases := []struct {
		name, throttleOp, failOp, warnOp string
	}{
		{"cluster phases throttled", "ListClusters", "ListAccountSettings", "ecs"},
		{"account settings throttled", "ListAccountSettings", "ListClusters", ecsListAccountSettingsOp},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := recordWarnings(st)
			h := ecsTopLevelHandlers()
			h[tc.throttleOp] = func(any) (any, error) { return nil, apiErr("ThrottlingException", "Rate exceeded") }
			h[tc.failOp] = func(any) (any, error) { return nil, apiErr("ClientException", "bad") }
			acct := &account{ID: testAccountID, Name: "Test Account", cfg: opStubConfig(h, ecsDenied)}
			_, _, err := scanECS(context.Background(), acct, testRegion, st, testScanID)
			if got := classifyServiceError(err); got != outcomeError || !isAPIErrorCode(err, "ClientException") {
				t.Errorf("err = %v classified %v; want the ClientException as outcomeError", err, got)
			}
			if got := *warns; len(got) != 1 || got[0].Service != tc.warnOp {
				t.Errorf("warnings = %v; want one for %s", got, tc.warnOp)
			}
		})
	}
}
