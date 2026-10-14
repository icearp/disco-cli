package aws

import (
	"context"
	"maps"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/opensearch"
	opensearchtypes "github.com/aws/aws-sdk-go-v2/service/opensearch/types"
	"github.com/aws/smithy-go"
	"github.com/icearp/disco-cli/store"
)

var (
	osDenied   = &smithy.GenericAPIError{Code: "AccessDeniedException", Message: "User: arn:aws:iam::123456789012:user/t is not authorized to perform: es:List"}
	osBadInput = &smithy.GenericAPIError{Code: "ValidationException", Message: "bad"}
	osUnknown  = &smithy.GenericAPIError{Code: "UnknownOperationException", Message: "unknown operation"}
	osGone     = &smithy.GenericAPIError{Code: "ResourceNotFoundException", Message: "not found"}
	osCreated  = time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)
)

const (
	osDomainARN = "arn:aws:es:us-east-1:123456789012:domain/logs"
	osDQ1ARN    = "arn:aws:opensearch:us-east-1:123456789012:datasource/cwl"
	osDQ2ARN    = "arn:aws:opensearch:us-east-1:123456789012:datasource/prom"
)

func osClient(handlers map[string]func(in any) (any, error)) *opensearch.Client {
	return opensearch.NewFromConfig(opStubConfig(handlers, osDenied))
}

func osScanAccount(t *testing.T, scan func(context.Context, opensearchAPI, *account, string, *store.Store, string) (int, int, error),
	handlers map[string]func(in any) (any, error),
) (*store.Store, int, error) {
	t.Helper()
	st := newTestStore(t)
	total, _, err := scan(context.Background(), osClient(handlers), newTestAccount(testAccountID), testRegion, st, testScanID)
	return st, total, err
}

// osDQHandler serves two pages (cwl; then prom and an element with no ARN).
// fail maps "p1" or "p2" to the error that page returns instead.
func osDQHandler(fail map[string]error) func(in any) (any, error) {
	return func(in any) (any, error) {
		if in.(*opensearch.ListDirectQueryDataSourcesInput).NextToken == nil {
			if err := fail["p1"]; err != nil {
				return nil, err
			}
			return &opensearch.ListDirectQueryDataSourcesOutput{NextToken: ptrStr("t2"), DirectQueryDataSources: []opensearchtypes.DirectQueryDataSource{{
				DataSourceArn: ptrStr(osDQ1ARN), DataSourceName: ptrStr("cwl"),
				DataSourceType: &opensearchtypes.DirectQueryDataSourceTypeMemberCloudWatchLog{Value: opensearchtypes.CloudWatchDirectQueryDataSource{RoleArn: ptrStr("arn:aws:iam::123456789012:role/dq")}},
				TagList:        []opensearchtypes.Tag{{Key: ptrStr("team"), Value: ptrStr("obs")}},
			}}}, nil
		}
		if err := fail["p2"]; err != nil {
			return nil, err
		}
		return &opensearch.ListDirectQueryDataSourcesOutput{DirectQueryDataSources: []opensearchtypes.DirectQueryDataSource{
			{DataSourceArn: ptrStr(osDQ2ARN), DataSourceName: ptrStr("prom")},
			{DataSourceName: ptrStr("no-arn")},
		}}, nil
	}
}

func TestScanOpenSearchDirectQueryDataSources_StoresEveryPage(t *testing.T) {
	st, total, err := osScanAccount(t, scanOpenSearchDirectQueryDataSources, map[string]func(any) (any, error){"ListDirectQueryDataSources": osDQHandler(nil)})
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	rows := rowsByNativeID(t, st, TypeOpenSearchDirectQueryDataSource)
	if ids := slices.Sorted(maps.Keys(rows)); total != 2 || !slices.Equal(ids, []string{osDQ1ARN, osDQ2ARN}) {
		t.Fatalf("total = %d, rows = %v; want 2, [%s %s]", total, ids, osDQ1ARN, osDQ2ARN)
	}
	cwl := rows[osDQ1ARN]
	if sv(cwl.Name) != "cwl" || sv(cwl.Region) != testRegion || !strings.Contains(sv(cwl.TagsJSON), `"team":"obs"`) {
		t.Errorf("cwl name/region/tags = %q/%q/%s", sv(cwl.Name), sv(cwl.Region), sv(cwl.TagsJSON))
	}
	if !strings.Contains(cwl.AttributesJSON, `"DataSourceType":{"CloudWatchLog":{"RoleArn":"arn:aws:iam::123456789012:role/dq"}}`) {
		t.Errorf("cwl attrs = %s; want the union member labelled CloudWatchLog", cwl.AttributesJSON)
	}
}

