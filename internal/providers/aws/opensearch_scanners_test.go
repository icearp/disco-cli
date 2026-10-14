package aws

import (
	"context"
	"errors"
	"fmt"
	"net"
	"path/filepath"
	"slices"
	"sync"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/opensearch"
	opensearchtypes "github.com/aws/aws-sdk-go-v2/service/opensearch/types"
	"github.com/aws/smithy-go"
	"github.com/icearp/disco-cli/store"
)

// osNewPhaseOps are the ops of the phases that run after domains,
// applications and data sources.
var osNewPhaseOps = []string{
	"ListDirectQueryDataSources", "ListDataSourceAttachments", "ListVpcEndpoints",
	"DescribePackages", "DescribeReservedInstances", "DescribeOutboundConnections", "DescribeInboundConnections",
}

// osTopLevelHandlers answers every op scanOpenSearch calls with one element.
func osTopLevelHandlers() map[string]func(any) (any, error) {
	local := osDomainInfo(testAccountID, testRegion, "logs")
	return map[string]func(any) (any, error){
		"ListDomainNames": func(any) (any, error) {
			return &opensearch.ListDomainNamesOutput{DomainNames: []opensearchtypes.DomainInfo{{DomainName: ptrStr("logs")}}}, nil
		},
		"DescribeDomain": func(any) (any, error) {
			return &opensearch.DescribeDomainOutput{DomainStatus: &opensearchtypes.DomainStatus{ARN: ptrStr(osDomainARN), DomainName: ptrStr("logs")}}, nil
		},
		"ListApplications": func(any) (any, error) {
			return &opensearch.ListApplicationsOutput{ApplicationSummaries: []opensearchtypes.ApplicationSummary{{Id: ptrStr("a1"), Arn: ptrStr(osApp1ARN)}}}, nil
		},
		"ListDataSources": func(any) (any, error) { return &opensearch.ListDataSourcesOutput{}, nil },
		"ListDirectQueryDataSources": func(any) (any, error) {
			return &opensearch.ListDirectQueryDataSourcesOutput{DirectQueryDataSources: []opensearchtypes.DirectQueryDataSource{{DataSourceArn: ptrStr(osDQ1ARN)}}}, nil
		},
		"ListDataSourceAttachments": func(any) (any, error) {
			return &opensearch.ListDataSourceAttachmentsOutput{Attachments: []opensearchtypes.DataSourceAttachmentSummary{{AttachmentId: ptrStr("att1")}}}, nil
		},
		"ListVpcEndpoints": func(any) (any, error) {
			return &opensearch.ListVpcEndpointsOutput{VpcEndpointSummaryList: []opensearchtypes.VpcEndpointSummary{{VpcEndpointId: ptrStr("aos-ep1")}}}, nil
		},
		"DescribeVpcEndpoints": func(any) (any, error) {
			return &opensearch.DescribeVpcEndpointsOutput{VpcEndpoints: []opensearchtypes.VpcEndpoint{{VpcEndpointId: ptrStr("aos-ep1")}}}, nil
		},
		"DescribePackages": func(any) (any, error) {
			return &opensearch.DescribePackagesOutput{PackageDetailsList: []opensearchtypes.PackageDetails{{PackageID: ptrStr("F1")}}}, nil
		},
		"DescribeReservedInstances": func(any) (any, error) {
			return &opensearch.DescribeReservedInstancesOutput{ReservedInstances: []opensearchtypes.ReservedInstance{{ReservedInstanceId: ptrStr("ri-1")}}}, nil
		},
		"DescribeOutboundConnections": func(any) (any, error) {
			return &opensearch.DescribeOutboundConnectionsOutput{Connections: []opensearchtypes.OutboundConnection{{ConnectionId: ptrStr("c1"), LocalDomainInfo: local}}}, nil
		},
		"DescribeInboundConnections": func(any) (any, error) {
			return &opensearch.DescribeInboundConnectionsOutput{Connections: []opensearchtypes.InboundConnection{{ConnectionId: ptrStr("c2"), RemoteDomainInfo: local}}}, nil
		},
	}
}

