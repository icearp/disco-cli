package aws

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/snowball"
	sbtypes "github.com/aws/aws-sdk-go-v2/service/snowball/types"
	"github.com/aws/smithy-go"
	"github.com/icearp/disco-cli/store"
)

var (
	sbDenied    = &smithy.GenericAPIError{Code: "AccessDeniedException", Message: "User: arn:aws:iam::123456789012:user/t is not authorized to perform: snowball:Describe"}
	sbThrottled = &smithy.GenericAPIError{Code: "ThrottlingException", Message: "rate exceeded"}
	sbHardErr   = &smithy.GenericAPIError{Code: "UnrecognizedFailureException", Message: "boom"}
	sbGone      = &smithy.GenericAPIError{Code: "InvalidResourceException", Message: "The specified resource can't be found"}
	sbCreated   = time.Date(2026, 9, 1, 8, 0, 0, 0, time.UTC)
)

func sbClient(handlers map[string]func(in any) (any, error)) *snowball.Client {
	return snowball.NewFromConfig(opStubConfig(handlers, sbDenied))
}

func sbAddressID(n string) string { return "ADID" + n + "0000000-0000-0000-0000-000000000000" }
func sbClusterID(n string) string { return "CID" + n + "0000000-0000-0000-0000-000000000000" }

func sbAddressNative(n string) string {
	return "arn:aws:snowball::" + testAccountID + ":address/" + sbAddressID(n)
}

func sbClusterNative(region, n string) string {
	return "arn:aws:snowball:" + region + ":" + testAccountID + ":cluster/" + sbClusterID(n)
}

func sbAddress(n string) sbtypes.Address {
	return sbtypes.Address{
		AddressId: ptrStr(sbAddressID(n)), Name: ptrStr("Recipient " + n), City: ptrStr("City " + n),
		Country: ptrStr("US"), Type: sbtypes.AddressTypeCustPickup,
	}
}

func sbCluster(n string) *sbtypes.ClusterMetadata {
	return &sbtypes.ClusterMetadata{
		ClusterId: ptrStr(sbClusterID(n)), ClusterState: sbtypes.ClusterStateInUse, CreationDate: &sbCreated,
		AddressId: ptrStr(sbAddressID(n)), KmsKeyARN: ptrStr("arn:aws:kms:us-east-1:123456789012:key/k-" + n),
		JobType: sbtypes.JobTypeLocalUse,
	}
}

func TestScanSnowballAddresses_StoresEveryPage(t *testing.T) {
	st := newTestStore(t)
	var (
		mu       sync.Mutex
		maxSizes []int32
	)
	handlers := map[string]func(any) (any, error){
		"DescribeAddresses": func(in any) (any, error) {
			req := in.(*snowball.DescribeAddressesInput)
			mu.Lock()
			maxSizes = append(maxSizes, *req.MaxResults)
			mu.Unlock()
			if req.NextToken == nil {
				return &snowball.DescribeAddressesOutput{Addresses: []sbtypes.Address{sbAddress("1"), {City: ptrStr("no id")}}, NextToken: ptrStr("p2")}, nil
			}
			return &snowball.DescribeAddressesOutput{Addresses: []sbtypes.Address{sbAddress("2")}}, nil
		},
	}
	complete, total, _, err := scanSnowballAddresses(context.Background(), sbClient(handlers), newTestAccount(testAccountID), testRegion, testRegion, st, testScanID)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if total != 2 || !complete {
		t.Errorf("total, complete = %d, %v; want 2, true", total, complete)
	}
	if !slices.Equal(maxSizes, []int32{100, 100}) {
		t.Errorf("MaxResults = %v, want [100 100]", maxSizes)
	}
	rows := rowsByNativeID(t, st, TypeSnowballAddress)
	if len(rows) != 2 {
		t.Errorf("address rows = %d, want 2", len(rows))
	}
	if _, ok := rows[sbAddressNative("1")]; !ok {
		t.Errorf("address %s missing", sbAddressNative("1"))
	}
	got, ok := rows[sbAddressNative("2")]
	if !ok {
		t.Fatalf("address %s missing", sbAddressNative("2"))
	}
	if got.Type != TypeSnowballAddress || sv(got.Region) != testRegion || got.Name != nil {
		t.Errorf("row = type %q region %q name %v; want %q %q nil", got.Type, sv(got.Region), got.Name, TypeSnowballAddress, testRegion)
	}
	var attrs struct{ City, Type string }
	if err := json.Unmarshal([]byte(got.AttributesJSON), &attrs); err != nil || attrs.City != "City 2" || attrs.Type != "CUST_PICKUP" {
		t.Errorf("attrs = %+v (%v), want City 2, CUST_PICKUP", attrs, err)
	}
}

