package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/store"
	"github.com/aws/aws-sdk-go-v2/service/fms"
)

// scanFMSAppsLists discovers customer-defined Firewall Manager applications lists
// (DefaultLists=false excludes AWS-managed ones). Leaf — policies reference these,
// no outbound edges.
func scanFMSAppsLists(ctx context.Context, client fmsAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := fms.NewListAppsListsPaginator(client, &fms.ListAppsListsInput{}, func(o *fms.ListAppsListsPaginatorOptions) { o.Limit = 100 })
	var batch []*store.Resource
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
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeFMSAppsList, NativeID: arn,
				Name: a.ListName, Region: regionGlobal,
				AttributesJSON: mustJSON(a), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "fms applications-lists")
}

// scanFMSProtocolsLists discovers customer-defined Firewall Manager protocols lists. Leaf.
func scanFMSProtocolsLists(ctx context.Context, client fmsAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := fms.NewListProtocolsListsPaginator(client, &fms.ListProtocolsListsInput{}, func(o *fms.ListProtocolsListsPaginatorOptions) { o.Limit = 100 })
	var batch []*store.Resource
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
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeFMSProtocolsList, NativeID: arn,
				Name: p.ListName, Region: regionGlobal,
				AttributesJSON: mustJSON(p), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "fms protocols-lists")
}
