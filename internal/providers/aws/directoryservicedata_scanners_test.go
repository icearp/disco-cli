package aws

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"net"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	awsmw "github.com/aws/aws-sdk-go-v2/aws/middleware"
	"github.com/aws/aws-sdk-go-v2/service/directoryservice"
	dstypes "github.com/aws/aws-sdk-go-v2/service/directoryservice/types"
	"github.com/aws/aws-sdk-go-v2/service/directoryservicedata"
	dsdatatypes "github.com/aws/aws-sdk-go-v2/service/directoryservicedata/types"
	"github.com/aws/smithy-go"
	"github.com/icearp/disco-cli/store"
)

func dsDataClient(handlers map[string]func(in any) (any, error)) *directoryservicedata.Client {
	return directoryservicedata.NewFromConfig(opStubConfig(handlers, dsDenied))
}

// dsSID is a SID in the test domain with relative identifier rid.
func dsSID(rid string) string { return "S-1-5-21-1111111111-2222222222-3333333333-" + rid }

// dsDataOps are the Directory Service Data phases, by op name.
var dsDataOps = []struct{ op, typ string }{
	{"ListUsers", TypeDSDataUser},
	{"ListGroups", TypeDSDataGroup},
}

// dsDataHandlers answers DescribeDirectories with dsTestDirectories and, for
// every directory, ListUsers and ListGroups with a well-known object (RID
// below 1000) and a SID-less element, then one user-created object on a
// second page. calls[op] records the directories asked. Ops in override
// answer instead.
func dsDataHandlers(calls map[string]*parentCalls, override map[string]func(any) (any, error)) map[string]func(any) (any, error) {
	for _, c := range dsDataOps {
		if calls[c.op] == nil {
			calls[c.op] = &parentCalls{}
		}
	}
	page1, page2 := dsTestDirectories()
	handlers := map[string]func(any) (any, error){
		"DescribeDirectories": func(in any) (any, error) {
			if in.(*directoryservice.DescribeDirectoriesInput).NextToken == nil {
				return &directoryservice.DescribeDirectoriesOutput{DirectoryDescriptions: page1, NextToken: ptrStr("p2")}, nil
			}
			return &directoryservice.DescribeDirectoriesOutput{DirectoryDescriptions: page2}, nil
		},
		"ListUsers": func(in any) (any, error) {
			req := in.(*directoryservicedata.ListUsersInput)
			calls["ListUsers"].add(*req.DirectoryId)
			if req.MaxResults == nil || *req.MaxResults != 250 {
				return nil, &smithy.GenericAPIError{Code: "ValidationException", Message: "want MaxResults 250, the documented page cap"}
			}
			if req.NextToken == nil {
				return &directoryservicedata.ListUsersOutput{Users: []dsdatatypes.UserSummary{
					{SID: ptrStr(dsSID("500")), SAMAccountName: ptrStr("Administrator"), Enabled: sdkaws.Bool(true)},
					{SAMAccountName: ptrStr("no-sid")},
				}, NextToken: ptrStr("p2")}, nil
			}
			return &directoryservicedata.ListUsersOutput{Users: []dsdatatypes.UserSummary{
				{SID: ptrStr(dsSID("1104")), SAMAccountName: ptrStr("jdoe"), GivenName: ptrStr("Jane"), Surname: ptrStr("Doe"), Enabled: sdkaws.Bool(false)},
			}}, nil
		},
		"ListGroups": func(in any) (any, error) {
			req := in.(*directoryservicedata.ListGroupsInput)
			calls["ListGroups"].add(*req.DirectoryId)
			if req.MaxResults == nil || *req.MaxResults != 250 {
				return nil, &smithy.GenericAPIError{Code: "ValidationException", Message: "want MaxResults 250, the documented page cap"}
			}
			if req.NextToken == nil {
				return &directoryservicedata.ListGroupsOutput{Groups: []dsdatatypes.GroupSummary{
					{SID: ptrStr("S-1-5-32-544"), SAMAccountName: ptrStr("Administrators"), GroupScope: dsdatatypes.GroupScopeBuiltinLocal, GroupType: dsdatatypes.GroupTypeSecurity},
					{SAMAccountName: ptrStr("no-sid")},
				}, NextToken: ptrStr("p2")}, nil
			}
			return &directoryservicedata.ListGroupsOutput{Groups: []dsdatatypes.GroupSummary{
				{SID: ptrStr(dsSID("1105")), SAMAccountName: ptrStr("app-admins"), GroupScope: dsdatatypes.GroupScopeGlobal, GroupType: dsdatatypes.GroupTypeSecurity},
			}}, nil
		},
	}
	maps.Copy(handlers, override)
	return handlers
}

