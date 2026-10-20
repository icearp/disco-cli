package aws

import (
	"context"
	"errors"
	"fmt"
	"sync"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"

	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeEC2FastSnapshotRestore, Service: "ec2"})
	registerType(restype.Descriptor{Type: TypeEC2FastLaunchImage, Service: "ec2"})
	registerType(restype.Descriptor{Type: TypeEC2AllowedImagesSetting, Service: "ec2", Untaggable: true})
	registerType(restype.Descriptor{Type: TypeEC2ReservedInstancesListing, Service: "ec2"})
	registerType(restype.Descriptor{Type: TypeEC2ServiceLinkVirtualInterface, Service: "ec2"})
}

const (
	ec2FastSnapshotRestoresOp          = "ec2:DescribeFastSnapshotRestores"
	ec2FastLaunchImagesOp              = "ec2:DescribeFastLaunchImages"
	ec2AllowedImagesSettingsOp         = "ec2:GetAllowedImagesSettings"
	ec2ReservedInstancesListingsOp     = "ec2:DescribeReservedInstancesListings"
	ec2ServiceLinkVirtualInterfacesOp  = "ec2:DescribeServiceLinkVirtualInterfaces"
	ec2FastSnapshotRestoresPageLimit   = 200  // DescribeFastSnapshotRestoresMaxResults maximum
	ec2FastLaunchImagesPageLimit       = 200  // DescribeFastLaunchImagesRequestMaxResults maximum
	ec2ServiceLinkVirtualIfacePageSize = 1000 // ServiceLinkMaxResults maximum
)

// scanEC2Late runs the phases scanEC2 calls after its category scanners,
// because runScanners cancels every category on the first error, and because
// the child phases read the parents those categories stored. The phases are
// independent and run concurrently under a WaitGroup, not an errgroup, so one
// failure cancels no sibling. Each has its transient error demoted to a
// warning (one transient member would make the dispatcher read the whole join
// as transient), and the errors are joined in phase order.
func scanEC2Late(ctx context.Context, client ec2API, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	phases := []struct {
		op  string
		run func(context.Context, ec2API, *account, string, *store.Store, string) (int, int, error)
	}{
		{ec2FastSnapshotRestoresOp, scanEC2FastSnapshotRestores},
		{ec2FastLaunchImagesOp, scanEC2FastLaunchImages},
		{ec2AllowedImagesSettingsOp, scanEC2AllowedImagesSetting},
		{ec2ReservedInstancesListingsOp, scanEC2ReservedInstancesListings},
		{ec2ServiceLinkVirtualInterfacesOp, scanEC2ServiceLinkVirtualInterfaces},
		{ec2TGWPolicyTableEntriesOp, scanEC2TGWPolicyTableEntries},
		{ec2TGWPrefixListReferencesOp, scanEC2TGWPrefixListReferences},
		{ec2SnapshotAttributeOp, scanEC2SnapshotAttributes},
		{ec2ImageAttributeOp, scanEC2ImageAttributes},
		{ec2LockedSnapshotsOp, scanEC2LockedSnapshots},
		{ec2VALoggingConfigurationsOp, scanEC2VerifiedAccessLoggingConfigurations},
		{ec2ApplicationStatusChecksOp, scanEC2ApplicationStatusChecks},
		{ec2RouteServerAssociationsOp, scanEC2RouteServerAssociations},
		{ec2RouteServerPropagationsOp, scanEC2RouteServerPropagations},
	}
	type result struct {
		total, inserted int
		err             error
	}
	results := make([]result, len(phases))
	var wg sync.WaitGroup
	for i, phase := range phases {
		wg.Go(func() {
			t, n, perr := phase.run(ctx, client, acct, region, st, scanID)
			results[i] = result{t, n, demoteTransient(st, phase.op, acct, region, perr)}
		})
	}
	wg.Wait()
	errs := make([]error, len(results))
	for i, r := range results {
		total += r.total
		inserted += r.inserted
		errs[i] = r.err
	}
	return total, inserted, errors.Join(errs...)
}

// fastSnapshotRestoreNativeID builds the NativeID of fast snapshot restore on
// one snapshot in one Availability Zone. AWS documents no ARN for it; the
// (snapshot, zone) pair is the unit EnableFastSnapshotRestores turns on.
func fastSnapshotRestoreNativeID(region, accountID, snapshotID, zone string) string {
	return ec2ARN(region, accountID, "snapshot", snapshotID) + "/fast-snapshot-restore/" + zone
}

