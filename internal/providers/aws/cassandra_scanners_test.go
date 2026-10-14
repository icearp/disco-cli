package aws

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"path/filepath"
	"slices"
	"sync"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/keyspaces"
	keyspacestypes "github.com/aws/aws-sdk-go-v2/service/keyspaces/types"
	"github.com/aws/aws-sdk-go-v2/service/keyspacesstreams"
	kstypes "github.com/aws/aws-sdk-go-v2/service/keyspacesstreams/types"
	"github.com/icearp/disco-cli/store"
)

var (
	ksDenied     = apiErr("AccessDeniedException", "User: arn:aws:iam::123456789012:user/t is not authorized to perform: cassandra:ListStreams")
	ksValidation = apiErr("ValidationException", "bad")
)

const (
	ksKeyspaceARN = "arn:aws:cassandra:us-east-1:123456789012:/keyspace/ks1/"
	ksTable1ARN   = "arn:aws:cassandra:us-east-1:123456789012:/keyspace/ks1/table/t1"
	ksTable2ARN   = "arn:aws:cassandra:us-east-1:123456789012:/keyspace/ks1/table/t2"
	ksStream1ARN  = "arn:aws:cassandra:us-east-1:123456789012:/keyspace/ks1/table/t1/stream/2025-05-20T20:51:22.896"
	ksStream2ARN  = "arn:aws:cassandra:us-east-1:123456789012:/keyspace/ks1/table/t2/stream/2025-06-01T08:00:00.000"
	ksStream3ARN  = "arn:aws:cassandra:us-east-1:123456789012:/keyspace/ks2/table/t9/stream/2025-07-01T08:00:00.000"
)

// ksStreamPages serves a two-page ListStreams: page one answers a call without
// a token, page two the call carrying "p2", unless pageTwoErr replaces it.
// Every call's input is recorded.
type ksStreamPages struct {
	mu     sync.Mutex
	inputs []keyspacesstreams.ListStreamsInput
}

// handler serves ks1/t1's stream on page one; ks1/t2's, ks2/t9's and an
// element without an ARN on page two.
func (p *ksStreamPages) handler(pageTwoErr error) func(in any) (any, error) {
	return func(in any) (any, error) {
		req, ok := in.(*keyspacesstreams.ListStreamsInput)
		if !ok {
			return nil, errors.New("unexpected ListStreams input")
		}
		p.mu.Lock()
		p.inputs = append(p.inputs, *req)
		p.mu.Unlock()
		if req.NextToken == nil {
			return &keyspacesstreams.ListStreamsOutput{Streams: []kstypes.Stream{{
				StreamArn: ptrStr(ksStream1ARN), StreamLabel: ptrStr("2025-05-20T20:51:22.896"),
				KeyspaceName: ptrStr("ks1"), TableName: ptrStr("t1"),
			}}, NextToken: ptrStr("p2")}, nil
		}
		if pageTwoErr != nil {
			return nil, pageTwoErr
		}
		return &keyspacesstreams.ListStreamsOutput{Streams: []kstypes.Stream{
			{StreamArn: ptrStr(ksStream2ARN), StreamLabel: ptrStr("2025-06-01T08:00:00.000"), KeyspaceName: ptrStr("ks1"), TableName: ptrStr("t2")},
			{StreamArn: ptrStr(ksStream3ARN), StreamLabel: ptrStr("2025-07-01T08:00:00.000"), KeyspaceName: ptrStr("ks2"), TableName: ptrStr("t9")},
			{StreamLabel: ptrStr("no-arn"), KeyspaceName: ptrStr("ks1"), TableName: ptrStr("t1")},
		}}, nil
	}
}

func scanKSStreamsWith(st *store.Store, tableARNs map[cassandraTableKey]string, handler func(in any) (any, error)) (int, error) {
	client := keyspacesstreams.NewFromConfig(opStubConfig(map[string]func(any) (any, error){"ListStreams": handler}, ksDenied))
	total, _, err := scanCassandraStreams(context.Background(), client, tableARNs, newTestAccount(testAccountID), testRegion, st, testScanID)
	return total, err
}

