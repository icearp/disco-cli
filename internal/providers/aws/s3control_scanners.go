package aws

import (
	"context"
	"fmt"
	"sync"

	"github.com/aws/aws-sdk-go-v2/service/s3control"
	s3ctypes "github.com/aws/aws-sdk-go-v2/service/s3control/types"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
	"golang.org/x/sync/errgroup"
	"golang.org/x/sync/semaphore"
)

// s3controlAPI is the narrow set of S3 Control ops used by scanS3Control's
// sub-phases.
type s3controlAPI interface {
	ListAccessGrantsInstances(context.Context, *s3control.ListAccessGrantsInstancesInput, ...func(*s3control.Options)) (*s3control.ListAccessGrantsInstancesOutput, error)
	ListAccessGrantsLocations(context.Context, *s3control.ListAccessGrantsLocationsInput, ...func(*s3control.Options)) (*s3control.ListAccessGrantsLocationsOutput, error)
	ListAccessGrants(context.Context, *s3control.ListAccessGrantsInput, ...func(*s3control.Options)) (*s3control.ListAccessGrantsOutput, error)
	ListAccessPoints(context.Context, *s3control.ListAccessPointsInput, ...func(*s3control.Options)) (*s3control.ListAccessPointsOutput, error)
	ListMultiRegionAccessPoints(context.Context, *s3control.ListMultiRegionAccessPointsInput, ...func(*s3control.Options)) (*s3control.ListMultiRegionAccessPointsOutput, error)
	GetMultiRegionAccessPointPolicy(context.Context, *s3control.GetMultiRegionAccessPointPolicyInput, ...func(*s3control.Options)) (*s3control.GetMultiRegionAccessPointPolicyOutput, error)
	ListStorageLensConfigurations(context.Context, *s3control.ListStorageLensConfigurationsInput, ...func(*s3control.Options)) (*s3control.ListStorageLensConfigurationsOutput, error)
	GetStorageLensConfiguration(context.Context, *s3control.GetStorageLensConfigurationInput, ...func(*s3control.Options)) (*s3control.GetStorageLensConfigurationOutput, error)
	ListStorageLensGroups(context.Context, *s3control.ListStorageLensGroupsInput, ...func(*s3control.Options)) (*s3control.ListStorageLensGroupsOutput, error)
	ListAccessPointsForObjectLambda(context.Context, *s3control.ListAccessPointsForObjectLambdaInput, ...func(*s3control.Options)) (*s3control.ListAccessPointsForObjectLambdaOutput, error)
	GetAccessPointPolicyForObjectLambda(context.Context, *s3control.GetAccessPointPolicyForObjectLambdaInput, ...func(*s3control.Options)) (*s3control.GetAccessPointPolicyForObjectLambdaOutput, error)
}

func init() {
	registerType(restype.Descriptor{Type: TypeS3AccessGrantsInstance, Service: "s3", Upstream: "AWS::S3::AccessGrantsInstance", Leaf: true})
	registerType(restype.Descriptor{Type: TypeS3AccessGrantsLocation, Service: "s3", Upstream: "AWS::S3::AccessGrantsLocation"})
	registerType(restype.Descriptor{Type: TypeS3AccessGrant, Service: "s3", Upstream: "AWS::S3::AccessGrant"})
	registerType(restype.Descriptor{Type: TypeS3AccessPoint, Service: "s3", Upstream: "AWS::S3::AccessPoint"})
	registerType(restype.Descriptor{Type: TypeS3MultiRegionAccessPoint, Service: "s3", Upstream: "AWS::S3::MultiRegionAccessPoint"})
	registerType(restype.Descriptor{Type: TypeS3MultiRegionAccessPointPolicy, Service: "s3", Upstream: "AWS::S3::MultiRegionAccessPointPolicy"})
	registerType(restype.Descriptor{Type: TypeS3StorageLens, Service: "s3", Upstream: "AWS::S3::StorageLens"})
	registerType(restype.Descriptor{Type: TypeS3StorageLensGroup, Service: "s3", Upstream: "AWS::S3::StorageLensGroup", Leaf: true})
	registerType(restype.Descriptor{Type: TypeS3ObjectLambdaAccessPoint, Service: "s3-object-lambda", Upstream: "AWS::S3ObjectLambda::AccessPoint", Leaf: true})
	registerType(restype.Descriptor{Type: TypeS3ObjectLambdaAccessPointPolicy, Service: "s3-object-lambda", Upstream: "AWS::S3ObjectLambda::AccessPointPolicy", Leaf: true})
	registerService(serviceEntry{
		name:   "aws:s3control",
		global: false,
		fn:     scanS3Control,
	})
}

