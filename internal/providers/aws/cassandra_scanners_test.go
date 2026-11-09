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

	"github.com/aws/aws-sdk-go-v2/service/keyspaces"
	keyspacestypes "github.com/aws/aws-sdk-go-v2/service/keyspaces/types"
	"github.com/aws/aws-sdk-go-v2/service/keyspacesstreams"
	kstypes "github.com/aws/aws-sdk-go-v2/service/keyspacesstreams/types"
	"github.com/icearp/disco-cli/store"
)

var (
	ksDenied     = apiErr("AccessDeniedException", "User: arn:aws:iam::123456789012:user/t is not authorized to perform: cassandra:Select")
	ksValidation = apiErr("ValidationException", "bad")
	ksGone       = apiErr("ResourceNotFoundException", "not found")
	ksThrottle   = apiErr("ThrottlingException", "slow down")
)

const (
	ksKeyspaceARN    = "arn:aws:cassandra:us-east-1:123456789012:/keyspace/ks1/"
	ksKeyspace2ARN   = "arn:aws:cassandra:us-east-1:123456789012:/keyspace/ks2/"
	ksSystemARN      = "arn:aws:cassandra:us-east-1:123456789012:/keyspace/system/"
	ksSystemTableARN = "arn:aws:cassandra:us-east-1:123456789012:/keyspace/system/table/local"
	ksTable1ARN      = "arn:aws:cassandra:us-east-1:123456789012:/keyspace/ks1/table/t1"
	ksTable2ARN      = "arn:aws:cassandra:us-east-1:123456789012:/keyspace/ks1/table/t2"
	ksTable3ARN      = "arn:aws:cassandra:us-east-1:123456789012:/keyspace/ks2/table/t3"
	ksTable4ARN      = "arn:aws:cassandra:us-east-1:123456789012:/keyspace/ks2/table/t4"
	ksStream1ARN     = "arn:aws:cassandra:us-east-1:123456789012:/keyspace/ks1/table/t1/stream/2025-05-20T20:51:22.896"
	ksStream2ARN     = "arn:aws:cassandra:us-east-1:123456789012:/keyspace/ks1/table/t2/stream/2025-06-01T08:00:00.000"
	ksStream3ARN     = "arn:aws:cassandra:us-east-1:123456789012:/keyspace/ks2/table/t1/stream/2025-07-01T08:00:00.000"
	// ksScanTotal is the default scan's rows: 1 keyspace, 2 tables, 1 type
	// and 3 streams.
	ksScanTotal = 7
)

// ksStreamPages serves a two-page ListStreams: page one answers a call without
// a token, page two the call carrying "p2", unless pageTwoErr replaces it.
// Every call's input is recorded.
type ksStreamPages struct {
	mu     sync.Mutex
	inputs []keyspacesstreams.ListStreamsInput
}

// handler serves ks1/t1's stream on page one; ks1/t2's, ks2/t1's and an
// element without an ARN on page two. ks2/t1 shares ks1/t1's table name, so
// a stream pairs with a table only through its keyspace too.
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
			{StreamArn: ptrStr(ksStream3ARN), StreamLabel: ptrStr("2025-07-01T08:00:00.000"), KeyspaceName: ptrStr("ks2"), TableName: ptrStr("t1")},
			{StreamLabel: ptrStr("no-arn"), KeyspaceName: ptrStr("ks1"), TableName: ptrStr("t1"), StreamArn: nil},
		}}, nil
	}
}

func (p *ksStreamPages) calls() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.inputs)
}

func ksTag(k, v string) keyspacestypes.Tag { return keyspacestypes.Tag{Key: &k, Value: &v} }

// ksTagStub answers ListTagsForResource with tags or errs, both keyed by ARN
// and fixed before the scan, and records every input.
type ksTagStub struct {
	tags map[string][]keyspacestypes.Tag
	errs map[string]error

	mu     sync.Mutex
	inputs []keyspaces.ListTagsForResourceInput
}

// newKSTagStub tags keyspace ks1, table t1 and stream 1; the other rows have
// no tags.
func newKSTagStub() *ksTagStub {
	return &ksTagStub{
		tags: map[string][]keyspacestypes.Tag{
			ksKeyspaceARN: {ksTag("env", "prod")},
			ksTable1ARN:   {ksTag("team", "data")},
			ksStream1ARN:  {ksTag("cdc", "on")},
		},
		errs: map[string]error{},
	}
}

func (s *ksTagStub) handler(in any) (any, error) {
	req, ok := in.(*keyspaces.ListTagsForResourceInput)
	if !ok {
		return nil, errors.New("unexpected ListTagsForResource input")
	}
	s.mu.Lock()
	s.inputs = append(s.inputs, *req)
	s.mu.Unlock()
	arn := sv(req.ResourceArn)
	if err, ok := s.errs[arn]; ok {
		return nil, err
	}
	return &keyspaces.ListTagsForResourceOutput{Tags: s.tags[arn]}, nil
}

// sentARNs is the sorted ARNs every ListTagsForResource call was sent.
func (s *ksTagStub) sentARNs() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	arns := make([]string, 0, len(s.inputs))
	for _, in := range s.inputs {
		arns = append(arns, sv(in.ResourceArn))
	}
	slices.Sort(arns)
	return arns
}

// ksAllTagged is the sorted ARNs of every row of the default scan that has
// its tags read.
func ksAllTagged() []string {
	arns := []string{ksKeyspaceARN, ksTable1ARN, ksTable2ARN, ksStream1ARN, ksStream2ARN, ksStream3ARN}
	slices.Sort(arns)
	return arns
}

func scanKSStreamsWith(st *store.Store, tableARNs map[cassandraTableKey]string, handler func(in any) (any, error)) (int, error) {
	tags := newKSTagStub()
	cfg := opStubConfig(map[string]func(any) (any, error){"ListStreams": handler, "ListTagsForResource": tags.handler}, ksDenied)
	acct := newTestAccount(testAccountID)
	var p phaseTotals
	scanCassandraStreams(context.Background(), keyspacesstreams.NewFromConfig(cfg), tableARNs, acct, testRegion, st, testScanID, cassandraTags(keyspaces.NewFromConfig(cfg)), &p)
	total, err := p.result(st, acct, testRegion)
	return total, err
}

// ksAttrs decodes a row's attributes.
func ksAttrs(t *testing.T, r store.Resource) map[string]any {
	t.Helper()
	var attrs map[string]any
	if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
		t.Fatalf("attributes %s: %v", r.AttributesJSON, err)
	}
	return attrs
}

