package aws

import (
	"context"
	"fmt"

	"codeburg.org/icearp/disco/internal/store"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
)

// scanDynamoDB discovers DynamoDB tables in one region. ListTables returns
// names only; we fetch full details via DescribeTable for each.
func scanDynamoDB(ctx context.Context, acct *account, region string, st *store.Store, scanID string) error {
	client := dynamodb.NewFromConfig(acct.cfg, func(o *dynamodb.Options) { o.Region = region })

	pager := dynamodb.NewListTablesPaginator(client, &dynamodb.ListTablesInput{})
	for pager.HasMorePages() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return skipIfAccessDenied("dynamodb:ListTables", acct.ID, region, err)
			}
			return fmt.Errorf("dynamodb:ListTables: %w", err)
		}
		var batch []*store.Resource
		for _, name := range page.TableNames {
			desc, err := client.DescribeTable(ctx, &dynamodb.DescribeTableInput{TableName: &name})
			if err != nil {
				if isAccessDenied(err) {
					continue
				}
				return fmt.Errorf("dynamodb:DescribeTable %s: %w", name, err)
			}
			t := desc.Table
			r := &store.Resource{
				Provider:       "aws",
				AccountID:      acct.ID,
				AccountName:    &acct.Name,
				Type:           "aws:dynamodb:table",
				NativeID:       sv(t.TableArn),
				Name:           t.TableName,
				Region:         &region,
				Status:         sp(string(t.TableStatus)),
				AttributesJSON: mustJSON(t),
				ScanID:         scanID,
			}
			batch = append(batch, r)
		}
		if len(batch) > 0 {
			if err := st.UpsertResources(batch); err != nil {
				return fmt.Errorf("upsert DynamoDB tables: %w", err)
			}
		}
	}
	return nil
}
