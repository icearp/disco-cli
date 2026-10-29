package aws

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"net"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/timestreaminfluxdb"
	tsitypes "github.com/aws/aws-sdk-go-v2/service/timestreaminfluxdb/types"
	"github.com/aws/aws-sdk-go-v2/service/timestreamquery"
	tsqtypes "github.com/aws/aws-sdk-go-v2/service/timestreamquery/types"
	"github.com/aws/aws-sdk-go-v2/service/timestreamwrite"
	tswtypes "github.com/aws/aws-sdk-go-v2/service/timestreamwrite/types"

	"github.com/icearp/disco-cli/store"
)

var (
	tsiDenied     = apiErr("AccessDeniedException", "User: arn:aws:iam::123456789012:user/t is not authorized to perform: timestream-influxdb:ListDbBackups")
	tsiValidation = apiErr("ValidationException", "bad")
)

const (
	tsiClusterARN   = "arn:aws:timestream-influxdb:us-east-1:123456789012:db-cluster/c-1"
	tsiInstanceARN  = "arn:aws:timestream-influxdb:us-east-1:123456789012:db-instance/i-1"
	tsiPGARN        = "arn:aws:timestream-influxdb:us-east-1:123456789012:db-parameter-group/pg-1"
	tsiBackup1ARN   = "arn:aws:timestream-influxdb:us-east-1:123456789012:db-backup/b-1"
	tsiBackup2ARN   = "arn:aws:timestream-influxdb:us-east-1:123456789012:db-backup/b-2"
	tsiBackup3ARN   = "arn:aws:timestream-influxdb:us-east-1:123456789012:db-backup/b-3"
	tsiDeletedARN   = "arn:aws:timestream-influxdb:us-east-1:123456789012:db-backup/b-gone"
	tsiNoStatusARN  = "arn:aws:timestream-influxdb:us-east-1:123456789012:db-backup/b-nostatus"
	tsiNoSourceARN  = "arn:aws:timestream-influxdb:us-east-1:123456789012:db-backup/b-nosource"
	tsiBackupLister = "timestream:ListDbBackups"
)

// tsiBackupPages serves a two-page ListDbBackups: page one answers a call
// without a token, page two the call carrying "p2", unless pageTwoErr replaces
// it. Every call's input is recorded.
type tsiBackupPages struct {
	mu     sync.Mutex
	inputs []timestreaminfluxdb.ListDbBackupsInput
}

// handler serves cluster c-1's COMPLETED backup on page one; on page two,
// instance i-1's DELETING backup, a backup of an unlisted source, a DELETED
// backup, a backup with no status and an element without an ARN.
func (p *tsiBackupPages) handler(pageTwoErr error) func(in any) (any, error) {
	created := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	return func(in any) (any, error) {
		req, ok := in.(*timestreaminfluxdb.ListDbBackupsInput)
		if !ok {
			return nil, errors.New("unexpected ListDbBackups input")
		}
		p.mu.Lock()
		p.inputs = append(p.inputs, *req)
		p.mu.Unlock()
		if req.NextToken == nil {
			return &timestreaminfluxdb.ListDbBackupsOutput{Items: []tsitypes.DbBackupSummary{{
				Arn: ptrStr(tsiBackup1ARN), Id: ptrStr("b-1"), Name: ptrStr("nightly"), DbResourceId: ptrStr("c-1"),
				Status: tsitypes.DbBackupStatusCompleted, CreatedAt: &created,
			}}, NextToken: ptrStr("p2")}, nil
		}
		if pageTwoErr != nil {
			return nil, pageTwoErr
		}
		return &timestreaminfluxdb.ListDbBackupsOutput{Items: []tsitypes.DbBackupSummary{
			{Arn: ptrStr(tsiBackup2ARN), Id: ptrStr("b-2"), Name: ptrStr("going"), DbResourceId: ptrStr("i-1"), Status: tsitypes.DbBackupStatusDeleting},
			{Arn: ptrStr(tsiBackup3ARN), Id: ptrStr("b-3"), Name: ptrStr("orphan"), DbResourceId: ptrStr("i-unlisted"), Status: tsitypes.DbBackupStatusFailed},
			{Arn: ptrStr(tsiDeletedARN), Id: ptrStr("b-gone"), DbResourceId: ptrStr("c-1"), Status: tsitypes.DbBackupStatusDeleted},
			{Arn: ptrStr(tsiNoStatusARN), Id: ptrStr("b-nostatus"), DbResourceId: ptrStr("c-1")},
			{Id: ptrStr("b-noarn"), DbResourceId: ptrStr("c-1"), Status: tsitypes.DbBackupStatusCompleted},
		}}, nil
	}
}

func scanTSIBackupsWith(st *store.Store, parents map[string]string, handler func(in any) (any, error)) (int, error) {
	client := timestreaminfluxdb.NewFromConfig(opStubConfig(map[string]func(any) (any, error){"ListDbBackups": handler}, tsiDenied))
	total, _, err := scanTSInfluxBackups(context.Background(), client, parents, newTestAccount(testAccountID), testRegion, st, testScanID)
	return total, err
}

// Both pages are stored unfiltered at page size 100; a DELETED backup and an
// element without an ARN are skipped; each backup whose source the cluster or
// instance phase stored is contained by it.
func TestScanTSInfluxBackups_StoresEveryPage(t *testing.T) {
	st := newTestStore(t)
	seedParentRows(t, st, TypeTimestreamInfluxDBCluster, tsiClusterARN)
	seedParentRows(t, st, TypeTimestreamInfluxDBInstance, tsiInstanceARN)
	var pages tsiBackupPages
	parents := map[string]string{"c-1": tsiClusterARN, "i-1": tsiInstanceARN}

	total, err := scanTSIBackupsWith(st, parents, pages.handler(nil))
	if err != nil {
		t.Fatalf("scanTSInfluxBackups: %v", err)
	}
	if total != 4 {
		t.Errorf("total = %d, want 4", total)
	}
	if len(pages.inputs) != 2 {
		t.Fatalf("ListDbBackups called %d times, want 2", len(pages.inputs))
	}
	for _, in := range pages.inputs {
		if in.DbResourceId != nil || in.MaxResults == nil || *in.MaxResults != 100 {
			t.Errorf("ListDbBackups input = resource %v, max %v; want unfiltered at 100", in.DbResourceId, in.MaxResults)
		}
	}

	rows := rowsByNativeID(t, st, TypeTimestreamInfluxDBBackup)
	if len(rows) != 4 {
		t.Fatalf("stored %d backups, want 4: %v", len(rows), rows)
	}
	if _, ok := rows[tsiDeletedARN]; ok {
		t.Errorf("DELETED backup %s stored", tsiDeletedARN)
	}
	r, ok := rows[tsiBackup1ARN]
	if !ok {
		t.Fatalf("missing backup %s", tsiBackup1ARN)
	}
	if sv(r.Name) != "nightly" || sv(r.Status) != "COMPLETED" || sv(r.Region) != "us-east-1" || sv(r.CreatedAt) != "2026-01-02T03:04:05Z" || r.Type != "aws:timestream-influxdb:db-backup" {
		t.Errorf("row = name %q, status %q, region %q, created %q, type %q", sv(r.Name), sv(r.Status), sv(r.Region), sv(r.CreatedAt), r.Type)
	}
	var attrs struct {
		DbResourceID string `json:"DbResourceId"`
	}
	if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil || attrs.DbResourceID != "c-1" {
		t.Errorf("attributes = %s (%v), want DbResourceId c-1", r.AttributesJSON, err)
	}
	if got := sv(rows[tsiBackup2ARN].Status); got != "DELETING" {
		t.Errorf("backup %s status = %q, want DELETING", tsiBackup2ARN, got)
	}
	if s := rows[tsiNoStatusARN].Status; s != nil {
		t.Errorf("backup %s status = %q, want unset", tsiNoStatusARN, *s)
	}

	assertContainsNative(t, st, tsiClusterARN, tsiBackup1ARN)
	assertContainsNative(t, st, tsiInstanceARN, tsiBackup2ARN)
	rels, err := st.RelationshipsTo(store.ResourceID("aws", testAccountID, tsiBackup3ARN))
	if err != nil {
		t.Fatalf("RelationshipsTo: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("backup of an unlisted source has relationships %v, want none", rels)
	}
}

func TestScanTSInfluxBackups_Empty(t *testing.T) {
	st := newTestStore(t)
	total, err := scanTSIBackupsWith(st, nil, func(any) (any, error) { return &timestreaminfluxdb.ListDbBackupsOutput{}, nil })
	if err != nil || total != 0 {
		t.Errorf("total = %d, err = %v; want 0, nil", total, err)
	}
}

// On the first call, the unrouted-operation shapes are a silent feature gap
// and a denial warns once; any other error, NXDOMAIN included, is returned.
func TestScanTSInfluxBackups_FirstPageError(t *testing.T) {
	for _, tc := range []struct {
		name      string
		err       error
		wantCode  string
		wantDNS   bool
		wantWarns int
	}{
		{name: "unknown operation", err: apiErr("UnknownOperationException", "")},
		{name: "unrouted gateway", err: apiErr("NotFoundException", "Unable to determine service/operation name to be authorized")},
		{name: "unrouted gateway denial", err: apiErr("AccessDeniedException", "Unable to determine service/operation name to be authorized")},
		{name: "http 404", err: respErrCode(404)},
		{name: "denied", err: tsiDenied, wantWarns: 1},
		{name: "nxdomain", err: &net.DNSError{Err: "no such host", Name: "timestream-influxdb.us-east-1.amazonaws.com", IsNotFound: true}, wantDNS: true},
		{name: "other", err: tsiValidation, wantCode: "ValidationException"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := countOpWarnings(st, tsiBackupLister)
			total, err := scanTSIBackupsWith(st, nil, func(any) (any, error) { return nil, tc.err })
			var dnsErr *net.DNSError
			switch {
			case tc.wantCode != "":
				assertAPICode(t, err, tc.wantCode)
			case tc.wantDNS:
				if !errors.As(err, &dnsErr) {
					t.Fatalf("err = %v, want the DNS error", err)
				}
			case err != nil:
				t.Fatalf("err = %v, want nil", err)
			}
			if total != 0 || *warns != tc.wantWarns {
				t.Errorf("total = %d, warnings = %d; want 0, %d", total, *warns, tc.wantWarns)
			}
		})
	}
}