// Both pages are stored unfiltered at page size 100, an element without an ARN
// is skipped, and each stream whose table the tables phase stored is contained
// by it.
func TestScanCassandraStreams_StoresEveryPage(t *testing.T) {
	st := newTestStore(t)
	seedParentRows(t, st, TypeCassandraTable, ksTable1ARN, ksTable2ARN)
	var pages ksStreamPages
	tables := map[cassandraTableKey]string{{"ks1", "t1"}: ksTable1ARN, {"ks1", "t2"}: ksTable2ARN}

	total, err := scanKSStreamsWith(st, tables, pages.handler(nil))
	if err != nil {
		t.Fatalf("scanCassandraStreams: %v", err)
	}
	if total != 3 {
		t.Errorf("total = %d, want 3", total)
	}
	if len(pages.inputs) != 2 {
		t.Fatalf("ListStreams called %d times, want 2", len(pages.inputs))
	}
	for _, in := range pages.inputs {
		if in.KeyspaceName != nil || in.TableName != nil || in.MaxResults == nil || *in.MaxResults != 100 {
			t.Errorf("ListStreams input = keyspace %v, table %v, max %v; want unfiltered at 100", in.KeyspaceName, in.TableName, in.MaxResults)
		}
	}

	rows := rowsByNativeID(t, st, TypeCassandraStream)
	if len(rows) != 3 {
		t.Fatalf("stored %d streams, want 3: %v", len(rows), rows)
	}
	r, ok := rows[ksStream1ARN]
	if !ok {
		t.Fatalf("missing stream %s", ksStream1ARN)
	}
	if sv(r.Name) != "2025-05-20T20:51:22.896" || sv(r.Region) != "us-east-1" || r.Type != "aws:cassandra:stream" {
		t.Errorf("row = name %q, region %q, type %q", sv(r.Name), sv(r.Region), r.Type)
	}
	var attrs struct{ KeyspaceName, TableName string }
	if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil || attrs.KeyspaceName != "ks1" || attrs.TableName != "t1" {
		t.Errorf("attributes = %s (%v), want KeyspaceName ks1, TableName t1", r.AttributesJSON, err)
	}

	assertContainsNative(t, st, ksTable1ARN, ksStream1ARN)
	assertContainsNative(t, st, ksTable2ARN, ksStream2ARN)
	rels, err := st.RelationshipsTo(store.ResourceID("aws", testAccountID, ksStream3ARN))
	if err != nil {
		t.Fatalf("RelationshipsTo: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("stream of an unlisted table has relationships %v, want none", rels)
	}
}

func TestScanCassandraStreams_Empty(t *testing.T) {
	st := newTestStore(t)
	total, err := scanKSStreamsWith(st, nil, func(any) (any, error) { return &keyspacesstreams.ListStreamsOutput{}, nil })
	if err != nil || total != 0 {
		t.Errorf("total = %d, err = %v; want 0, nil", total, err)
	}
}

// On the first call, the unserved-endpoint shapes are a silent feature gap, and
// a network failure or a denial warns once; any other error is returned.
func TestScanCassandraStreams_FirstPageError(t *testing.T) {
	for _, tc := range []struct {
		name      string
		err       error
		wantCode  string
		wantWarns int
	}{
		{name: "unknown operation", err: apiErr("UnknownOperationException", "")},
		{name: "unrouted gateway", err: apiErr("NotFoundException", "Unable to determine service/operation name to be authorized")},
		{name: "unrouted gateway denial", err: apiErr("AccessDeniedException", "Unable to determine service/operation name to be authorized")},
		{name: "http 404", err: respErrCode(404)},
		{name: "nxdomain", err: &net.DNSError{Err: "no such host", Name: "cassandra-streams.us-east-1.api.aws", IsNotFound: true}, wantWarns: 1},
		{name: "connection refused", err: &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connection refused")}, wantWarns: 1},
		{name: "denied", err: ksDenied, wantWarns: 1},
		{name: "other", err: ksValidation, wantCode: "ValidationException"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := countOpWarnings(st, "cassandra:ListStreams")
			total, err := scanKSStreamsWith(st, nil, func(any) (any, error) { return nil, tc.err })
			if tc.wantCode != "" {
				assertAPICode(t, err, tc.wantCode)
			} else if err != nil {
				t.Fatalf("err = %v, want nil", err)
			}
			if total != 0 || *warns != tc.wantWarns {
				t.Errorf("total = %d, warnings = %d; want 0, %d", total, *warns, tc.wantWarns)
			}
		})
	}
}

// After a page has succeeded, a denial or a network failure warns once and keeps
// page one's row; every other error, the not-deployed shapes included, is
// returned with page one's row stored and contained by its table.
func TestScanCassandraStreams_PageTwoError(t *testing.T) {
	for _, tc := range []struct {
		name      string
		err       error
		wantCode  string
		wantHTTP  int
		wantWarns int
	}{
		{name: "denied", err: ksDenied, wantWarns: 1},
		{name: "unknown operation", err: apiErr("UnknownOperationException", ""), wantCode: "UnknownOperationException"},
		{name: "http 404", err: respErrCode(404), wantHTTP: 404},
		{name: "nxdomain", err: &net.DNSError{Err: "no such host", IsNotFound: true}, wantWarns: 1},
		{name: "throttled", err: apiErr("ThrottlingException", "slow down"), wantWarns: 1},
		{name: "other", err: ksValidation, wantCode: "ValidationException"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			seedParentRows(t, st, TypeCassandraTable, ksTable1ARN)
			warns := countOpWarnings(st, "cassandra:ListStreams")
			var pages ksStreamPages
			total, err := scanKSStreamsWith(st, map[cassandraTableKey]string{{"ks1", "t1"}: ksTable1ARN}, pages.handler(tc.err))
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
			rows := rowsByNativeID(t, st, TypeCassandraStream)
			if _, ok := rows[ksStream1ARN]; total != 1 || len(rows) != 1 || !ok {
				t.Errorf("total = %d, rows = %v; want %s only", total, rows, ksStream1ARN)
			}
			assertContainsNative(t, st, ksTable1ARN, ksStream1ARN)
		})
	}
}

