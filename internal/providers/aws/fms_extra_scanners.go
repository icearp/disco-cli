package aws

import (
	"context"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/fms"
	fmstypes "github.com/aws/aws-sdk-go-v2/service/fms/types"
	"github.com/icearp/disco-cli/store"
)

// scanFMSAppsLists stores the customer-defined Firewall Manager applications
// lists, each page written as soon as its rows' tags are read. DefaultLists
// is left false, so the lists Firewall Manager itself provides are not
// listed. Policies reference these lists; they have no outbound edges. It
// reports whether a write failed, after which the scan must stop.
func scanFMSAppsLists(ctx context.Context, client fmsAPI, acct *account, region string, st *store.Store, scanID string, gate *fmsGate, tags rowTagReader, p *phaseTotals) bool {
	return storeTokenPagesTags(ctx, st, acct, region, "fms:ListAppsLists",
		fmsGatedPages(gate, "fms:ListAppsLists", func(ctx context.Context, token *string) ([]fmstypes.AppsListDataSummary, *string, error) {
			out, err := client.ListAppsLists(ctx, &fms.ListAppsListsInput{MaxResults: sdkaws.Int32(fmsPageSize), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.AppsLists, out.NextToken, nil
		}),
		func(a fmstypes.AppsListDataSummary) (*store.Resource, string, string) {
			return arnRow("applications lists", a.ListArn, a.ListName, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeFMSAppsList, NativeID: sv(a.ListArn),
				Name: a.ListName, Region: &region,
				AttributesJSON: mustJSON(a), DiscoveredBy: scanID,
			})
		},
		tags, p)
}

// scanFMSProtocolsLists stores the customer-defined Firewall Manager
// protocols lists, each page written as soon as its rows' tags are read.
// DefaultLists is left false, so the lists Firewall Manager itself provides
// are not listed. They have no outbound edges. It reports whether a write
// failed, after which the scan must stop.
func scanFMSProtocolsLists(ctx context.Context, client fmsAPI, acct *account, region string, st *store.Store, scanID string, gate *fmsGate, tags rowTagReader, p *phaseTotals) bool {
	return storeTokenPagesTags(ctx, st, acct, region, "fms:ListProtocolsLists",
		fmsGatedPages(gate, "fms:ListProtocolsLists", func(ctx context.Context, token *string) ([]fmstypes.ProtocolsListDataSummary, *string, error) {
			out, err := client.ListProtocolsLists(ctx, &fms.ListProtocolsListsInput{MaxResults: sdkaws.Int32(fmsPageSize), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.ProtocolsLists, out.NextToken, nil
		}),
		func(l fmstypes.ProtocolsListDataSummary) (*store.Resource, string, string) {
			return arnRow("protocols lists", l.ListArn, l.ListName, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeFMSProtocolsList, NativeID: sv(l.ListArn),
				Name: l.ListName, Region: &region,
				AttributesJSON: mustJSON(l), DiscoveredBy: scanID,
			})
		},
		tags, p)
}
