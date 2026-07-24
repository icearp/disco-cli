package aws

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/icearp/disco-cli/store"
	"github.com/aws/aws-sdk-go-v2/service/sagemaker"
	smtypes "github.com/aws/aws-sdk-go-v2/service/sagemaker/types"
)

// stubSageMakerStudio satisfies sagemakerStudioAPI for tests: each op
// returns a prepared output or empty page. It does not model paginator
// NextToken — the first NextPage call yields the full slice.
type stubSageMakerStudio struct {
	domains             []smtypes.DomainDetails
	domainOut           map[string]*sagemaker.DescribeDomainOutput
	userProfiles        []smtypes.UserProfileDetails
	userProfileOut      map[string]*sagemaker.DescribeUserProfileOutput
	spaces              []smtypes.SpaceDetails
	spaceOut            map[string]*sagemaker.DescribeSpaceOutput
	apps                []smtypes.AppDetails
	appOut              map[string]*sagemaker.DescribeAppOutput
	appImageConfigs     []smtypes.AppImageConfigDetails
	appImageConfigOut   map[string]*sagemaker.DescribeAppImageConfigOutput
	studioLifecycles    []smtypes.StudioLifecycleConfigDetails
	studioLifecyclesOut map[string]*sagemaker.DescribeStudioLifecycleConfigOutput
}

func (s *stubSageMakerStudio) ListDomains(_ context.Context, _ *sagemaker.ListDomainsInput, _ ...func(*sagemaker.Options)) (*sagemaker.ListDomainsOutput, error) {
	return &sagemaker.ListDomainsOutput{Domains: s.domains}, nil
}

func (s *stubSageMakerStudio) DescribeDomain(_ context.Context, in *sagemaker.DescribeDomainInput, _ ...func(*sagemaker.Options)) (*sagemaker.DescribeDomainOutput, error) {
	return s.domainOut[*in.DomainId], nil
}

func (s *stubSageMakerStudio) ListUserProfiles(_ context.Context, _ *sagemaker.ListUserProfilesInput, _ ...func(*sagemaker.Options)) (*sagemaker.ListUserProfilesOutput, error) {
	return &sagemaker.ListUserProfilesOutput{UserProfiles: s.userProfiles}, nil
}

func (s *stubSageMakerStudio) DescribeUserProfile(_ context.Context, in *sagemaker.DescribeUserProfileInput, _ ...func(*sagemaker.Options)) (*sagemaker.DescribeUserProfileOutput, error) {
	return s.userProfileOut[*in.DomainId+"/"+*in.UserProfileName], nil
}

func (s *stubSageMakerStudio) ListSpaces(_ context.Context, _ *sagemaker.ListSpacesInput, _ ...func(*sagemaker.Options)) (*sagemaker.ListSpacesOutput, error) {
	return &sagemaker.ListSpacesOutput{Spaces: s.spaces}, nil
}

func (s *stubSageMakerStudio) DescribeSpace(_ context.Context, in *sagemaker.DescribeSpaceInput, _ ...func(*sagemaker.Options)) (*sagemaker.DescribeSpaceOutput, error) {
	return s.spaceOut[*in.DomainId+"/"+*in.SpaceName], nil
}

func (s *stubSageMakerStudio) ListApps(_ context.Context, _ *sagemaker.ListAppsInput, _ ...func(*sagemaker.Options)) (*sagemaker.ListAppsOutput, error) {
	return &sagemaker.ListAppsOutput{Apps: s.apps}, nil
}

func (s *stubSageMakerStudio) DescribeApp(_ context.Context, in *sagemaker.DescribeAppInput, _ ...func(*sagemaker.Options)) (*sagemaker.DescribeAppOutput, error) {
	return s.appOut[*in.DomainId+"/"+string(in.AppType)+"/"+*in.AppName], nil
}

func (s *stubSageMakerStudio) ListAppImageConfigs(_ context.Context, _ *sagemaker.ListAppImageConfigsInput, _ ...func(*sagemaker.Options)) (*sagemaker.ListAppImageConfigsOutput, error) {
	return &sagemaker.ListAppImageConfigsOutput{AppImageConfigs: s.appImageConfigs}, nil
}

func (s *stubSageMakerStudio) DescribeAppImageConfig(_ context.Context, in *sagemaker.DescribeAppImageConfigInput, _ ...func(*sagemaker.Options)) (*sagemaker.DescribeAppImageConfigOutput, error) {
	return s.appImageConfigOut[*in.AppImageConfigName], nil
}

func (s *stubSageMakerStudio) ListStudioLifecycleConfigs(_ context.Context, _ *sagemaker.ListStudioLifecycleConfigsInput, _ ...func(*sagemaker.Options)) (*sagemaker.ListStudioLifecycleConfigsOutput, error) {
	return &sagemaker.ListStudioLifecycleConfigsOutput{StudioLifecycleConfigs: s.studioLifecycles}, nil
}

func (s *stubSageMakerStudio) DescribeStudioLifecycleConfig(_ context.Context, in *sagemaker.DescribeStudioLifecycleConfigInput, _ ...func(*sagemaker.Options)) (*sagemaker.DescribeStudioLifecycleConfigOutput, error) {
	return s.studioLifecyclesOut[*in.StudioLifecycleConfigName], nil
}