// scanS3Control discovers S3 Control resources for the given region.
// Regional resources (Access Grants, Access Points, Storage Lens) scan in
// every region; global resources (Multi-Region Access Points) scan only in
// us-east-1.
func scanS3Control(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := s3control.NewFromConfig(acct.cfg, func(o *s3control.Options) { o.Region = region })
	// Regional resources: scan in every region.
	for _, scan := range []func(context.Context, *account, string, s3controlAPI, *store.Store, string) (int, int, error){
		scanAccessGrantsInstances,
		scanAccessGrantsLocations,
		scanAccessGrants,
		scanS3AccessPoints,
		scanStorageLens,
		scanStorageLensGroups,
		scanS3ObjectLambdaAccessPoints,
	} {
		tt, nn, e := scan(ctx, acct, region, client, st, scanID)
		if e != nil {
			return total, inserted, e
		}
		total += tt
		inserted += nn
	}
	// MRAPs are a global resource but the API endpoint is only available in
	// us-west-2 — scan once with a client pinned to that region.
	if region == "us-west-2" {
		mrapClient := s3control.NewFromConfig(acct.cfg, func(o *s3control.Options) { o.Region = "us-west-2" })
		tt, nn, e := scanMultiRegionAccessPoints(ctx, acct, mrapClient, st, scanID)
		if e != nil {
			return total, inserted, e
		}
		total += tt
		inserted += nn
	}
	return
}

// scanAccessGrantsInstances lists S3 Access Grants instances for one region.
// There is at most one instance per region per account.
func scanAccessGrantsInstances(ctx context.Context, acct *account, region string, client s3controlAPI, st *store.Store, scanID string) (total, inserted int, err error) {
	var batch []*store.Resource
	p := s3control.NewListAccessGrantsInstancesPaginator(client, &s3control.ListAccessGrantsInstancesInput{AccountId: &acct.ID})
	for p.HasMorePages() {
		out, apiErr := p.NextPage(ctx)
		if apiErr != nil {
			if isAccessDenied(apiErr) {
				return 0, 0, skipIfAccessDenied(st, "s3control:ListAccessGrantsInstances", acct.ID, region, apiErr)
			}
			return 0, 0, fmt.Errorf("s3control:ListAccessGrantsInstances: %w", apiErr)
		}
		for _, e := range out.AccessGrantsInstancesList {
			arn := sv(e.AccessGrantsInstanceArn)
			id := sv(e.AccessGrantsInstanceId)
			batch = append(batch, &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeS3AccessGrantsInstance,
				NativeID:       arn,
				Name:           &id,
				Region:         &region,
				CreatedAt:      tp(e.CreatedAt),
				AttributesJSON: mustJSON(e),
				DiscoveredBy:   scanID,
			})
		}
	}
	if len(batch) > 0 {
		n, err := st.UpsertResources(batch)
		if err != nil {
			return 0, 0, fmt.Errorf("upsert S3 access grants instances: %w", err)
		}
		total = len(batch)
		inserted = n
	}
	return
}

// scanAccessGrantsLocations lists the registered locations in the account's
// S3 Access Grants instance for the given region.
func scanAccessGrantsLocations(ctx context.Context, acct *account, region string, client s3controlAPI, st *store.Store, scanID string) (total, inserted int, err error) {
	var batch []*store.Resource
	p := s3control.NewListAccessGrantsLocationsPaginator(client, &s3control.ListAccessGrantsLocationsInput{AccountId: &acct.ID})
	for p.HasMorePages() {
		out, apiErr := p.NextPage(ctx)
		if apiErr != nil {
			if isAPIErrorCode(apiErr, "AccessGrantsInstanceNotExistsError") {
				return 0, 0, nil // no instance in this region — expected
			}
			if isAccessDenied(apiErr) {
				return 0, 0, skipIfAccessDenied(st, "s3control:ListAccessGrantsLocations", acct.ID, region, apiErr)
			}
			return 0, 0, fmt.Errorf("s3control:ListAccessGrantsLocations: %w", apiErr)
		}
		for _, e := range out.AccessGrantsLocationsList {
			arn := sv(e.AccessGrantsLocationArn)
			id := sv(e.AccessGrantsLocationId)
			batch = append(batch, &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeS3AccessGrantsLocation,
				NativeID:       arn,
				Name:           &id,
				Region:         &region,
				CreatedAt:      tp(e.CreatedAt),
				AttributesJSON: mustJSON(e),
				DiscoveredBy:   scanID,
			})
		}
	}
	if len(batch) > 0 {
		n, err := st.UpsertResources(batch)
		if err != nil {
			return 0, 0, fmt.Errorf("upsert S3 access grants locations: %w", err)
		}
		total = len(batch)
		inserted = n
	}
	return
}

