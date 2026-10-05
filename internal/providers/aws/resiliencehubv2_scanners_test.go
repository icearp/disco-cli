package aws

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"sync"
	"testing"
	"time"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/resiliencehub"
	rhtypes "github.com/aws/aws-sdk-go-v2/service/resiliencehub/types"
	"github.com/aws/aws-sdk-go-v2/service/resiliencehubv2"
	rhv2types "github.com/aws/aws-sdk-go-v2/service/resiliencehubv2/types"
	"github.com/icearp/disco-cli/internal/util"
	"github.com/icearp/disco-cli/store"
)

var rhCreated = time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)

const (
	rhName         = "nm"
	rhOtherAccount = "999999999999"
	rhOtherRegion  = "eu-west-1"
)

func rhARN(account, kind, id string) string {
	return rhARNIn(testRegion, account, kind, id)
}

func rhARNIn(region, account, kind, id string) string {
	return "arn:aws:resiliencehub:" + region + ":" + account + ":" + kind + "/" + id
}

// rhV2Stub serves canned pages keyed by "<Op>" (parent ops) or
// "<Op>/<parentARN>" (child ops), then by NextToken ("" = first page).
type rhV2Stub struct {
	mu    sync.Mutex
	pages map[string]map[string]any
	errs  map[string]error
	calls []string // first-page calls, keyed like pages
	// pageErrs fails a key's call for one NextToken, after earlier pages succeed.
	pageErrs map[string]map[string]error
}

func (s *rhV2Stub) page(key string, token *string) (any, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if token == nil {
		s.calls = append(s.calls, key)
	}
	if err := s.pageErrs[key][sdkaws.ToString(token)]; err != nil {
		return nil, err
	}
	if err := s.errs[key]; err != nil {
		return nil, err
	}
	return s.pages[key][sdkaws.ToString(token)], nil
}

func rhStubOut[T any](out any, err error) (*T, error) {
	if err != nil {
		return nil, err
	}
	if out == nil {
		return new(T), nil
	}
	return out.(*T), nil
}

func (s *rhV2Stub) ListPolicies(_ context.Context, in *resiliencehubv2.ListPoliciesInput, _ ...func(*resiliencehubv2.Options)) (*resiliencehubv2.ListPoliciesOutput, error) {
	return rhStubOut[resiliencehubv2.ListPoliciesOutput](s.page("ListPolicies", in.NextToken))
}

func (s *rhV2Stub) ListServices(_ context.Context, in *resiliencehubv2.ListServicesInput, _ ...func(*resiliencehubv2.Options)) (*resiliencehubv2.ListServicesOutput, error) {
	return rhStubOut[resiliencehubv2.ListServicesOutput](s.page("ListServices", in.NextToken))
}

func (s *rhV2Stub) ListSystems(_ context.Context, in *resiliencehubv2.ListSystemsInput, _ ...func(*resiliencehubv2.Options)) (*resiliencehubv2.ListSystemsOutput, error) {
	return rhStubOut[resiliencehubv2.ListSystemsOutput](s.page("ListSystems", in.NextToken))
}

func (s *rhV2Stub) ListTests(_ context.Context, in *resiliencehubv2.ListTestsInput, _ ...func(*resiliencehubv2.Options)) (*resiliencehubv2.ListTestsOutput, error) {
	return rhStubOut[resiliencehubv2.ListTestsOutput](s.page("ListTests/"+sdkaws.ToString(in.ServiceArn), in.NextToken))
}

func (s *rhV2Stub) ListUserJourneys(_ context.Context, in *resiliencehubv2.ListUserJourneysInput, _ ...func(*resiliencehubv2.Options)) (*resiliencehubv2.ListUserJourneysOutput, error) {
	return rhStubOut[resiliencehubv2.ListUserJourneysOutput](s.page("ListUserJourneys/"+sdkaws.ToString(in.SystemArn), in.NextToken))
}

