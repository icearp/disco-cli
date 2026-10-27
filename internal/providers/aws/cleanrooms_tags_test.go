package aws

import (
	"context"
	"errors"
	"net"
	"slices"
	"strings"
	"testing"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cleanrooms"
	crtypes "github.com/aws/aws-sdk-go-v2/service/cleanrooms/types"
	"github.com/aws/smithy-go"
	"github.com/icearp/disco-cli/store"
)

// The fixture: every account-wide listing serves "-t" (tagged) on its first
// page and "-b" (bare) on its second; the second page of collaborations also
// carries c-x, created by crtForeignAccount, and that of configured tables
// ct-gov-t, the account's own in aws-us-gov. Each membership holds one item of
// every per-membership kind, and each collaboration one member, the account
// itself. ListTagsForResource answers with tags for every ARN ending in "-t"
// and for c-x.

// crtForeignAccount created the collaboration the account was invited to.
const crtForeignAccount = "210987654321"

var crtBadInput = &smithy.GenericAPIError{Code: "ValidationException", Message: "bad"}

func crtARN(res string) string { return arnFor("aws", "cleanrooms", testRegion, testAccountID, res) }

func crtForeignCollab() string {
	return arnFor("aws", "cleanrooms", testRegion, crtForeignAccount, "collaboration/c-x")
}

func crtGovTable() string {
	return arnFor("aws-us-gov", "cleanrooms", "us-gov-west-1", testAccountID, "configuredtable/ct-gov-t")
}

func crtTagged(arn string) bool { return strings.HasSuffix(arn, "-t") || arn == crtForeignCollab() }

// crtKinds is every per-membership listing: the op, the ARN segment and the
// type of its items.
var crtKinds = []struct{ op, kind, typ string }{
	{"ListAnalysisTemplates", "analysistemplate", TypeCleanRoomsAnalysisTemplate},
	{"ListConfiguredTableAssociations", "configuredtableassociation", TypeCleanRoomsConfiguredTableAssociation},
	{"ListConfiguredAudienceModelAssociations", "configuredaudiencemodelassociation", TypeCleanRoomsConfiguredAudienceModelAssociation},
	{"ListIdMappingTables", "idmappingtable", TypeCleanRoomsIDMappingTable},
	{"ListIdNamespaceAssociations", "idnamespaceassociation", TypeCleanRoomsIDNamespaceAssociation},
	{"ListPrivacyBudgetTemplates", "privacybudgettemplate", TypeCleanRoomsPrivacyBudgetTemplate},
	{"ListIntermediateTables", "intermediatetable", TypeCleanRoomsIntermediateTable},
}

// crtChild is the ARN of membership m's item of kind; its id is
// "<kind>-<m's suffix>", so it is tagged when m is. Analysis template ids are
// UUIDs, as real ids are, so their ARNs exceed the 100 characters the model
// caps ListTagsForResource's resourceArn at.
func crtChild(m, kind string) string {
	id := kind
	if kind == "analysistemplate" {
		id = "0f4b5a6c-7d8e-4f90-a1b2-c3d4e5f60718"
	}
	return crtARN("membership/" + m + "/" + kind + "/" + id + strings.TrimPrefix(m, "m"))
}

func crtMember(collab string) string { return collab + "/member/" + testAccountID }

type crtRow struct{ typ, arn string }

// crtRows is every row the fixture stores.
func crtRows() []crtRow {
	rows := []crtRow{{TypeCleanRoomsCollaboration, crtForeignCollab()}, {TypeCleanRoomsConfiguredTable, crtGovTable()}}
	for _, n := range []string{"t", "b"} {
		m := "m-" + n
		rows = append(rows,
			crtRow{TypeCleanRoomsCollaboration, crtARN("collaboration/c-" + n)},
			crtRow{TypeCleanRoomsConfiguredTable, crtARN("configuredtable/ct-" + n)},
			crtRow{TypeCleanRoomsMembership, crtARN("membership/" + m)})
		for _, k := range crtKinds {
			rows = append(rows, crtRow{k.typ, crtChild(m, k.kind)})
		}
	}
	for _, c := range []string{crtARN("collaboration/c-t"), crtARN("collaboration/c-b"), crtForeignCollab()} {
		rows = append(rows, crtRow{TypeCleanRoomsMember, crtMember(c)})
	}
	return rows
}

// crtCalls records the stubbed calls and answers ListTagsForResource from
// tags and errs, keyed by ARN.
type crtCalls struct{ *bacCalls }

