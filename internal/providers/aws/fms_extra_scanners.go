package aws

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/service/fms"
	"github.com/icearp/disco-cli/store"
)

// scanFMSAppsLists discovers customer-defined Firewall Manager applications lists
// (DefaultLists=false excludes AWS-managed ones). edge-less — policies reference these,
// no outbound edges.
func scanFMSAppsLists(ctx context.Context, client fmsAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := fms.NewListAppsListsPaginator(client, &fms.ListAppsListsInput{}, func(o *fms.ListAppsListsPaginatorOptions) { o.Limit = 100 })
	var (
		batch   []*store.Resource
		unnamed skipTally
	)
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isFMSNotEnabled(err) {
				return 0, 0, markServiceDisabled(err)
			}
			if isFMSAdminOnlyDenial(err) {
				return 0, 0, nil
			}
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "fms:ListAppsLists", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("fms:ListAppsLists: %w", err)
		}
		for _, a := range out.AppsLists {
			arn := sv(a.ListArn)
			if arn == "" {
				unnamed.skipped(fmt.Sprintf("%q", sv(a.ListName)), nil)
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeFMSAppsList, NativeID: arn,
				Name: a.ListName, Region: &region,
				AttributesJSON: mustJSON(a), DiscoveredBy: scanID,
			})
		}
	}
	total, inserted, err := upsertBatch(st, batch, "fms applications-lists")
	if err != nil {
		return total, inserted, err
	}
	warnNotStored(st, "fms:ListAppsLists", acct, region, &unnamed, "applications lists have no ListArn")
	return total, inserted, nil
}

// scanFMSProtocolsLists discovers customer-defined Firewall Manager protocols lists. edge-less.
func scanFMSProtocolsLists(ctx context.Context, client fmsAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := fms.NewListProtocolsListsPaginator(client, &fms.ListProtocolsListsInput{}, func(o *fms.ListProtocolsListsPaginatorOptions) { o.Limit = 100 })
	var (
		batch   []*store.Resource
		unnamed skipTally
	)
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isFMSNotEnabled(err) {
				return 0, 0, markServiceDisabled(err)
			}
			if isFMSAdminOnlyDenial(err) {
				return 0, 0, nil
			}
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "fms:ListProtocolsLists", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("fms:ListProtocolsLists: %w", err)
		}
		for _, p := range out.ProtocolsLists {
			arn := sv(p.ListArn)
			if arn == "" {
				unnamed.skipped(fmt.Sprintf("%q", sv(p.ListName)), nil)
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeFMSProtocolsList, NativeID: arn,
				Name: p.ListName, Region: &region,
				AttributesJSON: mustJSON(p), DiscoveredBy: scanID,
			})
		}
	}
	total, inserted, err := upsertBatch(st, batch, "fms protocols-lists")
	if err != nil {
		return total, inserted, err
	}
	warnNotStored(st, "fms:ListProtocolsLists", acct, region, &unnamed, "protocols lists have no ListArn")
	return total, inserted, nil
}
