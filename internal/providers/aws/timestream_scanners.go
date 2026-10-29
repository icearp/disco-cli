package aws

import (
	"context"
	"errors"
	"fmt"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/timestreaminfluxdb"
	timestreaminfluxdbtypes "github.com/aws/aws-sdk-go-v2/service/timestreaminfluxdb/types"
	"github.com/aws/aws-sdk-go-v2/service/timestreamquery"
	timestreamquerytypes "github.com/aws/aws-sdk-go-v2/service/timestreamquery/types"
	"github.com/aws/aws-sdk-go-v2/service/timestreamwrite"
	timestreamwritetypes "github.com/aws/aws-sdk-go-v2/service/timestreamwrite/types"
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
	ListTagsForResource(context.Context, *timestreamwrite.ListTagsForResourceInput, ...func(*timestreamwrite.Options)) (*timestreamwrite.ListTagsForResourceOutput, error)
}

type tsQueryAPI interface {
	ListScheduledQueries(context.Context, *timestreamquery.ListScheduledQueriesInput, ...func(*timestreamquery.Options)) (*timestreamquery.ListScheduledQueriesOutput, error)
	DescribeScheduledQuery(context.Context, *timestreamquery.DescribeScheduledQueryInput, ...func(*timestreamquery.Options)) (*timestreamquery.DescribeScheduledQueryOutput, error)
	ListTagsForResource(context.Context, *timestreamquery.ListTagsForResourceInput, ...func(*timestreamquery.Options)) (*timestreamquery.ListTagsForResourceOutput, error)
}

type tsInfluxAPI interface {
	ListDbClusters(context.Context, *timestreaminfluxdb.ListDbClustersInput, ...func(*timestreaminfluxdb.Options)) (*timestreaminfluxdb.ListDbClustersOutput, error)
	ListDbInstances(context.Context, *timestreaminfluxdb.ListDbInstancesInput, ...func(*timestreaminfluxdb.Options)) (*timestreaminfluxdb.ListDbInstancesOutput, error)
	ListDbParameterGroups(context.Context, *timestreaminfluxdb.ListDbParameterGroupsInput, ...func(*timestreaminfluxdb.Options)) (*timestreaminfluxdb.ListDbParameterGroupsOutput, error)
	GetDbCluster(context.Context, *timestreaminfluxdb.GetDbClusterInput, ...func(*timestreaminfluxdb.Options)) (*timestreaminfluxdb.GetDbClusterOutput, error)
	GetDbInstance(context.Context, *timestreaminfluxdb.GetDbInstanceInput, ...func(*timestreaminfluxdb.Options)) (*timestreaminfluxdb.GetDbInstanceOutput, error)
	ListDbBackups(context.Context, *timestreaminfluxdb.ListDbBackupsInput, ...func(*timestreaminfluxdb.Options)) (*timestreaminfluxdb.ListDbBackupsOutput, error)
	ListTagsForResource(context.Context, *timestreaminfluxdb.ListTagsForResourceInput, ...func(*timestreaminfluxdb.Options)) (*timestreaminfluxdb.ListTagsForResourceOutput, error)
}

// The hosts scanTimestream calls, one per SDK package.
const (
	tsWriteHost = iota
	tsQueryHost
	tsInfluxHost
	tsHostCount
)

// tsNoPrimaryHost is passed to runHostPhases as the primary host and names
// none, so no single host's NXDOMAIN on its first phase reports the region
// unavailable. Timestream for LiveAnalytics (the write and query hosts) and
// Timestream for InfluxDB are separate products: the SDK endpoint tables list
// regions for the first only, so neither can be shown to be served wherever
// the other is. With the write host primary, a region serving InfluxDB alone
// would fail the scan once InfluxDB stored rows; with the InfluxDB host
// primary, a region serving LiveAnalytics alone would. scanTimestream reports
// the region unavailable itself when every host answers NXDOMAIN.
const tsNoPrimaryHost = -1

