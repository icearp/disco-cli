package aws

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"sync/atomic"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/ssm"
	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"
	smithymw "github.com/aws/smithy-go/middleware"
	"github.com/icearp/disco-cli/store"
)

const (
	ssmTestDocA      = "doc-a"
	ssmTestDocB      = "doc-b"
	ssmTestAcctLow   = "111111111111"
	ssmTestAcctHigh  = "222222222222"
	ssmTestOwnerSelf = "Self"
)

var ssmDenied = apiErr("AccessDeniedException", "not authorized to perform ssm action")

func ssmTestDocARN(name string) string {
	return "arn:aws:ssm:" + testRegion + ":" + testAccountID + ":document/" + name
}

func ssmTestPermID(name string) string {
	return ssmDocumentPermissionNativeID(ssmTestDocARN(name))
}

// ssmTestSharedPages are doc-a's two permission pages, each out of order, so a
// stored body is sorted only if the scanner sorts across pages.
func ssmTestSharedPages() []*ssm.DescribeDocumentPermissionOutput {
	return []*ssm.DescribeDocumentPermissionOutput{
		{
			AccountIds: []string{ssmTestAcctHigh},
			AccountSharingInfoList: []ssmtypes.AccountSharingInfo{
				{AccountId: ptrStr(ssmTestAcctHigh), SharedDocumentVersion: ptrStr("2")},
				{AccountId: ptrStr(ssmTestAcctLow), SharedDocumentVersion: ptrStr("3")},
			},
			NextToken: ptrStr("t2"),
		},
		{
			AccountIds: []string{ssmTestAcctLow},
			AccountSharingInfoList: []ssmtypes.AccountSharingInfo{
				{AccountId: ptrStr(ssmTestAcctLow), SharedDocumentVersion: ptrStr("1")},
			},
		},
	}
}

// ssmDocPermHandlers lists two owned documents over two pages (the second page
// also carries one without a name) and serves their permissions: doc-a is
// shared over two pages, doc-b with no one. A permission call that does not
// ask for one Share page of at most 200, or a document listing not filtered
// to Owner=Self, fails. fail's error, when non-nil, answers the permission
// call for that document. Every other SSM op lists nothing.
func ssmDocPermHandlers(t *testing.T, calls *parentCalls, fail func(name string) error) map[string]func(in any) (any, error) {
	t.Helper()
	sharedPages := ssmTestSharedPages()
	empty := func(out any) func(any) (any, error) { return func(any) (any, error) { return out, nil } }
	return map[string]func(in any) (any, error){
		"DescribeParameters":          empty(&ssm.DescribeParametersOutput{}),
		"DescribePatchBaselines":      empty(&ssm.DescribePatchBaselinesOutput{}),
		"ListAssociations":            empty(&ssm.ListAssociationsOutput{}),
		"DescribeMaintenanceWindows":  empty(&ssm.DescribeMaintenanceWindowsOutput{}),
		"ListResourceDataSync":        empty(&ssm.ListResourceDataSyncOutput{}),
		"DescribeInstanceInformation": empty(&ssm.DescribeInstanceInformationOutput{}),
		"ListOpsMetadata":             empty(&ssm.ListOpsMetadataOutput{}),
		"DescribeActivations":         empty(&ssm.DescribeActivationsOutput{}),
		"ListCloudConnectors":         empty(&ssm.ListCloudConnectorsOutput{}),
		"ListDocuments": func(in any) (any, error) {
			li := in.(*ssm.ListDocumentsInput)
			if len(li.Filters) != 1 || sv(li.Filters[0].Key) != "Owner" || !slices.Equal(li.Filters[0].Values, []string{ssmTestOwnerSelf}) {
				t.Errorf("ListDocuments filters = %+v, want Owner=Self only", li.Filters)
			}
			if li.NextToken == nil {
				return &ssm.ListDocumentsOutput{NextToken: ptrStr("p2"), DocumentIdentifiers: []ssmtypes.DocumentIdentifier{
					{Name: ptrStr(ssmTestDocA)},
				}}, nil
			}
			return &ssm.ListDocumentsOutput{DocumentIdentifiers: []ssmtypes.DocumentIdentifier{
				{Name: ptrStr(ssmTestDocB)},
				{},
			}}, nil
		},
		"DescribeDocument": func(in any) (any, error) {
			return &ssm.DescribeDocumentOutput{Document: &ssmtypes.DocumentDescription{Name: in.(*ssm.DescribeDocumentInput).Name}}, nil
		},
		"DescribeDocumentPermission": func(in any) (any, error) {
			pi := in.(*ssm.DescribeDocumentPermissionInput)
			name := sv(pi.Name)
			calls.add(name)
			if pi.PermissionType != ssmtypes.DocumentPermissionTypeShare || pi.MaxResults == nil || *pi.MaxResults != 200 {
				t.Errorf("DescribeDocumentPermission(%s) PermissionType/MaxResults = %q/%v, want Share/200", name, pi.PermissionType, pi.MaxResults)
			}
			if err := fail(name); err != nil {
				return nil, err
			}
			if name != ssmTestDocA {
				return &ssm.DescribeDocumentPermissionOutput{}, nil
			}
			if pi.NextToken == nil {
				return sharedPages[0], nil
			}
			return sharedPages[1], nil
		},
	}
}

