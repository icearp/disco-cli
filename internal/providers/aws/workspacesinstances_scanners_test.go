package aws

import (
	"context"
	"testing"

	"codeberg.org/icearp/disco/store"
	"github.com/aws/aws-sdk-go-v2/service/workspacesinstances"
	wsitypes "github.com/aws/aws-sdk-go-v2/service/workspacesinstances/types"
)

type stubWorkspacesInstances struct {
	regions       []string
	instances     []wsitypes.WorkspaceInstance
	listInstCalls int
}

func (s *stubWorkspacesInstances) ListRegions(_ context.Context, _ *workspacesinstances.ListRegionsInput, _ ...func(*workspacesinstances.Options)) (*workspacesinstances.ListRegionsOutput, error) {
	out := &workspacesinstances.ListRegionsOutput{}
	for i := range s.regions {
		out.Regions = append(out.Regions, wsitypes.Region{RegionName: &s.regions[i]})
	}
	return out, nil
}

func (s *stubWorkspacesInstances) ListWorkspaceInstances(_ context.Context, _ *workspacesinstances.ListWorkspaceInstancesInput, _ ...func(*workspacesinstances.Options)) (*workspacesinstances.ListWorkspaceInstancesOutput, error) {
	s.listInstCalls++
	return &workspacesinstances.ListWorkspaceInstancesOutput{WorkspaceInstances: s.instances}, nil
}

func TestScanWorkspacesInstancesIn_EnabledRegionScans(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	instID := "wsinst-abc123"
	stub := &stubWorkspacesInstances{
		instances: []wsitypes.WorkspaceInstance{
			{WorkspaceInstanceId: &instID, ProvisionState: wsitypes.ProvisionStateEnumAllocated},
		},
	}
	enabled := map[string]bool{"us-east-1": true}

	total, inserted, err := scanWorkspacesInstancesIn(context.Background(), stub, enabled, acct, "us-east-1", st, testScanID)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != 1 || inserted != 1 {
		t.Fatalf("total=%d inserted=%d, want 1/1", total, inserted)
	}
	if stub.listInstCalls != 1 {
		t.Fatalf("ListWorkspaceInstances calls = %d, want 1", stub.listInstCalls)
	}
	arn := "arn:aws:workspaces-instances:us-east-1:" + acct.ID + ":workspace-instance/" + instID
	if _, err := st.GetResource(store.ResourceID("aws", acct.ID, arn)); err != nil {
		t.Errorf("instance row missing: %v", err)
	}
}

func TestScanWorkspacesInstancesIn_DisabledRegionSkips(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	stub := &stubWorkspacesInstances{}
	enabled := map[string]bool{"us-east-1": true} // ap-northeast-3 absent

	total, inserted, err := scanWorkspacesInstancesIn(context.Background(), stub, enabled, acct, "ap-northeast-3", st, testScanID)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != 0 || inserted != 0 {
		t.Fatalf("total=%d inserted=%d, want 0/0", total, inserted)
	}
	if stub.listInstCalls != 0 {
		t.Fatalf("ListWorkspaceInstances must not be called in a disabled region, got %d calls", stub.listInstCalls)
	}
}

func TestScanWorkspacesInstancesIn_EmptyEnabledSetSkipsAll(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	stub := &stubWorkspacesInstances{}
	enabled := map[string]bool{} // service enabled nowhere

	for _, region := range []string{"us-east-1", "eu-west-1", "ap-south-1"} {
		total, inserted, err := scanWorkspacesInstancesIn(context.Background(), stub, enabled, acct, region, st, testScanID)
		if err != nil || total != 0 || inserted != 0 {
			t.Fatalf("region %s: total=%d inserted=%d err=%v, want 0/0/nil", region, total, inserted, err)
		}
	}
	if stub.listInstCalls != 0 {
		t.Fatalf("ListWorkspaceInstances must not be called, got %d calls", stub.listInstCalls)
	}
}

func TestListWorkspacesInstancesRegions(t *testing.T) {
	stub := &stubWorkspacesInstances{regions: []string{"us-east-1", "eu-west-1"}}
	set, err := listWorkspacesInstancesRegions(context.Background(), stub)
	if err != nil {
		t.Fatalf("list regions: %v", err)
	}
	if !set["us-east-1"] || !set["eu-west-1"] {
		t.Errorf("expected both regions in set, got %v", set)
	}
	if set["ap-northeast-3"] {
		t.Error("ap-northeast-3 should be absent")
	}
}
