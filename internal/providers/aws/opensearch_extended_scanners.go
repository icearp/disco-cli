package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/store"
	"github.com/aws/aws-sdk-go-v2/service/opensearch"
)

// scanOpenSearchApplications discovers OpenSearch applications.
func scanOpenSearchApplications(ctx context.Context, client opensearchAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	var batch []*store.Resource
	var nextToken *string
	for {
		out, err := client.ListApplications(ctx, &opensearch.ListApplicationsInput{NextToken: nextToken})
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "opensearch:ListApplications", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("opensearch:ListApplications: %w", err)
		}
		for _, a := range out.ApplicationSummaries {
			arn := sv(a.Arn)
			if arn == "" {
				continue
			}
			status := string(a.Status)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeOpenSearchApplication, NativeID: arn,
				Name: a.Name, Region: &region, Status: &status,
				AttributesJSON: mustJSON(a), DiscoveredBy: scanID,
			})
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		nextToken = out.NextToken
	}
	return upsertBatch(st, batch, "opensearch applications")
}
