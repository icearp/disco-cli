package aws

import (
	"context"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"

	"github.com/aws/aws-sdk-go-v2/service/sagemaker"
	smtypes "github.com/aws/aws-sdk-go-v2/service/sagemaker/types"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeSageMakerDomain, Service: "sagemaker"})
	registerType(restype.Descriptor{Type: TypeSageMakerUserProfile, Service: "sagemaker"})
	registerType(restype.Descriptor{Type: TypeSageMakerSpace, Service: "sagemaker"})
	registerType(restype.Descriptor{Type: TypeSageMakerApp, Service: "sagemaker"})
	registerType(restype.Descriptor{Type: TypeSageMakerAppImageConfig, Service: "sagemaker"})
	registerType(restype.Descriptor{Type: TypeSageMakerStudioLifecycleConfig, Service: "sagemaker"})
}

// sagemakerStudioAPI is the narrow surface used by SageMaker Studio
// sub-phases. Each phase List+fan-out Describe so attrs carry the full
// Describe body — list summaries omit ARNs for user-profile/space/app and
// lack edge-bearing fields (VPC/SG/role/EFS settings) on every type.
type sagemakerStudioAPI interface {
	sagemakerTagsAPI
	ListDomains(context.Context, *sagemaker.ListDomainsInput, ...func(*sagemaker.Options)) (*sagemaker.ListDomainsOutput, error)
	DescribeDomain(context.Context, *sagemaker.DescribeDomainInput, ...func(*sagemaker.Options)) (*sagemaker.DescribeDomainOutput, error)
	ListUserProfiles(context.Context, *sagemaker.ListUserProfilesInput, ...func(*sagemaker.Options)) (*sagemaker.ListUserProfilesOutput, error)
	DescribeUserProfile(context.Context, *sagemaker.DescribeUserProfileInput, ...func(*sagemaker.Options)) (*sagemaker.DescribeUserProfileOutput, error)
	ListSpaces(context.Context, *sagemaker.ListSpacesInput, ...func(*sagemaker.Options)) (*sagemaker.ListSpacesOutput, error)
	DescribeSpace(context.Context, *sagemaker.DescribeSpaceInput, ...func(*sagemaker.Options)) (*sagemaker.DescribeSpaceOutput, error)
	ListApps(context.Context, *sagemaker.ListAppsInput, ...func(*sagemaker.Options)) (*sagemaker.ListAppsOutput, error)
	DescribeApp(context.Context, *sagemaker.DescribeAppInput, ...func(*sagemaker.Options)) (*sagemaker.DescribeAppOutput, error)
	ListAppImageConfigs(context.Context, *sagemaker.ListAppImageConfigsInput, ...func(*sagemaker.Options)) (*sagemaker.ListAppImageConfigsOutput, error)
	DescribeAppImageConfig(context.Context, *sagemaker.DescribeAppImageConfigInput, ...func(*sagemaker.Options)) (*sagemaker.DescribeAppImageConfigOutput, error)
	ListStudioLifecycleConfigs(context.Context, *sagemaker.ListStudioLifecycleConfigsInput, ...func(*sagemaker.Options)) (*sagemaker.ListStudioLifecycleConfigsOutput, error)
	DescribeStudioLifecycleConfig(context.Context, *sagemaker.DescribeStudioLifecycleConfigInput, ...func(*sagemaker.Options)) (*sagemaker.DescribeStudioLifecycleConfigOutput, error)
}

// scanSageMakerStudio runs all Studio-family phases for one region: Studio
// domains, the user profiles / spaces / apps that hang off them, plus the
// account-scoped AppImageConfigs and StudioLifecycleConfigs.
func scanSageMakerStudio(ctx context.Context, client sagemakerStudioAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	return runSageMakerPhases(
		sagemakerPhase{"sagemaker:ListDomains", func() (int, int, error) { return scanSageMakerDomains(ctx, client, acct, region, st, scanID) }},
		sagemakerPhase{"sagemaker:ListUserProfiles", func() (int, int, error) { return scanSageMakerUserProfiles(ctx, client, acct, region, st, scanID) }},
		sagemakerPhase{"sagemaker:ListSpaces", func() (int, int, error) { return scanSageMakerSpaces(ctx, client, acct, region, st, scanID) }},
		sagemakerPhase{"sagemaker:ListApps", func() (int, int, error) { return scanSageMakerApps(ctx, client, acct, region, st, scanID) }},
		sagemakerPhase{"sagemaker:ListAppImageConfigs", func() (int, int, error) { return scanSageMakerAppImageConfigs(ctx, client, acct, region, st, scanID) }},
		sagemakerPhase{"sagemaker:ListStudioLifecycleConfigs", func() (int, int, error) {
			return scanSageMakerStudioLifecycleConfigs(ctx, client, acct, region, st, scanID)
		}},
	)
}