func newCRTCalls() *crtCalls {
	c := &crtCalls{newBACCalls()}
	for _, r := range crtRows() {
		if crtTagged(r.arn) && r.typ != TypeCleanRoomsMember {
			c.tags[r.arn] = map[string]string{"type": r.typ}
		}
	}
	return c
}

func (c *crtCalls) listTags(in any) (any, error) {
	arn := sv(in.(*cleanrooms.ListTagsForResourceInput).ResourceArn)
	c.mu.Lock()
	c.tagARNs = append(c.tagARNs, arn)
	err, tags := c.errs[arn], c.tags[arn]
	c.mu.Unlock()
	if err != nil {
		return nil, err
	}
	return &cleanrooms.ListTagsForResourceOutput{Tags: tags}, nil
}

// twoPages answers op with "t" on the first page and "b" on the second.
func (c *crtCalls) twoPages(op string, out func(n string, next *string) any) func(any) (any, error) {
	return c.answer(op, func(in any) any {
		if bacField[*string](in, "NextToken") == nil {
			return out("t", sdkaws.String("p2"))
		}
		return out("b", nil)
	})
}

// crtChildOut is op's one page for membership m, the item of kind.
func crtChildOut(op, m, kind string) any {
	arn := crtChild(m, kind)
	id := arn[strings.LastIndex(arn, "/")+1:]
	name := sdkaws.String("name " + id)
	switch op {
	case "ListAnalysisTemplates":
		return &cleanrooms.ListAnalysisTemplatesOutput{AnalysisTemplateSummaries: []crtypes.AnalysisTemplateSummary{{Arn: &arn, Id: &id, Name: name}}}
	case "ListConfiguredTableAssociations":
		return &cleanrooms.ListConfiguredTableAssociationsOutput{ConfiguredTableAssociationSummaries: []crtypes.ConfiguredTableAssociationSummary{{Arn: &arn, Id: &id, Name: name}}}
	case "ListConfiguredAudienceModelAssociations":
		return &cleanrooms.ListConfiguredAudienceModelAssociationsOutput{ConfiguredAudienceModelAssociationSummaries: []crtypes.ConfiguredAudienceModelAssociationSummary{{Arn: &arn, Id: &id, Name: name}}}
	case "ListIdMappingTables":
		return &cleanrooms.ListIdMappingTablesOutput{IdMappingTableSummaries: []crtypes.IdMappingTableSummary{{Arn: &arn, Id: &id, Name: name}}}
	case "ListIdNamespaceAssociations":
		return &cleanrooms.ListIdNamespaceAssociationsOutput{IdNamespaceAssociationSummaries: []crtypes.IdNamespaceAssociationSummary{{Arn: &arn, Id: &id, Name: name}}}
	case "ListPrivacyBudgetTemplates":
		return &cleanrooms.ListPrivacyBudgetTemplatesOutput{PrivacyBudgetTemplateSummaries: []crtypes.PrivacyBudgetTemplateSummary{{Arn: &arn, Id: &id}}}
	default:
		return &cleanrooms.ListIntermediateTablesOutput{IntermediateTableSummaries: []crtypes.IntermediateTableSummary{{Arn: &arn, Id: &id, Name: name}}}
	}
}

