package aws

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/emrcontainers"
	emrctypes "github.com/aws/aws-sdk-go-v2/service/emrcontainers/types"
	"github.com/aws/smithy-go"
	"github.com/icearp/disco-cli/store"
)

var (
	emrcDenied    = &smithy.GenericAPIError{Code: "AccessDeniedException", Message: "User: arn:aws:iam::123456789012:user/t is not authorized to perform: emr-containers:List"}
	emrcBadInput  = &smithy.GenericAPIError{Code: "ValidationException", Message: "bad"}
	emrcGone      = &smithy.GenericAPIError{Code: "ResourceNotFoundException", Message: "not found"}
	emrcThrottled = apiErr("ThrottlingException", "Rate exceeded")
	emrcTags      = map[string]string{"team": "emr"}
	emrcCreated   = time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
)

func emrcARN(path string) string {
	return fmt.Sprintf("arn:aws:emr-containers:%s:%s:/%s", testRegion, testAccountID, path)
}

func emrcVCARN(id string) string     { return emrcARN("virtualclusters/" + id) }
func emrcEPARN(vc, id string) string { return emrcVCARN(vc) + "/endpoints/" + id }
func emrcSCARN(id string) string     { return emrcARN("securityconfigurations/" + id) }
func emrcJTARN(id string) string     { return emrcARN("jobtemplates/" + id) }
func emrcName(id string) *string     { return ptrStr("name-" + id) }
func emrcTagsOn(tagged bool) map[string]string {
	if tagged {
		return emrcTags
	}
	return nil
}

// emrcFixture is what the stub lists: every page-one element is tagged, the
// other elements are not, and each listing holds an element with no Arn.
// Virtual cluster vc-1 is RUNNING, vc-2 TERMINATED, and vc-3 has no Id.
type emrcFixture struct {
	vcs [][]emrctypes.VirtualCluster
	eps map[string][][]emrctypes.Endpoint // by virtual cluster id
	scs [][]emrctypes.SecurityConfiguration
	jts [][]emrctypes.JobTemplate
}

func newEMRCFixture() emrcFixture {
	ep := func(vc, id string, state emrctypes.EndpointState, tagged bool) emrctypes.Endpoint {
		return emrctypes.Endpoint{Arn: ptrStr(emrcEPARN(vc, id)), Id: ptrStr(id), Name: emrcName(id), State: state, VirtualClusterId: ptrStr(vc), Tags: emrcTagsOn(tagged)}
	}
	sc := func(id string, tagged bool) emrctypes.SecurityConfiguration {
		return emrctypes.SecurityConfiguration{Arn: ptrStr(emrcSCARN(id)), Id: ptrStr(id), Name: emrcName(id), Tags: emrcTagsOn(tagged)}
	}
	jt := func(id string, tagged bool) emrctypes.JobTemplate {
		return emrctypes.JobTemplate{Arn: ptrStr(emrcJTARN(id)), Id: ptrStr(id), Name: emrcName(id), CreatedAt: &emrcCreated, Tags: emrcTagsOn(tagged)}
	}
	return emrcFixture{
		vcs: [][]emrctypes.VirtualCluster{
			{{Arn: ptrStr(emrcVCARN("vc-1")), Id: ptrStr("vc-1"), Name: emrcName("vc-1"), State: emrctypes.VirtualClusterStateRunning, SecurityConfigurationId: ptrStr("sc-1"), Tags: emrcTags}},
			{
				{Arn: ptrStr(emrcVCARN("vc-2")), Id: ptrStr("vc-2"), Name: emrcName("vc-2"), State: emrctypes.VirtualClusterStateTerminated},
				{Id: ptrStr("vc-noarn")},
				{Arn: ptrStr(emrcVCARN("vc-3")), Name: emrcName("vc-3")},
			},
		},
		eps: map[string][][]emrctypes.Endpoint{
			"vc-1": {{ep("vc-1", "ep-1", emrctypes.EndpointStateActive, true)}, {{Id: ptrStr("ep-noarn")}}},
			"vc-2": {{ep("vc-2", "ep-2", emrctypes.EndpointStateTerminated, false)}},
		},
		scs: [][]emrctypes.SecurityConfiguration{{sc("sc-1", true)}, {sc("sc-2", false), {Id: ptrStr("sc-noarn")}}},
		jts: [][]emrctypes.JobTemplate{{jt("jt-1", true)}, {jt("jt-2", false), {Id: ptrStr("jt-noarn")}}},
	}
}

