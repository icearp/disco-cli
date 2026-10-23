package aws

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ecs"
	ecstypes "github.com/aws/aws-sdk-go-v2/service/ecs/types"
	"github.com/aws/smithy-go"
	"github.com/icearp/disco-cli/store"
)

func ecsTestARN(kind, id string) string {
	return fmt.Sprintf("arn:aws:ecs:%s:%s:%s/%s", testRegion, testAccountID, kind, id)
}

func ecsTags(kv ...string) []ecstypes.Tag {
	var tags []ecstypes.Tag
	for i := 0; i < len(kv); i += 2 {
		tags = append(tags, ecstypes.Tag{Key: sdkaws.String(kv[i]), Value: sdkaws.String(kv[i+1])})
	}
	return tags
}

// ecsWarnings records every warning st reports.
func ecsWarnings(st *store.Store) *[]store.ScanWarning {
	var (
		mu sync.Mutex
		ws []store.ScanWarning
	)
	st.OnWarn = func(w store.ScanWarning) {
		mu.Lock()
		defer mu.Unlock()
		ws = append(ws, w)
	}
	return &ws
}

// assertECSWarnings checks that op was warned about once per key, each
// warning naming its key.
func assertECSWarnings(t *testing.T, ws []store.ScanWarning, op string, keys ...string) {
	t.Helper()
	var msgs []string
	for _, w := range ws {
		if w.Service == op {
			msgs = append(msgs, w.Message)
		}
	}
	if len(msgs) != len(keys) {
		t.Errorf("%s warnings = %q; want %d naming %q", op, msgs, len(keys), keys)
		return
	}
	for _, key := range keys {
		if !slices.ContainsFunc(msgs, func(m string) bool { return strings.Contains(m, key) }) {
			t.Errorf("%s warnings = %q; none names %s", op, msgs, key)
		}
	}
}

// ecsErrCodes returns the sorted API error codes of every member of err.
func ecsErrCodes(err error) []string {
	var codes []string
	var walk func(error)
	walk = func(err error) {
		if members := joinedMembers(err); len(members) > 0 {
			for _, m := range members {
				walk(m)
			}
			return
		}
		var ae smithy.APIError
		if errors.As(err, &ae) {
			codes = append(codes, ae.ErrorCode())
		}
	}
	walk(err)
	slices.Sort(codes)
	return codes
}