const (
	tsTagsOp       = "timestream:ListTagsForResource"
	tsInfluxTagsOp = "timestream-influxdb:ListTagsForResource"
	// tsWritePageCap is the model's MaxResults cap (1 to 20) of ListDatabases
	// and ListTables.
	tsWritePageCap = 20
	// tsScheduledQueryPageCap is the model's MaxResults cap (1 to 1000) of
	// ListScheduledQueries.
	tsScheduledQueryPageCap = 1000
	// tsQueryTagPageCap is the model's MaxResults cap (1 to 200) of the query
	// endpoint's ListTagsForResource.
	tsQueryTagPageCap = 200
	// tsInfluxPageCap is the model's MaxResults cap (1 to 100) of
	// ListDbClusters and ListDbInstances.
	tsInfluxPageCap = 100
)

// tsNotFound is the not-found code that each op reading a listed resource
// models: both ListTagsForResource ops of LiveAnalytics, DescribeScheduledQuery,
// GetDbCluster, GetDbInstance and the InfluxDB ListTagsForResource.
var tsNotFound = []string{"ResourceNotFoundException"}

var (
	// errTSNoScheduledQueryBody is the error of a DescribeScheduledQuery
	// response that lacks its model-required ScheduledQuery.
	errTSNoScheduledQueryBody = errors.New("DescribeScheduledQuery response has no ScheduledQuery")
	// errTSInfluxNoID is the error of a cluster or instance listed without its
	// model-required id, which its Get takes.
	errTSInfluxNoID = errors.New("the listed summary has no id to read its details by")
)

// scanTimestream discovers Timestream resources across three SDK clients:
// timestreamwrite (Database, Table), timestreamquery (ScheduledQuery), and
// timestreaminfluxdb (DbCluster, DbInstance, DbParameterGroup, DbBackup).
// Every listing runs as its own phase, one at a time, in the order below
// (runHostPhases): a phase's error does not stop the later ones; a failed
// write and ctx's end do. Databases, tables, scheduled queries, clusters and
// instances have their tags read per row, written a page at a time; the tag
// denials of each product are one warning for the scan.
//
// The closed-to-new-customers state covers Timestream for LiveAnalytics
// only: the first LiveAnalytics listing refused so on its first page skips
// the later LiveAnalytics listings. InfluxDB is a separate product on this
// service entry, so it is scanned anyway, and the not-entitled sentinel is
// returned only when nothing was found and nothing failed. A region where the
// first listing of every host answers NXDOMAIN serves neither product and is
// reported unavailable.
func scanTimestream(ctx context.Context, acct *account, region string, st *store.Store, scanID string) (int, int, error) {
	wc := timestreamwrite.NewFromConfig(acct.cfg, func(o *timestreamwrite.Options) { o.Region = region })
	qc := timestreamquery.NewFromConfig(acct.cfg, func(o *timestreamquery.Options) { o.Region = region })
	ic := timestreaminfluxdb.NewFromConfig(acct.cfg, func(o *timestreaminfluxdb.Options) { o.Region = region })
	liveTags := tsLiveTags(wc, qc).sharingDenials()
	influxTags := tsInfluxTags(ic).sharingDenials()

	var closed error
	isClosed := func(err error) bool {
		if isTimestreamLiveAnalyticsClosed(err) {
			closed = err
			return true
		}
		return false
	}
	// absent holds, by host, the NXDOMAIN its first listing failed with alone,
	// storing nothing (hostAbsentAfter): runHostPhases drops it (tsNoPrimaryHost).
	absent := map[int]error{}
	firstOfHost := func(ph hostPhase) hostPhase {
		run := ph.run
		ph.run = func(p *phaseTotals) bool {
			total, errs := p.total, len(p.errs)
			failed := run(p)
			if hostAbsentAfter(p, total, errs) {
				absent[ph.host] = p.errs[errs].err
			}
			return failed
		}
		return ph
	}
	var clusters, instances map[string]string
	total, inserted, err := runHostPhases(ctx, st, acct, region, "timestream", tsNoPrimaryHost, []hostPhase{
		firstOfHost(hostPhase{op: "timestream:ListDatabases", host: tsWriteHost, run: func(p *phaseTotals) bool {
			return scanTSDatabases(ctx, wc, acct, region, st, scanID, isClosed, liveTags, p)
		}}),
		{op: "timestream:ListTables", host: tsWriteHost, run: func(p *phaseTotals) bool {
			if closed != nil {
				return false
			}
			return scanTSTables(ctx, wc, acct, region, st, scanID, isClosed, liveTags, p)
		}},
		firstOfHost(hostPhase{op: "timestream:ListScheduledQueries", host: tsQueryHost, run: func(p *phaseTotals) bool {
			if closed != nil {
				return false
			}
			return scanTSScheduledQueries(ctx, qc, acct, region, st, scanID, isClosed, liveTags, p)
		}}),
		firstOfHost(hostPhase{op: "timestream-influxdb:ListDbClusters", host: tsInfluxHost, run: func(p *phaseTotals) (failed bool) {
			clusters, failed = scanTSInfluxClusters(ctx, ic, acct, region, st, scanID, influxTags, p)
			return failed
		}}),
		{op: "timestream-influxdb:ListDbInstances", host: tsInfluxHost, run: func(p *phaseTotals) (failed bool) {
			instances, failed = scanTSInfluxInstances(ctx, ic, acct, region, st, scanID, influxTags, p)
			return failed
		}},
		countedPhase(tsInfluxHost, "timestream:ListDbParameterGroups", func() (int, int, error) {
			return scanTSInfluxParameterGroups(ctx, ic, acct, region, st, scanID)
		}),
		countedPhase(tsInfluxHost, "timestream:ListDbBackups", func() (int, int, error) {
			return scanTSInfluxBackups(ctx, ic, tsInfluxBackupSources(clusters, instances), acct, region, st, scanID)
		}),
	})
	liveTags.warnDenials(st, acct, region)
	influxTags.warnDenials(st, acct, region)
	if closed != nil && total == 0 && err == nil {
		return 0, 0, markServiceNotEntitled(closed)
	}
	if len(absent) == tsHostCount && err == nil {
		return 0, 0, markServiceUnavailable(absent[tsWriteHost])
	}
	return total, inserted, err
}

