package aws

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/lakeformation"
	lftypes "github.com/aws/aws-sdk-go-v2/service/lakeformation/types"
	"github.com/icearp/disco-cli/store"
)

var (
	lfDenied   = apiErr("AccessDeniedException", "User: arn:aws:iam::123456789012:role/r is not authorized to perform: lakeformation:ListLakeFormationOptIns")
	lfBadInput = apiErr("InvalidInputException", "bad")
	lfThrottle = apiErr("ThrottlingException", "slow down")
)

func lfOptIn(principal, db string) lftypes.LakeFormationOptInsInfo {
	return lftypes.LakeFormationOptInsInfo{
		Principal:     &lftypes.DataLakePrincipal{DataLakePrincipalIdentifier: sdkaws.String(principal)},
		Resource:      &lftypes.Resource{Database: &lftypes.DatabaseResource{Name: sdkaws.String(db)}},
		LastModified:  sdkaws.Time(time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)),
		LastUpdatedBy: sdkaws.String("arn:aws:iam::123456789012:user/admin"),
	}
}

var (
	lfOptInA = lfOptIn("arn:aws:iam::123456789012:role/analyst", "sales")
	lfOptInB = lfOptIn("arn:aws:iam::123456789012:user/bob", "hr")
)

// lfOptInCalls records the NextToken and MaxResults of every opt-in list call.
type lfOptInCalls struct {
	mu         sync.Mutex
	tokens     []string
	maxResults []int32
}

func (c *lfOptInCalls) record(in *lakeformation.ListLakeFormationOptInsInput) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.tokens = append(c.tokens, sv(in.NextToken))
	c.maxResults = append(c.maxResults, sdkaws.ToInt32(in.MaxResults))
}

// lfHandlers answers every catalog phase with an empty result and the opt-in
// list with optIns as its single page.
func lfHandlers(calls *lfOptInCalls, optIns ...lftypes.LakeFormationOptInsInfo) map[string]func(in any) (any, error) {
	return map[string]func(in any) (any, error){
		"ListResources":       func(any) (any, error) { return &lakeformation.ListResourcesOutput{}, nil },
		"ListDataCellsFilter": func(any) (any, error) { return &lakeformation.ListDataCellsFilterOutput{}, nil },
		"GetDataLakeSettings": func(any) (any, error) { return &lakeformation.GetDataLakeSettingsOutput{}, nil },
		"ListPermissions":     func(any) (any, error) { return &lakeformation.ListPermissionsOutput{}, nil },
		"ListLFTags":          func(any) (any, error) { return &lakeformation.ListLFTagsOutput{}, nil },
		"ListLakeFormationOptIns": func(in any) (any, error) {
			calls.record(in.(*lakeformation.ListLakeFormationOptInsInput))
			return &lakeformation.ListLakeFormationOptInsOutput{LakeFormationOptInsInfoList: optIns}, nil
		},
	}
}

// lfTwoPages answers the opt-in list with page one holding first and page two
// either holding second or failing with page2Err.
func lfTwoPages(calls *lfOptInCalls, first, second lftypes.LakeFormationOptInsInfo, page2Err error) func(in any) (any, error) {
	return func(in any) (any, error) {
		req := in.(*lakeformation.ListLakeFormationOptInsInput)
		calls.record(req)
		if req.NextToken == nil {
			return &lakeformation.ListLakeFormationOptInsOutput{
				LakeFormationOptInsInfoList: []lftypes.LakeFormationOptInsInfo{first},
				NextToken:                   sdkaws.String("page2"),
			}, nil
		}
		if page2Err != nil {
			return nil, page2Err
		}
		return &lakeformation.ListLakeFormationOptInsOutput{LakeFormationOptInsInfoList: []lftypes.LakeFormationOptInsInfo{second}}, nil
	}
}

func scanLakeFormationTopLevel(t *testing.T, st *store.Store, handlers map[string]func(in any) (any, error)) (int, error) {
	t.Helper()
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(handlers, lfDenied)
	total, _, err := scanLakeFormation(context.Background(), acct, testRegion, st, testScanID)
	return total, err
}

