package aws

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/emr"
	emrtypes "github.com/aws/aws-sdk-go-v2/service/emr/types"
	"github.com/icearp/disco-cli/store"
)

func emrTestClusterARN(id string) string {
	return emrARN(testRegion, testAccountID, "cluster", id)
}

func emrListed(id string) emrtypes.ClusterSummary {
	return emrtypes.ClusterSummary{
		ClusterArn: ptrStr(emrTestClusterARN(id)), Id: ptrStr(id), Name: ptrStr("listed-" + id),
		Status: &emrtypes.ClusterStatus{State: emrtypes.ClusterStateWaiting},
	}
}

// emrDescribed is the DescribeCluster body the fake returns for id.
func emrDescribed(id string, tags []emrtypes.Tag) *emrtypes.Cluster {
	return &emrtypes.Cluster{
		ClusterArn: ptrStr(emrTestClusterARN(id)), Id: ptrStr(id), Name: ptrStr("described-" + id),
		ReleaseLabel: ptrStr("emr-7.1.0"), ServiceRole: ptrStr("EMR_DefaultRole"),
		Status: &emrtypes.ClusterStatus{State: emrtypes.ClusterStateWaiting},
		Tags:   tags,
	}
}

// emrFake answers the EMR ops. Its fields are read-only once a scan starts;
// calls is locked because DescribeCluster runs on fan-out goroutines.
type emrFake struct {
	pages     [][]emrtypes.ClusterSummary // ListClusters; Marker "1" is the second page
	tags      map[string][]emrtypes.Tag   // by cluster id
	bodiless  string                      // cluster id whose DescribeCluster has no Cluster
	errs      map[string]error            // "<Op> <cluster id, or ListClusters Marker>"
	secConfig string                      // name of the one listed security configuration
	afterList func()

	mu    sync.Mutex
	calls []string // "<Op> <cluster id>" of every per-cluster call
}

func newEMRFake(pages ...[]emrtypes.ClusterSummary) *emrFake {
	return &emrFake{pages: pages, tags: map[string][]emrtypes.Tag{}, errs: map[string]error{}, secConfig: "sc-1"}
}

func (f *emrFake) called(op, id string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, op+" "+id)
}

func (f *emrFake) wasCalled(op, id string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Contains(f.calls, op+" "+id)
}

// perCluster serves one of the per-cluster child listings: the call is
// recorded and fails with errs["<op> <cluster id>"] when set.
func (f *emrFake) perCluster(op string, id *string) error {
	f.called(op, sv(id))
	return f.errs[op+" "+sv(id)]
}