func noSSMPermErr(string) error { return nil }

// scanSSMTopLevel runs scanSSM with every op handlers lacks denied.
func scanSSMTopLevel(st *store.Store, handlers map[string]func(in any) (any, error), log *opLog) (int, int, error) {
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(handlers, ssmDenied)
	acct.cfg.APIOptions = append([]func(*smithymw.Stack) error{recordOps(log)}, acct.cfg.APIOptions...)
	return scanSSM(context.Background(), acct, testRegion, st, testScanID)
}

func ssmPermBody(t *testing.T, r store.Resource) ssmDocumentPermission {
	t.Helper()
	var p ssmDocumentPermission
	if err := json.Unmarshal([]byte(r.AttributesJSON), &p); err != nil {
		t.Fatalf("attrs %s: %v", r.AttributesJSON, err)
	}
	return p
}

// scanSSM reads the permission of every owned document it stored, after every
// older phase, and stores one row per document under it: doc-a's accounts
// from both pages, sorted, and doc-b's empty lists.
func TestScanSSM_StoresDocumentPermissionsLast(t *testing.T) {
	st := newTestStore(t)
	var calls parentCalls
	var log opLog
	total, inserted, err := scanSSMTopLevel(st, ssmDocPermHandlers(t, &calls, noSSMPermErr), &log)
	if err != nil {
		t.Fatalf("scanSSM: %v", err)
	}
	// 2 documents + 2 permissions; every other phase lists nothing.
	if total != 4 || inserted != 4 {
		t.Errorf("total, inserted = %d, %d; want 4, 4", total, inserted)
	}
	if got := calls.distinct(); !slices.Equal(got, []string{ssmTestDocA, ssmTestDocB}) {
		t.Errorf("permissions described for %v, want [%s %s]", got, ssmTestDocA, ssmTestDocB)
	}
	ops := log.snapshot()
	isPermOp := func(op string) bool { return op == "DescribeDocumentPermission" }
	first := slices.IndexFunc(ops, isPermOp)
	if first < 0 || slices.ContainsFunc(ops[first:], func(op string) bool { return !isPermOp(op) }) {
		t.Errorf("call order %v: every permission call must follow every other phase's calls", ops)
	}

	rows := rowsByNativeID(t, st, TypeSSMDocumentPermission)
	sharedID, unsharedID := ssmTestPermID(ssmTestDocA), ssmTestPermID(ssmTestDocB)
	if got := nativeIDs(rows); !slices.Equal(got, []string{sharedID, unsharedID}) {
		t.Fatalf("permission rows = %v, want [%s %s]", got, sharedID, unsharedID)
	}
	shared := rows[sharedID]
	if shared.Type != TypeSSMDocumentPermission || sv(shared.Name) != ssmTestDocA || sv(shared.Region) != testRegion {
		t.Errorf("%s type/name/region = %q/%q/%q, want %s/%s/%s", sharedID, shared.Type, sv(shared.Name), sv(shared.Region), TypeSSMDocumentPermission, ssmTestDocA, testRegion)
	}
	body := ssmPermBody(t, shared)
	if !slices.Equal(body.AccountIDs, []string{ssmTestAcctLow, ssmTestAcctHigh}) {
		t.Errorf("%s AccountIds = %v, want [%s %s]", sharedID, body.AccountIDs, ssmTestAcctLow, ssmTestAcctHigh)
	}
	var sharing []string
	for _, s := range body.AccountSharingInfoList {
		sharing = append(sharing, sv(s.AccountId)+"@"+sv(s.SharedDocumentVersion))
	}
	if want := []string{ssmTestAcctLow + "@1", ssmTestAcctLow + "@3", ssmTestAcctHigh + "@2"}; !slices.Equal(sharing, want) {
		t.Errorf("%s AccountSharingInfoList = %v, want %v", sharedID, sharing, want)
	}
	if got := rows[unsharedID].AttributesJSON; got != `{"AccountIds":[],"AccountSharingInfoList":[]}` {
		t.Errorf("%s attrs = %s, want both lists empty, not null", unsharedID, got)
	}
	assertContainsNative(t, st, ssmTestDocARN(ssmTestDocA), sharedID)
	assertContainsNative(t, st, ssmTestDocARN(ssmTestDocB), unsharedID)
}

