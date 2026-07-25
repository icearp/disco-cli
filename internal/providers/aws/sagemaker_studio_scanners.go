package aws

import (
	"context"
	"fmt"
	"sync"

	"github.com/aws/aws-sdk-go-v2/service/sagemaker"
	smtypes "github.com/aws/aws-sdk-go-v2/service/sagemaker/types"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
	"golang.org/x/sync/errgroup"
	"golang.org/x/sync/semaphore"
)

func init() {
	registerType(restype.Descriptor{Type: TypeSageMakerDomain, Service: "sagemaker"})
	registerType(restype.Descriptor{Type: TypeSageMakerUserProfile, Service: "sagemaker"})
	registerType(restype.Descriptor{Type: TypeSageMakerSpace, Service: "sagemaker"})
	registerType(restype.Descriptor{Type: TypeSageMakerApp, Service: "sagemaker"})
	registerType(restype.Descriptor{Type: TypeSageMakerAppImageConfig, Service: "sagemaker", Leaf: true})
	registerType(restype.Descriptor{Type: TypeSageMakerStudioLifecycleConfig, Service: "sagemaker", Leaf: true})
}

// sagemakerStudioAPI is the narrow surface used by SageMaker Studio
// sub-phases. Each phase List+fan-out Describe so attrs carry the full
// Describe body — list summaries omit ARNs for user-profile/space/app and
// lack edge-bearing fields (VPC/SG/role/EFS settings) on every type.
type sagemakerStudioAPI interface {
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
func scanSageMakerStudio(ctx context.Context, client sagemakerStudioAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	for _, phase := range []func(context.Context, sagemakerStudioAPI, *account, string, *store.Store, string) (int, int, error){
		scanSageMakerDomains,
		scanSageMakerUserProfiles,
		scanSageMakerSpaces,
		scanSageMakerApps,
		scanSageMakerAppImageConfigs,
		scanSageMakerStudioLifecycleConfigs,
	} {
		t, i, ferr := phase(ctx, client, acct, region, st, scanID)
		if ferr != nil {
			return total, inserted, ferr
		}
		total += t
		inserted += i
	}
	return total, inserted, nil
}

// scanSageMakerDomains lists Studio domains then fans out DescribeDomain
// for full body (DefaultUserSettings, VpcId, SubnetIds, KmsKeyId,
// AppNetworkAccessType, etc.) — list summary omits all edge-bearing fields.
func scanSageMakerDomains(ctx context.Context, client sagemakerStudioAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := sagemaker.NewListDomainsPaginator(client, &sagemaker.ListDomainsInput{})
	var ids []string
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "sagemaker:ListDomains", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("sagemaker:ListDomains: %w", perr)
		}
		for _, d := range out.Domains {
			if d.DomainId != nil {
				ids = append(ids, *d.DomainId)
			}
		}
	}
	if len(ids) == 0 {
		return 0, 0, nil
	}
	sem := semaphore.NewWeighted(fanoutMed)
	var (
		mu    sync.Mutex
		batch []*store.Resource
	)
	g, gctx := errgroup.WithContext(ctx)
	for _, id := range ids {
		if err := sem.Acquire(gctx, 1); err != nil {
			return 0, 0, err
		}
		g.Go(func() error {
			defer sem.Release(1)
			out, derr := client.DescribeDomain(gctx, &sagemaker.DescribeDomainInput{DomainId: &id})
			if derr != nil {
				if isAccessDenied(derr) {
					return nil
				}
				return fmt.Errorf("sagemaker:DescribeDomain %s: %w", id, derr)
			}
			arn := sv(out.DomainArn)
			if arn == "" {
				return nil
			}
			name := sv(out.DomainName)
			status := string(out.Status)
			mu.Lock()
			batch = append(batch, &store.Resource{
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
			})
			mu.Unlock()
			return nil
		})
	}
	if werr := g.Wait(); werr != nil {
		return 0, 0, werr
	}
	if len(batch) == 0 {
		return 0, 0, nil
	}
	n, uerr := st.UpsertResources(batch)
	if uerr != nil {
		return 0, 0, fmt.Errorf("upsert sagemaker domains: %w", uerr)
	}
	return len(batch), n, nil
}

// userProfileKey carries the (DomainId, UserProfileName) pair needed for
// DescribeUserProfile fan-out — list output gives no ARN.
type userProfileKey struct{ domainID, name string }