func TestScanLFOptIns_TwoPagesBothStored(t *testing.T) {
	st := newTestStore(t)
	var calls lfOptInCalls
	h := lfHandlers(&calls)
	h["ListLakeFormationOptIns"] = lfTwoPages(&calls, lfOptInA, lfOptInB, nil)

	total, err := scanLakeFormationTopLevel(t, st, h)
	if err != nil {
		t.Fatalf("scanLakeFormation: %v", err)
	}
	if total != 2 {
		t.Errorf("total = %d, want 2", total)
	}
	if want := []string{"", "page2"}; strings.Join(calls.tokens, ",") != strings.Join(want, ",") {
		t.Errorf("NextTokens = %q, want %q", calls.tokens, want)
	}
	for i, m := range calls.maxResults {
		if m != 1000 {
			t.Errorf("call %d MaxResults = %d, want the model cap 1000", i, m)
		}
	}
	rows := rowsByNativeID(t, st, TypeLakeFormationOptIn)
	if len(rows) != 2 {
		t.Fatalf("stored %d opt-ins, want 2: %v", len(rows), rows)
	}
	// The literal pins opt-in identity across releases: a change to it re-keys
	// every stored opt-in, so update it only for a deliberate identity change.
	r, ok := rows["arn:aws:lakeformation:us-east-1:123456789012:lake-formation-opt-in/5c7052af122a3145"]
	if !ok {
		t.Fatalf("opt-in A missing; stored %v", rows)
	}
	if r.Type != TypeLakeFormationOptIn || sv(r.Region) != testRegion || sv(r.Name) != "arn:aws:iam::123456789012:role/analyst" {
		t.Errorf("row = type %q region %q name %q", r.Type, sv(r.Region), sv(r.Name))
	}
	if !strings.Contains(r.AttributesJSON, `"LastUpdatedBy":"arn:aws:iam::123456789012:user/admin"`) {
		t.Errorf("attributes lack LastUpdatedBy: %s", r.AttributesJSON)
	}
}

func TestScanLFOptIns_EmptyStoresNothing(t *testing.T) {
	st := newTestStore(t)
	var calls lfOptInCalls
	total, err := scanLakeFormationTopLevel(t, st, lfHandlers(&calls))
	if err != nil {
		t.Fatalf("scanLakeFormation: %v", err)
	}
	if total != 0 || len(rowsByNativeID(t, st, TypeLakeFormationOptIn)) != 0 {
		t.Errorf("total = %d, want 0 and no opt-in rows", total)
	}
}

func TestScanLFOptIns_SkipsRowsWithoutPrincipalOrResource(t *testing.T) {
	st := newTestStore(t)
	var calls lfOptInCalls
	noPrincipal := lfOptInA
	noPrincipal.Principal = nil
	emptyPrincipal := lfOptInA
	emptyPrincipal.Principal = &lftypes.DataLakePrincipal{}
	noResource := lfOptInA
	noResource.Resource = nil
	total, err := scanLakeFormationTopLevel(t, st, lfHandlers(&calls, noPrincipal, emptyPrincipal, noResource, lfOptInB))
	if err != nil {
		t.Fatalf("scanLakeFormation: %v", err)
	}
	rows := rowsByNativeID(t, st, TypeLakeFormationOptIn)
	if total != 1 || len(rows) != 1 {
		t.Fatalf("total = %d, rows = %v; want only opt-in B", total, rows)
	}
	for _, r := range rows {
		if sv(r.Name) != "arn:aws:iam::123456789012:user/bob" {
			t.Errorf("stored %q, want opt-in B", sv(r.Name))
		}
	}
}