// scanSageMakerDomains lists Studio domains then fans out DescribeDomain
// for full body (DefaultUserSettings, VpcId, SubnetIds, KmsKeyId,
// AppNetworkAccessType, etc.) — list summary omits all edge-bearing fields.
func scanSageMakerDomains(ctx context.Context, client sagemakerStudioAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	return sagemakerDescribe[smtypes.DomainDetails]{
		listOp: "sagemaker:ListDomains", describeOp: "sagemaker:DescribeDomain", notFound: []string{"ResourceNotFound"},
		list: func(ctx context.Context, token *string) ([]smtypes.DomainDetails, *string, error) {
			out, err := client.ListDomains(ctx, &sagemaker.ListDomainsInput{MaxResults: sdkaws.Int32(sagemakerListPageLimit), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.Domains, out.NextToken, nil
		},
		name: func(s smtypes.DomainDetails) string { return sv(s.DomainId) },
		listed: func(s smtypes.DomainDetails) *store.Resource {
			return sagemakerListedRow(acct, region, scanID, TypeSageMakerDomain, s.DomainArn, s.DomainName, s.CreationTime, string(s.Status), s)
		},
		describe: func(ctx context.Context, s smtypes.DomainDetails) (*store.Resource, error) {
			id := sv(s.DomainId)
			out, err := client.DescribeDomain(ctx, &sagemaker.DescribeDomainInput{DomainId: &id})
			if err != nil {
				return nil, err
			}
			arn := sv(out.DomainArn)
			if arn == "" {
				return nil, nil
			}
			name := sv(out.DomainName)
			status := string(out.Status)
			return &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeSageMakerDomain,
				NativeID:       arn,
				Name:           &name,
				Region:         &region,
				Status:         &status,
				CreatedAt:      tp(out.CreationTime),
				AttributesJSON: mustJSON(out),
				DiscoveredBy:   scanID,
			}, nil
		},
	}.scan(ctx, st, acct, region, sagemakerStoreTagged(ctx, client, acct, region, st, "sagemaker:ListDomains"))
}

// userProfileKey carries the (DomainId, UserProfileName) pair needed for
// DescribeUserProfile fan-out — list output gives no ARN.
type userProfileKey struct{ domainID, name string }

// scanSageMakerUserProfiles lists user profiles across all domains in the
// account+region, then fans out DescribeUserProfile for full body.
func scanSageMakerUserProfiles(ctx context.Context, client sagemakerStudioAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	return sagemakerDescribe[userProfileKey]{
		listOp: "sagemaker:ListUserProfiles", describeOp: "sagemaker:DescribeUserProfile", notFound: []string{"ResourceNotFound"},
		list: func(ctx context.Context, token *string) ([]userProfileKey, *string, error) {
			out, err := client.ListUserProfiles(ctx, &sagemaker.ListUserProfilesInput{MaxResults: sdkaws.Int32(sagemakerListPageLimit), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			keys := make([]userProfileKey, len(out.UserProfiles))
			for i, u := range out.UserProfiles {
				keys[i] = userProfileKey{sv(u.DomainId), sv(u.UserProfileName)}
			}
			return keys, out.NextToken, nil
		},
		name: func(k userProfileKey) string {
			if k.domainID == "" || k.name == "" {
				return ""
			}
			return k.domainID + "/" + k.name
		},
		// No listed: the summary carries no ARN of its own, so a key whose
		// describe fails is not stored.
		describe: func(ctx context.Context, k userProfileKey) (*store.Resource, error) {
			out, err := client.DescribeUserProfile(ctx, &sagemaker.DescribeUserProfileInput{
				DomainId:        &k.domainID,
				UserProfileName: &k.name,
			})
			if err != nil {
				return nil, err
			}
			arn := sv(out.UserProfileArn)
			if arn == "" {
				return nil, nil
			}
			name := sv(out.UserProfileName)
			status := string(out.Status)
			return &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeSageMakerUserProfile,
				NativeID:       arn,
				Name:           &name,
				Region:         &region,
				Status:         &status,
				CreatedAt:      tp(out.CreationTime),
				AttributesJSON: mustJSON(out),
				DiscoveredBy:   scanID,
			}, nil
		},
	}.scan(ctx, st, acct, region, sagemakerStoreTagged(ctx, client, acct, region, st, "sagemaker:ListUserProfiles"))
}

// spaceKey carries (DomainId, SpaceName) for DescribeSpace fan-out.
type spaceKey struct{ domainID, name string }

// scanSageMakerSpaces lists shared spaces across all domains then fans out
// DescribeSpace for full body (SpaceSettings, EBS volume, custom file system).
func scanSageMakerSpaces(ctx context.Context, client sagemakerStudioAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	return sagemakerDescribe[spaceKey]{
		listOp: "sagemaker:ListSpaces", describeOp: "sagemaker:DescribeSpace", notFound: []string{"ResourceNotFound"},
		list: func(ctx context.Context, token *string) ([]spaceKey, *string, error) {
			out, err := client.ListSpaces(ctx, &sagemaker.ListSpacesInput{MaxResults: sdkaws.Int32(sagemakerListPageLimit), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			keys := make([]spaceKey, len(out.Spaces))
			for i, s := range out.Spaces {
				keys[i] = spaceKey{sv(s.DomainId), sv(s.SpaceName)}
			}
			return keys, out.NextToken, nil
		},
		name: func(k spaceKey) string {
			if k.domainID == "" || k.name == "" {
				return ""
			}
			return k.domainID + "/" + k.name
		},
		// No listed: the summary carries no ARN of its own, so a key whose
		// describe fails is not stored.
		describe: func(ctx context.Context, k spaceKey) (*store.Resource, error) {
			out, err := client.DescribeSpace(ctx, &sagemaker.DescribeSpaceInput{
				DomainId:  &k.domainID,
				SpaceName: &k.name,
			})
			if err != nil {
				return nil, err
			}
			arn := sv(out.SpaceArn)
			if arn == "" {
				return nil, nil
			}
			name := sv(out.SpaceName)
			status := string(out.Status)
			return &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeSageMakerSpace,
				NativeID:       arn,
				Name:           &name,
				Region:         &region,
				Status:         &status,
				CreatedAt:      tp(out.CreationTime),
				AttributesJSON: mustJSON(out),
				DiscoveredBy:   scanID,
			}, nil
		},
	}.scan(ctx, st, acct, region, sagemakerStoreTagged(ctx, client, acct, region, st, "sagemaker:ListSpaces"))
}

// appKey carries the four-part composite key DescribeApp requires —
// (DomainId, AppType, AppName) plus exactly one of UserProfileName / SpaceName.
type appKey struct {
	domainID, name     string
	appType            smtypes.AppType
	userProfile, space string
}

// scanSageMakerApps lists apps across all domains then fans out DescribeApp
// for full body (LastHealthCheckTimestamp, ResourceSpec, BuiltInLifecycleConfigArn).
func scanSageMakerApps(ctx context.Context, client sagemakerStudioAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	return sagemakerDescribe[appKey]{
		listOp: "sagemaker:ListApps", describeOp: "sagemaker:DescribeApp", notFound: []string{"ResourceNotFound"},
		list: func(ctx context.Context, token *string) ([]appKey, *string, error) {
			out, err := client.ListApps(ctx, &sagemaker.ListAppsInput{MaxResults: sdkaws.Int32(sagemakerListPageLimit), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			keys := make([]appKey, len(out.Apps))
			for i, a := range out.Apps {
				keys[i] = appKey{domainID: sv(a.DomainId), name: sv(a.AppName), appType: a.AppType, userProfile: sv(a.UserProfileName), space: sv(a.SpaceName)}
			}
			return keys, out.NextToken, nil
		},
		name: func(k appKey) string {
			if k.domainID == "" || k.name == "" || k.appType == "" || k.userProfile == "" && k.space == "" {
				return ""
			}
			return k.domainID + "/" + string(k.appType) + "/" + k.name
		},
		// No listed: the summary carries no ARN of its own, so a key whose
		// describe fails is not stored.
		describe: func(ctx context.Context, k appKey) (*store.Resource, error) {
			in := &sagemaker.DescribeAppInput{
				DomainId: &k.domainID,
				AppType:  k.appType,
				AppName:  &k.name,
			}
			if k.userProfile != "" {
				in.UserProfileName = &k.userProfile
			}
			if k.space != "" {
				in.SpaceName = &k.space
			}
			out, err := client.DescribeApp(ctx, in)
			if err != nil {
				return nil, err
			}
			arn := sv(out.AppArn)
			if arn == "" {
				return nil, nil
			}
			name := sv(out.AppName)
			status := string(out.Status)
			return &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeSageMakerApp,
				NativeID:       arn,
				Name:           &name,
				Region:         &region,
				Status:         &status,
				CreatedAt:      tp(out.CreationTime),
				AttributesJSON: mustJSON(out),
				DiscoveredBy:   scanID,
			}, nil
		},
	}.scan(ctx, st, acct, region, sagemakerStoreTagged(ctx, client, acct, region, st, "sagemaker:ListApps"))
}

// scanSageMakerAppImageConfigs lists app image configs (account+region scoped)
// then fans out DescribeAppImageConfig for full body (KernelGatewayImageConfig,
// JupyterLabAppImageConfig, CodeEditorAppImageConfig).
func scanSageMakerAppImageConfigs(ctx context.Context, client sagemakerStudioAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	return sagemakerDescribe[smtypes.AppImageConfigDetails]{
		listOp: "sagemaker:ListAppImageConfigs", describeOp: "sagemaker:DescribeAppImageConfig", notFound: []string{"ResourceNotFound"},
		list: func(ctx context.Context, token *string) ([]smtypes.AppImageConfigDetails, *string, error) {
			out, err := client.ListAppImageConfigs(ctx, &sagemaker.ListAppImageConfigsInput{MaxResults: sdkaws.Int32(sagemakerListPageLimit), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.AppImageConfigs, out.NextToken, nil
		},
		name: func(s smtypes.AppImageConfigDetails) string { return sv(s.AppImageConfigName) },
		listed: func(s smtypes.AppImageConfigDetails) *store.Resource {
			return sagemakerListedRow(acct, region, scanID, TypeSageMakerAppImageConfig, s.AppImageConfigArn, s.AppImageConfigName, s.CreationTime, "", s)
		},
		describe: func(ctx context.Context, s smtypes.AppImageConfigDetails) (*store.Resource, error) {
			name := sv(s.AppImageConfigName)
			out, err := client.DescribeAppImageConfig(ctx, &sagemaker.DescribeAppImageConfigInput{AppImageConfigName: &name})
			if err != nil {
				return nil, err
			}
			arn := sv(out.AppImageConfigArn)
			if arn == "" {
				return nil, nil
			}
			outName := sv(out.AppImageConfigName)
			return &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeSageMakerAppImageConfig,
				NativeID:       arn,
				Name:           &outName,
				Region:         &region,
				CreatedAt:      tp(out.CreationTime),
				AttributesJSON: mustJSON(out),
				DiscoveredBy:   scanID,
			}, nil
		},
	}.scan(ctx, st, acct, region, sagemakerStoreTagged(ctx, client, acct, region, st, "sagemaker:ListAppImageConfigs"))
}

// scanSageMakerStudioLifecycleConfigs lists Studio lifecycle configs then
// fans out DescribeStudioLifecycleConfig for full body (StudioLifecycleConfigContent).
func scanSageMakerStudioLifecycleConfigs(ctx context.Context, client sagemakerStudioAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	return sagemakerDescribe[smtypes.StudioLifecycleConfigDetails]{
		listOp: "sagemaker:ListStudioLifecycleConfigs", describeOp: "sagemaker:DescribeStudioLifecycleConfig", notFound: []string{"ResourceNotFound"},
		list: func(ctx context.Context, token *string) ([]smtypes.StudioLifecycleConfigDetails, *string, error) {
			out, err := client.ListStudioLifecycleConfigs(ctx, &sagemaker.ListStudioLifecycleConfigsInput{MaxResults: sdkaws.Int32(sagemakerListPageLimit), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.StudioLifecycleConfigs, out.NextToken, nil
		},
		name: func(s smtypes.StudioLifecycleConfigDetails) string { return sv(s.StudioLifecycleConfigName) },
		listed: func(s smtypes.StudioLifecycleConfigDetails) *store.Resource {
			return sagemakerListedRow(acct, region, scanID, TypeSageMakerStudioLifecycleConfig, s.StudioLifecycleConfigArn, s.StudioLifecycleConfigName, s.CreationTime, "", s)
		},
		describe: func(ctx context.Context, s smtypes.StudioLifecycleConfigDetails) (*store.Resource, error) {
			name := sv(s.StudioLifecycleConfigName)
			out, err := client.DescribeStudioLifecycleConfig(ctx, &sagemaker.DescribeStudioLifecycleConfigInput{StudioLifecycleConfigName: &name})
			if err != nil {
				return nil, err
			}
			arn := sv(out.StudioLifecycleConfigArn)
			if arn == "" {
				return nil, nil
			}
			outName := sv(out.StudioLifecycleConfigName)
			return &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeSageMakerStudioLifecycleConfig,
				NativeID:       arn,
				Name:           &outName,
				Region:         &region,
				CreatedAt:      tp(out.CreationTime),
				AttributesJSON: mustJSON(out),
				DiscoveredBy:   scanID,
			}, nil
		},
	}.scan(ctx, st, acct, region, sagemakerStoreTagged(ctx, client, acct, region, st, "sagemaker:ListStudioLifecycleConfigs"))
}
