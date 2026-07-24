package aws

import (
	"context"
	"fmt"

	"github.com/icearp/disco-cli/store"
	"github.com/aws/aws-sdk-go-v2/service/detective"
)

// scanDetectiveOrgAdmins captures Detective delegated administrator
// accounts. Synth ARN: arn:aws:detective:{r}:{a}:organization-admin/{id}.
func scanDetectiveOrgAdmins(ctx context.Context, client detectiveAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var batch []*store.Resource
	var nextToken *string
	for {
		out, err := client.ListOrganizationAdminAccounts(ctx, &detective.ListOrganizationAdminAccountsInput{NextToken: nextToken})
		if err != nil {
			if isAccessDenied(err) {
				_ = skipIfAccessDenied(st, "detective:ListOrganizationAdminAccounts", acct.ID, region, err)
				return 0, 0, nil
			}
			return 0, 0, fmt.Errorf("detective:ListOrganizationAdminAccounts: %w", err)
		}
		for _, a := range out.Administrators {
			id := sv(a.AccountId)
			if id == "" {
				continue
			}
			arn := fmt.Sprintf("arn:aws:detective:%s:%s:organization-admin/%s", region, acct.ID, id)
			label := id
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeDetectiveOrganizationAdmin, NativeID: arn,
				Name: &label, Region: &region,
				AttributesJSON: mustJSON(a), DiscoveredBy: scanID,
			})
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		nextToken = out.NextToken
	}
	return upsertBatch(st, batch, "detective organization-admins")
}
