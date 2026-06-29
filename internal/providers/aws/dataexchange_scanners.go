package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/store"
	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/dataexchange"
)

func init() {
	registerService(serviceEntry{
		name: "aws:dataexchange",
		fn:   scanDataExchange,
		emits: []coverage.TypeDecl{
			{Service: "dataexchange", DiscoType: TypeDataExchangeDataSets, Leaf: true},
			{Service: "dataexchange", DiscoType: TypeDataExchangeDataGrants},
			{Service: "dataexchange", DiscoType: TypeDataExchangeEventActions},
		},
	})
}

type dataExchangeAPI interface {
	ListDataSets(context.Context, *dataexchange.ListDataSetsInput, ...func(*dataexchange.Options)) (*dataexchange.ListDataSetsOutput, error)
	ListDataGrants(context.Context, *dataexchange.ListDataGrantsInput, ...func(*dataexchange.Options)) (*dataexchange.ListDataGrantsOutput, error)
	ListEventActions(context.Context, *dataexchange.ListEventActionsInput, ...func(*dataexchange.Options)) (*dataexchange.ListEventActionsOutput, error)
}

// scanDataExchange discovers owned AWS Data Exchange data sets, the data grants
// shared from this account, and the event actions automating revision exports.
// Revisions/assets (content within a data set), entitled-* (consumer-side
// marketplace subscriptions) and jobs (import/export run records) are not scanned.
func scanDataExchange(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := dataexchange.NewFromConfig(acct.cfg, func(o *dataexchange.Options) { o.Region = region })

	for _, phase := range []func() (int, int, error){
		func() (int, int, error) { return scanDataExchangeDataSets(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanDataExchangeDataGrants(ctx, client, acct, region, st, scanID) },
		func() (int, int, error) { return scanDataExchangeEventActions(ctx, client, acct, region, st, scanID) },
	} {
		t, i, ferr := phase()
		if ferr != nil {
			return total, inserted, ferr
		}
		total += t
		inserted += i
	}
	return total, inserted, nil
}

func scanDataExchangeDataSets(ctx context.Context, client dataExchangeAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var batch []*store.Resource
	// Owned-only: entitled (subscribed-from-others) data sets are out of scope —
	// they're catalogued separately upstream as entitled-data-sets (not scanned).
	pager := dataexchange.NewListDataSetsPaginator(client, &dataexchange.ListDataSetsInput{Origin: sdkaws.String("OWNED")})
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				return 0, 0, skipIfAccessDenied(st, "dataexchange:ListDataSets", acct.ID, region, perr)
			}
			return 0, 0, fmt.Errorf("dataexchange:ListDataSets: %w", perr)
		}
		for _, d := range out.DataSets {
			arn := sv(d.Arn)
			if arn == "" {
				continue
			}
			name := sv(d.Name)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeDataExchangeDataSets, NativeID: arn,
				Name: &name, Region: &region,
				AttributesJSON: mustJSON(d), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "dataexchange data-sets")
}

func scanDataExchangeDataGrants(ctx context.Context, client dataExchangeAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var batch []*store.Resource
	pager := dataexchange.NewListDataGrantsPaginator(client, &dataexchange.ListDataGrantsInput{})
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				return 0, 0, skipIfAccessDenied(st, "dataexchange:ListDataGrants", acct.ID, region, perr)
			}
			return 0, 0, fmt.Errorf("dataexchange:ListDataGrants: %w", perr)
		}
		for _, g := range out.DataGrantSummaries {
			arn := sv(g.Arn)
			if arn == "" {
				continue
			}
			name := sv(g.Name)
			status := string(g.AcceptanceState)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeDataExchangeDataGrants, NativeID: arn,
				Name: &name, Region: &region, Status: &status,
				AttributesJSON: mustJSON(g), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "dataexchange data-grants")
}

func scanDataExchangeEventActions(ctx context.Context, client dataExchangeAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var batch []*store.Resource
	pager := dataexchange.NewListEventActionsPaginator(client, &dataexchange.ListEventActionsInput{})
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			if isAccessDenied(perr) {
				return 0, 0, skipIfAccessDenied(st, "dataexchange:ListEventActions", acct.ID, region, perr)
			}
			return 0, 0, fmt.Errorf("dataexchange:ListEventActions: %w", perr)
		}
		for _, e := range out.EventActions {
			arn := sv(e.Arn)
			if arn == "" {
				continue
			}
			id := sv(e.Id)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeDataExchangeEventActions, NativeID: arn,
				Name: &id, Region: &region,
				AttributesJSON: mustJSON(e), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "dataexchange event-actions")
}