// crtHandlers answers every op the scan calls with the fixture (crtRows).
func crtHandlers(c *crtCalls) map[string]func(any) (any, error) {
	h := map[string]func(any) (any, error){
		"ListTagsForResource": c.listTags,
		"ListCollaborations": c.twoPages("ListCollaborations", func(n string, next *string) any {
			items := []crtypes.CollaborationSummary{{Arn: sdkaws.String(crtARN("collaboration/c-" + n)), Id: sdkaws.String("c-" + n), Name: sdkaws.String("collab " + n)}}
			if n == "b" {
				items = append(items, crtypes.CollaborationSummary{Arn: sdkaws.String(crtForeignCollab()), Id: sdkaws.String("c-x"), Name: sdkaws.String("collab x")})
			}
			return &cleanrooms.ListCollaborationsOutput{CollaborationList: items, NextToken: next}
		}),
		"ListConfiguredTables": c.twoPages("ListConfiguredTables", func(n string, next *string) any {
			items := []crtypes.ConfiguredTableSummary{{Arn: sdkaws.String(crtARN("configuredtable/ct-" + n)), Id: sdkaws.String("ct-" + n), Name: sdkaws.String("table " + n)}}
			if n == "b" {
				items = append(items, crtypes.ConfiguredTableSummary{Arn: sdkaws.String(crtGovTable()), Id: sdkaws.String("ct-gov-t")})
			}
			return &cleanrooms.ListConfiguredTablesOutput{ConfiguredTableSummaries: items, NextToken: next}
		}),
		"ListMemberships": c.twoPages("ListMemberships", func(n string, next *string) any {
			return &cleanrooms.ListMembershipsOutput{MembershipSummaries: []crtypes.MembershipSummary{
				{Arn: sdkaws.String(crtARN("membership/m-" + n)), Id: sdkaws.String("m-" + n), CollaborationName: sdkaws.String("collab " + n)},
			}, NextToken: next}
		}),
		"ListMembers": c.answer("ListMembers", func(in any) any {
			collab := sv(in.(*cleanrooms.ListMembersInput).CollaborationIdentifier)
			return &cleanrooms.ListMembersOutput{MemberSummaries: []crtypes.MemberSummary{
				{AccountId: sdkaws.String(testAccountID), DisplayName: sdkaws.String("member of " + collab), Status: crtypes.MemberStatusActive},
			}}
		}),
	}
	for _, k := range crtKinds {
		h[k.op] = c.answer(k.op, func(in any) any {
			return crtChildOut(k.op, sv(bacField[*string](in, "MembershipIdentifier")), k.kind)
		})
	}
	return h
}

// crtScan runs the scan's real entry point against handlers; every op they
// do not answer is denied.
func crtScan(ctx context.Context, t *testing.T, st *store.Store, handlers map[string]func(any) (any, error)) (int, int, error) {
	t.Helper()
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(handlers, crDenied)
	return scanCleanRooms(ctx, acct, testRegion, st, testScanID)
}

func crtRowOf(t *testing.T, st *store.Store, typ, arn string) (store.Resource, bool) {
	t.Helper()
	r, ok := rowsByNativeID(t, st, typ)[arn]
	return r, ok
}