// rhWant is one expected stored row. idKey/idVal name a field of the SDK
// element AttributesJSON must carry verbatim; "" name/status means unset.
type rhWant struct {
	nativeID, typ, name, status, idKey, idVal string
}

func assertRHRow(t *testing.T, rows map[string]store.Resource, w rhWant) {
	t.Helper()
	r, ok := rows[w.nativeID]
	if !ok {
		t.Errorf("missing row %s; have %d rows", w.nativeID, len(rows))
		return
	}
	if r.Type != w.typ {
		t.Errorf("%s: Type = %q; want %q", w.nativeID, r.Type, w.typ)
	}
	if got := sdkaws.ToString(r.Region); got != testRegion {
		t.Errorf("%s: Region = %q; want %q", w.nativeID, got, testRegion)
	}
	if w.name == "" && r.Name != nil {
		t.Errorf("%s: Name = %q; want unset", w.nativeID, *r.Name)
	}
	if got := sdkaws.ToString(r.Name); got != w.name {
		t.Errorf("%s: Name = %q; want %q", w.nativeID, got, w.name)
	}
	if w.status == "" && r.Status != nil {
		t.Errorf("%s: Status = %q; want unset", w.nativeID, *r.Status)
	}
	if got := sdkaws.ToString(r.Status); got != w.status {
		t.Errorf("%s: Status = %q; want %q", w.nativeID, got, w.status)
	}
	if got, want := sdkaws.ToString(r.CreatedAt), sdkaws.ToString(util.TimeRFC3339(&rhCreated)); got != want {
		t.Errorf("%s: CreatedAt = %q; want %q", w.nativeID, got, want)
	}
	var attrs map[string]any
	if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
		t.Fatalf("%s: AttributesJSON does not decode: %v", w.nativeID, err)
	}
	if got := attrs[w.idKey]; got != w.idVal {
		t.Errorf("%s: attributes[%q] = %v; want %q (the SDK element verbatim)", w.nativeID, w.idKey, got, w.idVal)
	}
}

func rhWarnRecorder(st *store.Store) func() []store.ScanWarning {
	var (
		mu    sync.Mutex
		warns []store.ScanWarning
	)
	st.OnWarn = func(w store.ScanWarning) {
		mu.Lock()
		warns = append(warns, w)
		mu.Unlock()
	}
	return func() []store.ScanWarning {
		mu.Lock()
		defer mu.Unlock()
		return slices.Clone(warns)
	}
}

type rhParentCase struct {
	name, op, typ, kind, idKey, status string
	page                               func(arns []string, next *string) any
	scan                               func(context.Context, resilienceHubV2API, *account, string, *store.Store, string) ([]string, int, int, error)
}

func rhParentCases() []rhParentCase {
	return []rhParentCase{
		{
			name: "Policies", op: "ListPolicies", typ: TypeResilienceHubPolicy, kind: "policy", idKey: "PolicyArn",
			page: func(arns []string, next *string) any {
				out := &resiliencehubv2.ListPoliciesOutput{NextToken: next}
				for _, a := range arns {
					out.PolicySummaries = append(out.PolicySummaries, rhv2types.PolicySummary{PolicyArn: sdkaws.String(a), Name: sdkaws.String(rhName), CreatedAt: &rhCreated})
				}
				return out
			},
			scan: func(ctx context.Context, c resilienceHubV2API, acct *account, region string, st *store.Store, scanID string) ([]string, int, int, error) {
				_, t, i, err := scanRHPolicies(ctx, c, acct, region, st, scanID)
				return nil, t, i, err
			},
		},
		{
			name: "Services", op: "ListServices", typ: TypeResilienceHubService, kind: "service", idKey: "ServiceArn", status: "SUCCESS",
			page: func(arns []string, next *string) any {
				out := &resiliencehubv2.ListServicesOutput{NextToken: next}
				for _, a := range arns {
					out.ServiceSummaries = append(out.ServiceSummaries, rhv2types.ServiceSummary{
						ServiceArn: sdkaws.String(a), Name: sdkaws.String(rhName), CreatedAt: &rhCreated,
						AssessmentStatus: rhv2types.AssessmentStatusSuccess,
					})
				}
				return out
			},
			scan: scanRHServices,
		},
		{
			name: "Systems", op: "ListSystems", typ: TypeResilienceHubSystem, kind: "system", idKey: "SystemArn",
			page: func(arns []string, next *string) any {
				out := &resiliencehubv2.ListSystemsOutput{NextToken: next}
				for _, a := range arns {
					out.SystemSummaries = append(out.SystemSummaries, rhv2types.SystemSummary{
						SystemArn: sdkaws.String(a), SystemId: sdkaws.String("sys-id"), Name: sdkaws.String(rhName), CreatedAt: &rhCreated,
					})
				}
				return out
			},
			scan: scanRHSystems,
		},
	}
}