// A failed write of the rows listed before a list error reports both errors.
func TestScanCassandraStreams_WriteAndListErrorsBothReported(t *testing.T) {
	// Not newTestStore: its cleanup queries the DB, which a closed store can't serve.
	st, err := store.Open(filepath.Join(t.TempDir(), "closed.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := st.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	var pages ksStreamPages
	_, err = scanKSStreamsWith(st, nil, pages.handler(ksValidation))
	if !errors.Is(err, store.ErrStoreWrite) {
		t.Errorf("err = %v, want store.ErrStoreWrite", err)
	}
	assertAPICode(t, err, "ValidationException")
}

// cassandraScanHandlers serves keyspace ks1 holding tables t1 and t2 and type
// addr, and the stream pages; overrides replace a handler by op name.
func cassandraScanHandlers(streams *ksStreamPages, overrides map[string]func(any) (any, error)) map[string]func(any) (any, error) {
	h := map[string]func(any) (any, error){
		"ListKeyspaces": func(any) (any, error) {
			return &keyspaces.ListKeyspacesOutput{Keyspaces: []keyspacestypes.KeyspaceSummary{{
				KeyspaceName: ptrStr("ks1"), ResourceArn: ptrStr(ksKeyspaceARN),
			}}}, nil
		},
		"ListTables": func(any) (any, error) {
			return &keyspaces.ListTablesOutput{Tables: []keyspacestypes.TableSummary{
				{KeyspaceName: ptrStr("ks1"), TableName: ptrStr("t1"), ResourceArn: ptrStr(ksTable1ARN)},
				{KeyspaceName: ptrStr("ks1"), TableName: ptrStr("t2"), ResourceArn: ptrStr(ksTable2ARN)},
			}}, nil
		},
		"GetTable": func(in any) (any, error) {
			req := in.(*keyspaces.GetTableInput)
			arn := ksTable1ARN
			if sv(req.TableName) == "t2" {
				arn = ksTable2ARN
			}
			return &keyspaces.GetTableOutput{KeyspaceName: req.KeyspaceName, TableName: req.TableName, ResourceArn: &arn}, nil
		},
		"ListTypes":   func(any) (any, error) { return &keyspaces.ListTypesOutput{Types: []string{"addr"}}, nil },
		"ListStreams": streams.handler(nil),
	}
	for op, fn := range overrides {
		h[op] = fn
	}
	return h
}

// The stream phase runs in the scan's own region after the keyspace phases,
// and contains each stream under the table that phase stored.
func TestScanCassandra_StoresStreamsUnderTables(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	var streams ksStreamPages
	var regions parentCalls
	cfg := opStubConfig(cassandraScanHandlers(&streams, nil), ksDenied)
	cfg.Region = "ap-southeast-2"
	acct.cfg = recordCallRegions(cfg, &regions)

	total, _, err := scanCassandra(context.Background(), acct, "us-east-1", st, testScanID)
	if err != nil {
		t.Fatalf("scanCassandra: %v", err)
	}
	if total != 7 {
		t.Errorf("total = %d, want 7 (1 keyspace, 2 tables, 1 type, 3 streams)", total)
	}
	if got := regions.distinct(); !slices.Equal(got, []string{"us-east-1"}) {
		t.Errorf("calls went to regions %v, want [us-east-1]", got)
	}
	if r, ok := rowsByNativeID(t, st, TypeCassandraStream)[ksStream1ARN]; !ok || sv(r.Region) != "us-east-1" {
		t.Errorf("stream %s missing or not in us-east-1", ksStream1ARN)
	}
	assertContainsNative(t, st, ksTable1ARN, ksStream1ARN)
	assertContainsNative(t, st, ksTable2ARN, ksStream2ARN)
}

// Each phase's hard error reaches the caller without costing any other phase
// its rows: the keyspace phases' failures leave the streams stored, and a
// stream failure leaves the keyspace, tables and type stored.
func TestScanCassandra_PhaseErrorKeepsOtherPhases(t *testing.T) {
	fail := func(any) (any, error) { return nil, ksValidation }
	for _, tc := range []struct {
		name      string
		failOp    string
		wantTotal int
		wantTypes map[string]int
	}{
		{name: "keyspaces", failOp: "ListKeyspaces", wantTotal: 3, wantTypes: map[string]int{TypeCassandraKeyspace: 0, TypeCassandraTable: 0, TypeCassandraType: 0, TypeCassandraStream: 3}},
		{name: "tables", failOp: "ListTables", wantTotal: 5, wantTypes: map[string]int{TypeCassandraKeyspace: 1, TypeCassandraTable: 0, TypeCassandraType: 1, TypeCassandraStream: 3}},
		{name: "types", failOp: "ListTypes", wantTotal: 6, wantTypes: map[string]int{TypeCassandraKeyspace: 1, TypeCassandraTable: 2, TypeCassandraType: 0, TypeCassandraStream: 3}},
		{name: "streams", failOp: "ListStreams", wantTotal: 4, wantTypes: map[string]int{TypeCassandraKeyspace: 1, TypeCassandraTable: 2, TypeCassandraType: 1, TypeCassandraStream: 0}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			acct := newTestAccount(testAccountID)
			var streams ksStreamPages
			acct.cfg = opStubConfig(cassandraScanHandlers(&streams, map[string]func(any) (any, error){tc.failOp: fail}), ksDenied)

			total, _, err := scanCassandra(context.Background(), acct, testRegion, st, testScanID)
			assertAPICode(t, err, "ValidationException")
			if total != tc.wantTotal {
				t.Errorf("total = %d, want %d", total, tc.wantTotal)
			}
			for typ, want := range tc.wantTypes {
				if got := len(rowsByNativeID(t, st, typ)); got != want {
					t.Errorf("%s rows = %d, want %d", typ, got, want)
				}
			}
		})
	}
}

// An unreachable streams endpoint warns once and returns nil, so the keyspace
// phases' rows and counts survive dispatch instead of the whole service being
// reclassified by the network error.
func TestScanCassandra_StreamsNXDOMAINKeepsOtherPhases(t *testing.T) {
	st := newTestStore(t)
	warns := countOpWarnings(st, "cassandra:ListStreams")
	acct := newTestAccount(testAccountID)
	var streams ksStreamPages
	acct.cfg = opStubConfig(cassandraScanHandlers(&streams, map[string]func(any) (any, error){
		"ListStreams": func(any) (any, error) {
			return nil, &net.DNSError{Err: "no such host", Name: "cassandra-streams.us-east-1.api.aws", IsNotFound: true}
		},
	}), ksDenied)

	total, _, err := scanCassandra(context.Background(), acct, testRegion, st, testScanID)
	if err != nil {
		t.Fatalf("scanCassandra: %v", err)
	}
	if total != 4 || *warns != 1 {
		t.Errorf("total = %d, warnings = %d; want 4 (1 keyspace, 2 tables, 1 type), 1", total, *warns)
	}
	for typ, want := range map[string]int{TypeCassandraKeyspace: 1, TypeCassandraTable: 2, TypeCassandraType: 1, TypeCassandraStream: 0} {
		if got := len(rowsByNativeID(t, st, typ)); got != want {
			t.Errorf("%s rows = %d, want %d", typ, got, want)
		}
	}
}

// A table whose GetTable failed is stored from its summary and still contains
// its stream.
func TestScanCassandra_SummaryOnlyTableParentsStream(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	var streams ksStreamPages
	acct.cfg = opStubConfig(cassandraScanHandlers(&streams, map[string]func(any) (any, error){
		"GetTable": func(any) (any, error) { return nil, ksValidation },
	}), ksDenied)

	if _, _, err := scanCassandra(context.Background(), acct, testRegion, st, testScanID); err != nil {
		t.Fatalf("scanCassandra: %v", err)
	}
	r, ok := rowsByNativeID(t, st, TypeCassandraTable)[ksTable1ARN]
	if !ok {
		t.Fatalf("missing table %s", ksTable1ARN)
	}
	var attrs map[string]any
	if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
		t.Fatalf("table attributes %s: %v", r.AttributesJSON, err)
	}
	// GetTableOutput marshals SchemaDefinition even when nil; the summary has no such key.
	if _, detail := attrs["SchemaDefinition"]; detail {
		t.Errorf("table attributes = %s, want the summary only", r.AttributesJSON)
	}
	assertContainsNative(t, st, ksTable1ARN, ksStream1ARN)
}