// scanSageMakerUserProfiles lists user profiles across all domains in the
// account+region, then fans out DescribeUserProfile for full body.
func scanSageMakerUserProfiles(ctx context.Context, client sagemakerStudioAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := sagemaker.NewListUserProfilesPaginator(client, &sagemaker.ListUserProfilesInput{})
	var keys []userProfileKey
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "sagemaker:ListUserProfiles", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("sagemaker:ListUserProfiles: %w", perr)
		}
		for _, u := range out.UserProfiles {
			if u.DomainId != nil && u.UserProfileName != nil {
				keys = append(keys, userProfileKey{*u.DomainId, *u.UserProfileName})
			}
		}
	}
	if len(keys) == 0 {
		return 0, 0, nil
	}
	sem := semaphore.NewWeighted(fanoutMed)
	var (
		mu    sync.Mutex
		batch []*store.Resource
	)
	g, gctx := errgroup.WithContext(ctx)
	for _, k := range keys {
		if err := sem.Acquire(gctx, 1); err != nil {
			return 0, 0, err
		}
		g.Go(func() error {
			defer sem.Release(1)
			out, derr := client.DescribeUserProfile(gctx, &sagemaker.DescribeUserProfileInput{
				DomainId:        &k.domainID,
				UserProfileName: &k.name,
			})
			if derr != nil {
				if isAccessDenied(derr) {
					return nil
				}
				return fmt.Errorf("sagemaker:DescribeUserProfile %s/%s: %w", k.domainID, k.name, derr)
			}
			arn := sv(out.UserProfileArn)
			if arn == "" {
				return nil
			}
			name := sv(out.UserProfileName)
			status := string(out.Status)
			mu.Lock()
			batch = append(batch, &store.Resource{
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
			})
			mu.Unlock()
			return nil
		})
	}
	if werr := g.Wait(); werr != nil {
		return 0, 0, werr
	}
	if len(batch) == 0 {
		return 0, 0, nil
	}
	n, uerr := st.UpsertResources(batch)
	if uerr != nil {
		return 0, 0, fmt.Errorf("upsert sagemaker user profiles: %w", uerr)
	}
	return len(batch), n, nil
}

// spaceKey carries (DomainId, SpaceName) for DescribeSpace fan-out.
type spaceKey struct{ domainID, name string }

// scanSageMakerSpaces lists shared spaces across all domains then fans out
// DescribeSpace for full body (SpaceSettings, EBS volume, custom file system).
func scanSageMakerSpaces(ctx context.Context, client sagemakerStudioAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := sagemaker.NewListSpacesPaginator(client, &sagemaker.ListSpacesInput{})
	var keys []spaceKey
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "sagemaker:ListSpaces", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("sagemaker:ListSpaces: %w", perr)
		}
		for _, s := range out.Spaces {
			if s.DomainId != nil && s.SpaceName != nil {
				keys = append(keys, spaceKey{*s.DomainId, *s.SpaceName})
			}
		}
	}
	if len(keys) == 0 {
		return 0, 0, nil
	}
	sem := semaphore.NewWeighted(fanoutMed)
	var (
		mu    sync.Mutex
		batch []*store.Resource
	)
	g, gctx := errgroup.WithContext(ctx)
	for _, k := range keys {
		if err := sem.Acquire(gctx, 1); err != nil {
			return 0, 0, err
		}
		g.Go(func() error {
			defer sem.Release(1)
			out, derr := client.DescribeSpace(gctx, &sagemaker.DescribeSpaceInput{
				DomainId:  &k.domainID,
				SpaceName: &k.name,
			})
			if derr != nil {
				if isAccessDenied(derr) {
					return nil
				}
				return fmt.Errorf("sagemaker:DescribeSpace %s/%s: %w", k.domainID, k.name, derr)
			}
			arn := sv(out.SpaceArn)
			if arn == "" {
				return nil
			}
			name := sv(out.SpaceName)
			status := string(out.Status)
			mu.Lock()
			batch = append(batch, &store.Resource{
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
			})
			mu.Unlock()
			return nil
		})
	}
	if werr := g.Wait(); werr != nil {
		return 0, 0, werr
	}
	if len(batch) == 0 {
		return 0, 0, nil
	}
	n, uerr := st.UpsertResources(batch)
	if uerr != nil {
		return 0, 0, fmt.Errorf("upsert sagemaker spaces: %w", uerr)
	}
	return len(batch), n, nil
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
	pager := sagemaker.NewListAppsPaginator(client, &sagemaker.ListAppsInput{})
	var keys []appKey
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "sagemaker:ListApps", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("sagemaker:ListApps: %w", perr)
		}
		for _, a := range out.Apps {
			if a.DomainId == nil || a.AppName == nil || a.AppType == "" {
				continue
			}
			k := appKey{domainID: *a.DomainId, name: *a.AppName, appType: a.AppType}
			if a.UserProfileName != nil {
				k.userProfile = *a.UserProfileName
			}
			if a.SpaceName != nil {
				k.space = *a.SpaceName
			}
			if k.userProfile == "" && k.space == "" {
				continue
			}
			keys = append(keys, k)
		}
	}
	if len(keys) == 0 {
		return 0, 0, nil
	}
	sem := semaphore.NewWeighted(fanoutMed)
	var (
		mu    sync.Mutex
		batch []*store.Resource
	)
	g, gctx := errgroup.WithContext(ctx)
	for _, k := range keys {
		if err := sem.Acquire(gctx, 1); err != nil {
			return 0, 0, err
		}
		g.Go(func() error {
			defer sem.Release(1)
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
			out, derr := client.DescribeApp(gctx, in)
			if derr != nil {
				if isAccessDenied(derr) {
					return nil
				}
				return fmt.Errorf("sagemaker:DescribeApp %s/%s/%s: %w", k.domainID, k.appType, k.name, derr)
			}
			arn := sv(out.AppArn)
			if arn == "" {
				return nil
			}
			name := sv(out.AppName)
			status := string(out.Status)
			mu.Lock()
			batch = append(batch, &store.Resource{
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
			})
			mu.Unlock()
			return nil
		})
	}
	if werr := g.Wait(); werr != nil {
		return 0, 0, werr
	}
	if len(batch) == 0 {
		return 0, 0, nil
	}
	n, uerr := st.UpsertResources(batch)
	if uerr != nil {
		return 0, 0, fmt.Errorf("upsert sagemaker apps: %w", uerr)
	}
	return len(batch), n, nil
}