// fastLaunchImageNativeID builds the NativeID of the Windows fast launch
// configuration of one AMI. AWS documents no ARN for it; an AMI has at most
// one.
func fastLaunchImageNativeID(region, accountID, imageID string) string {
	return ec2ARN(region, accountID, "image", imageID) + "/fast-launch"
}

// scanEC2FastSnapshotRestores stores the fast snapshot restore state of each
// (snapshot, Availability Zone) pair this account enabled it for. The owner-id
// filter (the account that enabled it) keeps out pairs another account enabled
// on a snapshot shared with it, which would otherwise share this account's
// NativeID for the pair. A disabled pair is dropped; a disabling one is kept
// until it reaches disabled. The zone comes from AvailabilityZone alone: an
// entry without it is dropped, not keyed by AvailabilityZoneId, so the
// NativeID has one source.
func scanEC2FastSnapshotRestores(ctx context.Context, client ec2API, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	in := &ec2.DescribeFastSnapshotRestoresInput{Filters: []ec2types.Filter{{Name: sdkaws.String("owner-id"), Values: []string{acct.ID}}}}
	pager := ec2.NewDescribeFastSnapshotRestoresPaginator(client, in,
		func(o *ec2.DescribeFastSnapshotRestoresPaginatorOptions) { o.Limit = ec2FastSnapshotRestoresPageLimit })
	return ec2PageScan(ctx, ec2FastSnapshotRestoresOp, acct, region, st, pager,
		func(page *ec2.DescribeFastSnapshotRestoresOutput) []*store.Resource {
			var out []*store.Resource
			for _, f := range page.FastSnapshotRestores {
				snapshotID, zone := sv(f.SnapshotId), sv(f.AvailabilityZone)
				if snapshotID == "" || zone == "" || f.State == ec2types.FastSnapshotRestoreStateCodeDisabled {
					continue
				}
				out = append(out, &store.Resource{
					Provider:       "aws",
					AccountID:      acct.ID,
					AccountName:    &acct.Name,
					Type:           TypeEC2FastSnapshotRestore,
					NativeID:       fastSnapshotRestoreNativeID(region, acct.ID, snapshotID, zone),
					Region:         &region,
					Status:         nonEmptyPtr(string(f.State)),
					CreatedAt:      tp(f.EnablingTime),
					AttributesJSON: mustJSON(f),
					DiscoveredBy:   scanID,
				})
			}
			return out
		})
}

// scanEC2FastLaunchImages stores the Windows fast launch configuration of each
// AMI it is enabled for. FastLaunchStateCode has no disabled value, so every
// listed state is kept.
func scanEC2FastLaunchImages(ctx context.Context, client ec2API, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := ec2.NewDescribeFastLaunchImagesPaginator(client, &ec2.DescribeFastLaunchImagesInput{},
		func(o *ec2.DescribeFastLaunchImagesPaginatorOptions) { o.Limit = ec2FastLaunchImagesPageLimit })
	return ec2PageScan(ctx, ec2FastLaunchImagesOp, acct, region, st, pager,
		func(page *ec2.DescribeFastLaunchImagesOutput) []*store.Resource {
			var out []*store.Resource
			for _, f := range page.FastLaunchImages {
				imageID := sv(f.ImageId)
				if imageID == "" {
					continue
				}
				out = append(out, &store.Resource{
					Provider:       "aws",
					AccountID:      acct.ID,
					AccountName:    &acct.Name,
					Type:           TypeEC2FastLaunchImage,
					NativeID:       fastLaunchImageNativeID(region, acct.ID, imageID),
					Region:         &region,
					Status:         nonEmptyPtr(string(f.State)),
					AttributesJSON: mustJSON(f),
					DiscoveredBy:   scanID,
				})
			}
			return out
		})
}

// scanEC2AllowedImagesSetting stores the Allowed AMIs setting, one per account
// and Region. It cannot be created or deleted, only change state (the API
// documents "disabled" as the state in which all AMIs are allowed). It is not
// Managed: that state is the customer's choice, and Managed rows are left out
// of default `disco check` input. AWS documents no ARN for it; the NativeID is
// synthesized in ec2ARN shape and carries the Region, so Regions do not share
// one row.
func scanEC2AllowedImagesSetting(ctx context.Context, client ec2API, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	out, err := client.GetAllowedImagesSettings(ctx, &ec2.GetAllowedImagesSettingsInput{})
	if err != nil {
		if isAccessDenied(err) {
			return 0, 0, skipIfAccessDenied(st, ec2AllowedImagesSettingsOp, acct.ID, region, err)
		}
		if isEC2RegionFeatureGap(err) {
			return 0, 0, nil
		}
		return 0, 0, fmt.Errorf("%s: %w", ec2AllowedImagesSettingsOp, err)
	}
	return upsertBatch(st, []*store.Resource{{
		Provider:       "aws",
		AccountID:      acct.ID,
		AccountName:    &acct.Name,
		Type:           TypeEC2AllowedImagesSetting,
		NativeID:       ec2ARN(region, acct.ID, "allowed-images-setting", acct.ID),
		Region:         &region,
		Status:         out.State,
		AttributesJSON: mustJSON(out),
		DiscoveredBy:   scanID,
	}}, ec2AllowedImagesSettingsOp)
}