// assertECSRow checks a stored row's name, attributes (compared decoded), tags
// (nil: none stored) and managed flag.
func assertECSRow(t *testing.T, rows map[string]store.Resource, nativeID, name string, attrs any, wantTags map[string]string, managed bool) {
	t.Helper()
	row, ok := rows[nativeID]
	if !ok {
		t.Errorf("row %s missing", nativeID)
		return
	}
	if sv(row.Name) != name || sv(row.Region) != testRegion || row.ManagedByProvider != managed {
		t.Errorf("%s: name/region/managed = %q/%q/%v; want %q/%q/%v", nativeID, sv(row.Name), sv(row.Region), row.ManagedByProvider, name, testRegion, managed)
	}
	var got, want any
	if err := json.Unmarshal([]byte(row.AttributesJSON), &got); err != nil {
		t.Fatalf("%s: decode attributes: %v", nativeID, err)
	}
	if err := json.Unmarshal([]byte(mustJSON(attrs)), &want); err != nil {
		t.Fatalf("decode want: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("%s: attrs = %s, want %s", nativeID, row.AttributesJSON, mustJSON(attrs))
	}
	if wantTags == nil {
		if row.TagsJSON != nil {
			t.Errorf("%s: tags = %s; want none", nativeID, *row.TagsJSON)
		}
		return
	}
	assertTagsMap(t, row.TagsJSON, wantTags)
}

var (
	ecsCPASG = ecstypes.CapacityProvider{
		CapacityProviderArn: sdkaws.String(ecsTestARN("capacity-provider", "asg-cp")), Name: sdkaws.String("asg-cp"),
		Type: ecstypes.CapacityProviderTypeEc2Autoscaling, Status: ecstypes.CapacityProviderStatusActive,
		AutoScalingGroupProvider: &ecstypes.AutoScalingGroupProvider{AutoScalingGroupArn: sdkaws.String("arn:aws:autoscaling:us-east-1:123456789012:autoScalingGroup:g:autoScalingGroupName/asg")},
		Tags:                     ecsTags("team", "core", "env", "prod"),
	}
	ecsCPFargate = ecstypes.CapacityProvider{
		CapacityProviderArn: sdkaws.String(ecsTestARN("capacity-provider", "FARGATE")), Name: sdkaws.String("FARGATE"),
		Type: ecstypes.CapacityProviderTypeFargate, Status: ecstypes.CapacityProviderStatusActive,
	}
	ecsCPFargateSpot = ecstypes.CapacityProvider{
		CapacityProviderArn: sdkaws.String(ecsTestARN("capacity-provider", "FARGATE_SPOT")), Name: sdkaws.String("FARGATE_SPOT"),
		Type: ecstypes.CapacityProviderTypeFargateSpot, Status: ecstypes.CapacityProviderStatusActive,
	}
	ecsCPManagedInstances = ecstypes.CapacityProvider{
		CapacityProviderArn: sdkaws.String(ecsTestARN("capacity-provider", "mi-cp")), Name: sdkaws.String("mi-cp"),
		Cluster: sdkaws.String("cl-1"), Type: ecstypes.CapacityProviderTypeManagedInstances,
		Tags: ecsTags("owner", "platform"),
	}
	ecsCPNoARN = ecstypes.CapacityProvider{Name: sdkaws.String("no-arn-cp")}
)

// ecsCapacityProvidersHandler answers DescribeCapacityProviders from pages; a
// page other than the last carries NextToken "p<next index>". errs[i] fails
// page i. A call that does not ask for tags at the model's page cap fails, so
// both inputs are pinned.
func ecsCapacityProvidersHandler(pages [][]ecstypes.CapacityProvider, errs map[int]error) func(any) (any, error) {
	return func(in any) (any, error) {
		req := in.(*ecs.DescribeCapacityProvidersInput)
		if !slices.Equal(req.Include, []ecstypes.CapacityProviderField{ecstypes.CapacityProviderFieldTags}) || sdkaws.ToInt32(req.MaxResults) != 10 {
			return nil, fmt.Errorf("Include, MaxResults = %v, %d; want [TAGS], 10", req.Include, sdkaws.ToInt32(req.MaxResults))
		}
		page := 0
		if tok := sdkaws.ToString(req.NextToken); tok != "" {
			if _, err := fmt.Sscanf(tok, "p%d", &page); err != nil {
				return nil, err
			}
		}
		if err := errs[page]; err != nil {
			return nil, err
		}
		out := &ecs.DescribeCapacityProvidersOutput{CapacityProviders: pages[page]}
		if page+1 < len(pages) {
			out.NextToken = sdkaws.String(fmt.Sprintf("p%d", page+1))
		}
		return out, nil
	}
}

func scanECSCapacityProvidersWith(st *store.Store, handler func(any) (any, error)) (int, int, error) {
	client := ecs.NewFromConfig(opStubConfig(map[string]func(any) (any, error){"DescribeCapacityProviders": handler}, ecsDenied))
	return scanECSCapacityProviders(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID)
}

// Tags come from the listed element, and only FARGATE / FARGATE_SPOT are
// AWS-managed: an Auto Scaling group provider has no Cluster either.
func TestScanECSCapacityProviders_StoresTagsAndManaged(t *testing.T) {
	st := newTestStore(t)
	ws := ecsWarnings(st)
	total, inserted, err := scanECSCapacityProvidersWith(st, ecsCapacityProvidersHandler([][]ecstypes.CapacityProvider{
		{ecsCPASG, ecsCPFargate},
		{ecsCPManagedInstances, ecsCPNoARN, ecsCPFargateSpot},
	}, nil))
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != 4 || inserted != 4 {
		t.Errorf("total, inserted = %d, %d; want 4, 4", total, inserted)
	}
	rows := rowsByNativeID(t, st, TypeECSCapacityProvider)
	if len(rows) != 4 {
		t.Errorf("rows = %d; want 4", len(rows))
	}
	assertECSRow(t, rows, ecsTestARN("capacity-provider", "asg-cp"), "asg-cp", ecsCPASG, map[string]string{"team": "core", "env": "prod"}, false)
	assertECSRow(t, rows, ecsTestARN("capacity-provider", "FARGATE"), "FARGATE", ecsCPFargate, nil, true)
	assertECSRow(t, rows, ecsTestARN("capacity-provider", "FARGATE_SPOT"), "FARGATE_SPOT", ecsCPFargateSpot, nil, true)
	assertECSRow(t, rows, ecsTestARN("capacity-provider", "mi-cp"), "mi-cp", ecsCPManagedInstances, map[string]string{"owner": "platform"}, false)
	assertECSWarnings(t, *ws, "ecs:DescribeCapacityProviders", "no-arn-cp")
}

// A failed later page keeps the rows of the pages before it: a denial is
// warned about, any other error is returned.
func TestScanECSCapacityProviders_LaterPageFails(t *testing.T) {
	cases := []struct {
		name     string
		err      error
		wantCode string
		warned   int
	}{
		{"denied", ecsDenied, "", 1},
		{"other error", apiErr("ClientException", "boom"), "ClientException", 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			warnings := countOpWarnings(st, "ecs:DescribeCapacityProviders")
			total, inserted, err := scanECSCapacityProvidersWith(st, ecsCapacityProvidersHandler([][]ecstypes.CapacityProvider{
				{ecsCPASG, ecsCPFargate}, {ecsCPManagedInstances},
			}, map[int]error{1: tc.err}))
			if tc.wantCode == "" && err != nil || tc.wantCode != "" && !isAPIErrorCode(err, tc.wantCode) {
				t.Errorf("err = %v; want code %q", err, tc.wantCode)
			}
			if total != 2 || inserted != 2 || len(rowsByNativeID(t, st, TypeECSCapacityProvider)) != 2 {
				t.Errorf("total, inserted = %d, %d; want 2, 2 (page 1's rows)", total, inserted)
			}
			if *warnings != tc.warned {
				t.Errorf("warnings = %d; want %d", *warnings, tc.warned)
			}
		})
	}
}