// Both pages are stored unfiltered at page size 100 with their tags, an
// element without an ARN is warned about, and each stream whose table the
// tables phase stored is contained by it; ks2/t1's stream is not contained by
// ks1/t1.
func TestScanCassandraStreams_StoresEveryPage(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	warns := countOpWarnings(st, "cassandra:ListStreams")
	seedParentRows(t, st, TypeCassandraTable, ksTable1ARN, ksTable2ARN)
	var pages ksStreamPages
	tables := map[cassandraTableKey]string{{"ks1", "t1"}: ksTable1ARN, {"ks1", "t2"}: ksTable2ARN}

	total, err := scanKSStreamsWith(st, tables, pages.handler(nil))
	if err != nil {
		t.Fatalf("scanCassandraStreams: %v", err)
	}
	if total != 3 || *warns != 1 {
		t.Errorf("total = %d, warnings = %d; want 3, 1 (the element without an ARN)", total, *warns)
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
	if sv(r.Name) != "2025-05-20T20:51:22.896" || sv(r.Region) != "us-east-1" || r.Type != "aws:cassandra:stream" || r.ManagedByProvider {
		t.Errorf("row = name %q, region %q, type %q, managed %v", sv(r.Name), sv(r.Region), r.Type, r.ManagedByProvider)
	}
	if a := ksAttrs(t, r); a["KeyspaceName"] != "ks1" || a["TableName"] != "t1" {
		t.Errorf("attributes = %s, want KeyspaceName ks1, TableName t1", r.AttributesJSON)
	}
	assertTagsMap(t, r.TagsJSON, map[string]string{"cdc": "on"})
	if r2 := rows[ksStream2ARN]; r2.TagsJSON != nil {
		t.Errorf("untagged stream TagsJSON = %q; want nil", *r2.TagsJSON)
	}

	assertContainsNative(t, st, ksTable1ARN, ksStream1ARN)
	assertContainsNative(t, st, ksTable2ARN, ksStream2ARN)
	rels, err := st.RelationshipsTo(store.ResourceID("aws", testAccountID, ksStream3ARN))
	if err != nil {
		t.Fatalf("RelationshipsTo: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("stream of ks2/t1, a table not stored, has relationships %v; want none (not ks1/t1)", rels)
	}
}

func TestScanCassandraStreams_Empty(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	total, err := scanKSStreamsWith(st, nil, func(any) (any, error) { return &keyspacesstreams.ListStreamsOutput{}, nil })
	if err != nil || total != 0 {
		t.Errorf("total = %d, err = %v; want 0, nil", total, err)
	}
}

// On the first call, the unserved-endpoint shapes are a silent feature gap, and
// an NXDOMAIN or a denial warns once; any other error, a refused connection
// included, is returned.
func TestScanCassandraStreams_FirstPageError(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name          string
		err           error
		wantCode      string
		wantTransient bool
		wantWarns     int
	}{
		{name: "unknown operation", err: apiErr("UnknownOperationException", "")},
		{name: "unrouted gateway", err: apiErr("NotFoundException", "Unable to determine service/operation name to be authorized")},
		{name: "unrouted gateway denial", err: apiErr("AccessDeniedException", "Unable to determine service/operation name to be authorized")},
		{name: "http 404", err: respErrCode(404)},
		{name: "nxdomain", err: &net.DNSError{Err: "no such host", Name: "cassandra-streams.us-east-1.api.aws", IsNotFound: true}, wantWarns: 1},
		{name: "connection refused", err: &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connection refused")}, wantTransient: true},
		{name: "denied", err: ksDenied, wantWarns: 1},
		{name: "other", err: ksValidation, wantCode: "ValidationException"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := countOpWarnings(st, "cassandra:ListStreams")
			total, err := scanKSStreamsWith(st, nil, func(any) (any, error) { return nil, tc.err })
			switch {
			case tc.wantCode != "":
				assertAPICode(t, err, tc.wantCode)
			case tc.wantTransient:
				if got := classifyServiceError(err); got != outcomeTransient {
					t.Fatalf("classifyServiceError(%v) = %v, want outcomeTransient", err, got)
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
// every other error, the not-deployed shapes, a throttle and an NXDOMAIN
// included, is returned with page one's row stored and contained by its
// table. The NXDOMAIN is returned as a plain error, so the dispatcher does not
// report the region unavailable and drop the counts.
func TestScanCassandraStreams_PageTwoError(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name        string
		err         error
		wantCode    string
		wantHTTP    int
		wantOutcome serviceOutcome
		wantWarns   int
	}{
		{name: "denied", err: ksDenied, wantWarns: 1},
		{name: "unknown operation", err: apiErr("UnknownOperationException", ""), wantCode: "UnknownOperationException"},
		{name: "http 404", err: respErrCode(404), wantHTTP: 404},
		{name: "nxdomain", err: &net.DNSError{Err: "no such host", IsNotFound: true}, wantOutcome: outcomeError},
		{name: "throttled", err: ksThrottle, wantOutcome: outcomeTransient},
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
			case tc.wantOutcome != outcomeStoreWrite:
				if got := classifyServiceError(err); err == nil || got != tc.wantOutcome {
					t.Fatalf("classifyServiceError(%v) = %v, want %v", err, got, tc.wantOutcome)
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

// A failed write of page one stops the listing: page two is not read, and the
// write's error is returned.
func TestScanCassandraStreams_FailedWriteStopsListing(t *testing.T) {
	t.Parallel()
	// Not newTestStore: its cleanup queries the DB, which a closed store can't serve.
	st, err := store.Open(filepath.Join(t.TempDir(), "closed.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := st.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	var pages ksStreamPages
	_, err = scanKSStreamsWith(st, nil, pages.handler(nil))
	if !errors.Is(err, store.ErrStoreWrite) {
		t.Errorf("err = %v, want store.ErrStoreWrite", err)
	}
	if n := pages.calls(); n != 1 {
		t.Errorf("ListStreams called %d times; want 1", n)
	}
}

// cassandraScanHandlers serves keyspace ks1 holding tables t1 and t2 and type
// addr, the stream pages and the tags of tags; overrides replace a handler by
// op name.
func cassandraScanHandlers(streams *ksStreamPages, tags *ksTagStub, overrides map[string]func(any) (any, error)) map[string]func(any) (any, error) {
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
		"ListTypes":           func(any) (any, error) { return &keyspaces.ListTypesOutput{Types: []string{"addr"}}, nil },
		"ListStreams":         streams.handler(nil),
		"ListTagsForResource": tags.handler,
	}
	for op, fn := range overrides {
		h[op] = fn
	}
	return h
}

// recordKSGetTables wraps handlers' GetTable to record each call's
// "keyspace/table".
func recordKSGetTables(handlers map[string]func(any) (any, error)) *parentCalls {
	var calls parentCalls
	getTable := handlers["GetTable"]
	handlers["GetTable"] = func(in any) (any, error) {
		req := in.(*keyspaces.GetTableInput)
		calls.add(sv(req.KeyspaceName) + "/" + sv(req.TableName))
		return getTable(in)
	}
	return &calls
}

func ksScan(ctx context.Context, st *store.Store, handlers map[string]func(any) (any, error)) (int, error) {
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(handlers, ksDenied)
	return scanCassandra(ctx, acct, testRegion, st, testScanID)
}

// Keyspaces, tables and streams are stored with the tags ListTagsForResource
// returns for their listed ARNs, each read at page size 1000; a row without
// tags has none, and user-defined types are not read.
func TestScanCassandra_StoresTagsOfEachType(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	var streams ksStreamPages
	tags := newKSTagStub()
	handlers := cassandraScanHandlers(&streams, tags, nil)
	getTables := recordKSGetTables(handlers)

	counted, upserts := countUpserts(st)
	total, err := ksScan(context.Background(), counted, handlers)
	inserted := upserts()
	if err != nil {
		t.Fatalf("scanCassandra: %v", err)
	}
	if got := getTables.distinct(); !slices.Equal(got, []string{"ks1/t1", "ks1/t2"}) {
		t.Errorf("GetTable sent %v; want [ks1/t1 ks1/t2]", got)
	}
	if total != ksScanTotal || inserted != ksScanTotal {
		t.Errorf("total, inserted = %d, %d; want %d, %d", total, inserted, ksScanTotal, ksScanTotal)
	}
	if got := tags.sentARNs(); !slices.Equal(got, ksAllTagged()) {
		t.Errorf("ListTagsForResource sent %v; want %v", got, ksAllTagged())
	}
	for _, in := range tags.inputs {
		if in.MaxResults == nil || *in.MaxResults != 1000 || in.NextToken != nil {
			t.Errorf("ListTagsForResource %s: MaxResults %v, NextToken %v; want 1000, nil", sv(in.ResourceArn), in.MaxResults, in.NextToken)
		}
	}

	ks := rowsByNativeID(t, st, TypeCassandraKeyspace)[ksKeyspaceARN]
	assertTagsMap(t, ks.TagsJSON, map[string]string{"env": "prod"})
	if sv(ks.Name) != "ks1" || sv(ks.Region) != testRegion || ks.ManagedByProvider || ksAttrs(t, ks)["ResourceArn"] != ksKeyspaceARN {
		t.Errorf("keyspace row = name %q, region %q, managed %v, attrs %s", sv(ks.Name), sv(ks.Region), ks.ManagedByProvider, ks.AttributesJSON)
	}

	tables := rowsByNativeID(t, st, TypeCassandraTable)
	t1 := tables[ksTable1ARN]
	assertTagsMap(t, t1.TagsJSON, map[string]string{"team": "data"})
	// GetTableOutput marshals SchemaDefinition even when nil; the summary has no such key.
	if _, detail := ksAttrs(t, t1)["SchemaDefinition"]; !detail || sv(t1.Name) != "t1" || sv(t1.Region) != testRegion || t1.ManagedByProvider {
		t.Errorf("table row = name %q, region %q, managed %v, attrs %s; want the GetTable body", sv(t1.Name), sv(t1.Region), t1.ManagedByProvider, t1.AttributesJSON)
	}
	if t2 := tables[ksTable2ARN]; t2.TagsJSON != nil {
		t.Errorf("untagged table TagsJSON = %q; want nil", *t2.TagsJSON)
	}

	s1 := rowsByNativeID(t, st, TypeCassandraStream)[ksStream1ARN]
	assertTagsMap(t, s1.TagsJSON, map[string]string{"cdc": "on"})

	for _, r := range []store.Resource{ks, t1, s1} {
		if r.Provider != "aws" || r.AccountID != testAccountID || sv(r.AccountName) != "Test Account" {
			t.Errorf("%s: provider %q, account %q, account name %q; want aws, %s, Test Account", r.NativeID, r.Provider, r.AccountID, sv(r.AccountName), testAccountID)
		}
	}

	typ := onlyRow(t, st, TypeCassandraType)
	if typ.TagsJSON != nil || sv(typ.Name) != "addr" {
		t.Errorf("type row = name %q, tags %v; want addr, nil", sv(typ.Name), typ.TagsJSON)
	}
}

// With no keyspaces and no streams nothing is stored and no tag, table or
// type read is made.
func TestScanCassandra_NoKeyspaces(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	tags := newKSTagStub()
	var streams ksStreamPages
	var tableCalls parentCalls
	total, err := ksScan(context.Background(), st, cassandraScanHandlers(&streams, tags, map[string]func(any) (any, error){
		"ListKeyspaces": func(any) (any, error) { return &keyspaces.ListKeyspacesOutput{}, nil },
		"ListTables": func(in any) (any, error) {
			tableCalls.add(sv(in.(*keyspaces.ListTablesInput).KeyspaceName))
			return &keyspaces.ListTablesOutput{}, nil
		},
		"ListStreams": func(any) (any, error) { return &keyspacesstreams.ListStreamsOutput{}, nil },
	}))
	if err != nil || total != 0 {
		t.Errorf("total, err = %d, %v; want 0, nil", total, err)
	}
	if got := tags.sentARNs(); len(got) != 0 || len(tableCalls.distinct()) != 0 {
		t.Errorf("tag reads %v, ListTables keyspaces %v; want none", got, tableCalls.distinct())
	}
}

// A system keyspace and its tables are stored managed and untagged, with no
// tag read and no GetTable; ks1's rows beside them are read.
func TestScanCassandra_SystemKeyspaceNotRead(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	var streams ksStreamPages
	tags := newKSTagStub()
	handlers := cassandraScanHandlers(&streams, tags, nil)
	getTables := recordKSGetTables(handlers)
	handlers["ListKeyspaces"] = func(any) (any, error) {
		return &keyspaces.ListKeyspacesOutput{Keyspaces: []keyspacestypes.KeyspaceSummary{
			{KeyspaceName: ptrStr("ks1"), ResourceArn: ptrStr(ksKeyspaceARN)},
			{KeyspaceName: ptrStr("system"), ResourceArn: ptrStr(ksSystemARN)},
		}}, nil
	}
	listTables := handlers["ListTables"]
	handlers["ListTables"] = func(in any) (any, error) {
		if sv(in.(*keyspaces.ListTablesInput).KeyspaceName) == "system" {
			return &keyspaces.ListTablesOutput{Tables: []keyspacestypes.TableSummary{
				{KeyspaceName: ptrStr("system"), TableName: ptrStr("local"), ResourceArn: ptrStr(ksSystemTableARN)},
			}}, nil
		}
		return listTables(in)
	}

	if _, err := ksScan(context.Background(), st, handlers); err != nil {
		t.Fatalf("scanCassandra: %v", err)
	}
	if got := tags.sentARNs(); !slices.Equal(got, ksAllTagged()) {
		t.Errorf("ListTagsForResource sent %v; want %v", got, ksAllTagged())
	}
	if got := getTables.distinct(); !slices.Equal(got, []string{"ks1/t1", "ks1/t2"}) {
		t.Errorf("GetTable sent %v; want [ks1/t1 ks1/t2]", got)
	}
	sys := rowsByNativeID(t, st, TypeCassandraKeyspace)[ksSystemARN]
	sysTable := rowsByNativeID(t, st, TypeCassandraTable)[ksSystemTableARN]
	for _, r := range []store.Resource{sys, sysTable} {
		if !r.ManagedByProvider || r.TagsJSON != nil {
			t.Errorf("%s: managed %v, tags %v; want managed, untagged", r.NativeID, r.ManagedByProvider, r.TagsJSON)
		}
	}
	if _, detail := ksAttrs(t, sysTable)["SchemaDefinition"]; detail || sv(sysTable.Name) != "local" {
		t.Errorf("system table = name %q, attrs %s; want local, the summary", sv(sysTable.Name), sysTable.AttributesJSON)
	}
}

// A failed tag read. ResourceNotFoundException is modeled, so a resource
// deleted since it was listed is dropped silently (a keyspace with its
// tables and types, which are not listed). A denial keeps the row untagged
// and warns once under the tag op with the scan's scope; a throttle keeps it
// and is transient; an NXDOMAIN after the listings reached the host and any
// other error keep it and are errors.
func TestScanCassandra_TagReadFailures(t *testing.T) {
	t.Parallel()
	nx := &net.DNSError{Err: "no such host", Name: "cassandra.us-east-1.amazonaws.com", IsNotFound: true}
	for _, c := range []struct {
		name      string
		arn, typ  string
		err       error
		outcome   serviceOutcome // -1: no error
		code      string
		wantTotal int
		wantKept  bool
		warn      bool
	}{
		{"keyspace/not found", ksKeyspaceARN, TypeCassandraKeyspace, ksGone, -1, "", 3, false, false},
		{"keyspace/denied", ksKeyspaceARN, TypeCassandraKeyspace, ksDenied, -1, "", ksScanTotal, true, true},
		{"keyspace/transient", ksKeyspaceARN, TypeCassandraKeyspace, ksThrottle, outcomeTransient, "ThrottlingException", ksScanTotal, true, false},
		{"keyspace/nxdomain", ksKeyspaceARN, TypeCassandraKeyspace, nx, outcomeError, "", ksScanTotal, true, false},
		{"keyspace/other error", ksKeyspaceARN, TypeCassandraKeyspace, ksValidation, outcomeError, "ValidationException", ksScanTotal, true, false},
		{"table/not found", ksTable1ARN, TypeCassandraTable, ksGone, -1, "", ksScanTotal - 1, false, false},
		{"table/denied", ksTable1ARN, TypeCassandraTable, ksDenied, -1, "", ksScanTotal, true, true},
		{"table/transient", ksTable1ARN, TypeCassandraTable, ksThrottle, outcomeTransient, "ThrottlingException", ksScanTotal, true, false},
		{"table/other error", ksTable1ARN, TypeCassandraTable, ksValidation, outcomeError, "ValidationException", ksScanTotal, true, false},
		{"stream/not found", ksStream1ARN, TypeCassandraStream, ksGone, -1, "", ksScanTotal - 1, false, false},
		{"stream/denied", ksStream1ARN, TypeCassandraStream, ksDenied, -1, "", ksScanTotal, true, true},
		{"stream/transient", ksStream1ARN, TypeCassandraStream, ksThrottle, outcomeTransient, "ThrottlingException", ksScanTotal, true, false},
		{"stream/other error", ksStream1ARN, TypeCassandraStream, ksValidation, outcomeError, "ValidationException", ksScanTotal, true, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := bacWarnings(st, cassandraTagsOp)
			var streams ksStreamPages
			tags := newKSTagStub()
			tags.errs[c.arn] = c.err

			total, err := ksScan(context.Background(), st, cassandraScanHandlers(&streams, tags, nil))
			if c.outcome < 0 {
				if err != nil {
					t.Fatalf("err = %v; want nil", err)
				}
			} else {
				assertGlueOutcome(t, err, c.outcome)
				if c.code != "" {
					assertAPICode(t, err, c.code)
				}
			}
			if total != c.wantTotal {
				t.Errorf("total = %d; want %d", total, c.wantTotal)
			}
			r, ok := rowsByNativeID(t, st, c.typ)[c.arn]
			if ok != c.wantKept {
				t.Fatalf("row stored = %v; want %v", ok, c.wantKept)
			}
			if ok && r.TagsJSON != nil {
				t.Errorf("TagsJSON = %q; want nil", *r.TagsJSON)
			}
			w := *warns
			if c.warn != (len(w) == 1) || len(w) > 1 {
				t.Fatalf("tag warnings = %v; want one: %v", w, c.warn)
			}
			for _, x := range w {
				if x.Scope != testAccountID+"/"+testRegion {
					t.Errorf("warning Scope %q; want %s/%s", x.Scope, testAccountID, testRegion)
				}
			}
			// The rows of the other types keep their tags.
			sibling, siblingType, siblingTags := ksKeyspaceARN, TypeCassandraKeyspace, map[string]string{"env": "prod"}
			if c.typ == TypeCassandraKeyspace {
				sibling, siblingType, siblingTags = ksStream1ARN, TypeCassandraStream, map[string]string{"cdc": "on"}
			}
			assertTagsMap(t, rowsByNativeID(t, st, siblingType)[sibling].TagsJSON, siblingTags)
		})
	}
}

// A keyspace dropped as not found has no tables or types listed, and a table
// dropped so contains no stream.
func TestScanCassandra_NotFoundParentsListNoChildren(t *testing.T) {
	t.Parallel()
	t.Run("keyspace", func(t *testing.T) {
		st := newTestStore(t)
		var streams ksStreamPages
		var tableCalls parentCalls
		tags := newKSTagStub()
		tags.errs[ksKeyspaceARN] = ksGone
		handlers := cassandraScanHandlers(&streams, tags, nil)
		listTables := handlers["ListTables"]
		handlers["ListTables"] = func(in any) (any, error) {
			tableCalls.add(sv(in.(*keyspaces.ListTablesInput).KeyspaceName))
			return listTables(in)
		}
		if _, err := ksScan(context.Background(), st, handlers); err != nil {
			t.Fatalf("scanCassandra: %v", err)
		}
		if n := len(tableCalls.distinct()); n != 0 || len(rowsByNativeID(t, st, TypeCassandraType)) != 0 {
			t.Errorf("ListTables keyspaces %v, types %d; want none", tableCalls.distinct(), len(rowsByNativeID(t, st, TypeCassandraType)))
		}
	})
	t.Run("table", func(t *testing.T) {
		st := newTestStore(t)
		var streams ksStreamPages
		tags := newKSTagStub()
		tags.errs[ksTable1ARN] = ksGone
		if _, err := ksScan(context.Background(), st, cassandraScanHandlers(&streams, tags, nil)); err != nil {
			t.Fatalf("scanCassandra: %v", err)
		}
		rels, err := st.RelationshipsTo(store.ResourceID("aws", testAccountID, ksStream1ARN))
		if err != nil {
			t.Fatalf("RelationshipsTo: %v", err)
		}
		if _, ok := rowsByNativeID(t, st, TypeCassandraStream)[ksStream1ARN]; !ok || len(rels) != 0 {
			t.Errorf("stream stored %v, relationships %v; want stored with none", ok, rels)
		}
	})
}

// Every tag read denied warns once for the whole scan and keeps every row.
func TestScanCassandra_TagDenialsWarnOncePerScan(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	warns := countOpWarnings(st, cassandraTagsOp)
	var streams ksStreamPages
	tags := newKSTagStub()
	for _, arn := range ksAllTagged() {
		tags.errs[arn] = ksDenied
	}
	total, err := ksScan(context.Background(), st, cassandraScanHandlers(&streams, tags, nil))
	if err != nil || total != ksScanTotal || *warns != 1 {
		t.Errorf("err, total, warnings = %v, %d, %d; want nil, %d, 1", err, total, *warns, ksScanTotal)
	}
}

// A throttled tag read beside another's hard error is demoted to one warning
// under the tag op, so the hard error classifies the scan.
func TestScanCassandra_TagThrottleDemotedBesideHardError(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	warns := countOpWarnings(st, cassandraTagsOp)
	var streams ksStreamPages
	tags := newKSTagStub()
	tags.errs[ksTable1ARN] = ksThrottle
	tags.errs[ksStream1ARN] = ksValidation
	total, err := ksScan(context.Background(), st, cassandraScanHandlers(&streams, tags, nil))
	assertGlueOutcome(t, err, outcomeError)
	assertAPICode(t, err, "ValidationException")
	if total != ksScanTotal || *warns != 1 {
		t.Errorf("total, warnings = %d, %d; want %d, 1", total, *warns, ksScanTotal)
	}
}

// ListTagsForResource pages are all read; a repeated nextToken fails the
// read, keeping the row untagged.
func TestScanCassandra_TagPages(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	var streams ksStreamPages
	var tableTokens parentCalls
	tags := newKSTagStub()
	handlers := cassandraScanHandlers(&streams, tags, map[string]func(any) (any, error){
		"ListTagsForResource": func(in any) (any, error) {
			req := in.(*keyspaces.ListTagsForResourceInput)
			switch sv(req.ResourceArn) {
			case ksKeyspaceARN:
				return &keyspaces.ListTagsForResourceOutput{Tags: []keyspacestypes.Tag{ksTag("a", "1")}, NextToken: ptrStr("same")}, nil
			case ksTable1ARN:
				tableTokens.add(sv(req.NextToken))
				if req.NextToken == nil {
					return &keyspaces.ListTagsForResourceOutput{Tags: []keyspacestypes.Tag{ksTag("a", "1")}, NextToken: ptrStr("t2")}, nil
				}
				return &keyspaces.ListTagsForResourceOutput{Tags: []keyspacestypes.Tag{ksTag("b", "2")}}, nil
			}
			return &keyspaces.ListTagsForResourceOutput{}, nil
		},
	})
	_, err := ksScan(context.Background(), st, handlers)
	if !errors.Is(err, errRepeatedNextToken) {
		t.Errorf("err = %v; want errRepeatedNextToken", err)
	}
	if got := tableTokens.distinct(); !slices.Equal(got, []string{"", "t2"}) {
		t.Errorf("table tag calls sent tokens %v; want [\"\" t2]", got)
	}
	assertTagsMap(t, rowsByNativeID(t, st, TypeCassandraTable)[ksTable1ARN].TagsJSON, map[string]string{"a": "1", "b": "2"})
	if ks, ok := rowsByNativeID(t, st, TypeCassandraKeyspace)[ksKeyspaceARN]; !ok || ks.TagsJSON != nil {
		t.Errorf("keyspace stored %v, tags %v; want stored untagged", ok, ks.TagsJSON)
	}
}

// A failed GetTable. ResourceNotFoundException is modeled, so a table deleted
// since it was listed is not stored and contains no stream. Any other
// failure stores the table as its summary with its tags: a denial warns once
// under GetTable, a throttle is transient, any other error is returned. The
// other table keeps its GetTable body.
func TestScanCassandra_GetTableFailures(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name     string
		err      error
		outcome  serviceOutcome // -1: no error
		code     string
		wantKept bool
		warn     bool
	}{
		{"not found", ksGone, -1, "", false, false},
		{"denied", ksDenied, -1, "", true, true},
		{"transient", ksThrottle, outcomeTransient, "ThrottlingException", true, false},
		{"other error", ksValidation, outcomeError, "ValidationException", true, false},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := countOpWarnings(st, cassandraGetTableOp)
			missingParents := countOpWarnings(st, "hierarchy")
			var streams ksStreamPages
			handlers := cassandraScanHandlers(&streams, newKSTagStub(), nil)
			getTable := handlers["GetTable"]
			handlers["GetTable"] = func(in any) (any, error) {
				if sv(in.(*keyspaces.GetTableInput).TableName) == "t1" {
					return nil, c.err
				}
				return getTable(in)
			}

			_, err := ksScan(context.Background(), st, handlers)
			if c.outcome < 0 {
				if err != nil {
					t.Fatalf("err = %v; want nil", err)
				}
			} else {
				assertGlueOutcome(t, err, c.outcome)
				assertAPICode(t, err, c.code)
			}
			if c.warn != (*warns == 1) || *warns > 1 {
				t.Errorf("GetTable warnings = %d; want one: %v", *warns, c.warn)
			}
			// A table not stored is not handed to the stream phase as a parent.
			if *missingParents != 0 {
				t.Errorf("missing hierarchy endpoint warnings = %d; want 0", *missingParents)
			}
			tables := rowsByNativeID(t, st, TypeCassandraTable)
			if _, detail := ksAttrs(t, tables[ksTable2ARN])["SchemaDefinition"]; !detail {
				t.Errorf("t2 attributes = %s; want its GetTable body", tables[ksTable2ARN].AttributesJSON)
			}
			t1, ok := tables[ksTable1ARN]
			if ok != c.wantKept {
				t.Fatalf("t1 stored = %v; want %v", ok, c.wantKept)
			}
			if !ok {
				rels, err := st.RelationshipsTo(store.ResourceID("aws", testAccountID, ksStream1ARN))
				if err != nil || len(rels) != 0 {
					t.Errorf("stream relationships = %v (%v); want none", rels, err)
				}
				return
			}
			if _, detail := ksAttrs(t, t1)["SchemaDefinition"]; detail {
				t.Errorf("t1 attributes = %s; want the summary", t1.AttributesJSON)
			}
			assertTagsMap(t, t1.TagsJSON, map[string]string{"team": "data"})
			assertContainsNative(t, st, ksTable1ARN, ksStream1ARN)
		})
	}
}

// ListTables runs per keyspace, each at page size 1000: one keyspace's
// failure costs the other none of its tables, and the pages it read before
// the failure are kept. A keyspace deleted since it was listed is skipped
// silently, a denied one warns once, a repeated nextToken is an error.
func TestScanCassandra_ListTablesPerKeyspace(t *testing.T) {
	t.Parallel()
	t3 := keyspacestypes.TableSummary{KeyspaceName: ptrStr("ks2"), TableName: ptrStr("t3"), ResourceArn: ptrStr(ksTable3ARN)}
	t4 := keyspacestypes.TableSummary{KeyspaceName: ptrStr("ks2"), TableName: ptrStr("t4"), ResourceArn: ptrStr(ksTable4ARN)}
	for _, c := range []struct {
		name       string
		page1Err   error
		page2Err   error
		page2Token *string
		wantErr    func(t *testing.T, err error)
		wantKS2    []string
		warns      int
	}{
		{name: "both pages", wantKS2: []string{ksTable3ARN, ksTable4ARN}},
		{name: "first page fails", page1Err: ksValidation, wantErr: func(t *testing.T, err error) { assertAPICode(t, err, "ValidationException") }},
		{name: "keyspace gone", page1Err: ksGone},
		{name: "keyspace denied", page1Err: ksDenied, warns: 1},
		{name: "second page fails", page2Err: ksValidation, wantKS2: []string{ksTable3ARN}, wantErr: func(t *testing.T, err error) { assertAPICode(t, err, "ValidationException") }},
		{name: "repeated token", page2Token: ptrStr("p2"), wantKS2: []string{ksTable3ARN, ksTable4ARN}, wantErr: func(t *testing.T, err error) {
			if !errors.Is(err, errRepeatedNextToken) {
				t.Errorf("err = %v; want errRepeatedNextToken", err)
			}
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := countOpWarnings(st, "cassandra:ListTables")
			var streams ksStreamPages
			var mu sync.Mutex
			var inputs []keyspaces.ListTablesInput
			handlers := cassandraScanHandlers(&streams, newKSTagStub(), nil)
			getTables := recordKSGetTables(handlers)
			listTables := handlers["ListTables"]
			handlers["ListKeyspaces"] = func(any) (any, error) {
				return &keyspaces.ListKeyspacesOutput{Keyspaces: []keyspacestypes.KeyspaceSummary{
					{KeyspaceName: ptrStr("ks1"), ResourceArn: ptrStr(ksKeyspaceARN)},
					{KeyspaceName: ptrStr("ks2"), ResourceArn: ptrStr(ksKeyspace2ARN)},
				}}, nil
			}
			handlers["ListTables"] = func(in any) (any, error) {
				req := in.(*keyspaces.ListTablesInput)
				mu.Lock()
				inputs = append(inputs, *req)
				mu.Unlock()
				switch {
				case sv(req.KeyspaceName) == "ks1":
					return listTables(in)
				case req.NextToken == nil && c.page1Err != nil:
					return nil, c.page1Err
				case req.NextToken == nil:
					return &keyspaces.ListTablesOutput{Tables: []keyspacestypes.TableSummary{t3}, NextToken: ptrStr("p2")}, nil
				case c.page2Err != nil:
					return nil, c.page2Err
				}
				return &keyspaces.ListTablesOutput{Tables: []keyspacestypes.TableSummary{t4}, NextToken: c.page2Token}, nil
			}

			_, err := ksScan(context.Background(), st, handlers)
			if c.wantErr != nil {
				c.wantErr(t, err)
			} else if err != nil {
				t.Fatalf("err = %v; want nil", err)
			}
			if *warns != c.warns {
				t.Errorf("ListTables warnings = %d; want %d", *warns, c.warns)
			}
			want := append([]string{ksTable1ARN, ksTable2ARN}, c.wantKS2...)
			slices.Sort(want)
			if got := slices.Sorted(maps.Keys(rowsByNativeID(t, st, TypeCassandraTable))); !slices.Equal(got, want) {
				t.Errorf("tables = %v; want %v", got, want)
			}
			// Each table's GetTable names the keyspace that listed it.
			wantGets := []string{"ks1/t1", "ks1/t2"}
			for _, arn := range c.wantKS2 {
				wantGets = append(wantGets, map[string]string{ksTable3ARN: "ks2/t3", ksTable4ARN: "ks2/t4"}[arn])
			}
			if got := getTables.distinct(); !slices.Equal(got, wantGets) {
				t.Errorf("GetTable sent %v; want %v", got, wantGets)
			}
			for _, in := range inputs {
				if in.MaxResults == nil || *in.MaxResults != 1000 || (sv(in.KeyspaceName) != "ks1" && sv(in.KeyspaceName) != "ks2") {
					t.Errorf("ListTables input keyspace %q, MaxResults %v; want ks1 or ks2 at 1000", sv(in.KeyspaceName), in.MaxResults)
				}
			}
		})
	}
}

// ListKeyspaces reads every page at page size 1000. A keyspace without an
// ARN is not stored and one without a name is stored without its children,
// each warned about once. A later page's failure keeps the earlier pages and
// their tables: a denial warns once more, any other error is returned; a
// repeated nextToken is an error.
func TestScanCassandra_ListKeyspacesPages(t *testing.T) {
	t.Parallel()
	const noNameARN = "arn:aws:cassandra:us-east-1:123456789012:/keyspace/unnamed/"
	for _, c := range []struct {
		name       string
		page2Err   error
		page2Token *string
		wantErr    func(t *testing.T, err error)
		wantKS2    bool
		warns      int
	}{
		{name: "both pages", wantKS2: true, warns: 2},
		{name: "second page fails", page2Err: ksValidation, warns: 2, wantErr: func(t *testing.T, err error) { assertAPICode(t, err, "ValidationException") }},
		{name: "second page denied", page2Err: ksDenied, warns: 3},
		{name: "repeated token", page2Token: ptrStr("k2"), wantKS2: true, warns: 2, wantErr: func(t *testing.T, err error) {
			if !errors.Is(err, errRepeatedNextToken) {
				t.Errorf("err = %v; want errRepeatedNextToken", err)
			}
		}},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := countOpWarnings(st, "cassandra:ListKeyspaces")
			var streams ksStreamPages
			var mu sync.Mutex
			var inputs []keyspaces.ListKeyspacesInput
			var tableCalls parentCalls
			handlers := cassandraScanHandlers(&streams, newKSTagStub(), nil)
			listTables := handlers["ListTables"]
			handlers["ListTables"] = func(in any) (any, error) {
				tableCalls.add(sv(in.(*keyspaces.ListTablesInput).KeyspaceName))
				return listTables(in)
			}
			handlers["ListKeyspaces"] = func(in any) (any, error) {
				req := in.(*keyspaces.ListKeyspacesInput)
				mu.Lock()
				inputs = append(inputs, *req)
				mu.Unlock()
				if req.NextToken == nil {
					return &keyspaces.ListKeyspacesOutput{Keyspaces: []keyspacestypes.KeyspaceSummary{
						{KeyspaceName: ptrStr("ks1"), ResourceArn: ptrStr(ksKeyspaceARN)},
						{KeyspaceName: ptrStr("no-arn")},
						{KeyspaceName: ptrStr(""), ResourceArn: ptrStr(noNameARN)},
					}, NextToken: ptrStr("k2")}, nil
				}
				if c.page2Err != nil {
					return nil, c.page2Err
				}
				return &keyspaces.ListKeyspacesOutput{Keyspaces: []keyspacestypes.KeyspaceSummary{
					{KeyspaceName: ptrStr("ks2"), ResourceArn: ptrStr(ksKeyspace2ARN)},
				}, NextToken: c.page2Token}, nil
			}

			_, err := ksScan(context.Background(), st, handlers)
			if c.wantErr != nil {
				c.wantErr(t, err)
			} else if err != nil {
				t.Fatalf("err = %v; want nil", err)
			}
			if len(inputs) != 2 {
				t.Fatalf("ListKeyspaces called %d times; want 2", len(inputs))
			}
			for _, in := range inputs {
				if in.MaxResults == nil || *in.MaxResults != 1000 {
					t.Errorf("ListKeyspaces MaxResults = %v; want 1000", in.MaxResults)
				}
			}
			if *warns != c.warns {
				t.Errorf("ListKeyspaces warnings = %d; want %d (no ARN, no name, a denial)", *warns, c.warns)
			}
			rows := rowsByNativeID(t, st, TypeCassandraKeyspace)
			if nn, ok := rows[noNameARN]; !ok || nn.Name != nil {
				t.Errorf("unnamed keyspace stored %v, name %v; want stored with no name", ok, nn.Name)
			}
			wantKeyspaces, wantTables := []string{ksKeyspaceARN, noNameARN}, []string{"ks1"}
			if c.wantKS2 {
				wantKeyspaces, wantTables = append(wantKeyspaces, ksKeyspace2ARN), []string{"ks1", "ks2"}
			}
			slices.Sort(wantKeyspaces)
			if got := slices.Sorted(maps.Keys(rows)); !slices.Equal(got, wantKeyspaces) {
				t.Errorf("keyspaces = %v; want %v", got, wantKeyspaces)
			}
			if got := tableCalls.distinct(); !slices.Equal(got, wantTables) {
				t.Errorf("ListTables keyspaces = %v; want %v", got, wantTables)
			}
		})
	}
}

// A denied first ListKeyspaces page warns once with the scan's scope and
// lists no tables or types; the streams are still stored.
func TestScanCassandra_ListKeyspacesDenied(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	warns := bacWarnings(st, "cassandra:ListKeyspaces")
	var streams ksStreamPages
	var tableCalls parentCalls
	total, err := ksScan(context.Background(), st, cassandraScanHandlers(&streams, newKSTagStub(), map[string]func(any) (any, error){
		"ListKeyspaces": func(any) (any, error) { return nil, ksDenied },
		"ListTables": func(in any) (any, error) {
			tableCalls.add(sv(in.(*keyspaces.ListTablesInput).KeyspaceName))
			return &keyspaces.ListTablesOutput{}, nil
		},
	}))
	if err != nil {
		t.Fatalf("scanCassandra: %v", err)
	}
	if len(*warns) != 1 || (*warns)[0].Scope != testAccountID+"/"+testRegion {
		t.Errorf("ListKeyspaces warnings = %v; want one scoped %s/%s", *warns, testAccountID, testRegion)
	}
	if got := tableCalls.distinct(); len(got) != 0 {
		t.Errorf("ListTables keyspaces = %v; want none", got)
	}
	if n := len(rowsByNativeID(t, st, TypeCassandraStream)); total != 3 || n != 3 {
		t.Errorf("total = %d, streams = %d; want 3, 3", total, n)
	}
}

// A table listed with an ARN but no name is stored as its summary with no
// name, and no GetTable is sent for it.
func TestScanCassandra_UnnamedTableKeepsSummary(t *testing.T) {
	t.Parallel()
	const noNameARN = "arn:aws:cassandra:us-east-1:123456789012:/keyspace/ks1/table/unnamed"
	st := newTestStore(t)
	var streams ksStreamPages
	handlers := cassandraScanHandlers(&streams, newKSTagStub(), nil)
	getTables := recordKSGetTables(handlers)
	listTables := handlers["ListTables"]
	handlers["ListTables"] = func(in any) (any, error) {
		out, err := listTables(in)
		if err != nil {
			return nil, err
		}
		tables := out.(*keyspaces.ListTablesOutput)
		tables.Tables = append(tables.Tables, keyspacestypes.TableSummary{KeyspaceName: ptrStr("ks1"), ResourceArn: ptrStr(noNameARN)})
		return tables, nil
	}
	if _, err := ksScan(context.Background(), st, handlers); err != nil {
		t.Fatalf("scanCassandra: %v", err)
	}
	if got := getTables.distinct(); !slices.Equal(got, []string{"ks1/t1", "ks1/t2"}) {
		t.Errorf("GetTable sent %v; want [ks1/t1 ks1/t2]", got)
	}
	r, ok := rowsByNativeID(t, st, TypeCassandraTable)[noNameARN]
	if !ok {
		t.Fatalf("unnamed table %s not stored", noNameARN)
	}
	if _, detail := ksAttrs(t, r)["SchemaDefinition"]; detail || r.Name != nil || ksAttrs(t, r)["ResourceArn"] != noNameARN {
		t.Errorf("unnamed table = name %v, attrs %s; want no name, the summary", r.Name, r.AttributesJSON)
	}
}

// In GovCloud the listed ARNs reach ListTagsForResource unchanged.
func TestScanCassandra_GovARNsReachTagOp(t *testing.T) {
	t.Parallel()
	const partition = "aws-us-gov"
	acct := partitionAccount(partition)
	region := acct.Regions[0]
	ksARN := partitionARN(partition, "cassandra", region, "/keyspace/ks1/")
	tableARN := partitionARN(partition, "cassandra", region, "/keyspace/ks1/table/t1")
	streamARN := partitionARN(partition, "cassandra", region, "/keyspace/ks1/table/t1/stream/2025-05-20T20:51:22.896")
	st := newTestStore(t)
	tags := &ksTagStub{tags: map[string][]keyspacestypes.Tag{tableARN: {ksTag("team", "data")}}, errs: map[string]error{}}
	acct.cfg = opStubConfig(map[string]func(any) (any, error){
		"ListKeyspaces": func(any) (any, error) {
			return &keyspaces.ListKeyspacesOutput{Keyspaces: []keyspacestypes.KeyspaceSummary{{KeyspaceName: ptrStr("ks1"), ResourceArn: &ksARN}}}, nil
		},
		"ListTables": func(any) (any, error) {
			return &keyspaces.ListTablesOutput{Tables: []keyspacestypes.TableSummary{{KeyspaceName: ptrStr("ks1"), TableName: ptrStr("t1"), ResourceArn: &tableARN}}}, nil
		},
		"GetTable":  func(any) (any, error) { return &keyspaces.GetTableOutput{ResourceArn: &tableARN}, nil },
		"ListTypes": func(any) (any, error) { return &keyspaces.ListTypesOutput{}, nil },
		"ListStreams": func(any) (any, error) {
			return &keyspacesstreams.ListStreamsOutput{Streams: []kstypes.Stream{{StreamArn: &streamARN, KeyspaceName: ptrStr("ks1"), TableName: ptrStr("t1")}}}, nil
		},
		"ListTagsForResource": tags.handler,
	}, ksDenied)

	if _, err := scanCassandra(context.Background(), acct, region, st, testScanID); err != nil {
		t.Fatalf("scanCassandra: %v", err)
	}
	want := []string{ksARN, tableARN, streamARN}
	slices.Sort(want)
	if got := tags.sentARNs(); !slices.Equal(got, want) {
		t.Errorf("ListTagsForResource sent %v; want %v", got, want)
	}
	assertTagsMap(t, rowsByNativeID(t, st, TypeCassandraTable)[tableARN].TagsJSON, map[string]string{"team": "data"})
	assertContainsNative(t, st, tableARN, streamARN)
}

// A failed keyspace write stops the scan: no later phase runs and no tag
// denial is warned about for the rows the write lost.
func TestScanCassandra_FailedWriteStopsWithoutTagWarning(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	refuseStoreWrites(t, st, TypeCassandraKeyspace)
	warns := countOpWarnings(st, cassandraTagsOp)
	var streams ksStreamPages
	var tableCalls parentCalls
	tags := newKSTagStub()
	tags.errs[ksKeyspaceARN] = ksDenied
	_, err := ksScan(context.Background(), st, cassandraScanHandlers(&streams, tags, map[string]func(any) (any, error){
		"ListTables": func(in any) (any, error) {
			tableCalls.add(sv(in.(*keyspaces.ListTablesInput).KeyspaceName))
			return &keyspaces.ListTablesOutput{}, nil
		},
	}))
	if !errors.Is(err, store.ErrStoreWrite) {
		t.Fatalf("err = %v; want store.ErrStoreWrite", err)
	}
	if *warns != 0 || len(tableCalls.distinct()) != 0 || streams.calls() != 0 {
		t.Errorf("tag warnings %d, ListTables keyspaces %v, ListStreams calls %d; want none", *warns, tableCalls.distinct(), streams.calls())
	}
}

// Once ctx ends, the stop is one member of the error although the keyspace
// phase saw it in its tag reads; the keyspace is kept untagged and the later
// phases are not run.
func TestScanCassandra_ContextEndReportedOnce(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var streams ksStreamPages
	handlers := cassandraScanHandlers(&streams, newKSTagStub(), map[string]func(any) (any, error){
		"ListTagsForResource": func(any) (any, error) {
			cancel()
			return nil, context.Canceled
		},
	})
	_, err := ksScan(ctx, st, handlers)
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
	if ks, ok := rowsByNativeID(t, st, TypeCassandraKeyspace)[ksKeyspaceARN]; !ok || ks.TagsJSON != nil {
		t.Errorf("keyspace stored %v, tags %v; want stored untagged", ok, ks.TagsJSON)
	}
	if n := streams.calls(); n != 0 || len(rowsByNativeID(t, st, TypeCassandraTable)) != 0 {
		t.Errorf("ListStreams calls %d, tables %d; want none", n, len(rowsByNativeID(t, st, TypeCassandraTable)))
	}
}

// The stream phase runs in the scan's own region after the keyspace phases,
// and contains each stream under the table that phase stored.
func TestScanCassandra_StoresStreamsUnderTables(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	var streams ksStreamPages
	var regions parentCalls
	cfg := opStubConfig(cassandraScanHandlers(&streams, newKSTagStub(), nil), ksDenied)
	cfg.Region = "ap-southeast-2"
	acct.cfg = recordCallRegions(cfg, &regions)

	total, err := scanCassandra(context.Background(), acct, "us-east-1", st, testScanID)
	if err != nil {
		t.Fatalf("scanCassandra: %v", err)
	}
	if total != ksScanTotal {
		t.Errorf("total = %d, want %d (1 keyspace, 2 tables, 1 type, 3 streams)", total, ksScanTotal)
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
	t.Parallel()
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
			var streams ksStreamPages
			total, err := ksScan(context.Background(), st, cassandraScanHandlers(&streams, newKSTagStub(), map[string]func(any) (any, error){tc.failOp: fail}))
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

// A streams endpoint that does not resolve warns once and returns nil, so the
// keyspace phases' rows and counts survive dispatch instead of the whole
// service being reported unavailable in the region.
func TestScanCassandra_StreamsNXDOMAINKeepsOtherPhases(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	warns := countOpWarnings(st, "cassandra:ListStreams")
	var streams ksStreamPages
	total, err := ksScan(context.Background(), st, cassandraScanHandlers(&streams, newKSTagStub(), map[string]func(any) (any, error){
		"ListStreams": func(any) (any, error) {
			return nil, &net.DNSError{Err: "no such host", Name: "cassandra-streams.us-east-1.api.aws", IsNotFound: true}
		},
	}))
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

// A throttled streams listing beside no other error is returned, so the scan
// is marked partial with the keyspace phases' rows counted.
func TestScanCassandra_LoneStreamsThrottleMarksPartial(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	warns := countOpWarnings(st, "cassandra:ListStreams")
	var streams ksStreamPages
	total, err := ksScan(context.Background(), st, cassandraScanHandlers(&streams, newKSTagStub(), map[string]func(any) (any, error){
		"ListStreams": func(any) (any, error) { return nil, ksThrottle },
	}))
	if got := classifyServiceError(err); got != outcomeTransient {
		t.Errorf("classifyServiceError(%v) = %v, want outcomeTransient", err, got)
	}
	if total != 4 || *warns != 0 {
		t.Errorf("total = %d, warnings = %d; want 4 (1 keyspace, 2 tables, 1 type), 0", total, *warns)
	}
}

// A per-keyspace phase's NXDOMAIN, after ListKeyspaces reached the same host,
// is a plain error, so the dispatcher does not report the region unavailable
// and drop the counts of the rows already stored.
func TestScanCassandra_LatePhaseNXDOMAINKeepsCounts(t *testing.T) {
	t.Parallel()
	for _, op := range []string{"ListTables", "ListTypes"} {
		t.Run(op, func(t *testing.T) {
			st := newTestStore(t)
			var streams ksStreamPages
			total, err := ksScan(context.Background(), st, cassandraScanHandlers(&streams, newKSTagStub(), map[string]func(any) (any, error){
				op: func(any) (any, error) {
					return nil, &net.DNSError{Err: "no such host", Name: "cassandra.us-east-1.amazonaws.com", IsNotFound: true}
				},
			}))
			if got := classifyServiceError(err); err == nil || got != outcomeError {
				t.Errorf("classifyServiceError(%v) = %v, want outcomeError", err, got)
			}
			if stored := countStoredRows(t, st); total == 0 || total != stored {
				t.Errorf("total = %d, stored rows = %d; want the other phases' rows counted", total, stored)
			}
		})
	}
}

// A region whose Keyspaces endpoint does not resolve has no Keyspaces, so the
// streams endpoint is not probed and warns nothing.
func TestScanCassandra_KeyspacesNXDOMAINSkipsStreams(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	warns := countOpWarnings(st, "cassandra:ListStreams")
	var streams ksStreamPages
	_, err := ksScan(context.Background(), st, cassandraScanHandlers(&streams, newKSTagStub(), map[string]func(any) (any, error){
		"ListKeyspaces": func(any) (any, error) {
			return nil, &net.DNSError{Err: "no such host", Name: "cassandra.us-east-1.amazonaws.com", IsNotFound: true}
		},
	}))
	if got := classifyServiceError(err); got != outcomeUnavailable {
		t.Errorf("classifyServiceError(%v) = %v, want outcomeUnavailable", err, got)
	}
	if n := streams.calls(); n != 0 || *warns != 0 {
		t.Errorf("ListStreams calls = %d, warnings = %d; want 0, 0", n, *warns)
	}
}
