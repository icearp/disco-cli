package aws

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"
	"github.com/icearp/disco-cli/internal/util"
	"github.com/icearp/disco-cli/store"
)

// stubSSMHybrid serves DescribeActivations and ListCloudConnectors pages in
// order; every other ssmAPI method panics via the nil embedded interface.
type stubSSMHybrid struct {
	ssmAPI
	activationPages []*ssm.DescribeActivationsOutput
	activationErr   error
	activationIns   []*ssm.DescribeActivationsInput
	connectorPages  []*ssm.ListCloudConnectorsOutput
	connectorErr    error
	connectorIns    []*ssm.ListCloudConnectorsInput
}

func (s *stubSSMHybrid) DescribeActivations(_ context.Context, in *ssm.DescribeActivationsInput, _ ...func(*ssm.Options)) (*ssm.DescribeActivationsOutput, error) {
	s.activationIns = append(s.activationIns, in)
	if s.activationErr != nil {
		return nil, s.activationErr
	}
	return s.activationPages[len(s.activationIns)-1], nil
}

func (s *stubSSMHybrid) ListCloudConnectors(_ context.Context, in *ssm.ListCloudConnectorsInput, _ ...func(*ssm.Options)) (*ssm.ListCloudConnectorsOutput, error) {
	s.connectorIns = append(s.connectorIns, in)
	if s.connectorErr != nil {
		return nil, s.connectorErr
	}
	return s.connectorPages[len(s.connectorIns)-1], nil
}

func listSSMRows(t *testing.T, st *store.Store, rtype string) map[string]store.Resource {
	t.Helper()
	rows, err := st.ListResources(store.ResourceFilter{Types: []string{rtype}, Limit: util.AllResources, IncludeManaged: true})
	if err != nil {
		t.Fatalf("ListResources: %v", err)
	}
	byID := make(map[string]store.Resource, len(rows))
	for _, r := range rows {
		byID[r.NativeID] = r
	}
	return byID
}

func TestScanSSMActivations_PaginatesAndStores(t *testing.T) {
	const (
		arn1 = "arn:aws:ssm:us-east-1:123456789012:activation/act-1"
		arn2 = "arn:aws:ssm:us-east-1:123456789012:activation/act-2"
	)
	created := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	stub := &stubSSMHybrid{activationPages: []*ssm.DescribeActivationsOutput{
		{
			ActivationList: []ssmtypes.Activation{
				{
					ActivationId: sdkaws.String("act-1"), CreatedDate: &created,
					IamRole: sdkaws.String("SSMServiceRole"),
					Tags:    []ssmtypes.Tag{{Key: sdkaws.String("env"), Value: sdkaws.String("prod")}},
				},
				{IamRole: sdkaws.String("no-id")},
			},
			NextToken: sdkaws.String("page2"),
		},
		{ActivationList: []ssmtypes.Activation{{ActivationId: sdkaws.String("act-2"), Expired: true}}},
	}}
	st := newTestStore(t)

	total, _, err := scanSSMActivations(context.Background(), stub, newTestAccount(testAccountID), testRegion, st, testScanID)
	if err != nil {
		t.Fatalf("scanSSMActivations: %v", err)
	}
	if total != 2 {
		t.Errorf("total = %d, want 2 (empty-id element skipped)", total)
	}
	if len(stub.activationIns) != 2 || sv(stub.activationIns[1].NextToken) != "page2" {
		t.Fatalf("DescribeActivations inputs = %+v, want 2 calls, second with NextToken page2", stub.activationIns)
	}

	byID := listSSMRows(t, st, TypeSSMActivation)
	if len(byID) != 2 {
		t.Fatalf("stored %d rows, want 2: %v", len(byID), byID)
	}
	r1, ok := byID[arn1]
	if !ok {
		t.Fatalf("row %s missing", arn1)
	}
	if r1.Region == nil || *r1.Region != testRegion {
		t.Errorf("Region = %v, want %s", r1.Region, testRegion)
	}
	if sv(r1.Name) != "act-1" {
		t.Errorf("Name = %q, want act-1", sv(r1.Name))
	}
	if sv(r1.Status) != "active" {
		t.Errorf("Status = %q, want active", sv(r1.Status))
	}
	if sv(r1.CreatedAt) != created.Format(time.RFC3339) {
		t.Errorf("CreatedAt = %q, want %s", sv(r1.CreatedAt), created.Format(time.RFC3339))
	}
	if !strings.Contains(r1.AttributesJSON, `"IamRole":"SSMServiceRole"`) {
		t.Errorf("attrs missing IamRole: %s", r1.AttributesJSON)
	}
	if sv(r1.TagsJSON) != `{"env":"prod"}` {
		t.Errorf("TagsJSON = %q, want {\"env\":\"prod\"}", sv(r1.TagsJSON))
	}
	r2, ok := byID[arn2]
	if !ok {
		t.Fatalf("row %s missing (second page)", arn2)
	}
	if sv(r2.Status) != "expired" {
		t.Errorf("Status = %q, want expired", sv(r2.Status))
	}
	if r2.TagsJSON != nil {
		t.Errorf("TagsJSON = %q, want unset for an untagged activation", *r2.TagsJSON)
	}
}