func runScanOpenSearch(t *testing.T, st *store.Store, handlers map[string]func(any) (any, error)) (int, error) {
	t.Helper()
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(handlers, osDenied)
	total, _, err := scanOpenSearch(context.Background(), acct, testRegion, st, testScanID)
	return total, err
}

func TestScanOpenSearch_RunsEveryPhase(t *testing.T) {
	st := newTestStore(t)
	total, err := runScanOpenSearch(t, st, osTopLevelHandlers())
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	for typ, nativeID := range map[string]string{
		TypeOpenSearchDomain:                osDomainARN,
		TypeOpenSearchApplication:           osApp1ARN,
		TypeOpenSearchDirectQueryDataSource: osDQ1ARN,
		TypeOpenSearchDataSourceAttachment:  osApp1ARN + "/data-source-attachment/att1",
		TypeOpenSearchVpcEndpoint:           osVpcEndpointID("aos-ep1"),
		TypeOpenSearchPackage:               osPackageID("F1"),
		TypeOpenSearchReservedInstance:      osReservedID("ri-1"),
		TypeOpenSearchOutboundConnection:    osDomainARN + "/outbound-connection/c1",
		TypeOpenSearchInboundConnection:     osDomainARN + "/inbound-connection/c2",
	} {
		if _, ok := rowsByNativeID(t, st, typ)[nativeID]; !ok {
			t.Errorf("%s row %s missing", typ, nativeID)
		}
	}
	if total != 9 {
		t.Errorf("total = %d; want 9", total)
	}
	assertContainsNative(t, st, osApp1ARN, osApp1ARN+"/data-source-attachment/att1")
}

// Every new phase runs after the existing ones and its error is joined: the
// domain and application rows survive every new op failing, and each failure
// is reported.
func TestScanOpenSearch_NewPhaseErrorsJoinedAfterExistingPhases(t *testing.T) {
	st := newTestStore(t)
	handlers := osTopLevelHandlers()
	var (
		mu     sync.Mutex
		failed []string
	)
	for _, op := range osNewPhaseOps {
		handlers[op] = func(any) (any, error) {
			mu.Lock()
			failed = append(failed, op)
			mu.Unlock()
			return nil, osBadInput
		}
	}
	total, err := runScanOpenSearch(t, st, handlers)
	joined, ok := err.(interface{ Unwrap() []error })
	if !ok || len(joined.Unwrap()) != len(osNewPhaseOps) || !isAPIErrorCode(err, "ValidationException") {
		t.Fatalf("err = %v; want %d joined ValidationException errors", err, len(osNewPhaseOps))
	}
	slices.Sort(failed)
	if want := slices.Sorted(slices.Values(osNewPhaseOps)); !slices.Equal(failed, want) {
		t.Errorf("failed ops = %v; want each of %v once", failed, want)
	}
	if total != 2 || len(rowsByNativeID(t, st, TypeOpenSearchDomain)) != 1 || len(rowsByNativeID(t, st, TypeOpenSearchApplication)) != 1 {
		t.Errorf("total = %d; want the domain and application rows kept", total)
	}
}

// A failed domain phase does not stop the phases after it.
func TestScanOpenSearch_DomainErrorDoesNotStopLaterPhases(t *testing.T) {
	st := newTestStore(t)
	handlers := osTopLevelHandlers()
	handlers["ListDomainNames"] = func(any) (any, error) { return nil, osBadInput }
	total, err := runScanOpenSearch(t, st, handlers)
	if !isAPIErrorCode(err, "ValidationException") {
		t.Errorf("err = %v; want the domain phase's ValidationException", err)
	}
	if total != 8 || len(rowsByNativeID(t, st, TypeOpenSearchInboundConnection)) != 1 {
		t.Errorf("total = %d; want 8 rows from the phases after domains", total)
	}
}

