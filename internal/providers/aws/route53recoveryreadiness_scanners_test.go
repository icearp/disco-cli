package aws

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	awsmw "github.com/aws/aws-sdk-go-v2/aws/middleware"
	"github.com/aws/aws-sdk-go-v2/service/route53recoveryreadiness"
	r53rrtypes "github.com/aws/aws-sdk-go-v2/service/route53recoveryreadiness/types"
	"github.com/aws/smithy-go"
	"github.com/icearp/disco-cli/store"
)

var (
	r53rrDenied    = &smithy.GenericAPIError{Code: "AccessDeniedException", Message: "User: arn:aws:iam::123456789012:user/t is not authorized to perform: route53-recovery-readiness:List"}
	r53rrBadInput  = &smithy.GenericAPIError{Code: "ValidationException", Message: "bad"}
	r53rrThrottled = apiErr("ThrottlingException", "Rate exceeded")
	r53rrTags      = map[string]string{"team": "readiness"}
)

func r53rrARN(kind, name string) string {
	return fmt.Sprintf("arn:aws:route53-recovery-readiness::%s:%s/%s", testAccountID, kind, name)
}

// r53rrFixture is what the stub lists, two pages per op: page one holds a
// tagged element and one with no ARN, page two an untagged element.
type r53rrFixture struct {
	cells  [][]r53rrtypes.CellOutput
	checks [][]r53rrtypes.ReadinessCheckOutput
	groups [][]r53rrtypes.RecoveryGroupOutput
	sets   [][]r53rrtypes.ResourceSetOutput
}

func newR53RRFixture() r53rrFixture {
	return r53rrFixture{
		cells: [][]r53rrtypes.CellOutput{
			{
				{CellArn: ptrStr(r53rrARN("cell", "c1")), CellName: ptrStr("c1"), Cells: []string{}, ParentReadinessScopes: []string{r53rrARN("recovery-group", "g1")}, Tags: r53rrTags},
				{CellName: ptrStr("c-noarn")},
			},
			{{CellArn: ptrStr(r53rrARN("cell", "c2")), CellName: ptrStr("c2")}},
		},
		checks: [][]r53rrtypes.ReadinessCheckOutput{
			{
				{ReadinessCheckArn: ptrStr(r53rrARN("readiness-check", "k1")), ReadinessCheckName: ptrStr("k1"), ResourceSet: ptrStr("s1"), Tags: r53rrTags},
				{ReadinessCheckName: ptrStr("k-noarn")},
			},
			// ReadinessCheckName is optional in the model: the row has no name.
			{{ReadinessCheckArn: ptrStr(r53rrARN("readiness-check", "k2")), ResourceSet: ptrStr("s2")}},
		},
		groups: [][]r53rrtypes.RecoveryGroupOutput{
			{
				{RecoveryGroupArn: ptrStr(r53rrARN("recovery-group", "g1")), RecoveryGroupName: ptrStr("g1"), Cells: []string{r53rrARN("cell", "c1")}, Tags: r53rrTags},
				{RecoveryGroupName: ptrStr("g-noarn")},
			},
			{{RecoveryGroupArn: ptrStr(r53rrARN("recovery-group", "g2")), RecoveryGroupName: ptrStr("g2")}},
		},
		sets: [][]r53rrtypes.ResourceSetOutput{
			{
				{ResourceSetArn: ptrStr(r53rrARN("resource-set", "s1")), ResourceSetName: ptrStr("s1"), ResourceSetType: ptrStr("AWS::DynamoDB::Table"), Resources: []r53rrtypes.Resource{{ResourceArn: ptrStr("arn:aws:dynamodb:us-east-1:123456789012:table/t")}}, Tags: r53rrTags},
				{ResourceSetName: ptrStr("s-noarn")},
			},
			{{ResourceSetArn: ptrStr(r53rrARN("resource-set", "s2")), ResourceSetName: ptrStr("s2"), ResourceSetType: ptrStr("AWS::DynamoDB::Table")}},
		},
	}
}

// r53rrServe returns page token ("" or "p2") of pages, failing a call sent
// anywhere but us-west-2, one whose MaxResults is not the model cap or whose
// token names no page, and returning fail[token] when set.
func r53rrServe[Item any](ctx context.Context, pages [][]Item, token *string, maxResults *int32, fail map[string]error) ([]Item, *string, error) {
	if region := awsmw.GetRegion(ctx); region != "us-west-2" {
		return nil, nil, fmt.Errorf("called in %q, want us-west-2", region)
	}
	if maxResults == nil || *maxResults != 1000 {
		return nil, nil, fmt.Errorf("MaxResults = %v, want 1000", maxResults)
	}
	if err := fail[sv(token)]; err != nil {
		return nil, nil, err
	}
	switch {
	case token == nil:
		return pages[0], ptrStr("p2"), nil
	case *token == "p2":
		return pages[1], nil, nil
	}
	return nil, nil, fmt.Errorf("unexpected NextToken %q", *token)
}

