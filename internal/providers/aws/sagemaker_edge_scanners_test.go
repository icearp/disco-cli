package aws

import (
	"context"
	"fmt"
	"testing"
	"time"

	"codeberg.org/icearp/disco/store"
	"github.com/aws/aws-sdk-go-v2/service/sagemaker"
	smtypes "github.com/aws/aws-sdk-go-v2/service/sagemaker/types"
)

type stubSageMakerEdge struct {
	fleets        []smtypes.DeviceFleetSummary
	fleetOut      map[string]*sagemaker.DescribeDeviceFleetOutput
	devices       []smtypes.DeviceSummary
	deviceOut     map[string]*sagemaker.DescribeDeviceOutput
	images        []smtypes.Image
	imageOut      map[string]*sagemaker.DescribeImageOutput
	versionsByImg map[string][]smtypes.ImageVersion
	versionOut    map[string]*sagemaker.DescribeImageVersionOutput
}

func (s *stubSageMakerEdge) ListDeviceFleets(_ context.Context, _ *sagemaker.ListDeviceFleetsInput, _ ...func(*sagemaker.Options)) (*sagemaker.ListDeviceFleetsOutput, error) {
	return &sagemaker.ListDeviceFleetsOutput{DeviceFleetSummaries: s.fleets}, nil
}

func (s *stubSageMakerEdge) DescribeDeviceFleet(_ context.Context, in *sagemaker.DescribeDeviceFleetInput, _ ...func(*sagemaker.Options)) (*sagemaker.DescribeDeviceFleetOutput, error) {
	return s.fleetOut[*in.DeviceFleetName], nil
}

func (s *stubSageMakerEdge) ListDevices(_ context.Context, _ *sagemaker.ListDevicesInput, _ ...func(*sagemaker.Options)) (*sagemaker.ListDevicesOutput, error) {
	return &sagemaker.ListDevicesOutput{DeviceSummaries: s.devices}, nil
}

func (s *stubSageMakerEdge) DescribeDevice(_ context.Context, in *sagemaker.DescribeDeviceInput, _ ...func(*sagemaker.Options)) (*sagemaker.DescribeDeviceOutput, error) {
	return s.deviceOut[*in.DeviceFleetName+"/"+*in.DeviceName], nil
}

func (s *stubSageMakerEdge) ListImages(_ context.Context, _ *sagemaker.ListImagesInput, _ ...func(*sagemaker.Options)) (*sagemaker.ListImagesOutput, error) {
	return &sagemaker.ListImagesOutput{Images: s.images}, nil
}

func (s *stubSageMakerEdge) DescribeImage(_ context.Context, in *sagemaker.DescribeImageInput, _ ...func(*sagemaker.Options)) (*sagemaker.DescribeImageOutput, error) {
	return s.imageOut[*in.ImageName], nil
}

func (s *stubSageMakerEdge) ListImageVersions(_ context.Context, in *sagemaker.ListImageVersionsInput, _ ...func(*sagemaker.Options)) (*sagemaker.ListImageVersionsOutput, error) {
	return &sagemaker.ListImageVersionsOutput{ImageVersions: s.versionsByImg[*in.ImageName]}, nil
}

func (s *stubSageMakerEdge) DescribeImageVersion(_ context.Context, in *sagemaker.DescribeImageVersionInput, _ ...func(*sagemaker.Options)) (*sagemaker.DescribeImageVersionOutput, error) {
	return s.versionOut[fmt.Sprintf("%s/%d", *in.ImageName, *in.Version)], nil
}

func TestScanSageMakerEdge(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	now := time.Unix(1700000000, 0).UTC()

	fleetName := "fleet-1"
	fleetARN := fmt.Sprintf("arn:aws:sagemaker:%s:%s:device-fleet/%s", testRegion, acct.ID, fleetName)
	devName := "dev-1"
	devARN := fmt.Sprintf("arn:aws:sagemaker:%s:%s:device-fleet/%s/device/%s", testRegion, acct.ID, fleetName, devName)
	imgName := "img-1"
	imgARN := fmt.Sprintf("arn:aws:sagemaker:%s:%s:image/%s", testRegion, acct.ID, imgName)
	verVal := int32(1)
	verARN := fmt.Sprintf("arn:aws:sagemaker:%s:%s:image-version/%s/1", testRegion, acct.ID, imgName)

	stub := &stubSageMakerEdge{
		fleets: []smtypes.DeviceFleetSummary{{DeviceFleetArn: &fleetARN, DeviceFleetName: &fleetName, CreationTime: &now}},
		fleetOut: map[string]*sagemaker.DescribeDeviceFleetOutput{
			fleetName: {DeviceFleetArn: &fleetARN, DeviceFleetName: &fleetName, CreationTime: &now},
		},
		devices: []smtypes.DeviceSummary{{DeviceArn: &devARN, DeviceFleetName: &fleetName, DeviceName: &devName}},
		deviceOut: map[string]*sagemaker.DescribeDeviceOutput{
			fleetName + "/" + devName: {DeviceArn: &devARN, DeviceFleetName: &fleetName, DeviceName: &devName, RegistrationTime: &now},
		},
		images: []smtypes.Image{{ImageArn: &imgARN, ImageName: &imgName, CreationTime: &now}},
		imageOut: map[string]*sagemaker.DescribeImageOutput{
			imgName: {ImageArn: &imgARN, ImageName: &imgName, ImageStatus: smtypes.ImageStatusCreated, CreationTime: &now},
		},
		versionsByImg: map[string][]smtypes.ImageVersion{
			imgName: {{ImageVersionArn: &verARN, ImageArn: &imgARN, Version: &verVal, CreationTime: &now}},
		},
		versionOut: map[string]*sagemaker.DescribeImageVersionOutput{
			imgName + "/1": {ImageVersionArn: &verARN, ImageArn: &imgARN, Version: &verVal, ImageVersionStatus: smtypes.ImageVersionStatusCreated, CreationTime: &now},
		},
	}

	total, inserted, err := scanSageMakerEdge(context.Background(), stub, acct, testRegion, st, testScanID)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != 4 || inserted != 4 {
		t.Fatalf("total=%d inserted=%d want 4/4", total, inserted)
	}
	for _, want := range []struct{ typ, id string }{
		{TypeSageMakerDeviceFleet, fleetARN},
		{TypeSageMakerDevice, devARN},
		{TypeSageMakerImage, imgARN},
		{TypeSageMakerImageVersion, verARN},
	} {
		if _, err := st.GetResource(store.ResourceID("aws", acct.ID, want.typ, want.id)); err != nil {
			t.Errorf("%s missing: %v", want.typ, err)
		}
	}
}

func TestScanSageMakerEdgeEmpty(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	stub := &stubSageMakerEdge{}
	total, inserted, err := scanSageMakerEdge(context.Background(), stub, acct, testRegion, st, testScanID)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != 0 || inserted != 0 {
		t.Fatalf("total=%d inserted=%d want 0/0", total, inserted)
	}
}