// After a page has succeeded, a denial warns once and keeps page one's row;
// every other error, the not-deployed shapes included, is returned with page
// one's row stored and contained by its cluster.
func TestScanTSInfluxBackups_PageTwoError(t *testing.T) {
	for _, tc := range []struct {
		name      string
		err       error
		wantCode  string
		wantHTTP  int
		wantWarns int
	}{
		{name: "denied", err: tsiDenied, wantWarns: 1},
		{name: "unknown operation", err: apiErr("UnknownOperationException", ""), wantCode: "UnknownOperationException"},
		{name: "http 404", err: respErrCode(404), wantHTTP: 404},
		{name: "other", err: tsiValidation, wantCode: "ValidationException"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			seedParentRows(t, st, TypeTimestreamInfluxDBCluster, tsiClusterARN)
			warns := countOpWarnings(st, tsiBackupLister)
			var pages tsiBackupPages
			total, err := scanTSIBackupsWith(st, map[string]string{"c-1": tsiClusterARN}, pages.handler(tc.err))
			switch {
			case tc.wantCode != "":
				assertAPICode(t, err, tc.wantCode)
			case tc.wantHTTP != 0:
				if c, ok := httpStatusCode(err); !ok || c != tc.wantHTTP {
					t.Fatalf("err = %v, want HTTP %d", err, tc.wantHTTP)
				}
			case err != nil:
				t.Fatalf("err = %v, want nil", err)
			}
			if *warns != tc.wantWarns {
				t.Errorf("warnings = %d, want %d", *warns, tc.wantWarns)
			}
			rows := rowsByNativeID(t, st, TypeTimestreamInfluxDBBackup)
			if _, ok := rows[tsiBackup1ARN]; total != 1 || len(rows) != 1 || !ok {
				t.Errorf("total = %d, rows = %v; want %s only", total, rows, tsiBackup1ARN)
			}
			assertContainsNative(t, st, tsiClusterARN, tsiBackup1ARN)
		})
	}
}

// A failed write of the rows listed before a list error reports both errors.
func TestScanTSInfluxBackups_WriteAndListErrorsBothReported(t *testing.T) {
	// Not newTestStore: its cleanup queries the DB, which a closed store can't serve.
	st, err := store.Open(filepath.Join(t.TempDir(), "closed.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := st.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	var pages tsiBackupPages
	_, err = scanTSIBackupsWith(st, nil, pages.handler(tsiValidation))
	if !errors.Is(err, store.ErrStoreWrite) {
		t.Errorf("err = %v, want store.ErrStoreWrite", err)
	}
	assertAPICode(t, err, "ValidationException")
}

var (
	tsClosed    = apiErr("AccessDeniedException", "Only existing Timestream for LiveAnalytics customers can access the service")
	tsThrottled = apiErr("ThrottlingException", "slow down")
	tsGone      = apiErr("ResourceNotFoundException", "gone")
	tsNXDOMAIN  = &net.DNSError{Err: "no such host", Name: "ingest.timestream.us-east-1.amazonaws.com", IsNotFound: true}
)

// The endpoints tsCalls records tag reads under.
const (
	tsWriteReads = "write"
	tsQueryReads = "query"
	tsInfluxRead = "influx"
)

// tsTotal is the rows tsHandlers lists: two databases, a table, two scheduled
// queries, a cluster, an instance, a parameter group and four backups.
const tsTotal = 12

// tsCalls answers one scan's calls for an account in partition and region,
// and records what was asked. errs fails the tag read of an ARN.
type tsCalls struct {
	partition, region string

	mu       sync.Mutex
	errs     map[string]error
	tagReads map[string][]string // ARNs sent, by endpoint: tsWriteReads, tsQueryReads, tsInfluxRead
	caps     map[string][]int32  // MaxResults sent, by op
	counts   map[string]int      // calls, by op
}

func newTSCalls(partition, region string) *tsCalls {
	return &tsCalls{partition: partition, region: region, errs: map[string]error{}, tagReads: map[string][]string{}, caps: map[string][]int32{}, counts: map[string]int{}}
}

func (c *tsCalls) arn(svc, res string) string { return partitionARN(c.partition, svc, c.region, res) }

func (c *tsCalls) db1() string     { return c.arn("timestream", "database/db1") }
func (c *tsCalls) db2() string     { return c.arn("timestream", "database/db2") }
func (c *tsCalls) table() string   { return c.arn("timestream", "database/db1/table/t1") }
func (c *tsCalls) query() string   { return c.arn("timestream", "scheduled-query/sq1") }
func (c *tsCalls) query2() string  { return c.arn("timestream", "scheduled-query/sq2") }
func (c *tsCalls) cluster() string { return c.arn("timestream-influxdb", "db-cluster/c-1") }
func (c *tsCalls) inst() string    { return c.arn("timestream-influxdb", "db-instance/i-1") }

// tagsOf is what ListTagsForResource returns for arn: nil for db2, sq2 and
// the instance, which have none.
func (c *tsCalls) tagsOf(arn string) map[string]string {
	return map[string]map[string]string{
		c.db1():     {"env": "prod"},
		c.table():   {"team": "a"},
		c.query():   {"a": "1", "b": "2"},
		c.cluster(): {"tier": "gold"},
	}[arn]
}

func (c *tsCalls) record(op string, maxResults *int32) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.counts[op]++
	if maxResults != nil {
		c.caps[op] = append(c.caps[op], *maxResults)
	}
}

func (c *tsCalls) count(op string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.counts[op]
}

func (c *tsCalls) reads(endpoint string) []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Clone(c.tagReads[endpoint])
}