// stubSSMDocPerm serves DescribeDocumentPermission pages in order.
type stubSSMDocPerm struct {
	ssmAPI
	pages []*ssm.DescribeDocumentPermissionOutput
	ins   []*ssm.DescribeDocumentPermissionInput
}

func (s *stubSSMDocPerm) DescribeDocumentPermission(_ context.Context, in *ssm.DescribeDocumentPermissionInput, _ ...func(*ssm.Options)) (*ssm.DescribeDocumentPermissionOutput, error) {
	cp := *in
	s.ins = append(s.ins, &cp)
	return s.pages[len(s.ins)-1], nil
}

// The stored body does not depend on the order the API returns accounts in,
// within or across pages.
func TestDescribeSSMDocumentPermission_SortIsTotal(t *testing.T) {
	pages := ssmTestSharedPages()
	reversed := []*ssm.DescribeDocumentPermissionOutput{
		{AccountIds: pages[1].AccountIds, AccountSharingInfoList: pages[1].AccountSharingInfoList, NextToken: ptrStr("t2")},
		{AccountIds: pages[0].AccountIds, AccountSharingInfoList: slices.Clone(pages[0].AccountSharingInfoList)},
	}
	slices.Reverse(reversed[1].AccountSharingInfoList)
	var bodies []string
	for _, in := range [][]*ssm.DescribeDocumentPermissionOutput{pages, reversed} {
		perm, err := describeSSMDocumentPermission(context.Background(), &stubSSMDocPerm{pages: in}, ssmTestDocA)
		if err != nil {
			t.Fatalf("describeSSMDocumentPermission: %v", err)
		}
		bodies = append(bodies, mustJSON(perm))
	}
	if bodies[0] != bodies[1] {
		t.Errorf("bodies differ by input order:\n%s\n%s", bodies[0], bodies[1])
	}
}

// A repeated token fails the read and returns no share lists: a partial list
// stored as the document's whole permission would claim accounts it never
// listed are not shared.
func TestDescribeSSMDocumentPermission_RepeatedTokenFails(t *testing.T) {
	page := &ssm.DescribeDocumentPermissionOutput{AccountIds: []string{ssmTestAcctLow}, NextToken: ptrStr("same")}
	stub := &stubSSMDocPerm{pages: []*ssm.DescribeDocumentPermissionOutput{page, page, page}}
	perm, err := describeSSMDocumentPermission(context.Background(), stub, ssmTestDocA)
	if !errors.Is(err, errRepeatedNextToken) {
		t.Fatalf("err = %v, want errRepeatedNextToken", err)
	}
	if len(stub.ins) != 2 || sv(stub.ins[1].NextToken) != "same" {
		t.Errorf("calls = %d (second token %q), want 2 ending on the repeated token", len(stub.ins), sv(stub.ins[len(stub.ins)-1].NextToken))
	}
	if perm.AccountIDs != nil || perm.AccountSharingInfoList != nil {
		t.Errorf("perm = %+v; want both lists nil (not read)", perm)
	}
}

