package aws

import (
	"context"
	"fmt"
	"testing"

	"codeberg.org/icearp/disco/internal/store"
	"github.com/aws/aws-sdk-go-v2/service/connect"
	cttypes "github.com/aws/aws-sdk-go-v2/service/connect/types"
)

type stubConnectRouting struct {
	queues     []cttypes.QueueSummary
	queueOut   map[string]*connect.DescribeQueueOutput
	profiles   []cttypes.RoutingProfileSummary
	profileOut map[string]*connect.DescribeRoutingProfileOutput
	hours      []cttypes.HoursOfOperationSummary
	hoursOut   map[string]*connect.DescribeHoursOfOperationOutput
	statuses   []cttypes.AgentStatusSummary
	statusOut  map[string]*connect.DescribeAgentStatusOutput
	quicks     []cttypes.QuickConnectSummary
	quickOut   map[string]*connect.DescribeQuickConnectOutput
}

func (s *stubConnectRouting) ListQueues(_ context.Context, _ *connect.ListQueuesInput, _ ...func(*connect.Options)) (*connect.ListQueuesOutput, error) {
	return &connect.ListQueuesOutput{QueueSummaryList: s.queues}, nil
}
func (s *stubConnectRouting) DescribeQueue(_ context.Context, in *connect.DescribeQueueInput, _ ...func(*connect.Options)) (*connect.DescribeQueueOutput, error) {
	return s.queueOut[*in.QueueId], nil
}
func (s *stubConnectRouting) ListRoutingProfiles(_ context.Context, _ *connect.ListRoutingProfilesInput, _ ...func(*connect.Options)) (*connect.ListRoutingProfilesOutput, error) {
	return &connect.ListRoutingProfilesOutput{RoutingProfileSummaryList: s.profiles}, nil
}
func (s *stubConnectRouting) DescribeRoutingProfile(_ context.Context, in *connect.DescribeRoutingProfileInput, _ ...func(*connect.Options)) (*connect.DescribeRoutingProfileOutput, error) {
	return s.profileOut[*in.RoutingProfileId], nil
}
func (s *stubConnectRouting) ListHoursOfOperations(_ context.Context, _ *connect.ListHoursOfOperationsInput, _ ...func(*connect.Options)) (*connect.ListHoursOfOperationsOutput, error) {
	return &connect.ListHoursOfOperationsOutput{HoursOfOperationSummaryList: s.hours}, nil
}
func (s *stubConnectRouting) DescribeHoursOfOperation(_ context.Context, in *connect.DescribeHoursOfOperationInput, _ ...func(*connect.Options)) (*connect.DescribeHoursOfOperationOutput, error) {
	return s.hoursOut[*in.HoursOfOperationId], nil
}
func (s *stubConnectRouting) ListAgentStatuses(_ context.Context, _ *connect.ListAgentStatusesInput, _ ...func(*connect.Options)) (*connect.ListAgentStatusesOutput, error) {
	return &connect.ListAgentStatusesOutput{AgentStatusSummaryList: s.statuses}, nil
}
func (s *stubConnectRouting) DescribeAgentStatus(_ context.Context, in *connect.DescribeAgentStatusInput, _ ...func(*connect.Options)) (*connect.DescribeAgentStatusOutput, error) {
	return s.statusOut[*in.AgentStatusId], nil
}
func (s *stubConnectRouting) ListQuickConnects(_ context.Context, _ *connect.ListQuickConnectsInput, _ ...func(*connect.Options)) (*connect.ListQuickConnectsOutput, error) {
	return &connect.ListQuickConnectsOutput{QuickConnectSummaryList: s.quicks}, nil
}
func (s *stubConnectRouting) DescribeQuickConnect(_ context.Context, in *connect.DescribeQuickConnectInput, _ ...func(*connect.Options)) (*connect.DescribeQuickConnectOutput, error) {
	return s.quickOut[*in.QuickConnectId], nil
}