func (c rhParentCase) returnsParents() bool { return c.typ != TypeResilienceHubPolicy }

// Two pages are both stored; elements owned by another account, homed in
// another region, or with no ARN are skipped. Services and systems hand their
// stored ARNs on as fan-out parents.
func TestRHV2ParentScanners_PaginatesHomedRowsOnly(t *testing.T) {
	for _, c := range rhParentCases() {
		t.Run(c.name, func(t *testing.T) {
			own1, own2 := rhARN(testAccountID, c.kind, "a"), rhARN(testAccountID, c.kind, "b")
			other := rhARN(rhOtherAccount, c.kind, "c")
			elsewhere := rhARNIn(rhOtherRegion, testAccountID, c.kind, "d")
			stub := &rhV2Stub{pages: map[string]map[string]any{c.op: {
				"":   c.page([]string{own1, other}, sdkaws.String("t2")),
				"t2": c.page([]string{own2, "", elsewhere}, nil),
			}}}
			st := newTestStore(t)
			parents, total, _, err := c.scan(context.Background(), stub, newTestAccount(testAccountID), testRegion, st, testScanID)
			if err != nil {
				t.Fatalf("scan: %v", err)
			}
			rows := listNetExtraRows(t, st, c.typ)
			if total != 2 || len(rows) != 2 {
				t.Fatalf("total = %d, rows = %d; want 2, 2 (other-account, other-region and ARN-less elements skipped)", total, len(rows))
			}
			for _, skipped := range []string{other, elsewhere} {
				if _, ok := rows[skipped]; ok {
					t.Errorf("row %s not homed in the scanned account and region was stored", skipped)
				}
			}
			for _, a := range []string{own1, own2} {
				assertRHRow(t, rows, rhWant{nativeID: a, typ: c.typ, name: rhName, status: c.status, idKey: c.idKey, idVal: a})
			}
			var want []string
			if c.returnsParents() {
				want = []string{own1, own2}
			}
			if !slices.Equal(parents, want) {
				t.Errorf("returned parents = %v; want %v", parents, want)
			}
		})
	}
}

func TestRHV2ParentScanners_Empty(t *testing.T) {
	for _, c := range rhParentCases() {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			parents, total, _, err := c.scan(context.Background(), &rhV2Stub{}, newTestAccount(testAccountID), testRegion, st, testScanID)
			if err != nil || total != 0 || len(parents) != 0 || len(warns()) != 0 {
				t.Errorf("got (parents=%v, total=%d, err=%v, warns=%d); want none, 0, nil, 0", parents, total, err, len(warns()))
			}
		})
	}
}