func TestScanSnowballAddresses_Errors(t *testing.T) {
	for _, tc := range []struct {
		name         string
		err          error
		wantErr      bool
		wantWarns    int
		wantComplete bool
	}{
		{"empty", nil, false, 0, true},
		{"access denied warns", sbDenied, false, 1, false},
		{"other error propagates", sbHardErr, true, 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			warned := countOpWarnings(st, snowballDescribeAddressesOp)
			handlers := map[string]func(any) (any, error){
				"DescribeAddresses": func(any) (any, error) {
					if tc.err != nil {
						return nil, tc.err
					}
					return &snowball.DescribeAddressesOutput{}, nil
				},
			}
			complete, total, _, err := scanSnowballAddresses(context.Background(), sbClient(handlers), newTestAccount(testAccountID), testRegion, testRegion, st, testScanID)
			if tc.wantErr != (err != nil) || (tc.wantErr && !errors.Is(err, tc.err)) {
				t.Errorf("err = %v, want error %v", err, tc.wantErr)
			}
			if complete != tc.wantComplete {
				t.Errorf("complete = %v, want %v", complete, tc.wantComplete)
			}
			if total != 0 || len(rowsByNativeID(t, st, TypeSnowballAddress)) != 0 {
				t.Errorf("total = %d, want 0 and no rows", total)
			}
			if *warned != tc.wantWarns {
				t.Errorf("warnings = %d, want %d", *warned, tc.wantWarns)
			}
		})
	}
}

func TestScanSnowballAddresses_LaterPageErrorKeepsStoredPages(t *testing.T) {
	st := newTestStore(t)
	handlers := map[string]func(any) (any, error){
		"DescribeAddresses": func(in any) (any, error) {
			if in.(*snowball.DescribeAddressesInput).NextToken == nil {
				return &snowball.DescribeAddressesOutput{Addresses: []sbtypes.Address{sbAddress("1")}, NextToken: ptrStr("p2")}, nil
			}
			return nil, sbHardErr
		},
	}
	complete, total, _, err := scanSnowballAddresses(context.Background(), sbClient(handlers), newTestAccount(testAccountID), testRegion, testRegion, st, testScanID)
	if !errors.Is(err, sbHardErr) {
		t.Errorf("err = %v, want the page-2 error", err)
	}
	if _, ok := rowsByNativeID(t, st, TypeSnowballAddress)[sbAddressNative("1")]; total != 1 || !ok || complete {
		t.Errorf("total = %d, page-1 address stored = %v, complete = %v; want 1, true, false", total, ok, complete)
	}
}

// sbClusterHandlers lists clusters 1 and 2 on two pages (plus an id-less
// entry) and describes each from sbCluster, except where describe overrides.
func sbClusterHandlers(calls *parentCalls, describe func(id string) (any, error)) map[string]func(any) (any, error) {
	return map[string]func(any) (any, error){
		"ListClusters": func(in any) (any, error) {
			if in.(*snowball.ListClustersInput).NextToken == nil {
				return &snowball.ListClustersOutput{
					ClusterListEntries: []sbtypes.ClusterListEntry{{ClusterId: ptrStr(sbClusterID("1"))}, {Description: ptrStr("no id")}},
					NextToken:          ptrStr("p2"),
				}, nil
			}
			return &snowball.ListClustersOutput{ClusterListEntries: []sbtypes.ClusterListEntry{{ClusterId: ptrStr(sbClusterID("2"))}}}, nil
		},
		"DescribeCluster": func(in any) (any, error) {
			id := *in.(*snowball.DescribeClusterInput).ClusterId
			calls.add(id)
			if describe != nil {
				return describe(id)
			}
			return &snowball.DescribeClusterOutput{ClusterMetadata: sbCluster(id[3:4])}, nil
		},
	}
}