// Every type but the member stores the tags ListTagsForResource returns for
// its ARN, and nil for a resource without tags; every stored row but the
// members is sent to the tag op, the other account's collaboration and the
// aws-us-gov table included. Both pages of every account-wide listing are
// stored, every listing asks for the model's page cap of 100, and the scan
// reports no error or warning.
func TestScanCleanRooms_StoresTagsOfEveryType(t *testing.T) {
	st := newTestStore(t)
	warns := countWarnings(st)
	calls := newCRTCalls()

	total, inserted, err := crtScan(context.Background(), t, st, crtHandlers(calls))
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	rows := crtRows()
	if total != len(rows) || inserted != len(rows) || len(rows) != 25 {
		t.Errorf("total, inserted = %d, %d; want %d (25 fixture rows)", total, inserted, len(rows))
	}
	if *warns != 0 {
		t.Errorf("warnings = %d; want 0", *warns)
	}
	var wantTagARNs []string
	for _, want := range rows {
		r, ok := crtRowOf(t, st, want.typ, want.arn)
		if !ok {
			t.Errorf("%s %s not stored", want.typ, want.arn)
			continue
		}
		if want.typ == TypeCleanRoomsMember {
			if r.TagsJSON != nil {
				t.Errorf("member %s TagsJSON = %s; want nil", want.arn, *r.TagsJSON)
			}
			continue
		}
		wantTagARNs = append(wantTagARNs, want.arn)
		if crtTagged(want.arn) {
			assertTagsMap(t, r.TagsJSON, map[string]string{"type": want.typ})
		} else if r.TagsJSON != nil {
			t.Errorf("%s %s TagsJSON = %s; want nil", want.typ, want.arn, *r.TagsJSON)
		}
	}
	got := slices.Clone(calls.tagARNs)
	slices.Sort(got)
	slices.Sort(wantTagARNs)
	if !slices.Equal(got, wantTagARNs) {
		t.Errorf("ListTagsForResource ARNs = %v; want %v", got, wantTagARNs)
	}
	if long := crtChild("m-t", "analysistemplate"); len(long) <= 100 || !slices.Contains(got, long) {
		t.Errorf("ARN %s (%d chars) sent = %v; want a UUID-length ARN over 100 chars sent", long, len(long), slices.Contains(got, long))
	}
	ops := []string{"ListCollaborations", "ListConfiguredTables", "ListMemberships", "ListMembers"}
	for _, k := range crtKinds {
		ops = append(ops, k.op)
	}
	for _, op := range ops {
		caps := calls.maxResults[op]
		if len(caps) == 0 {
			t.Errorf("%s sent no MaxResults", op)
		}
		for _, n := range caps {
			if n != 100 {
				t.Errorf("%s MaxResults = %d; want 100", op, n)
			}
		}
	}
	// Name is the list element's name, else its Id; the membership's is its
	// collaboration's name, the privacy budget template's its Id, the
	// member's its display name.
	for _, n := range []struct{ typ, arn, name string }{
		{TypeCleanRoomsCollaboration, crtARN("collaboration/c-t"), "collab t"},
		{TypeCleanRoomsConfiguredTable, crtARN("configuredtable/ct-t"), "table t"},
		{TypeCleanRoomsConfiguredTable, crtGovTable(), "ct-gov-t"},
		{TypeCleanRoomsMembership, crtARN("membership/m-t"), "collab t"},
		{TypeCleanRoomsAnalysisTemplate, crtChild("m-t", "analysistemplate"), "name 0f4b5a6c-7d8e-4f90-a1b2-c3d4e5f60718-t"},
		{TypeCleanRoomsConfiguredTableAssociation, crtChild("m-t", "configuredtableassociation"), "name configuredtableassociation-t"},
		{TypeCleanRoomsConfiguredAudienceModelAssociation, crtChild("m-t", "configuredaudiencemodelassociation"), "name configuredaudiencemodelassociation-t"},
		{TypeCleanRoomsIDMappingTable, crtChild("m-t", "idmappingtable"), "name idmappingtable-t"},
		{TypeCleanRoomsIDNamespaceAssociation, crtChild("m-t", "idnamespaceassociation"), "name idnamespaceassociation-t"},
		{TypeCleanRoomsPrivacyBudgetTemplate, crtChild("m-t", "privacybudgettemplate"), "privacybudgettemplate-t"},
		{TypeCleanRoomsIntermediateTable, crtChild("m-t", "intermediatetable"), "name intermediatetable-t"},
		{TypeCleanRoomsMember, crtMember(crtForeignCollab()), "member of c-x"},
	} {
		if r, _ := crtRowOf(t, st, n.typ, n.arn); sv(r.Name) != n.name {
			t.Errorf("%s %s Name = %q; want %q", n.typ, n.arn, sv(r.Name), n.name)
		}
	}
	assertContainsNative(t, st, crtARN("membership/m-b"), crtChild("m-b", "intermediatetable"))
	assertContainsNative(t, st, crtForeignCollab(), crtMember(crtForeignCollab()))
}

// A rescan stores the same rows without inserting any.
func TestScanCleanRooms_RescanInsertsNone(t *testing.T) {
	st := newTestStore(t)
	calls := newCRTCalls()
	if _, _, err := crtScan(context.Background(), t, st, crtHandlers(calls)); err != nil {
		t.Fatalf("first scan: %v", err)
	}
	total, inserted, err := crtScan(context.Background(), t, st, crtHandlers(calls))
	if err != nil {
		t.Fatalf("rescan: %v", err)
	}
	if total != 25 || inserted != 0 {
		t.Errorf("rescan total, inserted = %d, %d; want 25, 0", total, inserted)
	}
}