func TestScanOpenSearchDirectQueryDataSources_Empty(t *testing.T) {
	st, total, err := osScanAccount(t, scanOpenSearchDirectQueryDataSources, map[string]func(any) (any, error){
		"ListDirectQueryDataSources": func(any) (any, error) { return &opensearch.ListDirectQueryDataSourcesOutput{}, nil },
	})
	if err != nil || total != 0 || len(rowsByNativeID(t, st, TypeOpenSearchDirectQueryDataSource)) != 0 {
		t.Errorf("total = %d, err = %v; want 0 rows, nil", total, err)
	}
}

func TestScanOpenSearchDirectQueryDataSources_StopsOnRepeatedToken(t *testing.T) {
	calls := 0
	_, total, err := osScanAccount(t, scanOpenSearchDirectQueryDataSources, map[string]func(any) (any, error){
		"ListDirectQueryDataSources": func(any) (any, error) {
			calls++
			arn := osDQ1ARN
			if calls > 1 {
				arn = osDQ2ARN
			}
			return &opensearch.ListDirectQueryDataSourcesOutput{NextToken: ptrStr("same"), DirectQueryDataSources: []opensearchtypes.DirectQueryDataSource{{DataSourceArn: &arn}}}, nil
		},
	})
	if err != nil || calls != 2 || total != 2 {
		t.Errorf("calls = %d, total = %d, err = %v; want 2 calls, 2 rows, nil", calls, total, err)
	}
}

// A first-page gap is silent and a denial warns; a later failure keeps the
// first page's row. Any other error fails the phase.
func TestScanOpenSearchDirectQueryDataSources_Errors(t *testing.T) {
	for _, tc := range []struct {
		name      string
		fail      map[string]error
		wantCode  string
		wantTotal int
		wantWarns int
	}{
		{name: "op not deployed", fail: map[string]error{"p1": osUnknown}},
		{name: "denied", fail: map[string]error{"p1": osDenied}, wantWarns: 1},
		{name: "other on first page", fail: map[string]error{"p1": osBadInput}, wantCode: "ValidationException"},
		{name: "not deployed on page two", fail: map[string]error{"p2": osUnknown}, wantCode: "UnknownOperationException", wantTotal: 1},
		{name: "denied on page two", fail: map[string]error{"p2": osDenied}, wantTotal: 1, wantWarns: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := countOpWarnings(st, "opensearch:ListDirectQueryDataSources")
			total, _, err := scanOpenSearchDirectQueryDataSources(context.Background(), osClient(map[string]func(any) (any, error){"ListDirectQueryDataSources": osDQHandler(tc.fail)}), newTestAccount(testAccountID), testRegion, st, testScanID)
			if tc.wantCode == "" && err != nil || tc.wantCode != "" && !isAPIErrorCode(err, tc.wantCode) {
				t.Errorf("err = %v; want code %q", err, tc.wantCode)
			}
			if got := len(rowsByNativeID(t, st, TypeOpenSearchDirectQueryDataSource)); total != tc.wantTotal || got != tc.wantTotal {
				t.Errorf("total = %d, stored = %d; want %d", total, got, tc.wantTotal)
			}
			if *warns != tc.wantWarns {
				t.Errorf("warnings = %d; want %d", *warns, tc.wantWarns)
			}
		})
	}
}