// scanAccessGrants lists all access grants in the account's S3 Access Grants
// instance for the given region.
func scanAccessGrants(ctx context.Context, acct *account, region string, client s3controlAPI, st *store.Store, scanID string) (total, inserted int, err error) {
	var batch []*store.Resource
	p := s3control.NewListAccessGrantsPaginator(client, &s3control.ListAccessGrantsInput{AccountId: &acct.ID})
	for p.HasMorePages() {
		out, apiErr := p.NextPage(ctx)
		if apiErr != nil {
			if isAPIErrorCode(apiErr, "AccessGrantsInstanceNotExistsError") {
				return 0, 0, nil // no instance in this region — expected
			}
			if isAccessDenied(apiErr) {
				return 0, 0, skipIfAccessDenied(st, "s3control:ListAccessGrants", acct.ID, region, apiErr)
			}
			return 0, 0, fmt.Errorf("s3control:ListAccessGrants: %w", apiErr)
		}
		for _, e := range out.AccessGrantsList {
			arn := sv(e.AccessGrantArn)
			id := sv(e.AccessGrantId)
			batch = append(batch, &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeS3AccessGrant,
				NativeID:       arn,
				Name:           &id,
				Region:         &region,
				CreatedAt:      tp(e.CreatedAt),
				AttributesJSON: mustJSON(e),
				DiscoveredBy:   scanID,
			})
		}
	}
	if len(batch) > 0 {
		n, err := st.UpsertResources(batch)
		if err != nil {
			return 0, 0, fmt.Errorf("upsert S3 access grants: %w", err)
		}
		total = len(batch)
		inserted = n
	}
	return
}

// scanS3AccessPoints lists S3 access points in the given region.
func scanS3AccessPoints(ctx context.Context, acct *account, region string, client s3controlAPI, st *store.Store, scanID string) (total, inserted int, err error) {
	var batch []*store.Resource
	p := s3control.NewListAccessPointsPaginator(client, &s3control.ListAccessPointsInput{AccountId: &acct.ID})
	for p.HasMorePages() {
		out, apiErr := p.NextPage(ctx)
		if apiErr != nil {
			if isAccessDenied(apiErr) {
				return 0, 0, skipIfAccessDenied(st, "s3control:ListAccessPoints", acct.ID, region, apiErr)
			}
			return 0, 0, fmt.Errorf("s3control:ListAccessPoints: %w", apiErr)
		}
		for _, e := range out.AccessPointList {
			arn := sv(e.AccessPointArn)
			name := sv(e.Name)
			batch = append(batch, &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeS3AccessPoint,
				NativeID:       arn,
				Name:           &name,
				Region:         &region,
				AttributesJSON: mustJSON(e),
				DiscoveredBy:   scanID,
			})
		}
	}
	if len(batch) > 0 {
		n, err := st.UpsertResources(batch)
		if err != nil {
			return 0, 0, fmt.Errorf("upsert S3 access points: %w", err)
		}
		total = len(batch)
		inserted = n
	}
	return
}