func TestScanSnowballClusters_DescribesEveryListedCluster(t *testing.T) {
	st := newTestStore(t)
	calls := &parentCalls{}
	var (
		mu       sync.Mutex
		maxSizes []int32
	)
	handlers := sbClusterHandlers(calls, nil)
	list := handlers["ListClusters"]
	handlers["ListClusters"] = func(in any) (any, error) {
		mu.Lock()
		maxSizes = append(maxSizes, *in.(*snowball.ListClustersInput).MaxResults)
		mu.Unlock()
		return list(in)
	}
	total, _, err := scanSnowballClusters(context.Background(), sbClient(handlers), newTestAccount(testAccountID), testRegion, st, testScanID)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if total != 2 {
		t.Errorf("total = %d, want 2", total)
	}
	if !slices.Equal(maxSizes, []int32{100, 100}) {
		t.Errorf("MaxResults = %v, want [100 100]", maxSizes)
	}
	if got, want := calls.distinct(), []string{sbClusterID("1"), sbClusterID("2")}; !slices.Equal(got, want) {
		t.Errorf("clusters described %v, want %v", got, want)
	}
	rows := rowsByNativeID(t, st, TypeSnowballCluster)
	if len(rows) != 2 {
		t.Errorf("cluster rows = %d, want 2", len(rows))
	}
	if _, ok := rows[sbClusterNative(testRegion, "1")]; !ok {
		t.Errorf("cluster %s missing", sbClusterNative(testRegion, "1"))
	}
	got, ok := rows[sbClusterNative(testRegion, "2")]
	if !ok {
		t.Fatalf("cluster %s missing", sbClusterNative(testRegion, "2"))
	}
	if got.Type != TypeSnowballCluster || sv(got.Region) != testRegion || sv(got.Status) != "InUse" || sv(got.CreatedAt) != "2026-09-01T08:00:00Z" {
		t.Errorf("row = type %q region %q status %q created %q", got.Type, sv(got.Region), sv(got.Status), sv(got.CreatedAt))
	}
	var attrs struct {
		KmsKeyARN string
		AddressID string `json:"AddressId"`
	}
	if err := json.Unmarshal([]byte(got.AttributesJSON), &attrs); err != nil ||
		attrs.KmsKeyARN != "arn:aws:kms:us-east-1:123456789012:key/k-2" || attrs.AddressID != sbAddressID("2") {
		t.Errorf("attrs = %+v (%v), want cluster 2's key and address", attrs, err)
	}
}

// A cluster whose DescribeCluster returns no metadata is dropped; the other
// is stored.
func TestScanSnowballClusters_NoMetadataDropped(t *testing.T) {
	st := newTestStore(t)
	handlers := sbClusterHandlers(&parentCalls{}, func(id string) (any, error) {
		if id == sbClusterID("1") {
			return &snowball.DescribeClusterOutput{}, nil
		}
		return &snowball.DescribeClusterOutput{ClusterMetadata: sbCluster("2")}, nil
	})
	total, _, err := scanSnowballClusters(context.Background(), sbClient(handlers), newTestAccount(testAccountID), testRegion, st, testScanID)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	rows := rowsByNativeID(t, st, TypeSnowballCluster)
	if _, ok := rows[sbClusterNative(testRegion, "2")]; total != 1 || len(rows) != 1 || !ok {
		t.Errorf("total = %d, rows = %d, cluster 2 stored = %v; want 1, 1, true", total, len(rows), ok)
	}
}