// A page that repeats the token it was sent ends the listing as an error,
// keeping the rows read.
func TestScanECSCapacityProviders_RepeatedToken(t *testing.T) {
	st := newTestStore(t)
	pinned := ecsCapacityProvidersHandler([][]ecstypes.CapacityProvider{{ecsCPASG}, {ecsCPFargate}}, nil)
	total, inserted, err := scanECSCapacityProvidersWith(st, func(in any) (any, error) {
		out, err := pinned(in)
		if err == nil {
			// Page p1 names itself as the next page.
			out.(*ecs.DescribeCapacityProvidersOutput).NextToken = sdkaws.String("p1")
		}
		return out, err
	})
	if !errors.Is(err, errRepeatedNextToken) {
		t.Errorf("err = %v; want errRepeatedNextToken", err)
	}
	if total != 2 || inserted != 2 || len(rowsByNativeID(t, st, TypeECSCapacityProvider)) != 2 {
		t.Errorf("total, inserted = %d, %d; want 2, 2 (both pages' rows)", total, inserted)
	}
}

// A refused write is returned, and nothing is warned about: neither the
// denial nor the provider without an ARN.
func TestScanECSCapacityProviders_WriteRefusedWarnsNothing(t *testing.T) {
	st := newTestStore(t)
	refuseStoreWrites(t, st, TypeECSCapacityProvider)
	ws := ecsWarnings(st)
	total, inserted, err := scanECSCapacityProvidersWith(st, ecsCapacityProvidersHandler([][]ecstypes.CapacityProvider{
		{ecsCPASG, ecsCPNoARN}, nil,
	}, map[int]error{1: ecsDenied}))
	if !errors.Is(err, store.ErrStoreWrite) {
		t.Errorf("err = %v; want ErrStoreWrite", err)
	}
	if total != 0 || inserted != 0 {
		t.Errorf("total, inserted = %d, %d; want 0, 0", total, inserted)
	}
	if len(*ws) != 0 {
		t.Errorf("warnings = %v; want none", *ws)
	}
}

