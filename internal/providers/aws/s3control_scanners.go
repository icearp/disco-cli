package aws

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"codeburg.org/icearp/disco/internal/store"
	"github.com/aws/aws-sdk-go-v2/service/s3control"
	s3ctypes "github.com/aws/aws-sdk-go-v2/service/s3control/types"
	smithy "github.com/aws/smithy-go"
	"golang.org/x/sync/errgroup"
	"golang.org/x/sync/semaphore"
)

func init() {
	registerService(serviceEntry{
		name:   "aws:s3control",
		global: false,
		fn:     scanS3Control,
	})
}

// scanS3Control discovers S3 Control resources for the given region.
// Regional resources (Access Grants, Access Points, Storage Lens) are scanned
// in every region. Global resources (Multi-Region Access Points) are only
// scanned in us-east-1.
func scanS3Control(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := s3control.NewFromConfig(acct.cfg, func(o *s3control.Options) { o.Region = region })
	// Regional resources: scan in every region.
	for _, scan := range []func(context.Context, *account, string, *s3control.Client, *store.Store, string) (int, int, error){
		scanAccessGrantsInstances,
		scanAccessGrantsLocations,
		scanAccessGrants,
		scanS3AccessPoints,
		scanStorageLens,
		scanStorageLensGroups,
	} {
		tt, nn, e := scan(ctx, acct, region, client, st, scanID)
		if e != nil {
			return total, inserted, e
		}
		total += tt
		inserted += nn
	}
	// Multi-Region Access Points are a global resource but the API endpoint is
	// only available in us-west-2. Scan once, using a client pinned to that region.
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
func scanAccessGrantsInstances(ctx context.Context, acct *account, region string, client *s3control.Client, st *store.Store, scanID string) (total, inserted int, err error) {
	var batch []*store.Resource
	var nextToken *string
	for {
		out, apiErr := client.ListAccessGrantsInstances(ctx, &s3control.ListAccessGrantsInstancesInput{
			AccountId: &acct.ID,
			NextToken: nextToken,
		})
		if apiErr != nil {
			if isAccessDenied(apiErr) {
				return 0, 0, skipIfAccessDenied("s3control:ListAccessGrantsInstances", acct.ID, region, apiErr)
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
		if out.NextToken == nil {
			break
		}
		nextToken = out.NextToken
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

// isAccessGrantsInstanceNotExists reports whether err indicates that no S3
// Access Grants instance exists in the region. ListAccessGrantsLocations and
// ListAccessGrants both return this 404 error when no instance is present.
func isAccessGrantsInstanceNotExists(err error) bool {
	var ae smithy.APIError
	return errors.As(err, &ae) && ae.ErrorCode() == "AccessGrantsInstanceNotExistsError"
}

// scanAccessGrantsLocations lists the registered locations in the account's
// S3 Access Grants instance for the given region.
func scanAccessGrantsLocations(ctx context.Context, acct *account, region string, client *s3control.Client, st *store.Store, scanID string) (total, inserted int, err error) {
	var batch []*store.Resource
	var nextToken *string
	for {
		out, apiErr := client.ListAccessGrantsLocations(ctx, &s3control.ListAccessGrantsLocationsInput{
			AccountId: &acct.ID,
			NextToken: nextToken,
		})
		if apiErr != nil {
			if isAccessGrantsInstanceNotExists(apiErr) {
				return 0, 0, nil // no instance in this region — expected
			}
			if isAccessDenied(apiErr) {
				return 0, 0, skipIfAccessDenied("s3control:ListAccessGrantsLocations", acct.ID, region, apiErr)
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
		if out.NextToken == nil {
			break
		}
		nextToken = out.NextToken
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
func scanAccessGrants(ctx context.Context, acct *account, region string, client *s3control.Client, st *store.Store, scanID string) (total, inserted int, err error) {
	var batch []*store.Resource
	var nextToken *string
	for {
		out, apiErr := client.ListAccessGrants(ctx, &s3control.ListAccessGrantsInput{
			AccountId: &acct.ID,
			NextToken: nextToken,
		})
		if apiErr != nil {
			if isAccessGrantsInstanceNotExists(apiErr) {
				return 0, 0, nil // no instance in this region — expected
			}
			if isAccessDenied(apiErr) {
				return 0, 0, skipIfAccessDenied("s3control:ListAccessGrants", acct.ID, region, apiErr)
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
		if out.NextToken == nil {
			break
		}
		nextToken = out.NextToken
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
func scanS3AccessPoints(ctx context.Context, acct *account, region string, client *s3control.Client, st *store.Store, scanID string) (total, inserted int, err error) {
	var batch []*store.Resource
	var nextToken *string
	for {
		out, apiErr := client.ListAccessPoints(ctx, &s3control.ListAccessPointsInput{
			AccountId: &acct.ID,
			NextToken: nextToken,
		})
		if apiErr != nil {
			if isAccessDenied(apiErr) {
				return 0, 0, skipIfAccessDenied("s3control:ListAccessPoints", acct.ID, region, apiErr)
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
		if out.NextToken == nil {
			break
		}
		nextToken = out.NextToken
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
func scanMultiRegionAccessPoints(ctx context.Context, acct *account, client *s3control.Client, st *store.Store, scanID string) (total, inserted int, err error) {
	var mraps []s3ctypes.MultiRegionAccessPointReport
	var nextToken *string
	for {
		out, apiErr := client.ListMultiRegionAccessPoints(ctx, &s3control.ListMultiRegionAccessPointsInput{
			AccountId: &acct.ID,
			NextToken: nextToken,
		})
		if apiErr != nil {
			if isAccessDenied(apiErr) {
				return 0, 0, skipIfAccessDenied("s3control:ListMultiRegionAccessPoints", acct.ID, "global", apiErr)
			}
			return 0, 0, fmt.Errorf("s3control:ListMultiRegionAccessPoints: %w", apiErr)
		}
		mraps = append(mraps, out.AccessPoints...)
		if out.NextToken == nil {
			break
		}
		nextToken = out.NextToken
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
func scanMRAPPolicies(ctx context.Context, acct *account, client *s3control.Client, mraps []s3ctypes.MultiRegionAccessPointReport, st *store.Store, scanID string) (total, inserted int, err error) {
	const maxConcurrent = 10
	sem := semaphore.NewWeighted(maxConcurrent)
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

// scanStorageLens lists S3 Storage Lens configurations for the given region.
func scanStorageLens(ctx context.Context, acct *account, region string, client *s3control.Client, st *store.Store, scanID string) (total, inserted int, err error) {
	var batch []*store.Resource
	var nextToken *string
	for {
		out, apiErr := client.ListStorageLensConfigurations(ctx, &s3control.ListStorageLensConfigurationsInput{
			AccountId: &acct.ID,
			NextToken: nextToken,
		})
		if apiErr != nil {
			if isAccessDenied(apiErr) {
				return 0, 0, skipIfAccessDenied("s3control:ListStorageLensConfigurations", acct.ID, region, apiErr)
			}
			return 0, 0, fmt.Errorf("s3control:ListStorageLensConfigurations: %w", apiErr)
		}
		for _, e := range out.StorageLensConfigurationList {
			arn := sv(e.StorageLensArn)
			id := sv(e.Id)
			batch = append(batch, &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeS3StorageLens,
				NativeID:       arn,
				Name:           &id,
				Region:         &region,
				AttributesJSON: mustJSON(e),
				DiscoveredBy:   scanID,
			})
		}
		if out.NextToken == nil {
			break
		}
		nextToken = out.NextToken
	}
	if len(batch) > 0 {
		n, err := st.UpsertResources(batch)
		if err != nil {
			return 0, 0, fmt.Errorf("upsert S3 Storage Lens configurations: %w", err)
		}
		total = len(batch)
		inserted = n
	}
	return
}

// scanStorageLensGroups lists S3 Storage Lens groups for the given region.
func scanStorageLensGroups(ctx context.Context, acct *account, region string, client *s3control.Client, st *store.Store, scanID string) (total, inserted int, err error) {
	var batch []*store.Resource
	var nextToken *string
	for {
		out, apiErr := client.ListStorageLensGroups(ctx, &s3control.ListStorageLensGroupsInput{
			AccountId: &acct.ID,
			NextToken: nextToken,
		})
		if apiErr != nil {
			if isAccessDenied(apiErr) {
				return 0, 0, skipIfAccessDenied("s3control:ListStorageLensGroups", acct.ID, region, apiErr)
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
		if out.NextToken == nil {
			break
		}
		nextToken = out.NextToken
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
