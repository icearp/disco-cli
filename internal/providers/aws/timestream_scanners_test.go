package aws

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"net"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/timestreaminfluxdb"
	tsitypes "github.com/aws/aws-sdk-go-v2/service/timestreaminfluxdb/types"
	"github.com/aws/aws-sdk-go-v2/service/timestreamquery"
	tsqtypes "github.com/aws/aws-sdk-go-v2/service/timestreamquery/types"
	"github.com/aws/aws-sdk-go-v2/service/timestreamwrite"
	tswtypes "github.com/aws/aws-sdk-go-v2/service/timestreamwrite/types"

	"github.com/icearp/disco-cli/store"
)

// stubTSWrite returns errList on ListDatabases; only that method is exercised.
type stubTSWrite struct {
	tsWriteAPI
	errList error
}

func (s *stubTSWrite) ListDatabases(context.Context, *timestreamwrite.ListDatabasesInput, ...func(*timestreamwrite.Options)) (*timestreamwrite.ListDatabasesOutput, error) {
	return nil, s.errList
}

// TestScanTSDatabases_ClosedToNewCustomers pins that the closed-to-new-customers
// AccessDeniedException maps to the not-entitled sentinel (not disabled) and
// records no warning — Timestream for LiveAnalytics can't be self-enabled.
func TestScanTSDatabases_ClosedToNewCustomers(t *testing.T) {
	st := newTestStore(t)
	var warned bool
	st.OnWarn = func(store.ScanWarning) { warned = true }
	acct := newTestAccount(testAccountID)

	closed := apiErr("AccessDeniedException",
		"Only existing Timestream for LiveAnalytics customers can access the service")
	stub := &stubTSWrite{errList: closed}

	_, _, err := scanTSDatabases(context.Background(), stub, acct, testRegion, st, testScanID)
	if !errors.Is(err, errServiceNotEntitled) {
		t.Fatalf("got %v; want errServiceNotEntitled", err)
	}
	if errors.Is(err, errServiceDisabled) {
		t.Errorf("got %v; must not also be errServiceDisabled", err)
	}
	if warned {
		t.Errorf("closed-to-new-customers should not record a ScanWarning")
	}
}

// TestScanTSDatabases_RealIAMDeny is the negative case: a genuine per-action IAM
// denial carries an action-identifying message, so it must NOT be classified as
// not-entitled — it soft-skips (nil) and records a warning instead.
func TestScanTSDatabases_RealIAMDeny(t *testing.T) {
	st := newTestStore(t)
	var warned bool
	st.OnWarn = func(store.ScanWarning) { warned = true }
	acct := newTestAccount(testAccountID)

	deny := apiErr("AccessDeniedException",
		"User: arn:aws:iam::123456789012:user/x is not authorized to perform: timestream:ListDatabases")
	stub := &stubTSWrite{errList: deny}

	_, _, err := scanTSDatabases(context.Background(), stub, acct, testRegion, st, testScanID)
	if err != nil {
		t.Fatalf("got %v; want nil (soft-skip)", err)
	}
	if !warned {
		t.Errorf("real IAM denial should record a ScanWarning")
	}
}

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