// ecsTaskSetFixture serves cl-1's services over two ListServices pages and a
// DescribeTaskSets outcome per service; cl-2's listing is denied, cl-3 is
// gone and cl-4's listing fails. Inputs that decide the response (MaxResults,
// Include, the service's cluster) are pinned.
type ecsTaskSetFixture struct {
	mu        sync.Mutex
	described []string
}

var (
	ecsTSCluster2 = ecsTestARN("cluster", "cl-2")
	ecsTSCluster3 = ecsTestARN("cluster", "cl-3")
	ecsTSCluster4 = ecsTestARN("cluster", "cl-4")
	ecsTSSvc      = func(name string) string { return ecsTestARN("service", "cl-1/"+name) }

	ecsTSTagged = ecstypes.TaskSet{
		TaskSetArn: sdkaws.String(ecsTestARN("task-set", "cl-1/svc-a/ecs-svc/1")), Id: sdkaws.String("ecs-svc/1"),
		ServiceArn: sdkaws.String(ecsTSSvc("svc-a")), ClusterArn: sdkaws.String(ecsTestClusterARN), Status: sdkaws.String("PRIMARY"),
		ExternalId: sdkaws.String("ext-1"), Tags: ecsTags("app", "web"),
	}
	ecsTSUntagged = ecstypes.TaskSet{
		TaskSetArn: sdkaws.String(ecsTestARN("task-set", "cl-1/svc-a/ecs-svc/2")), Id: sdkaws.String("ecs-svc/2"),
		ServiceArn: sdkaws.String(ecsTSSvc("svc-a")), ClusterArn: sdkaws.String(ecsTestClusterARN), Status: sdkaws.String("ACTIVE"),
	}
	ecsTSNoARN  = ecstypes.TaskSet{Id: sdkaws.String("ecs-svc/noarn"), ServiceArn: sdkaws.String(ecsTSSvc("svc-a"))}
	ecsTSSecond = ecstypes.TaskSet{
		TaskSetArn: sdkaws.String(ecsTestARN("task-set", "cl-1/svc-c/ecs-svc/3")), Id: sdkaws.String("ecs-svc/3"),
		ServiceArn: sdkaws.String(ecsTSSvc("svc-c")), ClusterArn: sdkaws.String(ecsTestClusterARN), Tags: ecsTags("tier", "back"),
	}
)

func (f *ecsTaskSetFixture) handlers() map[string]func(any) (any, error) {
	return map[string]func(any) (any, error){
		"ListServices": func(in any) (any, error) {
			req := in.(*ecs.ListServicesInput)
			if sdkaws.ToInt32(req.MaxResults) != 100 {
				return nil, fmt.Errorf("MaxResults = %d; want 100", sdkaws.ToInt32(req.MaxResults))
			}
			switch sdkaws.ToString(req.Cluster) {
			case ecsTSCluster2:
				return nil, ecsDenied
			case ecsTSCluster3:
				return nil, apiErr("ClusterNotFoundException", "Cluster not found.")
			case ecsTSCluster4:
				return nil, apiErr("ServerException", "boom")
			}
			if sdkaws.ToString(req.NextToken) == "" {
				// svc-a, the one read here that succeeds, is second on its page.
				return &ecs.ListServicesOutput{ServiceArns: []string{ecsTSSvc("svc-b"), ecsTSSvc("svc-a")}, NextToken: sdkaws.String("p1")}, nil
			}
			// svc-e precedes svc-d, so a not-active read tallied as a denial
			// would be the one the denial warning names.
			return &ecs.ListServicesOutput{ServiceArns: []string{ecsTSSvc("svc-c"), ecsTSSvc("svc-e"), ecsTSSvc("svc-d"), ecsTSSvc("svc-f"), ecsTSSvc("svc-g")}}, nil
		},
		"DescribeTaskSets": func(in any) (any, error) {
			req := in.(*ecs.DescribeTaskSetsInput)
			if !slices.Equal(req.Include, []ecstypes.TaskSetField{ecstypes.TaskSetFieldTags}) || sdkaws.ToString(req.Cluster) != ecsTestClusterARN {
				return nil, fmt.Errorf("Include, Cluster = %v, %s; want [TAGS], %s", req.Include, sdkaws.ToString(req.Cluster), ecsTestClusterARN)
			}
			svc := sdkaws.ToString(req.Service)
			f.mu.Lock()
			f.described = append(f.described, svc)
			f.mu.Unlock()
			switch svc {
			case ecsTSSvc("svc-a"):
				return &ecs.DescribeTaskSetsOutput{TaskSets: []ecstypes.TaskSet{ecsTSTagged, ecsTSUntagged, ecsTSNoARN}}, nil
			case ecsTSSvc("svc-b"):
				return nil, apiErr("InvalidParameterException", "TaskSets cannot be empty.")
			case ecsTSSvc("svc-c"):
				return &ecs.DescribeTaskSetsOutput{TaskSets: []ecstypes.TaskSet{ecsTSSecond}}, nil
			case ecsTSSvc("svc-d"):
				return nil, ecsDenied
			case ecsTSSvc("svc-e"):
				return nil, apiErr("ServiceNotActiveException", "Service was not ACTIVE.")
			case ecsTSSvc("svc-f"):
				return nil, apiErr("ServiceNotFoundException", "Service not found.")
			}
			return nil, apiErr("ClientException", "boom")
		},
	}
}