// A failed tag read keeps the row without tags unless the resource is gone:
// a denial warns once under cleanrooms:ListTagsForResource, a throttle after
// retries is a transient error, any other code a hard one. A membership the
// tag read no longer finds is dropped, and its children are never listed; an
// analysis template, from a per-membership listing, likewise.
func TestScanCleanRooms_TagReadFailures(t *testing.T) {
	for _, target := range []crtRow{
		{TypeCleanRoomsMembership, crtARN("membership/m-t")},
		{TypeCleanRoomsAnalysisTemplate, crtChild("m-t", "analysistemplate")},
	} {
		t.Run(target.typ, func(t *testing.T) {
			for _, c := range []struct {
				name      string
				err       error
				kept      bool
				outcome   serviceOutcome
				wantWarns int
			}{
				{"denied", crDenied, true, 0, 1},
				{"not found", crNotFound, false, 0, 0},
				{"other error", crtBadInput, true, outcomeError, 0},
				{"transient", crThrottled, true, outcomeTransient, 0},
			} {
				t.Run(c.name, func(t *testing.T) {
					st := newTestStore(t)
					warnings := bacWarnings(st, crListTagsOp)
					calls := newCRTCalls()
					calls.errs[target.arn] = c.err

					_, _, err := crtScan(context.Background(), t, st, crtHandlers(calls))
					if c.outcome == 0 && err != nil {
						t.Fatalf("err = %v; want nil", err)
					}
					if c.outcome != 0 {
						assertGlueOutcome(t, err, c.outcome)
					}
					if c.outcome == outcomeError {
						assertAPICode(t, err, "ValidationException")
					}
					r, ok := crtRowOf(t, st, target.typ, target.arn)
					if ok != c.kept {
						t.Fatalf("%s row stored = %v; want %v", target.typ, ok, c.kept)
					}
					if ok && r.TagsJSON != nil {
						t.Errorf("TagsJSON = %q; want nil", *r.TagsJSON)
					}
					if target.typ == TypeCleanRoomsMembership {
						_, child := crtRowOf(t, st, TypeCleanRoomsIntermediateTable, crtChild("m-t", "intermediatetable"))
						_, sibling := crtRowOf(t, st, TypeCleanRoomsIntermediateTable, crtChild("m-b", "intermediatetable"))
						if child != c.kept || !sibling {
							t.Errorf("intermediate tables stored of m-t %v, m-b %v; want m-t's only when it is kept, m-b's always", child, sibling)
						}
					}
					if len(*warnings) != c.wantWarns {
						t.Fatalf("%s warnings = %v; want %d", crListTagsOp, *warnings, c.wantWarns)
					}
					for _, w := range *warnings {
						if w.Scope != testAccountID+"/"+testRegion {
							t.Errorf("warning Scope = %q; want %s/%s", w.Scope, testAccountID, testRegion)
						}
					}
				})
			}
		})
	}
}

// A throttled tag read beside a hard failure of the same op is demoted to
// one warning, so the hard error decides the outcome.
func TestScanCleanRooms_TagThrottleDemotedBesideHardError(t *testing.T) {
	st := newTestStore(t)
	warns := countOpWarnings(st, crListTagsOp)
	calls := newCRTCalls()
	calls.errs[crtARN("configuredtable/ct-t")] = crThrottled
	calls.errs[crtChild("m-b", "idmappingtable")] = crtBadInput

	_, _, err := crtScan(context.Background(), t, st, crtHandlers(calls))
	assertGlueOutcome(t, err, outcomeError)
	assertAPICode(t, err, "ValidationException")
	if *warns != 1 {
		t.Errorf("%s warnings = %d; want 1", crListTagsOp, *warns)
	}
}

// A denied listing warns once under its own op and costs no other phase its
// rows.
func TestScanCleanRooms_DeniedListingWarnsUnderItsOp(t *testing.T) {
	ops := []string{"ListCollaborations", "ListConfiguredTables", "ListMemberships", "ListMembers"}
	for _, k := range crtKinds {
		ops = append(ops, k.op)
	}
	for _, op := range ops {
		t.Run(op, func(t *testing.T) {
			st := newTestStore(t)
			warns := countWarnings(st)
			opWarns := countOpWarnings(st, "cleanrooms:"+op)
			h := crtHandlers(newCRTCalls())
			h[op] = func(any) (any, error) { return nil, crDenied }

			total, _, err := crtScan(context.Background(), t, st, h)
			if err != nil {
				t.Fatalf("err = %v; want nil", err)
			}
			if *warns != 1 || *opWarns != 1 {
				t.Errorf("warnings = %d (cleanrooms:%s: %d); want 1 under the op", *warns, op, *opWarns)
			}
			if stored := countStoredRows(t, st); total == 0 || total != stored {
				t.Errorf("total = %d, stored rows = %d; want the other phases' rows counted", total, stored)
			}
		})
	}
}

// A failed write stops the scan before any later listing and sends no
// warning about the rows it lost.
func TestScanCleanRooms_FailedWriteStopsWithoutWarning(t *testing.T) {
	st := newTestStore(t)
	warns := countWarnings(st)
	refuseStoreWrites(t, st, TypeCleanRoomsMembership)
	calls := newCRTCalls()
	calls.errs[crtARN("membership/m-t")] = crDenied

	_, _, err := crtScan(context.Background(), t, st, crtHandlers(calls))
	if !errors.Is(err, store.ErrStoreWrite) {
		t.Fatalf("err = %v; want store.ErrStoreWrite", err)
	}
	if *warns != 0 {
		t.Errorf("warnings = %d; want 0", *warns)
	}
	for _, op := range []string{"ListAnalysisTemplates", "ListMembers", "ListIntermediateTables"} {
		if n := calls.count(op); n != 0 {
			t.Errorf("%s called %d times after the failed write", op, n)
		}
	}
}