// r53rrHandlers serves f; fail is keyed by op, then by the token sent ("" on
// page one). calls records the op of each call.
func r53rrHandlers(f r53rrFixture, fail map[string]map[string]error, calls *parentCalls) map[string]func(context.Context, any) (any, error) {
	return map[string]func(context.Context, any) (any, error){
		"ListCells": func(ctx context.Context, in any) (any, error) {
			calls.add("ListCells")
			p := in.(*route53recoveryreadiness.ListCellsInput)
			items, next, err := r53rrServe(ctx, f.cells, p.NextToken, p.MaxResults, fail["ListCells"])
			if err != nil {
				return nil, err
			}
			return &route53recoveryreadiness.ListCellsOutput{Cells: items, NextToken: next}, nil
		},
		"ListReadinessChecks": func(ctx context.Context, in any) (any, error) {
			calls.add("ListReadinessChecks")
			p := in.(*route53recoveryreadiness.ListReadinessChecksInput)
			items, next, err := r53rrServe(ctx, f.checks, p.NextToken, p.MaxResults, fail["ListReadinessChecks"])
			if err != nil {
				return nil, err
			}
			return &route53recoveryreadiness.ListReadinessChecksOutput{ReadinessChecks: items, NextToken: next}, nil
		},
		"ListRecoveryGroups": func(ctx context.Context, in any) (any, error) {
			calls.add("ListRecoveryGroups")
			p := in.(*route53recoveryreadiness.ListRecoveryGroupsInput)
			items, next, err := r53rrServe(ctx, f.groups, p.NextToken, p.MaxResults, fail["ListRecoveryGroups"])
			if err != nil {
				return nil, err
			}
			return &route53recoveryreadiness.ListRecoveryGroupsOutput{RecoveryGroups: items, NextToken: next}, nil
		},
		"ListResourceSets": func(ctx context.Context, in any) (any, error) {
			calls.add("ListResourceSets")
			p := in.(*route53recoveryreadiness.ListResourceSetsInput)
			items, next, err := r53rrServe(ctx, f.sets, p.NextToken, p.MaxResults, fail["ListResourceSets"])
			if err != nil {
				return nil, err
			}
			return &route53recoveryreadiness.ListResourceSetsOutput{ResourceSets: items, NextToken: next}, nil
		},
	}
}

var r53rrOpLabels = map[string]string{
	"ListCells":           "route53recoveryreadiness:ListCells",
	"ListReadinessChecks": "route53recoveryreadiness:ListReadinessChecks",
	"ListRecoveryGroups":  "route53recoveryreadiness:ListRecoveryGroups",
	"ListResourceSets":    "route53recoveryreadiness:ListResourceSets",
}

// r53rrResult is what one top-level scan returned and recorded.
type r53rrResult struct {
	total, inserted int
	err             error
	warns           map[string]*int
	warnings        []store.ScanWarning
	calls           *parentCalls
}

// scanR53RRTopLevel runs scanR53RecoveryReadiness against the stub as the
// global dispatcher does (region "").
func scanR53RRTopLevel(t *testing.T, st *store.Store, fail map[string]map[string]error) r53rrResult {
	t.Helper()
	res := r53rrResult{warns: map[string]*int{}, calls: &parentCalls{}}
	for op, label := range r53rrOpLabels {
		res.warns[op] = countOpWarnings(st, label)
	}
	prev := st.OnWarn
	st.OnWarn = func(w store.ScanWarning) {
		res.warnings = append(res.warnings, w)
		prev(w)
	}
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfigCtx(r53rrHandlers(newR53RRFixture(), fail, res.calls), r53rrDenied)
	res.total, res.inserted, res.err = scanR53RecoveryReadiness(context.Background(), acct, "", st, testScanID)
	return res
}

// r53rrAllRows is every NativeID the full fixture stores, per type.
var r53rrAllRows = map[string][]string{
	TypeR53RRCell:           {r53rrARN("cell", "c1"), r53rrARN("cell", "c2")},
	TypeR53RRReadinessCheck: {r53rrARN("readiness-check", "k1"), r53rrARN("readiness-check", "k2")},
	TypeR53RRRecoveryGroup:  {r53rrARN("recovery-group", "g1"), r53rrARN("recovery-group", "g2")},
	TypeR53RRResourceSet:    {r53rrARN("resource-set", "s1"), r53rrARN("resource-set", "s2")},
}