func (f *emrFake) client() *emr.Client {
	return emr.NewFromConfig(opStubConfig(map[string]func(in any) (any, error){
		"ListClusters": func(in any) (any, error) {
			i := in.(*emr.ListClustersInput)
			if i.ClusterStates != nil || i.CreatedAfter != nil || i.CreatedBefore != nil {
				return nil, apiErr("InvalidRequestException", fmt.Sprintf("unexpected filter in %+v", *i))
			}
			if err := f.errs["ListClusters "+sv(i.Marker)]; err != nil {
				return nil, err
			}
			if f.afterList != nil {
				defer f.afterList()
			}
			page, next := cogPage(f.pages, sv(i.Marker))
			return &emr.ListClustersOutput{Clusters: page, Marker: next}, nil
		},
		"DescribeCluster": func(in any) (any, error) {
			id := sv(in.(*emr.DescribeClusterInput).ClusterId)
			f.called("DescribeCluster", id)
			if err := f.errs["DescribeCluster "+id]; err != nil {
				return nil, err
			}
			if id == f.bodiless {
				return &emr.DescribeClusterOutput{}, nil
			}
			return &emr.DescribeClusterOutput{Cluster: emrDescribed(id, f.tags[id])}, nil
		},
		"ListInstanceFleets": func(in any) (any, error) {
			id := in.(*emr.ListInstanceFleetsInput).ClusterId
			if err := f.perCluster("ListInstanceFleets", id); err != nil {
				return nil, err
			}
			return &emr.ListInstanceFleetsOutput{InstanceFleets: []emrtypes.InstanceFleet{{Id: ptrStr("if-" + sv(id))}}}, nil
		},
		"ListInstanceGroups": func(in any) (any, error) {
			id := in.(*emr.ListInstanceGroupsInput).ClusterId
			if err := f.perCluster("ListInstanceGroups", id); err != nil {
				return nil, err
			}
			return &emr.ListInstanceGroupsOutput{InstanceGroups: []emrtypes.InstanceGroup{{Id: ptrStr("ig-" + sv(id))}}}, nil
		},
		"ListSteps": func(in any) (any, error) {
			id := in.(*emr.ListStepsInput).ClusterId
			if err := f.perCluster("ListSteps", id); err != nil {
				return nil, err
			}
			return &emr.ListStepsOutput{Steps: []emrtypes.StepSummary{{Id: ptrStr("s-" + sv(id))}}}, nil
		},
		"ListSecurityConfigurations": func(any) (any, error) {
			f.called("ListSecurityConfigurations", "")
			if err := f.errs["ListSecurityConfigurations "]; err != nil {
				return nil, err
			}
			return &emr.ListSecurityConfigurationsOutput{SecurityConfigurations: []emrtypes.SecurityConfigurationSummary{{Name: &f.secConfig}}}, nil
		},
		"ListStudios": func(any) (any, error) {
			f.called("ListStudios", "")
			return &emr.ListStudiosOutput{Studios: []emrtypes.StudioSummary{{StudioId: ptrStr("es-1")}, {StudioId: ptrStr("es-2")}}}, nil
		},
		"ListStudioSessionMappings": func(in any) (any, error) {
			id := sv(in.(*emr.ListStudioSessionMappingsInput).StudioId)
			f.called("ListStudioSessionMappings", id)
			return &emr.ListStudioSessionMappingsOutput{SessionMappings: []emrtypes.SessionMappingSummary{{IdentityId: ptrStr("u-" + id)}}}, nil
		},
	}, apiErr("AccessDeniedException", "User: x is not authorized to perform: elasticmapreduce:Op")))
}

func scanEMRTestClusters(ctx context.Context, f *emrFake, st *store.Store) ([]childParent, int, int, error) {
	return scanEMRClusters(ctx, f.client(), newTestAccount(testAccountID), testRegion, st, testScanID)
}

func scanEMRTestAll(ctx context.Context, f *emrFake, st *store.Store) (int, int, error) {
	return scanEMRAll(ctx, f.client(), newTestAccount(testAccountID), testRegion, st, testScanID)
}

// assertEMRWarning fails unless exactly one warning was reported under op,
// scoped to the test account and region, naming key.
func assertEMRWarning(t *testing.T, warns []store.ScanWarning, op, key string) {
	t.Helper()
	var got []store.ScanWarning
	for _, w := range warns {
		if w.Service == op {
			got = append(got, w)
		}
	}
	if len(got) != 1 || got[0].Scope != testAccountID+"/"+testRegion || !strings.Contains(got[0].Message, key) {
		t.Errorf("%s warnings = %+v; want one scoped %s/%s naming %s", op, got, testAccountID, testRegion, key)
	}
}

// assertEMRClusterRow fails unless row is the cluster stored under id's ARN
// with name, attributes attrs (the SDK element it was built from) and tags
// (nil: none).
func assertEMRClusterRow(t *testing.T, rows map[string]store.Resource, id, name string, attrs any, tags map[string]string) {
	t.Helper()
	row, ok := rows[emrTestClusterARN(id)]
	if !ok {
		t.Fatalf("no row %s; got %v", emrTestClusterARN(id), rows)
	}
	if sv(row.Name) != name || sv(row.Region) != testRegion || row.Status != nil || row.AttributesJSON != mustJSON(attrs) {
		t.Errorf("%s: name %q, region %q, status %v, attrs %s; want %q, %q, nil, %s", id, sv(row.Name), sv(row.Region), row.Status, row.AttributesJSON, name, testRegion, mustJSON(attrs))
	}
	if tags == nil {
		if row.TagsJSON != nil {
			t.Errorf("%s: tags %s; want none", id, *row.TagsJSON)
		}
		return
	}
	assertTagsMap(t, row.TagsJSON, tags)
}