// A listing's failed page keeps the pages read before it, and no listing's
// failure stops the phases after it. A membership a child listing no longer
// finds is skipped silently, its sibling's children stored.
func TestScanCleanRooms_ListFailureKeepsPagesAndRunsEveryPhase(t *testing.T) {
	st := newTestStore(t)
	warns := countWarnings(st)
	calls := newCRTCalls()
	h := crtHandlers(calls)
	h["ListCollaborations"] = failOnCall(2, apiErr("ConflictException", "page 2"), h["ListCollaborations"])
	h["ListConfiguredTables"] = func(any) (any, error) { return nil, crtBadInput }
	templates := h["ListAnalysisTemplates"]
	h["ListAnalysisTemplates"] = func(in any) (any, error) {
		if sv(in.(*cleanrooms.ListAnalysisTemplatesInput).MembershipIdentifier) == "m-t" {
			return nil, crNotFound
		}
		return templates(in)
	}

	_, _, err := crtScan(context.Background(), t, st, h)
	for _, code := range []string{"ConflictException", "ValidationException"} {
		if !joinCarriesAPIErrorCode(err, code) {
			t.Errorf("err = %v; want it to carry %s", err, code)
		}
	}
	if *warns != 0 {
		t.Errorf("warnings = %d; want 0", *warns)
	}
	for _, want := range []crtRow{
		{TypeCleanRoomsCollaboration, crtARN("collaboration/c-t")},
		{TypeCleanRoomsMember, crtMember(crtARN("collaboration/c-t"))},
		{TypeCleanRoomsAnalysisTemplate, crtChild("m-b", "analysistemplate")},
		{TypeCleanRoomsIntermediateTable, crtChild("m-t", "intermediatetable")},
	} {
		if _, ok := crtRowOf(t, st, want.typ, want.arn); !ok {
			t.Errorf("%s %s missing", want.typ, want.arn)
		}
	}
	for _, gone := range []crtRow{
		{TypeCleanRoomsCollaboration, crtARN("collaboration/c-b")},
		{TypeCleanRoomsAnalysisTemplate, crtChild("m-t", "analysistemplate")},
	} {
		if _, ok := crtRowOf(t, st, gone.typ, gone.arn); ok {
			t.Errorf("%s %s stored; want it not read", gone.typ, gone.arn)
		}
	}
}

// Items listed without an ARN, or a member without an account id, are not
// stored and are warned about once per listing, under a key that is never
// empty.
func TestScanCleanRooms_UnstorableItemsWarn(t *testing.T) {
	for _, c := range []struct{ op, key string }{
		{"ListConfiguredTables", "(a configured table with no name)"},
		{"ListPrivacyBudgetTemplates", "(a privacy budget template with no id) of membership m-"},
		{"ListMembers", "(a member with no name) of collaboration arn:"},
	} {
		t.Run(c.op, func(t *testing.T) {
			st := newTestStore(t)
			warnings := bacWarnings(st, "cleanrooms:"+c.op)
			h := crtHandlers(newCRTCalls())
			h[c.op] = func(any) (any, error) {
				switch c.op {
				case "ListConfiguredTables":
					return &cleanrooms.ListConfiguredTablesOutput{ConfiguredTableSummaries: []crtypes.ConfiguredTableSummary{{}, {}}}, nil
				case "ListPrivacyBudgetTemplates":
					return &cleanrooms.ListPrivacyBudgetTemplatesOutput{PrivacyBudgetTemplateSummaries: []crtypes.PrivacyBudgetTemplateSummary{{}}}, nil
				default:
					return &cleanrooms.ListMembersOutput{MemberSummaries: []crtypes.MemberSummary{{}}}, nil
				}
			}

			if _, _, err := crtScan(context.Background(), t, st, h); err != nil {
				t.Fatalf("err = %v; want nil", err)
			}
			if len(*warnings) != 1 || !strings.Contains((*warnings)[0].Message, c.key) {
				t.Errorf("warnings = %v; want one naming %q", *warnings, c.key)
			}
		})
	}
}