// dsDataWantNativeIDs is every row a full scan of dsDataHandlers stores, by
// type: the readable directories are d-ms and d-std.
func dsDataWantNativeIDs() map[string][]string {
	ms, std := dsDirARN("d-ms"), dsDirARN("d-std")
	return map[string][]string{
		TypeDSDataUser: {
			ms + "/user/" + dsSID("1104"), ms + "/user/" + dsSID("500"),
			std + "/user/" + dsSID("1104"), std + "/user/" + dsSID("500"),
		},
		TypeDSDataGroup: {
			ms + "/group/" + dsSID("1105"), ms + "/group/S-1-5-32-544",
			std + "/group/" + dsSID("1105"), std + "/group/S-1-5-32-544",
		},
	}
}

// dsDataWantWithout is the sorted dsDataWantNativeIDs()[typ] less the rows
// under the directories dirIDs.
func dsDataWantWithout(typ string, dirIDs ...string) []string {
	var ids []string
	for _, id := range dsDataWantNativeIDs()[typ] {
		if !slices.ContainsFunc(dirIDs, func(d string) bool { return strings.HasPrefix(id, dsDirARN(d)+"/") }) {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	return ids
}

func runDSDataScan(t *testing.T, st *store.Store, handlers map[string]func(any) (any, error)) (int, error) {
	t.Helper()
	total, _, err := scanDirectoryServiceDataIn(context.Background(), dsClient(handlers), dsDataClient(handlers), newTestAccount(testAccountID), testRegion, st, testScanID)
	return total, err
}

// dsDataDirID returns the directory a Directory Service Data call names.
func dsDataDirID(in any) string {
	switch req := in.(type) {
	case *directoryservicedata.ListUsersInput:
		return *req.DirectoryId
	case *directoryservicedata.ListGroupsInput:
		return *req.DirectoryId
	}
	panic("unexpected input")
}

// dsDataNotFirstPage reports whether a Directory Service Data call asks for a
// later page.
func dsDataNotFirstPage(in any) bool {
	switch req := in.(type) {
	case *directoryservicedata.ListUsersInput:
		return req.NextToken != nil
	case *directoryservicedata.ListGroupsInput:
		return req.NextToken != nil
	}
	panic("unexpected input")
}

// Users and groups are stored keyed by SID under their directory, both pages
// of each readable directory, with the well-known (RID below 1000) objects
// provider-managed. The directories themselves and the contains edges are
// left to aws:directory-service and resolveDSDataHierarchy.
func TestScanDirectoryServiceDataIn_StoresUsersAndGroups(t *testing.T) {
	st := newTestStore(t)
	calls := map[string]*parentCalls{}
	total, err := runDSDataScan(t, st, dsDataHandlers(calls, nil))
	if err != nil || total != 8 {
		t.Fatalf("total, err = %d, %v; want 8, nil", total, err)
	}
	for typ, want := range dsDataWantNativeIDs() {
		sort.Strings(want)
		if got := dsStoredNativeIDs(t, st, typ); !slices.Equal(got, want) {
			t.Errorf("%s rows = %v, want %v", typ, got, want)
		}
	}
	for _, c := range dsDataOps {
		if got := calls[c.op].distinct(); !slices.Equal(got, []string{"d-ms", "d-std"}) {
			t.Errorf("%s asked for %v, want [d-ms d-std]", c.op, got)
		}
	}
	ms := dsDirARN("d-ms")
	for _, c := range []struct {
		typ, nativeID, name string
		managed             bool
	}{
		{TypeDSDataUser, ms + "/user/" + dsSID("500"), "Administrator", true},
		{TypeDSDataUser, ms + "/user/" + dsSID("1104"), "jdoe", false},
		{TypeDSDataGroup, ms + "/group/S-1-5-32-544", "Administrators", true},
		{TypeDSDataGroup, ms + "/group/" + dsSID("1105"), "app-admins", false},
	} {
		r, ok := rowsByNativeID(t, st, c.typ)[c.nativeID]
		if !ok || r.Type != c.typ || sv(r.Name) != c.name || sv(r.Region) != testRegion || r.ManagedByProvider != c.managed {
			t.Errorf("%s row = found %v type %q name %q region %q managed %v; want %q %q %q %v",
				c.nativeID, ok, r.Type, sv(r.Name), sv(r.Region), r.ManagedByProvider, c.typ, c.name, testRegion, c.managed)
		}
	}
	if n := len(rowsByNativeID(t, st, TypeDSMicrosoftAD)); n != 0 {
		t.Errorf("directory rows = %d, want 0 (aws:directory-service stores them)", n)
	}
	if rels, err := st.ListRelationships(); err != nil || len(rels) != 0 {
		t.Errorf("relationships = %d, %v; want none at scan time", len(rels), err)
	}

	var user dsdatatypes.UserSummary
	if err := json.Unmarshal([]byte(rowsByNativeID(t, st, TypeDSDataUser)[ms+"/user/"+dsSID("1104")].AttributesJSON), &user); err != nil ||
		sv(user.GivenName) != "Jane" || user.Enabled == nil || *user.Enabled {
		t.Errorf("user attrs = %+v, %v; want GivenName Jane, Enabled false", user, err)
	}
	var group dsdatatypes.GroupSummary
	if err := json.Unmarshal([]byte(rowsByNativeID(t, st, TypeDSDataGroup)[ms+"/group/S-1-5-32-544"].AttributesJSON), &group); err != nil ||
		group.GroupScope != dsdatatypes.GroupScopeBuiltinLocal {
		t.Errorf("group attrs GroupScope = %q, %v; want BUILTIN_LOCAL", group.GroupScope, err)
	}
}

// Groups are listed before users, so a deadline during the users phase still
// leaves the groups stored.
func TestScanDirectoryServiceDataIn_GroupsBeforeUsers(t *testing.T) {
	st := newTestStore(t)
	var (
		mu    sync.Mutex
		order []string
	)
	base := dsDataHandlers(map[string]*parentCalls{}, nil)
	logged := map[string]func(any) (any, error){}
	for _, c := range dsDataOps {
		logged[c.op] = func(in any) (any, error) {
			mu.Lock()
			order = append(order, c.op)
			mu.Unlock()
			return base[c.op](in)
		}
	}
	if _, err := runDSDataScan(t, st, dsDataHandlers(map[string]*parentCalls{}, logged)); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if last := slices.Index(order, "ListUsers"); last < 0 || slices.Contains(order[last:], "ListGroups") {
		t.Errorf("call order = %v; want every ListGroups before any ListUsers", order)
	}
}

func TestDSDataWellKnown(t *testing.T) {
	for _, c := range []struct {
		sid  string
		want bool
	}{
		{dsSID("500"), true},
		{dsSID("999"), true},
		{dsSID("1000"), false},
		{dsSID("1104"), false},
		{"S-1-5-32-544", true},
		{dsSID("x"), false},
		{"no-rid-", false},
		{"", false},
	} {
		if got := dsDataWellKnown(c.sid); got != c.want {
			t.Errorf("dsDataWellKnown(%q) = %v, want %v", c.sid, got, c.want)
		}
	}
}

// Directory Service Data is asked only of live owner-side Standard and
// Enterprise Managed Microsoft AD directories whose primary region is this one.
func TestDSDataReadable(t *testing.T) {
	dir := func(typ dstypes.DirectoryType, edition dstypes.DirectoryEdition, stage dstypes.DirectoryStage, regions *dstypes.RegionsInfo) dstypes.DirectoryDescription {
		d := dsDir("d-x", typ, edition, stage)
		d.RegionsInfo = regions
		return d
	}
	ms, ent, active := dstypes.DirectoryTypeMicrosoftAd, dstypes.DirectoryEditionEnterprise, dstypes.DirectoryStageActive
	for name, c := range map[string]struct {
		d    dstypes.DirectoryDescription
		want bool
	}{
		"single-region standard": {dir(ms, dstypes.DirectoryEditionStandard, active, nil), true},
		"primary here":           {dir(ms, ent, active, &dstypes.RegionsInfo{PrimaryRegion: ptrStr(testRegion), AdditionalRegions: []string{"eu-west-1"}}), true},
		"no primary region":      {dir(ms, ent, active, &dstypes.RegionsInfo{}), true},
		"replica here":           {dir(ms, ent, active, &dstypes.RegionsInfo{PrimaryRegion: ptrStr("eu-west-1"), AdditionalRegions: []string{testRegion}}), false},
		"deleting":               {dir(ms, ent, dstypes.DirectoryStageDeleting, nil), false},
		"hybrid":                 {dir(ms, dstypes.DirectoryEditionHybrid, active, nil), false},
		"simple ad":              {dir(dstypes.DirectoryTypeSimpleAd, "", active, nil), false},
		"consumer of a share":    {dir(dstypes.DirectoryTypeSharedMicrosoftAd, dstypes.DirectoryEditionStandard, active, nil), false},
	} {
		if got := dsDataReadable(c.d, testRegion); got != c.want {
			t.Errorf("%s: dsDataReadable = %v, want %v", name, got, c.want)
		}
	}
}

// A directory without Directory Service Data enabled is skipped silently; its
// siblings keep their rows.
func TestScanDirectoryServiceDataIn_DisabledSkippedSilently(t *testing.T) {
	for _, c := range dsDataOps {
		t.Run(c.op, func(t *testing.T) {
			st := newTestStore(t)
			warned := countOpWarnings(st, "ds-data:"+c.op)
			base := dsDataHandlers(map[string]*parentCalls{}, nil)
			handlers := dsDataHandlers(map[string]*parentCalls{}, map[string]func(any) (any, error){
				c.op: func(in any) (any, error) {
					if dsDataDirID(in) == "d-ms" {
						return nil, &dsdatatypes.AccessDeniedException{Message: ptrStr("data access disabled"), Reason: dsdatatypes.AccessDeniedReasonDataDisabled}
					}
					return base[c.op](in)
				},
			})
			if _, err := runDSDataScan(t, st, handlers); err != nil {
				t.Fatalf("scan: %v", err)
			}
			want := dsDataWantWithout(c.typ, "d-ms")
			if got := dsStoredNativeIDs(t, st, c.typ); !slices.Equal(got, want) || *warned != 0 {
				t.Errorf("%s rows = %v, warnings %d; want %v, 0", c.typ, got, *warned, want)
			}
		})
	}
}

// A real denial, however many directories it hits, warns once and stores none
// of the op's type; the other phase keeps its rows.
func TestScanDirectoryServiceDataIn_DeniedWarnsOnce(t *testing.T) {
	for _, c := range dsDataOps {
		t.Run(c.op, func(t *testing.T) {
			st := newTestStore(t)
			warned := countOpWarnings(st, "ds-data:"+c.op)
			handlers := dsDataHandlers(map[string]*parentCalls{}, map[string]func(any) (any, error){
				c.op: func(any) (any, error) {
					return nil, &dsdatatypes.AccessDeniedException{Message: ptrStr("not authorized"), Reason: dsdatatypes.AccessDeniedReasonIamAuth}
				},
			})
			total, err := runDSDataScan(t, st, handlers)
			if err != nil || total != 4 || *warned != 1 {
				t.Errorf("total, err, warnings = %d, %v, %d; want 4, nil, 1", total, err, *warned)
			}
			if n := len(rowsByNativeID(t, st, c.typ)); n != 0 {
				t.Errorf("%s rows = %d, want 0", c.typ, n)
			}
		})
	}
}

// A directory that cannot serve the call is skipped with one warning per op,
// keeping the page stored before it failed and its siblings' rows.
func TestScanDirectoryServiceDataIn_UnavailableWarnsOnceKeepsPages(t *testing.T) {
	for _, c := range dsDataOps {
		t.Run(c.op, func(t *testing.T) {
			st := newTestStore(t)
			warned := countOpWarnings(st, "ds-data:"+c.op)
			base := dsDataHandlers(map[string]*parentCalls{}, nil)
			unavailable := &dsdatatypes.DirectoryUnavailableException{Message: ptrStr("unavailable"), Reason: dsdatatypes.DirectoryUnavailableReasonInvalidDirectoryState}
			handlers := dsDataHandlers(map[string]*parentCalls{}, map[string]func(any) (any, error){
				c.op: func(in any) (any, error) {
					// d-ms fails on its second page, d-std on its first.
					if dsDataDirID(in) == "d-std" || dsDataNotFirstPage(in) {
						return nil, unavailable
					}
					return base[c.op](in)
				},
			})
			total, err := runDSDataScan(t, st, handlers)
			if err != nil {
				t.Fatalf("scan: %v", err)
			}
			var want []string
			for _, id := range dsDataWantWithout(c.typ, "d-std") {
				// The first page holds only the well-known object.
				if strings.HasSuffix(id, "-500") || strings.HasSuffix(id, "S-1-5-32-544") {
					want = append(want, id)
				}
			}
			if got := dsStoredNativeIDs(t, st, c.typ); !slices.Equal(got, want) || *warned != 1 {
				t.Errorf("%s rows = %v, warnings %d; want %v, 1", c.typ, got, *warned, want)
			}
			// The other phase's 4 rows plus d-ms's first page.
			if total != 5 {
				t.Errorf("total = %d, want 5", total)
			}
		})
	}
}

// Any other error is returned as a hard error beside the other phase's
// transient warning, and the other phase's rows are kept.
func TestScanDirectoryServiceDataIn_ErrorReturnedBesideTransient(t *testing.T) {
	for _, c := range []struct{ failing, throttled string }{
		{"ListUsers", "ListGroups"},
		{"ListGroups", "ListUsers"},
	} {
		t.Run(c.failing, func(t *testing.T) {
			st := newTestStore(t)
			warned := countOpWarnings(st, "ds-data:"+c.throttled)
			invalid := &dsdatatypes.ValidationException{Message: ptrStr("bad token"), Reason: dsdatatypes.ValidationExceptionReasonInvalidNextToken}
			handlers := dsDataHandlers(map[string]*parentCalls{}, map[string]func(any) (any, error){
				c.throttled: func(any) (any, error) { return nil, dsThrottled },
				c.failing:   func(any) (any, error) { return nil, invalid },
			})
			_, err := runDSDataScan(t, st, handlers)
			if !errors.Is(err, invalid) || classifyServiceError(err) != outcomeError || *warned != 1 {
				t.Errorf("err = %v classified %v, %s warnings %d; want the %s error, outcomeError, 1",
					err, classifyServiceError(err), c.throttled, *warned, c.failing)
			}
		})
	}
	t.Run("rows of the other phase kept", func(t *testing.T) {
		st := newTestStore(t)
		invalid := &dsdatatypes.ValidationException{Message: ptrStr("bad token")}
		handlers := dsDataHandlers(map[string]*parentCalls{}, map[string]func(any) (any, error){
			"ListGroups": func(any) (any, error) { return nil, invalid },
		})
		total, err := runDSDataScan(t, st, handlers)
		if !errors.Is(err, invalid) || total != 4 || len(rowsByNativeID(t, st, TypeDSDataUser)) != 4 {
			t.Errorf("total, err = %d, %v; want the 4 users and the ListGroups error", total, err)
		}
	})
}

// An unresolvable Directory Service Data host reaches the dispatcher as the
// DNS error, which it reports as the region unavailable without a warning.
func TestScanDirectoryServiceDataIn_HostUnresolvedIsUnavailable(t *testing.T) {
	st := newTestStore(t)
	warnings := 0
	st.OnWarn = func(store.ScanWarning) { warnings++ }
	nxdomain := &net.DNSError{Err: "no such host", Name: "ds-data.us-east-1.amazonaws.com", IsNotFound: true}
	handlers := dsDataHandlers(map[string]*parentCalls{}, map[string]func(any) (any, error){
		"ListUsers":  func(any) (any, error) { return nil, nxdomain },
		"ListGroups": func(any) (any, error) { return nil, nxdomain },
	})
	total, err := runDSDataScan(t, st, handlers)
	if got := classifyServiceError(err); got != outcomeUnavailable || total != 0 || warnings != 0 {
		t.Errorf("classifyServiceError(%v) = %v, total %d, warnings %d; want outcomeUnavailable, 0, 0", err, got, total, warnings)
	}
}

// Once the groups are stored, an unresolvable host on the users phase is an
// error reported with the groups' count, which the unavailable outcome would
// discard, so the scan is marked partial.
func TestScanDirectoryServiceDataIn_HostUnresolvedAfterRowsFails(t *testing.T) {
	st := newTestStore(t)
	warned := countOpWarnings(st, "ds-data:ListUsers")
	nxdomain := &net.DNSError{Err: "no such host", Name: "ds-data.us-east-1.amazonaws.com", IsNotFound: true}
	handlers := dsDataHandlers(map[string]*parentCalls{}, map[string]func(any) (any, error){
		"ListUsers": func(any) (any, error) { return nil, nxdomain },
	})
	total, err := runDSDataScan(t, st, handlers)
	if got := classifyServiceError(err); got != outcomeError || total != 4 || *warned != 0 {
		t.Errorf("classifyServiceError(%v) = %v, total %d, warnings %d; want outcomeError, 4, 0", err, got, total, *warned)
	}
}

// A denied or unreachable directory listing calls no Directory Service Data
// op: denied warns once, an unresolvable host is reported unavailable.
func TestScanDirectoryServiceDataIn_DirectoryListingFails(t *testing.T) {
	t.Run("denied", func(t *testing.T) {
		st := newTestStore(t)
		warned := countOpWarnings(st, "ds:DescribeDirectories")
		calls := map[string]*parentCalls{}
		handlers := dsDataHandlers(calls, nil)
		delete(handlers, "DescribeDirectories")
		if total, err := runDSDataScan(t, st, handlers); err != nil || total != 0 || *warned != 1 || len(calls["ListUsers"].distinct()) != 0 {
			t.Errorf("total, err, warnings = %d, %v, %d, users asked %v; want 0, nil, 1, none", total, err, *warned, calls["ListUsers"].distinct())
		}
	})
	t.Run("unresolvable", func(t *testing.T) {
		st := newTestStore(t)
		handlers := dsDataHandlers(map[string]*parentCalls{}, map[string]func(any) (any, error){
			"DescribeDirectories": func(any) (any, error) {
				return nil, &net.DNSError{Err: "no such host", Name: "ds.us-east-1.amazonaws.com", IsNotFound: true}
			},
		})
		if _, err := runDSDataScan(t, st, handlers); classifyServiceError(err) != outcomeUnavailable {
			t.Errorf("classifyServiceError(%v) = %v, want outcomeUnavailable", err, classifyServiceError(err))
		}
	})
}

// Directories with no users or groups store nothing and fail nothing.
func TestScanDirectoryServiceDataIn_Empty(t *testing.T) {
	st := newTestStore(t)
	handlers := dsDataHandlers(map[string]*parentCalls{}, map[string]func(any) (any, error){
		"ListUsers":  func(any) (any, error) { return &directoryservicedata.ListUsersOutput{}, nil },
		"ListGroups": func(any) (any, error) { return &directoryservicedata.ListGroupsOutput{}, nil },
	})
	if total, err := runDSDataScan(t, st, handlers); err != nil || total != 0 {
		t.Errorf("total, err = %d, %v; want 0, nil", total, err)
	}
}

// Both clients call the region being scanned, the directory's own, not the
// region the account config names.
func TestScanDirectoryServiceData_CallsScanRegion(t *testing.T) {
	const region = "eu-west-1"
	st := newTestStore(t)
	var regions parentCalls
	record := func(ctx context.Context, out any) (any, error) {
		regions.add(awsmw.GetOperationName(ctx) + "@" + awsmw.GetRegion(ctx))
		return out, nil
	}
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfigCtx(map[string]func(context.Context, any) (any, error){
		"DescribeDirectories": func(ctx context.Context, _ any) (any, error) {
			return record(ctx, &directoryservice.DescribeDirectoriesOutput{DirectoryDescriptions: []dstypes.DirectoryDescription{
				dsDir("d-std", dstypes.DirectoryTypeMicrosoftAd, dstypes.DirectoryEditionStandard, dstypes.DirectoryStageActive),
			}})
		},
		"ListUsers": func(ctx context.Context, _ any) (any, error) {
			return record(ctx, &directoryservicedata.ListUsersOutput{Users: []dsdatatypes.UserSummary{{SID: ptrStr(dsSID("1104"))}}})
		},
		"ListGroups": func(ctx context.Context, _ any) (any, error) {
			return record(ctx, &directoryservicedata.ListGroupsOutput{})
		},
	}, dsDenied)
	if acct.cfg.Region == region {
		t.Fatalf("config region %q must differ from the scan region", acct.cfg.Region)
	}
	if _, _, err := scanDirectoryServiceData(context.Background(), acct, region, st, testScanID); err != nil {
		t.Fatalf("scan: %v", err)
	}
	want := []string{"DescribeDirectories@" + region, "ListGroups@" + region, "ListUsers@" + region}
	if got := regions.distinct(); !slices.Equal(got, want) {
		t.Errorf("calls = %v; want %v", got, want)
	}
	if _, ok := rowsByNativeID(t, st, TypeDSDataUser)[dsDirectoryARN("aws", region, testAccountID, "d-std")+"/user/"+dsSID("1104")]; !ok {
		t.Errorf("no user row under the %s directory", region)
	}
}
