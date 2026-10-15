package aws

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/service/outposts"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeOutpostsSite, Service: "outposts"})
	registerType(restype.Descriptor{Type: TypeOutpostsOutpost, Service: "outposts"})
	registerType(restype.Descriptor{Type: TypeOutpostsAsset, Service: "outposts"})
	registerService(serviceEntry{
		name: "aws:outposts",
		fn:   scanOutposts,
	})
}

type outpostsAPI interface {
	ListSites(context.Context, *outposts.ListSitesInput, ...func(*outposts.Options)) (*outposts.ListSitesOutput, error)
	ListOutposts(context.Context, *outposts.ListOutpostsInput, ...func(*outposts.Options)) (*outposts.ListOutpostsOutput, error)
	ListAssets(context.Context, *outposts.ListAssetsInput, ...func(*outposts.Options)) (*outposts.ListAssetsOutput, error)
}

const (
	outpostsListAssetsOp = "outposts:ListAssets"
	// outpostsAssetPageLimit is the model's MaxResults maximum for ListAssets.
	outpostsAssetPageLimit = 1000
)

// scanOutposts discovers AWS Outposts sites, outposts and their hardware
// assets. ListAssets is called per outpost, over the outposts this account
// owns that ListOutposts returned. A ListSites or ListOutposts error returns
// before the asset phase. A ListAssets error keeps the site and outpost rows
// already stored.
func scanOutposts(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	client := outposts.NewFromConfig(acct.cfg, func(o *outposts.Options) { o.Region = region })
	total, inserted, err := scanOutpostsSites(ctx, client, acct, region, st, scanID)
	if err != nil {
		return total, inserted, err
	}
	parents, t, i, err := scanOutpostsOutposts(ctx, client, acct, region, st, scanID)
	total += t
	inserted += i
	if err != nil {
		return total, inserted, err
	}
	t, i, err = scanOutpostsAssets(ctx, client, acct, region, st, scanID, parents)
	return total + t, inserted + i, err
}

func scanOutpostsSites(ctx context.Context, client outpostsAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := outposts.NewListSitesPaginator(client, &outposts.ListSitesInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "outposts:ListSites", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("outposts:ListSites: %w", err)
		}
		for _, s := range out.Sites {
			arn := sv(s.SiteArn)
			if arn == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeOutpostsSite, NativeID: arn,
				Name: s.Name, Region: &region,
				AttributesJSON: mustJSON(s), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "outposts sites")
}

func scanOutpostsOutposts(ctx context.Context, client outpostsAPI, acct *account, region string, st *store.Store, scanID string) ([]childParent, int, int, error) {
	pager := outposts.NewListOutpostsPaginator(client, &outposts.ListOutpostsInput{})
	var (
		batch   []*store.Resource
		parents []childParent
	)
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return nil, 0, 0, skipIfAccessDenied(st, "outposts:ListOutposts", acct.ID, region, err)
			}
			return nil, 0, 0, fmt.Errorf("outposts:ListOutposts: %w", err)
		}
		for _, o := range out.Outposts {
			arn := sv(o.OutpostArn)
			if arn == "" {
				continue
			}
			// A RAM-shared outpost belongs to another account: its hardware is
			// the owner's inventory, and listing it here would store the
			// owner's assets under this account (or fail the consumer's call).
			if owner := sv(o.OwnerId); owner == "" || owner == acct.ID {
				parents = append(parents, childParent{id: arn, arn: arn})
			}
			status := sv(o.LifeCycleStatus)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeOutpostsOutpost, NativeID: arn,
				Name: o.Name, Region: &region, Status: &status,
				AttributesJSON: mustJSON(o), DiscoveredBy: scanID,
			})
		}
	}
	t, i, err := upsertBatch(st, batch, "outposts outpost resources")
	return parents, t, i, err
}

// outpostsAssetNativeID builds the NativeID of a hardware asset. ListAssets
// issues no ARN and the Service Reference defines no asset resource, so the id
// is scoped under the outpost ARN; no API documents an asset id as unique
// beyond its outpost.
func outpostsAssetNativeID(outpostARN, assetID string) string {
	return outpostARN + "/asset/" + assetID
}

// scanOutpostsAssets stores each outpost's hardware assets. The ListAssets
// model documents no form-factor restriction (AssetInfo.AssetId covers "a
// single server within an Outposts rack or an Outposts server
// configuration"), so a ValidationException is returned like any other error.
// An outpost deleted since it was listed (NotFoundException) is skipped; one
// whose call still fails transiently after the SDK's retries is skipped,
// keeping its siblings' rows, with one summary warning.
func scanOutpostsAssets(ctx context.Context, client outpostsAPI, acct *account, region string, st *store.Store, scanID string, outpostList []childParent) (int, int, error) {
	var transient transientTally
	isGone := func(err error) bool { return isAPIErrorCode(err, "NotFoundException") }
	total, inserted, err := childFanOut(ctx, st, acct, region, outpostsListAssetsOp, outpostList, isGone,
		func(ctx context.Context, outpost childParent) ([]*store.Resource, error) {
			rows, err := listOutpostsAssetRows(ctx, client, acct, region, scanID, outpost)
			if isTransientNetworkError(err) {
				transient.skipped(outpost.arn, err)
				return nil, nil
			}
			return rows, err
		})
	transient.warnSkipped(st, outpostsListAssetsOp, acct, region)
	return total, inserted, err
}

func listOutpostsAssetRows(ctx context.Context, client outpostsAPI, acct *account, region, scanID string, outpost childParent) ([]*store.Resource, error) {
	pager := outposts.NewListAssetsPaginator(client,
		&outposts.ListAssetsInput{OutpostIdentifier: &outpost.id},
		func(o *outposts.ListAssetsPaginatorOptions) { o.Limit = outpostsAssetPageLimit })
	var rows []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			return nil, err
		}
		for _, a := range out.Assets {
			id := sv(a.AssetId)
			if id == "" {
				continue
			}
			var status *string
			if a.ComputeAttributes != nil {
				status = nonEmptyPtr(string(a.ComputeAttributes.State))
			}
			rows = append(rows, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeOutpostsAsset, NativeID: outpostsAssetNativeID(outpost.arn, id),
				Name: &id, Region: &region, Status: status,
				AttributesJSON: mustJSON(a), DiscoveredBy: scanID,
			})
		}
	}
	return rows, nil
}