// emrcServe returns page token ("" or "p2") of pages, failing a call whose
// token names no page, and returning fail[parent+"/"+token] when set.
func emrcServe[Item any](pages [][]Item, parent string, token *string, fail map[string]error) ([]Item, *string, error) {
	if err := fail[parent+"/"+sv(token)]; err != nil {
		return nil, nil, err
	}
	i := 0
	if sv(token) == "p2" {
		i = 1
	} else if token != nil {
		return nil, nil, fmt.Errorf("unexpected NextToken %q", sv(token))
	}
	if i >= len(pages) {
		return nil, nil, fmt.Errorf("parent %q has no page %d", parent, i+1)
	}
	var next *string
	if i+1 < len(pages) {
		next = ptrStr("p2")
	}
	return pages[i], next, nil
}

// emrcPinned fails a call whose input sets anything beyond want, the token
// and parent: no MaxResults, state filter or other filter is sent.
func emrcPinned(got, want any) error {
	if !reflect.DeepEqual(got, want) {
		return fmt.Errorf("input = %+v, want %+v", got, want)
	}
	return nil
}

// emrcHandlers serves f; fail is keyed by op, then by "<parent>/<token>" (the
// parent is the virtual cluster id for ListManagedEndpoints, "" otherwise;
// the token is "" on page one). calls records the clusters ListManagedEndpoints
// was called for, and ops every op called.
func emrcHandlers(f emrcFixture, fail map[string]map[string]error, calls, ops *parentCalls) map[string]func(in any) (any, error) {
	return map[string]func(in any) (any, error){
		"ListVirtualClusters": func(in any) (any, error) {
			p := in.(*emrcontainers.ListVirtualClustersInput)
			ops.add("ListVirtualClusters")
			if err := emrcPinned(p, &emrcontainers.ListVirtualClustersInput{NextToken: p.NextToken}); err != nil {
				return nil, err
			}
			items, next, err := emrcServe(f.vcs, "", p.NextToken, fail["ListVirtualClusters"])
			if err != nil {
				return nil, err
			}
			return &emrcontainers.ListVirtualClustersOutput{VirtualClusters: items, NextToken: next}, nil
		},
		"ListManagedEndpoints": func(in any) (any, error) {
			p := in.(*emrcontainers.ListManagedEndpointsInput)
			ops.add("ListManagedEndpoints")
			if err := emrcPinned(p, &emrcontainers.ListManagedEndpointsInput{VirtualClusterId: p.VirtualClusterId, NextToken: p.NextToken}); err != nil {
				return nil, err
			}
			vc := sv(p.VirtualClusterId)
			calls.add(vc)
			items, next, err := emrcServe(f.eps[vc], vc, p.NextToken, fail["ListManagedEndpoints"])
			if err != nil {
				return nil, err
			}
			return &emrcontainers.ListManagedEndpointsOutput{Endpoints: items, NextToken: next}, nil
		},
		"ListSecurityConfigurations": func(in any) (any, error) {
			p := in.(*emrcontainers.ListSecurityConfigurationsInput)
			ops.add("ListSecurityConfigurations")
			if err := emrcPinned(p, &emrcontainers.ListSecurityConfigurationsInput{NextToken: p.NextToken}); err != nil {
				return nil, err
			}
			items, next, err := emrcServe(f.scs, "", p.NextToken, fail["ListSecurityConfigurations"])
			if err != nil {
				return nil, err
			}
			return &emrcontainers.ListSecurityConfigurationsOutput{SecurityConfigurations: items, NextToken: next}, nil
		},
		"ListJobTemplates": func(in any) (any, error) {
			p := in.(*emrcontainers.ListJobTemplatesInput)
			ops.add("ListJobTemplates")
			if err := emrcPinned(p, &emrcontainers.ListJobTemplatesInput{NextToken: p.NextToken}); err != nil {
				return nil, err
			}
			items, next, err := emrcServe(f.jts, "", p.NextToken, fail["ListJobTemplates"])
			if err != nil {
				return nil, err
			}
			return &emrcontainers.ListJobTemplatesOutput{Templates: items, NextToken: next}, nil
		},
	}
}

