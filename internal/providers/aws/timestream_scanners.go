package aws

import (
	"context"
	"errors"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/service/timestreaminfluxdb"
	timestreaminfluxdbtypes "github.com/aws/aws-sdk-go-v2/service/timestreaminfluxdb/types"
	"github.com/aws/aws-sdk-go-v2/service/timestreamquery"
	"github.com/aws/aws-sdk-go-v2/service/timestreamwrite"
	"github.com/icearp/disco-cli/internal/restype"
	"github.com/icearp/disco-cli/store"
)

// isTimestreamLiveAnalyticsClosed disambiguates the "Only existing
// Timestream for LiveAnalytics customers can access the service"
// closed-to-new-customers state from a real IAM denial.
func isTimestreamLiveAnalyticsClosed(err error) bool {
	return isAccessDeniedWithMessage(err, "existing Timestream for LiveAnalytics customers")
}

func init() {
	registerType(restype.Descriptor{Type: TypeTimestreamDatabase, Service: "timestream"})
	registerType(restype.Descriptor{Type: TypeTimestreamTable, Service: "timestream"})
	registerType(restype.Descriptor{Type: TypeTimestreamScheduledQuery, Service: "timestream"})
	registerType(restype.Descriptor{Type: TypeTimestreamInfluxDBCluster, Service: "timestream"})
	registerType(restype.Descriptor{Type: TypeTimestreamInfluxDBInstance, Service: "timestream"})
	registerType(restype.Descriptor{Type: TypeTimestreamInfluxDBParameterGroup, Service: "timestream-influxdb"})
	registerType(restype.Descriptor{Type: TypeTimestreamInfluxDBBackup, Service: "timestream-influxdb"})
	registerService(serviceEntry{
		name: "aws:timestream",
		fn:   scanTimestream,
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
	ListDbParameterGroups(context.Context, *timestreaminfluxdb.ListDbParameterGroupsInput, ...func(*timestreaminfluxdb.Options)) (*timestreaminfluxdb.ListDbParameterGroupsOutput, error)
	GetDbCluster(context.Context, *timestreaminfluxdb.GetDbClusterInput, ...func(*timestreaminfluxdb.Options)) (*timestreaminfluxdb.GetDbClusterOutput, error)
	GetDbInstance(context.Context, *timestreaminfluxdb.GetDbInstanceInput, ...func(*timestreaminfluxdb.Options)) (*timestreaminfluxdb.GetDbInstanceOutput, error)
	ListDbBackups(context.Context, *timestreaminfluxdb.ListDbBackupsInput, ...func(*timestreaminfluxdb.Options)) (*timestreaminfluxdb.ListDbBackupsOutput, error)
}

// scanTimestream discovers Timestream resources across three SDK clients:
// timestreamwrite (Database, Table), timestreamquery (ScheduledQuery), and
// timestreaminfluxdb (DbCluster, DbInstance, DbParameterGroup, DbBackup).
func scanTimestream(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	wc := timestreamwrite.NewFromConfig(acct.cfg, func(o *timestreamwrite.Options) { o.Region = region })
	qc := timestreamquery.NewFromConfig(acct.cfg, func(o *timestreamquery.Options) { o.Region = region })
	ic := timestreaminfluxdb.NewFromConfig(acct.cfg, func(o *timestreaminfluxdb.Options) { o.Region = region })

	// The closed-to-new-customers state covers Timestream for LiveAnalytics
	// only; InfluxDB is a separate product on this service entry, so it is
	// scanned anyway, and the sentinel is returned only when nothing was found.
	var notEntitled error
	for _, phase := range []func() (int, int, error){
		func() (int, int, error) { return scanTSDatabases(ctx, wc, acct, region, st, scanID) },
		func() (int, int, error) { return scanTSTables(ctx, wc, acct, region, st, scanID) },
		func() (int, int, error) { return scanTSScheduledQueries(ctx, qc, acct, region, st, scanID) },
	} {
		t, i, perr := phase()
		if errors.Is(perr, errServiceNotEntitled) {
			notEntitled = perr
			break
		}
		if perr != nil {
			return total, inserted, perr
		}
		total += t
		inserted += i
	}
	it, ii, ierr := scanTSInflux(ctx, ic, acct, region, st, scanID)
	total += it
	inserted += ii
	if notEntitled != nil && total == 0 && ierr == nil {
		return 0, 0, notEntitled
	}
	return total, inserted, ierr
}

// scanTSInflux runs every Timestream for InfluxDB phase even when an earlier
// one fails, so one phase's error costs no other phase its rows. Backups list
// the whole region and use the clusters and instances only for their contains
// pairs.
func scanTSInflux(ctx context.Context, client tsInfluxAPI, acct *account, region string, st *store.Store, scanID string) (total, inserted int, err error) {
	clusterARNs, ct, ci, cerr := scanTSInfluxClusters(ctx, client, acct, region, st, scanID)
	instanceARNs, nt, ni, nerr := scanTSInfluxInstances(ctx, client, acct, region, st, scanID)
	pt, pi, perr := scanTSInfluxParameterGroups(ctx, client, acct, region, st, scanID)
	// A backup summary names its source by Id alone, not by kind, so an Id
	// both a cluster and an instance carry gets no pair rather than a guess.
	parentARNs := make(map[string]string, len(clusterARNs)+len(instanceARNs))
	for id, arn := range clusterARNs {
		if _, both := instanceARNs[id]; !both {
			parentARNs[id] = arn
		}
	}
	for id, arn := range instanceARNs {
		if _, both := clusterARNs[id]; !both {
			parentARNs[id] = arn
		}
	}
	bt, bi, berr := scanTSInfluxBackups(ctx, client, parentARNs, acct, region, st, scanID)
	return ct + nt + pt + bt, ci + ni + pi + bi, errors.Join(cerr, nerr, perr, berr)
}

func scanTSDatabases(ctx context.Context, client tsWriteAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := timestreamwrite.NewListDatabasesPaginator(client, &timestreamwrite.ListDatabasesInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isTimestreamLiveAnalyticsClosed(err) {
				return 0, 0, markServiceNotEntitled(err)
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
				return 0, 0, markServiceNotEntitled(err)
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
				return 0, 0, markServiceNotEntitled(err)
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
			// NotificationConfiguration are absent from the list-summary.
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

// scanTSInfluxClusters returns the ARN of each stored row keyed by its Id, the
// form a backup's DbResourceId names its source by.
func scanTSInfluxClusters(ctx context.Context, client tsInfluxAPI, acct *account, region string, st *store.Store, scanID string) (map[string]string, int, int, error) {
	pager := timestreaminfluxdb.NewListDbClustersPaginator(client, &timestreaminfluxdb.ListDbClustersInput{})
	var batch []*store.Resource
	arnByID := map[string]string{}
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return nil, 0, 0, skipIfAccessDenied(st, "timestream:ListDbClusters", acct.ID, region, err)
			}
			return nil, 0, 0, fmt.Errorf("timestream:ListDbClusters: %w", err)
		}
		for _, c := range out.Items {
			arn := sv(c.Arn)
			if arn == "" {
				continue
			}
			status := string(c.Status)
			// Enrich with GetDbCluster body — VpcSubnetIds, VpcSecurityGroupIds,
			// InfluxAuthParametersSecretArn, LogDeliveryConfiguration are absent
			// from the list-summary.
			attrs := mustJSON(c)
			cid := c.Id
			if cid != nil {
				arnByID[*cid] = arn
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
	t, i, err := upsertBatch(st, batch, "timestream influx-db-clusters")
	if err != nil {
		return nil, 0, 0, err
	}
	return arnByID, t, i, nil
}

// scanTSInfluxInstances returns the ARN of each stored row keyed by its Id, the
// form a backup's DbResourceId names its source by.
func scanTSInfluxInstances(ctx context.Context, client tsInfluxAPI, acct *account, region string, st *store.Store, scanID string) (map[string]string, int, int, error) {
	pager := timestreaminfluxdb.NewListDbInstancesPaginator(client, &timestreaminfluxdb.ListDbInstancesInput{})
	var batch []*store.Resource
	arnByID := map[string]string{}
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return nil, 0, 0, skipIfAccessDenied(st, "timestream:ListDbInstances", acct.ID, region, err)
			}
			return nil, 0, 0, fmt.Errorf("timestream:ListDbInstances: %w", err)
		}
		for _, inst := range out.Items {
			arn := sv(inst.Arn)
			if arn == "" {
				continue
			}
			status := string(inst.Status)
			// Enrich with GetDbInstance body — VpcSubnetIds, VpcSecurityGroupIds,
			// InfluxAuthParametersSecretArn, LogDeliveryConfiguration are absent
			// from the list-summary.
			attrs := mustJSON(inst)
			iid := inst.Id
			if iid != nil {
				arnByID[*iid] = arn
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
	t, i, err := upsertBatch(st, batch, "timestream influx-db-instances")
	if err != nil {
		return nil, 0, 0, err
	}
	return arnByID, t, i, nil
}

func scanTSInfluxParameterGroups(ctx context.Context, client tsInfluxAPI, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	pager := timestreaminfluxdb.NewListDbParameterGroupsPaginator(client, &timestreaminfluxdb.ListDbParameterGroupsInput{})
	var batch []*store.Resource
	for pager.HasMorePages() {
		out, err := pager.NextPage(ctx)
		if err != nil {
			if isAccessDenied(err) {
				return 0, 0, skipIfAccessDenied(st, "timestream:ListDbParameterGroups", acct.ID, region, err)
			}
			return 0, 0, fmt.Errorf("timestream:ListDbParameterGroups: %w", err)
		}
		for _, pg := range out.Items {
			arn := sv(pg.Arn)
			if arn == "" {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeTimestreamInfluxDBParameterGroup, NativeID: arn,
				Name: pg.Name, Region: &region,
				AttributesJSON: mustJSON(pg), DiscoveredBy: scanID,
			})
		}
	}
	return upsertBatch(st, batch, "timestream influx-db-parameter-groups")
}

// scanTSInfluxBackups lists the region's backups with one unfiltered
// ListDbBackups (DbResourceId is optional) and records each under the cluster
// or instance it was taken from when that phase stored the source this scan.
// A DELETED backup is skipped; a DELETING one is stored.
//
// ListDbBackups arrived in SDK v1.23.0, after the cluster and instance API it
// shares an endpoint with, so a region may serve those without it. On the
// first call, the shapes an unrouted operation takes (isOpNotDeployed) are a
// feature gap, skipped silently. The op models ResourceNotFoundException as a
// 404, but the unfiltered call names no resource, so a 404 on its first call is
// read as the op being absent. Once a page has succeeded the op is deployed: a
// later error of any shape except a denial is returned, after the rows listed
// so far are stored.
func scanTSInfluxBackups(ctx context.Context, client tsInfluxAPI, parentARNs map[string]string, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	const op = "timestream:ListDbBackups"
	pager := timestreaminfluxdb.NewListDbBackupsPaginator(client, &timestreaminfluxdb.ListDbBackupsInput{},
		func(o *timestreaminfluxdb.ListDbBackupsPaginatorOptions) { o.Limit = 100 })
	var batch []*store.Resource
	var sources []string
	var listErr error
	listed := false
	for pager.HasMorePages() {
		out, perr := pager.NextPage(ctx)
		if perr != nil {
			switch {
			case !listed && isOpNotDeployed(perr):
				return 0, 0, nil
			case isAccessDenied(perr):
				_ = skipIfAccessDenied(st, op, acct.ID, region, perr)
			default:
				listErr = fmt.Errorf("%s: %w", op, perr)
			}
			break
		}
		listed = true
		for _, b := range out.Items {
			arn := sv(b.Arn)
			if arn == "" || b.Status == timestreaminfluxdbtypes.DbBackupStatusDeleted {
				continue
			}
			batch = append(batch, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeTimestreamInfluxDBBackup, NativeID: arn,
				Name: b.Name, Status: nonEmptyPtr(string(b.Status)), Region: &region,
				CreatedAt: tp(b.CreatedAt), AttributesJSON: mustJSON(b), DiscoveredBy: scanID,
			})
			sources = append(sources, parentARNs[sv(b.DbResourceId)])
		}
	}
	t, i, uerr := upsertBatch(st, batch, "timestream influx-db-backups")
	if uerr != nil {
		return 0, 0, errors.Join(listErr, uerr)
	}
	var pairs [][2]string
	for n, r := range batch {
		if sources[n] != "" {
			pairs = append(pairs, [2]string{r.ID, store.ResourceID("aws", acct.ID, sources[n])})
		}
	}
	if herr := st.RecordHierarchyBatch(pairs); herr != nil {
		return t, i, errors.Join(listErr, fmt.Errorf("closure timestream influx-db-backups: %w", herr))
	}
	return t, i, listErr
}
