package aws

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sync/atomic"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/workspaces"
	wstypes "github.com/aws/aws-sdk-go-v2/service/workspaces/types"
	"github.com/icearp/disco-cli/store"
)

// stubWorkSpaces answers DescribeWorkspacesPools with a canned error and
// nothing else — the other methods exist only to satisfy workSpacesAPI.
type stubWorkSpaces struct{ poolsErr error }

func (s *stubWorkSpaces) DescribeWorkspacesPools(_ context.Context, _ *workspaces.DescribeWorkspacesPoolsInput, _ ...func(*workspaces.Options)) (*workspaces.DescribeWorkspacesPoolsOutput, error) {
	return nil, s.poolsErr
}

func (s *stubWorkSpaces) DescribeWorkspaces(context.Context, *workspaces.DescribeWorkspacesInput, ...func(*workspaces.Options)) (*workspaces.DescribeWorkspacesOutput, error) {
	return &workspaces.DescribeWorkspacesOutput{}, nil
}

func (s *stubWorkSpaces) DescribeConnectionAliases(context.Context, *workspaces.DescribeConnectionAliasesInput, ...func(*workspaces.Options)) (*workspaces.DescribeConnectionAliasesOutput, error) {
	return &workspaces.DescribeConnectionAliasesOutput{}, nil
}

func (s *stubWorkSpaces) DescribeWorkspaceDirectories(context.Context, *workspaces.DescribeWorkspaceDirectoriesInput, ...func(*workspaces.Options)) (*workspaces.DescribeWorkspaceDirectoriesOutput, error) {
	return &workspaces.DescribeWorkspaceDirectoriesOutput{}, nil
}

func (s *stubWorkSpaces) DescribeWorkspaceBundles(context.Context, *workspaces.DescribeWorkspaceBundlesInput, ...func(*workspaces.Options)) (*workspaces.DescribeWorkspaceBundlesOutput, error) {
	return &workspaces.DescribeWorkspaceBundlesOutput{}, nil
}

func (s *stubWorkSpaces) DescribeWorkspaceImages(context.Context, *workspaces.DescribeWorkspaceImagesInput, ...func(*workspaces.Options)) (*workspaces.DescribeWorkspaceImagesOutput, error) {
	return &workspaces.DescribeWorkspaceImagesOutput{}, nil
}

//nolint:revive // method name must match the SDK op (DescribeIpGroups) to satisfy workSpacesAPI.
func (s *stubWorkSpaces) DescribeIpGroups(context.Context, *workspaces.DescribeIpGroupsInput, ...func(*workspaces.Options)) (*workspaces.DescribeIpGroupsOutput, error) {
	return &workspaces.DescribeIpGroupsOutput{}, nil
}

func (s *stubWorkSpaces) DescribeApplications(context.Context, *workspaces.DescribeApplicationsInput, ...func(*workspaces.Options)) (*workspaces.DescribeApplicationsOutput, error) {
	return &workspaces.DescribeApplicationsOutput{}, nil
}

func (s *stubWorkSpaces) ListAccountLinks(context.Context, *workspaces.ListAccountLinksInput, ...func(*workspaces.Options)) (*workspaces.ListAccountLinksOutput, error) {
	return &workspaces.ListAccountLinksOutput{}, nil
}

func (s *stubWorkSpaces) DescribeConnectClientAddIns(context.Context, *workspaces.DescribeConnectClientAddInsInput, ...func(*workspaces.Options)) (*workspaces.DescribeConnectClientAddInsOutput, error) {
	return &workspaces.DescribeConnectClientAddInsOutput{}, nil
}

func (s *stubWorkSpaces) DescribeConnectionAliasPermissions(context.Context, *workspaces.DescribeConnectionAliasPermissionsInput, ...func(*workspaces.Options)) (*workspaces.DescribeConnectionAliasPermissionsOutput, error) {
	return &workspaces.DescribeConnectionAliasPermissionsOutput{}, nil
}