// A later phase's transient network error warns instead of joining, so the
// service keeps its counts and its hard errors are still classified as errors.
func TestScanOpenSearch_LaterTransientErrorWarnsAndKeepsHardErrors(t *testing.T) {
	st := newTestStore(t)
	warns := countOpWarnings(st, "opensearch:ListApplications")
	handlers := osTopLevelHandlers()
	handlers["ListApplications"] = func(any) (any, error) {
		return nil, &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connection refused")}
	}
	handlers["DescribePackages"] = func(any) (any, error) { return nil, osBadInput }
	total, err := runScanOpenSearch(t, st, handlers)
	if got := classifyServiceError(err); got != outcomeError || !isAPIErrorCode(err, "ValidationException") || isTransientNetworkError(err) {
		t.Errorf("err = %v classified %v; want the DescribePackages ValidationException alone, a hard error", err, got)
	}
	if *warns != 1 {
		t.Errorf("ListApplications warnings = %d; want 1 transient warning", *warns)
	}
	if total != 6 || len(rowsByNativeID(t, st, TypeOpenSearchDomain)) != 1 {
		t.Errorf("total = %d; want 6 (the domain and 5 later rows; no app, attachment or package)", total)
	}
}

// A later phase whose error joins a transient list error with a failed write
// of the rows listed before it stays a hard store-write error, not a warning.
// Only the direct-query phase lists rows, so its write is the only one made.
func TestScanOpenSearch_StoreWriteBesideTransientStaysHardError(t *testing.T) {
	// Not newTestStore: its cleanup queries the DB, which a closed store can't serve.
	st, err := store.Open(filepath.Join(t.TempDir(), "closed.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if err := st.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	var warns int
	st.OnWarn = func(store.ScanWarning) { warns++ }
	empty := func(out any) func(any) (any, error) { return func(any) (any, error) { return out, nil } }
	handlers := map[string]func(any) (any, error){
		"ListDomainNames":             empty(&opensearch.ListDomainNamesOutput{}),
		"ListApplications":            empty(&opensearch.ListApplicationsOutput{}),
		"ListVpcEndpoints":            empty(&opensearch.ListVpcEndpointsOutput{}),
		"DescribePackages":            empty(&opensearch.DescribePackagesOutput{}),
		"DescribeReservedInstances":   empty(&opensearch.DescribeReservedInstancesOutput{}),
		"DescribeOutboundConnections": empty(&opensearch.DescribeOutboundConnectionsOutput{}),
		"DescribeInboundConnections":  empty(&opensearch.DescribeInboundConnectionsOutput{}),
		"ListDirectQueryDataSources": func(in any) (any, error) {
			if in.(*opensearch.ListDirectQueryDataSourcesInput).NextToken == nil {
				return &opensearch.ListDirectQueryDataSourcesOutput{NextToken: ptrStr("t2"), DirectQueryDataSources: []opensearchtypes.DirectQueryDataSource{{DataSourceArn: ptrStr(osDQ1ARN)}}}, nil
			}
			return nil, &net.OpError{Op: "read", Net: "tcp", Err: errors.New("connection reset")}
		},
	}
	_, err = runScanOpenSearch(t, st, handlers)
	if !errors.Is(err, store.ErrStoreWrite) || classifyServiceError(err) != outcomeStoreWrite {
		t.Errorf("err = %v classified %v; want store.ErrStoreWrite, outcomeStoreWrite", err, classifyServiceError(err))
	}
	if warns != 0 {
		t.Errorf("warnings = %d; want none (the joined error is not downgraded to a transient warning)", warns)
	}
}

// A domain phase that cannot reach the endpoint returns that error alone and
// no later phase calls it.
func TestScanOpenSearch_DomainEndpointUnreachableStopsTheScan(t *testing.T) {
	for _, tc := range []struct {
		name    string
		err     error
		outcome serviceOutcome
	}{
		{name: "nxdomain", err: &net.DNSError{Err: "no such host", Name: "es.us-east-1.amazonaws.com", IsNotFound: true}, outcome: outcomeUnavailable},
		{name: "dial", err: &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connection refused")}, outcome: outcomeTransient},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			var calls parentCalls
			handlers := map[string]func(any) (any, error){}
			for op, h := range osTopLevelHandlers() {
				handlers[op] = func(in any) (any, error) {
					calls.add(op)
					return h(in)
				}
			}
			handlers["ListDomainNames"] = func(any) (any, error) {
				calls.add("ListDomainNames")
				return nil, tc.err
			}
			total, err := runScanOpenSearch(t, st, handlers)
			if got := classifyServiceError(err); got != tc.outcome {
				t.Errorf("err = %v classified %v; want %v", err, got, tc.outcome)
			}
			if got := calls.distinct(); total != 0 || !slices.Equal(got, []string{"ListDomainNames"}) {
				t.Errorf("total = %d, ops called = %v; want 0 and only ListDomainNames", total, got)
			}
		})
	}
}

// An API-coded throttle from the domain phase is that phase's error: the
// later phases still run, the data-source phase over the listed names.
func TestScanOpenSearch_DomainThrottleJoinedAndLaterPhasesRun(t *testing.T) {
	st := newTestStore(t)
	var dsDomains parentCalls
	handlers := osTopLevelHandlers()
	handlers["DescribeDomain"] = func(any) (any, error) {
		return nil, &smithy.GenericAPIError{Code: "ThrottlingException", Message: "Rate exceeded"}
	}
	handlers["ListDataSources"] = func(in any) (any, error) {
		dsDomains.add(sv(in.(*opensearch.ListDataSourcesInput).DomainName))
		return &opensearch.ListDataSourcesOutput{}, nil
	}
	total, err := runScanOpenSearch(t, st, handlers)
	if !isAPIErrorCode(err, "ThrottlingException") {
		t.Errorf("err = %v; want the DescribeDomain ThrottlingException", err)
	}
	if got := dsDomains.distinct(); !slices.Equal(got, []string{"logs"}) {
		t.Errorf("ListDataSources domains = %v; want [logs]", got)
	}
	if total != 8 || len(rowsByNativeID(t, st, TypeOpenSearchDomain)) != 0 {
		t.Errorf("total = %d; want 8 rows from the later phases and no domain row", total)
	}
}

// osDomainStub fails DescribeDomain for d00 and holds every other describe
// until the fan-out context is cancelled, so the launch loop is still waiting
// for a slot when the failure cancels it.
type osDomainStub struct {
	opensearchAPI
	names []opensearchtypes.DomainInfo
}

func (s osDomainStub) ListDomainNames(context.Context, *opensearch.ListDomainNamesInput, ...func(*opensearch.Options)) (*opensearch.ListDomainNamesOutput, error) {
	return &opensearch.ListDomainNamesOutput{DomainNames: s.names}, nil
}

func (s osDomainStub) DescribeDomain(ctx context.Context, in *opensearch.DescribeDomainInput, _ ...func(*opensearch.Options)) (*opensearch.DescribeDomainOutput, error) {
	if sv(in.DomainName) == "d00" {
		return nil, osBadInput
	}
	<-ctx.Done()
	return nil, ctx.Err()
}

// More domains than fan-out slots, one failing: the returned error is that
// describe's, not the Canceled the blocked launch loop sees.
func TestScanOpenSearchDomains_FirstDescribeErrorWins(t *testing.T) {
	st := newTestStore(t)
	stub := osDomainStub{}
	for i := range fanoutMed + 5 {
		stub.names = append(stub.names, opensearchtypes.DomainInfo{DomainName: ptrStr(fmt.Sprintf("d%02d", i))})
	}
	names, total, _, err := scanOpenSearchDomains(context.Background(), stub, newTestAccount(testAccountID), testRegion, st, testScanID)
	if !isAPIErrorCode(err, "ValidationException") || errors.Is(err, context.Canceled) {
		t.Errorf("err = %v; want the d00 ValidationException", err)
	}
	if total != 0 || len(names) != fanoutMed+5 {
		t.Errorf("total = %d, names = %d; want 0 rows and all %d listed names", total, len(names), fanoutMed+5)
	}
}

// A caller cancellation that stops the launch loop is returned, not read as a
// complete listing.
func TestScanOpenSearchDomains_CallerCancelReturned(t *testing.T) {
	st := newTestStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	stub := osDomainStub{names: []opensearchtypes.DomainInfo{{DomainName: ptrStr("d01")}}}
	_, total, _, err := scanOpenSearchDomains(ctx, stub, newTestAccount(testAccountID), testRegion, st, testScanID)
	if !errors.Is(err, context.Canceled) || total != 0 {
		t.Errorf("total = %d, err = %v; want 0, context.Canceled", total, err)
	}
}