func TestScanEMRClusters_StoresDescribedClustersWithTags(t *testing.T) {
	st := newTestStore(t)
	warns := recordWarnings(st)
	unnamed := emrListed("j-untagged")
	unnamed.Name = nil
	f := newEMRFake([]emrtypes.ClusterSummary{emrListed("j-tagged")}, []emrtypes.ClusterSummary{unnamed})
	f.tags["j-tagged"] = []emrtypes.Tag{{Key: ptrStr("env"), Value: ptrStr("prod")}, {Key: ptrStr("team"), Value: ptrStr("data")}}

	parents, total, inserted, err := scanEMRTestClusters(context.Background(), f, st)
	if err != nil || total != 2 || inserted != 2 {
		t.Fatalf("scan = %d, %d, %v; want 2, 2, nil", total, inserted, err)
	}
	want := []childParent{{"j-tagged", emrTestClusterARN("j-tagged")}, {"j-untagged", emrTestClusterARN("j-untagged")}}
	if !reflect.DeepEqual(parents, want) {
		t.Errorf("parents = %v; want %v", parents, want)
	}
	rows := rowsByNativeID(t, st, TypeEMRCluster)
	assertEMRClusterRow(t, rows, "j-tagged", "listed-j-tagged", emrDescribed("j-tagged", f.tags["j-tagged"]), map[string]string{"env": "prod", "team": "data"})
	assertEMRClusterRow(t, rows, "j-untagged", "j-untagged", emrDescribed("j-untagged", nil), nil)
	if len(*warns) != 0 {
		t.Errorf("warnings = %+v; want none", *warns)
	}
}

func TestScanEMRClusters_DescribeFailuresKeepListedRow(t *testing.T) {
	const id = "j-failing"
	for _, tc := range []struct {
		name     string
		err      error
		bodiless bool
		wantWarn string // op warned under, "" for none
		wantErr  func(error) bool
	}{
		{"denied", apiErr("AccessDeniedException", "x"), false, emrDescribeClusterOp, func(err error) bool { return err == nil }},
		{"transient", apiErr("ThrottlingException", "x"), false, "", isTransientNetworkError},
		{"invalid request", apiErr("InvalidRequestException", "x"), false, "", func(err error) bool {
			return isAPIErrorCode(err, "InvalidRequestException") && !isTransientNetworkError(err)
		}},
		{"no body", nil, true, emrDescribeClusterOp, func(err error) bool { return err == nil }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := recordWarnings(st)
			f := newEMRFake([]emrtypes.ClusterSummary{emrListed(id), emrListed("j-ok")})
			f.tags[id] = []emrtypes.Tag{{Key: ptrStr("env"), Value: ptrStr("prod")}}
			if tc.err != nil {
				f.errs["DescribeCluster "+id] = tc.err
			}
			if tc.bodiless {
				f.bodiless = id
			}

			parents, total, inserted, err := scanEMRTestClusters(context.Background(), f, st)
			if !tc.wantErr(err) {
				t.Errorf("err = %v; want it handled as %s", err, tc.name)
			}
			if total != 2 || inserted != 2 {
				t.Errorf("counts = %d, %d; want 2, 2", total, inserted)
			}
			if !slices.Contains(parents, childParent{id, emrTestClusterARN(id)}) {
				t.Errorf("parents = %v; want %s among them, so its children are still read", parents, id)
			}
			rows := rowsByNativeID(t, st, TypeEMRCluster)
			assertEMRClusterRow(t, rows, id, "listed-"+id, emrListed(id), nil)
			assertEMRClusterRow(t, rows, "j-ok", "listed-j-ok", emrDescribed("j-ok", nil), nil)
			if tc.wantWarn != "" {
				assertEMRWarning(t, *warns, tc.wantWarn, id)
			} else if len(*warns) != 0 {
				t.Errorf("warnings = %+v; want none", *warns)
			}
		})
	}
}