// The predicate decides whether a DescribeWorkspacesPools refusal reaches the
// operator, so assert the OUTCOME on the scan rather than the predicate again:
// both canned availability shapes are silent, a genuine denial warns, and
// neither aborts the scan.
func TestScanWSWorkspacesPools_SkipsAvailabilityGapsAndWarnsOnDenial(t *testing.T) {
	const denial = "User: arn:aws:sts::123456789012:assumed-role/disco/scan is not " +
		"authorized to perform: workspaces:DescribeWorkspacesPools on resource: *"

	for _, tc := range []struct {
		name     string
		msg      string
		wantWarn bool
	}{
		{
			"feature not launched in this region",
			"You do not have the permissions required to perform this action. Refer to " +
				"https://docs.aws.amazon.com/workspaces/latest/adminguide/workspaces-access-control.html",
			false,
		},
		{
			"closed to new customers as of 7/30/2026",
			"Amazon WorkSpaces Pools is no longer available to new customers as of 7/30/2026. " +
				"Refer to https://docs.aws.amazon.com/workspaces/latest/adminguide/wsp-pools-end-of-support.html",
			false,
		},
		{"genuine IAM denial", denial, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			var warnings []store.ScanWarning
			st.OnWarn = func(w store.ScanWarning) { warnings = append(warnings, w) }

			client := &stubWorkSpaces{poolsErr: apiErr("AccessDeniedException", tc.msg)}
			_, _, err := scanWSWorkspacesPools(context.Background(), client,
				newTestAccount(testAccountID), "us-east-1", st, "scan-1")
			if err != nil {
				t.Fatalf("scanWSWorkspacesPools returned %v; want nil — an access denial must never abort the scan", err)
			}
			if got := len(warnings) > 0; got != tc.wantWarn {
				t.Errorf("warned = %v (%+v); want %v", got, warnings, tc.wantWarn)
			}
		})
	}
}

var (
	wsDenied = apiErr("AccessDeniedException", "User: arn:aws:sts::123456789012:assumed-role/disco/scan is not "+
		"authorized to perform: workspaces:List on resource: *")
	wsGone       = apiErr("ResourceNotFoundException", "directory not found")
	wsBadInput   = apiErr("ValidationException", "bad")
	wsBadAddInID = apiErr("InvalidParameterValuesException", "bad")
)

const (
	wsTestDirID1 = "d-1111111111"
	wsTestDirID2 = "d-2222222222"
	wsTestDir1   = "arn:aws:workspaces:us-east-1:123456789012:directory/" + wsTestDirID1
	wsTestDir2   = "arn:aws:workspaces:us-east-1:123456789012:directory/" + wsTestDirID2
	wsTestApp    = "arn:aws:workspaces:us-east-1:123456789012:workspaceapplication/wsa-1"
	wsTestLink1  = "arn:aws:workspaces:us-east-1:123456789012:accountlink/link-11111111"
	wsTestLink2  = "arn:aws:workspaces:us-east-1:123456789012:accountlink/link-22222222"
	wsTestAddIn1 = wsTestDir1 + "/connect-client-add-in/00000000-0000-0000-0000-000000000001"
	wsTestAddIn2 = wsTestDir1 + "/connect-client-add-in/00000000-0000-0000-0000-000000000002"
	wsTestAddIn3 = wsTestDir2 + "/connect-client-add-in/00000000-0000-0000-0000-000000000003"
)

// wsCheckLimit fails a call whose page size is not the model's cap.
func wsCheckLimit(maxResults *int32) error {
	if maxResults == nil || *maxResults != 25 {
		return fmt.Errorf("MaxResults = %v, want 25", maxResults)
	}
	return nil
}