func TestScanSSMCloudConnectors_PaginatesAndStores(t *testing.T) {
	const (
		arn1 = "arn:aws:ssm:us-east-1:123456789012:cloud-connector/cc-1"
		arn2 = "arn:aws:ssm:us-east-1:123456789012:cloud-connector/cc-2"
	)
	created := time.Date(2026, 2, 3, 4, 5, 6, 0, time.UTC)
	stub := &stubSSMHybrid{connectorPages: []*ssm.ListCloudConnectorsOutput{
		{
			CloudConnectors: []ssmtypes.CloudConnectorSummary{
				{
					CloudConnectorId: sdkaws.String("cc-1"), DisplayName: sdkaws.String("azure-prod"),
					CreatedAt: &created, RoleArn: sdkaws.String("arn:aws:iam::123456789012:role/cc"),
				},
				{DisplayName: sdkaws.String("no-id")},
			},
			NextToken: sdkaws.String("page2"),
		},
		{CloudConnectors: []ssmtypes.CloudConnectorSummary{{CloudConnectorId: sdkaws.String("cc-2")}}},
	}}
	st := newTestStore(t)

	total, _, err := scanSSMCloudConnectors(context.Background(), stub, newTestAccount(testAccountID), testRegion, st, testScanID)
	if err != nil {
		t.Fatalf("scanSSMCloudConnectors: %v", err)
	}
	if total != 2 {
		t.Errorf("total = %d, want 2 (empty-id element skipped)", total)
	}
	if len(stub.connectorIns) != 2 || sv(stub.connectorIns[1].NextToken) != "page2" {
		t.Fatalf("ListCloudConnectors inputs = %+v, want 2 calls, second with NextToken page2", stub.connectorIns)
	}

	byID := listSSMRows(t, st, TypeSSMCloudConnector)
	if len(byID) != 2 {
		t.Fatalf("stored %d rows, want 2: %v", len(byID), byID)
	}
	r1, ok := byID[arn1]
	if !ok {
		t.Fatalf("row %s missing", arn1)
	}
	if r1.Region == nil || *r1.Region != testRegion {
		t.Errorf("Region = %v, want %s", r1.Region, testRegion)
	}
	if sv(r1.Name) != "azure-prod" {
		t.Errorf("Name = %q, want azure-prod", sv(r1.Name))
	}
	if sv(r1.CreatedAt) != created.Format(time.RFC3339) {
		t.Errorf("CreatedAt = %q, want %s", sv(r1.CreatedAt), created.Format(time.RFC3339))
	}
	if !strings.Contains(r1.AttributesJSON, `"RoleArn":"arn:aws:iam::123456789012:role/cc"`) {
		t.Errorf("attrs missing RoleArn: %s", r1.AttributesJSON)
	}
	r2, ok := byID[arn2]
	if !ok {
		t.Fatalf("row %s missing (second page)", arn2)
	}
	if sv(r2.Name) != "cc-2" {
		t.Errorf("Name = %q, want the id when DisplayName is unset", sv(r2.Name))
	}
}

// ssmHybridScanner runs one of the two new sub-scanners against a stub, so the
// shared empty / denied / error cases below run against both.
type ssmHybridScanner struct {
	name   string
	op     string
	rtype  string
	run    func(*stubSSMHybrid, *store.Store) (int, int, error)
	setErr func(*stubSSMHybrid, error)
	empty  func(*stubSSMHybrid)
}