func scanECSTaskSetsWith(st *store.Store, f *ecsTaskSetFixture, clusters ...string) (int, int, error) {
	client := ecs.NewFromConfig(opStubConfig(f.handlers(), ecsDenied))
	return scanECSTaskSets(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID, clusters)
}

// Every service of every page is read; each failure costs only its own
// cluster or service, and the errors come back after the rows are stored.
func TestScanECSTaskSets_StoresTagsAndSettlesEachFailure(t *testing.T) {
	st := newTestStore(t)
	ws := ecsWarnings(st)
	var f ecsTaskSetFixture
	total, inserted, err := scanECSTaskSetsWith(st, &f, ecsTestClusterARN, ecsTSCluster2, ecsTSCluster3, ecsTSCluster4)
	if total != 3 || inserted != 3 {
		t.Errorf("total, inserted = %d, %d; want 3, 3", total, inserted)
	}
	rows := rowsByNativeID(t, st, TypeECSTaskSet)
	if len(rows) != 3 {
		t.Errorf("rows = %d; want 3", len(rows))
	}
	assertECSRow(t, rows, sv(ecsTSTagged.TaskSetArn), "ecs-svc/1", ecsTSTagged, map[string]string{"app": "web"}, false)
	assertECSRow(t, rows, sv(ecsTSUntagged.TaskSetArn), "ecs-svc/2", ecsTSUntagged, nil, false)
	assertECSRow(t, rows, sv(ecsTSSecond.TaskSetArn), "ecs-svc/3", ecsTSSecond, map[string]string{"tier": "back"}, false)

	// cl-4's listing and svc-g's read fail; every other failure is skipped.
	if got, want := ecsErrCodes(err), []string{"ClientException", "ServerException"}; !slices.Equal(got, want) {
		t.Errorf("error codes = %v (%v); want %v", got, err, want)
	}
	if got := classifyServiceError(err); got != outcomeError {
		t.Errorf("classifyServiceError = %v; want outcomeError", got)
	}
	if len(f.described) != 7 {
		t.Errorf("DescribeTaskSets calls = %d (%v); want 7, one per listed service", len(f.described), f.described)
	}
	// svc-e (not active) is skipped silently, like svc-f (not found).
	assertECSWarnings(t, *ws, "ecs:ListServices(task-sets)", ecsTSCluster2)
	assertECSWarnings(t, *ws, "ecs:DescribeTaskSets", ecsTSSvc("svc-d"), "ecs-svc/noarn")
}

// A refused write is returned, and nothing is warned about.
func TestScanECSTaskSets_WriteRefusedWarnsNothing(t *testing.T) {
	st := newTestStore(t)
	refuseStoreWrites(t, st, TypeECSTaskSet)
	ws := ecsWarnings(st)
	total, inserted, err := scanECSTaskSetsWith(st, &ecsTaskSetFixture{}, ecsTestClusterARN, ecsTSCluster2)
	if !errors.Is(err, store.ErrStoreWrite) {
		t.Errorf("err = %v; want ErrStoreWrite", err)
	}
	if total != 0 || inserted != 0 {
		t.Errorf("total, inserted = %d, %d; want 0, 0", total, inserted)
	}
	if len(*ws) != 0 {
		t.Errorf("warnings = %v; want none", *ws)
	}
}