func TestScanLFOptIns_AccessDeniedWarnsOnce(t *testing.T) {
	st := newTestStore(t)
	warns := countOpWarnings(st, lfOptInOp)
	var calls lfOptInCalls
	h := lfHandlers(&calls)
	delete(h, "ListLakeFormationOptIns")
	total, err := scanLakeFormationTopLevel(t, st, h)
	if err != nil {
		t.Fatalf("scanLakeFormation: %v, want nil after a denial", err)
	}
	if total != 0 || *warns != 1 {
		t.Errorf("total = %d, warnings = %d; want 0 and 1", total, *warns)
	}
}

func TestScanLFOptIns_ErrorAfterPageKeepsListedRows(t *testing.T) {
	cases := []struct {
		name      string
		page2Err  error
		wantCode  string
		wantWarns int
	}{
		{name: "denial", page2Err: lfDenied, wantWarns: 1},
		{name: "hard error", page2Err: lfBadInput, wantCode: "InvalidInputException"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := countOpWarnings(st, lfOptInOp)
			var calls lfOptInCalls
			h := lfHandlers(&calls)
			h["ListLakeFormationOptIns"] = lfTwoPages(&calls, lfOptInA, lfOptInB, c.page2Err)
			total, err := scanLakeFormationTopLevel(t, st, h)
			if c.wantCode == "" {
				if err != nil {
					t.Fatalf("err = %v, want nil", err)
				}
			} else {
				assertAPICode(t, err, c.wantCode)
			}
			if total != 1 || len(rowsByNativeID(t, st, TypeLakeFormationOptIn)) != 1 {
				t.Errorf("total = %d; want page one's single row kept", total)
			}
			if *warns != c.wantWarns {
				t.Errorf("warnings = %d, want %d", *warns, c.wantWarns)
			}
		})
	}
}

func TestScanLFOptIns_StopsOnRepeatedToken(t *testing.T) {
	st := newTestStore(t)
	var calls lfOptInCalls
	h := lfHandlers(&calls)
	h["ListLakeFormationOptIns"] = func(in any) (any, error) {
		calls.record(in.(*lakeformation.ListLakeFormationOptInsInput))
		if len(calls.tokens) > 3 {
			return nil, lfBadInput
		}
		return &lakeformation.ListLakeFormationOptInsOutput{NextToken: sdkaws.String("same")}, nil
	}
	if _, err := scanLakeFormationTopLevel(t, st, h); err != nil {
		t.Fatalf("scanLakeFormation: %v", err)
	}
	if len(calls.tokens) != 2 {
		t.Errorf("list calls = %d, want 2: the repeated token must end the listing", len(calls.tokens))
	}
}

// The opt-in phase runs after the catalog phases and whatever they return: a
// catalog error costs no opt-in rows, and an opt-in error costs no catalog rows.
func TestScanLakeFormation_PhasesIndependent(t *testing.T) {
	t.Run("catalog error", func(t *testing.T) {
		st := newTestStore(t)
		var calls lfOptInCalls
		h := lfHandlers(&calls, lfOptInA)
		h["ListResources"] = func(any) (any, error) { return nil, lfBadInput }
		total, err := scanLakeFormationTopLevel(t, st, h)
		assertAPICode(t, err, "InvalidInputException")
		if total != 1 || len(rowsByNativeID(t, st, TypeLakeFormationOptIn)) != 1 {
			t.Errorf("total = %d; want the opt-in row stored despite the catalog error", total)
		}
	})
	t.Run("opt-in error", func(t *testing.T) {
		st := newTestStore(t)
		var calls lfOptInCalls
		h := lfHandlers(&calls)
		h["ListResources"] = func(any) (any, error) {
			return &lakeformation.ListResourcesOutput{ResourceInfoList: []lftypes.ResourceInfo{{ResourceArn: sdkaws.String("arn:aws:s3:::lake")}}}, nil
		}
		h["ListLakeFormationOptIns"] = func(any) (any, error) { return nil, lfBadInput }
		total, err := scanLakeFormationTopLevel(t, st, h)
		assertAPICode(t, err, "InvalidInputException")
		if _, ok := rowsByNativeID(t, st, TypeLakeFormationResource)["arn:aws:s3:::lake"]; !ok || total != 1 {
			t.Errorf("total = %d; want the catalog row stored despite the opt-in error", total)
		}
	})
}

