package aws

import (
	"context"
	"fmt"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/connect"
	cttypes "github.com/aws/aws-sdk-go-v2/service/connect/types"
	"github.com/icearp/disco-cli/store"
)

type stubConnectFlows struct {
	flows         []cttypes.ContactFlowSummary
	flowOut       map[string]*connect.DescribeContactFlowOutput
	flowVersions  map[string][]cttypes.ContactFlowVersionSummary
	modules       []cttypes.ContactFlowModuleSummary
	moduleOut     map[string]*connect.DescribeContactFlowModuleOutput
	moduleVer     map[string][]cttypes.ContactFlowModuleVersionSummary
	moduleAliases map[string][]cttypes.ContactFlowModuleAliasSummary
	aliasOut      map[string]*connect.DescribeContactFlowModuleAliasOutput
}

func (s *stubConnectFlows) ListContactFlows(_ context.Context, _ *connect.ListContactFlowsInput, _ ...func(*connect.Options)) (*connect.ListContactFlowsOutput, error) {
	return &connect.ListContactFlowsOutput{ContactFlowSummaryList: s.flows}, nil
}

func (s *stubConnectFlows) DescribeContactFlow(_ context.Context, in *connect.DescribeContactFlowInput, _ ...func(*connect.Options)) (*connect.DescribeContactFlowOutput, error) {
	return s.flowOut[*in.ContactFlowId], nil
}

func (s *stubConnectFlows) ListContactFlowVersions(_ context.Context, in *connect.ListContactFlowVersionsInput, _ ...func(*connect.Options)) (*connect.ListContactFlowVersionsOutput, error) {
	return &connect.ListContactFlowVersionsOutput{ContactFlowVersionSummaryList: s.flowVersions[*in.ContactFlowId]}, nil
}

func (s *stubConnectFlows) ListContactFlowModules(_ context.Context, _ *connect.ListContactFlowModulesInput, _ ...func(*connect.Options)) (*connect.ListContactFlowModulesOutput, error) {
	return &connect.ListContactFlowModulesOutput{ContactFlowModulesSummaryList: s.modules}, nil
}

func (s *stubConnectFlows) DescribeContactFlowModule(_ context.Context, in *connect.DescribeContactFlowModuleInput, _ ...func(*connect.Options)) (*connect.DescribeContactFlowModuleOutput, error) {
	return s.moduleOut[*in.ContactFlowModuleId], nil
}

func (s *stubConnectFlows) ListContactFlowModuleVersions(_ context.Context, in *connect.ListContactFlowModuleVersionsInput, _ ...func(*connect.Options)) (*connect.ListContactFlowModuleVersionsOutput, error) {
	return &connect.ListContactFlowModuleVersionsOutput{ContactFlowModuleVersionSummaryList: s.moduleVer[*in.ContactFlowModuleId]}, nil
}

func (s *stubConnectFlows) ListContactFlowModuleAliases(_ context.Context, in *connect.ListContactFlowModuleAliasesInput, _ ...func(*connect.Options)) (*connect.ListContactFlowModuleAliasesOutput, error) {
	return &connect.ListContactFlowModuleAliasesOutput{ContactFlowModuleAliasSummaryList: s.moduleAliases[*in.ContactFlowModuleId]}, nil
}

func (s *stubConnectFlows) DescribeContactFlowModuleAlias(_ context.Context, in *connect.DescribeContactFlowModuleAliasInput, _ ...func(*connect.Options)) (*connect.DescribeContactFlowModuleAliasOutput, error) {
	return s.aliasOut[*in.AliasId], nil
}

func TestScanConnectFlows(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	instID := "11111111-1111-1111-1111-111111111111"
	instARN := fmt.Sprintf("arn:aws:connect:%s:%s:instance/%s", testRegion, acct.ID, instID)
	instances := []cttypes.InstanceSummary{{Id: &instID, Arn: &instARN}}

	fID := "f-1"
	fARN := fmt.Sprintf("arn:aws:connect:%s:%s:instance/%s/contact-flow/%s", testRegion, acct.ID, instID, fID)
	fName := "flow"
	fvARN := fARN + ":1"
	fvVer := int64(1)
	mID := "m-1"
	mARN := fmt.Sprintf("arn:aws:connect:%s:%s:instance/%s/flow-module/%s", testRegion, acct.ID, instID, mID)
	mName := "mod"
	mvARN := mARN + ":1"
	mvVer := int64(1)
	aID := "a-1"
	aARN := mARN + "/alias/" + aID
	aName := "live"

	stub := &stubConnectFlows{
		flows: []cttypes.ContactFlowSummary{{Id: &fID, Arn: &fARN, Name: &fName}},
		flowOut: map[string]*connect.DescribeContactFlowOutput{
			fID: {ContactFlow: &cttypes.ContactFlow{Arn: &fARN, Name: &fName, Status: cttypes.ContactFlowStatusPublished}},
		},
		flowVersions: map[string][]cttypes.ContactFlowVersionSummary{
			fID: {{Arn: &fvARN, Version: &fvVer}},
		},
		modules: []cttypes.ContactFlowModuleSummary{{Id: &mID, Arn: &mARN, Name: &mName}},
		moduleOut: map[string]*connect.DescribeContactFlowModuleOutput{
			mID: {ContactFlowModule: &cttypes.ContactFlowModule{Arn: &mARN, Name: &mName, Status: cttypes.ContactFlowModuleStatusPublished}},
		},
		moduleVer: map[string][]cttypes.ContactFlowModuleVersionSummary{
			mID: {{Arn: &mvARN, Version: &mvVer}},
		},
		moduleAliases: map[string][]cttypes.ContactFlowModuleAliasSummary{
			mID: {{AliasId: &aID, Arn: &aARN, AliasName: &aName}},
		},
		aliasOut: map[string]*connect.DescribeContactFlowModuleAliasOutput{
			aID: {ContactFlowModuleAlias: &cttypes.ContactFlowModuleAliasInfo{AliasId: &aID, Name: &aName}},
		},
	}

	total, inserted, err := scanConnectFlows(context.Background(), stub, instances, acct, testRegion, st, testScanID)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != 5 || inserted != 5 {
		t.Fatalf("total=%d inserted=%d want 5/5", total, inserted)
	}
	for _, want := range []struct{ typ, id string }{
		{TypeConnectContactFlow, fARN},
		{TypeConnectContactFlowVersion, fvARN},
		{TypeConnectContactFlowModule, mARN},
		{TypeConnectContactFlowModuleVersion, mvARN},
		{TypeConnectContactFlowModuleAlias, aARN},
	} {
		if _, err := st.GetResource(store.ResourceID("aws", acct.ID, want.id)); err != nil {
			t.Errorf("%s missing: %v", want.typ, err)
		}
	}
}

func TestScanConnectFlowsEmpty(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	stub := &stubConnectFlows{}
	total, inserted, err := scanConnectFlows(context.Background(), stub, nil, acct, testRegion, st, testScanID)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != 0 || inserted != 0 {
		t.Fatalf("total=%d inserted=%d want 0/0", total, inserted)
	}
}