func TestScanSnowballClusters_ListErrors(t *testing.T) {
	for _, tc := range []struct {
		name      string
		err       error
		wantErr   bool
		wantWarns int
	}{
		{"empty", nil, false, 0},
		{"access denied warns", sbDenied, false, 1},
		{"other error propagates", sbHardErr, true, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			warned := countOpWarnings(st, snowballListClustersOp)
			handlers := map[string]func(any) (any, error){
				"ListClusters": func(any) (any, error) {
					if tc.err != nil {
						return nil, tc.err
					}
					return &snowball.ListClustersOutput{}, nil
				},
			}
			total, _, err := scanSnowballClusters(context.Background(), sbClient(handlers), newTestAccount(testAccountID), testRegion, st, testScanID)
			if tc.wantErr != (err != nil) || (tc.wantErr && !errors.Is(err, tc.err)) {
				t.Errorf("err = %v, want error %v", err, tc.wantErr)
			}
			if total != 0 {
				t.Errorf("total = %d, want 0", total)
			}
			if *warned != tc.wantWarns {
				t.Errorf("warnings = %d, want %d", *warned, tc.wantWarns)
			}
		})
	}
}

// A failed second page still describes and stores the cluster the first
// page listed, and the error is returned.
func TestScanSnowballClusters_LaterListPageErrorStoresListedClusters(t *testing.T) {
	st := newTestStore(t)
	handlers := sbClusterHandlers(&parentCalls{}, nil)
	list := handlers["ListClusters"]
	handlers["ListClusters"] = func(in any) (any, error) {
		if in.(*snowball.ListClustersInput).NextToken != nil {
			return nil, sbHardErr
		}
		return list(in)
	}
	total, _, err := scanSnowballClusters(context.Background(), sbClient(handlers), newTestAccount(testAccountID), testRegion, st, testScanID)
	if !errors.Is(err, sbHardErr) {
		t.Errorf("err = %v, want the page-2 error", err)
	}
	if _, ok := rowsByNativeID(t, st, TypeSnowballCluster)[sbClusterNative(testRegion, "1")]; total != 1 || !ok {
		t.Errorf("total = %d, page-1 cluster stored = %v; want 1, true", total, ok)
	}
}

// Cluster 1 fails DescribeCluster with err; cluster 2 is kept unless the
// error is fatal.
func TestScanSnowballClusters_PerClusterErrors(t *testing.T) {
	for _, tc := range []struct {
		name      string
		err       error
		wantErr   bool
		wantWarns int
	}{
		{"gone after listing is silent", sbGone, false, 0},
		{"denied warns once", sbDenied, false, 1},
		{"other error propagates", sbHardErr, true, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			warned := countOpWarnings(st, snowballDescribeClusterOp)
			handlers := sbClusterHandlers(&parentCalls{}, func(id string) (any, error) {
				if id == sbClusterID("1") {
					return nil, tc.err
				}
				return &snowball.DescribeClusterOutput{ClusterMetadata: sbCluster("2")}, nil
			})
			total, _, err := scanSnowballClusters(context.Background(), sbClient(handlers), newTestAccount(testAccountID), testRegion, st, testScanID)
			if tc.wantErr {
				if !errors.Is(err, tc.err) {
					t.Errorf("err = %v, want %v", err, tc.err)
				}
				return
			}
			if err != nil {
				t.Fatalf("err = %v", err)
			}
			if *warned != tc.wantWarns {
				t.Errorf("warnings = %d, want %d", *warned, tc.wantWarns)
			}
			rows := rowsByNativeID(t, st, TypeSnowballCluster)
			if _, ok := rows[sbClusterNative(testRegion, "2")]; total != 1 || len(rows) != 1 || !ok {
				t.Errorf("total = %d, rows = %d, cluster 2 stored = %v; want 1, 1, true", total, len(rows), ok)
			}
		})
	}
}

func sbTopLevelHandlers(override map[string]func(any) (any, error)) map[string]func(any) (any, error) {
	h := sbClusterHandlers(&parentCalls{}, nil)
	h["DescribeAddresses"] = func(any) (any, error) {
		return &snowball.DescribeAddressesOutput{Addresses: []sbtypes.Address{sbAddress("1")}}, nil
	}
	for op, fn := range override {
		h[op] = fn
	}
	return h
}