// A document whose permission listing repeats its token keeps its row with
// the share lists null (not read), and the scan returns the error; the
// sibling document's row keeps its lists.
func TestScanSSMDocumentPermissions_RepeatedTokenKeepsRowWithoutLists(t *testing.T) {
	st := newTestStore(t)
	var calls parentCalls
	var log opLog
	handlers := ssmDocPermHandlers(t, &calls, noSSMPermErr)
	describe := handlers["DescribeDocumentPermission"]
	var repeats atomic.Int32
	handlers["DescribeDocumentPermission"] = func(in any) (any, error) {
		if sv(in.(*ssm.DescribeDocumentPermissionInput).Name) != ssmTestDocA {
			return describe(in)
		}
		if repeats.Add(1) > 3 {
			return nil, errors.New("called again after a repeated token")
		}
		return &ssm.DescribeDocumentPermissionOutput{AccountIds: []string{ssmTestAcctLow}, NextToken: ptrStr("same")}, nil
	}
	if _, _, err := scanSSMTopLevel(st, handlers, &log); !errors.Is(err, errRepeatedNextToken) {
		t.Fatalf("err = %v, want errRepeatedNextToken", err)
	}
	rows := rowsByNativeID(t, st, TypeSSMDocumentPermission)
	if got, want := nativeIDs(rows), []string{ssmTestPermID(ssmTestDocA), ssmTestPermID(ssmTestDocB)}; !slices.Equal(got, want) {
		t.Fatalf("permission rows = %v, want %v", got, want)
	}
	if a := ssmPermBody(t, rows[ssmTestPermID(ssmTestDocA)]); a.AccountIDs != nil || a.AccountSharingInfoList != nil {
		t.Errorf("repeating document body = %+v; want both lists null", a)
	}
	if b := ssmPermBody(t, rows[ssmTestPermID(ssmTestDocB)]); b.AccountIDs == nil || b.AccountSharingInfoList == nil {
		t.Errorf("sibling document body = %+v; want both lists read ([])", b)
	}
}

// Per-document errors: a deleted document is skipped silently; a throttled
// one, a denied one and one refused with the unobserved
// InvalidDocumentOperation are skipped with one warning for all; each keeps
// its sibling's row and the scan succeeds.
func TestScanSSMDocumentPermissions_PerDocumentSkips(t *testing.T) {
	cases := []struct {
		name      string
		failing   []string
		err       error
		wantRows  []string
		wantWarns int
	}{
		{"deleted document skipped", []string{ssmTestDocA}, apiErr("InvalidDocument", "gone"), []string{ssmTestPermID(ssmTestDocB)}, 0},
		{"throttled document keeps sibling", []string{ssmTestDocB}, apiErr("Throttling", "Rate exceeded"), []string{ssmTestPermID(ssmTestDocA)}, 1},
		{"denial warns once", []string{ssmTestDocA, ssmTestDocB}, ssmDenied, nil, 1},
		{"unobserved operation code warns once", []string{ssmTestDocA, ssmTestDocB}, apiErr("InvalidDocumentOperation", "bad"), nil, 1},
		{"unobserved operation code keeps sibling", []string{ssmTestDocA}, apiErr("InvalidDocumentOperation", "bad"), []string{ssmTestPermID(ssmTestDocB)}, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := countOpWarnings(st, ssmDescribeDocumentPermissionOp)
			var calls parentCalls
			var log opLog
			fail := func(name string) error {
				if slices.Contains(tc.failing, name) {
					return tc.err
				}
				return nil
			}
			total, _, err := scanSSMTopLevel(st, ssmDocPermHandlers(t, &calls, fail), &log)
			if err != nil {
				t.Fatalf("scanSSM: %v", err)
			}
			rows := rowsByNativeID(t, st, TypeSSMDocumentPermission)
			if got := nativeIDs(rows); !slices.Equal(got, tc.wantRows) {
				t.Errorf("permission rows = %v, want %v", got, tc.wantRows)
			}
			if want := 2 + len(tc.wantRows); total != want {
				t.Errorf("total = %d, want %d", total, want)
			}
			if *warns != tc.wantWarns {
				t.Errorf("%s warnings = %d, want %d", ssmDescribeDocumentPermissionOp, *warns, tc.wantWarns)
			}
		})
	}
}

// No owned document means no permission call and no row.
func TestScanSSM_NoDocumentsNoPermissionCalls(t *testing.T) {
	st := newTestStore(t)
	var calls parentCalls
	var log opLog
	h := ssmDocPermHandlers(t, &calls, noSSMPermErr)
	h["ListDocuments"] = func(any) (any, error) { return &ssm.ListDocumentsOutput{}, nil }
	total, _, err := scanSSMTopLevel(st, h, &log)
	if err != nil {
		t.Fatalf("scanSSM: %v", err)
	}
	if got := calls.distinct(); len(got) != 0 || total != 0 {
		t.Errorf("permission calls = %v, total = %d; want none, 0", got, total)
	}
	if rows := rowsByNativeID(t, st, TypeSSMDocumentPermission); len(rows) != 0 {
		t.Errorf("permission rows = %v, want none", nativeIDs(rows))
	}
}

