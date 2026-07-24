package aws

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/icearp/disco-cli/store"
	"github.com/aws/aws-sdk-go-v2/service/connect"
	cttypes "github.com/aws/aws-sdk-go-v2/service/connect/types"
)

type stubConnectCore struct {
	instances    []cttypes.InstanceSummary
	instanceOut  map[string]*connect.DescribeInstanceOutput
	tdgs         []cttypes.TrafficDistributionGroupSummary
	tdgOut       map[string]*connect.DescribeTrafficDistributionGroupOutput
	phoneNumbers []cttypes.ListPhoneNumbersSummary
	phoneOut     map[string]*connect.DescribePhoneNumberOutput
	emailByInst  map[string][]cttypes.EmailAddressMetadata
}

func (s *stubConnectCore) ListInstances(_ context.Context, _ *connect.ListInstancesInput, _ ...func(*connect.Options)) (*connect.ListInstancesOutput, error) {
	return &connect.ListInstancesOutput{InstanceSummaryList: s.instances}, nil
}

func (s *stubConnectCore) DescribeInstance(_ context.Context, in *connect.DescribeInstanceInput, _ ...func(*connect.Options)) (*connect.DescribeInstanceOutput, error) {
	return s.instanceOut[*in.InstanceId], nil
}

func (s *stubConnectCore) ListTrafficDistributionGroups(_ context.Context, _ *connect.ListTrafficDistributionGroupsInput, _ ...func(*connect.Options)) (*connect.ListTrafficDistributionGroupsOutput, error) {
	return &connect.ListTrafficDistributionGroupsOutput{TrafficDistributionGroupSummaryList: s.tdgs}, nil
}

func (s *stubConnectCore) DescribeTrafficDistributionGroup(_ context.Context, in *connect.DescribeTrafficDistributionGroupInput, _ ...func(*connect.Options)) (*connect.DescribeTrafficDistributionGroupOutput, error) {
	return s.tdgOut[*in.TrafficDistributionGroupId], nil
}

func (s *stubConnectCore) ListPhoneNumbersV2(_ context.Context, _ *connect.ListPhoneNumbersV2Input, _ ...func(*connect.Options)) (*connect.ListPhoneNumbersV2Output, error) {
	return &connect.ListPhoneNumbersV2Output{ListPhoneNumbersSummaryList: s.phoneNumbers}, nil
}

func (s *stubConnectCore) DescribePhoneNumber(_ context.Context, in *connect.DescribePhoneNumberInput, _ ...func(*connect.Options)) (*connect.DescribePhoneNumberOutput, error) {
	return s.phoneOut[*in.PhoneNumberId], nil
}

func (s *stubConnectCore) SearchEmailAddresses(_ context.Context, in *connect.SearchEmailAddressesInput, _ ...func(*connect.Options)) (*connect.SearchEmailAddressesOutput, error) {
	return &connect.SearchEmailAddressesOutput{EmailAddresses: s.emailByInst[*in.InstanceId]}, nil
}

func TestScanConnectCore(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	now := time.Unix(1700000000, 0).UTC()

	instID := "11111111-1111-1111-1111-111111111111"
	instARN := fmt.Sprintf("arn:aws:connect:%s:%s:instance/%s", testRegion, acct.ID, instID)
	instAlias := "team"
	tdgID := "22222222-2222-2222-2222-222222222222"
	tdgARN := fmt.Sprintf("arn:aws:connect:%s:%s:traffic-distribution-group/%s", testRegion, acct.ID, tdgID)
	tdgName := "tdg"
	pnID := "33333333-3333-3333-3333-333333333333"
	pnARN := fmt.Sprintf("arn:aws:connect:%s:%s:phone-number/%s", testRegion, acct.ID, pnID)
	pnNum := "+18005551212"
	emailID := "44444444-4444-4444-4444-444444444444"
	emailARN := fmt.Sprintf("arn:aws:connect:%s:%s:instance/%s/email-address/%s", testRegion, acct.ID, instID, emailID)
	email := "support@example.com"

	stub := &stubConnectCore{
		instances: []cttypes.InstanceSummary{{Id: &instID, Arn: &instARN, InstanceAlias: &instAlias, CreatedTime: &now}},
		instanceOut: map[string]*connect.DescribeInstanceOutput{
			instID: {Instance: &cttypes.Instance{Id: &instID, Arn: &instARN, InstanceAlias: &instAlias, InstanceStatus: cttypes.InstanceStatusActive, CreatedTime: &now}},
		},
		tdgs: []cttypes.TrafficDistributionGroupSummary{{Id: &tdgID, Arn: &tdgARN, Name: &tdgName, InstanceArn: &instARN}},
		tdgOut: map[string]*connect.DescribeTrafficDistributionGroupOutput{
			tdgID: {TrafficDistributionGroup: &cttypes.TrafficDistributionGroup{Id: &tdgID, Arn: &tdgARN, Name: &tdgName, InstanceArn: &instARN, Status: cttypes.TrafficDistributionGroupStatusActive}},
		},
		phoneNumbers: []cttypes.ListPhoneNumbersSummary{{PhoneNumberId: &pnID, PhoneNumberArn: &pnARN, PhoneNumber: &pnNum, InstanceId: &instID}},
		phoneOut: map[string]*connect.DescribePhoneNumberOutput{
			pnID: {ClaimedPhoneNumberSummary: &cttypes.ClaimedPhoneNumberSummary{PhoneNumberId: &pnID, PhoneNumberArn: &pnARN, PhoneNumber: &pnNum, InstanceId: &instID}},
		},
		emailByInst: map[string][]cttypes.EmailAddressMetadata{
			instID: {{EmailAddressId: &emailID, EmailAddressArn: &emailARN, EmailAddress: &email}},
		},
	}

	total, inserted, err := scanConnectCore(context.Background(), stub, stub.instances, acct, testRegion, st, testScanID)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != 4 || inserted != 4 {
		t.Fatalf("total=%d inserted=%d want 4/4", total, inserted)
	}
	for _, want := range []struct{ typ, id string }{
		{TypeConnectInstance, instARN},
		{TypeConnectTrafficDistributionGroup, tdgARN},
		{TypeConnectPhoneNumber, pnARN},
		{TypeConnectEmailAddress, emailARN},
	} {
		if _, err := st.GetResource(store.ResourceID("aws", acct.ID, want.id)); err != nil {
			t.Errorf("%s missing: %v", want.typ, err)
		}
	}
}

func TestScanConnectCoreEmpty(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	stub := &stubConnectCore{}
	total, inserted, err := scanConnectCore(context.Background(), stub, nil, acct, testRegion, st, testScanID)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != 0 || inserted != 0 {
		t.Fatalf("total=%d inserted=%d want 0/0", total, inserted)
	}
}
