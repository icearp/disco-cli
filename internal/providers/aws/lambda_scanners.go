package aws

import (
	"context"
	"fmt"

	"codeburg.org/icearp/disco/internal/store"
	"github.com/aws/aws-sdk-go-v2/service/lambda"
)

func init() { registerService(serviceEntry{name: "aws:lambda", fn: scanLambda}) }

// scanLambda discovers Lambda functions in one region.
func scanLambda(ctx context.Context, acct *account, region string, st *store.Store, scanID string) error {
	client := lambda.NewFromConfig(acct.cfg, func(o *lambda.Options) { o.Region = region })

	pager := lambda.NewListFunctionsPaginator(client, &lambda.ListFunctionsInput{})
	for pager.HasMorePages() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return skipIfAccessDenied("lambda:ListFunctions", acct.ID, region, err)
			}
			return fmt.Errorf("lambda:ListFunctions: %w", err)
		}
		var batch []*store.Resource
		for _, fn := range page.Functions {
			name := sv(fn.FunctionName)
			// Tags are not included in ListFunctions; fetch separately if needed.
			r := &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           TypeLambdaFunction,
				NativeID:       sv(fn.FunctionArn),
				Name:           &name,
				Region:         &region,
				AttributesJSON: mustJSON(fn),
				DiscoveredBy:         scanID,
			}
			batch = append(batch, r)
		}
		if len(batch) > 0 {
			if err := st.UpsertResources(batch); err != nil {
				return fmt.Errorf("upsert Lambda functions: %w", err)
			}
		}
	}
	return nil
}