// wsTopLevelHandlers serves one application, directories over two pages (d-1;
// then d-2 and an element with no id) and account links over two pages (a
// LINKED link; then a REJECTED link, a LINK_NOT_FOUND link and an element with
// no id). Under d-1, DescribeConnectClientAddIns returns two pages: one
// add-in, then another and an element with no id; under d-2, one add-in.
func wsTopLevelHandlers(addInCalls *parentCalls) map[string]func(in any) (any, error) {
	return map[string]func(in any) (any, error){
		"DescribeWorkspaces":        func(any) (any, error) { return &workspaces.DescribeWorkspacesOutput{}, nil },
		"DescribeConnectionAliases": func(any) (any, error) { return &workspaces.DescribeConnectionAliasesOutput{}, nil },
		"DescribeWorkspacesPools":   func(any) (any, error) { return &workspaces.DescribeWorkspacesPoolsOutput{}, nil },
		"DescribeWorkspaceBundles":  func(any) (any, error) { return &workspaces.DescribeWorkspaceBundlesOutput{}, nil },
		"DescribeWorkspaceImages":   func(any) (any, error) { return &workspaces.DescribeWorkspaceImagesOutput{}, nil },
		"DescribeIpGroups":          func(any) (any, error) { return &workspaces.DescribeIpGroupsOutput{}, nil },
		"DescribeApplications": func(any) (any, error) {
			return &workspaces.DescribeApplicationsOutput{Applications: []wstypes.WorkSpaceApplication{{ApplicationId: ptrStr("wsa-1")}}}, nil
		},
		"DescribeWorkspaceDirectories": func(in any) (any, error) {
			if in.(*workspaces.DescribeWorkspaceDirectoriesInput).NextToken == nil {
				return &workspaces.DescribeWorkspaceDirectoriesOutput{
					NextToken:   ptrStr("p2"),
					Directories: []wstypes.WorkspaceDirectory{{DirectoryId: ptrStr(wsTestDirID1)}},
				}, nil
			}
			return &workspaces.DescribeWorkspaceDirectoriesOutput{Directories: []wstypes.WorkspaceDirectory{
				{DirectoryId: ptrStr(wsTestDirID2)},
				{DirectoryName: ptrStr("no-id")},
			}}, nil
		},
		"ListAccountLinks": func(in any) (any, error) {
			p := in.(*workspaces.ListAccountLinksInput)
			if err := wsCheckLimit(p.MaxResults); err != nil {
				return nil, err
			}
			if p.NextToken == nil {
				return &workspaces.ListAccountLinksOutput{NextToken: ptrStr("p2"), AccountLinks: []wstypes.AccountLink{{
					AccountLinkId: ptrStr("link-11111111"), AccountLinkStatus: wstypes.AccountLinkStatusEnumLinked,
					SourceAccountId: ptrStr("123456789012"), TargetAccountId: ptrStr("210987654321"),
				}}}, nil
			}
			return &workspaces.ListAccountLinksOutput{AccountLinks: []wstypes.AccountLink{
				{AccountLinkId: ptrStr("link-22222222"), AccountLinkStatus: wstypes.AccountLinkStatusEnumRejected},
				{AccountLinkId: ptrStr("link-33333333"), AccountLinkStatus: wstypes.AccountLinkStatusEnumLinkNotFound},
				{AccountLinkStatus: wstypes.AccountLinkStatusEnumLinked},
			}}, nil
		},
		"DescribeConnectClientAddIns": func(in any) (any, error) {
			p := in.(*workspaces.DescribeConnectClientAddInsInput)
			addInCalls.add(sv(p.ResourceId))
			if err := wsCheckLimit(p.MaxResults); err != nil {
				return nil, err
			}
			switch {
			case sv(p.ResourceId) == wsTestDirID2:
				return &workspaces.DescribeConnectClientAddInsOutput{AddIns: []wstypes.ConnectClientAddIn{
					{AddInId: ptrStr("00000000-0000-0000-0000-000000000003"), ResourceId: ptrStr(wsTestDirID2)},
				}}, nil
			case p.NextToken == nil:
				return &workspaces.DescribeConnectClientAddInsOutput{NextToken: ptrStr("p2"), AddIns: []wstypes.ConnectClientAddIn{{
					AddInId: ptrStr("00000000-0000-0000-0000-000000000001"), ResourceId: ptrStr(wsTestDirID1),
					Name: ptrStr("support-panel"), URL: ptrStr("https://example.my.connect.aws/agent-app-v2"),
				}}}, nil
			default:
				return &workspaces.DescribeConnectClientAddInsOutput{AddIns: []wstypes.ConnectClientAddIn{
					{AddInId: ptrStr("00000000-0000-0000-0000-000000000002"), ResourceId: ptrStr(wsTestDirID1)},
					{Name: ptrStr("no-id")},
				}}, nil
			}
		},
	}
}

func scanWorkSpacesTopLevel(t *testing.T, st *store.Store, handlers map[string]func(in any) (any, error)) (int, error) {
	t.Helper()
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(handlers, wsDenied)
	total, _, err := scanWorkSpaces(context.Background(), acct, testRegion, st, testScanID)
	return total, err
}