func ssmHybridScanners() []ssmHybridScanner {
	acct := newTestAccount(testAccountID)
	return []ssmHybridScanner{
		{
			name: "activations", op: "ssm:DescribeActivations", rtype: TypeSSMActivation,
			run: func(s *stubSSMHybrid, st *store.Store) (int, int, error) {
				return scanSSMActivations(context.Background(), s, acct, testRegion, st, testScanID)
			},
			setErr: func(s *stubSSMHybrid, err error) { s.activationErr = err },
			empty:  func(s *stubSSMHybrid) { s.activationPages = []*ssm.DescribeActivationsOutput{{}} },
		},
		{
			name: "cloud connectors", op: "ssm:ListCloudConnectors", rtype: TypeSSMCloudConnector,
			run: func(s *stubSSMHybrid, st *store.Store) (int, int, error) {
				return scanSSMCloudConnectors(context.Background(), s, acct, testRegion, st, testScanID)
			},
			setErr: func(s *stubSSMHybrid, err error) { s.connectorErr = err },
			empty:  func(s *stubSSMHybrid) { s.connectorPages = []*ssm.ListCloudConnectorsOutput{{}} },
		},
	}
}

func TestScanSSMHybrid_Empty(t *testing.T) {
	for _, sc := range ssmHybridScanners() {
		t.Run(sc.name, func(t *testing.T) {
			stub := &stubSSMHybrid{}
			sc.empty(stub)
			st := newTestStore(t)
			total, inserted, err := sc.run(stub, st)
			if err != nil {
				t.Fatalf("%s: %v", sc.op, err)
			}
			if total != 0 || inserted != 0 {
				t.Errorf("want (0,0), got (%d,%d)", total, inserted)
			}
			if rows := listSSMRows(t, st, sc.rtype); len(rows) != 0 {
				t.Errorf("stored %d rows, want 0", len(rows))
			}
		})
	}
}

func TestScanSSMHybrid_AccessDeniedWarns(t *testing.T) {
	for _, sc := range ssmHybridScanners() {
		t.Run(sc.name, func(t *testing.T) {
			stub := &stubSSMHybrid{}
			sc.setErr(stub, apiErr("AccessDeniedException", "not authorized to perform: "+sc.op))
			st := newTestStore(t)
			var warnings []store.ScanWarning
			st.OnWarn = func(w store.ScanWarning) { warnings = append(warnings, w) }

			if _, _, err := sc.run(stub, st); err != nil {
				t.Fatalf("access denied must not error, got %v", err)
			}
			if len(warnings) != 1 {
				t.Fatalf("warnings = %d, want 1", len(warnings))
			}
			if warnings[0].Service != sc.op {
				t.Errorf("warning Service = %q, want %s", warnings[0].Service, sc.op)
			}
		})
	}
}

func TestScanSSMHybrid_OtherErrorPropagates(t *testing.T) {
	for _, sc := range ssmHybridScanners() {
		t.Run(sc.name, func(t *testing.T) {
			stub := &stubSSMHybrid{}
			sc.setErr(stub, apiErr("InternalServerError", "boom"))
			_, _, err := sc.run(stub, newTestStore(t))
			if !isAPIErrorCode(err, "InternalServerError") {
				t.Fatalf("want wrapped InternalServerError, got %v", err)
			}
		})
	}
}

// emptySSMResponses queues one empty page for every op scanSSM calls, so a
// test overrides only the ops it cares about.
func emptySSMResponses() map[string][]stubCall {
	return map[string][]stubCall{
		"DescribeParameters":          {{Output: &ssm.DescribeParametersOutput{}}},
		"ListDocuments":               {{Output: &ssm.ListDocumentsOutput{}}},
		"DescribePatchBaselines":      {{Output: &ssm.DescribePatchBaselinesOutput{}}},
		"ListAssociations":            {{Output: &ssm.ListAssociationsOutput{}}},
		"DescribeMaintenanceWindows":  {{Output: &ssm.DescribeMaintenanceWindowsOutput{}}},
		"ListResourceDataSync":        {{Output: &ssm.ListResourceDataSyncOutput{}}},
		"DescribeInstanceInformation": {{Output: &ssm.DescribeInstanceInformationOutput{}}},
		"ListOpsMetadata":             {{Output: &ssm.ListOpsMetadataOutput{}}},
		"DescribeActivations":         {{Output: &ssm.DescribeActivationsOutput{}}},
		"ListCloudConnectors":         {{Output: &ssm.ListCloudConnectorsOutput{}}},
	}
}