// A repeated token ends the listing as an error, keeping its pages.
func TestScanCleanRooms_RepeatedTokenKeepsPages(t *testing.T) {
	st := newTestStore(t)
	h := crtHandlers(newCRTCalls())
	h["ListConfiguredTables"] = func(any) (any, error) {
		return &cleanrooms.ListConfiguredTablesOutput{ConfiguredTableSummaries: []crtypes.ConfiguredTableSummary{{Arn: sdkaws.String(crtARN("configuredtable/ct-t"))}}, NextToken: sdkaws.String("same")}, nil
	}

	_, _, err := crtScan(context.Background(), t, st, h)
	if !errors.Is(err, errRepeatedNextToken) {
		t.Fatalf("err = %v; want errRepeatedNextToken", err)
	}
	if _, ok := crtRowOf(t, st, TypeCleanRoomsConfiguredTable, crtARN("configuredtable/ct-t")); !ok {
		t.Error("configured table from the pages read missing")
	}
}

// Once ctx ends no further phase starts, and the stop is reported once,
// naming the phases not run, although the phase in flight saw it in its
// listing and its tag reads.
func TestScanCleanRooms_ContextEndStopsOnce(t *testing.T) {
	st := newTestStore(t)
	calls := newCRTCalls()
	h := crtHandlers(calls)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	memberships := h["ListMemberships"]
	h["ListMemberships"] = func(in any) (any, error) {
		if in.(*cleanrooms.ListMembershipsInput).NextToken != nil {
			cancel()
			return nil, context.Canceled
		}
		return memberships(in)
	}

	_, _, err := crtScan(ctx, t, st, h)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v; want context.Canceled", err)
	}
	stops := 0
	for _, m := range bacTopMembers(err) {
		if errors.Is(m, context.Canceled) {
			stops++
		}
	}
	if stops != 1 {
		t.Errorf("members carrying the stop = %d; want 1 (err %v)", stops, err)
	}
	if n := calls.count("ListAnalysisTemplates"); n != 0 {
		t.Errorf("ListAnalysisTemplates called %d times after ctx ended", n)
	}
	// Memberships are phase 3 of 11, so 8 never started.
	if !strings.Contains(err.Error(), "cleanrooms: 8 phases not run") {
		t.Errorf("err = %v; want it to say 8 phases not run", err)
	}
}

// An NXDOMAIN after rows were stored is a failure of this scan, not a region
// gap: the counts are kept. An unreachable endpoint on the first listing
// stops the scan; on that listing's tag reads it does not, since the listing
// reached the endpoint.
func TestScanCleanRooms_Unreachable(t *testing.T) {
	nx := &net.DNSError{Err: "no such host", Name: "cleanrooms.us-east-1.amazonaws.com", IsNotFound: true}
	refused := &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connection refused")}
	t.Run("tag read after rows were listed", func(t *testing.T) {
		st := newTestStore(t)
		calls := newCRTCalls()
		calls.errs[crtARN("configuredtable/ct-t")] = nx
		total, _, err := crtScan(context.Background(), t, st, crtHandlers(calls))
		assertGlueOutcome(t, err, outcomeError)
		if total != 25 {
			t.Errorf("total = %d; want 25", total)
		}
	})
	t.Run("first listing's tag read", func(t *testing.T) {
		st := newTestStore(t)
		calls := newCRTCalls()
		calls.errs[crtARN("collaboration/c-t")] = refused
		total, _, err := crtScan(context.Background(), t, st, crtHandlers(calls))
		if err == nil {
			t.Fatal("err = nil; want the refused tag read")
		}
		if total != 25 || calls.count("ListIntermediateTables") == 0 {
			t.Errorf("total = %d, ListIntermediateTables calls = %d; want 25 and every phase run", total, calls.count("ListIntermediateTables"))
		}
	})
	t.Run("first listing refused after a stored page", func(t *testing.T) {
		st := newTestStore(t)
		calls := newCRTCalls()
		h := crtHandlers(calls)
		h["ListCollaborations"] = failOnCall(2, refused, h["ListCollaborations"])
		total, _, err := crtScan(context.Background(), t, st, h)
		if !errors.Is(err, refused) {
			t.Errorf("err = %v; want the refused dial", err)
		}
		if _, ok := crtRowOf(t, st, TypeCleanRoomsCollaboration, crtARN("collaboration/c-t")); !ok {
			t.Error("collaboration from page 1 missing")
		}
		for _, op := range []string{"ListConfiguredTables", "ListMembers", "ListIntermediateTables"} {
			if calls.count(op) == 0 {
				t.Errorf("%s not called; want every later phase run", op)
			}
		}
		// The fixture's 25 rows less c-b, c-x and their members.
		if total != 21 {
			t.Errorf("total = %d; want 21", total)
		}
	})
	t.Run("first listing refused", func(t *testing.T) {
		st := newTestStore(t)
		calls := newCRTCalls()
		h := crtHandlers(calls)
		h["ListCollaborations"] = func(any) (any, error) { return nil, refused }
		total, _, err := crtScan(context.Background(), t, st, h)
		if !errors.Is(err, refused) {
			t.Errorf("err = %v; want the refused dial", err)
		}
		if total != 0 || calls.count("ListConfiguredTables") != 0 {
			t.Errorf("total = %d, ListConfiguredTables calls = %d; want 0, 0", total, calls.count("ListConfiguredTables"))
		}
	})
}

