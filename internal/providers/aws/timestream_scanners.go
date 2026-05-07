package aws

import (
	"context"
	"fmt"

	"codeberg.org/icearp/disco/internal/coverage"
	"codeberg.org/icearp/disco/internal/store"
	"github.com/aws/aws-sdk-go-v2/service/timestreaminfluxdb"
	"github.com/aws/aws-sdk-go-v2/service/timestreamquery"
	"github.com/aws/aws-sdk-go-v2/service/timestreamwrite"
)

// isTimestreamLiveAnalyticsClosed disambiguates the "Only existing
// Timestream for LiveAnalytics customers can access the service"
// closed-to-new-customers state from a real IAM denial.
func isTimestreamLiveAnalyticsClosed(err error) bool {
	return isAccessDeniedWithMessage(err, "existing Timestream for LiveAnalytics customers")
}

func init() {
	registerService(serviceEntry{
		name: "aws:timestream",
		fn:   scanTimestream,
		emits: []coverage.TypeDecl{
			{Service: "timestream", DiscoType: TypeTimestreamDatabase},
			{Service: "timestream", DiscoType: TypeTimestreamTable},
			{Service: "timestream", DiscoType: TypeTimestreamScheduledQuery},
			{Service: "timestream", DiscoType: TypeTimestreamInfluxDBCluster},
			{Service: "timestream", DiscoType: TypeTimestreamInfluxDBInstance},
		},
	})
}

type tsWriteAPI interface {
	ListDatabases(context.Context, *timestreamwrite.ListDatabasesInput, ...func(*timestreamwrite.Options)) (*timestreamwrite.ListDatabasesOutput, error)
	ListTables(context.Context, *timestreamwrite.ListTablesInput, ...func(*timestreamwrite.Options)) (*timestreamwrite.ListTablesOutput, error)
}

type tsQueryAPI interface {
	ListScheduledQueries(context.Context, *timestreamquery.ListScheduledQueriesInput, ...func(*timestreamquery.Options)) (*timestreamquery.ListScheduledQueriesOutput, error)
	DescribeScheduledQuery(context.Context, *timestreamquery.DescribeScheduledQueryInput, ...func(*timestreamquery.Options)) (*timestreamquery.DescribeScheduledQueryOutput, error)
}

type tsInfluxAPI interface {
	ListDbClusters(context.Context, *timestreaminfluxdb.ListDbClustersInput, ...func(*timestreaminfluxdb.Options)) (*timestreaminfluxdb.ListDbClustersOutput, error)
	ListDbInstances(context.Context, *timestreaminfluxdb.ListDbInstancesInput, ...func(*timestreaminfluxdb.Options)) (*timestreaminfluxdb.ListDbInstancesOutput, error)
	GetDbCluster(context.Context, *timestreaminfluxdb.GetDbClusterInput, ...func(*timestreaminfluxdb.Options)) (*timestreaminfluxdb.GetDbClusterOutput, error)
	GetDbInstance(context.Context, *timestreaminfluxdb.GetDbInstanceInput, ...func(*timestreaminfluxdb.Options)) (*timestreaminfluxdb.GetDbInstanceOutput, error)
}

// scanTimestream discovers Timestream resources across three SDK clients:
// timestreamwrite (Database, Table), timestreamquery (ScheduledQuery), and
// timestreaminfluxdb (DbCluster, DbInstance).
func scanTimestream(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	wc := timestreamwrite.NewFromConfig(acct.cfg, func(o *timestreamwrite.Options) { o.Region = region })
	qc := timestreamquery.NewFromConfig(acct.cfg, func(o *timestreamquery.Options) { o.Region = region })
	ic := timestreaminfluxdb.NewFromConfig(acct.cfg, func(o *timestreaminfluxdb.Options) { o.Region = region })

	for _, phase := range []func() (int, int, error){
		func() (int, int, error) { return scanTSDatabases(ctx, wc, acct, region, st, scanID) },
		func() (int, int, error) { return scanTSTables(ctx, wc, acct, region, st, scanID) },
		func() (int, int, error) { return scanTSScheduledQueries(ctx, qc, acct, region, st, scanID) },
		func() (int, int, error) { return scanTSInfluxClusters(ctx, ic, acct, region, st, scanID) },
		func() (int, int, error) { return scanTSInfluxInstances(ctx, ic, acct, region, st, scanID) },
	} {
		t, i, perr := phase()
		if perr != nil {
			return total, inserted, perr
		}
		total += t
		inserted += i
	}
	return total, inserted, nil
}

func scanTSDatabases(ctx context.Context, client tsWriteAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := timestreamwrite.NewListDatabasesPaginator(client, &timestreamwrite.ListDatabasesInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isTimestreamLiveAnalyticsClosed(err) {
				return 0, 0, markServiceDisabled(err)
			}
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "timestream:ListDatabases", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("timestream:ListDatabases: %w", err)
		}
		for _, d := range out.Databases {
			arn := sv(d.Arn)
			if arn == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeTimestreamDatabase, NativeID: arn,
				Name: d.DatabaseName, Region: &region,
				AttributesJSON: mustJSON(d), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "timestream databases")
}