func TestScanConnectRouting(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	instID := "11111111-1111-1111-1111-111111111111"
	instARN := fmt.Sprintf("arn:aws:connect:%s:%s:instance/%s", testRegion, acct.ID, instID)
	instances := []cttypes.InstanceSummary{{Id: &instID, Arn: &instARN}}

	qID := "q-1"
	qARN := fmt.Sprintf("arn:aws:connect:%s:%s:instance/%s/queue/%s", testRegion, acct.ID, instID, qID)
	qName := "q"
	rpID := "rp-1"
	rpARN := fmt.Sprintf("arn:aws:connect:%s:%s:instance/%s/routing-profile/%s", testRegion, acct.ID, instID, rpID)
	rpName := "rp"
	hID := "h-1"
	hARN := fmt.Sprintf("arn:aws:connect:%s:%s:instance/%s/operating-hours/%s", testRegion, acct.ID, instID, hID)
	hName := "h"
	asID := "as-1"
	asARN := fmt.Sprintf("arn:aws:connect:%s:%s:instance/%s/agent-state/%s", testRegion, acct.ID, instID, asID)
	asName := "as"
	qcID := "qc-1"
	qcARN := fmt.Sprintf("arn:aws:connect:%s:%s:instance/%s/transfer-destination/%s", testRegion, acct.ID, instID, qcID)
	qcName := "qc"

	stub := &stubConnectRouting{
		queues:     []cttypes.QueueSummary{{Id: &qID, Arn: &qARN}},
		queueOut:   map[string]*connect.DescribeQueueOutput{qID: {Queue: &cttypes.Queue{QueueArn: &qARN, Name: &qName, Status: cttypes.QueueStatusEnabled}}},
		profiles:   []cttypes.RoutingProfileSummary{{Id: &rpID, Arn: &rpARN}},
		profileOut: map[string]*connect.DescribeRoutingProfileOutput{rpID: {RoutingProfile: &cttypes.RoutingProfile{RoutingProfileArn: &rpARN, Name: &rpName}}},
		hours:      []cttypes.HoursOfOperationSummary{{Id: &hID, Arn: &hARN}},
		hoursOut:   map[string]*connect.DescribeHoursOfOperationOutput{hID: {HoursOfOperation: &cttypes.HoursOfOperation{HoursOfOperationArn: &hARN, Name: &hName}}},
		statuses:   []cttypes.AgentStatusSummary{{Id: &asID, Arn: &asARN}},
		statusOut:  map[string]*connect.DescribeAgentStatusOutput{asID: {AgentStatus: &cttypes.AgentStatus{AgentStatusARN: &asARN, Name: &asName, State: cttypes.AgentStatusStateEnabled}}},
		quicks:     []cttypes.QuickConnectSummary{{Id: &qcID, Arn: &qcARN}},
		quickOut:   map[string]*connect.DescribeQuickConnectOutput{qcID: {QuickConnect: &cttypes.QuickConnect{QuickConnectARN: &qcARN, Name: &qcName}}},
	}

	total, inserted, err := scanConnectRouting(context.Background(), stub, instances, acct, testRegion, st, testScanID)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != 5 || inserted != 5 {
		t.Fatalf("total=%d inserted=%d want 5/5", total, inserted)
	}
	for _, want := range []struct{ typ, id string }{
		{TypeConnectQueue, qARN},
		{TypeConnectRoutingProfile, rpARN},
		{TypeConnectHoursOfOperation, hARN},
		{TypeConnectAgentStatus, asARN},
		{TypeConnectQuickConnect, qcARN},
	} {
		if _, err := st.GetResource(store.ResourceID("aws", acct.ID, want.typ, want.id)); err != nil {
			t.Errorf("%s missing: %v", want.typ, err)
		}
	}
}

func TestScanConnectRoutingEmpty(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	stub := &stubConnectRouting{}
	total, inserted, err := scanConnectRouting(context.Background(), stub, nil, acct, testRegion, st, testScanID)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != 0 || inserted != 0 {
		t.Fatalf("total=%d inserted=%d want 0/0", total, inserted)
	}
}