func TestRHV2ParentScanners_AccessDeniedWarns(t *testing.T) {
	for _, c := range rhParentCases() {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			stub := &rhV2Stub{errs: map[string]error{c.op: apiErr("AccessDeniedException", "User: arn:aws:iam::1:user/u is not authorized to perform: x")}}
			parents, total, _, err := c.scan(context.Background(), stub, newTestAccount(testAccountID), testRegion, st, testScanID)
			if err != nil || total != 0 || len(parents) != 0 {
				t.Fatalf("got (parents=%v, total=%d, err=%v); want none, 0, nil", parents, total, err)
			}
			if w := warns(); len(w) != 1 || w[0].Service != "resiliencehub:"+c.op {
				t.Errorf("warnings = %+v; want exactly one for resiliencehub:%s", w, c.op)
			}
		})
	}
}

func TestRHV2ParentScanners_OtherErrorPropagates(t *testing.T) {
	for _, c := range rhParentCases() {
		t.Run(c.name, func(t *testing.T) {
			stub := &rhV2Stub{errs: map[string]error{c.op: apiErr("ValidationException", "bad")}}
			_, _, _, err := c.scan(context.Background(), stub, newTestAccount(testAccountID), testRegion, newTestStore(t), testScanID)
			if !isAPIErrorCode(err, "ValidationException") {
				t.Errorf("err = %v; want the ValidationException propagated", err)
			}
		})
	}
}

// An unrouted v2 call in a region where only v1 is deployed skips the whole
// v2 pass silently: no error, no warning, no further v2 calls.
func TestScanResilienceHubV2_NotDeployedSkipsV2(t *testing.T) {
	for name, gap := range map[string]error{
		"UnknownOperationException": apiErr("UnknownOperationException", ""),
		"NotFoundException":         apiErr("NotFoundException", "Unable to determine service/operation name to be authorized"),
		"HTTP404":                   respErrCode(404),
	} {
		t.Run(name, func(t *testing.T) {
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			stub := &rhV2Stub{errs: map[string]error{"ListPolicies": gap}}
			total, _, err := scanResilienceHubV2(context.Background(), stub, newTestAccount(testAccountID), testRegion, st, testScanID)
			if err != nil || total != 0 || len(warns()) != 0 {
				t.Fatalf("got (total=%d, err=%v, warns=%d); want 0, nil, 0", total, err, len(warns()))
			}
			if !slices.Equal(stub.calls, []string{"ListPolicies"}) {
				t.Errorf("calls = %v; want only ListPolicies", stub.calls)
			}
		})
	}
}

// Once a ListPolicies page has succeeded the v2 API is deployed, so a
// not-deployed shape on a later page is a real failure, not a skip.
func TestRHV2Policies_NotDeployedShapeAfterFirstPageErrors(t *testing.T) {
	policyARN := rhARN(testAccountID, "policy", "a")
	c := rhParentCases()[0]
	stub := &rhV2Stub{
		pages:    map[string]map[string]any{"ListPolicies": {"": c.page([]string{policyARN}, sdkaws.String("t2"))}},
		pageErrs: map[string]map[string]error{"ListPolicies": {"t2": apiErr("UnknownOperationException", "")}},
	}
	available, _, _, err := scanRHPolicies(context.Background(), stub, newTestAccount(testAccountID), testRegion, newTestStore(t), testScanID)
	if !isAPIErrorCode(err, "UnknownOperationException") {
		t.Errorf("err = %v; want the page-2 UnknownOperationException returned", err)
	}
	if !available {
		t.Error("available = false; want true (page 1 proved the API is deployed)")
	}
}

type rhChildCase struct {
	name, op, typ, parentType, parentKind, kind, idKey, childName string
	page                                                          func(parentARN string, ids []string, next *string) any
	scan                                                          func(context.Context, resilienceHubV2API, *account, string, *store.Store, string, []string) (int, int, error)
}