// A transient catalog error is demoted to a warning so the opt-in phase's hard
// error still reaches the dispatcher as a hard error.
func TestScanLakeFormation_CatalogTransientDoesNotMaskOptInError(t *testing.T) {
	st := newTestStore(t)
	warns := countOpWarnings(st, "lakeformation")
	var calls lfOptInCalls
	h := lfHandlers(&calls)
	h["ListResources"] = func(any) (any, error) { return nil, lfThrottle }
	h["ListLakeFormationOptIns"] = func(any) (any, error) { return nil, lfBadInput }
	_, err := scanLakeFormationTopLevel(t, st, h)
	assertAPICode(t, err, "InvalidInputException")
	if got := classifyServiceError(err); got != outcomeError {
		t.Errorf("classifyServiceError = %v, want outcomeError", got)
	}
	if *warns != 1 {
		t.Errorf("transient warnings = %d, want 1", *warns)
	}
}

// A network error in the opt-in phase is demoted to a warning so the catalog
// phase's hard error still reaches the dispatcher as a hard error.
func TestScanLakeFormation_OptInTransientDoesNotMaskCatalogError(t *testing.T) {
	st := newTestStore(t)
	warns := countOpWarnings(st, lfOptInOp)
	var calls lfOptInCalls
	h := lfHandlers(&calls)
	h["ListResources"] = func(any) (any, error) { return nil, lfBadInput }
	h["ListLakeFormationOptIns"] = func(any) (any, error) {
		return nil, &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connection refused")}
	}
	_, err := scanLakeFormationTopLevel(t, st, h)
	assertAPICode(t, err, "InvalidInputException")
	if got := classifyServiceError(err); got != outcomeError {
		t.Errorf("classifyServiceError = %v, want outcomeError", got)
	}
	if *warns != 1 {
		t.Errorf("transient warnings = %d, want 1", *warns)
	}
}

func TestLFOptInARN_Identity(t *testing.T) {
	base := lfOptInA
	edited := base
	edited.LastModified = sdkaws.Time(time.Date(2026, 5, 6, 7, 8, 9, 0, time.UTC))
	edited.LastUpdatedBy = sdkaws.String("arn:aws:iam::123456789012:user/other")
	withCondition := base
	withCondition.Condition = &lftypes.Condition{Expression: sdkaws.String(`context.region == "us-east-1"`)}
	otherPrincipal := lfOptIn("arn:aws:iam::123456789012:role/other", "sales")
	otherResource := lfOptIn("arn:aws:iam::123456789012:role/analyst", "finance")

	tableWildcard := lfOptIn("arn:aws:iam::123456789012:role/analyst", "sales")
	tableWildcard.Resource = &lftypes.Resource{Table: &lftypes.TableResource{DatabaseName: sdkaws.String("sales"), TableWildcard: &lftypes.TableWildcard{}}}
	tableNoMarker := tableWildcard
	tableNoMarker.Resource = &lftypes.Resource{Table: &lftypes.TableResource{DatabaseName: sdkaws.String("sales")}}

	id := mustLFOptInARN(t, base)
	if got := mustLFOptInARN(t, edited); got != id {
		t.Errorf("LastModified/LastUpdatedBy changed the NativeID: %s vs %s", got, id)
	}
	for name, o := range map[string]lftypes.LakeFormationOptInsInfo{
		"condition": withCondition, "principal": otherPrincipal, "resource": otherResource,
	} {
		if mustLFOptInARN(t, o) == id {
			t.Errorf("a different %s kept the NativeID %s", name, id)
		}
	}
	if mustLFOptInARN(t, tableWildcard) == mustLFOptInARN(t, tableNoMarker) {
		t.Error("dropping the empty TableWildcard marker merged two different resources")
	}
}

func mustLFOptInARN(t *testing.T, o lftypes.LakeFormationOptInsInfo) string {
	t.Helper()
	id, err := lfOptInARN("aws", testRegion, testAccountID, o)
	if err != nil {
		t.Fatalf("lfOptInARN: %v", err)
	}
	return id
}