// osVpcHandlers list two pages (ep1, ep2; then ep3) and describe each page's
// ids: ep2 comes back ENDPOINT_NOT_FOUND, and the second call also reports two
// SERVER_ERROR entries. describeFail maps a call number (1 or 2) to the error it returns.
func osVpcHandlers(described *[][]string, mu *sync.Mutex, describeFail map[int]error) map[string]func(any) (any, error) {
	return map[string]func(any) (any, error){
		"ListVpcEndpoints": func(in any) (any, error) {
			if in.(*opensearch.ListVpcEndpointsInput).NextToken == nil {
				return &opensearch.ListVpcEndpointsOutput{NextToken: ptrStr("t2"), VpcEndpointSummaryList: []opensearchtypes.VpcEndpointSummary{
					{VpcEndpointId: ptrStr("aos-ep1")}, {VpcEndpointId: ptrStr("aos-ep2")}, {},
				}}, nil
			}
			return &opensearch.ListVpcEndpointsOutput{NextToken: ptrStr(""), VpcEndpointSummaryList: []opensearchtypes.VpcEndpointSummary{{VpcEndpointId: ptrStr("aos-ep3")}}}, nil
		},
		"DescribeVpcEndpoints": func(in any) (any, error) {
			mu.Lock()
			*described = append(*described, in.(*opensearch.DescribeVpcEndpointsInput).VpcEndpointIds)
			n := len(*described)
			mu.Unlock()
			if err := describeFail[n]; err != nil {
				return nil, err
			}
			if n == 1 {
				return &opensearch.DescribeVpcEndpointsOutput{
					VpcEndpoints: []opensearchtypes.VpcEndpoint{{
						VpcEndpointId: ptrStr("aos-ep1"), DomainArn: ptrStr(osDomainARN), Status: opensearchtypes.VpcEndpointStatusActive,
						VpcOptions: &opensearchtypes.VPCDerivedInfo{VPCId: ptrStr("vpc-1"), SubnetIds: []string{"subnet-1"}},
					}},
					VpcEndpointErrors: []opensearchtypes.VpcEndpointError{{VpcEndpointId: ptrStr("aos-ep2"), ErrorCode: opensearchtypes.VpcEndpointErrorCodeEndpointNotFound}},
				}, nil
			}
			return &opensearch.DescribeVpcEndpointsOutput{
				VpcEndpoints: []opensearchtypes.VpcEndpoint{{VpcEndpointId: ptrStr("aos-ep3")}},
				VpcEndpointErrors: []opensearchtypes.VpcEndpointError{
					{VpcEndpointId: ptrStr("aos-ep4"), ErrorCode: opensearchtypes.VpcEndpointErrorCodeServerError},
					{VpcEndpointId: ptrStr("aos-ep5"), ErrorCode: opensearchtypes.VpcEndpointErrorCodeServerError},
				},
			}, nil
		},
	}
}

func osVpcEndpointID(id string) string { return "arn:aws:es:us-east-1:123456789012:vpc-endpoint/" + id }

func TestScanOpenSearchVpcEndpoints_DescribesEachListPage(t *testing.T) {
	st := newTestStore(t)
	var warns []store.ScanWarning
	st.OnWarn = func(w store.ScanWarning) { warns = append(warns, w) }
	var (
		described [][]string
		mu        sync.Mutex
	)
	total, _, err := scanOpenSearchVpcEndpoints(context.Background(), osClient(osVpcHandlers(&described, &mu, nil)), newTestAccount(testAccountID), testRegion, st, testScanID)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if len(described) != 2 || !slices.Equal(described[0], []string{"aos-ep1", "aos-ep2"}) || !slices.Equal(described[1], []string{"aos-ep3"}) {
		t.Errorf("described = %v; want [[aos-ep1 aos-ep2] [aos-ep3]]", described)
	}
	rows := rowsByNativeID(t, st, TypeOpenSearchVpcEndpoint)
	want := []string{osVpcEndpointID("aos-ep1"), osVpcEndpointID("aos-ep3")}
	if ids := slices.Sorted(maps.Keys(rows)); total != 2 || !slices.Equal(ids, want) {
		t.Fatalf("total = %d, rows = %v; want 2, %v", total, ids, want)
	}
	ep1 := rows[want[0]]
	if sv(ep1.Name) != "aos-ep1" || sv(ep1.Status) != "ACTIVE" || sv(ep1.Region) != testRegion || !strings.Contains(ep1.AttributesJSON, `"SubnetIds":["subnet-1"]`) {
		t.Errorf("ep1 name/status/region = %q/%q/%q, attrs = %s", sv(ep1.Name), sv(ep1.Status), sv(ep1.Region), ep1.AttributesJSON)
	}
	if ep3 := rows[want[1]]; ep3.Status != nil {
		t.Errorf("ep3 status = %q; want unset", sv(ep3.Status))
	}
	if len(warns) != 1 || warns[0].Service != "opensearch:DescribeVpcEndpoints" ||
		!strings.Contains(warns[0].Message, "aos-ep4") || !strings.Contains(warns[0].Message, "aos-ep5") || strings.Contains(warns[0].Message, "aos-ep2") {
		t.Errorf("warnings = %+v; want one DescribeVpcEndpoints warning naming aos-ep4 and aos-ep5, not the ENDPOINT_NOT_FOUND aos-ep2", warns)
	}
}

// An endpoint deleted between list and describe is dropped without a warning.
func TestScanOpenSearchVpcEndpoints_NotFoundIsSilent(t *testing.T) {
	st := newTestStore(t)
	warns := countOpWarnings(st, "opensearch:DescribeVpcEndpoints")
	client := osClient(map[string]func(any) (any, error){
		"ListVpcEndpoints": func(any) (any, error) {
			return &opensearch.ListVpcEndpointsOutput{VpcEndpointSummaryList: []opensearchtypes.VpcEndpointSummary{{VpcEndpointId: ptrStr("aos-gone")}}}, nil
		},
		"DescribeVpcEndpoints": func(any) (any, error) {
			return &opensearch.DescribeVpcEndpointsOutput{VpcEndpointErrors: []opensearchtypes.VpcEndpointError{
				{VpcEndpointId: ptrStr("aos-gone"), ErrorCode: opensearchtypes.VpcEndpointErrorCodeEndpointNotFound},
			}}, nil
		},
	})
	total, _, err := scanOpenSearchVpcEndpoints(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID)
	if err != nil || total != 0 || *warns != 0 {
		t.Errorf("total = %d, warnings = %d, err = %v; want 0, 0, nil", total, *warns, err)
	}
}