func rhChildCases() []rhChildCase {
	return []rhChildCase{
		{
			name: "Tests", op: "ListTests", typ: TypeResilienceHubTest, parentType: TypeResilienceHubService,
			parentKind: "service", kind: "test", idKey: "TestId",
			page: func(parentARN string, ids []string, next *string) any {
				out := &resiliencehubv2.ListTestsOutput{NextToken: next}
				for _, id := range ids {
					out.Tests = append(out.Tests, rhv2types.TestSummary{
						TestId: sdkaws.String(id), ServiceArn: sdkaws.String(parentARN), CreationTime: &rhCreated,
					})
				}
				return out
			},
			scan: scanRHTests,
		},
		{
			name: "UserJourneys", op: "ListUserJourneys", typ: TypeResilienceHubUserJourney, parentType: TypeResilienceHubSystem,
			parentKind: "system", kind: "user-journey", idKey: "UserJourneyId", childName: rhName,
			page: func(_ string, ids []string, next *string) any {
				out := &resiliencehubv2.ListUserJourneysOutput{NextToken: next}
				for _, id := range ids {
					out.UserJourneySummaries = append(out.UserJourneySummaries, rhv2types.UserJourneySummary{
						UserJourneyId: sdkaws.String(id), Name: sdkaws.String(rhName), CreatedAt: &rhCreated,
					})
				}
				return out
			},
			scan: scanRHUserJourneys,
		},
	}
}

func (c rhChildCase) parent(id string) string { return rhARN(testAccountID, c.parentKind, id) }

// runRHChild seeds the parent rows (as the parent phase does in production,
// so the contains closure can link children) and runs the child scanner.
func runRHChild(t *testing.T, c rhChildCase, stub *rhV2Stub, parents []string) (int, map[string]store.Resource, []store.ScanWarning, *store.Store) {
	t.Helper()
	st := newTestStore(t)
	for _, p := range parents {
		upsertTestResource(t, st, "aws", testAccountID, c.parentType, p, testRegion, "{}")
	}
	warns := rhWarnRecorder(st)
	total, _, err := c.scan(context.Background(), stub, newTestAccount(testAccountID), testRegion, st, testScanID, parents)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	return total, listNetExtraRows(t, st, c.typ), warns(), st
}

func assertRHChild(t *testing.T, st *store.Store, rows map[string]store.Resource, c rhChildCase, parentARN, id string) {
	t.Helper()
	nativeID := parentARN + "/" + c.kind + "/" + id
	assertRHRow(t, rows, rhWant{nativeID: nativeID, typ: c.typ, name: c.childName, idKey: c.idKey, idVal: id})
	rels, err := st.RelationshipsFrom(store.ResourceID("aws", testAccountID, parentARN), store.RelContains)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	childID := store.ResourceID("aws", testAccountID, nativeID)
	if !slices.ContainsFunc(rels, func(rel store.Relationship) bool { return rel.ToID == childID }) {
		t.Errorf("%s: no contains edge from parent %s", nativeID, parentARN)
	}
}

func TestRHV2ChildScanners_PaginatesEveryParent(t *testing.T) {
	for _, c := range rhChildCases() {
		t.Run(c.name, func(t *testing.T) {
			p1, p2 := c.parent("p1"), c.parent("p2")
			stub := &rhV2Stub{pages: map[string]map[string]any{
				c.op + "/" + p1: {"": c.page(p1, []string{"1"}, sdkaws.String("t2")), "t2": c.page(p1, []string{"2", ""}, nil)},
				c.op + "/" + p2: {"": c.page(p2, []string{"3"}, nil)},
			}}
			total, rows, _, st := runRHChild(t, c, stub, []string{p1, p2})
			if total != 3 || len(rows) != 3 {
				t.Fatalf("total = %d, rows = %d; want 3, 3 (the id-less child is skipped)", total, len(rows))
			}
			assertRHChild(t, st, rows, c, p1, "1")
			assertRHChild(t, st, rows, c, p1, "2")
			assertRHChild(t, st, rows, c, p2, "3")
			want := []string{c.op + "/" + p1, c.op + "/" + p2}
			if got := slices.Sorted(slices.Values(stub.calls)); !slices.Equal(got, want) {
				t.Errorf("first-page calls = %v; want %v (every parent)", got, want)
			}
		})
	}
}