// TestScanEMRClusters_DeniedReadsShareOneWarning pins one warning per op for
// every denied DescribeCluster.
func TestScanEMRClusters_DeniedReadsShareOneWarning(t *testing.T) {
	st := newTestStore(t)
	warns := countOpWarnings(st, emrDescribeClusterOp)
	f := newEMRFake([]emrtypes.ClusterSummary{emrListed("j-a"), emrListed("j-b")})
	f.errs["DescribeCluster j-a"] = apiErr("AccessDeniedException", "x")
	f.errs["DescribeCluster j-b"] = apiErr("AccessDeniedException", "x")
	if _, total, inserted, err := scanEMRTestClusters(context.Background(), f, st); err != nil || total != 2 || inserted != 2 {
		t.Fatalf("scan = %d, %d, %v; want 2, 2, nil", total, inserted, err)
	}
	if *warns != 1 {
		t.Errorf("DescribeCluster warnings = %d; want 1", *warns)
	}
}

func TestScanEMRClusters_ListFailures(t *testing.T) {
	for _, tc := range []struct {
		name       string
		failMarker string
		code       string
		wantIDs    []string
		wantWarn   bool
		wantCode   string
	}{
		{"first page denied", "", "AccessDeniedException", nil, true, ""},
		{"later page denied", "1", "AccessDeniedException", []string{"j-first"}, true, ""},
		{"later page fails", "1", "InvalidRequestException", []string{"j-first"}, false, "InvalidRequestException"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := recordWarnings(st)
			f := newEMRFake([]emrtypes.ClusterSummary{emrListed("j-first")}, []emrtypes.ClusterSummary{emrListed("j-second")})
			f.errs["ListClusters "+tc.failMarker] = apiErr(tc.code, "x")

			parents, total, inserted, err := scanEMRTestClusters(context.Background(), f, st)
			if tc.wantCode == "" && err != nil || tc.wantCode != "" && !isAPIErrorCode(err, tc.wantCode) {
				t.Errorf("err = %v; want code %q", err, tc.wantCode)
			}
			if n := len(tc.wantIDs); total != n || inserted != n || len(parents) != n {
				t.Errorf("counts = %d, %d, parents %v; want %d", total, inserted, parents, n)
			}
			rows := rowsByNativeID(t, st, TypeEMRCluster)
			for _, id := range tc.wantIDs {
				assertEMRClusterRow(t, rows, id, "listed-"+id, emrDescribed(id, nil), nil)
			}
			if tc.wantWarn {
				assertEMRWarning(t, *warns, emrListClustersOp, "AccessDeniedException")
			} else if len(*warns) != 0 {
				t.Errorf("warnings = %+v; want none", *warns)
			}
		})
	}
}

// TestScanEMRClusters_EmptyIDsWarn pins that a listed cluster without a
// ClusterArn is not stored, and one without an Id is stored as its listed
// element, is not described and is not a parent; each is warned about.
func TestScanEMRClusters_EmptyIDsWarn(t *testing.T) {
	st := newTestStore(t)
	warns := recordWarnings(st)
	noARN := emrListed("j-noarn")
	noARN.ClusterArn = nil
	noID := emrListed("j-noid")
	noID.Id = nil
	f := newEMRFake([]emrtypes.ClusterSummary{noARN, noID, emrListed("j-ok")})

	parents, total, inserted, err := scanEMRTestClusters(context.Background(), f, st)
	if err != nil || total != 2 || inserted != 2 {
		t.Fatalf("scan = %d, %d, %v; want 2, 2, nil", total, inserted, err)
	}
	if want := []childParent{{"j-ok", emrTestClusterARN("j-ok")}}; !reflect.DeepEqual(parents, want) {
		t.Errorf("parents = %v; want %v", parents, want)
	}
	rows := rowsByNativeID(t, st, TypeEMRCluster)
	assertEMRClusterRow(t, rows, "j-noid", "listed-j-noid", noID, nil)
	if f.wasCalled("DescribeCluster", "") {
		t.Errorf("DescribeCluster was called without a ClusterId")
	}
	var listWarns []store.ScanWarning
	for _, w := range *warns {
		if w.Service == emrListClustersOp {
			listWarns = append(listWarns, w)
		}
	}
	if len(listWarns) != 2 || len(*warns) != 2 {
		t.Fatalf("warnings = %+v; want two under %s", *warns, emrListClustersOp)
	}
	assertEMRWarning(t, listWarns[:1], emrListClustersOp, "j-noarn")
	assertEMRWarning(t, listWarns[1:], emrListClustersOp, emrTestClusterARN("j-noid"))
}