// listTags answers the three ListTagsForResource ops, told apart by input
// type. The query op serves a scheduled query's tags over two pages.
func (c *tsCalls) listTags(in any) (any, error) {
	var arn, endpoint string
	var token *string
	switch req := in.(type) {
	case *timestreamwrite.ListTagsForResourceInput:
		arn, endpoint = sv(req.ResourceARN), tsWriteReads
	case *timestreamquery.ListTagsForResourceInput:
		arn, endpoint, token = sv(req.ResourceARN), tsQueryReads, req.NextToken
		c.record("query:ListTagsForResource", req.MaxResults)
	case *timestreaminfluxdb.ListTagsForResourceInput:
		arn, endpoint = sv(req.ResourceArn), tsInfluxRead
	default:
		return nil, errors.New("unexpected ListTagsForResource input")
	}
	c.mu.Lock()
	c.tagReads[endpoint] = append(c.tagReads[endpoint], arn)
	err := c.errs[arn]
	c.mu.Unlock()
	if err != nil {
		return nil, err
	}
	tags := c.tagsOf(arn)
	keys := slices.Sorted(maps.Keys(tags))
	switch endpoint {
	case tsWriteReads:
		out := &timestreamwrite.ListTagsForResourceOutput{}
		for _, k := range keys {
			out.Tags = append(out.Tags, tswtypes.Tag{Key: sdkaws.String(k), Value: sdkaws.String(tags[k])})
		}
		return out, nil
	case tsQueryReads:
		// Page one carries the first key, page two the rest.
		out := &timestreamquery.ListTagsForResourceOutput{}
		page := keys
		if len(keys) > 1 && token == nil {
			page, out.NextToken = keys[:1], sdkaws.String("t2")
		} else if len(keys) > 1 {
			page = keys[1:]
		}
		for _, k := range page {
			out.Tags = append(out.Tags, tsqtypes.Tag{Key: sdkaws.String(k), Value: sdkaws.String(tags[k])})
		}
		return out, nil
	}
	return &timestreaminfluxdb.ListTagsForResourceOutput{Tags: tags}, nil
}

func (c *tsCalls) database(name string) tswtypes.Database {
	return tswtypes.Database{Arn: sdkaws.String(c.arn("timestream", "database/"+name)), DatabaseName: sdkaws.String(name), KmsKeyId: sdkaws.String("key-" + name)}
}

func (c *tsCalls) tableElem() tswtypes.Table {
	return tswtypes.Table{Arn: sdkaws.String(c.table()), TableName: sdkaws.String("t1"), DatabaseName: sdkaws.String("db1"), TableStatus: tswtypes.TableStatusActive}
}

func (c *tsCalls) querySummary() tsqtypes.ScheduledQuery {
	return tsqtypes.ScheduledQuery{Arn: sdkaws.String(c.query()), Name: sdkaws.String("sq1"), State: tsqtypes.ScheduledQueryStateEnabled}
}

func (c *tsCalls) queryBody() *tsqtypes.ScheduledQueryDescription {
	return &tsqtypes.ScheduledQueryDescription{Arn: sdkaws.String(c.query()), Name: sdkaws.String("sq1"), State: tsqtypes.ScheduledQueryStateEnabled, KmsKeyId: sdkaws.String("sq-key")}
}

func (c *tsCalls) query2Summary() tsqtypes.ScheduledQuery {
	return tsqtypes.ScheduledQuery{Arn: sdkaws.String(c.query2()), Name: sdkaws.String("sq2"), State: tsqtypes.ScheduledQueryStateDisabled}
}

func (c *tsCalls) query2Body() *tsqtypes.ScheduledQueryDescription {
	return &tsqtypes.ScheduledQueryDescription{Arn: sdkaws.String(c.query2()), Name: sdkaws.String("sq2"), State: tsqtypes.ScheduledQueryStateDisabled}
}

func (c *tsCalls) clusterSummary() tsitypes.DbClusterSummary {
	return tsitypes.DbClusterSummary{Arn: sdkaws.String(c.cluster()), Id: sdkaws.String("c-1"), Name: sdkaws.String("c1"), Status: tsitypes.ClusterStatusAvailable}
}

func (c *tsCalls) clusterBody() *timestreaminfluxdb.GetDbClusterOutput {
	return &timestreaminfluxdb.GetDbClusterOutput{Arn: sdkaws.String(c.cluster()), Id: sdkaws.String("c-1"), Name: sdkaws.String("c1"), Status: tsitypes.ClusterStatusAvailable, VpcSubnetIds: []string{"subnet-c"}}
}

func (c *tsCalls) instSummary() tsitypes.DbInstanceSummary {
	return tsitypes.DbInstanceSummary{Arn: sdkaws.String(c.inst()), Id: sdkaws.String("i-1"), Name: sdkaws.String("i1"), Status: tsitypes.StatusAvailable}
}

func (c *tsCalls) instBody() *timestreaminfluxdb.GetDbInstanceOutput {
	return &timestreaminfluxdb.GetDbInstanceOutput{Arn: sdkaws.String(c.inst()), Id: sdkaws.String("i-1"), Name: sdkaws.String("i1"), Status: tsitypes.StatusAvailable, VpcSubnetIds: []string{"subnet-i"}}
}

// tsHandlers serves every listing of a scan: databases db1 (page one) and db2
// (page two), table t1, scheduled queries sq1 and sq2, cluster c-1, instance i-1,
// parameter group pg-1 and the backup pages.
func tsHandlers(c *tsCalls, backups *tsiBackupPages) map[string]func(any) (any, error) {
	return map[string]func(any) (any, error){
		// timestreamwrite and timestreamquery discover their endpoint first.
		"DescribeEndpoints": func(in any) (any, error) {
			ep := sdkaws.String("timestream." + c.region + ".amazonaws.com")
			if _, ok := in.(*timestreamquery.DescribeEndpointsInput); ok {
				return &timestreamquery.DescribeEndpointsOutput{Endpoints: []tsqtypes.Endpoint{{Address: ep, CachePeriodInMinutes: 1}}}, nil
			}
			return &timestreamwrite.DescribeEndpointsOutput{Endpoints: []tswtypes.Endpoint{{Address: ep, CachePeriodInMinutes: 1}}}, nil
		},
		"ListDatabases": func(in any) (any, error) {
			req := in.(*timestreamwrite.ListDatabasesInput)
			c.record("ListDatabases", req.MaxResults)
			if req.NextToken == nil {
				return &timestreamwrite.ListDatabasesOutput{Databases: []tswtypes.Database{c.database("db1")}, NextToken: sdkaws.String("p2")}, nil
			}
			return &timestreamwrite.ListDatabasesOutput{Databases: []tswtypes.Database{c.database("db2")}}, nil
		},
		"ListTables": func(in any) (any, error) {
			req := in.(*timestreamwrite.ListTablesInput)
			c.record("ListTables", req.MaxResults)
			if req.DatabaseName != nil {
				return nil, errors.New("ListTables filtered by database")
			}
			return &timestreamwrite.ListTablesOutput{Tables: []tswtypes.Table{c.tableElem()}}, nil
		},
		"ListScheduledQueries": func(in any) (any, error) {
			c.record("ListScheduledQueries", in.(*timestreamquery.ListScheduledQueriesInput).MaxResults)
			return &timestreamquery.ListScheduledQueriesOutput{ScheduledQueries: []tsqtypes.ScheduledQuery{c.querySummary(), c.query2Summary()}}, nil
		},
		"DescribeScheduledQuery": func(in any) (any, error) {
			c.record("DescribeScheduledQuery", nil)
			switch sv(in.(*timestreamquery.DescribeScheduledQueryInput).ScheduledQueryArn) {
			case c.query():
				return &timestreamquery.DescribeScheduledQueryOutput{ScheduledQuery: c.queryBody()}, nil
			case c.query2():
				return &timestreamquery.DescribeScheduledQueryOutput{ScheduledQuery: c.query2Body()}, nil
			}
			return nil, tsGone
		},
		"ListDbClusters": func(in any) (any, error) {
			c.record("ListDbClusters", in.(*timestreaminfluxdb.ListDbClustersInput).MaxResults)
			return &timestreaminfluxdb.ListDbClustersOutput{Items: []tsitypes.DbClusterSummary{c.clusterSummary()}}, nil
		},
		"GetDbCluster": func(in any) (any, error) {
			c.record("GetDbCluster", nil)
			if sv(in.(*timestreaminfluxdb.GetDbClusterInput).DbClusterId) != "c-1" {
				return nil, tsGone
			}
			return c.clusterBody(), nil
		},
		"ListDbInstances": func(in any) (any, error) {
			c.record("ListDbInstances", in.(*timestreaminfluxdb.ListDbInstancesInput).MaxResults)
			return &timestreaminfluxdb.ListDbInstancesOutput{Items: []tsitypes.DbInstanceSummary{c.instSummary()}}, nil
		},
		"GetDbInstance": func(in any) (any, error) {
			c.record("GetDbInstance", nil)
			if sv(in.(*timestreaminfluxdb.GetDbInstanceInput).Identifier) != "i-1" {
				return nil, tsGone
			}
			return c.instBody(), nil
		},
		"ListDbParameterGroups": func(any) (any, error) {
			return &timestreaminfluxdb.ListDbParameterGroupsOutput{Items: []tsitypes.DbParameterGroupSummary{{Arn: sdkaws.String(tsiPGARN), Id: sdkaws.String("pg-1"), Name: sdkaws.String("pg1")}}}, nil
		},
		"ListDbBackups":       backups.handler(nil),
		"ListTagsForResource": c.listTags,
	}
}