func TestScanOpenSearchVpcEndpoints_Errors(t *testing.T) {
	for _, tc := range []struct {
		name      string
		handlers  func(*[][]string, *sync.Mutex) map[string]func(any) (any, error)
		wantCode  string
		wantTotal int
		wantWarns map[string]int
	}{
		{
			name: "empty list",
			handlers: func(*[][]string, *sync.Mutex) map[string]func(any) (any, error) {
				return map[string]func(any) (any, error){"ListVpcEndpoints": func(any) (any, error) { return &opensearch.ListVpcEndpointsOutput{}, nil }}
			},
		},
		{
			name: "list denied",
			handlers: func(*[][]string, *sync.Mutex) map[string]func(any) (any, error) {
				return map[string]func(any) (any, error){}
			},
			wantWarns: map[string]int{"opensearch:ListVpcEndpoints": 1},
		},
		{
			name: "list other",
			handlers: func(*[][]string, *sync.Mutex) map[string]func(any) (any, error) {
				return map[string]func(any) (any, error){"ListVpcEndpoints": func(any) (any, error) { return nil, osBadInput }}
			},
			wantCode: "ValidationException",
		},
		{
			name: "describe denied on page two",
			handlers: func(d *[][]string, mu *sync.Mutex) map[string]func(any) (any, error) {
				return osVpcHandlers(d, mu, map[int]error{2: osDenied})
			},
			wantTotal: 1, wantWarns: map[string]int{"opensearch:DescribeVpcEndpoints": 1},
		},
		{
			name: "describe other on page two",
			handlers: func(d *[][]string, mu *sync.Mutex) map[string]func(any) (any, error) {
				return osVpcHandlers(d, mu, map[int]error{2: osBadInput})
			},
			wantCode: "ValidationException", wantTotal: 1,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := map[string]int{}
			var mu sync.Mutex
			st.OnWarn = func(w store.ScanWarning) {
				mu.Lock()
				warns[w.Service]++
				mu.Unlock()
			}
			var described [][]string
			total, _, err := scanOpenSearchVpcEndpoints(context.Background(), osClient(tc.handlers(&described, &mu)), newTestAccount(testAccountID), testRegion, st, testScanID)
			if tc.wantCode == "" && err != nil || tc.wantCode != "" && !isAPIErrorCode(err, tc.wantCode) {
				t.Errorf("err = %v; want code %q", err, tc.wantCode)
			}
			if got := len(rowsByNativeID(t, st, TypeOpenSearchVpcEndpoint)); total != tc.wantTotal || got != tc.wantTotal {
				t.Errorf("total = %d, stored = %d; want %d", total, got, tc.wantTotal)
			}
			if tc.wantWarns == nil {
				tc.wantWarns = map[string]int{}
			}
			if !maps.Equal(warns, tc.wantWarns) {
				t.Errorf("warnings = %v; want %v", warns, tc.wantWarns)
			}
		})
	}
}

// osPagedHandler serves page one, then page two, for a paginated Describe op,
// recording each request's MaxResults; fail maps "p1"/"p2" to an error.
func osPagedHandler[In any](maxResults *[]int32, limit func(In) (int32, *string), page1, page2 any, fail map[string]error) func(any) (any, error) {
	var mu sync.Mutex
	return func(in any) (any, error) {
		n, token := limit(in.(In))
		mu.Lock()
		*maxResults = append(*maxResults, n)
		mu.Unlock()
		key := "p1"
		if token != nil {
			key = "p2"
		}
		if err := fail[key]; err != nil {
			return nil, err
		}
		if key == "p1" {
			return page1, nil
		}
		return page2, nil
	}
}

