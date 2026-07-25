package aws

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/appflow"
	appflowtypes "github.com/aws/aws-sdk-go-v2/service/appflow/types"
	"github.com/icearp/disco-cli/store"
)

// stubAppFlow is a deterministic in-memory replacement for appflowAPI used
// by the AppFlow scanner tests. Each method returns a single page of canned
// data; pagination across multiple pages is exercised via the page slices.
type stubAppFlow struct {
	flowsPages         []*appflow.ListFlowsOutput
	flowsCallIdx       int
	connectorsPages    []*appflow.ListConnectorsOutput
	connectorsCallIdx  int
	profilesPages      []*appflow.DescribeConnectorProfilesOutput
	profilesCallIdx    int
	listFlowsErr       error
	listConnectorsErr  error
	describeProfileErr error
}

func (s *stubAppFlow) ListFlows(_ context.Context, _ *appflow.ListFlowsInput, _ ...func(*appflow.Options)) (*appflow.ListFlowsOutput, error) {
	if s.listFlowsErr != nil {
		return nil, s.listFlowsErr
	}
	if s.flowsCallIdx >= len(s.flowsPages) {
		return &appflow.ListFlowsOutput{}, nil
	}
	out := s.flowsPages[s.flowsCallIdx]
	s.flowsCallIdx++
	return out, nil
}

func (s *stubAppFlow) ListConnectors(_ context.Context, _ *appflow.ListConnectorsInput, _ ...func(*appflow.Options)) (*appflow.ListConnectorsOutput, error) {
	if s.listConnectorsErr != nil {
		return nil, s.listConnectorsErr
	}
	if s.connectorsCallIdx >= len(s.connectorsPages) {
		return &appflow.ListConnectorsOutput{}, nil
	}
	out := s.connectorsPages[s.connectorsCallIdx]
	s.connectorsCallIdx++
	return out, nil
}

func (s *stubAppFlow) DescribeConnectorProfiles(_ context.Context, _ *appflow.DescribeConnectorProfilesInput, _ ...func(*appflow.Options)) (*appflow.DescribeConnectorProfilesOutput, error) {
	if s.describeProfileErr != nil {
		return nil, s.describeProfileErr
	}
	if s.profilesCallIdx >= len(s.profilesPages) {
		return &appflow.DescribeConnectorProfilesOutput{}, nil
	}
	out := s.profilesPages[s.profilesCallIdx]
	s.profilesCallIdx++
	return out, nil
}

func TestAppFlowConnectorNativeID(t *testing.T) {
	got := appflowConnectorNativeID("us-east-1", "111111111111", "MyCustom")
	want := "arn:aws:appflow:us-east-1:111111111111:connector/MyCustom"
	if got != want {
		t.Errorf("appflowConnectorNativeID = %q, want %q", got, want)
	}
}

func TestScanAppFlowConnectors(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	region := testRegion

	label := "MyCustomConnector"
	stub := &stubAppFlow{
		connectorsPages: []*appflow.ListConnectorsOutput{
			{Connectors: []appflowtypes.ConnectorDetail{{ConnectorLabel: &label}}},
		},
	}
	total, inserted, err := scanAppFlowConnectors(context.Background(), stub, acct, region, st, testScanID)
	if err != nil {
		t.Fatalf("scanAppFlowConnectors: %v", err)
	}
	if total != 1 || inserted != 1 {
		t.Errorf("total=%d inserted=%d, want 1/1", total, inserted)
	}
	want := appflowConnectorNativeID(region, acct.ID, label)
	rows, err := st.ListResources(store.ResourceFilter{Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeAppFlowConnector}, Limit: 100})
	if err != nil {
		t.Fatalf("ListResources: %v", err)
	}
	if len(rows) != 1 || rows[0].NativeID != want {
		t.Errorf("rows=%+v, want one row with NativeID %s", rows, want)
	}
}

func TestScanAppFlowConnectorsEmpty(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	stub := &stubAppFlow{}
	total, inserted, err := scanAppFlowConnectors(context.Background(), stub, acct, testRegion, st, testScanID)
	if err != nil {
		t.Fatalf("scanAppFlowConnectors: %v", err)
	}
	if total != 0 || inserted != 0 {
		t.Errorf("total=%d inserted=%d, want 0/0", total, inserted)
	}
}

func TestScanAppFlowConnectorProfiles(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	region := testRegion

	arn := "arn:aws:appflow:us-east-1:111111111111:connectorprofile/SfdcProf"
	name := "SfdcProf"
	credARN := "arn:aws:secretsmanager:us-east-1:111111111111:secret:appflow!my-secret-AbCdEf"
	stub := &stubAppFlow{
		profilesPages: []*appflow.DescribeConnectorProfilesOutput{
			{ConnectorProfileDetails: []appflowtypes.ConnectorProfile{{
				ConnectorProfileArn:  &arn,
				ConnectorProfileName: &name,
				CredentialsArn:       &credARN,
			}}},
		},
	}
	total, inserted, err := scanAppFlowConnectorProfiles(context.Background(), stub, acct, region, st, testScanID)
	if err != nil {
		t.Fatalf("scanAppFlowConnectorProfiles: %v", err)
	}
	if total != 1 || inserted != 1 {
		t.Errorf("total=%d inserted=%d, want 1/1", total, inserted)
	}
	rows, err := st.ListResources(store.ResourceFilter{Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeAppFlowConnectorProfile}, Limit: 100})
	if err != nil {
		t.Fatalf("ListResources: %v", err)
	}
	if len(rows) != 1 || rows[0].NativeID != arn {
		t.Errorf("rows=%+v, want one row with NativeID %s", rows, arn)
	}
}