// tsFixture is a commercial account's calls and handlers.
func tsFixture() (*tsCalls, map[string]func(any) (any, error)) {
	c := newTSCalls("aws", testRegion)
	return c, tsHandlers(c, &tsiBackupPages{})
}

// tsScan runs the scan's real entry point against handlers; every op they do
// not answer is denied.
func tsScan(ctx context.Context, st *store.Store, handlers map[string]func(any) (any, error)) (int, int, error) {
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(handlers, tsiDenied)
	return scanTimestream(ctx, acct, testRegion, st, testScanID)
}

// tsWant is one row the fixture stores with its tags read.
type tsWant struct {
	typ, arn, name, status, attrs string
}

func (c *tsCalls) taggedRows() []tsWant {
	return []tsWant{
		{TypeTimestreamDatabase, c.db1(), "db1", "", mustJSON(c.database("db1"))},
		{TypeTimestreamDatabase, c.db2(), "db2", "", mustJSON(c.database("db2"))},
		{TypeTimestreamTable, c.table(), "t1", "ACTIVE", mustJSON(c.tableElem())},
		{TypeTimestreamScheduledQuery, c.query(), "sq1", "ENABLED", mustJSON(c.queryBody())},
		{TypeTimestreamScheduledQuery, c.query2(), "sq2", "DISABLED", mustJSON(c.query2Body())},
		{TypeTimestreamInfluxDBCluster, c.cluster(), "c1", "AVAILABLE", mustJSON(c.clusterBody())},
		{TypeTimestreamInfluxDBInstance, c.inst(), "i1", "AVAILABLE", mustJSON(c.instBody())},
	}
}

func tsRowOf(t *testing.T, st *store.Store, typ, arn string) (store.Resource, bool) {
	t.Helper()
	r, ok := rowsByNativeID(t, st, typ)[arn]
	return r, ok
}

func sortedCopy(s []string) []string {
	out := slices.Clone(s)
	sort.Strings(out)
	return out
}

// Databases, tables, scheduled queries, clusters and instances store the
// tags their ListTagsForResource returns, nil for a resource without tags;
// exactly their ARNs are sent, each to its own endpoint's op, and a scheduled
// query's tags are read over both pages. No parameter group or backup is
// tag-read. Every listing asks for its model's page cap, and each row keeps
// the base scan's Name, Status, Region, unset CreatedAt and attributes (the
// Describe or Get body where one is read).
func TestScanTimestream_StoresTagsOfEveryType(t *testing.T) {
	st := newTestStore(t)
	warns := countWarnings(st)
	c, h := tsFixture()

	total, inserted, err := tsScan(context.Background(), st, h)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != tsTotal || inserted != tsTotal {
		t.Errorf("total, inserted = %d, %d; want %d", total, inserted, tsTotal)
	}
	if *warns != 0 {
		t.Errorf("warnings = %d; want 0", *warns)
	}
	for _, want := range c.taggedRows() {
		r, ok := tsRowOf(t, st, want.typ, want.arn)
		if !ok {
			t.Errorf("%s %s not stored", want.typ, want.arn)
			continue
		}
		if tags := c.tagsOf(want.arn); tags != nil {
			assertTagsMap(t, r.TagsJSON, tags)
		} else if r.TagsJSON != nil {
			t.Errorf("%s TagsJSON = %s; want nil", want.arn, *r.TagsJSON)
		}
		if sv(r.Name) != want.name || sv(r.Region) != testRegion || r.CreatedAt != nil {
			t.Errorf("%s Name, Region, CreatedAt = %v, %v, %v; want %s, %s, unset", want.arn, r.Name, r.Region, r.CreatedAt, want.name, testRegion)
		}
		if (want.status == "") != (r.Status == nil) || sv(r.Status) != want.status {
			t.Errorf("%s Status = %v; want %q", want.arn, r.Status, want.status)
		}
		if r.AttributesJSON != want.attrs {
			t.Errorf("%s attributes = %s; want %s", want.arn, r.AttributesJSON, want.attrs)
		}
	}
	for endpoint, want := range map[string][]string{
		tsWriteReads: {c.db1(), c.db2(), c.table()},
		tsQueryReads: {c.query(), c.query(), c.query2()},
		tsInfluxRead: {c.cluster(), c.inst()},
	} {
		if got := sortedCopy(c.reads(endpoint)); !slices.Equal(got, sortedCopy(want)) {
			t.Errorf("%s ListTagsForResource ARNs = %v; want %v", endpoint, got, want)
		}
	}
	for op, want := range map[string][]int32{
		"ListDatabases":             {20, 20},
		"ListTables":                {20},
		"ListScheduledQueries":      {1000},
		"query:ListTagsForResource": {200, 200, 200},
		"ListDbClusters":            {100},
		"ListDbInstances":           {100},
	} {
		if got := c.caps[op]; !slices.Equal(got, want) {
			t.Errorf("%s MaxResults = %v; want %v", op, got, want)
		}
	}
}

func TestScanTimestream_RescanInsertsNone(t *testing.T) {
	st := newTestStore(t)
	_, h := tsFixture()
	if _, _, err := tsScan(context.Background(), st, h); err != nil {
		t.Fatalf("first scan: %v", err)
	}
	_, h = tsFixture()
	total, inserted, err := tsScan(context.Background(), st, h)
	if err != nil || total != tsTotal || inserted != 0 {
		t.Errorf("rescan = %d, %d, %v; want %d, 0, nil", total, inserted, err, tsTotal)
	}
}

// An aws-us-gov account scanned in us-gov-west-1 sends its gov ARNs to every
// tag op unchanged and stores their tags.
func TestScanTimestream_GovCloudARNsReachTagOps(t *testing.T) {
	const govRegion = "us-gov-west-1"
	st := newTestStore(t)
	c := newTSCalls("aws-us-gov", govRegion)
	h := tsHandlers(c, &tsiBackupPages{})
	h["ListDbParameterGroups"] = func(any) (any, error) { return &timestreaminfluxdb.ListDbParameterGroupsOutput{}, nil }
	h["ListDbBackups"] = func(any) (any, error) { return &timestreaminfluxdb.ListDbBackupsOutput{}, nil }
	acct := partitionAccount("aws-us-gov")
	acct.cfg = opStubConfig(h, tsiDenied)

	total, _, err := scanTimestream(context.Background(), acct, govRegion, st, testScanID)
	if err != nil || total != 7 {
		t.Fatalf("scan = %d, %v; want 7, nil", total, err)
	}
	var sent []string
	for _, endpoint := range []string{tsWriteReads, tsQueryReads, tsInfluxRead} {
		sent = append(sent, c.reads(endpoint)...)
	}
	want := []string{c.db1(), c.db2(), c.table(), c.query(), c.query(), c.query2(), c.cluster(), c.inst()}
	if !slices.Equal(sortedCopy(sent), sortedCopy(want)) {
		t.Errorf("tag reads = %v; want %v", sent, want)
	}
	for _, arn := range sent {
		if !strings.HasPrefix(arn, "arn:aws-us-gov:") {
			t.Errorf("tag read ARN %s; want the aws-us-gov partition", arn)
		}
	}
	for _, w := range c.taggedRows() {
		r, ok := tsRowOf(t, st, w.typ, w.arn)
		if !ok || sv(r.Region) != govRegion {
			t.Errorf("%s stored %v in %v; want it in %s", w.arn, ok, r.Region, govRegion)
			continue
		}
		if tags := c.tagsOf(w.arn); tags != nil {
			assertTagsMap(t, r.TagsJSON, tags)
		}
	}
}

// No resource anywhere stores nothing, reads no tags and reports nothing.
func TestScanTimestream_EmptyListings(t *testing.T) {
	st := newTestStore(t)
	warns := countWarnings(st)
	c, h := tsFixture()
	h["ListDatabases"] = func(any) (any, error) { return &timestreamwrite.ListDatabasesOutput{}, nil }
	h["ListTables"] = func(any) (any, error) { return &timestreamwrite.ListTablesOutput{}, nil }
	h["ListScheduledQueries"] = func(any) (any, error) { return &timestreamquery.ListScheduledQueriesOutput{}, nil }
	h["ListDbClusters"] = func(any) (any, error) { return &timestreaminfluxdb.ListDbClustersOutput{}, nil }
	h["ListDbInstances"] = func(any) (any, error) { return &timestreaminfluxdb.ListDbInstancesOutput{}, nil }
	h["ListDbParameterGroups"] = func(any) (any, error) { return &timestreaminfluxdb.ListDbParameterGroupsOutput{}, nil }
	h["ListDbBackups"] = func(any) (any, error) { return &timestreaminfluxdb.ListDbBackupsOutput{}, nil }

	total, _, err := tsScan(context.Background(), st, h)
	if err != nil || total != 0 || *warns != 0 {
		t.Errorf("scan = %d, %v, %d warnings; want 0, nil, 0", total, err, *warns)
	}
	if n := len(c.reads(tsWriteReads)) + len(c.reads(tsQueryReads)) + len(c.reads(tsInfluxRead)); n != 0 {
		t.Errorf("tag reads = %d; want 0", n)
	}
}