func osPackagesHandler(maxResults *[]int32, fail map[string]error) func(any) (any, error) {
	return osPagedHandler(maxResults, func(in *opensearch.DescribePackagesInput) (int32, *string) { return in.MaxResults, in.NextToken },
		&opensearch.DescribePackagesOutput{NextToken: ptrStr("t2"), PackageDetailsList: []opensearchtypes.PackageDetails{
			{PackageID: ptrStr("F1"), PackageName: ptrStr("synonyms"), PackageType: opensearchtypes.PackageTypeTxtDictionary, PackageStatus: opensearchtypes.PackageStatusAvailable, PackageOwner: ptrStr(testAccountID), CreatedAt: &osCreated},
			{PackageID: ptrStr("G-AWS"), PackageName: ptrStr("analysis-icu"), PackageType: opensearchtypes.PackageTypeZipPlugin, PackageOwner: ptrStr("AWS")},
		}},
		&opensearch.DescribePackagesOutput{PackageDetailsList: []opensearchtypes.PackageDetails{
			{PackageID: ptrStr("F2"), PackageName: ptrStr("plugin")},
			{PackageID: ptrStr("F3"), PackageStatus: opensearchtypes.PackageStatusDeleted},
			{PackageName: ptrStr("no-id")},
		}}, fail)
}

func osPackageID(id string) string { return "arn:aws:es:us-east-1:123456789012:package/" + id }

func TestScanOpenSearchPackages_StoresCustomPackages(t *testing.T) {
	var maxResults []int32
	st, total, err := osScanAccount(t, scanOpenSearchPackages, map[string]func(any) (any, error){"DescribePackages": osPackagesHandler(&maxResults, nil)})
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if !slices.Equal(maxResults, []int32{100, 100}) {
		t.Errorf("MaxResults per page = %v; want [100 100]", maxResults)
	}
	rows := rowsByNativeID(t, st, TypeOpenSearchPackage)
	want := []string{osPackageID("F1"), osPackageID("F2")}
	if ids := slices.Sorted(maps.Keys(rows)); total != 2 || !slices.Equal(ids, want) {
		t.Fatalf("total = %d, rows = %v; want 2, %v (AWS-owned, DELETED and id-less skipped)", total, ids, want)
	}
	f1 := rows[want[0]]
	if sv(f1.Name) != "synonyms" || sv(f1.Status) != "AVAILABLE" || sv(f1.CreatedAt) != "2026-03-04T05:06:07Z" || sv(f1.Region) != testRegion ||
		!strings.Contains(f1.AttributesJSON, `"PackageType":"TXT-DICTIONARY"`) || f1.ManagedByProvider {
		t.Errorf("F1 = name %q status %q created %q region %q managed %v attrs %s", sv(f1.Name), sv(f1.Status), sv(f1.CreatedAt), sv(f1.Region), f1.ManagedByProvider, f1.AttributesJSON)
	}
}

func TestScanOpenSearchPackages_Errors(t *testing.T) {
	for _, tc := range []struct {
		name      string
		fail      map[string]error
		wantCode  string
		wantTotal int
		wantWarns int
	}{
		{name: "denied", fail: map[string]error{"p1": osDenied}, wantWarns: 1},
		{name: "other", fail: map[string]error{"p1": osBadInput}, wantCode: "ValidationException"},
		{name: "denied on page two", fail: map[string]error{"p2": osDenied}, wantTotal: 1, wantWarns: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := countOpWarnings(st, "opensearch:DescribePackages")
			var maxResults []int32
			total, _, err := scanOpenSearchPackages(context.Background(), osClient(map[string]func(any) (any, error){"DescribePackages": osPackagesHandler(&maxResults, tc.fail)}), newTestAccount(testAccountID), testRegion, st, testScanID)
			if tc.wantCode == "" && err != nil || tc.wantCode != "" && !isAPIErrorCode(err, tc.wantCode) {
				t.Errorf("err = %v; want code %q", err, tc.wantCode)
			}
			if got := len(rowsByNativeID(t, st, TypeOpenSearchPackage)); total != tc.wantTotal || got != tc.wantTotal {
				t.Errorf("total = %d, stored = %d; want %d", total, got, tc.wantTotal)
			}
			if *warns != tc.wantWarns {
				t.Errorf("warnings = %d; want %d", *warns, tc.wantWarns)
			}
		})
	}
}

func osReservedHandler(maxResults *[]int32, fail map[string]error) func(any) (any, error) {
	return osPagedHandler(maxResults, func(in *opensearch.DescribeReservedInstancesInput) (int32, *string) {
		return in.MaxResults, in.NextToken
	},
		&opensearch.DescribeReservedInstancesOutput{NextToken: ptrStr("t2"), ReservedInstances: []opensearchtypes.ReservedInstance{
			{ReservedInstanceId: ptrStr("ri-1"), ReservationName: ptrStr("prod"), State: ptrStr("active"), StartTime: &osCreated, InstanceCount: 3},
		}},
		&opensearch.DescribeReservedInstancesOutput{ReservedInstances: []opensearchtypes.ReservedInstance{
			{ReservedInstanceId: ptrStr("ri-2"), State: ptrStr("retired")}, {ReservationName: ptrStr("no-id")},
		}}, fail)
}

