package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/internal/store"
	"github.com/aws/aws-sdk-go-v2/service/finspace"
)

func init() {
	registerService(serviceEntry{
		name: "aws:fin-space",
		fn:   scanFinSpace,
		emits: []coverage.TypeDecl{
			{Service: "fin-space", DiscoType: TypeFinSpaceEnvironment, Leaf: true},
		},
	})
}

// scanFinSpace discovers FinSpace environments.
func scanFinSpace(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := finspace.NewFromConfig(acct.cfg, func(o *finspace.Options) { o.Region = region })

	var batch []*store.Resource
	var nextToken *string
	for {
		// AWS marked ListEnvironments deprecated with no successor op; FinSpace
		// is in legacy maintenance and the only inventory entry point.
		out, err := client.ListEnvironments(ctx, &finspace.ListEnvironmentsInput{NextToken: nextToken}) //nolint:staticcheck // SA1019: no replacement
		if err != nil {
			// Per-region feature gap: "You cannot access API in this region".
			// FinSpace is deployed in a subset of regions only.
			if isAccessDeniedWithMessage(err, "cannot access API in this region") {
				return 0, 0, nil
			}
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "finspace:ListEnvironments", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("finspace:ListEnvironments: %w", err)
		}
		for _, e := range out.Environments {
			arn := sv(e.EnvironmentArn)
			if arn == "" {
				continue
			}
			status := string(e.Status)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeFinSpaceEnvironment, NativeID: arn,
				Name: e.Name, Region: &region, Status: &status,
				AttributesJSON: mustJSON(e), DiscoveredBy: scanID,
			})
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		nextToken = out.NextToken
	}
	return upsertBatch(st, batch, "fin-space environments")
}