// scanMultiRegionAccessPoints lists all Multi-Region Access Points (global,
// always routed through us-east-1) and then fetches each one's policy.
func scanMultiRegionAccessPoints(ctx context.Context, acct *account, client s3controlAPI, st *store.Store, scanID string) (total, inserted int, err error) {
	var mraps []s3ctypes.MultiRegionAccessPointReport
	p := s3control.NewListMultiRegionAccessPointsPaginator(client, &s3control.ListMultiRegionAccessPointsInput{AccountId: &acct.ID})
	for p.HasMorePages() {
		out, apiErr := p.NextPage(ctx)
		if apiErr != nil {
			if isAccessDenied(apiErr) {
				return 0, 0, skipIfAccessDenied(st, "s3control:ListMultiRegionAccessPoints", acct.ID, "global", apiErr)
			}
			return 0, 0, fmt.Errorf("s3control:ListMultiRegionAccessPoints: %w", apiErr)
		}
		mraps = append(mraps, out.AccessPoints...)
	}

	var batch []*store.Resource
	for _, m := range mraps {
		name := sv(m.Name)
		// MRAPs have no native ARN in the list output; construct a stable one.
		arn := fmt.Sprintf("arn:aws:s3::%s:accesspoint/%s", acct.ID, name)
		batch = append(batch, &store.Resource{
			Provider:       "aws",
			AccountID:      acct.ID,
			AccountName:    &acct.Name,
			Type:           TypeS3MultiRegionAccessPoint,
			NativeID:       arn,
			Name:           &name,
			CreatedAt:      tp(m.CreatedAt),
			AttributesJSON: mustJSON(m),
			DiscoveredBy:   scanID,
		})
	}
	if len(batch) > 0 {
		n, err := st.UpsertResources(batch)
		if err != nil {
			return 0, 0, fmt.Errorf("upsert S3 multi-region access points: %w", err)
		}
		total += len(batch)
		inserted += n
	}

	tt, nn, err := scanMRAPPolicies(ctx, acct, client, mraps, st, scanID)
	total += tt
	inserted += nn
	return total, inserted, err
}

// scanMRAPPolicies fetches the policy for each MRAP concurrently.
func scanMRAPPolicies(ctx context.Context, acct *account, client s3controlAPI, mraps []s3ctypes.MultiRegionAccessPointReport, st *store.Store, scanID string) (total, inserted int, err error) {
	sem := semaphore.NewWeighted(fanoutMed)
	var (
		mu    sync.Mutex
		batch []*store.Resource
	)
	g, gctx := errgroup.WithContext(ctx)
	for _, m := range mraps {
		g.Go(func() error {
			if err := sem.Acquire(gctx, 1); err != nil {
				return err
			}
			defer sem.Release(1)
			name := sv(m.Name)
			out, err := client.GetMultiRegionAccessPointPolicy(gctx, &s3control.GetMultiRegionAccessPointPolicyInput{
				AccountId: &acct.ID,
				Name:      &name,
			})
			if err != nil {
				if isAccessDenied(err) {
					return nil
				}
				return fmt.Errorf("s3control:GetMultiRegionAccessPointPolicy %s: %w", name, err)
			}
			if out.Policy == nil {
				return nil // no policy attached
			}
			policyARN := fmt.Sprintf("arn:aws:s3::%s:accesspoint/%s/policy", acct.ID, name)
			r := &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeS3MultiRegionAccessPointPolicy,
				NativeID:       policyARN,
				Name:           sp(name + "/policy"),
				AttributesJSON: mustJSON(out.Policy),
				DiscoveredBy:   scanID,
			}
			mu.Lock()
			batch = append(batch, r)
			mu.Unlock()
			return nil
		})
	}
	if err := g.Wait(); err != nil {
		return 0, 0, err
	}
	if len(batch) > 0 {
		n, err := st.UpsertResources(batch)
		if err != nil {
			return 0, 0, fmt.Errorf("upsert S3 MRAP policies: %w", err)
		}
		total = len(batch)
		inserted = n
	}
	return
}