func wsAttrs(t *testing.T, r store.Resource) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(r.AttributesJSON), &m); err != nil {
		t.Fatalf("attrs %s: %v", r.AttributesJSON, err)
	}
	return m
}

func TestScanWorkSpaces_StoresAccountLinksAndConnectClientAddIns(t *testing.T) {
	st := newTestStore(t)
	var addInCalls parentCalls
	total, err := scanWorkSpacesTopLevel(t, st, wsTopLevelHandlers(&addInCalls))
	if err != nil {
		t.Fatalf("scanWorkSpaces: %v", err)
	}
	// 1 application + 2 directories + 2 links + 3 add-ins
	if total != 8 {
		t.Errorf("total = %d, want 8", total)
	}

	links := rowsByNativeID(t, st, TypeWorkSpacesAccountLink)
	if got := nativeIDs(links); !slices.Equal(got, []string{wsTestLink1, wsTestLink2}) {
		t.Fatalf("link rows = %v, want [%s %s]", got, wsTestLink1, wsTestLink2)
	}
	for id, want := range map[string]string{wsTestLink1: "LINKED", wsTestLink2: "REJECTED"} {
		r := links[id]
		if sv(r.Status) != want || sv(r.Region) != testRegion {
			t.Errorf("%s status/region = %q/%q, want %q/%q", id, sv(r.Status), sv(r.Region), want, testRegion)
		}
	}
	if got := sv(links[wsTestLink1].Name); got != "link-11111111" {
		t.Errorf("link name = %q, want link-11111111", got)
	}
	if got := wsAttrs(t, links[wsTestLink1])["TargetAccountId"]; got != "210987654321" {
		t.Errorf("link TargetAccountId = %v, want 210987654321", got)
	}

	addIns := rowsByNativeID(t, st, TypeWorkSpacesConnectClientAddIn)
	if got := nativeIDs(addIns); !slices.Equal(got, []string{wsTestAddIn1, wsTestAddIn2, wsTestAddIn3}) {
		t.Fatalf("add-in rows = %v, want [%s %s %s]", got, wsTestAddIn1, wsTestAddIn2, wsTestAddIn3)
	}
	a1 := addIns[wsTestAddIn1]
	if sv(a1.Name) != "support-panel" || sv(a1.Region) != testRegion {
		t.Errorf("add-in name/region = %q/%q, want support-panel/%s", sv(a1.Name), sv(a1.Region), testRegion)
	}
	if got := wsAttrs(t, a1)["URL"]; got != "https://example.my.connect.aws/agent-app-v2" {
		t.Errorf("add-in URL = %v, want the Connect endpoint", got)
	}
	if got := addInCalls.distinct(); !slices.Equal(got, []string{wsTestDirID1, wsTestDirID2}) {
		t.Errorf("add-ins listed for %v, want [%s %s]", got, wsTestDirID1, wsTestDirID2)
	}
	assertContainsNative(t, st, wsTestDir1, wsTestAddIn1)
	assertContainsNative(t, st, wsTestDir1, wsTestAddIn2)
	assertContainsNative(t, st, wsTestDir2, wsTestAddIn3)
	assertContainsEdgesDeclared(t, st)
}

func TestScanWorkSpaces_NoDirectoriesOrLinks(t *testing.T) {
	st := newTestStore(t)
	var addInCalls parentCalls
	h := wsTopLevelHandlers(&addInCalls)
	h["DescribeWorkspaceDirectories"] = func(any) (any, error) { return &workspaces.DescribeWorkspaceDirectoriesOutput{}, nil }
	h["ListAccountLinks"] = func(any) (any, error) { return &workspaces.ListAccountLinksOutput{}, nil }
	total, err := scanWorkSpacesTopLevel(t, st, h)
	if err != nil {
		t.Fatalf("scanWorkSpaces: %v", err)
	}
	if total != 1 {
		t.Errorf("total = %d, want 1 (the application)", total)
	}
	if got := addInCalls.distinct(); len(got) != 0 {
		t.Errorf("add-ins listed for %v, want none", got)
	}
	for _, typ := range []string{TypeWorkSpacesAccountLink, TypeWorkSpacesConnectClientAddIn} {
		if n := len(rowsByNativeID(t, st, typ)); n != 0 {
			t.Errorf("%s rows = %d, want 0", typ, n)
		}
	}
}

