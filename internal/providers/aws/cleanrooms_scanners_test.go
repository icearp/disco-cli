package aws

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"net"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/cleanrooms"
	crtypes "github.com/aws/aws-sdk-go-v2/service/cleanrooms/types"
	"github.com/aws/smithy-go"
	"github.com/icearp/disco-cli/store"
)

var (
	crDenied    = &smithy.GenericAPIError{Code: "AccessDeniedException", Message: "User: arn:aws:iam::123456789012:user/t is not authorized to perform: cleanrooms:List"}
	crNotFound  = &smithy.GenericAPIError{Code: "ResourceNotFoundException", Message: "gone"}
	crThrottled = &smithy.GenericAPIError{Code: "ThrottlingException", Message: "rate exceeded"}
	crCreated   = time.Date(2026, 9, 1, 8, 0, 0, 0, time.UTC)
)

const (
	crCollabPrefix     = "arn:aws:cleanrooms:us-east-1:123456789012:collaboration/"
	crMembershipPrefix = "arn:aws:cleanrooms:us-east-1:123456789012:membership/"
)

func crClient(handlers map[string]func(in any) (any, error)) *cleanrooms.Client {
	return cleanrooms.NewFromConfig(opStubConfig(handlers, crDenied))
}

func crParents(prefix string, ids ...string) []childParent {
	out := make([]childParent, len(ids))
	for i, id := range ids {
		out[i] = childParent{id: id, arn: prefix + id}
	}
	return out
}

// crSeedParents stores the parent rows the children are linked under.
func crSeedParents(t *testing.T, st *store.Store, typ string, parents []childParent) {
	t.Helper()
	for _, p := range parents {
		upsertTestResource(t, st, "aws", testAccountID, typ, p.arn, testRegion, "{}")
	}
}

func crMember(accountID string, status crtypes.MemberStatus) crtypes.MemberSummary {
	return crtypes.MemberSummary{
		AccountId: ptrStr(accountID), DisplayName: ptrStr("member " + accountID), Status: status,
		Abilities: []crtypes.MemberAbility{crtypes.MemberAbilityCanQuery}, CreateTime: &crCreated,
	}
}

func crIntermediateTable(membershipID, id string) crtypes.IntermediateTableSummary {
	return crtypes.IntermediateTableSummary{
		Arn: ptrStr(crMembershipPrefix + membershipID + "/intermediatetable/" + id),
		Id:  ptrStr(id), Name: ptrStr("table " + id), MembershipId: ptrStr(membershipID),
		Status: crtypes.IntermediateTableStatusPopulateSuccess, CreateTime: &crCreated,
	}
}

func TestScanCRMembers_StoresEachCollaborationsRoster(t *testing.T) {
	st := newTestStore(t)
	collabs := crParents(crCollabPrefix, "c-a", "c-b", "c-gone")
	crSeedParents(t, st, TypeCleanRoomsCollaboration, collabs)
	calls := &parentCalls{}
	var (
		mu       sync.Mutex
		maxSizes []int32
	)
	handlers := map[string]func(any) (any, error){
		"ListMembers": func(in any) (any, error) {
			req := in.(*cleanrooms.ListMembersInput)
			calls.add(*req.CollaborationIdentifier)
			mu.Lock()
			maxSizes = append(maxSizes, *req.MaxResults)
			mu.Unlock()
			switch {
			case *req.CollaborationIdentifier == "c-gone":
				return nil, crNotFound
			case *req.CollaborationIdentifier == "c-b":
				return &cleanrooms.ListMembersOutput{MemberSummaries: []crtypes.MemberSummary{
					crMember("111111111111", crtypes.MemberStatusLeft), {DisplayName: ptrStr("no account")},
				}}, nil
			case req.NextToken == nil:
				return &cleanrooms.ListMembersOutput{MemberSummaries: []crtypes.MemberSummary{crMember("123456789012", crtypes.MemberStatusActive)}, NextToken: ptrStr("p2")}, nil
			default:
				return &cleanrooms.ListMembersOutput{MemberSummaries: []crtypes.MemberSummary{crMember("222222222222", crtypes.MemberStatusInvited)}}, nil
			}
		},
	}
	total, _, err := scanCRMembers(context.Background(), crClient(handlers), newTestAccount(testAccountID), testRegion, st, testScanID, collabs)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if total != 3 {
		t.Errorf("total = %d, want 3", total)
	}
	if got, want := calls.distinct(), []string{"c-a", "c-b", "c-gone"}; !slices.Equal(got, want) {
		t.Errorf("collaborations queried %v, want %v", got, want)
	}
	for _, n := range maxSizes {
		if n != 100 {
			t.Errorf("MaxResults = %d, want 100", n)
		}
	}
	rows := rowsByNativeID(t, st, TypeCleanRoomsMember)
	if len(rows) != 3 {
		t.Errorf("member rows = %d, want 3", len(rows))
	}
	for _, id := range []string{"c-a/member/123456789012", "c-a/member/222222222222", "c-b/member/111111111111"} {
		if _, ok := rows[crCollabPrefix+id]; !ok {
			t.Errorf("member %s missing", id)
		}
		assertContainsNative(t, st, crCollabPrefix+id[:3], crCollabPrefix+id)
	}
	left := rows[crCollabPrefix+"c-b/member/111111111111"]
	if left.Type != TypeCleanRoomsMember || sv(left.Region) != testRegion || sv(left.Name) != "member 111111111111" ||
		sv(left.Status) != "LEFT" || sv(left.CreatedAt) != "2026-09-01T08:00:00Z" {
		t.Errorf("row = type %q region %q name %q status %q created %q", left.Type, sv(left.Region), sv(left.Name), sv(left.Status), sv(left.CreatedAt))
	}
	var attrs struct{ Abilities []string }
	if err := json.Unmarshal([]byte(left.AttributesJSON), &attrs); err != nil || !slices.Equal(attrs.Abilities, []string{"CAN_QUERY"}) {
		t.Errorf("attrs Abilities = %v (%v), want [CAN_QUERY]", attrs.Abilities, err)
	}
	assertContainsEdgesDeclared(t, st)
}