// A failed tag read keeps the row without tags unless the resource is gone: a
// denial warns once under the op's label, a throttle after retries is a
// transient error, any other code a hard one, and ResourceNotFoundException,
// the not-found each op models, drops the row. A cluster dropped so is no
// backup's parent, and no pair to it is attempted.
func TestScanTimestream_TagReadFailures(t *testing.T) {
	c0 := newTSCalls("aws", testRegion)
	for _, target := range []struct {
		name, typ, arn, op string
	}{
		{"database", TypeTimestreamDatabase, c0.db1(), tsTagsOp},
		{"scheduled query", TypeTimestreamScheduledQuery, c0.query(), tsTagsOp},
		{"cluster", TypeTimestreamInfluxDBCluster, c0.cluster(), tsInfluxTagsOp},
	} {
		t.Run(target.name, func(t *testing.T) {
			for _, tc := range []struct {
				name      string
				err       error
				kept      bool
				outcome   serviceOutcome
				code      string
				wantWarns int
			}{
				{"denied", tsiDenied, true, 0, "", 1},
				{"not found", tsGone, false, 0, "", 0},
				{"other error", tsiValidation, true, outcomeError, "ValidationException", 0},
				{"transient", tsThrottled, true, outcomeTransient, "", 0},
			} {
				t.Run(tc.name, func(t *testing.T) {
					st := newTestStore(t)
					warnings := bacWarnings(st, target.op)
					hierarchy := countOpWarnings(st, "hierarchy")
					c, h := tsFixture()
					c.errs[target.arn] = tc.err

					total, _, err := tsScan(context.Background(), st, h)
					if tc.outcome == 0 && err != nil {
						t.Fatalf("err = %v; want nil", err)
					}
					if tc.outcome != 0 {
						assertGlueOutcome(t, err, tc.outcome)
					}
					if tc.code != "" {
						assertAPICode(t, err, tc.code)
					}
					r, ok := tsRowOf(t, st, target.typ, target.arn)
					if ok != tc.kept {
						t.Fatalf("stored = %v; want %v", ok, tc.kept)
					}
					if ok && r.TagsJSON != nil {
						t.Errorf("TagsJSON = %s; want nil", *r.TagsJSON)
					}
					want := tsTotal
					if !tc.kept {
						want--
					}
					if stored := countStoredRows(t, st); total != want || stored != want {
						t.Errorf("total = %d, stored rows = %d; want %d", total, stored, want)
					}
					if len(*warnings) != tc.wantWarns {
						t.Fatalf("%s warnings = %v; want %d", target.op, *warnings, tc.wantWarns)
					}
					for _, w := range *warnings {
						if w.Scope != testAccountID+"/"+testRegion {
							t.Errorf("warning Scope = %q; want %s/%s", w.Scope, testAccountID, testRegion)
						}
					}
					if *hierarchy != 0 {
						t.Errorf("hierarchy warnings = %d; want 0", *hierarchy)
					}
					if target.typ == TypeTimestreamInfluxDBCluster && !tc.kept {
						rels, err := st.RelationshipsTo(store.ResourceID("aws", testAccountID, tsiBackup1ARN))
						if err != nil || len(rels) != 0 {
							t.Errorf("backup of the dropped cluster has relationships %v (%v); want none", rels, err)
						}
					}
				})
			}
		})
	}
}

// Denied tag reads across the listings of one product are one warning for
// the scan, under that product's tag op.
func TestScanTimestream_TagDenialsWarnOncePerOp(t *testing.T) {
	st := newTestStore(t)
	live := countOpWarnings(st, tsTagsOp)
	influx := countOpWarnings(st, tsInfluxTagsOp)
	c, h := tsFixture()
	for _, arn := range []string{c.db1(), c.table(), c.query(), c.cluster(), c.inst()} {
		c.errs[arn] = tsiDenied
	}

	if _, _, err := tsScan(context.Background(), st, h); err != nil {
		t.Fatalf("err = %v; want nil", err)
	}
	if *live != 1 || *influx != 1 {
		t.Errorf("warnings = %d under %s, %d under %s; want 1 each", *live, tsTagsOp, *influx, tsInfluxTagsOp)
	}
}

// A throttled tag read beside a hard failure of the same op is demoted to one
// warning, so the hard error decides the outcome.
func TestScanTimestream_TagThrottleDemotedBesideHardError(t *testing.T) {
	st := newTestStore(t)
	warns := countOpWarnings(st, tsTagsOp)
	c, h := tsFixture()
	c.errs[c.db1()] = tsThrottled
	c.errs[c.query()] = tsiValidation

	_, _, err := tsScan(context.Background(), st, h)
	assertGlueOutcome(t, err, outcomeError)
	assertAPICode(t, err, "ValidationException")
	if *warns != 1 {
		t.Errorf("%s warnings = %d; want 1", tsTagsOp, *warns)
	}
}

// A scheduled query whose tag pages repeat a token is stored without tags,
// and the scan reports the incomplete read.
func TestScanTimestream_QueryTagPagesRepeatToken(t *testing.T) {
	st := newTestStore(t)
	c, h := tsFixture()
	h["ListTagsForResource"] = func(in any) (any, error) {
		if _, ok := in.(*timestreamquery.ListTagsForResourceInput); ok {
			return &timestreamquery.ListTagsForResourceOutput{Tags: []tsqtypes.Tag{{Key: sdkaws.String("a"), Value: sdkaws.String("1")}}, NextToken: sdkaws.String("same")}, nil
		}
		return c.listTags(in)
	}

	_, _, err := tsScan(context.Background(), st, h)
	if !errors.Is(err, errRepeatedNextToken) {
		t.Fatalf("err = %v; want errRepeatedNextToken", err)
	}
	r, ok := tsRowOf(t, st, TypeTimestreamScheduledQuery, c.query())
	if !ok || r.TagsJSON != nil {
		t.Errorf("stored %v, TagsJSON %v; want the row kept without tags", ok, r.TagsJSON)
	}
}