// TestScanEMRClusters_StoreWriteFailureStops pins that a failed write
// returns the store error alone, no parents and no warning claiming rows.
func TestScanEMRClusters_StoreWriteFailureStops(t *testing.T) {
	st := newTestStore(t)
	warns := recordWarnings(st)
	refuseStoreWrites(t, st, TypeEMRCluster)
	f := newEMRFake([]emrtypes.ClusterSummary{emrListed("j-denied"), emrListed("j-slow"), emrListed("j-empty")})
	f.errs["DescribeCluster j-denied"] = apiErr("AccessDeniedException", "x")
	f.errs["DescribeCluster j-slow"] = apiErr("ThrottlingException", "x")
	f.bodiless = "j-empty"

	parents, total, inserted, err := scanEMRTestClusters(context.Background(), f, st)
	if !errors.Is(err, store.ErrStoreWrite) || isTransientNetworkError(err) {
		t.Errorf("err = %v; want only the store write failure", err)
	}
	if total != 0 || inserted != 0 || parents != nil {
		t.Errorf("scan = %d, %d, %v; want 0, 0, no parents", total, inserted, parents)
	}
	if len(*warns) != 0 {
		t.Errorf("warnings = %+v; want none", *warns)
	}
}

// TestScanEMRClusters_ContextStop pins that once ctx ends no further cluster
// is described and the stop is returned.
func TestScanEMRClusters_ContextStop(t *testing.T) {
	st := newTestStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	f := newEMRFake([]emrtypes.ClusterSummary{emrListed("j-a")})
	f.afterList = cancel
	parents, total, inserted, err := scanEMRTestClusters(ctx, f, st)
	if !errors.Is(err, context.Canceled) || total != 0 || inserted != 0 || parents != nil {
		t.Errorf("scan = %d, %d, %v, %v; want 0, 0, no parents and the cancellation", total, inserted, parents, err)
	}
}

// emrAllRowCounts is, per type, how many rows a scanEMRAll run stored.
func emrAllRowCounts(t *testing.T, st *store.Store) map[string]int {
	t.Helper()
	got := map[string]int{}
	for _, typ := range []string{TypeEMRCluster, TypeEMRInstanceFleet, TypeEMRInstanceGroup, TypeEMRStep, TypeEMRSecurityConfig, TypeEMRStudio, TypeEMRStudioSessionMapping} {
		if n := len(rowsByNativeID(t, st, typ)); n > 0 {
			got[typ] = n
		}
	}
	return got
}

// TestScanEMRAll_PhasesRunPastErrors pins that a failed cluster read, a
// failed child listing of one cluster and a denied cluster listing do not
// stop the later clusters' children or the account-wide phases.
func TestScanEMRAll_PhasesRunPastErrors(t *testing.T) {
	accountWide := map[string]int{TypeEMRSecurityConfig: 1, TypeEMRStudio: 2, TypeEMRStudioSessionMapping: 2}
	for _, tc := range []struct {
		name      string
		errs      map[string]error
		wantRows  map[string]int
		wantCodes []string
	}{
		{
			"cluster read and child listing fail",
			map[string]error{
				"DescribeCluster j-a":    apiErr("InvalidRequestException", "x"),
				"ListInstanceFleets j-a": apiErr("ValidationException", "x"),
			},
			map[string]int{TypeEMRCluster: 2, TypeEMRInstanceFleet: 1, TypeEMRInstanceGroup: 2, TypeEMRStep: 2},
			[]string{"InvalidRequestException", "ValidationException"},
		},
		{"cluster listing denied", map[string]error{"ListClusters ": apiErr("AccessDeniedException", "x")}, map[string]int{}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			f := newEMRFake([]emrtypes.ClusterSummary{emrListed("j-a"), emrListed("j-b")})
			f.errs = tc.errs

			total, inserted, err := scanEMRTestAll(context.Background(), f, st)
			if (err == nil) != (len(tc.wantCodes) == 0) {
				t.Errorf("err = %v; want codes %v", err, tc.wantCodes)
			}
			for _, code := range tc.wantCodes {
				if !drsHasCode(err, code) {
					t.Errorf("err = %v; want it to hold %s", err, code)
				}
			}
			want := maps.Clone(tc.wantRows)
			maps.Copy(want, accountWide)
			got := emrAllRowCounts(t, st)
			if !maps.Equal(got, want) {
				t.Errorf("rows per type = %v; want %v", got, want)
			}
			n := 0
			for _, c := range want {
				n += c
			}
			if total != n || inserted != n {
				t.Errorf("counts = %d, %d; want %d", total, inserted, n)
			}
		})
	}
}

