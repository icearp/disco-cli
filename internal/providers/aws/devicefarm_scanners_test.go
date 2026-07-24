package aws

import (
	"context"
	"testing"

	"github.com/icearp/disco-cli/internal/util"
	"github.com/icearp/disco-cli/store"
	"github.com/aws/aws-sdk-go-v2/service/devicefarm"
	dftypes "github.com/aws/aws-sdk-go-v2/service/devicefarm/types"
)

type stubDeviceFarm struct {
	projects     []dftypes.Project
	pools        []dftypes.DevicePool
	netProfiles  []dftypes.NetworkProfile
	instProfiles []dftypes.InstanceProfile
	devInstances []dftypes.DeviceInstance
	vpces        []dftypes.VPCEConfiguration
	testGrids    []dftypes.TestGridProject
}

func (s *stubDeviceFarm) ListProjects(_ context.Context, _ *devicefarm.ListProjectsInput, _ ...func(*devicefarm.Options)) (*devicefarm.ListProjectsOutput, error) {
	return &devicefarm.ListProjectsOutput{Projects: s.projects}, nil
}

func (s *stubDeviceFarm) ListDevicePools(_ context.Context, _ *devicefarm.ListDevicePoolsInput, _ ...func(*devicefarm.Options)) (*devicefarm.ListDevicePoolsOutput, error) {
	return &devicefarm.ListDevicePoolsOutput{DevicePools: s.pools}, nil
}

func (s *stubDeviceFarm) ListNetworkProfiles(_ context.Context, _ *devicefarm.ListNetworkProfilesInput, _ ...func(*devicefarm.Options)) (*devicefarm.ListNetworkProfilesOutput, error) {
	return &devicefarm.ListNetworkProfilesOutput{NetworkProfiles: s.netProfiles}, nil
}

func (s *stubDeviceFarm) ListInstanceProfiles(_ context.Context, _ *devicefarm.ListInstanceProfilesInput, _ ...func(*devicefarm.Options)) (*devicefarm.ListInstanceProfilesOutput, error) {
	return &devicefarm.ListInstanceProfilesOutput{InstanceProfiles: s.instProfiles}, nil
}

func (s *stubDeviceFarm) ListDeviceInstances(_ context.Context, _ *devicefarm.ListDeviceInstancesInput, _ ...func(*devicefarm.Options)) (*devicefarm.ListDeviceInstancesOutput, error) {
	return &devicefarm.ListDeviceInstancesOutput{DeviceInstances: s.devInstances}, nil
}

func (s *stubDeviceFarm) ListVPCEConfigurations(_ context.Context, _ *devicefarm.ListVPCEConfigurationsInput, _ ...func(*devicefarm.Options)) (*devicefarm.ListVPCEConfigurationsOutput, error) {
	return &devicefarm.ListVPCEConfigurationsOutput{VpceConfigurations: s.vpces}, nil
}

func (s *stubDeviceFarm) ListTestGridProjects(_ context.Context, _ *devicefarm.ListTestGridProjectsInput, _ ...func(*devicefarm.Options)) (*devicefarm.ListTestGridProjectsOutput, error) {
	return &devicefarm.ListTestGridProjectsOutput{TestGridProjects: s.testGrids}, nil
}

func TestScanDeviceFarmPhases(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	region := "us-west-2"
	projARN := "arn:aws:devicefarm:us-west-2:111111111111:project:PROJ-1"
	stub := &stubDeviceFarm{
		projects:     []dftypes.Project{{Arn: ptrStr(projARN), Name: ptrStr("p1")}},
		pools:        []dftypes.DevicePool{{Arn: ptrStr(projARN + "/POOL-1"), Name: ptrStr("pool1")}},
		instProfiles: []dftypes.InstanceProfile{{Arn: ptrStr("arn:aws:devicefarm:us-west-2:111111111111:instanceprofile:IP-1"), Name: ptrStr("ip1")}},
		testGrids:    []dftypes.TestGridProject{{Arn: ptrStr("arn:aws:devicefarm:us-west-2:111111111111:testgrid-project:TG-1"), Name: ptrStr("tg1")}},
	}

	arns, _, _, err := scanDeviceFarmProjects(context.Background(), stub, acct, region, st, testScanID)
	if err != nil || len(arns) != 1 {
		t.Fatalf("scanDeviceFarmProjects: arns=%v err=%v", arns, err)
	}
	if _, _, err := scanDeviceFarmDevicePools(context.Background(), stub, acct, region, st, testScanID, arns[0]); err != nil {
		t.Fatalf("scanDeviceFarmDevicePools: %v", err)
	}
	if _, _, err := scanDeviceFarmInstanceProfiles(context.Background(), stub, acct, region, st, testScanID); err != nil {
		t.Fatalf("scanDeviceFarmInstanceProfiles: %v", err)
	}
	if _, _, err := scanDeviceFarmTestGridProjects(context.Background(), stub, acct, region, st, testScanID); err != nil {
		t.Fatalf("scanDeviceFarmTestGridProjects: %v", err)
	}
	for _, typ := range []string{TypeDeviceFarmProject, TypeDeviceFarmDevicePool, TypeDeviceFarmInstanceProfile, TypeDeviceFarmTestGridProject} {
		rows, err := st.ListResources(store.ResourceFilter{Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{typ}, Limit: util.AllResources})
		if err != nil {
			t.Fatalf("ListResources %s: %v", typ, err)
		}
		if len(rows) != 1 {
			t.Errorf("%s: got %d rows, want 1", typ, len(rows))
		}
	}
}