// A Describe or Get ResourceNotFoundException drops the row, which then has
// no tags read; any other failure keeps the list element as the row with its
// tags read: a denial warns once under the op, a throttle after retries is a
// transient error, any other code a hard one, and so is a response without
// its model-required body or a summary without its model-required Id. A
// dropped cluster or instance is no backup's parent, and no pair to it is
// attempted; one kept without its Id is not the parent of a backup listed
// without a source.
func TestScanTimestream_DetailReadFailures(t *testing.T) {
	c0 := newTSCalls("aws", testRegion)
	noID := c0.clusterSummary()
	noID.Id = nil
	instNoID := c0.instSummary()
	instNoID.Id = nil
	for _, target := range []struct {
		name, op, getOp, typ, arn string
		summary                   any
		// rows is the rows of typ the fixture lists, each failing alike;
		// backup is the backup the fixture lists under arn.
		rows   int
		backup string
		// missing answers the scan without the member the model requires,
		// failing with missingErr and storing missingRow.
		missing    map[string]func(any) (any, error)
		missingErr error
		missingRow any
	}{
		{
			name: "scheduled query", op: "DescribeScheduledQuery", getOp: "timestream:DescribeScheduledQuery",
			typ: TypeTimestreamScheduledQuery, arn: c0.query(), summary: c0.querySummary(), rows: 2,
			missing: map[string]func(any) (any, error){"DescribeScheduledQuery": func(any) (any, error) {
				return &timestreamquery.DescribeScheduledQueryOutput{}, nil
			}},
			missingErr: errTSNoScheduledQueryBody, missingRow: c0.querySummary(),
		},
		{
			name: "cluster", op: "GetDbCluster", getOp: "timestream-influxdb:GetDbCluster",
			typ: TypeTimestreamInfluxDBCluster, arn: c0.cluster(), summary: c0.clusterSummary(), rows: 1, backup: tsiBackup1ARN,
			missing: map[string]func(any) (any, error){"ListDbClusters": func(any) (any, error) {
				return &timestreaminfluxdb.ListDbClustersOutput{Items: []tsitypes.DbClusterSummary{noID}}, nil
			}},
			missingErr: errTSInfluxNoID, missingRow: noID,
		},
		{
			name: "instance", op: "GetDbInstance", getOp: "timestream-influxdb:GetDbInstance",
			typ: TypeTimestreamInfluxDBInstance, arn: c0.inst(), summary: c0.instSummary(), rows: 1, backup: tsiBackup2ARN,
			missing: map[string]func(any) (any, error){"ListDbInstances": func(any) (any, error) {
				return &timestreaminfluxdb.ListDbInstancesOutput{Items: []tsitypes.DbInstanceSummary{instNoID}}, nil
			}},
			missingErr: errTSInfluxNoID, missingRow: instNoID,
		},
	} {
		t.Run(target.name, func(t *testing.T) {
			for _, tc := range []struct {
				name      string
				err       error
				missing   bool
				kept      bool
				outcome   serviceOutcome
				wantWarns int
			}{
				{name: "not found", err: tsGone},
				{name: "denied", err: tsiDenied, kept: true, wantWarns: 1},
				{name: "other error", err: tsiValidation, kept: true, outcome: outcomeError},
				{name: "transient", err: tsThrottled, kept: true, outcome: outcomeTransient},
				{name: "required member missing", missing: true, kept: true, outcome: outcomeError},
			} {
				t.Run(tc.name, func(t *testing.T) {
					st := newTestStore(t)
					warnings := bacWarnings(st, target.getOp)
					hierarchy := countOpWarnings(st, "hierarchy")
					c, h := tsFixture()
					want := tsTotal
					if tc.missing {
						maps.Copy(h, target.missing)
					} else {
						h[target.op] = func(any) (any, error) { return nil, tc.err }
					}
					sourceless := tc.missing && target.backup != ""
					if sourceless {
						h["ListDbBackups"] = withSourcelessBackup(h["ListDbBackups"])
						want++
					}

					total, _, err := tsScan(context.Background(), st, h)
					if tc.outcome == 0 && err != nil {
						t.Fatalf("err = %v; want nil", err)
					}
					if tc.outcome != 0 {
						assertGlueOutcome(t, err, tc.outcome)
					}
					if tc.err == tsiValidation {
						assertAPICode(t, err, "ValidationException")
					}
					r, ok := tsRowOf(t, st, target.typ, target.arn)
					if ok != tc.kept {
						t.Fatalf("stored = %v; want %v", ok, tc.kept)
					}
					read := slices.Contains(c.reads(tsQueryReads), target.arn) || slices.Contains(c.reads(tsInfluxRead), target.arn)
					if read != tc.kept {
						t.Errorf("tag read = %v; want %v", read, tc.kept)
					}
					if !tc.kept {
						want -= target.rows
					}
					if total != want {
						t.Errorf("total = %d; want %d", total, want)
					}
					if *hierarchy != 0 {
						t.Errorf("hierarchy warnings = %d; want 0", *hierarchy)
					}
					if target.backup != "" && !tc.kept {
						rels, err := st.RelationshipsTo(store.ResourceID("aws", testAccountID, target.backup))
						if err != nil || len(rels) != 0 {
							t.Errorf("backup of the dropped %s has relationships %v (%v); want none", target.name, rels, err)
						}
					}
					if sourceless {
						if _, ok := rowsByNativeID(t, st, TypeTimestreamInfluxDBBackup)[tsiNoSourceARN]; !ok {
							t.Fatalf("backup %s not stored", tsiNoSourceARN)
						}
						rels, err := st.RelationshipsTo(store.ResourceID("aws", testAccountID, tsiNoSourceARN))
						if err != nil || len(rels) != 0 {
							t.Errorf("backup without a source has relationships %v (%v); want none", rels, err)
						}
					}
					if tc.kept {
						want := target.summary
						if tc.missing {
							want = target.missingRow
						}
						if r.AttributesJSON != mustJSON(want) {
							t.Errorf("attributes = %s; want the list element %s", r.AttributesJSON, mustJSON(want))
						}
						assertTagsMapOrNil(t, r.TagsJSON, c.tagsOf(target.arn))
					}
					if tc.missing && !errors.Is(err, target.missingErr) {
						t.Errorf("err = %v; want %v", err, target.missingErr)
					}
					if len(*warnings) != tc.wantWarns {
						t.Errorf("%s warnings = %v; want %d", target.getOp, *warnings, tc.wantWarns)
					}
				})
			}
		})
	}
}

// withSourcelessBackup adds to page one of next's backups one listed without
// the DbResourceId its model leaves optional.
func withSourcelessBackup(next func(any) (any, error)) func(any) (any, error) {
	return func(in any) (any, error) {
		out, err := next(in)
		if err != nil || in.(*timestreaminfluxdb.ListDbBackupsInput).NextToken != nil {
			return out, err
		}
		page := out.(*timestreaminfluxdb.ListDbBackupsOutput)
		page.Items = append(page.Items, tsitypes.DbBackupSummary{Arn: ptrStr(tsiNoSourceARN), Id: ptrStr("b-nosource"), Status: tsitypes.DbBackupStatusCompleted})
		return page, nil
	}
}

func assertTagsMapOrNil(t *testing.T, got *string, want map[string]string) {
	t.Helper()
	if want == nil {
		if got != nil {
			t.Errorf("TagsJSON = %s; want nil", *got)
		}
		return
	}
	assertTagsMap(t, got, want)
}

// Each listing's hard error is returned without costing any other listing
// its rows, and a failed later page keeps the pages before it. A failed
// cluster or instance listing leaves its backups stored without a parent.
func TestScanTimestream_ListFailureKeepsOtherListings(t *testing.T) {
	c0 := newTSCalls("aws", testRegion)
	for _, tc := range []struct {
		op             string
		failCall, lost int
		orphan         string
		wantKept       string
	}{
		{op: "ListDatabases", failCall: 2, lost: 1, wantKept: c0.db1()},
		{op: "ListTables", failCall: 1, lost: 1},
		{op: "ListScheduledQueries", failCall: 1, lost: 2},
		{op: "ListDbClusters", failCall: 1, lost: 1, orphan: tsiBackup1ARN},
		{op: "ListDbInstances", failCall: 1, lost: 1, orphan: tsiBackup2ARN},
		{op: "ListDbParameterGroups", failCall: 1, lost: 1},
		{op: "ListDbBackups", failCall: 1, lost: 4},
	} {
		t.Run(tc.op, func(t *testing.T) {
			st := newTestStore(t)
			_, h := tsFixture()
			h[tc.op] = failOnCall(tc.failCall, tsiValidation, h[tc.op])

			total, _, err := tsScan(context.Background(), st, h)
			assertAPICode(t, err, "ValidationException")
			if stored := countStoredRows(t, st); total != tsTotal-tc.lost || stored != tsTotal-tc.lost {
				t.Errorf("total = %d, stored rows = %d; want %d", total, stored, tsTotal-tc.lost)
			}
			if tc.wantKept != "" {
				if _, ok := tsRowOf(t, st, TypeTimestreamDatabase, tc.wantKept); !ok {
					t.Errorf("%s from the page read missing", tc.wantKept)
				}
			}
			if tc.orphan != "" {
				rels, err := st.RelationshipsTo(store.ResourceID("aws", testAccountID, tc.orphan))
				if err != nil || len(rels) != 0 {
					t.Errorf("backup %s of a failed source listing has relationships %v (%v); want none", tc.orphan, rels, err)
				}
			}
		})
	}
}

// A repeated token ends the listing as an error, keeping its pages.
func TestScanTimestream_RepeatedTokenKeepsPages(t *testing.T) {
	st := newTestStore(t)
	c, h := tsFixture()
	h["ListDatabases"] = func(any) (any, error) {
		return &timestreamwrite.ListDatabasesOutput{Databases: []tswtypes.Database{c.database("db1")}, NextToken: sdkaws.String("same")}, nil
	}

	_, _, err := tsScan(context.Background(), st, h)
	if !errors.Is(err, errRepeatedNextToken) {
		t.Fatalf("err = %v; want errRepeatedNextToken", err)
	}
	if got := len(rowsByNativeID(t, st, TypeTimestreamDatabase)); got != 1 {
		t.Errorf("databases stored = %d; want 1", got)
	}
	if _, ok := tsRowOf(t, st, TypeTimestreamDatabase, c.db1()); !ok {
		t.Error("db1, from the pages read, missing")
	}
	if n := c.count("ListTables"); n != 1 {
		t.Errorf("ListTables calls = %d; want 1", n)
	}
}