func assertR53RRRows(t *testing.T, st *store.Store, want map[string][]string) {
	t.Helper()
	for _, typ := range []string{TypeR53RRCell, TypeR53RRReadinessCheck, TypeR53RRRecoveryGroup, TypeR53RRResourceSet} {
		got := nativeIDs(rowsByNativeID(t, st, typ))
		if !slices.Equal(got, want[typ]) {
			t.Errorf("%s rows = %v, want %v", typ, got, want[typ])
		}
	}
}

// assertR53RRRow checks a stored row's name (nil for none), region, attributes
// and tags (nil wantTags: no tags stored).
func assertR53RRRow(t *testing.T, row store.Resource, name *string, attrs any, wantTags map[string]string) {
	t.Helper()
	if sv(row.Name) != sv(name) || (name == nil) != (row.Name == nil) || sv(row.Region) != "global" || row.AttributesJSON != mustJSON(attrs) {
		t.Errorf("%s: name/region = %v/%q, attrs = %s; want %v/global, %s", row.NativeID, row.Name, sv(row.Region), row.AttributesJSON, name, mustJSON(attrs))
	}
	if wantTags == nil {
		if row.TagsJSON != nil {
			t.Errorf("%s: tags = %s, want none", row.NativeID, *row.TagsJSON)
		}
		return
	}
	assertTagsMap(t, row.TagsJSON, wantTags)
}

func TestScanR53RecoveryReadiness_StoresEveryTypeWithTags(t *testing.T) {
	st := newTestStore(t)
	res := scanR53RRTopLevel(t, st, nil)
	if res.err != nil {
		t.Fatalf("scanR53RecoveryReadiness: %v", res.err)
	}
	if res.total != 8 || res.inserted != 8 {
		t.Errorf("total/inserted = %d/%d, want 8/8", res.total, res.inserted)
	}
	assertR53RRRows(t, st, r53rrAllRows)

	f := newR53RRFixture()
	cells := rowsByNativeID(t, st, TypeR53RRCell)
	assertR53RRRow(t, cells[r53rrARN("cell", "c1")], ptrStr("c1"), f.cells[0][0], r53rrTags)
	assertR53RRRow(t, cells[r53rrARN("cell", "c2")], ptrStr("c2"), f.cells[1][0], nil)
	checks := rowsByNativeID(t, st, TypeR53RRReadinessCheck)
	assertR53RRRow(t, checks[r53rrARN("readiness-check", "k1")], ptrStr("k1"), f.checks[0][0], r53rrTags)
	assertR53RRRow(t, checks[r53rrARN("readiness-check", "k2")], nil, f.checks[1][0], nil)
	groups := rowsByNativeID(t, st, TypeR53RRRecoveryGroup)
	assertR53RRRow(t, groups[r53rrARN("recovery-group", "g1")], ptrStr("g1"), f.groups[0][0], r53rrTags)
	assertR53RRRow(t, groups[r53rrARN("recovery-group", "g2")], ptrStr("g2"), f.groups[1][0], nil)
	sets := rowsByNativeID(t, st, TypeR53RRResourceSet)
	assertR53RRRow(t, sets[r53rrARN("resource-set", "s1")], ptrStr("s1"), f.sets[0][0], r53rrTags)
	assertR53RRRow(t, sets[r53rrARN("resource-set", "s2")], ptrStr("s2"), f.sets[1][0], nil)

	// One warning per op, for its element with no ARN, scoped to the endpoint's region.
	for op := range r53rrOpLabels {
		if *res.warns[op] != 1 {
			t.Errorf("%s warnings = %d, want 1", op, *res.warns[op])
		}
	}
	// The key of the element not stored is matched in the message: a
	// ScanWarning has no key field.
	noARN := map[string]string{
		"ListCells": "c-noarn", "ListReadinessChecks": "k-noarn",
		"ListRecoveryGroups": "g-noarn", "ListResourceSets": "s-noarn",
	}
	for _, w := range res.warnings {
		if w.Scope != testAccountID+"/us-west-2" {
			t.Errorf("warning scope = %q, want %s/us-west-2", w.Scope, testAccountID)
		}
	}
	for op, key := range noARN {
		if !slices.ContainsFunc(res.warnings, func(w store.ScanWarning) bool {
			return w.Service == r53rrOpLabels[op] && strings.Contains(w.Message, key)
		}) {
			t.Errorf("%s: no warning names the element %q with no ARN; warnings = %v", op, key, res.warnings)
		}
	}
}