func osReservedID(id string) string {
	return "arn:aws:es:us-east-1:123456789012:reserved-instance/" + id
}

func TestScanOpenSearchReservedInstances_StoresEveryState(t *testing.T) {
	var maxResults []int32
	st, total, err := osScanAccount(t, scanOpenSearchReservedInstances, map[string]func(any) (any, error){"DescribeReservedInstances": osReservedHandler(&maxResults, nil)})
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if !slices.Equal(maxResults, []int32{100, 100}) {
		t.Errorf("MaxResults per page = %v; want [100 100]", maxResults)
	}
	rows := rowsByNativeID(t, st, TypeOpenSearchReservedInstance)
	want := []string{osReservedID("ri-1"), osReservedID("ri-2")}
	if ids := slices.Sorted(maps.Keys(rows)); total != 2 || !slices.Equal(ids, want) {
		t.Fatalf("total = %d, rows = %v; want 2, %v", total, ids, want)
	}
	ri1 := rows[want[0]]
	if sv(ri1.Name) != "prod" || sv(ri1.Status) != "active" || sv(ri1.CreatedAt) != "2026-03-04T05:06:07Z" || sv(ri1.Region) != testRegion ||
		!strings.Contains(ri1.AttributesJSON, `"InstanceCount":3`) {
		t.Errorf("ri-1 = name %q status %q created %q region %q attrs %s", sv(ri1.Name), sv(ri1.Status), sv(ri1.CreatedAt), sv(ri1.Region), ri1.AttributesJSON)
	}
	if sv(rows[want[1]].Status) != "retired" {
		t.Errorf("ri-2 status = %q; want retired", sv(rows[want[1]].Status))
	}
}

func TestScanOpenSearchReservedInstances_Errors(t *testing.T) {
	for _, tc := range []struct {
		name      string
		fail      map[string]error
		wantCode  string
		wantTotal int
		wantWarns int
	}{
		{name: "denied", fail: map[string]error{"p1": osDenied}, wantWarns: 1},
		{name: "other on page two", fail: map[string]error{"p2": osBadInput}, wantCode: "ValidationException", wantTotal: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := countOpWarnings(st, "opensearch:DescribeReservedInstances")
			var maxResults []int32
			total, _, err := scanOpenSearchReservedInstances(context.Background(), osClient(map[string]func(any) (any, error){"DescribeReservedInstances": osReservedHandler(&maxResults, tc.fail)}), newTestAccount(testAccountID), testRegion, st, testScanID)
			if tc.wantCode == "" && err != nil || tc.wantCode != "" && !isAPIErrorCode(err, tc.wantCode) {
				t.Errorf("err = %v; want code %q", err, tc.wantCode)
			}
			if got := len(rowsByNativeID(t, st, TypeOpenSearchReservedInstance)); total != tc.wantTotal || got != tc.wantTotal {
				t.Errorf("total = %d, stored = %d; want %d", total, got, tc.wantTotal)
			}
			if *warns != tc.wantWarns {
				t.Errorf("warnings = %d; want %d", *warns, tc.wantWarns)
			}
		})
	}
}

func osDomainInfo(owner, region, name string) *opensearchtypes.DomainInformationContainer {
	return &opensearchtypes.DomainInformationContainer{AWSDomainInformation: &opensearchtypes.AWSDomainInformation{
		OwnerId: ptrStr(owner), Region: ptrStr(region), DomainName: ptrStr(name),
	}}
}

const osRemoteAccount = "210987654321"