// Elements listed without an ARN are not stored or read and are warned about
// once per listing, naming the element.
func TestScanTimestream_ElementWithoutARNWarns(t *testing.T) {
	for _, tc := range []struct {
		op, label, key string
		out            any
	}{
		{"ListDatabases", "timestream:ListDatabases", "nameless-db", &timestreamwrite.ListDatabasesOutput{Databases: []tswtypes.Database{{DatabaseName: sdkaws.String("nameless-db")}, {}}}},
		{"ListTables", "timestream:ListTables", noIdentifier, &timestreamwrite.ListTablesOutput{Tables: []tswtypes.Table{{}}}},
		{"ListScheduledQueries", "timestream:ListScheduledQueries", "q-x", &timestreamquery.ListScheduledQueriesOutput{ScheduledQueries: []tsqtypes.ScheduledQuery{{Name: sdkaws.String("q-x")}}}},
		{"ListDbClusters", "timestream-influxdb:ListDbClusters", "c-x", &timestreaminfluxdb.ListDbClustersOutput{Items: []tsitypes.DbClusterSummary{{Name: sdkaws.String("c-x"), Id: sdkaws.String("cx1")}}}},
		{"ListDbInstances", "timestream-influxdb:ListDbInstances", "i-x", &timestreaminfluxdb.ListDbInstancesOutput{Items: []tsitypes.DbInstanceSummary{{Name: sdkaws.String("i-x"), Id: sdkaws.String("ix1")}}}},
	} {
		t.Run(tc.op, func(t *testing.T) {
			st := newTestStore(t)
			warnings := bacWarnings(st, tc.label)
			c, h := tsFixture()
			h[tc.op] = func(any) (any, error) { return tc.out, nil }

			if _, _, err := tsScan(context.Background(), st, h); err != nil {
				t.Fatalf("err = %v; want nil", err)
			}
			if len(*warnings) != 1 || !strings.Contains((*warnings)[0].Message, tc.key) {
				t.Errorf("warnings = %v; want one naming %q", *warnings, tc.key)
			}
			if n := c.count("GetDbCluster") + c.count("GetDbInstance"); (tc.op == "ListDbClusters" || tc.op == "ListDbInstances") && n != 1 {
				t.Errorf("Get calls = %d; want 1, for the other kind only", n)
			}
		})
	}
}

// A failed write stops the scan before any later listing and sends no
// warning about the rows it lost.
func TestScanTimestream_FailedWriteStopsWithoutWarning(t *testing.T) {
	st := newTestStore(t)
	warns := countWarnings(st)
	refuseStoreWrites(t, st, TypeTimestreamTable)
	c, h := tsFixture()
	c.errs[c.table()] = tsiDenied

	_, _, err := tsScan(context.Background(), st, h)
	if !errors.Is(err, store.ErrStoreWrite) {
		t.Fatalf("err = %v; want store.ErrStoreWrite", err)
	}
	if *warns != 0 {
		t.Errorf("warnings = %d; want 0", *warns)
	}
	if n := c.count("ListScheduledQueries") + c.count("ListDbClusters"); n != 0 {
		t.Errorf("later listings called %d times after the failed write", n)
	}
}

// Once ctx ends no further listing starts, and the stop is reported once.
func TestScanTimestream_ContextEndStopsOnce(t *testing.T) {
	st := newTestStore(t)
	c, h := tsFixture()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	h["ListScheduledQueries"] = func(any) (any, error) {
		cancel()
		return nil, context.Canceled
	}

	_, _, err := tsScan(ctx, st, h)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v; want context.Canceled", err)
	}
	stops := 0
	for _, m := range bacTopMembers(err) {
		if errors.Is(m, context.Canceled) {
			stops++
		}
	}
	if stops != 1 {
		t.Errorf("members carrying the stop = %d; want 1 (err %v)", stops, err)
	}
	if n := c.count("ListDbClusters"); n != 0 {
		t.Errorf("ListDbClusters called %d times after ctx ended", n)
	}
}

// A host whose first listing answers NXDOMAIN with nothing stored is not
// served in the region: its later listings are not made, and the other hosts
// still scan, with no error. With every host absent the region is reported
// unavailable. An NXDOMAIN after rows were listed is a failure of this scan.
func TestScanTimestream_NXDOMAIN(t *testing.T) {
	nx := func(any) (any, error) { return nil, tsNXDOMAIN }
	t.Run("LiveAnalytics absent", func(t *testing.T) {
		st := newTestStore(t)
		c, h := tsFixture()
		h["ListDatabases"] = nx
		h["ListScheduledQueries"] = nx
		total, _, err := tsScan(context.Background(), st, h)
		if err != nil || total != 7 {
			t.Errorf("scan = %d, %v; want 7 InfluxDB rows, nil", total, err)
		}
		if n := c.count("ListTables"); n != 0 {
			t.Errorf("ListTables called %d times on an absent host", n)
		}
	})
	t.Run("LiveAnalytics absent, InfluxDB empty", func(t *testing.T) {
		st := newTestStore(t)
		_, h := tsFixture()
		h["ListDatabases"] = nx
		h["ListScheduledQueries"] = nx
		h["ListDbClusters"] = func(any) (any, error) { return &timestreaminfluxdb.ListDbClustersOutput{}, nil }
		h["ListDbInstances"] = func(any) (any, error) { return &timestreaminfluxdb.ListDbInstancesOutput{}, nil }
		h["ListDbParameterGroups"] = func(any) (any, error) { return &timestreaminfluxdb.ListDbParameterGroupsOutput{}, nil }
		h["ListDbBackups"] = func(any) (any, error) { return &timestreaminfluxdb.ListDbBackupsOutput{}, nil }
		total, _, err := tsScan(context.Background(), st, h)
		if err != nil || total != 0 {
			t.Errorf("scan = %d, %v; want 0, nil", total, err)
		}
	})
	t.Run("every host absent", func(t *testing.T) {
		st := newTestStore(t)
		_, h := tsFixture()
		for _, op := range []string{"ListDatabases", "ListScheduledQueries", "ListDbClusters"} {
			h[op] = nx
		}
		total, inserted, err := tsScan(context.Background(), st, h)
		assertGlueOutcome(t, err, outcomeUnavailable)
		if total != 0 || inserted != 0 {
			t.Errorf("total, inserted = %d, %d; want 0, 0", total, inserted)
		}
	})
	t.Run("tag read after rows were listed", func(t *testing.T) {
		st := newTestStore(t)
		c, h := tsFixture()
		c.errs[c.db1()] = tsNXDOMAIN
		total, _, err := tsScan(context.Background(), st, h)
		assertGlueOutcome(t, err, outcomeError)
		if total != tsTotal {
			t.Errorf("total = %d; want %d", total, tsTotal)
		}
	})
}

// The backup phase runs in the scan's own region after the other InfluxDB
// phases, and contains each backup under the cluster or instance they stored.
func TestScanTimestream_StoresBackupsUnderSources(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	_, h := tsFixture()
	var regions parentCalls
	cfg := opStubConfig(h, tsiDenied)
	cfg.Region = "ap-southeast-2"
	acct.cfg = recordCallRegions(cfg, &regions)

	total, _, err := scanTimestream(context.Background(), acct, "us-east-1", st, testScanID)
	if err != nil {
		t.Fatalf("scanTimestream: %v", err)
	}
	if total != tsTotal {
		t.Errorf("total = %d, want %d", total, tsTotal)
	}
	if got := regions.distinct(); !slices.Equal(got, []string{"us-east-1"}) {
		t.Errorf("calls went to regions %v, want [us-east-1]", got)
	}
	if r, ok := rowsByNativeID(t, st, TypeTimestreamInfluxDBBackup)[tsiBackup1ARN]; !ok || sv(r.Region) != "us-east-1" {
		t.Errorf("backup %s missing or not in us-east-1", tsiBackup1ARN)
	}
	assertContainsNative(t, st, tsiClusterARN, tsiBackup1ARN)
	assertContainsNative(t, st, tsiInstanceARN, tsiBackup2ARN)
	assertContainsEdgesDeclared(t, st)
}