// scanStorageLens lists S3 Storage Lens configs for the region, then fans out
// GetStorageLensConfiguration concurrently for the full body (Include/Exclude
// buckets, DataExport target) — the list response is sparse; only Get carries
// the edge-bearing fields resolveStorageLensRelationships needs. Per-item
// access-denied is tolerated so one unreadable config doesn't fail the whole
// scan.
func scanStorageLens(ctx context.Context, acct *account, region string, client s3controlAPI, st *store.Store, scanID string) (total, inserted int, err error) {
	// 1. List.
	var entries []s3ctypes.ListStorageLensConfigurationEntry
	p := s3control.NewListStorageLensConfigurationsPaginator(client, &s3control.ListStorageLensConfigurationsInput{AccountId: &acct.ID})
	for p.HasMorePages() {
		out, apiErr := p.NextPage(ctx)
		if apiErr != nil {
			if isAccessDenied(apiErr) {
				return 0, 0, skipIfAccessDenied(st, "s3control:ListStorageLensConfigurations", acct.ID, region, apiErr)
			}
			// S3 Control returns an untyped XML error body that smithy maps to the
			// generic UnknownError code; a 403 here is a permission signal — warn
			// rather than abort the region.
			if c, ok := httpStatusCode(apiErr); ok && c == 403 {
				return 0, 0, skipIfAccessDenied(st, "s3control:ListStorageLensConfigurations", acct.ID, region, apiErr)
			}
			return 0, 0, fmt.Errorf("s3control:ListStorageLensConfigurations: %w", apiErr)
		}
		entries = append(entries, out.StorageLensConfigurationList...)
	}
	if len(entries) == 0 {
		return 0, 0, nil
	}

	// 2. Fan-out Get per entry.
	sem := semaphore.NewWeighted(fanoutMed)
	var (
		mu    sync.Mutex
		batch []*store.Resource
	)
	g, gctx := errgroup.WithContext(ctx)
	for _, e := range entries {
		g.Go(func() error {
			if err := sem.Acquire(gctx, 1); err != nil {
				return err
			}
			defer sem.Release(1)
			id := sv(e.Id)
			arn := sv(e.StorageLensArn)
			out, gerr := client.GetStorageLensConfiguration(gctx, &s3control.GetStorageLensConfigurationInput{
				AccountId: &acct.ID,
				ConfigId:  &id,
			})
			if gerr != nil {
				if isAccessDenied(gerr) {
					return nil
				}
				return fmt.Errorf("s3control:GetStorageLensConfiguration %s: %w", id, gerr)
			}
			if out.StorageLensConfiguration == nil {
				return nil
			}
			res := &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeS3StorageLens,
				NativeID:       arn,
				Name:           &id,
				Region:         &region,
				AttributesJSON: mustJSON(out.StorageLensConfiguration),
				DiscoveredBy:   scanID,
				// Id "default-account-dashboard" is the AWS-managed default Storage
				// Lens dashboard present in every account.
				ManagedByProvider: id == "default-account-dashboard",
			}
			mu.Lock()
			batch = append(batch, res)
			mu.Unlock()
			return nil
		})
	}
	if werr := g.Wait(); werr != nil {
		return 0, 0, werr
	}
	if len(batch) > 0 {
		n, uerr := st.UpsertResources(batch)
		if uerr != nil {
			return 0, 0, fmt.Errorf("upsert S3 Storage Lens configurations: %w", uerr)
		}
		total = len(batch)
		inserted = n
	}
	return
}

// scanStorageLensGroups lists S3 Storage Lens groups for the given region.
func scanStorageLensGroups(ctx context.Context, acct *account, region string, client s3controlAPI, st *store.Store, scanID string) (total, inserted int, err error) {
	var batch []*store.Resource
	p := s3control.NewListStorageLensGroupsPaginator(client, &s3control.ListStorageLensGroupsInput{AccountId: &acct.ID})
	for p.HasMorePages() {
		out, apiErr := p.NextPage(ctx)
		if apiErr != nil {
			// Storage Lens groups live only in the account's supported home Region;
			// other regions reject with this message — region gap, not denial, so
			// silent-skip.
			if isAccessDeniedWithMessage(apiErr, "supported home Region") {
				return 0, 0, nil
			}
			if isAccessDenied(apiErr) {
				return 0, 0, skipIfAccessDenied(st, "s3control:ListStorageLensGroups", acct.ID, region, apiErr)
			}
			return 0, 0, fmt.Errorf("s3control:ListStorageLensGroups: %w", apiErr)
		}
		for _, e := range out.StorageLensGroupList {
			arn := sv(e.StorageLensGroupArn)
			name := sv(e.Name)
			batch = append(batch, &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeS3StorageLensGroup,
				NativeID:       arn,
				Name:           &name,
				Region:         &region,
				AttributesJSON: mustJSON(e),
				DiscoveredBy:   scanID,
			})
		}
	}
	if len(batch) > 0 {
		n, err := st.UpsertResources(batch)
		if err != nil {
			return 0, 0, fmt.Errorf("upsert S3 Storage Lens groups: %w", err)
		}
		total = len(batch)
		inserted = n
	}
	return
}