// scanSageMakerAppImageConfigs lists app image configs (account+region scoped)
// then fans out DescribeAppImageConfig for full body (KernelGatewayImageConfig,
// JupyterLabAppImageConfig, CodeEditorAppImageConfig).
func scanSageMakerAppImageConfigs(ctx context.Context, client sagemakerStudioAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := sagemaker.NewListAppImageConfigsPaginator(client, &sagemaker.ListAppImageConfigsInput{})
	var names []string
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "sagemaker:ListAppImageConfigs", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("sagemaker:ListAppImageConfigs: %w", perr)
		}
		for _, c := range out.AppImageConfigs {
			if c.AppImageConfigName != nil {
				names = append(names, *c.AppImageConfigName)
			}
		}
	}
	if len(names) == 0 {
		return 0, 0, nil
	}
	sem := semaphore.NewWeighted(fanoutMed)
	var (
		mu    sync.Mutex
		batch []*store.Resource
	)
	g, gctx := errgroup.WithContext(ctx)
	for _, name := range names {
		if err := sem.Acquire(gctx, 1); err != nil {
			return 0, 0, err
		}
		g.Go(func() error {
			defer sem.Release(1)
			out, derr := client.DescribeAppImageConfig(gctx, &sagemaker.DescribeAppImageConfigInput{AppImageConfigName: &name})
			if derr != nil {
				if isAccessDenied(derr) {
					return nil
				}
				return fmt.Errorf("sagemaker:DescribeAppImageConfig %s: %w", name, derr)
			}
			arn := sv(out.AppImageConfigArn)
			if arn == "" {
				return nil
			}
			outName := sv(out.AppImageConfigName)
			mu.Lock()
			batch = append(batch, &store.Resource{
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
			})
			mu.Unlock()
			return nil
		})
	}
	if werr := g.Wait(); werr != nil {
		return 0, 0, werr
	}
	if len(batch) == 0 {
		return 0, 0, nil
	}
	n, uerr := st.UpsertResources(batch)
	if uerr != nil {
		return 0, 0, fmt.Errorf("upsert sagemaker app-image-configs: %w", uerr)
	}
	return len(batch), n, nil
}

// scanSageMakerStudioLifecycleConfigs lists Studio lifecycle configs then
// fans out DescribeStudioLifecycleConfig for full body (StudioLifecycleConfigContent).
func scanSageMakerStudioLifecycleConfigs(ctx context.Context, client sagemakerStudioAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := sagemaker.NewListStudioLifecycleConfigsPaginator(client, &sagemaker.ListStudioLifecycleConfigsInput{})
	var names []string
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				_ = skipIfAccessDenied(st, "sagemaker:ListStudioLifecycleConfigs", acct.ID, region, perr)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("sagemaker:ListStudioLifecycleConfigs: %w", perr)
		}
		for _, c := range out.StudioLifecycleConfigs {
			if c.StudioLifecycleConfigName != nil {
				names = append(names, *c.StudioLifecycleConfigName)
			}
		}
	}
	if len(names) == 0 {
		return 0, 0, nil
	}
	sem := semaphore.NewWeighted(fanoutMed)
	var (
		mu    sync.Mutex
		batch []*store.Resource
	)
	g, gctx := errgroup.WithContext(ctx)
	for _, name := range names {
		if err := sem.Acquire(gctx, 1); err != nil {
			return 0, 0, err
		}
		g.Go(func() error {
			defer sem.Release(1)
			out, derr := client.DescribeStudioLifecycleConfig(gctx, &sagemaker.DescribeStudioLifecycleConfigInput{StudioLifecycleConfigName: &name})
			if derr != nil {
				if isAccessDenied(derr) {
					return nil
				}
				return fmt.Errorf("sagemaker:DescribeStudioLifecycleConfig %s: %w", name, derr)
			}
			arn := sv(out.StudioLifecycleConfigArn)
			if arn == "" {
				return nil
			}
			outName := sv(out.StudioLifecycleConfigName)
			mu.Lock()
			batch = append(batch, &store.Resource{
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
			})
			mu.Unlock()
			return nil
		})
	}
	if werr := g.Wait(); werr != nil {
		return 0, 0, werr
	}
	if len(batch) == 0 {
		return 0, 0, nil
	}
	n, uerr := st.UpsertResources(batch)
	if uerr != nil {
		return 0, 0, fmt.Errorf("upsert sagemaker studio-lifecycle-configs: %w", uerr)
	}
	return len(batch), n, nil
}