func TestRHV2ChildScanners_Empty(t *testing.T) {
	for _, c := range rhChildCases() {
		t.Run(c.name, func(t *testing.T) {
			total, rows, warns, _ := runRHChild(t, c, &rhV2Stub{}, []string{c.parent("p1")})
			if total != 0 || len(rows) != 0 || len(warns) != 0 {
				t.Errorf("total = %d, rows = %d, warns = %d; want 0, 0, 0", total, len(rows), len(warns))
			}
		})
	}
}

func TestRHV2ChildScanners_NoParentsMakesNoCalls(t *testing.T) {
	for _, c := range rhChildCases() {
		t.Run(c.name, func(t *testing.T) {
			stub := &rhV2Stub{}
			if total, _, _, _ := runRHChild(t, c, stub, nil); total != 0 {
				t.Errorf("total = %d; want 0", total)
			}
			if len(stub.calls) != 0 {
				t.Errorf("calls = %v; want none", stub.calls)
			}
		})
	}
}

func TestRHV2ChildScanners_ParentNotFoundSkipped(t *testing.T) {
	for _, c := range rhChildCases() {
		t.Run(c.name, func(t *testing.T) {
			p1, p2 := c.parent("p1"), c.parent("p2")
			stub := &rhV2Stub{
				errs:  map[string]error{c.op + "/" + p1: apiErr("ResourceNotFoundException", "gone")},
				pages: map[string]map[string]any{c.op + "/" + p2: {"": c.page(p2, []string{"3"}, nil)}},
			}
			total, rows, warns, st := runRHChild(t, c, stub, []string{p1, p2})
			if total != 1 || len(rows) != 1 || len(warns) != 0 {
				t.Fatalf("total = %d, rows = %d, warns = %d; want 1, 1, 0", total, len(rows), len(warns))
			}
			assertRHChild(t, st, rows, c, p2, "3")
		})
	}
}

func TestRHV2ChildScanners_AccessDeniedWarnsOnceAndContinues(t *testing.T) {
	for _, c := range rhChildCases() {
		t.Run(c.name, func(t *testing.T) {
			p1, p2, p3 := c.parent("p1"), c.parent("p2"), c.parent("p3")
			deny := apiErr("AccessDeniedException", "User: arn:aws:iam::1:user/u is not authorized to perform: x")
			stub := &rhV2Stub{
				errs:  map[string]error{c.op + "/" + p1: deny, c.op + "/" + p2: deny},
				pages: map[string]map[string]any{c.op + "/" + p3: {"": c.page(p3, []string{"3"}, nil)}},
			}
			total, rows, warns, st := runRHChild(t, c, stub, []string{p1, p2, p3})
			if total != 1 || len(rows) != 1 {
				t.Fatalf("total = %d, rows = %d; want 1, 1 (the readable sibling)", total, len(rows))
			}
			assertRHChild(t, st, rows, c, p3, "3")
			if len(warns) != 1 || warns[0].Service != "resiliencehub:"+c.op {
				t.Errorf("warnings = %+v; want exactly one for resiliencehub:%s", warns, c.op)
			}
		})
	}
}

func TestRHV2ChildScanners_OtherErrorPropagates(t *testing.T) {
	for _, c := range rhChildCases() {
		t.Run(c.name, func(t *testing.T) {
			p1 := c.parent("p1")
			stub := &rhV2Stub{errs: map[string]error{c.op + "/" + p1: apiErr("ValidationException", "bad")}}
			_, _, err := c.scan(context.Background(), stub, newTestAccount(testAccountID), testRegion, newTestStore(t), testScanID, []string{p1})
			if !isAPIErrorCode(err, "ValidationException") {
				t.Errorf("err = %v; want the ValidationException propagated", err)
			}
		})
	}
}