// emrcOpLabels maps each stub op to the label its warnings carry.
var emrcOpLabels = map[string]string{
	"ListVirtualClusters":        "emr-containers:ListVirtualClusters",
	"ListManagedEndpoints":       "emr-containers:ListManagedEndpoints",
	"ListSecurityConfigurations": "emr-containers:ListSecurityConfigurations",
	"ListJobTemplates":           "emr-containers:ListJobTemplates",
}

// emrcRun is one scanEMRContainers run against the stub.
type emrcRun struct {
	total, inserted int
	err             error
	warns           []store.ScanWarning
	endpointCalls   []string // distinct virtual cluster ids
	ops             []string // distinct ops called
}

// opWarns is the run's warnings for the stub op op.
func (r emrcRun) opWarns(op string) []store.ScanWarning {
	var out []store.ScanWarning
	for _, w := range r.warns {
		if w.Service == emrcOpLabels[op] {
			out = append(out, w)
		}
	}
	return out
}

func runEMRC(t *testing.T, st *store.Store, fail map[string]map[string]error) emrcRun {
	t.Helper()
	return runEMRCFixture(t, st, newEMRCFixture(), fail)
}

// runEMRCFixture is runEMRC against the stub serving f.
func runEMRCFixture(t *testing.T, st *store.Store, f emrcFixture, fail map[string]map[string]error) emrcRun {
	t.Helper()
	warns := recordWarnings(st)
	var calls, ops parentCalls
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(emrcHandlers(f, fail, &calls, &ops), emrcDenied)
	total, inserted, err := scanEMRContainers(context.Background(), acct, testRegion, st, testScanID)
	return emrcRun{total: total, inserted: inserted, err: err, warns: *warns, endpointCalls: calls.distinct(), ops: ops.distinct()}
}

// assertEMRCWarns fails unless op warned once per key, in order, each scoped
// to the test account and region and naming its key. ScanWarning has no key
// field, so the key is matched inside the message.
func assertEMRCWarns(t *testing.T, r emrcRun, op string, keys ...string) {
	t.Helper()
	got := r.opWarns(op)
	if len(got) != len(keys) {
		t.Fatalf("%s warnings = %+v, want %d naming %v", op, got, len(keys), keys)
	}
	for i, w := range got {
		if w.Scope != testAccountID+"/"+testRegion || !strings.Contains(w.Message, keys[i]) {
			t.Errorf("%s warning %d scope/message = %q/%q, want %q naming %q", op, i, w.Scope, w.Message, testAccountID+"/"+testRegion, keys[i])
		}
	}
}

// emrcAllRows is every NativeID the full fixture stores, per type.
var emrcAllRows = map[string][]string{
	TypeEMRContainersVirtualCluster: {emrcVCARN("vc-1"), emrcVCARN("vc-2"), emrcVCARN("vc-3")},
	TypeEMRContainersEndpoint:       {emrcEPARN("vc-1", "ep-1"), emrcEPARN("vc-2", "ep-2")},
	TypeEMRContainersSecurityConfig: {emrcSCARN("sc-1"), emrcSCARN("sc-2")},
	TypeEMRContainersJobTemplate:    {emrcJTARN("jt-1"), emrcJTARN("jt-2")},
}

// emrcRowsWithout is emrcAllRows less the given types, plus the rows in add.
func emrcRowsWithout(add map[string][]string, typs ...string) map[string][]string {
	out := map[string][]string{}
	for typ, ids := range emrcAllRows {
		if !slices.Contains(typs, typ) {
			out[typ] = ids
		}
	}
	for typ, ids := range add {
		out[typ] = ids
	}
	return out
}