// TestScanEMRAll_TransientDemotedBesideHardError pins that a throttled
// DescribeCluster beside a later phase's hard error is warned about under the
// cluster phase's label, and the scan is classified by the hard error.
func TestScanEMRAll_TransientDemotedBesideHardError(t *testing.T) {
	st := newTestStore(t)
	warns := recordWarnings(st)
	f := newEMRFake([]emrtypes.ClusterSummary{emrListed("j-slow")})
	f.errs["DescribeCluster j-slow"] = apiErr("ThrottlingException", "x")
	f.errs["ListSecurityConfigurations "] = apiErr("ValidationException", "x")

	total, inserted, err := scanEMRTestAll(context.Background(), f, st)
	if got := classifyServiceError(err); got != outcomeError || !isAPIErrorCode(err, "ValidationException") || isAPIErrorCode(err, "ThrottlingException") {
		t.Errorf("outcome = %d, err = %v; want outcomeError (%d) with only the ValidationException", got, err, outcomeError)
	}
	// The cluster, its fleet, group and step, two studios and their mappings.
	if total != 8 || inserted != 8 {
		t.Errorf("counts = %d, %d; want 8, 8", total, inserted)
	}
	assertEMRWarning(t, *warns, "emr", "j-slow")
	if len(*warns) != 1 {
		t.Errorf("warnings = %+v; want only the demoted one", *warns)
	}
}

// TestScanEMRAll_StoreWriteFailureStopsLaterPhases pins that a failed write
// in any phase stops the scan before the next call, keeping the counts of
// the rows written before it.
func TestScanEMRAll_StoreWriteFailureStopsLaterPhases(t *testing.T) {
	for _, tc := range []struct {
		refused   string
		notCalled string // "<Op> <key>" of the next call
		wantTotal int
	}{
		{TypeEMRCluster, "ListInstanceFleets j-a", 0},
		{TypeEMRInstanceFleet, "ListInstanceGroups j-a", 2},
		{TypeEMRInstanceGroup, "ListSteps j-a", 3},
		{TypeEMRStep, "ListInstanceFleets j-b", 4},
		{TypeEMRSecurityConfig, "ListStudios ", 8},
		{TypeEMRStudio, "ListStudioSessionMappings es-1", 9},
		{TypeEMRStudioSessionMapping, "ListStudioSessionMappings es-2", 11},
	} {
		t.Run(tc.refused, func(t *testing.T) {
			st := newTestStore(t)
			refuseStoreWrites(t, st, tc.refused)
			f := newEMRFake([]emrtypes.ClusterSummary{emrListed("j-a"), emrListed("j-b")})

			total, inserted, err := scanEMRTestAll(context.Background(), f, st)
			if !errors.Is(err, store.ErrStoreWrite) || total != tc.wantTotal || inserted != tc.wantTotal {
				t.Errorf("scan = %d, %d, %v; want %d, %d and the store write failure", total, inserted, err, tc.wantTotal, tc.wantTotal)
			}
			op, key, _ := strings.Cut(tc.notCalled, " ")
			if f.wasCalled(op, key) {
				t.Errorf("%s was called after the failed write", tc.notCalled)
			}
		})
	}
}
