package aws

import (
	"context"
	"fmt"
	"testing"

	"codeberg.org/icearp/disco/store"
	"github.com/aws/aws-sdk-go-v2/service/backupgateway"
	bgwtypes "github.com/aws/aws-sdk-go-v2/service/backupgateway/types"
)

type stubBackupGateway struct {
	hyps []bgwtypes.Hypervisor
}

func (s *stubBackupGateway) ListHypervisors(_ context.Context, _ *backupgateway.ListHypervisorsInput, _ ...func(*backupgateway.Options)) (*backupgateway.ListHypervisorsOutput, error) {
	return &backupgateway.ListHypervisorsOutput{Hypervisors: s.hyps}, nil
}

func (s *stubBackupGateway) ListGateways(_ context.Context, _ *backupgateway.ListGatewaysInput, _ ...func(*backupgateway.Options)) (*backupgateway.ListGatewaysOutput, error) {
	return &backupgateway.ListGatewaysOutput{}, nil
}

func (s *stubBackupGateway) ListVirtualMachines(_ context.Context, _ *backupgateway.ListVirtualMachinesInput, _ ...func(*backupgateway.Options)) (*backupgateway.ListVirtualMachinesOutput, error) {
	return &backupgateway.ListVirtualMachinesOutput{}, nil
}

func TestScanBackupGatewayHypervisors(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	arn := fmt.Sprintf("arn:aws:backup-gateway:%s:%s:hypervisor/hyp-1", testRegion, acct.ID)
	name := "vc1"
	stub := &stubBackupGateway{
		hyps: []bgwtypes.Hypervisor{
			{HypervisorArn: &arn, Name: &name, State: bgwtypes.HypervisorStateOnline},
		},
	}

	total, _, err := scanBackupGatewayHypervisors(context.Background(), stub, acct, testRegion, st, testScanID)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != 1 {
		t.Fatalf("total=%d want 1", total)
	}
	if _, err := st.GetResource(store.ResourceID("aws", acct.ID, TypeBackupGatewayHypervisor, arn)); err != nil {
		t.Errorf("hypervisor missing: %v", err)
	}
}
