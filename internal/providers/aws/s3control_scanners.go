package aws

import (
	"cmp"
	"context"
	"fmt"
	"sync"
	"time"

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
	ListTagsForResource(context.Context, *s3control.ListTagsForResourceInput, ...func(*s3control.Options)) (*s3control.ListTagsForResourceOutput, error)
	GetStorageLensConfigurationTagging(context.Context, *s3control.GetStorageLensConfigurationTaggingInput, ...func(*s3control.Options)) (*s3control.GetStorageLensConfigurationTaggingOutput, error)
}

func init() {
	registerType(restype.Descriptor{Type: TypeS3AccessGrantsInstance, Service: "s3"})
	registerType(restype.Descriptor{Type: TypeS3AccessGrantsLocation, Service: "s3"})
	registerType(restype.Descriptor{Type: TypeS3AccessGrant, Service: "s3"})
	registerType(restype.Descriptor{Type: TypeS3AccessPoint, Service: "s3"})
	registerType(restype.Descriptor{Type: TypeS3MultiRegionAccessPoint, Service: "s3", Untaggable: true})
	registerType(restype.Descriptor{Type: TypeS3MultiRegionAccessPointPolicy, Service: "s3"})
	registerType(restype.Descriptor{Type: TypeS3StorageLens, Service: "s3"})
	registerType(restype.Descriptor{Type: TypeS3StorageLensGroup, Service: "s3"})
	registerType(restype.Descriptor{Type: TypeS3ObjectLambdaAccessPoint, Service: "s3-object-lambda"})
	registerType(restype.Descriptor{Type: TypeS3ObjectLambdaAccessPointPolicy, Service: "s3-object-lambda"})
	registerService(serviceEntry{
		name:   "aws:s3control",
		global: false,
		fn:     scanS3Control,
	})
}

const (
	// s3controlPageCap is the model's MaxResults cap (0 to 1000) of the
	// Access Grants and access point listings.
	s3controlPageCap = 1000

	s3controlTagsOp = "s3control:ListTagsForResource"
	s3LensTagsOp    = "s3control:GetStorageLensConfigurationTagging"
)

// scanS3Control discovers S3 Control resources for the given region, one
// listing or policy read at a time: a phase's error does not stop the later
// ones; a failed write and ctx's end do; a region whose first listing finds
// no endpoint runs no other (runHostPhases). Regional resources (Access Grants, Access Points,
// Storage Lens) scan in every region; global resources (Multi-Region Access
// Points) scan only in us-west-2, and only for aws accounts. The Access
// Grants, access point and Storage Lens group rows have their tags read with
// ListTagsForResource, whose denials are one warning for the scan; Storage
// Lens configurations with GetStorageLensConfigurationTagging.
func scanS3Control(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	client := newS3ControlClient(acct.cfg, region)
	tags := s3controlTags(client, acct.ID).sharingDenials()
	// Each policy read is its own phase, so its errors are reported under
	// its own op rather than its listing's.
	var (
		objectLambdaAPs []s3ctypes.ObjectLambdaAccessPoint
		mraps           []s3ctypes.MultiRegionAccessPointReport
	)
	phases := []hostPhase{
		{op: "s3control:ListAccessGrantsInstances", run: func(p *phaseTotals) bool {
			return scanAccessGrantsInstances(ctx, acct, region, client, st, scanID, tags, p)
		}},
		{op: "s3control:ListAccessGrantsLocations", run: func(p *phaseTotals) bool {
			return scanAccessGrantsLocations(ctx, acct, region, client, st, scanID, tags, p)
		}},
		{op: "s3control:ListAccessGrants", run: func(p *phaseTotals) bool {
			return scanAccessGrants(ctx, acct, region, client, st, scanID, tags, p)
		}},
		{op: "s3control:ListAccessPoints", run: func(p *phaseTotals) bool {
			return scanS3AccessPoints(ctx, acct, region, client, st, scanID, tags, p)
		}},
		{op: "s3control:ListStorageLensConfigurations", run: func(p *phaseTotals) bool {
			return scanStorageLens(ctx, acct, region, client, st, scanID, p)
		}},
		{op: "s3control:ListStorageLensGroups", run: func(p *phaseTotals) bool {
			return scanStorageLensGroups(ctx, acct, region, client, st, scanID, tags, p)
		}},
		countedPhase(0, "s3control:ListAccessPointsForObjectLambda", func() (total, inserted int, err error) {
			objectLambdaAPs, total, inserted, err = scanS3ObjectLambdaAccessPoints(ctx, acct, region, client, st, scanID)
			return total, inserted, err
		}),
		countedPhase(0, "s3control:GetAccessPointPolicyForObjectLambda", func() (int, int, error) {
			return scanS3ObjectLambdaAccessPointPolicies(ctx, acct, region, client, objectLambdaAPs, st, scanID)
		}),
	}
	// MRAPs are a global resource but the commercial API endpoint is only
	// available in us-west-2 — scan once, from that region's client.
	// No SDK table names the MRAP endpoint of any partition (s3control's own
	// home, us-east-1, is not it), and us-west-2 belongs to aws alone, so
	// accounts in other partitions make no MRAP call.
	if region == "us-west-2" {
		phases = append(phases,
			countedPhase(0, "s3control:ListMultiRegionAccessPoints", func() (total, inserted int, err error) {
				mraps, total, inserted, err = scanMultiRegionAccessPoints(ctx, acct, client, st, scanID)
				return total, inserted, err
			}),
			countedPhase(0, "s3control:GetMultiRegionAccessPointPolicy", func() (int, int, error) {
				return scanMRAPPolicies(ctx, acct, client, mraps, st, scanID)
			}))
	}
	total, inserted, err := runHostPhases(ctx, st, acct, region, "s3control", 0, phases)
	tags.warnDenials(st, acct, region)
	return total, inserted, err
}