func TestScanSSM_StoresActivationsAndCloudConnectors(t *testing.T) {
	responses := emptySSMResponses()
	responses["DescribeActivations"] = []stubCall{{Output: &ssm.DescribeActivationsOutput{
		ActivationList: []ssmtypes.Activation{{ActivationId: sdkaws.String("act-1")}},
	}}}
	responses["ListCloudConnectors"] = []stubCall{{Output: &ssm.ListCloudConnectorsOutput{
		CloudConnectors: []ssmtypes.CloudConnectorSummary{{CloudConnectorId: sdkaws.String("cc-1")}},
	}}}
	st := newTestStore(t)
	acct := &account{ID: testAccountID, Name: "Test Account", cfg: cloud9CfgWithStub(stubResponses(t, responses), testRegion)}

	total, inserted, err := scanSSM(context.Background(), acct, testRegion, st, testScanID)
	if err != nil {
		t.Fatalf("scanSSM: %v", err)
	}
	// Fresh store and these are the only rows, so both counters must be 2.
	if total != 2 || inserted != 2 {
		t.Errorf("scanSSM = (%d total, %d inserted), want (2, 2)", total, inserted)
	}
	for rtype, want := range map[string]string{
		TypeSSMActivation:     "arn:aws:ssm:us-east-1:123456789012:activation/act-1",
		TypeSSMCloudConnector: "arn:aws:ssm:us-east-1:123456789012:cloud-connector/cc-1",
	} {
		rows := listSSMRows(t, st, rtype)
		if _, ok := rows[want]; !ok || len(rows) != 1 {
			t.Errorf("%s rows = %v, want one with NativeID %s", rtype, rows, want)
		}
	}
}

// A hard error from either new op must propagate without costing the rows of
// the associations phase (first in scanSSMExtended) or the OpsMetadata phase
// (last pre-existing one). Each case pins its op after both; the
// ListCloudConnectors case also pins cloud connectors after activations.
func TestScanSSM_NewPhaseErrorKeepsEarlierPhases(t *testing.T) {
	cases := []struct {
		failingOp   string
		wantActRows int
	}{
		{failingOp: "DescribeActivations", wantActRows: 0},
		{failingOp: "ListCloudConnectors", wantActRows: 1},
	}
	for _, tc := range cases {
		t.Run(tc.failingOp, func(t *testing.T) {
			responses := emptySSMResponses()
			responses["ListAssociations"] = []stubCall{{Output: &ssm.ListAssociationsOutput{
				Associations: []ssmtypes.Association{{AssociationId: sdkaws.String("assoc-1")}},
			}}}
			responses["ListOpsMetadata"] = []stubCall{{Output: &ssm.ListOpsMetadataOutput{
				OpsMetadataList: []ssmtypes.OpsMetadata{{OpsMetadataArn: sdkaws.String("arn:aws:ssm:us-east-1:123456789012:opsmetadata/app")}},
			}}}
			if tc.failingOp != "DescribeActivations" {
				responses["DescribeActivations"] = []stubCall{{Output: &ssm.DescribeActivationsOutput{
					ActivationList: []ssmtypes.Activation{{ActivationId: sdkaws.String("act-1")}},
				}}}
			}
			boom := errors.New("boom")
			responses[tc.failingOp] = []stubCall{{Err: boom}}
			st := newTestStore(t)
			acct := &account{ID: testAccountID, Name: "Test Account", cfg: cloud9CfgWithStub(stubResponses(t, responses), testRegion)}

			_, _, err := scanSSM(context.Background(), acct, testRegion, st, testScanID)
			if !errors.Is(err, boom) {
				t.Fatalf("want the %s error to propagate, got %v", tc.failingOp, err)
			}
			if rows := listSSMRows(t, st, TypeSSMAssociation); len(rows) != 1 {
				t.Errorf("association rows = %d, want 1 (must run before %s)", len(rows), tc.failingOp)
			}
			if rows := listSSMRows(t, st, TypeSSMOpsMetadata); len(rows) != 1 {
				t.Errorf("opsmetadata rows = %d, want 1 (must run before %s)", len(rows), tc.failingOp)
			}
			if rows := listSSMRows(t, st, TypeSSMActivation); len(rows) != tc.wantActRows {
				t.Errorf("activation rows = %d, want %d", len(rows), tc.wantActRows)
			}
		})
	}
}
