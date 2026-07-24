package aws

import (
	"context"
	"fmt"

	"github.com/icearp/disco-cli/store"
	"github.com/aws/aws-sdk-go-v2/service/macie2"
)

// scanMacieFindingsFilters discovers Macie findings filters in one region.
// Children of the per-region session; closure-wired via upsertMacieChildren.
func scanMacieFindingsFilters(ctx context.Context, client macie2API, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var batch []*store.Resource
	var nextToken *string
	for {
		out, err := client.ListFindingsFilters(ctx, &macie2.ListFindingsFiltersInput{NextToken: nextToken})
		if err != nil {
			if isAccessDenied(err) {
				_ = skipIfAccessDenied(st, "macie2:ListFindingsFilters", acct.ID, region, err)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("macie2:ListFindingsFilters: %w", err)
		}
		for _, f := range out.FindingsFilterListItems {
			arn := sv(f.Arn)
			if arn == "" {
				continue
			}
			status := string(f.Action)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeMacieFindingsFilter, NativeID: arn,
				Name: f.Name, Region: &region, Status: &status,
				AttributesJSON: mustJSON(f), DiscoveredBy: scanID,
			})
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		nextToken = out.NextToken
	}
	n, err := upsertMacieChildren(st, acct, region, batch, "findings-filters")
	if err != nil {
		return 0, 0, err
	}
	return len(batch), n, nil
}