func TestScanOpenSearchOutboundConnections_StoredUnderLocalDomain(t *testing.T) {
	var maxResults []int32
	local := osDomainInfo(testAccountID, testRegion, "logs")
	remote := osDomainInfo(osRemoteAccount, "eu-west-1", "central")
	handler := osPagedHandler(&maxResults, func(in *opensearch.DescribeOutboundConnectionsInput) (int32, *string) {
		return in.MaxResults, in.NextToken
	},
		&opensearch.DescribeOutboundConnectionsOutput{NextToken: ptrStr("t2"), Connections: []opensearchtypes.OutboundConnection{{
			ConnectionId: ptrStr("c1"), ConnectionAlias: ptrStr("to-central"), LocalDomainInfo: local, RemoteDomainInfo: remote,
			ConnectionStatus: &opensearchtypes.OutboundConnectionStatus{StatusCode: opensearchtypes.OutboundConnectionStatusCodeActive},
		}}},
		&opensearch.DescribeOutboundConnectionsOutput{Connections: []opensearchtypes.OutboundConnection{
			{ConnectionId: ptrStr("c2"), LocalDomainInfo: local, RemoteDomainInfo: remote},
			{ConnectionId: ptrStr("c-deleted"), LocalDomainInfo: local, ConnectionStatus: &opensearchtypes.OutboundConnectionStatus{StatusCode: opensearchtypes.OutboundConnectionStatusCodeDeleted}},
			{ConnectionId: ptrStr("c-no-local"), RemoteDomainInfo: remote},
			{ConnectionId: ptrStr("c-no-owner"), LocalDomainInfo: osDomainInfo("", testRegion, "logs")},
			{LocalDomainInfo: local},
		}}, nil)
	st, total, err := osScanAccount(t, scanOpenSearchOutboundConnections, map[string]func(any) (any, error){"DescribeOutboundConnections": handler})
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if !slices.Equal(maxResults, []int32{100, 100}) {
		t.Errorf("MaxResults per page = %v; want [100 100]", maxResults)
	}
	rows := rowsByNativeID(t, st, TypeOpenSearchOutboundConnection)
	want := []string{osDomainARN + "/outbound-connection/c1", osDomainARN + "/outbound-connection/c2"}
	if ids := slices.Sorted(maps.Keys(rows)); total != 2 || !slices.Equal(ids, want) {
		t.Fatalf("total = %d, rows = %v; want 2, %v", total, ids, want)
	}
	c1 := rows[want[0]]
	if sv(c1.Name) != "to-central" || sv(c1.Status) != "ACTIVE" || sv(c1.Region) != testRegion || !strings.Contains(c1.AttributesJSON, `"DomainName":"central"`) {
		t.Errorf("c1 = name %q status %q region %q attrs %s", sv(c1.Name), sv(c1.Status), sv(c1.Region), c1.AttributesJSON)
	}
	if c2 := rows[want[1]]; c2.Status != nil {
		t.Errorf("c2 status = %q; want unset", sv(c2.Status))
	}
}

func TestScanOpenSearchInboundConnections_StoredUnderRemoteDomain(t *testing.T) {
	var maxResults []int32
	source := osDomainInfo(osRemoteAccount, "eu-west-1", "edge")
	ours := osDomainInfo(testAccountID, testRegion, "logs")
	handler := osPagedHandler(&maxResults, func(in *opensearch.DescribeInboundConnectionsInput) (int32, *string) {
		return in.MaxResults, in.NextToken
	},
		&opensearch.DescribeInboundConnectionsOutput{NextToken: ptrStr("t2"), Connections: []opensearchtypes.InboundConnection{{
			ConnectionId: ptrStr("c1"), LocalDomainInfo: source, RemoteDomainInfo: ours,
			ConnectionStatus: &opensearchtypes.InboundConnectionStatus{StatusCode: opensearchtypes.InboundConnectionStatusCodePendingAcceptance},
		}}},
		&opensearch.DescribeInboundConnectionsOutput{Connections: []opensearchtypes.InboundConnection{
			{ConnectionId: ptrStr("c-deleted"), RemoteDomainInfo: ours, ConnectionStatus: &opensearchtypes.InboundConnectionStatus{StatusCode: opensearchtypes.InboundConnectionStatusCodeDeleted}},
			{ConnectionId: ptrStr("c-deleting"), RemoteDomainInfo: ours, ConnectionStatus: &opensearchtypes.InboundConnectionStatus{StatusCode: opensearchtypes.InboundConnectionStatusCodeDeleting}},
			{ConnectionId: ptrStr("c-no-remote"), LocalDomainInfo: source},
		}}, nil)
	st, total, err := osScanAccount(t, scanOpenSearchInboundConnections, map[string]func(any) (any, error){"DescribeInboundConnections": handler})
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if !slices.Equal(maxResults, []int32{100, 100}) {
		t.Errorf("MaxResults per page = %v; want [100 100]", maxResults)
	}
	rows := rowsByNativeID(t, st, TypeOpenSearchInboundConnection)
	want := []string{osDomainARN + "/inbound-connection/c-deleting", osDomainARN + "/inbound-connection/c1"}
	if ids := slices.Sorted(maps.Keys(rows)); total != 2 || !slices.Equal(ids, want) {
		t.Fatalf("total = %d, rows = %v; want 2, %v", total, ids, want)
	}
	if c1 := rows[want[1]]; sv(c1.Name) != "c1" || sv(c1.Status) != "PENDING_ACCEPTANCE" || sv(c1.Region) != testRegion {
		t.Errorf("c1 = name %q status %q region %q", sv(c1.Name), sv(c1.Status), sv(c1.Region))
	}
}