// tsInfluxBackupSources maps the Id of each cluster and instance stored this
// scan to its ARN, for the backups that name their source by Id alone. A
// backup summary does not say which kind its source is, so an Id both a
// cluster and an instance carry gets no pair rather than a guess.
func tsInfluxBackupSources(clusters, instances map[string]string) map[string]string {
	sources := make(map[string]string, len(clusters)+len(instances))
	for id, arn := range clusters {
		if _, both := instances[id]; !both {
			sources[id] = arn
		}
	}
	for id, arn := range instances {
		if _, both := clusters[id]; !both {
			sources[id] = arn
		}
	}
	return sources
}

// tsLiveTags reads a Timestream for LiveAnalytics row's tags with
// ListTagsForResource, sent the row's NativeID, the ARN its listing returned:
// a scheduled query's on the query endpoint ("all tags on a Timestream query
// resource"), a database's or table's on the write endpoint. Both are the one
// IAM action, so one reader keeps their denials to one warning. The write op
// returns every tag in one response; the query op pages, and every page is
// read. ResourceNotFoundException, the not-found both model, means the
// resource was deleted after it was listed.
func tsLiveTags(wc tsWriteAPI, qc tsQueryAPI) rowTagReader {
	return rowTagReader{
		op:       tsTagsOp,
		notFound: tsNotFound,
		read: func(ctx context.Context, r *store.Resource) (*string, error) {
			if r.Type == TypeTimestreamScheduledQuery {
				return tsQueryTagsJSON(ctx, qc, r.NativeID)
			}
			return tsWriteTagsJSON(ctx, wc, r.NativeID)
		},
	}
}