// tsInfluxScanHandlers serves cluster c-1, instance i-1, parameter group pg-1
// and the backup pages, with empty Timestream for LiveAnalytics listings;
// overrides replace a handler by op name.
func tsInfluxScanHandlers(backups *tsiBackupPages, overrides map[string]func(any) (any, error)) map[string]func(any) (any, error) {
	h := map[string]func(any) (any, error){
		// timestreamwrite and timestreamquery discover their endpoint first.
		"DescribeEndpoints": func(in any) (any, error) {
			ep := ptrStr("timestream.us-east-1.amazonaws.com")
			if _, ok := in.(*timestreamquery.DescribeEndpointsInput); ok {
				return &timestreamquery.DescribeEndpointsOutput{Endpoints: []tsqtypes.Endpoint{{Address: ep, CachePeriodInMinutes: 1}}}, nil
			}
			return &timestreamwrite.DescribeEndpointsOutput{Endpoints: []tswtypes.Endpoint{{Address: ep, CachePeriodInMinutes: 1}}}, nil
		},
		"ListDatabases":        func(any) (any, error) { return &timestreamwrite.ListDatabasesOutput{}, nil },
		"ListTables":           func(any) (any, error) { return &timestreamwrite.ListTablesOutput{}, nil },
		"ListScheduledQueries": func(any) (any, error) { return &timestreamquery.ListScheduledQueriesOutput{}, nil },
		"ListDbClusters": func(any) (any, error) {
			return &timestreaminfluxdb.ListDbClustersOutput{Items: []tsitypes.DbClusterSummary{{Arn: ptrStr(tsiClusterARN), Id: ptrStr("c-1"), Name: ptrStr("c1")}}}, nil
		},
		"GetDbCluster": func(any) (any, error) {
			return &timestreaminfluxdb.GetDbClusterOutput{Arn: ptrStr(tsiClusterARN), Id: ptrStr("c-1"), Name: ptrStr("c1")}, nil
		},
		"ListDbInstances": func(any) (any, error) {
			return &timestreaminfluxdb.ListDbInstancesOutput{Items: []tsitypes.DbInstanceSummary{{Arn: ptrStr(tsiInstanceARN), Id: ptrStr("i-1"), Name: ptrStr("i1")}}}, nil
		},
		"GetDbInstance": func(any) (any, error) {
			return &timestreaminfluxdb.GetDbInstanceOutput{Arn: ptrStr(tsiInstanceARN), Id: ptrStr("i-1"), Name: ptrStr("i1")}, nil
		},
		"ListDbParameterGroups": func(any) (any, error) {
			return &timestreaminfluxdb.ListDbParameterGroupsOutput{Items: []tsitypes.DbParameterGroupSummary{{Arn: ptrStr(tsiPGARN), Id: ptrStr("pg-1"), Name: ptrStr("pg1")}}}, nil
		},
		"ListDbBackups": backups.handler(nil),
	}
	for op, fn := range overrides {
		h[op] = fn
	}
	return h
}