// s3controlTags reads a row's tags with ListTagsForResource, sent the row's
// NativeID, the ARN its list element carries, and the scanned account, which
// every listing read here is filtered to; the op answers with every tag at
// once (no page token). Its model lists no error code, so a resource deleted
// between its listing and this read is kept and the error returned.
func s3controlTags(client s3controlAPI, accountID string) rowTagReader {
	return rowTagReader{
		op: s3controlTagsOp,
		read: func(ctx context.Context, r *store.Resource) (*string, error) {
			out, err := client.ListTagsForResource(ctx, &s3control.ListTagsForResourceInput{AccountId: &accountID, ResourceArn: &r.NativeID})
			if err != nil {
				return nil, err
			}
			return kvTagsJSON(out.Tags, func(t s3ctypes.Tag) (*string, *string) { return t.Key, t.Value }), nil
		},
	}
}

// s3controlRow is the row of a listed item stored under the ARN it carries,
// the item verbatim as the attributes. An item without an ARN is not stored:
// one empty NativeID would merge every such item into one resource; what
// names the items in that warning, key the item.
func s3controlRow(acct *account, region, scanID, typ, what string, arn *string, name string, created *time.Time, key string, item any) (*store.Resource, string, string) {
	if sv(arn) == "" {
		return nil, what + " have an empty ARN", key
	}
	return &store.Resource{
		Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
		Type: typ, NativeID: sv(arn), Name: &name, Region: &region,
		CreatedAt: tp(created), AttributesJSON: mustJSON(item), DiscoveredBy: scanID,
	}, "", ""
}

// isNoAccessGrantsInstance reports the error the Access Grants location and
// grant listings answer with in a region where the account has no Access
// Grants instance.
func isNoAccessGrantsInstance(err error) bool {
	return isAPIErrorCode(err, "AccessGrantsInstanceNotExistsError")
}

// scanAccessGrantsInstances stores the region's S3 Access Grants instance
// with its tags. There is at most one instance per region per account.
func scanAccessGrantsInstances(ctx context.Context, acct *account, region string, client s3controlAPI, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) (storeWriteFailed bool) {
	return storeTokenPagesTags(ctx, st, acct, region, "s3control:ListAccessGrantsInstances",
		func(ctx context.Context, token *string) ([]s3ctypes.ListAccessGrantsInstanceEntry, *string, error) {
			out, err := client.ListAccessGrantsInstances(ctx, &s3control.ListAccessGrantsInstancesInput{AccountId: &acct.ID, NextToken: token, MaxResults: s3controlPageCap})
			if err != nil {
				return nil, nil, err
			}
			return out.AccessGrantsInstancesList, out.NextToken, nil
		},
		func(e s3ctypes.ListAccessGrantsInstanceEntry) (*store.Resource, string, string) {
			id := sv(e.AccessGrantsInstanceId)
			return s3controlRow(acct, region, scanID, TypeS3AccessGrantsInstance, "access grants instances",
				e.AccessGrantsInstanceArn, id, e.CreatedAt, cmp.Or(id, "(an access grants instance with no id)"), e)
		}, tags, p)
}