// A failed page two keeps the rows read before it, the error is returned, and
// every other listing still runs.
func TestScanR53RecoveryReadiness_FailedPageKeepsRowsAndRunsOtherPhases(t *testing.T) {
	for op, typ := range map[string]string{
		"ListCells":           TypeR53RRCell,
		"ListReadinessChecks": TypeR53RRReadinessCheck,
		"ListRecoveryGroups":  TypeR53RRRecoveryGroup,
		"ListResourceSets":    TypeR53RRResourceSet,
	} {
		t.Run(op, func(t *testing.T) {
			st := newTestStore(t)
			res := scanR53RRTopLevel(t, st, map[string]map[string]error{op: {"p2": r53rrBadInput}})
			if classifyServiceError(res.err) != outcomeError || !isAPIErrorCode(res.err, "ValidationException") {
				t.Fatalf("err = %v, want a ValidationException error", res.err)
			}
			if res.total != 7 || res.inserted != 7 {
				t.Errorf("total/inserted = %d/%d, want 7/7", res.total, res.inserted)
			}
			want := map[string][]string{}
			for k, v := range r53rrAllRows {
				want[k] = v
			}
			want[typ] = r53rrAllRows[typ][:1]
			assertR53RRRows(t, st, want)
		})
	}
}

// A denied listing warns once for its op and is not an error; the other
// listings keep their rows.
func TestScanR53RecoveryReadiness_DeniedListingWarnsOnce(t *testing.T) {
	for op, typ := range map[string]string{
		"ListCells":           TypeR53RRCell,
		"ListReadinessChecks": TypeR53RRReadinessCheck,
		"ListRecoveryGroups":  TypeR53RRRecoveryGroup,
		"ListResourceSets":    TypeR53RRResourceSet,
	} {
		t.Run(op, func(t *testing.T) {
			st := newTestStore(t)
			res := scanR53RRTopLevel(t, st, map[string]map[string]error{op: {"": r53rrDenied}})
			if res.err != nil {
				t.Fatalf("err = %v, want nil", res.err)
			}
			if *res.warns[op] != 1 {
				t.Errorf("%s warnings = %d, want 1", op, *res.warns[op])
			}
			want := map[string][]string{}
			for k, v := range r53rrAllRows {
				if k != typ {
					want[k] = v
				}
			}
			assertR53RRRows(t, st, want)
		})
	}
}

// A throttled listing alone leaves the scan transient; beside another
// listing's hard error it is warned about once and the scan reads as an error.
func TestScanR53RecoveryReadiness_TransientPhaseBesideHardError(t *testing.T) {
	for _, tc := range []struct {
		name      string
		hard      bool
		want      serviceOutcome
		wantWarns int
	}{
		{"alone", false, outcomeTransient, 1},
		{"beside a hard error", true, outcomeError, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			fail := map[string]map[string]error{"ListCells": {"p2": r53rrThrottled}}
			if tc.hard {
				fail["ListResourceSets"] = map[string]error{"p2": r53rrBadInput}
			}
			res := scanR53RRTopLevel(t, st, fail)
			if got := classifyServiceError(res.err); got != tc.want {
				t.Errorf("classifyServiceError(%v) = %v, want %v", res.err, got, tc.want)
			}
			// The no-ARN warning, plus the demoted throttle beside a hard error.
			if *res.warns["ListCells"] != tc.wantWarns {
				t.Errorf("ListCells warnings = %d, want %d", *res.warns["ListCells"], tc.wantWarns)
			}
		})
	}
}

// A failed write stops the scan with no warning claiming the lost rows, and
// no later listing runs.
func TestScanR53RecoveryReadiness_WriteFailureStops(t *testing.T) {
	st := newTestStore(t)
	refuseStoreWrites(t, st, TypeR53RRReadinessCheck)
	res := scanR53RRTopLevel(t, st, nil)
	if !errors.Is(res.err, store.ErrStoreWrite) {
		t.Fatalf("err = %v; want the store write failure", res.err)
	}
	if *res.warns["ListReadinessChecks"] != 0 {
		t.Errorf("ListReadinessChecks warnings = %d; want none after a failed write", *res.warns["ListReadinessChecks"])
	}
	if got := res.calls.distinct(); !slices.Equal(got, []string{"ListCells", "ListReadinessChecks"}) {
		t.Errorf("ops called = %v; want only ListCells and ListReadinessChecks", got)
	}
	assertR53RRRows(t, st, map[string][]string{TypeR53RRCell: r53rrAllRows[TypeR53RRCell]})
}