func TestScanCRIntermediateTables_StoresEachMembershipsTables(t *testing.T) {
	st := newTestStore(t)
	memberships := crParents(crMembershipPrefix, "m-a", "m-b", "m-gone")
	crSeedParents(t, st, TypeCleanRoomsMembership, memberships)
	calls := &parentCalls{}
	var (
		mu       sync.Mutex
		maxSizes []int32
	)
	handlers := map[string]func(any) (any, error){
		"ListTagsForResource": func(any) (any, error) { return &cleanrooms.ListTagsForResourceOutput{}, nil },
		"ListIntermediateTables": func(in any) (any, error) {
			req := in.(*cleanrooms.ListIntermediateTablesInput)
			calls.add(*req.MembershipIdentifier)
			mu.Lock()
			maxSizes = append(maxSizes, *req.MaxResults)
			mu.Unlock()
			switch {
			case *req.MembershipIdentifier == "m-gone":
				return nil, crNotFound
			case *req.MembershipIdentifier == "m-b":
				return &cleanrooms.ListIntermediateTablesOutput{IntermediateTableSummaries: []crtypes.IntermediateTableSummary{
					crIntermediateTable("m-b", "t3"), {Name: ptrStr("no arn")},
				}}, nil
			case req.NextToken == nil:
				return &cleanrooms.ListIntermediateTablesOutput{IntermediateTableSummaries: []crtypes.IntermediateTableSummary{crIntermediateTable("m-a", "t1")}, NextToken: ptrStr("p2")}, nil
			default:
				return &cleanrooms.ListIntermediateTablesOutput{IntermediateTableSummaries: []crtypes.IntermediateTableSummary{crIntermediateTable("m-a", "t2")}}, nil
			}
		},
	}
	total, _, err := crRunIntermediateTables(context.Background(), crClient(handlers), st, memberships)
	if err != nil {
		t.Fatalf("err = %v", err)
	}
	if total != 3 {
		t.Errorf("total = %d, want 3", total)
	}
	if got, want := calls.distinct(), []string{"m-a", "m-b", "m-gone"}; !slices.Equal(got, want) {
		t.Errorf("memberships queried %v, want %v", got, want)
	}
	for _, n := range maxSizes {
		if n != 100 {
			t.Errorf("MaxResults = %d, want 100", n)
		}
	}
	rows := rowsByNativeID(t, st, TypeCleanRoomsIntermediateTable)
	if len(rows) != 3 {
		t.Errorf("intermediate table rows = %d, want 3", len(rows))
	}
	for _, id := range []string{"m-a/intermediatetable/t1", "m-a/intermediatetable/t2", "m-b/intermediatetable/t3"} {
		if _, ok := rows[crMembershipPrefix+id]; !ok {
			t.Errorf("intermediate table %s missing", id)
		}
		assertContainsNative(t, st, crMembershipPrefix+id[:3], crMembershipPrefix+id)
	}
	t3 := rows[crMembershipPrefix+"m-b/intermediatetable/t3"]
	if t3.Type != TypeCleanRoomsIntermediateTable || sv(t3.Region) != testRegion || sv(t3.Name) != "table t3" ||
		sv(t3.Status) != "POPULATE_SUCCESS" || sv(t3.CreatedAt) != "2026-09-01T08:00:00Z" {
		t.Errorf("row = type %q region %q name %q status %q created %q", t3.Type, sv(t3.Region), sv(t3.Name), sv(t3.Status), sv(t3.CreatedAt))
	}
	var attrs struct {
		MembershipID string `json:"MembershipId"`
	}
	if err := json.Unmarshal([]byte(t3.AttributesJSON), &attrs); err != nil || attrs.MembershipID != "m-b" {
		t.Errorf("attrs MembershipId = %q (%v), want m-b", attrs.MembershipID, err)
	}
	assertContainsEdgesDeclared(t, st)
}