func scanTSTables(ctx context.Context, client tsWriteAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := timestreamwrite.NewListTablesPaginator(client, &timestreamwrite.ListTablesInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isTimestreamLiveAnalyticsClosed(err) {
				return 0, 0, markServiceDisabled(err)
			}
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "timestream:ListTables", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("timestream:ListTables: %w", err)
		}
		for _, t := range out.Tables {
			arn := sv(t.Arn)
			if arn == "" {
				continue
			}
			status := string(t.TableStatus)
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeTimestreamTable, NativeID: arn,
				Name: t.TableName, Region: &region, Status: &status,
				AttributesJSON: mustJSON(t), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "timestream tables")
}

func scanTSScheduledQueries(ctx context.Context, client tsQueryAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := timestreamquery.NewListScheduledQueriesPaginator(client, &timestreamquery.ListScheduledQueriesInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isTimestreamLiveAnalyticsClosed(err) {
				return 0, 0, markServiceDisabled(err)
			}
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "timestream:ListScheduledQueries", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("timestream:ListScheduledQueries: %w", err)
		}
		for _, q := range out.ScheduledQueries {
			arn := sv(q.Arn)
			if arn == "" {
				continue
			}
			state := string(q.State)
			// Enrich with DescribeScheduledQuery body — KmsKeyId,
			// ScheduledQueryExecutionRoleArn, ErrorReportConfiguration, and
			// NotificationConfiguration are not on the list-summary shape.
			attrs := mustJSON(q)
			qarn := arn
			dout, derr := client.DescribeScheduledQuery(ctx, &timestreamquery.DescribeScheduledQueryInput{ScheduledQueryArn: &qarn})
			if derr != nil {
				if isAccessDenied(derr) {
					_ = skipIfAccessDenied(st, "timestream:DescribeScheduledQuery", acct.ID, region, derr)
				}
			} else if dout != nil && dout.ScheduledQuery != nil {
				attrs = mustJSON(dout.ScheduledQuery)
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeTimestreamScheduledQuery, NativeID: arn,
				Name: q.Name, Region: &region, Status: &state,
				AttributesJSON: attrs, DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "timestream scheduled-queries")
}

func scanTSInfluxClusters(ctx context.Context, client tsInfluxAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := timestreaminfluxdb.NewListDbClustersPaginator(client, &timestreaminfluxdb.ListDbClustersInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "timestream:ListDbClusters", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("timestream:ListDbClusters: %w", err)
		}
		for _, c := range out.Items {
			arn := sv(c.Arn)
			if arn == "" {
				continue
			}
			status := string(c.Status)
			// Enrich with GetDbCluster body — VpcSubnetIds, VpcSecurityGroupIds,
			// InfluxAuthParametersSecretArn, LogDeliveryConfiguration are not on
			// the list-summary shape.
			attrs := mustJSON(c)
			cid := c.Id
			if cid != nil {
				gout, gerr := client.GetDbCluster(ctx, &timestreaminfluxdb.GetDbClusterInput{DbClusterId: cid})
				if gerr != nil {
					if isAccessDenied(gerr) {
						_ = skipIfAccessDenied(st, "timestream:GetDbCluster", acct.ID, region, gerr)
					}
				} else if gout != nil {
					attrs = mustJSON(gout)
				}
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeTimestreamInfluxDBCluster, NativeID: arn,
				Name: c.Name, Region: &region, Status: &status,
				AttributesJSON: attrs, DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "timestream influx-db-clusters")
}

func scanTSInfluxInstances(ctx context.Context, client tsInfluxAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := timestreaminfluxdb.NewListDbInstancesPaginator(client, &timestreaminfluxdb.ListDbInstancesInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "timestream:ListDbInstances", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("timestream:ListDbInstances: %w", err)
		}
		for _, inst := range out.Items {
			arn := sv(inst.Arn)
			if arn == "" {
				continue
			}
			status := string(inst.Status)
			// Enrich with GetDbInstance body — VpcSubnetIds, VpcSecurityGroupIds,
			// InfluxAuthParametersSecretArn, LogDeliveryConfiguration are not on
			// the list-summary shape.
			attrs := mustJSON(inst)
			iid := inst.Id
			if iid != nil {
				gout, gerr := client.GetDbInstance(ctx, &timestreaminfluxdb.GetDbInstanceInput{Identifier: iid})
				if gerr != nil {
					if isAccessDenied(gerr) {
						_ = skipIfAccessDenied(st, "timestream:GetDbInstance", acct.ID, region, gerr)
					}
				} else if gout != nil {
					attrs = mustJSON(gout)
				}
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeTimestreamInfluxDBInstance, NativeID: arn,
				Name: inst.Name, Region: &region, Status: &status,
				AttributesJSON: attrs, DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "timestream influx-db-instances")
}