// One connection id reported by both sides, with both domains in the
// scanned account, is stored as two rows that do not merge.
func TestScanOpenSearchConnections_BothSidesInOneAccountStayDistinct(t *testing.T) {
	st := newTestStore(t)
	client := osClient(map[string]func(any) (any, error){
		"DescribeOutboundConnections": func(any) (any, error) {
			return &opensearch.DescribeOutboundConnectionsOutput{Connections: []opensearchtypes.OutboundConnection{{
				ConnectionId: ptrStr("c1"), LocalDomainInfo: osDomainInfo(testAccountID, testRegion, "logs"), RemoteDomainInfo: osDomainInfo(testAccountID, testRegion, "central"),
			}}}, nil
		},
		"DescribeInboundConnections": func(any) (any, error) {
			return &opensearch.DescribeInboundConnectionsOutput{Connections: []opensearchtypes.InboundConnection{{
				ConnectionId: ptrStr("c1"), LocalDomainInfo: osDomainInfo(testAccountID, testRegion, "logs"), RemoteDomainInfo: osDomainInfo(testAccountID, testRegion, "central"),
			}}}, nil
		},
	})
	acct := newTestAccount(testAccountID)
	if _, _, err := scanOpenSearchOutboundConnections(context.Background(), client, acct, testRegion, st, testScanID); err != nil {
		t.Fatalf("outbound: %v", err)
	}
	if _, _, err := scanOpenSearchInboundConnections(context.Background(), client, acct, testRegion, st, testScanID); err != nil {
		t.Fatalf("inbound: %v", err)
	}
	out := rowsByNativeID(t, st, TypeOpenSearchOutboundConnection)
	in := rowsByNativeID(t, st, TypeOpenSearchInboundConnection)
	if _, ok := out[osDomainARN+"/outbound-connection/c1"]; !ok || len(out) != 1 {
		t.Errorf("outbound rows = %v; want the row under domain/logs", slices.Collect(maps.Keys(out)))
	}
	if _, ok := in["arn:aws:es:us-east-1:123456789012:domain/central/inbound-connection/c1"]; !ok || len(in) != 1 {
		t.Errorf("inbound rows = %v; want the row under domain/central", slices.Collect(maps.Keys(in)))
	}
}

func TestScanOpenSearchConnections_Errors(t *testing.T) {
	for _, tc := range []struct {
		name     string
		op       string
		scan     func(context.Context, opensearchAPI, *account, string, *store.Store, string) (int, int, error)
		err      error
		wantCode string
		wantWarn int
	}{
		{name: "outbound denied", op: "DescribeOutboundConnections", scan: scanOpenSearchOutboundConnections, err: osDenied, wantWarn: 1},
		{name: "outbound other", op: "DescribeOutboundConnections", scan: scanOpenSearchOutboundConnections, err: osBadInput, wantCode: "ValidationException"},
		{name: "inbound denied", op: "DescribeInboundConnections", scan: scanOpenSearchInboundConnections, err: osDenied, wantWarn: 1},
		{name: "inbound other", op: "DescribeInboundConnections", scan: scanOpenSearchInboundConnections, err: osBadInput, wantCode: "ValidationException"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := countOpWarnings(st, "opensearch:"+tc.op)
			client := osClient(map[string]func(any) (any, error){tc.op: func(any) (any, error) { return nil, tc.err }})
			total, _, err := tc.scan(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID)
			if tc.wantCode == "" && err != nil || tc.wantCode != "" && !isAPIErrorCode(err, tc.wantCode) {
				t.Errorf("err = %v; want code %q", err, tc.wantCode)
			}
			if total != 0 || *warns != tc.wantWarn {
				t.Errorf("total = %d, warnings = %d; want 0, %d", total, *warns, tc.wantWarn)
			}
		})
	}
}

func TestScanOpenSearchConnections_Empty(t *testing.T) {
	st := newTestStore(t)
	client := osClient(map[string]func(any) (any, error){
		"DescribeOutboundConnections": func(any) (any, error) { return &opensearch.DescribeOutboundConnectionsOutput{}, nil },
		"DescribeInboundConnections":  func(any) (any, error) { return &opensearch.DescribeInboundConnectionsOutput{}, nil },
		"DescribePackages":            func(any) (any, error) { return &opensearch.DescribePackagesOutput{}, nil },
		"DescribeReservedInstances":   func(any) (any, error) { return &opensearch.DescribeReservedInstancesOutput{}, nil },
	})
	for name, scan := range map[string]func(context.Context, opensearchAPI, *account, string, *store.Store, string) (int, int, error){
		"outbound": scanOpenSearchOutboundConnections, "inbound": scanOpenSearchInboundConnections,
		"packages": scanOpenSearchPackages, "reserved": scanOpenSearchReservedInstances,
	} {
		if total, _, err := scan(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID); total != 0 || err != nil {
			t.Errorf("%s: total = %d, err = %v; want 0, nil", name, total, err)
		}
	}
}