func emrcCount(rows map[string][]string) int {
	n := 0
	for _, ids := range rows {
		n += len(ids)
	}
	return n
}

// assertEMRCRows checks the stored NativeIDs of every type, and that the run
// counted each as total and as inserted (a first scan of an empty store).
func assertEMRCRows(t *testing.T, st *store.Store, r emrcRun, want map[string][]string) {
	t.Helper()
	for _, typ := range []string{TypeEMRContainersVirtualCluster, TypeEMRContainersEndpoint, TypeEMRContainersSecurityConfig, TypeEMRContainersJobTemplate} {
		got := nativeIDs(rowsByNativeID(t, st, typ))
		w := slices.Clone(want[typ])
		slices.Sort(w)
		if !slices.Equal(got, w) {
			t.Errorf("%s rows = %v, want %v", typ, got, w)
		}
	}
	if n := emrcCount(want); r.total != n || r.inserted != n {
		t.Errorf("total/inserted = %d/%d, want %d/%d", r.total, r.inserted, n, n)
	}
}

// assertEMRCRow checks a stored row's name, status (nil for none),
// attributes and tags (nil wantTags: no tags stored).
func assertEMRCRow(t *testing.T, row store.Resource, name, status *string, attrs any, wantTags map[string]string) {
	t.Helper()
	if sv(row.Name) != sv(name) || sv(row.Region) != testRegion || row.AttributesJSON != mustJSON(attrs) {
		t.Errorf("%s: name/region = %q/%q, attrs = %s; want %q/%q, %s", row.NativeID, sv(row.Name), sv(row.Region), row.AttributesJSON, sv(name), testRegion, mustJSON(attrs))
	}
	if (status == nil) != (row.Status == nil) || sv(row.Status) != sv(status) {
		t.Errorf("%s: status = %v, want %v", row.NativeID, row.Status, status)
	}
	if wantTags == nil {
		if row.TagsJSON != nil {
			t.Errorf("%s: tags = %s, want none", row.NativeID, *row.TagsJSON)
		}
		return
	}
	assertTagsMap(t, row.TagsJSON, wantTags)
}

func TestScanEMRContainers_StoresEveryTypeWithTags(t *testing.T) {
	st := newTestStore(t)
	r := runEMRC(t, st, nil)
	if r.err != nil {
		t.Fatalf("scanEMRContainers: %v", r.err)
	}
	assertEMRCRows(t, st, r, emrcAllRows)

	f := newEMRCFixture()
	vcs := rowsByNativeID(t, st, TypeEMRContainersVirtualCluster)
	assertEMRCRow(t, vcs[emrcVCARN("vc-1")], ptrStr("name-vc-1"), ptrStr("RUNNING"), f.vcs[0][0], emrcTags)
	assertEMRCRow(t, vcs[emrcVCARN("vc-2")], ptrStr("name-vc-2"), ptrStr("TERMINATED"), f.vcs[1][0], nil)
	assertEMRCRow(t, vcs[emrcVCARN("vc-3")], ptrStr("name-vc-3"), nil, f.vcs[1][2], nil)
	eps := rowsByNativeID(t, st, TypeEMRContainersEndpoint)
	assertEMRCRow(t, eps[emrcEPARN("vc-1", "ep-1")], ptrStr("name-ep-1"), ptrStr("ACTIVE"), f.eps["vc-1"][0][0], emrcTags)
	assertEMRCRow(t, eps[emrcEPARN("vc-2", "ep-2")], ptrStr("name-ep-2"), ptrStr("TERMINATED"), f.eps["vc-2"][0][0], nil)
	scs := rowsByNativeID(t, st, TypeEMRContainersSecurityConfig)
	assertEMRCRow(t, scs[emrcSCARN("sc-1")], ptrStr("name-sc-1"), nil, f.scs[0][0], emrcTags)
	assertEMRCRow(t, scs[emrcSCARN("sc-2")], ptrStr("name-sc-2"), nil, f.scs[1][0], nil)
	jts := rowsByNativeID(t, st, TypeEMRContainersJobTemplate)
	assertEMRCRow(t, jts[emrcJTARN("jt-1")], ptrStr("name-jt-1"), nil, f.jts[0][0], emrcTags)
	assertEMRCRow(t, jts[emrcJTARN("jt-2")], ptrStr("name-jt-2"), nil, f.jts[1][0], nil)
	if got := sv(jts[emrcJTARN("jt-1")].CreatedAt); got != "2026-01-02T03:04:05Z" {
		t.Errorf("jt-1 CreatedAt = %q, want 2026-01-02T03:04:05Z", got)
	}

	// One warning each for the element with no Arn, and for vc-3, stored with
	// no Id, whose endpoints were not read.
	assertEMRCWarns(t, r, "ListVirtualClusters", "vc-noarn", emrcVCARN("vc-3"))
	assertEMRCWarns(t, r, "ListManagedEndpoints", "ep-noarn")
	assertEMRCWarns(t, r, "ListSecurityConfigurations", "sc-noarn")
	assertEMRCWarns(t, r, "ListJobTemplates", "jt-noarn")
	if want := []string{"vc-1", "vc-2"}; !slices.Equal(r.endpointCalls, want) {
		t.Errorf("ListManagedEndpoints called for %v, want %v", r.endpointCalls, want)
	}
}