// A denied or not-found tag read of a collaboration another account created,
// and a denied listing of its members, keep what was read and are not warned
// about: the resource is its owner's. The same on the account's own
// resources, in any partition, still warns once, or drops the row when gone.
func TestScanCleanRooms_OtherAccountsCollaboration(t *testing.T) {
	ownCollab := crtARN("collaboration/c-t")
	denyMembers := func(collab string) func(*crtCalls, map[string]func(any) (any, error)) {
		return func(_ *crtCalls, h map[string]func(any) (any, error)) {
			next := h["ListMembers"]
			h["ListMembers"] = func(in any) (any, error) {
				if sv(in.(*cleanrooms.ListMembersInput).CollaborationIdentifier) == collab[strings.LastIndex(collab, "/")+1:] {
					return nil, crDenied
				}
				return next(in)
			}
		}
	}
	for _, c := range []struct {
		name, op string
		fail     func(c *crtCalls, h map[string]func(any) (any, error))
		row      crtRow
		kept     bool
		warns    int
	}{
		{"tags of the other account's collaboration denied", crListTagsOp, func(c *crtCalls, _ map[string]func(any) (any, error)) {
			c.errs[crtForeignCollab()] = crDenied
		}, crtRow{TypeCleanRoomsCollaboration, crtForeignCollab()}, true, 0},
		{"tags of the other account's collaboration not found", crListTagsOp, func(c *crtCalls, _ map[string]func(any) (any, error)) {
			c.errs[crtForeignCollab()] = crNotFound
		}, crtRow{TypeCleanRoomsCollaboration, crtForeignCollab()}, true, 0},
		{"tags of an own collaboration denied", crListTagsOp, func(c *crtCalls, _ map[string]func(any) (any, error)) {
			c.errs[ownCollab] = crDenied
		}, crtRow{TypeCleanRoomsCollaboration, ownCollab}, true, 1},
		{"tags of an own collaboration not found", crListTagsOp, func(c *crtCalls, _ map[string]func(any) (any, error)) {
			c.errs[ownCollab] = crNotFound
		}, crtRow{TypeCleanRoomsCollaboration, ownCollab}, false, 0},
		{"tags of an own aws-us-gov table denied", crListTagsOp, func(c *crtCalls, _ map[string]func(any) (any, error)) {
			c.errs[crtGovTable()] = crDenied
		}, crtRow{TypeCleanRoomsConfiguredTable, crtGovTable()}, true, 1},
		{
			"members of the other account's collaboration denied", "cleanrooms:ListMembers", denyMembers(crtForeignCollab()),
			crtRow{TypeCleanRoomsMember, crtMember(ownCollab)},
			true, 0,
		},
		{
			"members of an own collaboration denied", "cleanrooms:ListMembers", denyMembers(ownCollab),
			crtRow{TypeCleanRoomsMember, crtMember(crtForeignCollab())},
			true, 1,
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := countWarnings(st)
			opWarns := countOpWarnings(st, c.op)
			calls := newCRTCalls()
			h := crtHandlers(calls)
			c.fail(calls, h)

			if _, _, err := crtScan(context.Background(), t, st, h); err != nil {
				t.Fatalf("err = %v; want nil", err)
			}
			if *warns != c.warns || *opWarns != c.warns {
				t.Errorf("warnings = %d (%s: %d); want %d", *warns, c.op, *opWarns, c.warns)
			}
			r, ok := crtRowOf(t, st, c.row.typ, c.row.arn)
			if ok != c.kept {
				t.Fatalf("%s %s stored = %v; want %v", c.row.typ, c.row.arn, ok, c.kept)
			}
			if ok && c.op == crListTagsOp && r.TagsJSON != nil {
				t.Errorf("TagsJSON = %s; want nil", *r.TagsJSON)
			}
		})
	}
}
