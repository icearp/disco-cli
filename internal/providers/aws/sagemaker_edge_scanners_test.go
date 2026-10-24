package aws

import (
	"context"
	"fmt"
	"maps"
	"testing"
	"time"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/sagemaker"
	smtypes "github.com/aws/aws-sdk-go-v2/service/sagemaker/types"
	"github.com/icearp/disco-cli/store"
)

type stubSageMakerEdge struct {
	*smTags
	fleets        []smtypes.DeviceFleetSummary
	fleetOut      map[string]*sagemaker.DescribeDeviceFleetOutput
	devices       []smtypes.DeviceSummary
	deviceOut     map[string]*sagemaker.DescribeDeviceOutput
	images        []smtypes.Image
	imageOut      map[string]*sagemaker.DescribeImageOutput
	versionsByImg map[string][]smtypes.ImageVersion
	versionOut    map[string]*sagemaker.DescribeImageVersionOutput
	aliases       map[string][]string
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

func (s *stubSageMakerEdge) ListAliases(_ context.Context, in *sagemaker.ListAliasesInput, _ ...func(*sagemaker.Options)) (*sagemaker.ListAliasesOutput, error) {
	return &sagemaker.ListAliasesOutput{SageMakerImageVersionAliases: s.aliases[*in.ImageName]}, nil
}

func TestScanSageMakerEdge(t *testing.T) {
	for _, mode := range smTagModes {
		t.Run(mode.String(), func(t *testing.T) { testScanSageMakerEdge(t, mode) })
	}
}

// testScanSageMakerEdge stores one row of each type the family lists and reads
// the tags of each one ListTags accepts, as mode says.
func testScanSageMakerEdge(t *testing.T, mode smTagMode) {
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
		aliases: map[string][]string{imgName: {"latest"}},
	}

	stub.smTags = newSMTags(mode, fleetARN, devARN, imgARN)
	total, inserted, err := scanSageMakerEdge(context.Background(), stub, acct, testRegion, st, testScanID)
	checkSMScanErr(t, err, mode)
	if total != 5 || inserted != 5 {
		t.Fatalf("total=%d inserted=%d want 5/5", total, inserted)
	}
	for _, want := range []struct{ typ, id string }{
		{TypeSageMakerDeviceFleet, fleetARN},
		{TypeSageMakerDevice, devARN},
		{TypeSageMakerImage, imgARN},
		{TypeSageMakerImageVersion, verARN},
		{TypeSageMakerAlias, imgARN + "/alias/latest"},
	} {
		if _, err := st.GetResource(store.ResourceID("aws", acct.ID, want.id)); err != nil {
			t.Errorf("%s missing: %v", want.typ, err)
		}
	}
	assertSMTags(t, st, stub.smTags, mode, fleetARN, devARN, imgARN)
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

// --- image aliases ---------------------------------------------------------

type stubSMAliases struct {
	sagemakerEdgeAPI
	pages map[string][][]string
	errs  map[string]error
}

func (s *stubSMAliases) ListAliases(_ context.Context, in *sagemaker.ListAliasesInput, _ ...func(*sagemaker.Options)) (*sagemaker.ListAliasesOutput, error) {
	if err := s.errs[*in.ImageName]; err != nil {
		return nil, err
	}
	items, next, err := smPage(s.pages[*in.ImageName], in.NextToken)
	if err != nil {
		return nil, err
	}
	return &sagemaker.ListAliasesOutput{SageMakerImageVersionAliases: items, NextToken: next}, nil
}

func smImageARN(name string) string {
	return "arn:aws:sagemaker:us-east-1:123456789012:image/" + name
}

func TestScanSageMakerImageAliases_PaginatesEachScannedImage(t *testing.T) {
	st := newTestStore(t)
	warns := recordWarnings(st)
	upsertTestResourceNamed(t, st, TypeSageMakerImage, smImageARN("nameless"), testRegion, "{}", "")
	img1, img2 := smImageARN("img1"), smImageARN("img2")
	img1ID := upsertTestResourceNamed(t, st, TypeSageMakerImage, img1, testRegion, "{}", "img1")
	img2ID := upsertTestResourceNamed(t, st, TypeSageMakerImage, img2, testRegion, "{}", "img2")
	upsertTestResourceNamed(t, st, TypeSageMakerImage, smImageARN("gone"), testRegion, "{}", "gone")
	stub := &stubSMAliases{
		pages: map[string][][]string{
			"img1": {{"latest"}, {"stable", ""}},
			"img2": {{"latest"}},
		},
		errs: map[string]error{"gone": apiErr("ResourceNotFound", "image not found")},
	}
	total, _, err := scanSageMakerImageAliases(context.Background(), stub, newTestAccount(testAccountID), testRegion, st, testScanID)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != 3 {
		t.Errorf("total = %d; want 3", total)
	}
	assertSMIDs(t, st, TypeSageMakerAlias, img1+"/alias/latest", img1+"/alias/stable", img2+"/alias/latest")
	assertSMContains(t, st, img1ID, img1+"/alias/stable")
	assertSMContains(t, st, img2ID, img2+"/alias/latest")
	// One for the empty alias, one for the image without a name.
	if got, want := smWarnServices(*warns), map[string]int{"sagemaker:ListAliases": 2}; !maps.Equal(got, want) {
		t.Errorf("warnings by op = %v; want %v", got, want)
	}
}

func TestScanSageMakerImageAliases_NoAliases(t *testing.T) {
	st := newTestStore(t)
	upsertTestResourceNamed(t, st, TypeSageMakerImage, smImageARN("img1"), testRegion, "{}", "img1")
	total, _, err := scanSageMakerImageAliases(context.Background(), &stubSMAliases{}, newTestAccount(testAccountID), testRegion, st, testScanID)
	if err != nil || total != 0 {
		t.Fatalf("scan = (%d, %v); want (0, nil)", total, err)
	}
}

func TestScanSageMakerImageAliases_AccessDeniedWarns(t *testing.T) {
	st := newTestStore(t)
	warnings := countSMWarnings(st)
	upsertTestResourceNamed(t, st, TypeSageMakerImage, smImageARN("img1"), testRegion, "{}", "img1")
	stub := &stubSMAliases{errs: map[string]error{"img1": apiErr("AccessDeniedException", "denied")}}
	total, _, err := scanSageMakerImageAliases(context.Background(), stub, newTestAccount(testAccountID), testRegion, st, testScanID)
	if err != nil || total != 0 {
		t.Fatalf("scan = (%d, %v); want (0, nil)", total, err)
	}
	if *warnings != 1 {
		t.Errorf("warnings = %d; want 1", *warnings)
	}
}

// --- image versions ------------------------------------------------------------

// smImageVersionClient serves ListImageVersions one version per image, or
// errs[image], and DescribeImageVersion the version's ARN or describeErr. It
// fails the test on ListImages: versions are read for the stored images.
func smImageVersionClient(t *testing.T, errs map[string]error, describeErr error) sagemakerClient {
	return newSageMakerClient(opStubConfig(map[string]func(in any) (any, error){
		"ListImages": func(any) (any, error) {
			t.Error("ListImages called; want the images this scan stored")
			return &sagemaker.ListImagesOutput{}, nil
		},
		"ListImageVersions": func(in any) (any, error) {
			image := sdkaws.ToString(in.(*sagemaker.ListImageVersionsInput).ImageName)
			if err := errs[image]; err != nil {
				return nil, err
			}
			return &sagemaker.ListImageVersionsOutput{ImageVersions: []smtypes.ImageVersion{{
				Version: sdkaws.Int32(1), ImageVersionArn: sdkaws.String(smTestARN("image-version", image+"/1")),
			}}}, nil
		},
		"DescribeImageVersion": func(in any) (any, error) {
			if describeErr != nil {
				return nil, describeErr
			}
			image := sdkaws.ToString(in.(*sagemaker.DescribeImageVersionInput).ImageName)
			return &sagemaker.DescribeImageVersionOutput{ImageVersionArn: sdkaws.String(smTestARN("image-version", image+"/1"))}, nil
		},
	}, apiErr("AccessDeniedException", "op not stubbed")), testRegion)
}

// An image whose versions could not be listed keeps the other images'
// versions: a vanished image is skipped silently, a denied or nameless one is
// warned about, and any other failure is returned.
func TestScanSageMakerImageVersions_FailedImageKeepsOthers(t *testing.T) {
	st := newTestStore(t)
	warns := recordWarnings(st)
	for _, name := range []string{"ok", "denied", "gone", "bad", ""} {
		upsertTestResourceNamed(t, st, TypeSageMakerImage, smImageARN("img-"+name), testRegion, "{}", name)
	}
	client := smImageVersionClient(t, map[string]error{
		"denied": apiErr("AccessDeniedException", "denied"),
		"gone":   apiErr("ResourceNotFound", "gone"),
		"bad":    apiErr("ValidationException", "bad"),
	}, nil)
	_, _, err := scanSageMakerImageVersions(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID)
	assertAPICode(t, err, "ValidationException")
	if isAPIErrorCode(err, "AccessDeniedException", "ResourceNotFound") {
		t.Errorf("err = %v; want neither the denial nor the vanished image", err)
	}
	assertSMIDs(t, st, TypeSageMakerImageVersion, smTestARN("image-version", "ok/1"))
	if got, want := smWarnServices(*warns), map[string]int{"sagemaker:ListImageVersions": 2}; !maps.Equal(got, want) {
		t.Errorf("warnings by op = %v; want %v", got, want)
	}
}

// An image whose versions listing is still throttled after the SDK's retries
// is returned, and the other images' versions are kept.
func TestScanSageMakerImageVersions_ThrottledImageReturned(t *testing.T) {
	st := newTestStore(t)
	for _, name := range []string{"ok", "slow"} {
		upsertTestResourceNamed(t, st, TypeSageMakerImage, smImageARN(name), testRegion, "{}", name)
	}
	client := smImageVersionClient(t, map[string]error{"slow": apiErr("ThrottlingException", "Rate exceeded")}, nil)
	_, _, err := scanSageMakerImageVersions(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID)
	if err == nil || !everyMember(err, func(m error) bool { return isAPIErrorCode(m, "ThrottlingException") }) {
		t.Errorf("err = %v; want only the throttled listing", err)
	}
	assertSMIDs(t, st, TypeSageMakerImageVersion, smTestARN("image-version", "ok/1"))
}

// A version whose describe is denied is stored from its listing, named
// image:version.
func TestScanSageMakerImageVersions_DeniedDescribeKeptFromListing(t *testing.T) {
	st := newTestStore(t)
	upsertTestResourceNamed(t, st, TypeSageMakerImage, smImageARN("ok"), testRegion, "{}", "ok")
	client := smImageVersionClient(t, nil, apiErr("AccessDeniedException", "denied"))
	if _, _, err := scanSageMakerImageVersions(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID); err != nil {
		t.Fatalf("scan: %v", err)
	}
	arn := smTestARN("image-version", "ok/1")
	r, ok := rowsByNativeID(t, st, TypeSageMakerImageVersion)[arn]
	if !ok {
		t.Fatalf("%s not stored", arn)
	}
	if got := sv(r.Name); got != "ok:1" {
		t.Errorf("name = %q; want ok:1", got)
	}
}

// A version's describe failing with a hard error is returned, and the version
// is still stored from its listing.
func TestScanSageMakerImageVersions_HardDescribeErrorReturned(t *testing.T) {
	st := newTestStore(t)
	upsertTestResourceNamed(t, st, TypeSageMakerImage, smImageARN("ok"), testRegion, "{}", "ok")
	client := smImageVersionClient(t, nil, apiErr("ValidationException", "bad"))
	_, _, err := scanSageMakerImageVersions(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID)
	assertAPICode(t, err, "ValidationException")
	assertSMIDs(t, st, TypeSageMakerImageVersion, smTestARN("image-version", "ok/1"))
}