// tsQueryTagsJSON reads every page of the query endpoint's ListTagsForResource
// for arn. A failed page, a repeated token included, returns no tags.
func tsQueryTagsJSON(ctx context.Context, client tsQueryAPI, arn string) (*string, error) {
	tags, err := readTokenPages(ctx, func(ctx context.Context, token *string) ([]timestreamquerytypes.Tag, *string, error) {
		out, err := client.ListTagsForResource(ctx, &timestreamquery.ListTagsForResourceInput{
			ResourceARN: &arn, MaxResults: sdkaws.Int32(tsQueryTagPageCap), NextToken: token,
		})
		if err != nil {
			return nil, nil, err
		}
		return out.Tags, out.NextToken, nil
	})
	if err != nil {
		return nil, err
	}
	return kvTagsJSON(tags, func(t timestreamquerytypes.Tag) (*string, *string) { return t.Key, t.Value }), nil
}

// tsWriteTagsJSON reads the write endpoint's ListTagsForResource for arn.
func tsWriteTagsJSON(ctx context.Context, client tsWriteAPI, arn string) (*string, error) {
	out, err := client.ListTagsForResource(ctx, &timestreamwrite.ListTagsForResourceInput{ResourceARN: &arn})
	if err != nil {
		return nil, err
	}
	return kvTagsJSON(out.Tags, func(t timestreamwritetypes.Tag) (*string, *string) { return t.Key, t.Value }), nil
}

// tsInfluxTags reads an InfluxDB row's tags with ListTagsForResource, sent
// the row's NativeID, the ARN its listing returned. The op returns every tag
// in one response; ResourceNotFoundException, the one error it models, means
// the resource was deleted after it was listed.
func tsInfluxTags(client tsInfluxAPI) rowTagReader {
	return rowTagReader{
		op:       tsInfluxTagsOp,
		notFound: tsNotFound,
		read: func(ctx context.Context, r *store.Resource) (*string, error) {
			arn := r.NativeID
			out, err := client.ListTagsForResource(ctx, &timestreaminfluxdb.ListTagsForResourceInput{ResourceArn: &arn})
			if err != nil {
				return nil, err
			}
			return mapTagsJSON(out.Tags), nil
		},
	}
}