// scanAccessGrantsLocations stores the locations registered in the account's
// S3 Access Grants instance for the region, with their tags, a page at a
// time. A region without an instance has none.
func scanAccessGrantsLocations(ctx context.Context, acct *account, region string, client s3controlAPI, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) (storeWriteFailed bool) {
	return storeTokenPagesTags(ctx, st, acct, region, "s3control:ListAccessGrantsLocations",
		firstPageGapPages(isNoAccessGrantsInstance, func(ctx context.Context, token *string) ([]s3ctypes.ListAccessGrantsLocationsEntry, *string, error) {
			out, err := client.ListAccessGrantsLocations(ctx, &s3control.ListAccessGrantsLocationsInput{AccountId: &acct.ID, NextToken: token, MaxResults: s3controlPageCap})
			if err != nil {
				return nil, nil, err
			}
			return out.AccessGrantsLocationsList, out.NextToken, nil
		}),
		func(e s3ctypes.ListAccessGrantsLocationsEntry) (*store.Resource, string, string) {
			id := sv(e.AccessGrantsLocationId)
			return s3controlRow(acct, region, scanID, TypeS3AccessGrantsLocation, "access grants locations",
				e.AccessGrantsLocationArn, id, e.CreatedAt, cmp.Or(id, sv(e.LocationScope), "(an access grants location with no id)"), e)
		}, tags, p)
}

// scanAccessGrants stores the grants in the account's S3 Access Grants
// instance for the region, with their tags, a page at a time. A region
// without an instance has none.
func scanAccessGrants(ctx context.Context, acct *account, region string, client s3controlAPI, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) (storeWriteFailed bool) {
	return storeTokenPagesTags(ctx, st, acct, region, "s3control:ListAccessGrants",
		firstPageGapPages(isNoAccessGrantsInstance, func(ctx context.Context, token *string) ([]s3ctypes.ListAccessGrantEntry, *string, error) {
			out, err := client.ListAccessGrants(ctx, &s3control.ListAccessGrantsInput{AccountId: &acct.ID, NextToken: token, MaxResults: s3controlPageCap})
			if err != nil {
				return nil, nil, err
			}
			return out.AccessGrantsList, out.NextToken, nil
		}),
		func(e s3ctypes.ListAccessGrantEntry) (*store.Resource, string, string) {
			id := sv(e.AccessGrantId)
			return s3controlRow(acct, region, scanID, TypeS3AccessGrant, "access grants",
				e.AccessGrantArn, id, e.CreatedAt, cmp.Or(id, sv(e.GrantScope), "(an access grant with no id)"), e)
		}, tags, p)
}

// scanS3AccessPoints stores the region's S3 access points with their tags, a
// page at a time.
func scanS3AccessPoints(ctx context.Context, acct *account, region string, client s3controlAPI, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) (storeWriteFailed bool) {
	return storeTokenPagesTags(ctx, st, acct, region, "s3control:ListAccessPoints",
		func(ctx context.Context, token *string) ([]s3ctypes.AccessPoint, *string, error) {
			out, err := client.ListAccessPoints(ctx, &s3control.ListAccessPointsInput{AccountId: &acct.ID, NextToken: token, MaxResults: s3controlPageCap})
			if err != nil {
				return nil, nil, err
			}
			return out.AccessPointList, out.NextToken, nil
		},
		func(e s3ctypes.AccessPoint) (*store.Resource, string, string) {
			name := sv(e.Name)
			return s3controlRow(acct, region, scanID, TypeS3AccessPoint, "access points",
				e.AccessPointArn, name, nil, cmp.Or(name, "(an access point with no name)"), e)
		}, tags, p)
}

// scanMultiRegionAccessPoints stores all Multi-Region Access Points (global,
// control plane routed through us-west-2 for aws accounts) and returns those
// stored, whose policies scanMRAPPolicies reads.
func scanMultiRegionAccessPoints(ctx context.Context, acct *account, client s3controlAPI, st *store.Store, scanID string) (stored []s3ctypes.MultiRegionAccessPointReport, total, inserted int, err error) {
	var mraps []s3ctypes.MultiRegionAccessPointReport
	p := s3control.NewListMultiRegionAccessPointsPaginator(client, &s3control.ListMultiRegionAccessPointsInput{AccountId: &acct.ID})
	for p.HasMorePages() {
		out, apiErr := p.NextPage(ctx)
		if apiErr != nil {
			if isAccessDenied(apiErr) {
				return nil, 0, 0, skipIfAccessDenied(st, "s3control:ListMultiRegionAccessPoints", acct.ID, "global", apiErr)
			}
			return nil, 0, 0, fmt.Errorf("s3control:ListMultiRegionAccessPoints: %w", apiErr)
		}
		mraps = append(mraps, out.AccessPoints...)
	}

	var batch []*store.Resource
	for _, m := range mraps {
		name := sv(m.Name)
		// MRAPs have no native ARN in the list output; construct a stable one.
		arn := arnFor(acct.partition(), "s3", "", acct.ID, "accesspoint/"+name)
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
			return nil, 0, 0, fmt.Errorf("upsert S3 multi-region access points: %w", err)
		}
		total += len(batch)
		inserted += n
	}
	return mraps, total, inserted, nil
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
			policyARN := arnFor(acct.partition(), "s3", "", acct.ID, "accesspoint/"+name+"/policy")
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