// ecsExtendedHandlers serves one capacity provider, cl-1's association and
// the task-set fixture, with failing replaced per op.
func ecsExtendedHandlers(f *ecsTaskSetFixture, failing map[string]error) map[string]func(any) (any, error) {
	h := f.handlers()
	h["DescribeCapacityProviders"] = ecsCapacityProvidersHandler([][]ecstypes.CapacityProvider{{ecsCPASG}}, nil)
	h["DescribeClusters"] = func(any) (any, error) {
		return &ecs.DescribeClustersOutput{Clusters: []ecstypes.Cluster{{ClusterArn: sdkaws.String(ecsTestClusterARN), ClusterName: sdkaws.String("cl-1")}}}, nil
	}
	for op, err := range failing {
		h[op] = func(any) (any, error) { return nil, err }
	}
	return h
}

func scanECSExtendedWith(st *store.Store, h map[string]func(any) (any, error)) (int, int, error) {
	client := ecs.NewFromConfig(opStubConfig(h, ecsDenied))
	return scanECSExtended(context.Background(), client, newTestAccount(testAccountID), testRegion, st, testScanID, []string{ecsTestClusterARN})
}

// A failed phase does not stop the later ones; its transient error is demoted
// to one warning beside a later phase's hard error, which is returned.
func TestScanECSExtended_PhaseErrorKeepsLaterPhases(t *testing.T) {
	st := newTestStore(t)
	warnings := countOpWarnings(st, "ecs:DescribeCapacityProviders")
	var f ecsTaskSetFixture
	total, inserted, err := scanECSExtendedWith(st, ecsExtendedHandlers(&f, map[string]error{
		"DescribeCapacityProviders": apiErr("ThrottlingException", "Rate exceeded"),
	}))
	if got := classifyServiceError(err); got != outcomeError || !isAPIErrorCode(err, "ClientException") {
		t.Errorf("err = %v (%v); want the task-set ClientException as outcomeError", err, got)
	}
	if isAPIErrorCode(err, "ThrottlingException") {
		t.Errorf("err = %v; want the throttle demoted", err)
	}
	if *warnings != 1 {
		t.Errorf("ecs:DescribeCapacityProviders warnings = %d; want 1", *warnings)
	}
	// One association plus three task sets.
	if total != 4 || inserted != 4 {
		t.Errorf("total, inserted = %d, %d; want 4, 4", total, inserted)
	}
	if n := len(rowsByNativeID(t, st, TypeECSTaskSet)); n != 3 {
		t.Errorf("task-set rows = %d; want 3", n)
	}
	if n := len(rowsByNativeID(t, st, TypeECSClusterCapacityProviderAssociations)); n != 1 {
		t.Errorf("association rows = %d; want 1", n)
	}
}

// A hard error in the association phase is returned beside the task-set
// phase's, which still ran.
func TestScanECSExtended_AssocErrorKeepsTaskSets(t *testing.T) {
	st := newTestStore(t)
	var f ecsTaskSetFixture
	total, inserted, err := scanECSExtendedWith(st, ecsExtendedHandlers(&f, map[string]error{
		"DescribeClusters": apiErr("ServerException", "boom"),
	}))
	if got, want := ecsErrCodes(err), []string{"ClientException", "ServerException"}; !slices.Equal(got, want) {
		t.Errorf("error codes = %v (%v); want %v", got, err, want)
	}
	// One capacity provider plus three task sets.
	if total != 4 || inserted != 4 || len(rowsByNativeID(t, st, TypeECSTaskSet)) != 3 {
		t.Errorf("total, inserted = %d, %d; want 4, 4 with 3 task-set rows", total, inserted)
	}
}