// emrcPageOneRows is what the full fixture stores when an account-wide
// listing stops after page one.
var emrcPageOneRows = map[string]map[string][]string{
	"ListVirtualClusters": emrcRowsWithout(map[string][]string{
		TypeEMRContainersVirtualCluster: {emrcVCARN("vc-1")},
		TypeEMRContainersEndpoint:       {emrcEPARN("vc-1", "ep-1")},
	}),
	"ListSecurityConfigurations": emrcRowsWithout(map[string][]string{TypeEMRContainersSecurityConfig: {emrcSCARN("sc-1")}}),
	"ListJobTemplates":           emrcRowsWithout(map[string][]string{TypeEMRContainersJobTemplate: {emrcJTARN("jt-1")}}),
}

// A failed page two keeps the rows read before it, the error is returned, and
// every later phase still runs, the endpoint phase over the clusters stored.
func TestScanEMRContainers_FailedPageKeepsRowsAndRunsLaterPhases(t *testing.T) {
	for _, tc := range []struct {
		op, parent string
		want       map[string][]string
	}{
		{"ListVirtualClusters", "", emrcPageOneRows["ListVirtualClusters"]},
		{"ListManagedEndpoints", "vc-1", emrcAllRows},
		{"ListSecurityConfigurations", "", emrcPageOneRows["ListSecurityConfigurations"]},
		{"ListJobTemplates", "", emrcPageOneRows["ListJobTemplates"]},
	} {
		t.Run(tc.op, func(t *testing.T) {
			st := newTestStore(t)
			r := runEMRC(t, st, map[string]map[string]error{tc.op: {tc.parent + "/p2": emrcBadInput}})
			if classifyServiceError(r.err) != outcomeError || !isAPIErrorCode(r.err, "ValidationException") {
				t.Fatalf("err = %v, want a ValidationException error", r.err)
			}
			assertEMRCRows(t, st, r, tc.want)
		})
	}
}

// A denial warns once for the op, naming the first denied cluster when the op
// fans out over clusters (an account-wide listing has no key to name), and is
// not an error; the other phases keep their rows.
func TestScanEMRContainers_DeniedWarnsOnce(t *testing.T) {
	for _, tc := range []struct {
		op      string
		parents []string
		key     string
		want    map[string][]string
	}{
		{"ListVirtualClusters", []string{""}, "", emrcRowsWithout(nil, TypeEMRContainersVirtualCluster, TypeEMRContainersEndpoint)},
		{"ListManagedEndpoints", []string{"vc-1", "vc-2"}, "first vc-1", emrcRowsWithout(nil, TypeEMRContainersEndpoint)},
		{"ListSecurityConfigurations", []string{""}, "", emrcRowsWithout(nil, TypeEMRContainersSecurityConfig)},
		{"ListJobTemplates", []string{""}, "", emrcRowsWithout(nil, TypeEMRContainersJobTemplate)},
	} {
		t.Run(tc.op, func(t *testing.T) {
			st := newTestStore(t)
			fail := map[string]error{}
			for _, p := range tc.parents {
				fail[p+"/"] = emrcDenied
			}
			r := runEMRC(t, st, map[string]map[string]error{tc.op: fail})
			if r.err != nil {
				t.Fatalf("err = %v, want nil", r.err)
			}
			// The element with no Arn on the denied listing's page two is never read.
			assertEMRCWarns(t, r, tc.op, tc.key)
			assertEMRCRows(t, st, r, tc.want)
		})
	}
}