// Lists inside the resource are sets or ANDs of conditions, so their order
// must not change the NativeID, while different contents must.
func TestLFOptInARN_ListOrderIgnored(t *testing.T) {
	withLists := func(cols, excluded, values, policyValues []string, swapConditions bool) lftypes.LakeFormationOptInsInfo {
		conditions := []lftypes.LFTag{
			{TagKey: sdkaws.String("tier"), TagValues: policyValues},
			{TagKey: sdkaws.String("region"), TagValues: []string{"eu"}},
		}
		if swapConditions {
			conditions[0], conditions[1] = conditions[1], conditions[0]
		}
		o := lfOptInA
		o.Resource = &lftypes.Resource{
			TableWithColumns: &lftypes.TableWithColumnsResource{
				DatabaseName: sdkaws.String("sales"), Name: sdkaws.String("orders"), ColumnNames: cols,
				ColumnWildcard: &lftypes.ColumnWildcard{ExcludedColumnNames: excluded},
			},
			LFTag: &lftypes.LFTagKeyResource{TagKey: sdkaws.String("tier"), TagValues: values},
			LFTagPolicy: &lftypes.LFTagPolicyResource{
				ResourceType: lftypes.ResourceTypeTable,
				Expression:   conditions,
			},
		}
		return o
	}
	ab, cd, ef, gh := []string{"a", "b"}, []string{"c", "d"}, []string{"e", "f"}, []string{"g", "h"}
	id := mustLFOptInARN(t, withLists(ab, cd, ef, gh, false))
	if got := mustLFOptInARN(t, withLists([]string{"b", "a"}, []string{"d", "c"}, []string{"f", "e"}, []string{"h", "g"}, false)); got != id {
		t.Errorf("reordered string lists changed the NativeID: %s vs %s", got, id)
	}
	if got := mustLFOptInARN(t, withLists(ab, cd, ef, gh, true)); got != id {
		t.Errorf("reordered Expression conditions changed the NativeID: %s vs %s", got, id)
	}
	if mustLFOptInARN(t, withLists([]string{"a", "x"}, cd, ef, gh, false)) == id {
		t.Error("a different column set kept the NativeID")
	}
	if mustLFOptInARN(t, withLists(ab, cd, ef, []string{"g", "x"}, true)) == id {
		t.Error("a different Expression condition kept the NativeID")
	}
}

// A member a module bump adds marshals as null, "" (an unset optional enum) or
// an empty list on every existing opt-in; the canonical key must not see it.
// false and 0 are values, not absence, and must still count.
func TestLFCanonical_IgnoresAbsentMembers(t *testing.T) {
	canonical := func(raw string) string {
		var v any
		if err := json.Unmarshal([]byte(raw), &v); err != nil {
			t.Fatalf("decode %s: %v", raw, err)
		}
		c, err := lfCanonical(v)
		if err != nil {
			t.Fatalf("lfCanonical(%s): %v", raw, err)
		}
		return mustJSON(c)
	}
	want := canonical(`{"Principal":{"DataLakePrincipalIdentifier":"p"},"Resource":{"Database":{"Name":"sales","CatalogId":null},"Table":null}}`)
	got := canonical(`{"X":null,"Y":[],"E":"","Principal":{"DataLakePrincipalIdentifier":"p","New":null},"Resource":{"Database":{"Name":"sales","Kind":""},"NewKind":null}}`)
	if got != want {
		t.Errorf("canonical with added absent members = %s; want %s", got, want)
	}
	for _, raw := range []string{
		`{"Principal":{"DataLakePrincipalIdentifier":"p"},"Resource":{"Database":{"Name":"sales"}},"F":false}`,
		`{"Principal":{"DataLakePrincipalIdentifier":"p"},"Resource":{"Database":{"Name":"sales"}},"N":0}`,
	} {
		if canonical(raw) == want {
			t.Errorf("canonical(%s) dropped a false/0 value", raw)
		}
	}
}