// A failed write stops the phases after it: every later write would fail too.
func TestScanECSExtended_WriteFailureStops(t *testing.T) {
	st := newTestStore(t)
	refuseStoreWrites(t, st, TypeECSCapacityProvider)
	var f ecsTaskSetFixture
	total, inserted, err := scanECSExtendedWith(st, ecsExtendedHandlers(&f, nil))
	if !errors.Is(err, store.ErrStoreWrite) {
		t.Errorf("err = %v; want ErrStoreWrite", err)
	}
	if total != 0 || inserted != 0 || len(f.described) != 0 || countStoredRows(t, st) != 0 {
		t.Errorf("total, inserted, DescribeTaskSets calls, rows = %d, %d, %d, %d; want all 0", total, inserted, len(f.described), countStoredRows(t, st))
	}
}

// scanECS reaches the capacity-provider and task-set phases with the clusters
// it listed.
func TestScanECS_StoresCapacityProvidersAndTaskSets(t *testing.T) {
	st := newTestStore(t)
	var f ecsTaskSetFixture
	h := ecsTopLevelHandlers()
	h["DescribeCapacityProviders"] = ecsCapacityProvidersHandler([][]ecstypes.CapacityProvider{{ecsCPASG}}, nil)
	// The services phase lists without MaxResults, so this listing pins none.
	h["ListServices"] = func(any) (any, error) {
		return &ecs.ListServicesOutput{ServiceArns: []string{ecsTSSvc("svc-c")}}, nil
	}
	h["DescribeTaskSets"] = f.handlers()["DescribeTaskSets"]
	acct := &account{ID: testAccountID, Name: "Test Account", cfg: opStubConfig(h, ecsDenied)}
	if _, _, err := scanECS(context.Background(), acct, testRegion, st, testScanID); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if _, ok := rowsByNativeID(t, st, TypeECSCapacityProvider)[sv(ecsCPASG.CapacityProviderArn)]; !ok {
		t.Error("capacity-provider row missing")
	}
	if _, ok := rowsByNativeID(t, st, TypeECSTaskSet)[sv(ecsTSSecond.TaskSetArn)]; !ok {
		t.Error("task-set row missing")
	}
}

// ecsComputeExtraHandlers serves scanECS one cluster with one container
// instance, and the capacity-provider and task-set fixtures.
// The services phase lists without MaxResults and is served no services; the
// task-set listing is the fixture's.
func ecsComputeExtraHandlers(f *ecsTaskSetFixture) map[string]func(any) (any, error) {
	h := ecsTopLevelHandlers()
	listTaskSetServices := f.handlers()["ListServices"]
	h["ListServices"] = func(in any) (any, error) {
		if in.(*ecs.ListServicesInput).MaxResults == nil {
			return &ecs.ListServicesOutput{}, nil
		}
		return listTaskSetServices(in)
	}
	h["DescribeTaskSets"] = f.handlers()["DescribeTaskSets"]
	h["DescribeCapacityProviders"] = ecsCapacityProvidersHandler([][]ecstypes.CapacityProvider{{ecsCPASG}}, nil)
	h["ListContainerInstances"] = func(any) (any, error) {
		return &ecs.ListContainerInstancesOutput{ContainerInstanceArns: []string{ecsTestARN("container-instance", "cl-1/ci-1")}}, nil
	}
	h["DescribeContainerInstances"] = func(any) (any, error) {
		return &ecs.DescribeContainerInstancesOutput{ContainerInstances: []ecstypes.ContainerInstance{{
			ContainerInstanceArn: sdkaws.String(ecsTestARN("container-instance", "cl-1/ci-1")), Status: sdkaws.String("ACTIVE"),
		}}}, nil
	}
	return h
}