// scanEC2ReservedInstancesListings stores the account's Reserved Instance
// Marketplace listings that are still open. Cancelled and closed listings are
// sale history, not live resources: the status filter keeps the unpaginated
// response from carrying that whole history. AWS documents no ARN for a
// listing; the NativeID is synthesized in ec2ARN shape from the listing id.
func scanEC2ReservedInstancesListings(ctx context.Context, client ec2API, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	out, err := client.DescribeReservedInstancesListings(ctx, &ec2.DescribeReservedInstancesListingsInput{
		Filters: []ec2types.Filter{{
			Name:   sdkaws.String("status"),
			Values: []string{string(ec2types.ListingStatusActive), string(ec2types.ListingStatusPending)},
		}},
	})
	if err != nil {
		if isAccessDenied(err) {
			return 0, 0, skipIfAccessDenied(st, ec2ReservedInstancesListingsOp, acct.ID, region, err)
		}
		if isEC2RegionFeatureGap(err) {
			return 0, 0, nil
		}
		return 0, 0, fmt.Errorf("%s: %w", ec2ReservedInstancesListingsOp, err)
	}
	var batch []*store.Resource
	for _, l := range out.ReservedInstancesListings {
		id := sv(l.ReservedInstancesListingId)
		// Kept beside the status filter: whether the API applies it to every
		// listing is unverified, and a closed listing must not be stored.
		if id == "" || l.Status == ec2types.ListingStatusCancelled || l.Status == ec2types.ListingStatusClosed {
			continue
		}
		batch = append(batch, &store.Resource{
			Provider:       "aws",
			AccountID:      acct.ID,
			AccountName:    &acct.Name,
			Type:           TypeEC2ReservedInstancesListing,
			NativeID:       ec2ARN(region, acct.ID, "reserved-instances-listing", id),
			Region:         &region,
			Status:         nonEmptyPtr(string(l.Status)),
			CreatedAt:      tp(l.CreateDate),
			TagsJSON:       awsTagsJSON(l.Tags),
			AttributesJSON: mustJSON(l),
			DiscoveredBy:   scanID,
		})
	}
	return upsertBatch(st, batch, ec2ReservedInstancesListingsOp)
}

// scanEC2ServiceLinkVirtualInterfaces stores the Outpost service link virtual
// interfaces, keyed by the ARN the API returns. Deleted interfaces are
// dropped. DescribeServiceLinkVirtualInterfaces has no SDK paginator.
func scanEC2ServiceLinkVirtualInterfaces(ctx context.Context, client ec2API, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var children []containedChild
	var token *string
	for {
		page, err := client.DescribeServiceLinkVirtualInterfaces(ctx, &ec2.DescribeServiceLinkVirtualInterfacesInput{
			MaxResults: sdkaws.Int32(ec2ServiceLinkVirtualIfacePageSize),
			NextToken:  token,
		})
		if err != nil {
			if token == nil && isEC2RegionFeatureGap(err) {
				return 0, 0, nil
			}
			return upsertRegionChildren(st, acct, region, ec2ServiceLinkVirtualInterfacesOp, children, err)
		}
		for _, vif := range page.ServiceLinkVirtualInterfaces {
			arn := sv(vif.ServiceLinkVirtualInterfaceArn)
			if arn == "" || vif.ConfigurationState == ec2types.ServiceLinkVirtualInterfaceConfigurationStateDeleted {
				continue
			}
			children = append(children, containedChild{row: &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeEC2ServiceLinkVirtualInterface,
				NativeID:       arn,
				Name:           ec2TagName(vif.Tags),
				Region:         &region,
				Status:         nonEmptyPtr(string(vif.ConfigurationState)),
				TagsJSON:       awsTagsJSON(vif.Tags),
				AttributesJSON: mustJSON(vif),
				DiscoveredBy:   scanID,
			}})
		}
		next := sv(page.NextToken)
		if next == "" || next == sv(token) {
			break
		}
		token = page.NextToken
	}
	return upsertRegionChildren(st, acct, region, ec2ServiceLinkVirtualInterfacesOp, children, nil)
}
