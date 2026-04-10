package aws

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"

	"codeburg.org/icearp/disco/internal/store"
	"github.com/aws/aws-sdk-go-v2/service/dynamodb"
	"golang.org/x/sync/errgroup"
)

func init() { registerService(serviceEntry{name: "aws:dynamodb", fn: scanDynamoDB}) }

// scanDynamoDB is the orchestrator for all DynamoDB resource types in one region.
func scanDynamoDB(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	var t, n atomic.Int64
	add := func(tt, nn int) { t.Add(int64(tt)); n.Add(int64(nn)) }
	g, gctx := errgroup.WithContext(ctx)
	g.Go(func() error { tt, nn, e := scanDynamoDBTables(gctx, acct, region, st, scanID); add(tt, nn); return e })
	g.Go(func() error { tt, nn, e := scanDynamoDBGlobalTables(gctx, acct, region, st, scanID); add(tt, nn); return e })
	err = g.Wait()
	return int(t.Load()), int(n.Load()), err
}

// scanDynamoDBTables discovers DynamoDB tables in one region. ListTables returns
// names only; we describe each table in parallel to avoid N+1 sequential API calls.
func scanDynamoDBTables(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := dynamodb.NewFromConfig(acct.cfg, func(o *dynamodb.Options) { o.Region = region })

	pager := dynamodb.NewListTablesPaginator(client, &dynamodb.ListTablesInput{})
	for pager.HasMorePages() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied("dynamodb:ListTables", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("dynamodb:ListTables: %w", err)
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
					Type:           TypeDynamoDBTable,
					NativeID:       sv(t.TableArn),
					Name:           t.TableName,
					Region:         &region,
					CreatedAt:      tp(t.CreationDateTime),
					Status:         sp(string(t.TableStatus)),
					AttributesJSON: mustJSON(t),
					DiscoveredBy:   scanID,
				}
				mu.Lock()
				batch = append(batch, r)
				mu.Unlock()
				return nil
			})
		}
		if err := g.Wait(); err != nil {
			return 0, 0, err
		}
		if len(batch) > 0 {
			n, err := st.UpsertResources(batch)
			if err != nil {
				return 0, 0, fmt.Errorf("upsert DynamoDB tables: %w", err)
			}
			total += len(batch)
			inserted += n
		}
	}
	return total, inserted, nil
}

// scanDynamoDBGlobalTables discovers DynamoDB global tables (legacy v2017.11.29
// API). ListGlobalTables is a global operation — it returns all global tables
// in the account regardless of which region the client is configured for. The
// scan is registered per-region like all other services; repeated upserts are
// idempotent because GlobalTableArn is the stable NativeID. Global tables are
// stored with Region=nil since they span multiple regions.
func scanDynamoDBGlobalTables(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	client := dynamodb.NewFromConfig(acct.cfg, func(o *dynamodb.Options) { o.Region = region })

	// ListGlobalTables has no SDK Paginator; paginate manually via LastEvaluatedGlobalTableName.
	var startName *string
	for {
		page, err := client.ListGlobalTables(ctx, &dynamodb.ListGlobalTablesInput{
			ExclusiveStartGlobalTableName: startName,
		})
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied("dynamodb:ListGlobalTables", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("dynamodb:ListGlobalTables: %w", err)
		}

		// Describe each global table in the page concurrently.
		var (
			mu    sync.Mutex
			batch []*store.Resource
		)
		g, gctx := errgroup.WithContext(ctx)
		for _, gt := range page.GlobalTables {
			name := gt.GlobalTableName
			g.Go(func() error {
				desc, err := client.DescribeGlobalTable(gctx, &dynamodb.DescribeGlobalTableInput{GlobalTableName: name})
				if err != nil {
					if isAccessDenied(err) {
						return nil
					}
					return fmt.Errorf("dynamodb:DescribeGlobalTable %s: %w", sv(name), err)
				}
				d := desc.GlobalTableDescription
				r := &store.Resource{
					Provider:       "aws",
					AccountID:      acct.ID,
					AccountName:    &acct.Name,
					Type:           TypeDynamoDBGlobalTable,
					NativeID:       sv(d.GlobalTableArn),
					Name:           d.GlobalTableName,
					Region:         nil, // global resource — spans multiple regions
					CreatedAt:      tp(d.CreationDateTime),
					Status:         sp(string(d.GlobalTableStatus)),
					AttributesJSON: mustJSON(d),
					DiscoveredBy:   scanID,
				}
				mu.Lock()
				batch = append(batch, r)
				mu.Unlock()
				return nil
			})
		}
		if err := g.Wait(); err != nil {
			return 0, 0, err
		}
		if len(batch) > 0 {
			n, err := st.UpsertResources(batch)
			if err != nil {
				return 0, 0, fmt.Errorf("upsert DynamoDB global tables: %w", err)
			}
			total += len(batch)
			inserted += n
		}

		if page.LastEvaluatedGlobalTableName == nil {
			break
		}
		startName = page.LastEvaluatedGlobalTableName
	}
	return total, inserted, nil
}