// emptyRHResponses queues one empty page for every op scanResilienceHub calls
// when no app assessments exist.
func emptyRHResponses() map[string][]stubCall {
	return map[string][]stubCall{
		"ListApps":               {{Output: &resiliencehub.ListAppsOutput{}}},
		"ListResiliencyPolicies": {{Output: &resiliencehub.ListResiliencyPoliciesOutput{}}},
		"ListAppAssessments":     {{Output: &resiliencehub.ListAppAssessmentsOutput{}}},
		"ListPolicies":           {{Output: &resiliencehubv2.ListPoliciesOutput{}}},
		"ListServices":           {{Output: &resiliencehubv2.ListServicesOutput{}}},
		"ListSystems":            {{Output: &resiliencehubv2.ListSystemsOutput{}}},
	}
}

// v1RHResponses queues one row for every v1 phase and returns the NativeID
// each type must be stored under.
func v1RHResponses(responses map[string][]stubCall) map[string]string {
	appARN := rhARN(testAccountID, "app", "a1")
	rpARN := rhARN(testAccountID, "resiliency-policy", "rp1")
	assessmentARN := rhARN(testAccountID, "app-assessment", "as1")
	rtARN := rhARN(testAccountID, "recommendation-template", "rt1")
	responses["ListApps"] = []stubCall{{Output: &resiliencehub.ListAppsOutput{
		AppSummaries: []rhtypes.AppSummary{{AppArn: sdkaws.String(appARN), Name: sdkaws.String("a1")}},
	}}}
	responses["ListResiliencyPolicies"] = []stubCall{{Output: &resiliencehub.ListResiliencyPoliciesOutput{
		ResiliencyPolicies: []rhtypes.ResiliencyPolicy{{PolicyArn: sdkaws.String(rpARN), PolicyName: sdkaws.String("rp1")}},
	}}}
	responses["ListAppAssessments"] = []stubCall{{Output: &resiliencehub.ListAppAssessmentsOutput{
		AssessmentSummaries: []rhtypes.AppAssessmentSummary{{AssessmentArn: sdkaws.String(assessmentARN)}},
	}}}
	responses["ListRecommendationTemplates"] = []stubCall{{Output: &resiliencehub.ListRecommendationTemplatesOutput{
		RecommendationTemplates: []rhtypes.RecommendationTemplate{{RecommendationTemplateArn: sdkaws.String(rtARN), Name: sdkaws.String("rt1")}},
	}}}
	return map[string]string{
		TypeResilienceHubApp:                    appARN,
		TypeResilienceHubResiliencyPolicy:       rpARN,
		TypeResilienceHubAppAssessment:          assessmentARN,
		TypeResilienceHubRecommendationTemplate: rtARN,
	}
}

func assertV1RHRows(t *testing.T, st *store.Store, want map[string]string) {
	t.Helper()
	for typ, nativeID := range want {
		if rows := listNetExtraRows(t, st, typ); len(rows) != 1 || rows[nativeID].Type != typ {
			t.Errorf("%s rows = %d; want one with NativeID %s (every v1 phase must run before v2)", typ, len(rows), nativeID)
		}
	}
}

