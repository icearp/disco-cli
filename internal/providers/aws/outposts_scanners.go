package aws

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/service/outposts"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerType(restype.Descriptor{Type: TypeOutpostsSite, Service: "outposts", Leaf: true})
	registerType(restype.Descriptor{Type: TypeOutpostsOutpost, Service: "outposts"})
	registerService(serviceEntry{
		name: "aws:outposts",
		fn:   scanOutposts,
	})
}

type outpostsAPI interface {
	ListSites(context.Context, *outposts.ListSitesInput, ...func(*outposts.Options)) (*outposts.ListSitesOutput, error)
	ListOutposts(context.Context, *outposts.ListOutpostsInput, ...func(*outposts.Options)) (*outposts.ListOutpostsOutput, error)
}

// scanOutposts discovers AWS Outposts sites and outposts.
func scanOutposts(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := outposts.NewFromConfig(acct.cfg, func(o *outposts.Options) { o.Region = region })

	for _, phase := range []func() (int, int, error){
		func() (int, int, error) { return scanOutpostsSites(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanOutpostsOutposts(ctx, client, acct, region, st, scanID) },
	} {
		t, i, perr := phase()
		if perr != nil {
			return total, inserted, perr
		}
		total += t
		inserted += i
	}
	return total, inserted, nil
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

func scanOutpostsOutposts(ctx context.Context, client outpostsAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := outposts.NewListOutpostsPaginator(client, &outposts.ListOutpostsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "outposts:ListOutposts", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("outposts:ListOutposts: %w", err)
		}
		for _, o := range out.Outposts {
			arn := sv(o.OutpostArn)
			if arn == "" {
				continue
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
	return upsertBatch(st, batch, "outposts outpost resources")
}