// A phase's hard error stops no later phase: the OpsMetadata listing and the
// permission phase still run, the latter describing every document stored;
// the error is returned with the counts of every row stored.
func TestScanSSM_PhaseErrorStillRunsLaterPhases(t *testing.T) {
	cases := []struct {
		failingOp string
		wantTotal int
		wantPerms int
	}{
		// The parameter and the OpsMetadata row: no document was stored, so
		// no permission is described.
		{"ListDocuments", 2, 0},
		// 1 parameter + 2 documents + 1 OpsMetadata row + 2 permissions.
		{"DescribePatchBaselines", 6, 2},
		{"ListAssociations", 6, 2},
		{"DescribeActivations", 6, 2},
	}
	for _, tc := range cases {
		t.Run(tc.failingOp, func(t *testing.T) {
			st := newTestStore(t)
			var calls parentCalls
			var log opLog
			h := ssmDocPermHandlers(t, &calls, noSSMPermErr)
			h["DescribeParameters"] = func(any) (any, error) {
				return &ssm.DescribeParametersOutput{Parameters: []ssmtypes.ParameterMetadata{{Name: ptrStr("/app/p")}}}, nil
			}
			h["ListOpsMetadata"] = func(any) (any, error) {
				return &ssm.ListOpsMetadataOutput{OpsMetadataList: []ssmtypes.OpsMetadata{{OpsMetadataArn: ptrStr("arn:aws:ssm:us-east-1:123456789012:opsmetadata/app")}}}, nil
			}
			h[tc.failingOp] = func(any) (any, error) { return nil, apiErr("InvalidFilter", "bad") }
			total, inserted, err := scanSSMTopLevel(st, h, &log)
			assertAPICode(t, err, "InvalidFilter")
			if total != tc.wantTotal || inserted != tc.wantTotal {
				t.Errorf("total, inserted = %d, %d; want %d, %d", total, inserted, tc.wantTotal, tc.wantTotal)
			}
			if rows := rowsByNativeID(t, st, TypeSSMDocumentPermission); len(rows) != tc.wantPerms {
				t.Errorf("permission rows = %v, want %d", nativeIDs(rows), tc.wantPerms)
			}
		})
	}
}

// A hard permission error is returned after every older phase stored its
// rows and reaches the dispatcher as a hard error.
func TestScanSSM_DocumentPermissionErrorKeepsOtherPhases(t *testing.T) {
	st := newTestStore(t)
	var calls parentCalls
	var log opLog
	h := ssmDocPermHandlers(t, &calls, func(string) error { return apiErr("InvalidPermissionType", "bad") })
	h["ListOpsMetadata"] = func(any) (any, error) {
		return &ssm.ListOpsMetadataOutput{OpsMetadataList: []ssmtypes.OpsMetadata{{OpsMetadataArn: ptrStr("arn:aws:ssm:us-east-1:123456789012:opsmetadata/app")}}}, nil
	}
	total, _, err := scanSSMTopLevel(st, h, &log)
	assertAPICode(t, err, "InvalidPermissionType")
	if got := classifyServiceError(err); got != outcomeError {
		t.Errorf("classifyServiceError = %v, want outcomeError", got)
	}
	docs, ops, perms := len(rowsByNativeID(t, st, TypeSSMDocument)), len(rowsByNativeID(t, st, TypeSSMOpsMetadata)), len(rowsByNativeID(t, st, TypeSSMDocumentPermission))
	if total != 3 || docs != 2 || ops != 1 || perms != 0 {
		t.Errorf("total = %d, rows document/opsmetadata/permission = %d/%d/%d; want 3, 2/1/0", total, docs, ops, perms)
	}
}

// A throttled older phase is demoted to one warning, so the permission phase's
// hard error still reaches the dispatcher as a hard error.
func TestScanSSM_ThrottledOlderPhaseKeepsPermissionHardError(t *testing.T) {
	st := newTestStore(t)
	warns := countOpWarnings(st, ssmListAssociationsOp)
	var calls parentCalls
	var log opLog
	h := ssmDocPermHandlers(t, &calls, func(string) error { return apiErr("InvalidPermissionType", "bad") })
	h["ListAssociations"] = func(any) (any, error) { return nil, apiErr("Throttling", "Rate exceeded") }
	_, _, err := scanSSMTopLevel(st, h, &log)
	assertAPICode(t, err, "InvalidPermissionType")
	if got := classifyServiceError(err); got != outcomeError {
		t.Errorf("classifyServiceError = %v, want outcomeError", got)
	}
	if *warns != 1 {
		t.Errorf("%s warnings = %d, want 1 for the demoted throttle", ssmListAssociationsOp, *warns)
	}
}