// ValidationException for vc-2 in a state other than RUNNING skips it with
// one warning naming it and keeps vc-1's endpoints; for a RUNNING vc-2 it
// fails the phase.
func TestScanEMRContainers_EndpointsValidationByClusterState(t *testing.T) {
	for _, tc := range []struct {
		state emrctypes.VirtualClusterState
		skip  bool
	}{
		{emrctypes.VirtualClusterStateTerminating, true},
		{emrctypes.VirtualClusterStateTerminated, true},
		{emrctypes.VirtualClusterStateArrested, true},
		{emrctypes.VirtualClusterStateRunning, false},
	} {
		t.Run(string(tc.state), func(t *testing.T) {
			st := newTestStore(t)
			f := newEMRCFixture()
			f.vcs[1][0].State = tc.state
			r := runEMRCFixture(t, st, f, map[string]map[string]error{"ListManagedEndpoints": {"vc-2/": emrcBadInput}})
			if tc.skip {
				if r.err != nil {
					t.Fatalf("err = %v, want nil", r.err)
				}
				assertEMRCWarns(t, r, "ListManagedEndpoints", "ep-noarn", "first vc-2")
			} else {
				if classifyServiceError(r.err) != outcomeError || !isAPIErrorCode(r.err, "ValidationException") {
					t.Fatalf("err = %v, want a ValidationException error", r.err)
				}
				assertEMRCWarns(t, r, "ListManagedEndpoints", "ep-noarn")
			}
			assertEMRCRows(t, st, r, emrcRowsWithout(map[string][]string{TypeEMRContainersEndpoint: {emrcEPARN("vc-1", "ep-1")}}))
		})
	}
}

// A throttled endpoint page is not skipped: alone it is returned, leaving the
// scan transient with the rows read before it kept; beside another phase's
// hard error it is warned about once and the hard error is returned.
func TestScanEMRContainers_EndpointsTransient(t *testing.T) {
	for _, tc := range []struct {
		name      string
		hard      bool
		want      serviceOutcome
		wantWarns int
	}{
		{"alone", false, outcomeTransient, 0},
		{"beside a hard error", true, outcomeError, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			fail := map[string]map[string]error{"ListManagedEndpoints": {"vc-1/p2": emrcThrottled}}
			want := emrcAllRows
			if tc.hard {
				fail["ListJobTemplates"] = map[string]error{"/p2": emrcBadInput}
				want = emrcPageOneRows["ListJobTemplates"]
			}
			r := runEMRC(t, st, fail)
			if got := classifyServiceError(r.err); got != tc.want {
				t.Errorf("classifyServiceError(%v) = %v, want %v", r.err, got, tc.want)
			}
			if tc.hard && !isAPIErrorCode(r.err, "ValidationException") {
				t.Errorf("err = %v, want the ListJobTemplates ValidationException", r.err)
			}
			// vc-1's page two, with the element warned about for no Arn, is not read.
			if got := r.opWarns("ListManagedEndpoints"); len(got) != tc.wantWarns {
				t.Errorf("ListManagedEndpoints warnings = %+v, want %d", got, tc.wantWarns)
			}
			assertEMRCRows(t, st, r, want)
		})
	}
}

