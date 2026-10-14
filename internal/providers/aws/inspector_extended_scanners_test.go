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

// fakeInspector2 serves ListConnectors pages keyed by NextToken ("" is the
// first page); pageErrs[token] fails that page.
type fakeInspector2 struct {
	inspector2API
	connectors map[string]*inspector2.ListConnectorsOutput
	pageErrs   map[string]error
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
// existing phase plus one connector; override replaces an op's queue.
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
	}
	for op, c := range override {
		calls[op] = c
	}
	return cfgWithStub(stubResponses(t, calls), testRegion)
}

func TestScanInspector2_StoresConnectors(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	acct.cfg = inspector2ServiceStub(t, nil)
	total, _, err := scanInspector2(context.Background(), acct, testRegion, st, testScanID)
	if err != nil {
		t.Fatalf("scanInspector2: %v", err)
	}
	if total != 6 {
		t.Errorf("total = %d; want 6 (one row per existing phase plus the connector)", total)
	}
	if _, ok := qsRows(t, st, TypeInspector2Connector)[i2ConnectorARN("c1")]; !ok {
		t.Errorf("connector %s not stored", i2ConnectorARN("c1"))
	}
}

// TestScanInspector2_ConnectorErrorAfterExistingPhases pins that the connector
// phase runs after every existing phase and that its hard error reaches the
// service result.
func TestScanInspector2_ConnectorErrorAfterExistingPhases(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	acct.cfg = inspector2ServiceStub(t, map[string][]stubCall{
		"ListConnectors": {{Err: apiErr("InternalServerException", "boom")}},
	})
	_, _, err := scanInspector2(context.Background(), acct, testRegion, st, testScanID)
	if !isAPIErrorCode(err, "InternalServerException") {
		t.Fatalf("err = %v; want the ListConnectors InternalServerException", err)
	}
	for _, typ := range []string{
		TypeInspector2Filter, TypeInspector2Member, TypeInspector2CisScanConfiguration,
		TypeInspector2CodeSecurityIntegration, TypeInspector2CodeSecurityScanConfiguration,
	} {
		if got := len(qsRows(t, st, typ)); got != 1 {
			t.Errorf("stored %d %s rows; want 1 (listed before the failing phase)", got, typ)
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
	if total != 5 {
		t.Errorf("total = %d; want 5 (one row per existing phase)", total)
	}
	if len(warned) != 1 || warned[0] != "inspector2:ListConnectors" {
		t.Errorf("warnings = %v; want exactly one naming inspector2:ListConnectors", warned)
	}
}