// crNewPhases runs each new phase over two parents.
var crNewPhases = []struct {
	op  string
	run func(context.Context, *cleanrooms.Client, *store.Store) (int, int, error)
}{
	{"ListMembers", func(ctx context.Context, c *cleanrooms.Client, st *store.Store) (int, int, error) {
		return scanCRMembers(ctx, c, newTestAccount(testAccountID), testRegion, st, testScanID, crParents(crCollabPrefix, "c-a", "c-b"))
	}},
	{"ListIntermediateTables", func(ctx context.Context, c *cleanrooms.Client, st *store.Store) (int, int, error) {
		return crRunIntermediateTables(ctx, c, st, crParents(crMembershipPrefix, "m-a", "m-b"))
	}},
}

// crRunIntermediateTables runs the intermediate table phase alone and
// returns its counts and errors as the scan would.
func crRunIntermediateTables(ctx context.Context, c *cleanrooms.Client, st *store.Store, memberships []childParent) (int, int, error) {
	acct := newTestAccount(testAccountID)
	var p phaseTotals
	scanCRIntermediateTables(ctx, c, acct, testRegion, st, testScanID, memberships, crTags(c, acct.ID), &p)
	return p.result(st, acct, testRegion)
}

func crEmptyOutput(op string) any {
	if op == "ListMembers" {
		return &cleanrooms.ListMembersOutput{}
	}
	return &cleanrooms.ListIntermediateTablesOutput{}
}

func TestCleanRoomsChildPhases_Errors(t *testing.T) {
	badInput := &smithy.GenericAPIError{Code: "ValidationException", Message: "bad"}
	for _, p := range crNewPhases {
		t.Run(p.op+"/empty", func(t *testing.T) {
			st := newTestStore(t)
			handlers := map[string]func(any) (any, error){p.op: func(any) (any, error) { return crEmptyOutput(p.op), nil }}
			total, _, err := p.run(context.Background(), crClient(handlers), st)
			if err != nil || total != 0 {
				t.Errorf("total, err = %d, %v; want 0, nil", total, err)
			}
		})
		t.Run(p.op+"/access denied warns once", func(t *testing.T) {
			st := newTestStore(t)
			warned := countOpWarnings(st, "cleanrooms:"+p.op)
			total, _, err := p.run(context.Background(), crClient(nil), st)
			if err != nil || total != 0 {
				t.Errorf("total, err = %d, %v; want 0, nil", total, err)
			}
			if *warned != 1 {
				t.Errorf("warnings = %d, want 1", *warned)
			}
		})
		t.Run(p.op+"/other error propagates", func(t *testing.T) {
			st := newTestStore(t)
			handlers := map[string]func(any) (any, error){p.op: func(any) (any, error) { return nil, badInput }}
			_, _, err := p.run(context.Background(), crClient(handlers), st)
			if !errors.Is(err, badInput) {
				t.Errorf("err = %v, want the ValidationException", err)
			}
		})
	}
}