func TestScanSnowballAll_StoresAddressesAndClusters(t *testing.T) {
	st := newTestStore(t)
	total, _, err := scanSnowballAll(context.Background(), sbClient(sbTopLevelHandlers(nil)), newTestAccount(testAccountID), testRegion, st, testScanID)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if total != 3 {
		t.Errorf("total = %d, want 3 (1 address + 2 clusters)", total)
	}
	if _, ok := rowsByNativeID(t, st, TypeSnowballAddress)[sbAddressNative("1")]; !ok {
		t.Errorf("address %s missing", sbAddressNative("1"))
	}
	if n := len(rowsByNativeID(t, st, TypeSnowballCluster)); n != 2 {
		t.Errorf("cluster rows = %d, want 2", n)
	}
}

// sbCountingAddresses answers DescribeAddresses with address 1 and counts
// the calls.
func sbCountingAddresses(calls *atomic.Int32) map[string]func(any) (any, error) {
	return map[string]func(any) (any, error){
		"DescribeAddresses": func(any) (any, error) {
			calls.Add(1)
			return &snowball.DescribeAddressesOutput{Addresses: []sbtypes.Address{sbAddress("1")}}, nil
		},
	}
}

// A US region whose listing is denied leaves the addresses to the next US
// region, which stores them under the first configured US region.
func TestScanSnowballAddressesOnce_DeniedRegionLeavesTurnToNext(t *testing.T) {
	st := newTestStore(t)
	warned := countOpWarnings(st, snowballDescribeAddressesOp)
	acct := newTestAccount(testAccountID)
	acct.Regions = []string{"us-west-2", "eu-west-1", "us-east-1"}
	denied := sbClient(map[string]func(any) (any, error){})
	if total, _, err := scanSnowballAddressesOnce(context.Background(), denied, acct, "us-east-1", st, testScanID); err != nil || total != 0 {
		t.Fatalf("us-east-1: total, err = %d, %v; want 0, nil", total, err)
	}
	var calls atomic.Int32
	total, _, err := scanSnowballAddressesOnce(context.Background(), sbClient(sbCountingAddresses(&calls)), acct, "us-west-2", st, testScanID)
	if err != nil || total != 1 || calls.Load() != 1 {
		t.Errorf("us-west-2: total, calls, err = %d, %d, %v; want 1, 1, nil", total, calls.Load(), err)
	}
	if got := rowsByNativeID(t, st, TypeSnowballAddress)[sbAddressNative("1")]; sv(got.Region) != "us-east-1" {
		t.Errorf("address region = %q, want us-east-1 (us-west-2 made the call)", sv(got.Region))
	}
	if *warned != 1 {
		t.Errorf("warnings = %d, want 1 (the us-east-1 denial)", *warned)
	}
}

func TestSnowballUSAddressRegion_FirstScannedUSRegion(t *testing.T) {
	for _, tc := range []struct {
		scanned []string
		region  string
		want    string
	}{
		{[]string{"us-west-2", "us-east-1"}, "us-west-2", "us-east-1"},
		{[]string{"us-west-2", "eu-west-1"}, "us-west-2", "us-west-2"},
		{[]string{"us-gov-east-1", "us-west-1"}, "us-west-1", "us-west-1"},
		{[]string{"ap-south-1", "us-east-2"}, "us-east-2", "us-east-2"},
	} {
		if got := snowballUSAddressRegion(tc.scanned, tc.region); got != tc.want {
			t.Errorf("snowballUSAddressRegion(%v, %q) = %q; want %q", tc.scanned, tc.region, got, tc.want)
		}
	}
}