func TestScanSageMakerStudio(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	now := time.Unix(1700000000, 0).UTC()

	domainID := "d-abc123"
	domainName := "team"
	domainARN := fmt.Sprintf("arn:aws:sagemaker:%s:%s:domain/%s", testRegion, acct.ID, domainID)

	userName := "alice"
	userARN := fmt.Sprintf("arn:aws:sagemaker:%s:%s:user-profile/%s/%s", testRegion, acct.ID, domainID, userName)

	spaceName := "shared"
	spaceARN := fmt.Sprintf("arn:aws:sagemaker:%s:%s:space/%s/%s", testRegion, acct.ID, domainID, spaceName)

	appName := "default"
	appARN := fmt.Sprintf("arn:aws:sagemaker:%s:%s:app/%s/%s/JupyterServer/%s", testRegion, acct.ID, domainID, userName, appName)

	aicName := "config-1"
	aicARN := fmt.Sprintf("arn:aws:sagemaker:%s:%s:app-image-config/%s", testRegion, acct.ID, aicName)

	slcName := "lifecycle-1"
	slcARN := fmt.Sprintf("arn:aws:sagemaker:%s:%s:studio-lifecycle-config/%s", testRegion, acct.ID, slcName)

	stub := &stubSageMakerStudio{
		domains: []smtypes.DomainDetails{{DomainId: &domainID, DomainArn: &domainARN, DomainName: &domainName, CreationTime: &now, Status: smtypes.DomainStatusInService}},
		domainOut: map[string]*sagemaker.DescribeDomainOutput{
			domainID: {DomainArn: &domainARN, DomainId: &domainID, DomainName: &domainName, CreationTime: &now, Status: smtypes.DomainStatusInService},
		},
		userProfiles: []smtypes.UserProfileDetails{{DomainId: &domainID, UserProfileName: &userName, CreationTime: &now, Status: smtypes.UserProfileStatusInService}},
		userProfileOut: map[string]*sagemaker.DescribeUserProfileOutput{
			domainID + "/" + userName: {UserProfileArn: &userARN, UserProfileName: &userName, DomainId: &domainID, CreationTime: &now, Status: smtypes.UserProfileStatusInService},
		},
		spaces: []smtypes.SpaceDetails{{DomainId: &domainID, SpaceName: &spaceName, CreationTime: &now, Status: smtypes.SpaceStatusInService}},
		spaceOut: map[string]*sagemaker.DescribeSpaceOutput{
			domainID + "/" + spaceName: {SpaceArn: &spaceARN, SpaceName: &spaceName, DomainId: &domainID, CreationTime: &now, Status: smtypes.SpaceStatusInService},
		},
		apps: []smtypes.AppDetails{{DomainId: &domainID, AppName: &appName, AppType: smtypes.AppTypeJupyterServer, UserProfileName: &userName, CreationTime: &now, Status: smtypes.AppStatusInService}},
		appOut: map[string]*sagemaker.DescribeAppOutput{
			domainID + "/JupyterServer/" + appName: {AppArn: &appARN, AppName: &appName, AppType: smtypes.AppTypeJupyterServer, DomainId: &domainID, UserProfileName: &userName, CreationTime: &now, Status: smtypes.AppStatusInService},
		},
		appImageConfigs: []smtypes.AppImageConfigDetails{{AppImageConfigName: &aicName, AppImageConfigArn: &aicARN, CreationTime: &now}},
		appImageConfigOut: map[string]*sagemaker.DescribeAppImageConfigOutput{
			aicName: {AppImageConfigArn: &aicARN, AppImageConfigName: &aicName, CreationTime: &now},
		},
		studioLifecycles: []smtypes.StudioLifecycleConfigDetails{{StudioLifecycleConfigName: &slcName, StudioLifecycleConfigArn: &slcARN, CreationTime: &now}},
		studioLifecyclesOut: map[string]*sagemaker.DescribeStudioLifecycleConfigOutput{
			slcName: {StudioLifecycleConfigArn: &slcARN, StudioLifecycleConfigName: &slcName, CreationTime: &now},
		},
	}

	total, inserted, err := scanSageMakerStudio(context.Background(), stub, acct, testRegion, st, testScanID)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != 6 || inserted != 6 {
		t.Fatalf("total=%d inserted=%d want 6/6", total, inserted)
	}
	for _, want := range []struct {
		typ, id string
	}{
		{TypeSageMakerDomain, domainARN},
		{TypeSageMakerUserProfile, userARN},
		{TypeSageMakerSpace, spaceARN},
		{TypeSageMakerApp, appARN},
		{TypeSageMakerAppImageConfig, aicARN},
		{TypeSageMakerStudioLifecycleConfig, slcARN},
	} {
		if _, err := st.GetResource(store.ResourceID("aws", acct.ID, want.id)); err != nil {
			t.Errorf("%s missing: %v", want.typ, err)
		}
	}
}

func TestScanSageMakerStudioEmpty(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	stub := &stubSageMakerStudio{}
	total, inserted, err := scanSageMakerStudio(context.Background(), stub, acct, testRegion, st, testScanID)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != 0 || inserted != 0 {
		t.Fatalf("total=%d inserted=%d want 0/0", total, inserted)
	}
}