// The top-level scan feeds the services and systems it lists to the test and
// user-journey fan-outs.
func TestScanResilienceHub_StoresV2Rows(t *testing.T) {
	policyARN := rhARN(testAccountID, "policy", "pol")
	serviceARN := rhARN(testAccountID, "service", "svc")
	systemARN := rhARN(testAccountID, "system", "sys")
	responses := emptyRHResponses()
	responses["ListPolicies"] = []stubCall{{Output: &resiliencehubv2.ListPoliciesOutput{
		PolicySummaries: []rhv2types.PolicySummary{{PolicyArn: sdkaws.String(policyARN), Name: sdkaws.String("pol")}},
	}}}
	responses["ListServices"] = []stubCall{{Output: &resiliencehubv2.ListServicesOutput{
		ServiceSummaries: []rhv2types.ServiceSummary{{ServiceArn: sdkaws.String(serviceARN), Name: sdkaws.String("svc")}},
	}}}
	responses["ListSystems"] = []stubCall{{Output: &resiliencehubv2.ListSystemsOutput{
		SystemSummaries: []rhv2types.SystemSummary{{SystemArn: sdkaws.String(systemARN), SystemId: sdkaws.String("sys"), Name: sdkaws.String("sys")}},
	}}}
	responses["ListTests"] = []stubCall{{Output: &resiliencehubv2.ListTestsOutput{
		Tests: []rhv2types.TestSummary{{TestId: sdkaws.String("t1"), ServiceArn: sdkaws.String(serviceARN)}},
	}}}
	responses["ListUserJourneys"] = []stubCall{{Output: &resiliencehubv2.ListUserJourneysOutput{
		UserJourneySummaries: []rhv2types.UserJourneySummary{{UserJourneyId: sdkaws.String("uj1"), Name: sdkaws.String("uj")}},
	}}}
	st := newTestStore(t)
	acct := &account{ID: testAccountID, Name: "Test Account", cfg: cloud9CfgWithStub(stubResponses(t, responses), testRegion)}

	if _, _, err := scanResilienceHub(context.Background(), acct, testRegion, st, testScanID); err != nil {
		t.Fatalf("scanResilienceHub: %v", err)
	}
	for typ, nativeID := range map[string]string{
		TypeResilienceHubPolicy:      policyARN,
		TypeResilienceHubService:     serviceARN,
		TypeResilienceHubSystem:      systemARN,
		TypeResilienceHubTest:        serviceARN + "/test/t1",
		TypeResilienceHubUserJourney: systemARN + "/user-journey/uj1",
	} {
		if rows := listNetExtraRows(t, st, typ); len(rows) != 1 || rows[nativeID].Type != typ {
			t.Errorf("%s rows = %+v; want one with NativeID %s", typ, rows, nativeID)
		}
	}
}

// A hard v2 failure must not cost the rows of the v1 phases that ran before
// it; this pins the v2 phases after every v1 one.
func TestScanResilienceHub_V2ErrorKeepsV1Rows(t *testing.T) {
	responses := emptyRHResponses()
	want := v1RHResponses(responses)
	boom := errors.New("boom")
	responses["ListPolicies"] = []stubCall{{Err: boom}}
	st := newTestStore(t)
	acct := &account{ID: testAccountID, Name: "Test Account", cfg: cloud9CfgWithStub(stubResponses(t, responses), testRegion)}

	_, _, err := scanResilienceHub(context.Background(), acct, testRegion, st, testScanID)
	if !errors.Is(err, boom) {
		t.Fatalf("want the ListPolicies error to propagate, got %v", err)
	}
	assertV1RHRows(t, st, want)
}

// In a region without the v2 API the scan succeeds with every v1 row and
// makes no v2 call after ListPolicies: stubResponses fails the test on a call
// to an op with no queued response.
func TestScanResilienceHub_V2NotDeployedKeepsV1Rows(t *testing.T) {
	responses := emptyRHResponses()
	want := v1RHResponses(responses)
	responses["ListPolicies"] = []stubCall{{Err: apiErr("UnknownOperationException", "")}}
	delete(responses, "ListServices")
	delete(responses, "ListSystems")
	st := newTestStore(t)
	acct := &account{ID: testAccountID, Name: "Test Account", cfg: cloud9CfgWithStub(stubResponses(t, responses), testRegion)}

	if _, _, err := scanResilienceHub(context.Background(), acct, testRegion, st, testScanID); err != nil {
		t.Fatalf("scanResilienceHub: %v; want nil (a v2 gap is not a failure)", err)
	}
	assertV1RHRows(t, st, want)
}