// ListAccountLinks failures never cost the other phases: the older phases'
// rows and the add-ins are stored whatever it returns. A denial on any page
// warns once and keeps the links already listed; an op the endpoint does not
// serve is silent on the first page only; a transient error warns; anything
// else fails the scan.
func TestScanWorkSpaces_FailedAccountLinksPage(t *testing.T) {
	notDeployed := apiErr("UnknownOperationException", "unknown operation")
	unavailable := apiErr("ServiceUnavailableException", "try later")
	for _, tc := range []struct {
		name      string
		onPage2   bool
		err       error
		wantCode  string // "" means the scan returns nil
		wantLinks []string
		wantWarns int
		wantTotal int
	}{
		{"denied first page", false, wsDenied, "", nil, 1, 6},
		{"denied second page", true, wsDenied, "", []string{wsTestLink1}, 1, 7},
		{"not deployed first page", false, notDeployed, "", nil, 0, 6},
		{"not deployed second page", true, notDeployed, "UnknownOperationException", nil, 0, 6},
		{"transient", false, unavailable, "", nil, 1, 6},
		{"other error first page", false, wsBadInput, "ValidationException", nil, 0, 6},
		{"other error second page", true, wsBadInput, "ValidationException", nil, 0, 6},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := countOpWarnings(st, "workspaces:ListAccountLinks")
			var addInCalls parentCalls
			h := wsTopLevelHandlers(&addInCalls)
			next := h["ListAccountLinks"]
			h["ListAccountLinks"] = func(in any) (any, error) {
				if (in.(*workspaces.ListAccountLinksInput).NextToken != nil) == tc.onPage2 {
					return nil, tc.err
				}
				return next(in)
			}
			total, err := scanWorkSpacesTopLevel(t, st, h)
			if tc.wantCode == "" {
				if err != nil {
					t.Fatalf("err = %v, want nil", err)
				}
			} else {
				assertAPICode(t, err, tc.wantCode)
			}
			if got := nativeIDs(rowsByNativeID(t, st, TypeWorkSpacesAccountLink)); !slices.Equal(got, tc.wantLinks) {
				t.Errorf("link rows = %v, want %v", got, tc.wantLinks)
			}
			if *warns != tc.wantWarns {
				t.Errorf("warnings = %d, want %d", *warns, tc.wantWarns)
			}
			if total != tc.wantTotal {
				t.Errorf("total = %d, want %d", total, tc.wantTotal)
			}
			for typ, id := range map[string]string{
				TypeWorkSpacesWorkspaceApplication: wsTestApp,
				TypeWorkSpacesDirectory:            wsTestDir1,
				TypeWorkSpacesConnectClientAddIn:   wsTestAddIn3,
			} {
				if _, ok := rowsByNativeID(t, st, typ)[id]; !ok {
					t.Errorf("row %s missing; a failed account-link phase must not cost another phase its rows", id)
				}
			}
		})
	}
}

// A hard error in an older phase still lets the account-link and add-in
// phases run, over the directories listed before the failure, and is returned.
func TestScanWorkSpaces_OlderPhaseErrorStillRunsNewPhases(t *testing.T) {
	st := newTestStore(t)
	var addInCalls parentCalls
	h := wsTopLevelHandlers(&addInCalls)
	h["DescribeApplications"] = func(any) (any, error) { return nil, wsBadInput }
	total, err := scanWorkSpacesTopLevel(t, st, h)
	assertAPICode(t, err, "ValidationException")
	if got := nativeIDs(rowsByNativeID(t, st, TypeWorkSpacesAccountLink)); !slices.Equal(got, []string{wsTestLink1, wsTestLink2}) {
		t.Errorf("link rows = %v, want [%s %s]", got, wsTestLink1, wsTestLink2)
	}
	if got := nativeIDs(rowsByNativeID(t, st, TypeWorkSpacesConnectClientAddIn)); !slices.Equal(got, []string{wsTestAddIn1, wsTestAddIn2, wsTestAddIn3}) {
		t.Errorf("add-in rows = %v, want all three", got)
	}
	// 2 directories + 2 links + 3 add-ins; the failed applications phase stores none.
	if total != 7 {
		t.Errorf("total = %d, want 7", total)
	}
}