// scanStorageLens stores the region's S3 Storage Lens configurations with
// their tags, a page at a time. The list entry is sparse, so each row is the
// configuration's GetStorageLensConfiguration body (Include/Exclude buckets,
// DataExport target), which resolveStorageLensRelationships needs; a
// configuration whose Get fails keeps its list entry as the row: a denial is
// warned about once, any other failure returned (readRowDetails). A region
// that is not a Storage Lens home Region has none. The row's Name is the
// configuration's Id, which the Get and the tag read are sent.
func scanStorageLens(ctx context.Context, acct *account, region string, client s3controlAPI, st *store.Store, scanID string, p *phaseTotals) (storeWriteFailed bool) {
	const op = "s3control:ListStorageLensConfigurations"
	listDenied := listDenial{isDenied: isStorageLensListDenied, unread: "storage lens configurations on later pages"}
	page := deniedPages(&listDenied, firstPageGapPages(isStorageLensHomeRegionGap,
		func(ctx context.Context, token *string) ([]s3ctypes.ListStorageLensConfigurationEntry, *string, error) {
			out, err := client.ListStorageLensConfigurations(ctx, &s3control.ListStorageLensConfigurationsInput{AccountId: &acct.ID, NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.StorageLensConfigurationList, out.NextToken, nil
		}))
	row := func(e s3ctypes.ListStorageLensConfigurationEntry) (*store.Resource, string, string) {
		id := sv(e.Id)
		return s3controlRow(acct, region, scanID, TypeS3StorageLens, "storage lens configurations",
			e.StorageLensArn, id, nil, cmp.Or(id, "(a storage lens configuration with no id)"), e)
	}
	details := rowDetailReader{
		op: "s3control:GetStorageLensConfiguration", what: "storage lens configurations",
		get: func(ctx context.Context, r *store.Resource) (any, error) {
			out, err := client.GetStorageLensConfiguration(ctx, &s3control.GetStorageLensConfigurationInput{AccountId: &acct.ID, ConfigId: r.Name})
			if err != nil {
				return nil, err
			}
			// A nil pointer in the any would replace the row's attributes
			// with null; untyped nil keeps the list entry.
			if out.StorageLensConfiguration == nil {
				return nil, nil
			}
			return out.StorageLensConfiguration, nil
		},
	}
	if storeTokenPagesTagsWith(ctx, st, acct, region, op, page, row, tokenPageSteps{details: details}, storageLensTags(client, acct.ID), p) {
		return true
	}
	listDenied.warn(st, op, acct, region)
	return false
}

// isStorageLensListDenied is isAccessDenied, plus a 403 whose body smithy
// could not map to a code (a generic UnknownError): a permission signal too.
func isStorageLensListDenied(err error) bool {
	c, ok := httpStatusCode(err)
	return isAccessDenied(err) || (ok && c == 403)
}

// storageLensTags reads a Storage Lens configuration row's tags with
// GetStorageLensConfigurationTagging, sent the row's Name, the configuration
// Id. Its model lists no error code, so a configuration deleted between its
// listing and this read is kept and the error returned.
func storageLensTags(client s3controlAPI, accountID string) rowTagReader {
	return rowTagReader{
		op: s3LensTagsOp,
		read: func(ctx context.Context, r *store.Resource) (*string, error) {
			out, err := client.GetStorageLensConfigurationTagging(ctx, &s3control.GetStorageLensConfigurationTaggingInput{AccountId: &accountID, ConfigId: r.Name})
			if err != nil {
				return nil, err
			}
			return kvTagsJSON(out.Tags, func(t s3ctypes.StorageLensTag) (*string, *string) { return t.Key, t.Value }), nil
		},
	}
}

// scanStorageLensGroups stores the region's S3 Storage Lens groups with their
// tags, a page at a time. A region that is not a Storage Lens home Region has
// none.
func scanStorageLensGroups(ctx context.Context, acct *account, region string, client s3controlAPI, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) (storeWriteFailed bool) {
	return storeTokenPagesTags(ctx, st, acct, region, "s3control:ListStorageLensGroups",
		firstPageGapPages(isStorageLensHomeRegionGap, func(ctx context.Context, token *string) ([]s3ctypes.ListStorageLensGroupEntry, *string, error) {
			out, err := client.ListStorageLensGroups(ctx, &s3control.ListStorageLensGroupsInput{AccountId: &acct.ID, NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.StorageLensGroupList, out.NextToken, nil
		}),
		func(e s3ctypes.ListStorageLensGroupEntry) (*store.Resource, string, string) {
			name := sv(e.Name)
			return s3controlRow(acct, region, scanID, TypeS3StorageLensGroup, "storage lens groups",
				e.StorageLensGroupArn, name, nil, cmp.Or(name, "(a storage lens group with no name)"), e)
		}, tags, p)
}