// crTopLevelHandlers serves one collaboration, one membership, one configured
// table and one child of each new phase, with the ops in override answering
// instead. Every other op is denied.
func crTopLevelHandlers(override map[string]func(any) (any, error)) map[string]func(any) (any, error) {
	handlers := map[string]func(any) (any, error){
		"ListCollaborations": func(any) (any, error) {
			return &cleanrooms.ListCollaborationsOutput{CollaborationList: []crtypes.CollaborationSummary{
				{Arn: ptrStr(crCollabPrefix + "c-a"), Id: ptrStr("c-a"), Name: ptrStr("collab")},
			}}, nil
		},
		"ListConfiguredTables": func(any) (any, error) {
			return &cleanrooms.ListConfiguredTablesOutput{ConfiguredTableSummaries: []crtypes.ConfiguredTableSummary{
				{Arn: ptrStr("arn:aws:cleanrooms:us-east-1:123456789012:configuredtable/ct-1"), Id: ptrStr("ct-1")},
			}}, nil
		},
		"ListMemberships": func(any) (any, error) {
			return &cleanrooms.ListMembershipsOutput{MembershipSummaries: []crtypes.MembershipSummary{
				{Arn: ptrStr(crMembershipPrefix + "m-a"), Id: ptrStr("m-a"), CollaborationName: ptrStr("collab")},
			}}, nil
		},
		"ListMembers": func(any) (any, error) {
			return &cleanrooms.ListMembersOutput{MemberSummaries: []crtypes.MemberSummary{crMember("123456789012", crtypes.MemberStatusActive)}}, nil
		},
		"ListIntermediateTables": func(any) (any, error) {
			return &cleanrooms.ListIntermediateTablesOutput{IntermediateTableSummaries: []crtypes.IntermediateTableSummary{crIntermediateTable("m-a", "t1")}}, nil
		},
		"ListTagsForResource": func(any) (any, error) { return &cleanrooms.ListTagsForResourceOutput{}, nil },
	}
	maps.Copy(handlers, override)
	return handlers
}

// The new phases run over the parents the earlier phases listed, and an
// earlier phase's error neither stops them nor is lost.
func TestScanCleanRoomsIn_RunsNewPhasesOverListedParents(t *testing.T) {
	st := newTestStore(t)
	templateErr := &smithy.GenericAPIError{Code: "ValidationException", Message: "bad"}
	handlers := crTopLevelHandlers(map[string]func(any) (any, error){
		"ListAnalysisTemplates": func(any) (any, error) { return nil, templateErr },
	})
	total, _, err := scanCleanRoomsIn(context.Background(), crClient(handlers), newTestAccount(testAccountID), testRegion, st, testScanID)
	if !errors.Is(err, templateErr) {
		t.Errorf("err = %v, want the ListAnalysisTemplates error", err)
	}
	// 1 collaboration + 1 configured table + 1 membership + 1 member + 1 intermediate table.
	if total != 5 {
		t.Errorf("total = %d, want 5", total)
	}
	assertContainsNative(t, st, crCollabPrefix+"c-a", crCollabPrefix+"c-a/member/123456789012")
	assertContainsNative(t, st, crMembershipPrefix+"m-a", crMembershipPrefix+"m-a/intermediatetable/t1")
	assertContainsEdgesDeclared(t, st)
}

// Every new phase runs after the existing phases: their rows are stored and
// counted when every new phase fails, and each new phase's error is returned.
func TestScanCleanRoomsIn_NewPhaseErrorsKeepEarlierRows(t *testing.T) {
	st := newTestStore(t)
	override := map[string]func(any) (any, error){}
	var phaseErrs []error
	for _, p := range crNewPhases {
		perr := &smithy.GenericAPIError{Code: "ValidationException", Message: p.op}
		phaseErrs = append(phaseErrs, perr)
		override[p.op] = func(any) (any, error) { return nil, perr }
	}
	total, _, err := scanCleanRoomsIn(context.Background(), crClient(crTopLevelHandlers(override)), newTestAccount(testAccountID), testRegion, st, testScanID)
	for i, perr := range phaseErrs {
		if !errors.Is(err, perr) {
			t.Errorf("err = %v, want it to carry the %s error", err, crNewPhases[i].op)
		}
	}
	if total != 3 {
		t.Errorf("total = %d, want 3 (collaboration + configured table + membership)", total)
	}
	for _, typ := range []string{TypeCleanRoomsCollaboration, TypeCleanRoomsConfiguredTable, TypeCleanRoomsMembership} {
		if n := len(rowsByNativeID(t, st, typ)); n != 1 {
			t.Errorf("%s rows = %d, want 1", typ, n)
		}
	}
}