// A transient error in an older phase is demoted to a warning so that the
// account-link phase's hard error still reaches the dispatcher as a hard
// error, not hidden inside a join the dispatcher would read as transient.
func TestScanWorkSpaces_OlderPhaseTransientDoesNotMaskHardError(t *testing.T) {
	st := newTestStore(t)
	warns := countOpWarnings(st, "workspaces")
	var addInCalls parentCalls
	h := wsTopLevelHandlers(&addInCalls)
	h["DescribeApplications"] = func(any) (any, error) { return nil, apiErr("ServiceUnavailableException", "try later") }
	h["ListAccountLinks"] = func(any) (any, error) { return nil, wsBadInput }
	_, err := scanWorkSpacesTopLevel(t, st, h)
	assertAPICode(t, err, "ValidationException")
	if isTransientNetworkError(err) {
		t.Errorf("err = %v reads as transient; the hard account-link error must not be masked", err)
	}
	if *warns != 1 {
		t.Errorf("warnings = %d, want 1 for the demoted transient", *warns)
	}
}

// A failed directory drops only its own add-ins: deregistered since listing is
// silent, a denial or a transient error after retries warns once however many
// directories hit it, and any other error fails the scan after every other
// phase has stored its rows.
func TestScanWorkSpaces_FailedAddInDirectory(t *testing.T) {
	for _, tc := range []struct {
		name       string
		failDirs   []string
		err        error
		wantErr    bool
		wantAddIns []string
		wantWarns  int
	}{
		{"directory gone", []string{wsTestDirID2}, wsGone, false, []string{wsTestAddIn1, wsTestAddIn2}, 0},
		{"throttled directory", []string{wsTestDirID2}, apiErr("ThrottlingException", "rate exceeded"), false, []string{wsTestAddIn1, wsTestAddIn2}, 1},
		{"denied on every directory", []string{wsTestDirID1, wsTestDirID2}, wsDenied, false, nil, 1},
		{"other error", []string{wsTestDirID2}, wsBadAddInID, true, nil, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := countOpWarnings(st, "workspaces:DescribeConnectClientAddIns")
			var addInCalls parentCalls
			h := wsTopLevelHandlers(&addInCalls)
			next := h["DescribeConnectClientAddIns"]
			h["DescribeConnectClientAddIns"] = func(in any) (any, error) {
				if slices.Contains(tc.failDirs, sv(in.(*workspaces.DescribeConnectClientAddInsInput).ResourceId)) {
					return nil, tc.err
				}
				return next(in)
			}
			_, err := scanWorkSpacesTopLevel(t, st, h)
			if tc.wantErr {
				assertAPICode(t, err, "InvalidParameterValuesException")
				if _, ok := rowsByNativeID(t, st, TypeWorkSpacesAccountLink)[wsTestLink1]; !ok {
					t.Errorf("link %s missing; the account-link phase must store before the add-in phase fails", wsTestLink1)
				}
			} else if err != nil {
				t.Fatalf("err = %v, want nil", err)
			}
			if got := nativeIDs(rowsByNativeID(t, st, TypeWorkSpacesConnectClientAddIn)); !slices.Equal(got, tc.wantAddIns) {
				t.Errorf("add-in rows = %v, want %v", got, tc.wantAddIns)
			}
			if *warns != tc.wantWarns {
				t.Errorf("warnings = %d, want %d", *warns, tc.wantWarns)
			}
		})
	}
}

func TestListWSConnectClientAddIns_StopsOnRepeatedToken(t *testing.T) {
	var calls atomic.Int32
	cfg := opStubConfig(map[string]func(in any) (any, error){
		"DescribeConnectClientAddIns": func(any) (any, error) {
			if calls.Add(1) > 2 {
				return nil, errors.New("called again after a repeated token")
			}
			return &workspaces.DescribeConnectClientAddInsOutput{NextToken: ptrStr("same")}, nil
		},
	}, wsDenied)
	client := workspaces.NewFromConfig(cfg)
	_, err := listWSConnectClientAddIns(context.Background(), client, newTestAccount(testAccountID), testRegion, testScanID,
		childParent{id: wsTestDirID1, arn: wsTestDir1})
	if err != nil {
		t.Fatalf("listWSConnectClientAddIns: %v", err)
	}
	if got := calls.Load(); got != 2 {
		t.Errorf("calls = %d, want 2", got)
	}
}