// The backup phase runs in the scan's own region after the other InfluxDB
// phases, and contains each backup under the cluster or instance they stored.
func TestScanTimestream_StoresBackupsUnderSources(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	var backups tsiBackupPages
	var regions parentCalls
	cfg := opStubConfig(tsInfluxScanHandlers(&backups, nil), tsiDenied)
	cfg.Region = "ap-southeast-2"
	acct.cfg = recordCallRegions(cfg, &regions)

	total, _, err := scanTimestream(context.Background(), acct, "us-east-1", st, testScanID)
	if err != nil {
		t.Fatalf("scanTimestream: %v", err)
	}
	if total != 7 {
		t.Errorf("total = %d, want 7 (1 cluster, 1 instance, 1 parameter group, 4 backups)", total)
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

// Each InfluxDB phase's hard error reaches the caller without costing any
// other phase its rows; a failed source phase leaves its backups stored
// without a parent.
func TestScanTSInflux_PhaseErrorKeepsOtherPhases(t *testing.T) {
	fail := func(any) (any, error) { return nil, tsiValidation }
	for _, tc := range []struct {
		name         string
		failOp       string
		wantTotal    int
		wantTypes    map[string]int
		wantContains [][2]string
		wantOrphans  []string
	}{
		{
			name: "clusters", failOp: "ListDbClusters", wantTotal: 6,
			wantTypes:    map[string]int{TypeTimestreamInfluxDBCluster: 0, TypeTimestreamInfluxDBInstance: 1, TypeTimestreamInfluxDBParameterGroup: 1, TypeTimestreamInfluxDBBackup: 4},
			wantContains: [][2]string{{tsiInstanceARN, tsiBackup2ARN}},
			wantOrphans:  []string{tsiBackup1ARN},
		},
		{
			name: "instances", failOp: "ListDbInstances", wantTotal: 6,
			wantTypes:    map[string]int{TypeTimestreamInfluxDBCluster: 1, TypeTimestreamInfluxDBInstance: 0, TypeTimestreamInfluxDBParameterGroup: 1, TypeTimestreamInfluxDBBackup: 4},
			wantContains: [][2]string{{tsiClusterARN, tsiBackup1ARN}},
			wantOrphans:  []string{tsiBackup2ARN},
		},
		{
			name: "parameter groups", failOp: "ListDbParameterGroups", wantTotal: 6,
			wantTypes:    map[string]int{TypeTimestreamInfluxDBCluster: 1, TypeTimestreamInfluxDBInstance: 1, TypeTimestreamInfluxDBParameterGroup: 0, TypeTimestreamInfluxDBBackup: 4},
			wantContains: [][2]string{{tsiClusterARN, tsiBackup1ARN}, {tsiInstanceARN, tsiBackup2ARN}},
		},
		{
			name: "backups", failOp: "ListDbBackups", wantTotal: 3,
			wantTypes: map[string]int{TypeTimestreamInfluxDBCluster: 1, TypeTimestreamInfluxDBInstance: 1, TypeTimestreamInfluxDBParameterGroup: 1, TypeTimestreamInfluxDBBackup: 0},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			var backups tsiBackupPages
			client := timestreaminfluxdb.NewFromConfig(opStubConfig(tsInfluxScanHandlers(&backups, map[string]func(any) (any, error){tc.failOp: fail}), tsiDenied))

			total, _, err := scanTSInflux(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID)
			assertAPICode(t, err, "ValidationException")
			if total != tc.wantTotal {
				t.Errorf("total = %d, want %d", total, tc.wantTotal)
			}
			for typ, want := range tc.wantTypes {
				if got := len(rowsByNativeID(t, st, typ)); got != want {
					t.Errorf("%s rows = %d, want %d", typ, got, want)
				}
			}
			for _, pc := range tc.wantContains {
				assertContainsNative(t, st, pc[0], pc[1])
			}
			for _, orphan := range tc.wantOrphans {
				rels, err := st.RelationshipsTo(store.ResourceID("aws", testAccountID, orphan))
				if err != nil {
					t.Fatalf("RelationshipsTo: %v", err)
				}
				if len(rels) != 0 {
					t.Errorf("backup %s of a failed source phase has relationships %v, want none", orphan, rels)
				}
			}
		})
	}
}

// When Timestream for LiveAnalytics is closed to the account, the other
// LiveAnalytics phases are skipped but InfluxDB is still scanned; the
// not-entitled sentinel is returned only when InfluxDB found nothing and did
// not fail.
func TestScanTimestream_LiveAnalyticsClosedStillScansInflux(t *testing.T) {
	closed := apiErr("AccessDeniedException", "Only existing Timestream for LiveAnalytics customers can access the service")
	empty := map[string]func(any) (any, error){
		"ListDbClusters":        func(any) (any, error) { return &timestreaminfluxdb.ListDbClustersOutput{}, nil },
		"ListDbInstances":       func(any) (any, error) { return &timestreaminfluxdb.ListDbInstancesOutput{}, nil },
		"ListDbParameterGroups": func(any) (any, error) { return &timestreaminfluxdb.ListDbParameterGroupsOutput{}, nil },
		"ListDbBackups":         func(any) (any, error) { return &timestreaminfluxdb.ListDbBackupsOutput{}, nil },
	}
	for _, tc := range []struct {
		name            string
		influx          map[string]func(any) (any, error)
		wantTotal       int
		wantBackups     int
		wantNotEntitled bool
		wantCode        string
	}{
		{name: "influx rows", wantTotal: 7, wantBackups: 4},
		{name: "influx empty", influx: empty, wantNotEntitled: true},
		{name: "influx error", influx: map[string]func(any) (any, error){
			"ListDbClusters": func(any) (any, error) { return nil, tsiValidation },
		}, wantTotal: 6, wantBackups: 4, wantCode: "ValidationException"},
		{name: "influx error, no rows", influx: map[string]func(any) (any, error){
			"ListDbClusters":        func(any) (any, error) { return nil, tsiValidation },
			"ListDbInstances":       empty["ListDbInstances"],
			"ListDbParameterGroups": empty["ListDbParameterGroups"],
			"ListDbBackups":         empty["ListDbBackups"],
		}, wantCode: "ValidationException"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			acct := newTestAccount(testAccountID)
			var backups tsiBackupPages
			var skipped parentCalls
			overrides := map[string]func(any) (any, error){
				"ListDatabases": func(any) (any, error) { return nil, closed },
				"ListTables": func(any) (any, error) {
					skipped.add("ListTables")
					return &timestreamwrite.ListTablesOutput{}, nil
				},
				"ListScheduledQueries": func(any) (any, error) {
					skipped.add("ListScheduledQueries")
					return &timestreamquery.ListScheduledQueriesOutput{}, nil
				},
			}
			maps.Copy(overrides, tc.influx)
			acct.cfg = opStubConfig(tsInfluxScanHandlers(&backups, overrides), tsiDenied)

			total, _, err := scanTimestream(context.Background(), acct, testRegion, st, testScanID)
			switch {
			case tc.wantNotEntitled:
				if !errors.Is(err, errServiceNotEntitled) || classifyServiceError(err) != outcomeNotEntitled {
					t.Fatalf("err = %v, want errServiceNotEntitled classified not entitled", err)
				}
			case tc.wantCode != "":
				assertAPICode(t, err, tc.wantCode)
				if errors.Is(err, errServiceNotEntitled) {
					t.Errorf("err = %v, must not carry errServiceNotEntitled", err)
				}
			case err != nil:
				t.Fatalf("err = %v, want nil", err)
			}
			if total != tc.wantTotal {
				t.Errorf("total = %d, want %d", total, tc.wantTotal)
			}
			if got := len(rowsByNativeID(t, st, TypeTimestreamInfluxDBBackup)); got != tc.wantBackups {
				t.Errorf("stored %d backups, want %d", got, tc.wantBackups)
			}
			if got := skipped.distinct(); len(got) != 0 {
				t.Errorf("LiveAnalytics phases %v ran after the closure, want none", got)
			}
		})
	}
}

