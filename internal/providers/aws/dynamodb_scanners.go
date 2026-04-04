package aws

import (
	"context"
	"fmt"
	"sync"

	"codeburg.org/icearp/disco/internal/store"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"golang.org/x/sync/errgroup"
)

// scanDynamoDB discovers DynamoDB tables in one region. ListTables returns
// names only; we describe each table in parallel to avoid N+1 sequential API calls.
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

		// Describe all tables in the page concurrently.
		var (
			mu    sync.Mutex
			batch []*store.Resource
		)
		g, gctx := errgroup.WithContext(ctx)
		for _, name := range page.TableNames {
			g.Go(func() error {
				desc, err := client.DescribeTable(gctx, &dynamodb.DescribeTableInput{TableName: &name})
				if err != nil {
					if isAccessDenied(err) {
						return nil
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
				mu.Lock()
				batch = append(batch, r)
				mu.Unlock()
				return nil
			})
		}
		if err := g.Wait(); err != nil {
			return err
		}
		if len(batch) > 0 {
			if err := st.UpsertResources(batch); err != nil {
				return fmt.Errorf("upsert DynamoDB tables: %w", err)
			}
		}
	}
	return nil
}