// A task-set error does not cost the extended phases their counts, nor skip
// the compute-extra phase after them; the error is returned.
func TestScanECS_ExtendedErrorKeepsCountsAndComputeExtra(t *testing.T) {
	st := newTestStore(t)
	var f ecsTaskSetFixture
	acct := &account{ID: testAccountID, Name: "Test Account", cfg: opStubConfig(ecsComputeExtraHandlers(&f), ecsDenied)}
	total, inserted, err := scanECS(context.Background(), acct, testRegion, st, testScanID)
	if got, want := ecsErrCodes(err), []string{"ClientException"}; !slices.Equal(got, want) {
		t.Errorf("error codes = %v (%v); want %v (svc-g's)", got, err, want)
	}
	if n := len(rowsByNativeID(t, st, TypeECSTaskSet)); n != 3 {
		t.Errorf("task-set rows = %d; want 3", n)
	}
	if n := len(rowsByNativeID(t, st, TypeECSContainerInstance)); n != 1 {
		t.Errorf("container-instance rows = %d; want 1: compute-extra did not run", n)
	}
	// Setting, cluster, capacity provider, association, 3 task sets, container instance.
	if stored := storedRowCount(t, st); stored != 8 || total != stored || inserted != stored {
		t.Errorf("total, inserted, rows = %d, %d, %d; want all 8", total, inserted, stored)
	}
}

// A hard compute-extra error is returned beside the extended phases' one.
func TestScanECS_ComputeExtraErrorJoinsExtendedError(t *testing.T) {
	st := newTestStore(t)
	var f ecsTaskSetFixture
	h := ecsComputeExtraHandlers(&f)
	h["ListContainerInstances"] = func(any) (any, error) { return nil, apiErr("ServerException", "boom") }
	acct := &account{ID: testAccountID, Name: "Test Account", cfg: opStubConfig(h, ecsDenied)}
	_, _, err := scanECS(context.Background(), acct, testRegion, st, testScanID)
	if got, want := ecsErrCodes(err), []string{"ClientException", "ServerException"}; !slices.Equal(got, want) {
		t.Errorf("error codes = %v (%v); want %v (svc-g's and ListContainerInstances')", got, err, want)
	}
}

// A failed write in the extended phases stops the cluster phases there:
// compute-extra's writes would fail too.
func TestScanECS_ExtendedWriteFailureSkipsComputeExtra(t *testing.T) {
	st := newTestStore(t)
	refuseStoreWrites(t, st, TypeECSCapacityProvider)
	var f ecsTaskSetFixture
	listed := false
	h := ecsComputeExtraHandlers(&f)
	listInstances := h["ListContainerInstances"]
	h["ListContainerInstances"] = func(in any) (any, error) {
		listed = true
		return listInstances(in)
	}
	acct := &account{ID: testAccountID, Name: "Test Account", cfg: opStubConfig(h, ecsDenied)}
	if _, _, err := scanECS(context.Background(), acct, testRegion, st, testScanID); !errors.Is(err, store.ErrStoreWrite) {
		t.Errorf("err = %v; want ErrStoreWrite", err)
	}
	if listed {
		t.Error("ListContainerInstances called after a failed write")
	}
}

// Once ctx ends mid-page no further service is read; the read ones are
// stored and the stop is returned.
func TestScanECSTaskSets_CancelledStopsAndReturns(t *testing.T) {
	st := newTestStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var services []string
	for i := range 12 {
		services = append(services, ecsTSSvc(fmt.Sprintf("svc-%d", i)))
	}
	h := map[string]func(any) (any, error){
		"ListServices": func(any) (any, error) { return &ecs.ListServicesOutput{ServiceArns: services}, nil },
		"DescribeTaskSets": func(in any) (any, error) {
			cancel()
			svc := sdkaws.ToString(in.(*ecs.DescribeTaskSetsInput).Service)
			return &ecs.DescribeTaskSetsOutput{TaskSets: []ecstypes.TaskSet{{TaskSetArn: sdkaws.String(svc + "/ts"), Id: sdkaws.String("ts")}}}, nil
		},
	}
	client := ecs.NewFromConfig(opStubConfig(h, ecsDenied))
	total, inserted, err := scanECSTaskSets(ctx, client, newTestAccount(testAccountID), testRegion, st, testScanID, []string{ecsTestClusterARN})
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v; want context.Canceled", err)
	}
	// Up to fanoutMed reads start before the cancel is seen; the 11th waits
	// for a slot, so at least one service is not read.
	stored := len(rowsByNativeID(t, st, TypeECSTaskSet))
	if stored == 0 || stored >= len(services) || total != stored || inserted != stored {
		t.Errorf("total, inserted, rows = %d, %d, %d; want equal, between 1 and %d", total, inserted, stored, len(services)-1)
	}
}