// ListManagedEndpoints models no not-found code, so ResourceNotFoundException
// for a cluster fails the phase rather than skipping the cluster.
func TestScanEMRContainers_EndpointsNotFoundIsAnError(t *testing.T) {
	st := newTestStore(t)
	r := runEMRC(t, st, map[string]map[string]error{"ListManagedEndpoints": {"vc-2/": emrcGone}})
	if classifyServiceError(r.err) != outcomeError || !isAPIErrorCode(r.err, "ResourceNotFoundException") {
		t.Fatalf("err = %v, want a ResourceNotFoundException error", r.err)
	}
	assertEMRCRows(t, st, r, emrcRowsWithout(map[string][]string{TypeEMRContainersEndpoint: {emrcEPARN("vc-1", "ep-1")}}))
}

// A throttled page alone leaves the scan transient with the rows read before
// it stored and counted; beside another phase's hard error it is warned about
// once and the scan reads as an error.
func TestScanEMRContainers_TransientPhaseBesideHardError(t *testing.T) {
	for _, op := range []string{"ListVirtualClusters", "ListSecurityConfigurations", "ListJobTemplates"} {
		for _, tc := range []struct {
			name      string
			hard      bool
			want      serviceOutcome
			wantWarns int
		}{
			{"alone", false, outcomeTransient, 0},
			{"beside a hard error", true, outcomeError, 1},
		} {
			t.Run(op+"/"+tc.name, func(t *testing.T) {
				st := newTestStore(t)
				fail := map[string]map[string]error{op: {"/p2": emrcThrottled}}
				want := emrcPageOneRows[op]
				if tc.hard {
					// vc-1 is listed on page one in every case.
					fail["ListManagedEndpoints"] = map[string]error{"vc-1/": emrcBadInput}
					want = maps.Clone(want)
					want[TypeEMRContainersEndpoint] = slices.DeleteFunc(slices.Clone(want[TypeEMRContainersEndpoint]),
						func(id string) bool { return id == emrcEPARN("vc-1", "ep-1") })
				}
				r := runEMRC(t, st, fail)
				if got := classifyServiceError(r.err); got != tc.want {
					t.Errorf("classifyServiceError(%v) = %v, want %v", r.err, got, tc.want)
				}
				// Page two, with the elements warned about, is not read.
				if got := len(r.opWarns(op)); got != tc.wantWarns {
					t.Errorf("%s warnings = %d, want %d", op, got, tc.wantWarns)
				}
				assertEMRCRows(t, st, r, want)
			})
		}
	}
}

// A failed endpoint write stops the scan with no warning claiming the lost
// rows or the skipped cluster.
func TestScanEMRContainers_EndpointWriteFailureStops(t *testing.T) {
	st := newTestStore(t)
	refuseStoreWrites(t, st, TypeEMRContainersEndpoint)
	r := runEMRC(t, st, map[string]map[string]error{"ListManagedEndpoints": {"vc-2/": emrcBadInput}})
	if !errors.Is(r.err, store.ErrStoreWrite) {
		t.Fatalf("err = %v; want the store write failure", r.err)
	}
	assertEMRCWarns(t, r, "ListManagedEndpoints")
	if want := []string{"ListManagedEndpoints", "ListVirtualClusters"}; !slices.Equal(r.ops, want) {
		t.Errorf("ops called = %v; want only %v", r.ops, want)
	}
}

// A failed virtual-cluster write stops the scan with no warning claiming the
// lost rows, and no later phase runs.
func TestScanEMRContainers_VirtualClusterWriteFailureStops(t *testing.T) {
	st := newTestStore(t)
	refuseStoreWrites(t, st, TypeEMRContainersVirtualCluster)
	r := runEMRC(t, st, nil)
	if !errors.Is(r.err, store.ErrStoreWrite) {
		t.Fatalf("err = %v; want the store write failure", r.err)
	}
	if got := r.opWarns("ListVirtualClusters"); len(got) != 0 {
		t.Errorf("ListVirtualClusters warnings = %+v; want none after a failed write", got)
	}
	if want := []string{"ListVirtualClusters"}; !slices.Equal(r.ops, want) {
		t.Errorf("ops called = %v; want only %v", r.ops, want)
	}
	if r.total != 0 || r.inserted != 0 {
		t.Errorf("total/inserted = %d/%d, want 0/0", r.total, r.inserted)
	}
}