// scanTSDatabases stores the databases a page at a time with their tags. A
// first page refused because LiveAnalytics is closed to the account
// (isClosed) stores nothing and is not an error.
func scanTSDatabases(ctx context.Context, client tsWriteAPI, acct *account, region string, st *store.Store, scanID string, isClosed func(error) bool, tags rowTagReader, p *phaseTotals) bool {
	return storeTokenPagesTags(ctx, st, acct, region, "timestream:ListDatabases",
		firstPageGapPages(isClosed, func(ctx context.Context, token *string) ([]timestreamwritetypes.Database, *string, error) {
			out, err := client.ListDatabases(ctx, &timestreamwrite.ListDatabasesInput{MaxResults: sdkaws.Int32(tsWritePageCap), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.Databases, out.NextToken, nil
		}),
		func(d timestreamwritetypes.Database) (*store.Resource, string, string) {
			return arnRow("databases", d.Arn, d.DatabaseName, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeTimestreamDatabase, NativeID: sv(d.Arn),
				Name: d.DatabaseName, Region: &region,
				AttributesJSON: mustJSON(d), DiscoveredBy: scanID,
			})
		}, tags, p)
}

// scanTSTables stores the tables of every database, listed in one unfiltered
// ListTables, a page at a time with their tags. A first page refused because
// LiveAnalytics is closed to the account (isClosed) stores nothing and is not
// an error.
func scanTSTables(ctx context.Context, client tsWriteAPI, acct *account, region string, st *store.Store, scanID string, isClosed func(error) bool, tags rowTagReader, p *phaseTotals) bool {
	return storeTokenPagesTags(ctx, st, acct, region, "timestream:ListTables",
		firstPageGapPages(isClosed, func(ctx context.Context, token *string) ([]timestreamwritetypes.Table, *string, error) {
			out, err := client.ListTables(ctx, &timestreamwrite.ListTablesInput{MaxResults: sdkaws.Int32(tsWritePageCap), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.Tables, out.NextToken, nil
		}),
		func(t timestreamwritetypes.Table) (*store.Resource, string, string) {
			status := string(t.TableStatus)
			return arnRow("tables", t.Arn, t.TableName, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeTimestreamTable, NativeID: sv(t.Arn),
				Name: t.TableName, Region: &region, Status: &status,
				AttributesJSON: mustJSON(t), DiscoveredBy: scanID,
			})
		}, tags, p)
}

// scanTSScheduledQueries stores the scheduled queries a page at a time with
// their tags, each row the query's DescribeScheduledQuery body (KmsKeyId,
// ScheduledQueryExecutionRoleArn, ErrorReportConfiguration and
// NotificationConfiguration are absent from the list summary). A
// DescribeScheduledQuery ResourceNotFoundException drops a query deleted since
// it was listed; any other failure, a response without its model-required
// ScheduledQuery included, keeps the summary as the row (rowDetailReader). A
// first page refused because LiveAnalytics is closed to the account
// (isClosed) stores nothing and is not an error.
func scanTSScheduledQueries(ctx context.Context, client tsQueryAPI, acct *account, region string, st *store.Store, scanID string, isClosed func(error) bool, tags rowTagReader, p *phaseTotals) bool {
	steps := tokenPageSteps{details: rowDetailReader{
		op: "timestream:DescribeScheduledQuery", what: "scheduled queries", notFound: tsNotFound,
		get: func(ctx context.Context, r *store.Resource) (any, error) {
			arn := r.NativeID
			out, err := client.DescribeScheduledQuery(ctx, &timestreamquery.DescribeScheduledQueryInput{ScheduledQueryArn: &arn})
			if err != nil {
				return nil, err
			}
			if out.ScheduledQuery == nil {
				return nil, errTSNoScheduledQueryBody
			}
			return out.ScheduledQuery, nil
		},
	}}
	return storeTokenPagesTagsWith(ctx, st, acct, region, "timestream:ListScheduledQueries",
		firstPageGapPages(isClosed, func(ctx context.Context, token *string) ([]timestreamquerytypes.ScheduledQuery, *string, error) {
			out, err := client.ListScheduledQueries(ctx, &timestreamquery.ListScheduledQueriesInput{MaxResults: sdkaws.Int32(tsScheduledQueryPageCap), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.ScheduledQueries, out.NextToken, nil
		}),
		func(q timestreamquerytypes.ScheduledQuery) (*store.Resource, string, string) {
			state := string(q.State)
			return arnRow("scheduled queries", q.Arn, q.Name, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeTimestreamScheduledQuery, NativeID: sv(q.Arn),
				Name: q.Name, Region: &region, Status: &state,
				AttributesJSON: mustJSON(q), DiscoveredBy: scanID,
			})
		}, steps, tags, p)
}

// scanTSInfluxClusters stores the clusters a page at a time with their tags,
// each row the cluster's GetDbCluster output (VpcSubnetIds,
// VpcSecurityGroupIds, InfluxAuthParametersSecretArn and
// LogDeliveryConfiguration are absent from the list summary). A GetDbCluster
// ResourceNotFoundException drops a cluster deleted since it was listed; any
// other failure, a summary without its model-required Id included, keeps the
// summary as the row (rowDetailReader). It returns the ARN of each stored row
// keyed by its Id, the form a backup's DbResourceId names its source by, and
// whether a write failed.
func scanTSInfluxClusters(ctx context.Context, client tsInfluxAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) (map[string]string, bool) {
	// ids maps each row of the page in flight to its cluster Id. The row
	// builder fills it and the reads only look entries up; it is emptied
	// before the next page is fetched, which follows that page's write.
	ids := map[*store.Resource]string{}
	arnByID := map[string]string{}
	steps := tokenPageSteps{
		details: rowDetailReader{
			op: "timestream-influxdb:GetDbCluster", what: "cluster(s)", notFound: tsNotFound,
			get: func(ctx context.Context, r *store.Resource) (any, error) {
				id := ids[r]
				if id == "" {
					return nil, errTSInfluxNoID
				}
				out, err := client.GetDbCluster(ctx, &timestreaminfluxdb.GetDbClusterInput{DbClusterId: &id})
				if err != nil {
					return nil, err
				}
				return out, nil
			},
		},
		stored: func(rows []*store.Resource) {
			for _, r := range rows {
				if id := ids[r]; id != "" {
					arnByID[id] = r.NativeID
				}
			}
		},
	}
	failed := storeTokenPagesTagsWith(ctx, st, acct, region, "timestream-influxdb:ListDbClusters",
		func(ctx context.Context, token *string) ([]timestreaminfluxdbtypes.DbClusterSummary, *string, error) {
			clear(ids)
			out, err := client.ListDbClusters(ctx, &timestreaminfluxdb.ListDbClustersInput{MaxResults: sdkaws.Int32(tsInfluxPageCap), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.Items, out.NextToken, nil
		},
		func(c timestreaminfluxdbtypes.DbClusterSummary) (*store.Resource, string, string) {
			status := string(c.Status)
			r, why, key := arnRow("clusters", c.Arn, c.Name, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeTimestreamInfluxDBCluster, NativeID: sv(c.Arn),
				Name: c.Name, Region: &region, Status: &status,
				AttributesJSON: mustJSON(c), DiscoveredBy: scanID,
			})
			if r != nil {
				ids[r] = sv(c.Id)
			}
			return r, why, key
		}, steps, tags, p)
	return arnByID, failed
}

// scanTSInfluxInstances stores the instances a page at a time with their
// tags, each row the instance's GetDbInstance output (VpcSubnetIds,
// VpcSecurityGroupIds, InfluxAuthParametersSecretArn and
// LogDeliveryConfiguration are absent from the list summary). A GetDbInstance
// ResourceNotFoundException drops an instance deleted since it was listed;
// any other failure, a summary without its model-required Id included, keeps
// the summary as the row (rowDetailReader). It returns the ARN of each stored
// row keyed by its Id, the form a backup's DbResourceId names its source by,
// and whether a write failed.
func scanTSInfluxInstances(ctx context.Context, client tsInfluxAPI, acct *account, region string, st *store.Store, scanID string, tags rowTagReader, p *phaseTotals) (map[string]string, bool) {
	// ids maps each row of the page in flight to its instance Id, as in
	// scanTSInfluxClusters.
	ids := map[*store.Resource]string{}
	arnByID := map[string]string{}
	steps := tokenPageSteps{
		details: rowDetailReader{
			op: "timestream-influxdb:GetDbInstance", what: "instance(s)", notFound: tsNotFound,
			get: func(ctx context.Context, r *store.Resource) (any, error) {
				id := ids[r]
				if id == "" {
					return nil, errTSInfluxNoID
				}
				out, err := client.GetDbInstance(ctx, &timestreaminfluxdb.GetDbInstanceInput{Identifier: &id})
				if err != nil {
					return nil, err
				}
				return out, nil
			},
		},
		stored: func(rows []*store.Resource) {
			for _, r := range rows {
				if id := ids[r]; id != "" {
					arnByID[id] = r.NativeID
				}
			}
		},
	}
	failed := storeTokenPagesTagsWith(ctx, st, acct, region, "timestream-influxdb:ListDbInstances",
		func(ctx context.Context, token *string) ([]timestreaminfluxdbtypes.DbInstanceSummary, *string, error) {
			clear(ids)
			out, err := client.ListDbInstances(ctx, &timestreaminfluxdb.ListDbInstancesInput{MaxResults: sdkaws.Int32(tsInfluxPageCap), NextToken: token})
			if err != nil {
				return nil, nil, err
			}
			return out.Items, out.NextToken, nil
		},
		func(inst timestreaminfluxdbtypes.DbInstanceSummary) (*store.Resource, string, string) {
			status := string(inst.Status)
			r, why, key := arnRow("instances", inst.Arn, inst.Name, &store.Resource{
				Provider: "aws", AccountID: acct.ID, AccountName: &acct.Name,
				Type: TypeTimestreamInfluxDBInstance, NativeID: sv(inst.Arn),
				Name: inst.Name, Region: &region, Status: &status,
				AttributesJSON: mustJSON(inst), DiscoveredBy: scanID,
			})
			if r != nil {
				ids[r] = sv(inst.Id)
			}
			return r, why, key
		}, steps, tags, p)
	return arnByID, failed
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