// A failed write of an address page leaves the listing incomplete, so the
// next US region lists again.
func TestScanSnowballAddresses_StoreWriteFailureIsIncomplete(t *testing.T) {
	// Not newTestStore: its cleanup queries the DB, which a closed store can't serve.
	st, err := store.Open(filepath.Join(t.TempDir(), "closing.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	insertTestScan(t, st, testScanID, "running")
	if err := st.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	var calls atomic.Int32
	complete, _, _, err := scanSnowballAddresses(context.Background(), sbClient(sbCountingAddresses(&calls)), newTestAccount(testAccountID), testRegion, testRegion, st, testScanID)
	if !errors.Is(err, store.ErrStoreWrite) || complete {
		t.Errorf("complete, err = %v, %v; want false, store.ErrStoreWrite", complete, err)
	}
}

// sbClientCtx is sbClient for handlers that need the call's context.
func sbClientCtx(handlers map[string]func(ctx context.Context, in any) (any, error)) *snowball.Client {
	return snowball.NewFromConfig(opStubConfigCtx(handlers, sbDenied))
}

// A US region whose context ends while another US region's DescribeAddresses
// holds the gate still stores its clusters and reports no error: it neither
// waits on the gate before its clusters nor blames itself for the wait. The
// holder's call fails after the waiter's deadline, so a waiter that ignored
// its context would then call DescribeAddresses itself and fail.
func TestScanSnowballAll_WaitingUSRegionKeepsClustersWithoutDeadline(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	acct.Regions = []string{"us-east-1", "us-west-2"}
	entered := make(chan struct{})
	var addressCalls atomic.Int32
	handlers := map[string]func(ctx context.Context, in any) (any, error){}
	for op, h := range sbClusterHandlers(&parentCalls{}, nil) {
		handlers[op] = func(ctx context.Context, in any) (any, error) {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			return h(in)
		}
	}
	handlers["DescribeAddresses"] = func(ctx context.Context, _ any) (any, error) {
		if addressCalls.Add(1) > 1 {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			return &snowball.DescribeAddressesOutput{}, nil
		}
		close(entered)
		select {
		case <-time.After(400 * time.Millisecond):
		case <-ctx.Done():
		}
		return nil, sbHardErr
	}
	client := sbClientCtx(handlers)
	holderDone := make(chan error, 1)
	go func() {
		_, _, err := scanSnowballAddressesOnce(context.Background(), client, acct, "us-east-1", st, testScanID)
		holderDone <- err
	}()
	<-entered
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	total, _, err := scanSnowballAll(ctx, client, acct, "us-west-2", st, testScanID)
	if err != nil {
		t.Errorf("waiter err = %v, want nil", err)
	}
	if _, ok := rowsByNativeID(t, st, TypeSnowballCluster)[sbClusterNative("us-west-2", "1")]; total != 2 || !ok {
		t.Errorf("waiter total = %d, cluster 1 stored = %v; want 2, true", total, ok)
	}
	if herr := <-holderDone; !errors.Is(herr, sbHardErr) {
		t.Errorf("holder err = %v, want its own DescribeAddresses error", herr)
	}
	if n := addressCalls.Load(); n != 1 {
		t.Errorf("DescribeAddresses calls = %d, want 1 (the holder's)", n)
	}
}

// Once one US region has stored the addresses, later US regions do not call
// DescribeAddresses.
func TestScanSnowballAddressesOnce_StoredOnceAcrossUSRegions(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	var calls atomic.Int32
	client := sbClient(sbCountingAddresses(&calls))
	for _, region := range []string{"us-east-1", "us-west-2", "us-east-2"} {
		if _, _, err := scanSnowballAddressesOnce(context.Background(), client, acct, region, st, testScanID); err != nil {
			t.Fatalf("%s: err = %v", region, err)
		}
	}
	if calls.Load() != 1 {
		t.Errorf("DescribeAddresses calls = %d, want 1", calls.Load())
	}
	if got := rowsByNativeID(t, st, TypeSnowballAddress)[sbAddressNative("1")]; sv(got.Region) != "us-east-1" {
		t.Errorf("address region = %q, want us-east-1", sv(got.Region))
	}
}

// A non-US region (GovCloud included) lists its addresses even after a US
// region has stored the US ones.
func TestScanSnowballAddressesOnce_NonUSRegionAlwaysLists(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	var calls atomic.Int32
	client := sbClient(sbCountingAddresses(&calls))
	for _, region := range []string{"us-east-1", "eu-west-1", "us-gov-west-1"} {
		if _, _, err := scanSnowballAddressesOnce(context.Background(), client, acct, region, st, testScanID); err != nil {
			t.Fatalf("%s: err = %v", region, err)
		}
	}
	if calls.Load() != 3 {
		t.Errorf("DescribeAddresses calls = %d, want 3 (one per region)", calls.Load())
	}
}

// US regions scanned concurrently call DescribeAddresses once: the first call
// waits briefly for a second one, which the account's lock must keep out.
func TestScanSnowballAddressesOnce_ConcurrentUSRegionsCallOnce(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	var calls atomic.Int32
	second := make(chan struct{}, 8)
	client := sbClient(map[string]func(any) (any, error){
		"DescribeAddresses": func(any) (any, error) {
			if calls.Add(1) == 1 {
				select {
				case <-second:
				case <-time.After(200 * time.Millisecond):
				}
			} else {
				second <- struct{}{}
			}
			return &snowball.DescribeAddressesOutput{Addresses: []sbtypes.Address{sbAddress("1")}}, nil
		},
	})
	var wg sync.WaitGroup
	for _, region := range []string{"us-east-1", "us-east-2", "us-west-1", "us-west-2"} {
		wg.Go(func() {
			if _, _, err := scanSnowballAddressesOnce(context.Background(), client, acct, region, st, testScanID); err != nil {
				t.Errorf("%s: err = %v", region, err)
			}
		})
	}
	wg.Wait()
	if calls.Load() != 1 {
		t.Errorf("DescribeAddresses calls = %d, want 1", calls.Load())
	}
}

// A hard error in either phase does not stop the other phase from storing
// its rows, and is returned.
func TestScanSnowballAll_PhaseErrorKeepsOtherPhase(t *testing.T) {
	for _, tc := range []struct {
		name     string
		failOp   string
		keptType string
		keptID   string
	}{
		{"addresses fail", "DescribeAddresses", TypeSnowballCluster, sbClusterNative(testRegion, "1")},
		{"clusters fail", "ListClusters", TypeSnowballAddress, sbAddressNative("1")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			handlers := sbTopLevelHandlers(map[string]func(any) (any, error){
				tc.failOp: func(any) (any, error) { return nil, sbHardErr },
			})
			_, _, err := scanSnowballAll(context.Background(), sbClient(handlers), newTestAccount(testAccountID), testRegion, st, testScanID)
			if !errors.Is(err, sbHardErr) {
				t.Errorf("err = %v, want the %s error", err, tc.failOp)
			}
			if _, ok := rowsByNativeID(t, st, tc.keptType)[tc.keptID]; !ok {
				t.Errorf("%s %s missing", tc.keptType, tc.keptID)
			}
		})
	}
}

// A throttle in one phase is demoted to one warning so that the other
// phase's hard error still classifies the service as failed.
func TestScanSnowballAll_TransientDoesNotMaskHardError(t *testing.T) {
	for _, tc := range []struct {
		name, throttledOp, warnOp, hardOp string
	}{
		{"addresses throttled", "DescribeAddresses", snowballDescribeAddressesOp, "ListClusters"},
		{"clusters throttled", "ListClusters", snowballListClustersOp, "DescribeAddresses"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			warned := countOpWarnings(st, tc.warnOp)
			handlers := sbTopLevelHandlers(map[string]func(any) (any, error){
				tc.throttledOp: func(any) (any, error) { return nil, sbThrottled },
				tc.hardOp:      func(any) (any, error) { return nil, sbHardErr },
			})
			_, _, err := scanSnowballAll(context.Background(), sbClient(handlers), newTestAccount(testAccountID), testRegion, st, testScanID)
			if got := classifyServiceError(err); got != outcomeError {
				t.Errorf("classifyServiceError(%v) = %v, want outcomeError", err, got)
			}
			if !errors.Is(err, sbHardErr) {
				t.Errorf("err = %v, want the %s error", err, tc.hardOp)
			}
			if *warned != 1 {
				t.Errorf("%s transient warnings = %d, want 1", tc.warnOp, *warned)
			}
		})
	}
}