// An Id that both a cluster and an instance carry is ambiguous, so its backup
// is stored without a parent; an unambiguous Id still gets its pair.
func TestScanTSInflux_AmbiguousSourceIDGetsNoPair(t *testing.T) {
	const dupInstanceARN = "arn:aws:timestream-influxdb:us-east-1:123456789012:db-instance/c-1"
	st := newTestStore(t)
	var backups tsiBackupPages
	handlers := tsInfluxScanHandlers(&backups, map[string]func(any) (any, error){
		"ListDbInstances": func(any) (any, error) {
			return &timestreaminfluxdb.ListDbInstancesOutput{Items: []tsitypes.DbInstanceSummary{
				{Arn: ptrStr(tsiInstanceARN), Id: ptrStr("i-1"), Name: ptrStr("i1")},
				{Arn: ptrStr(dupInstanceARN), Id: ptrStr("c-1"), Name: ptrStr("dup")},
			}}, nil
		},
		"GetDbInstance": func(in any) (any, error) {
			id := sv(in.(*timestreaminfluxdb.GetDbInstanceInput).Identifier)
			arn := tsiInstanceARN
			if id == "c-1" {
				arn = dupInstanceARN
			}
			return &timestreaminfluxdb.GetDbInstanceOutput{Arn: &arn, Id: &id}, nil
		},
	})
	client := timestreaminfluxdb.NewFromConfig(opStubConfig(handlers, tsiDenied))

	if _, _, err := scanTSInflux(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID); err != nil {
		t.Fatalf("scanTSInflux: %v", err)
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