// When Timestream for LiveAnalytics is closed to the account, the later
// LiveAnalytics listings are skipped but InfluxDB is still scanned; nothing
// is warned about, and the not-entitled sentinel is returned only when
// InfluxDB found nothing and did not fail. A closure first met by a later
// LiveAnalytics listing keeps the rows stored before it.
func TestScanTimestream_LiveAnalyticsClosedStillScansInflux(t *testing.T) {
	empty := map[string]func(any) (any, error){
		"ListDbClusters":        func(any) (any, error) { return &timestreaminfluxdb.ListDbClustersOutput{}, nil },
		"ListDbInstances":       func(any) (any, error) { return &timestreaminfluxdb.ListDbInstancesOutput{}, nil },
		"ListDbParameterGroups": func(any) (any, error) { return &timestreaminfluxdb.ListDbParameterGroupsOutput{}, nil },
		"ListDbBackups":         func(any) (any, error) { return &timestreaminfluxdb.ListDbBackupsOutput{}, nil },
	}
	closedAt := func(op string) map[string]func(any) (any, error) {
		return map[string]func(any) (any, error){op: func(any) (any, error) { return nil, tsClosed }}
	}
	for _, tc := range []struct {
		name            string
		overrides       []map[string]func(any) (any, error)
		wantTotal       int
		wantNotEntitled bool
		wantCode        string
		wantSkipped     []string
	}{
		{name: "influx rows", overrides: []map[string]func(any) (any, error){closedAt("ListDatabases")}, wantTotal: 7, wantSkipped: []string{"ListTables", "ListScheduledQueries"}},
		{name: "influx empty", overrides: []map[string]func(any) (any, error){closedAt("ListDatabases"), empty}, wantNotEntitled: true, wantSkipped: []string{"ListTables", "ListScheduledQueries"}},
		{name: "influx error", overrides: []map[string]func(any) (any, error){closedAt("ListDatabases"), {
			"ListDbClusters": func(any) (any, error) { return nil, tsiValidation },
		}}, wantTotal: 6, wantCode: "ValidationException", wantSkipped: []string{"ListTables", "ListScheduledQueries"}},
		{name: "influx error, no rows", overrides: []map[string]func(any) (any, error){closedAt("ListDatabases"), empty, {
			"ListDbClusters": func(any) (any, error) { return nil, tsiValidation },
		}}, wantCode: "ValidationException", wantSkipped: []string{"ListTables", "ListScheduledQueries"}},
		{name: "closed at tables", overrides: []map[string]func(any) (any, error){closedAt("ListTables")}, wantTotal: tsTotal - 3, wantSkipped: []string{"ListScheduledQueries"}},
		{name: "closed at scheduled queries", overrides: []map[string]func(any) (any, error){closedAt("ListScheduledQueries")}, wantTotal: tsTotal - 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := countWarnings(st)
			c, h := tsFixture()
			for _, o := range tc.overrides {
				maps.Copy(h, o)
			}

			total, _, err := tsScan(context.Background(), st, h)
			switch {
			case tc.wantNotEntitled:
				assertGlueOutcome(t, err, outcomeNotEntitled)
			case tc.wantCode != "":
				assertAPICode(t, err, tc.wantCode)
				if errors.Is(err, errServiceNotEntitled) {
					t.Errorf("err = %v, must not carry errServiceNotEntitled", err)
				}
			case err != nil:
				t.Fatalf("err = %v, want nil", err)
			}
			if total != tc.wantTotal || *warns != 0 {
				t.Errorf("total = %d, warnings = %d; want %d, 0", total, *warns, tc.wantTotal)
			}
			for _, op := range tc.wantSkipped {
				if n := c.count(op); n != 0 {
					t.Errorf("%s called %d times after the closure", op, n)
				}
			}
		})
	}
}

// A rescan of an account closed to LiveAnalytics reports the InfluxDB rows it
// stored before, inserting none, and is not the not-entitled state.
func TestScanTimestream_LiveAnalyticsClosedRescanCountsInflux(t *testing.T) {
	st := newTestStore(t)
	for scan, wantInserted := range []int{7, 0} {
		_, h := tsFixture()
		h["ListDatabases"] = func(any) (any, error) { return nil, tsClosed }
		total, inserted, err := tsScan(context.Background(), st, h)
		if err != nil || total != 7 || inserted != wantInserted {
			t.Errorf("scan %d = %d, %d, %v; want 7, %d, nil", scan+1, total, inserted, err, wantInserted)
		}
	}
}

// The closure answered by a later page of a LiveAnalytics listing follows a
// page the listing served, so it is a denial of that page: the page before it
// is kept, it warns once under the listing's op, and the later listings run.
func TestScanTimestream_ClosureOnLaterPageWarns(t *testing.T) {
	st := newTestStore(t)
	warns := countWarnings(st)
	opWarns := countOpWarnings(st, "timestream:ListDatabases")
	c, h := tsFixture()
	h["ListDatabases"] = failOnCall(2, tsClosed, h["ListDatabases"])

	total, _, err := tsScan(context.Background(), st, h)
	if err != nil || total != tsTotal-1 {
		t.Fatalf("scan = %d, %v; want %d, nil", total, err, tsTotal-1)
	}
	if _, ok := tsRowOf(t, st, TypeTimestreamDatabase, c.db1()); !ok {
		t.Error("db1, from page one, missing")
	}
	if _, ok := tsRowOf(t, st, TypeTimestreamDatabase, c.db2()); ok {
		t.Error("db2 stored from a refused page")
	}
	if *warns != 1 || *opWarns != 1 {
		t.Errorf("warnings = %d (%d under timestream:ListDatabases); want 1 under the op", *warns, *opWarns)
	}
	if c.count("ListTables") != 1 || c.count("ListScheduledQueries") != 1 {
		t.Errorf("ListTables, ListScheduledQueries calls = %d, %d; want 1 each", c.count("ListTables"), c.count("ListScheduledQueries"))
	}
}

// A real IAM denial of a LiveAnalytics listing is not the closure: it warns
// once under its op, and every other listing still runs.
func TestScanTimestream_DeniedListingWarnsUnderItsOp(t *testing.T) {
	st := newTestStore(t)
	warns := countWarnings(st)
	opWarns := countOpWarnings(st, "timestream:ListDatabases")
	c, h := tsFixture()
	h["ListDatabases"] = func(any) (any, error) { return nil, tsiDenied }

	total, _, err := tsScan(context.Background(), st, h)
	if err != nil || total != tsTotal-2 {
		t.Fatalf("scan = %d, %v; want %d, nil", total, err, tsTotal-2)
	}
	if *warns != 1 || *opWarns != 1 {
		t.Errorf("warnings = %d (%d under timestream:ListDatabases); want 1 under the op", *warns, *opWarns)
	}
	if c.count("ListTables") != 1 || c.count("ListScheduledQueries") != 1 {
		t.Errorf("ListTables, ListScheduledQueries calls = %d, %d; want 1 each", c.count("ListTables"), c.count("ListScheduledQueries"))
	}
}

// An Id that both a cluster and an instance carry is ambiguous, so its backup
// is stored without a parent; an unambiguous Id still gets its pair.
func TestScanTimestream_AmbiguousSourceIDGetsNoPair(t *testing.T) {
	const dupInstanceARN = "arn:aws:timestream-influxdb:us-east-1:123456789012:db-instance/c-1"
	st := newTestStore(t)
	c, h := tsFixture()
	h["ListDbInstances"] = func(any) (any, error) {
		return &timestreaminfluxdb.ListDbInstancesOutput{Items: []tsitypes.DbInstanceSummary{
			c.instSummary(),
			{Arn: sdkaws.String(dupInstanceARN), Id: sdkaws.String("c-1"), Name: sdkaws.String("dup")},
		}}, nil
	}
	h["GetDbInstance"] = func(in any) (any, error) {
		id := sv(in.(*timestreaminfluxdb.GetDbInstanceInput).Identifier)
		arn := tsiInstanceARN
		if id == "c-1" {
			arn = dupInstanceARN
		}
		return &timestreaminfluxdb.GetDbInstanceOutput{Arn: &arn, Id: &id}, nil
	}

	if _, _, err := tsScan(context.Background(), st, h); err != nil {
		t.Fatalf("scanTimestream: %v", err)
	}
	rels, err := st.RelationshipsTo(store.ResourceID("aws", testAccountID, tsiBackup1ARN))
	if err != nil {
		t.Fatalf("RelationshipsTo: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("backup of ambiguous source c-1 has relationships %v, want none", rels)
	}
	assertContainsNative(t, st, tsiInstanceARN, tsiBackup2ARN)
}