// Beside another phase's hard error, a later phase's transient error is
// reported as a warning under its phase instead of inside the hard error, and
// the phases after it still run.
func TestScanCleanRoomsIn_TransientPhaseDoesNotMaskOthers(t *testing.T) {
	st := newTestStore(t)
	warned := countOpWarnings(st, "cleanrooms:ListMembers")
	tableErr := &smithy.GenericAPIError{Code: "ValidationException", Message: "bad"}
	handlers := crTopLevelHandlers(map[string]func(any) (any, error){
		"ListMembers":          func(any) (any, error) { return nil, crThrottled },
		"ListConfiguredTables": func(any) (any, error) { return nil, tableErr },
	})
	total, _, err := scanCleanRoomsIn(context.Background(), crClient(handlers), newTestAccount(testAccountID), testRegion, st, testScanID)
	if got := classifyServiceError(err); err == nil || got != outcomeError {
		t.Errorf("classifyServiceError(%v) = %v, want outcomeError", err, got)
	}
	if !errors.Is(err, tableErr) {
		t.Errorf("err = %v, want the ListConfiguredTables error", err)
	}
	if *warned != 1 {
		t.Errorf("ListMembers transient warnings = %d, want 1", *warned)
	}
	// 1 collaboration + 1 membership + 1 intermediate table.
	if total != 3 {
		t.Errorf("total = %d, want 3", total)
	}
}

// In a region with no Clean Rooms endpoint the first phase's NXDOMAIN is
// returned alone, so the dispatcher reports the region unavailable.
func TestScanCleanRoomsIn_UnreachableEndpointStopsTheScan(t *testing.T) {
	st := newTestStore(t)
	var warnings []store.ScanWarning
	st.OnWarn = func(w store.ScanWarning) { warnings = append(warnings, w) }
	calls := &parentCalls{}
	handlers := map[string]func(any) (any, error){}
	for op := range crTopLevelHandlers(nil) {
		handlers[op] = func(any) (any, error) {
			calls.add(op)
			return nil, &net.DNSError{Err: "no such host", Name: "cleanrooms.us-east-1.amazonaws.com", IsNotFound: true}
		}
	}
	total, _, err := scanCleanRoomsIn(context.Background(), crClient(handlers), newTestAccount(testAccountID), testRegion, st, testScanID)
	if got := classifyServiceError(err); got != outcomeUnavailable {
		t.Errorf("classifyServiceError(%v) = %v, want outcomeUnavailable", err, got)
	}
	if got, want := calls.distinct(), []string{"ListCollaborations"}; !slices.Equal(got, want) {
		t.Errorf("ops called %v, want %v", got, want)
	}
	if total != 0 || len(warnings) != 0 {
		t.Errorf("total, warnings = %d, %v; want 0, none", total, warnings)
	}
}

// A later phase's transient error alone is returned, so the scan is marked
// partial rather than complete, with the other phases' rows counted.
func TestScanCleanRoomsIn_LoneTransientPhaseMarksPartial(t *testing.T) {
	st := newTestStore(t)
	warned := countOpWarnings(st, "cleanrooms:ListMembers")
	handlers := crTopLevelHandlers(map[string]func(any) (any, error){
		"ListMembers": func(any) (any, error) { return nil, crThrottled },
	})
	total, _, err := scanCleanRoomsIn(context.Background(), crClient(handlers), newTestAccount(testAccountID), testRegion, st, testScanID)
	if got := classifyServiceError(err); got != outcomeTransient {
		t.Errorf("classifyServiceError(%v) = %v, want outcomeTransient", err, got)
	}
	if *warned != 0 {
		t.Errorf("ListMembers transient warnings = %d, want 0", *warned)
	}
	if stored := countStoredRows(t, st); total == 0 || total != stored {
		t.Errorf("total = %d, stored rows = %d; want the other phases' rows counted", total, stored)
	}
}

// A later phase's NXDOMAIN is flattened to a plain error, so the dispatcher
// does not report the region unavailable and drop the rows already stored.
func TestScanCleanRoomsIn_LatePhaseNXDOMAINKeepsCounts(t *testing.T) {
	st := newTestStore(t)
	handlers := crTopLevelHandlers(map[string]func(any) (any, error){
		"ListMembers": func(any) (any, error) {
			return nil, &net.DNSError{Err: "no such host", Name: "cleanrooms.us-east-1.amazonaws.com", IsNotFound: true}
		},
	})
	total, _, err := scanCleanRoomsIn(context.Background(), crClient(handlers), newTestAccount(testAccountID), testRegion, st, testScanID)
	if got := classifyServiceError(err); err == nil || got != outcomeError {
		t.Errorf("classifyServiceError(%v) = %v, want outcomeError", err, got)
	}
	if stored := countStoredRows(t, st); total == 0 || total != stored {
		t.Errorf("total = %d, stored rows = %d; want the other phases' rows counted", total, stored)
	}
}
