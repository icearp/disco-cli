package aws

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"maps"
	"net"
	"slices"
	"strings"
	"sync"
	"testing"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/workspaces"
	wstypes "github.com/aws/aws-sdk-go-v2/service/workspaces/types"
	"github.com/icearp/disco-cli/store"
)

const (
	// wsTestListed is the rows of the two-page tagged listings: two per
	// listing, one per page.
	wsTestListed = 2 * 7
	// wsTestStored is every row the scan stores: the tagged listings' and
	// the permission row of each of the two owned aliases.
	wsTestStored  = wsTestListed + 2
	wsTestTagsOp  = "workspaces:DescribeTags"
	wsTestOther   = "210987654321"
	wsTestInvalid = "InvalidParameterValuesException"
)

var (
	wsThrottled = apiErr("ThrottlingException", "rate exceeded")
	wsInvalid   = apiErr(wsTestInvalid, "bad input")
)

// wsKind is one listing whose rows have their tags read: the Describe op, the
// type it stores, the resource type of its rows' ARNs, the prefix of its ids,
// the field the op must be sent the page cap 25 in ("" for none), the Status
// its rows carry ("" for none), whether a row is named by its id rather than
// its element's name, elem, which builds one element (arn is used only by
// elements that carry their ARN), and wrap, which builds the op's output
// holding that element, with next.
type wsKind struct {
	op, typ, arnKind, idPrefix string
	pageField, status          string
	namedByID                  bool
	elem                       func(arn, id, name string) any
	wrap                       func(elem any, next *string) any
}

func (k wsKind) id(suffix string) string   { return k.idPrefix + suffix }
func (k wsKind) name(suffix string) string { return "name-" + k.id(suffix) }

func (k wsKind) arnIn(partition, region, suffix string) string {
	return "arn:" + partition + ":workspaces:" + region + ":" + testAccountID + ":" + k.arnKind + "/" + k.id(suffix)
}

func (k wsKind) arn(suffix string) string { return k.arnIn("aws", testRegion, suffix) }

func wsKinds() []wsKind {
	return []wsKind{
		{"DescribeWorkspaces", TypeWorkSpacesWorkspace, "workspace", "ws-", "Limit", "AVAILABLE", true, func(_, id, name string) any {
			return wstypes.Workspace{WorkspaceId: &id, ComputerName: &name, State: wstypes.WorkspaceStateAvailable}
		}, func(e any, next *string) any {
			return &workspaces.DescribeWorkspacesOutput{NextToken: next, Workspaces: []wstypes.Workspace{e.(wstypes.Workspace)}}
		}},
		{"DescribeConnectionAliases", TypeWorkSpacesConnectionAlias, "connectionalias", "wsca-", "Limit", "CREATED", false, func(_, id, name string) any {
			return wstypes.ConnectionAlias{AliasId: &id, ConnectionString: &name, OwnerAccountId: sdkaws.String(testAccountID), State: wstypes.ConnectionAliasStateCreated}
		}, func(e any, next *string) any {
			return &workspaces.DescribeConnectionAliasesOutput{NextToken: next, ConnectionAliases: []wstypes.ConnectionAlias{e.(wstypes.ConnectionAlias)}}
		}},
		{"DescribeWorkspacesPools", TypeWorkSpacesWorkspacesPool, "workspacespool", "wspool-", "Limit", "", false, func(arn, id, name string) any {
			return wstypes.WorkspacesPool{PoolArn: &arn, PoolId: &id, PoolName: &name, State: wstypes.WorkspacesPoolStateRunning}
		}, func(e any, next *string) any {
			return &workspaces.DescribeWorkspacesPoolsOutput{NextToken: next, WorkspacesPools: []wstypes.WorkspacesPool{e.(wstypes.WorkspacesPool)}}
		}},
		{"DescribeWorkspaceDirectories", TypeWorkSpacesDirectory, "directory", "d-", "Limit", "REGISTERED", false, func(_, id, name string) any {
			return wstypes.WorkspaceDirectory{DirectoryId: &id, DirectoryName: &name, State: wstypes.WorkspaceDirectoryStateRegistered}
		}, func(e any, next *string) any {
			return &workspaces.DescribeWorkspaceDirectoriesOutput{NextToken: next, Directories: []wstypes.WorkspaceDirectory{e.(wstypes.WorkspaceDirectory)}}
		}},
		{"DescribeWorkspaceBundles", TypeWorkSpacesWorkspaceBundle, "workspacebundle", "wsb-", "", "", false, func(_, id, name string) any {
			return wstypes.WorkspaceBundle{BundleId: &id, Name: &name, Owner: sdkaws.String(testAccountID)}
		}, func(e any, next *string) any {
			return &workspaces.DescribeWorkspaceBundlesOutput{NextToken: next, Bundles: []wstypes.WorkspaceBundle{e.(wstypes.WorkspaceBundle)}}
		}},
		{"DescribeWorkspaceImages", TypeWorkSpacesWorkspaceImage, "workspaceimage", "wsi-", "MaxResults", "AVAILABLE", false, func(_, id, name string) any {
			return wstypes.WorkspaceImage{ImageId: &id, Name: &name, OwnerAccountId: sdkaws.String(testAccountID), State: wstypes.WorkspaceImageStateAvailable}
		}, func(e any, next *string) any {
			return &workspaces.DescribeWorkspaceImagesOutput{NextToken: next, Images: []wstypes.WorkspaceImage{e.(wstypes.WorkspaceImage)}}
		}},
		{"DescribeIpGroups", TypeWorkSpacesWorkspaceIPGroup, "workspaceipgroup", "wsipg-", "MaxResults", "", false, func(_, id, name string) any {
			return wstypes.WorkspacesIpGroup{GroupId: &id, GroupName: &name}
		}, func(e any, next *string) any {
			return &workspaces.DescribeIpGroupsOutput{NextToken: next, Result: []wstypes.WorkspacesIpGroup{e.(wstypes.WorkspacesIpGroup)}}
		}},
	}
}

func wsKindOf(t *testing.T, op string) wsKind {
	t.Helper()
	for _, k := range wsKinds() {
		if k.op == op {
			return k
		}
	}
	t.Fatalf("%s is no tagged WorkSpaces listing", op)
	return wsKind{}
}

// wsTagCalls records the calls of a scan and answers DescribeTags with tags,
// or errs, by ResourceId.
type wsTagCalls struct {
	mu     sync.Mutex
	inputs map[string][]any
	tagIDs []string
	tags   map[string]map[string]string
	errs   map[string]error
}

// newWSTagCalls tags the "tagged" row of every kind with its type.
func newWSTagCalls() *wsTagCalls {
	c := &wsTagCalls{inputs: map[string][]any{}, tags: map[string]map[string]string{}, errs: map[string]error{}}
	for _, k := range wsKinds() {
		c.tags[k.id("tagged")] = map[string]string{"type": k.typ}
	}
	return c
}

func (c *wsTagCalls) record(op string, in any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.inputs[op] = append(c.inputs[op], in)
}

func (c *wsTagCalls) count(op string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.inputs[op])
}

// sent is the string field of every input op was called with, sorted.
func (c *wsTagCalls) sent(op, field string) []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	var got []string
	for _, in := range c.inputs[op] {
		got = append(got, sv(bacField[*string](in, field)))
	}
	slices.Sort(got)
	return got
}

func (c *wsTagCalls) sentTagIDs() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Sorted(slices.Values(c.tagIDs))
}

func (c *wsTagCalls) describeTags(in any) (any, error) {
	id := sv(in.(*workspaces.DescribeTagsInput).ResourceId)
	c.record("DescribeTags", in)
	c.mu.Lock()
	c.tagIDs = append(c.tagIDs, id)
	err, tags := c.errs[id], c.tags[id]
	c.mu.Unlock()
	if err != nil {
		return nil, err
	}
	out := &workspaces.DescribeTagsOutput{}
	for _, key := range slices.Sorted(maps.Keys(tags)) {
		out.TagList = append(out.TagList, wstypes.Tag{Key: sdkaws.String(key), Value: sdkaws.String(tags[key])})
	}
	return out, nil
}

// twoPages answers k's op with "tagged" on the first page and "bare" on the
// second, its rows in partition and region.
func (c *wsTagCalls) twoPages(k wsKind, partition, region string) func(any) (any, error) {
	return func(in any) (any, error) {
		c.record(k.op, in)
		suffix, next := "tagged", sdkaws.String("t2")
		if bacField[*string](in, "NextToken") != nil {
			suffix, next = "bare", nil
		}
		return k.wrap(k.elem(k.arnIn(partition, region, suffix), k.id(suffix), k.name(suffix)), next), nil
	}
}

// wsTagHandlersIn answers every WorkSpaces op the scan calls, its rows in
// partition and region: each tagged kind in two pages, every other listing
// empty.
func wsTagHandlersIn(c *wsTagCalls, partition, region string) map[string]func(in any) (any, error) {
	// Each call gets a new output: the SDK writes ResultMetadata into it, and
	// the child ops run concurrently.
	empty := func(op string, out func() any) func(any) (any, error) {
		return func(in any) (any, error) {
			c.record(op, in)
			return out(), nil
		}
	}
	h := map[string]func(in any) (any, error){
		"DescribeTags":         c.describeTags,
		"DescribeApplications": empty("DescribeApplications", func() any { return &workspaces.DescribeApplicationsOutput{} }),
		"ListAccountLinks":     empty("ListAccountLinks", func() any { return &workspaces.ListAccountLinksOutput{} }),
		"DescribeConnectClientAddIns": empty("DescribeConnectClientAddIns", func() any {
			return &workspaces.DescribeConnectClientAddInsOutput{}
		}),
		"DescribeConnectionAliasPermissions": empty("DescribeConnectionAliasPermissions", func() any {
			return &workspaces.DescribeConnectionAliasPermissionsOutput{}
		}),
	}
	for _, k := range wsKinds() {
		h[k.op] = c.twoPages(k, partition, region)
	}
	return h
}

func wsTagHandlers(c *wsTagCalls) map[string]func(in any) (any, error) {
	return wsTagHandlersIn(c, "aws", testRegion)
}

// wsTagScan runs the scan's real entry point against handlers; every op they
// do not answer is denied.
func wsTagScan(ctx context.Context, st *store.Store, handlers map[string]func(in any) (any, error)) (int, int, error) {
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(handlers, wsDenied)
	return scanWorkSpaces(ctx, acct, testRegion, st, testScanID)
}

func wsTagReadIDs() []string {
	var ids []string
	for _, k := range wsKinds() {
		ids = append(ids, k.id("tagged"), k.id("bare"))
	}
	slices.Sort(ids)
	return ids
}

// Every tagged listing stores both its pages, each row under its ARN (a
// pool's own PoolArn, the others' built from their ids), named by its name (a
// workspace by its id), with its Status, the listed element as attributes and
// the tags DescribeTags returns for its id, nil for a resource without tags.
// Exactly those ids are sent to the tag op. Each listing is sent the page cap
// 25 where it takes one and is called once per page. The directories and the
// owned aliases stored are read for their children. No error, no warning.
func TestScanWorkSpaces_StoresTagsOfEveryType(t *testing.T) {
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	calls := newWSTagCalls()

	total, inserted, err := wsTagScan(context.Background(), st, wsTagHandlers(calls))
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != wsTestStored || inserted != wsTestStored {
		t.Errorf("total, inserted = %d, %d; want %d, %d", total, inserted, wsTestStored, wsTestStored)
	}
	if w := warns(); len(w) != 0 {
		t.Errorf("warnings = %v; want none", w)
	}
	acctName := newTestAccount(testAccountID).Name
	for _, k := range wsKinds() {
		rows := rowsByNativeID(t, st, k.typ)
		if len(rows) != 2 {
			t.Errorf("%s rows = %v; want 2", k.typ, nativeIDs(rows))
		}
		for _, suffix := range []string{"tagged", "bare"} {
			arn := k.arn(suffix)
			r, ok := rows[arn]
			if !ok {
				t.Errorf("%s %s missing", k.typ, arn)
				continue
			}
			wantName := k.name(suffix)
			if k.namedByID {
				wantName = k.id(suffix)
			}
			if sv(r.Name) != wantName || sv(r.Region) != testRegion || sv(r.Status) != k.status || (k.status == "") != (r.Status == nil) || r.CreatedAt != nil || r.ManagedByProvider {
				t.Errorf("%s Name, Region, Status, CreatedAt, Managed = %q, %q, %v, %v, %v; want %q, %q, %q, nil, false",
					arn, sv(r.Name), sv(r.Region), r.Status, r.CreatedAt, r.ManagedByProvider, wantName, testRegion, k.status)
			}
			if r.Provider != "aws" || r.AccountID != testAccountID || sv(r.AccountName) != acctName || r.DiscoveredBy != testScanID {
				t.Errorf("%s Provider, AccountID, AccountName, DiscoveredBy = %q, %q, %q, %q; want aws, %s, %s, %s",
					arn, r.Provider, r.AccountID, sv(r.AccountName), r.DiscoveredBy, testAccountID, acctName, testScanID)
			}
			if want := mustJSON(k.elem(arn, k.id(suffix), k.name(suffix))); r.AttributesJSON != want {
				t.Errorf("%s attrs = %s; want the listed element %s", arn, r.AttributesJSON, want)
			}
			if suffix == "tagged" {
				assertTagsMap(t, r.TagsJSON, map[string]string{"type": k.typ})
			} else if r.TagsJSON != nil {
				t.Errorf("%s TagsJSON = %q; want nil", arn, *r.TagsJSON)
			}
		}
		for _, in := range calls.inputs[k.op] {
			for _, field := range []string{"Limit", "MaxResults"} {
				n := bacField[*int32](in, field)
				switch {
				case field != k.pageField && n != nil:
					t.Errorf("%s %s = %d; want none sent", k.op, field, *n)
				case field == k.pageField && (n == nil || *n != 25):
					t.Errorf("%s %s = %v; want 25", k.op, field, n)
				}
			}
		}
		if n := calls.count(k.op); n != 2 {
			t.Errorf("%s calls = %d; want 2", k.op, n)
		}
	}
	if got := calls.sentTagIDs(); !slices.Equal(got, wsTagReadIDs()) {
		t.Errorf("DescribeTags ResourceIds = %v\nwant %v", got, wsTagReadIDs())
	}
	dirs := wsKindOf(t, "DescribeWorkspaceDirectories")
	if got, want := calls.sent("DescribeConnectClientAddIns", "ResourceId"), []string{dirs.id("bare"), dirs.id("tagged")}; !slices.Equal(got, want) {
		t.Errorf("add-ins read for %v; want %v", got, want)
	}
	aliases := wsKindOf(t, "DescribeConnectionAliases")
	if got, want := calls.sent("DescribeConnectionAliasPermissions", "AliasId"), []string{aliases.id("bare"), aliases.id("tagged")}; !slices.Equal(got, want) {
		t.Errorf("alias permissions read for %v; want %v", got, want)
	}
}

// Empty listings store nothing, read no tags or children and warn nothing.
func TestScanWorkSpaces_EmptyListings(t *testing.T) {
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	calls := newWSTagCalls()
	h := wsTagHandlers(calls)
	for op, out := range map[string]any{
		"DescribeWorkspaces":           &workspaces.DescribeWorkspacesOutput{},
		"DescribeConnectionAliases":    &workspaces.DescribeConnectionAliasesOutput{},
		"DescribeWorkspacesPools":      &workspaces.DescribeWorkspacesPoolsOutput{},
		"DescribeWorkspaceDirectories": &workspaces.DescribeWorkspaceDirectoriesOutput{},
		"DescribeWorkspaceBundles":     &workspaces.DescribeWorkspaceBundlesOutput{},
		"DescribeWorkspaceImages":      &workspaces.DescribeWorkspaceImagesOutput{},
		"DescribeIpGroups":             &workspaces.DescribeIpGroupsOutput{},
	} {
		h[op] = func(any) (any, error) { return out, nil }
	}
	total, inserted, err := wsTagScan(context.Background(), st, h)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != 0 || inserted != 0 {
		t.Errorf("total, inserted = %d, %d; want 0, 0", total, inserted)
	}
	for _, op := range []string{"DescribeTags", "DescribeConnectClientAddIns", "DescribeConnectionAliasPermissions"} {
		if n := calls.count(op); n != 0 {
			t.Errorf("%s calls = %d; want 0", op, n)
		}
	}
	if w := warns(); len(w) != 0 {
		t.Errorf("warnings = %v; want none", w)
	}
}

// A rescan stores the same rows without inserting any.
func TestScanWorkSpaces_RescanInsertsNone(t *testing.T) {
	st := newTestStore(t)
	if _, _, err := wsTagScan(context.Background(), st, wsTagHandlers(newWSTagCalls())); err != nil {
		t.Fatalf("first scan: %v", err)
	}
	total, inserted, err := wsTagScan(context.Background(), st, wsTagHandlers(newWSTagCalls()))
	if err != nil {
		t.Fatalf("rescan: %v", err)
	}
	if total != wsTestStored || inserted != 0 {
		t.Errorf("rescan total, inserted = %d, %d; want %d, 0", total, inserted, wsTestStored)
	}
}

// A failed tag read: ResourceNotFoundException (the one code the op models)
// drops the row silently, and a directory or owned alias so dropped is not
// read for children; a denial keeps the row untagged and warns once under
// the tag op; a throttle keeps it and is transient; any other error keeps it
// and is an error.
func TestScanWorkSpaces_TagReadFailures(t *testing.T) {
	type tagCase struct {
		name      string
		k         wsKind
		err       error
		outcome   serviceOutcome // -1: no error
		code      string
		wantTotal int
		wantKept  bool
		wantWarns int
	}
	var cases []tagCase
	for _, k := range wsKinds() {
		// A dropped alias also has no permission row.
		gone := wsTestStored - 1
		if k.op == "DescribeConnectionAliases" {
			gone--
		}
		cases = append(cases,
			tagCase{k.typ + "/not found", k, wsGone, -1, "", gone, false, 0},
			tagCase{k.typ + "/denied", k, wsDenied, -1, "", wsTestStored, true, 1},
			tagCase{k.typ + "/transient", k, wsThrottled, outcomeTransient, "ThrottlingException", wsTestStored, true, 0},
			tagCase{k.typ + "/other error", k, wsInvalid, outcomeError, wsTestInvalid, wsTestStored, true, 0},
		)
	}
	childOf := map[string]struct{ op, field string }{
		"DescribeWorkspaceDirectories": {"DescribeConnectClientAddIns", "ResourceId"},
		"DescribeConnectionAliases":    {"DescribeConnectionAliasPermissions", "AliasId"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			calls := newWSTagCalls()
			id, arn := c.k.id("tagged"), c.k.arn("tagged")
			calls.errs[id] = c.err

			total, _, err := wsTagScan(context.Background(), st, wsTagHandlers(calls))
			if c.outcome < 0 {
				if err != nil {
					t.Fatalf("err = %v; want nil", err)
				}
			} else {
				assertGlueOutcome(t, err, c.outcome)
				assertAPICode(t, err, c.code)
			}
			if total != c.wantTotal {
				t.Errorf("total = %d; want %d", total, c.wantTotal)
			}
			r, ok := rowsByNativeID(t, st, c.k.typ)[arn]
			if ok != c.wantKept {
				t.Fatalf("row stored = %v; want %v", ok, c.wantKept)
			}
			if ok && r.TagsJSON != nil {
				t.Errorf("TagsJSON = %q; want nil", *r.TagsJSON)
			}
			if child, isParent := childOf[c.k.op]; isParent {
				if got := slices.Contains(calls.sent(child.op, child.field), id); got != c.wantKept {
					t.Errorf("%s read for %s = %v; want %v", child.op, id, got, c.wantKept)
				}
			}
			w := warns()
			if len(w) != c.wantWarns {
				t.Fatalf("warnings = %v; want %d", w, c.wantWarns)
			}
			for _, x := range w {
				if x.Service != wsTestTagsOp || x.Scope != testAccountID+"/"+testRegion || !strings.Contains(x.Message, arn) {
					t.Errorf("warning %+v; want one under %s in %s/%s naming %s", x, wsTestTagsOp, testAccountID, testRegion, arn)
				}
			}
		})
	}
}

// Denied tag reads in two listings send one warning for the whole scan,
// under the tag op, naming the first denied row; the rows are kept untagged.
func TestScanWorkSpaces_TagDenialsWarnOncePerScan(t *testing.T) {
	st := newTestStore(t)
	warnings := bacWarnings(st, wsTestTagsOp)
	calls := newWSTagCalls()
	ops := []string{"DescribeWorkspaces", "DescribeWorkspaceImages"}
	for _, op := range ops {
		k := wsKindOf(t, op)
		calls.errs[k.id("tagged")] = wsDenied
		calls.errs[k.id("bare")] = wsDenied
	}

	total, _, err := wsTagScan(context.Background(), st, wsTagHandlers(calls))
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != wsTestStored {
		t.Errorf("total = %d; want %d", total, wsTestStored)
	}
	first := wsKindOf(t, "DescribeWorkspaces").arn("tagged")
	if len(*warnings) != 1 || (*warnings)[0].Scope != testAccountID+"/"+testRegion || !strings.Contains((*warnings)[0].Message, first) {
		t.Fatalf("%s warnings = %v; want 1 in %s/%s naming %s", wsTestTagsOp, *warnings, testAccountID, testRegion, first)
	}
	for _, op := range ops {
		k := wsKindOf(t, op)
		for _, arn := range []string{k.arn("tagged"), k.arn("bare")} {
			if r, ok := rowsByNativeID(t, st, k.typ)[arn]; !ok || r.TagsJSON != nil {
				t.Errorf("%s stored %v; want stored untagged", arn, ok)
			}
		}
	}
}

// A throttled tag read beside a hard failure of the same op is demoted to one
// warning, so the hard error decides the outcome.
func TestScanWorkSpaces_TagThrottleDemotedBesideHardError(t *testing.T) {
	st := newTestStore(t)
	warns := countOpWarnings(st, wsTestTagsOp)
	calls := newWSTagCalls()
	calls.errs[wsKindOf(t, "DescribeWorkspacesPools").id("tagged")] = wsThrottled
	calls.errs[wsKindOf(t, "DescribeIpGroups").id("bare")] = wsInvalid

	_, _, err := wsTagScan(context.Background(), st, wsTagHandlers(calls))
	assertGlueOutcome(t, err, outcomeError)
	assertAPICode(t, err, wsTestInvalid)
	if *warns != 1 {
		t.Errorf("%s warnings = %d; want 1", wsTestTagsOp, *warns)
	}
}

// A failed write stops the scan: no later phase runs and no warning is sent
// about rows the write lost; the phases before it keep their rows.
func TestScanWorkSpaces_FailedWriteStopsScan(t *testing.T) {
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	refuseStoreWrites(t, st, TypeWorkSpacesWorkspaceBundle)
	calls := newWSTagCalls()
	k := wsKindOf(t, "DescribeWorkspaceBundles")
	calls.errs[k.id("tagged")] = wsDenied

	total, _, err := wsTagScan(context.Background(), st, wsTagHandlers(calls))
	if !errors.Is(err, store.ErrStoreWrite) {
		t.Fatalf("err = %v; want store.ErrStoreWrite", err)
	}
	if w := warns(); len(w) != 0 {
		t.Errorf("warnings = %v; want none", w)
	}
	if n := calls.count(k.op); n != 1 {
		t.Errorf("%s called %d times; want 1, none after the failed write", k.op, n)
	}
	for _, op := range []string{"DescribeWorkspaceImages", "DescribeIpGroups", "DescribeApplications", "ListAccountLinks", "DescribeConnectClientAddIns", "DescribeConnectionAliasPermissions"} {
		if n := calls.count(op); n != 0 {
			t.Errorf("%s called %d times after the failed write; want 0", op, n)
		}
	}
	// Workspaces, aliases, pools and directories (2 each) ran before it.
	if total != 4*2 {
		t.Errorf("total = %d; want %d", total, 4*2)
	}
}

// A listing's failed page keeps the page read before it, and its error stops
// no later listing. The workspaces listing, which once ended every older
// phase on its first error, now keeps its first page and lets the rest run.
func TestScanWorkSpaces_ListFailureKeepsPagesAndLaterPhases(t *testing.T) {
	for _, op := range []string{"DescribeWorkspaces", "DescribeWorkspaceDirectories", "DescribeIpGroups"} {
		t.Run(op, func(t *testing.T) {
			st := newTestStore(t)
			calls := newWSTagCalls()
			handlers := wsTagHandlers(calls)
			k := wsKindOf(t, op)
			handlers[k.op] = failOnCall(2, wsInvalid, handlers[k.op])

			total, _, err := wsTagScan(context.Background(), st, handlers)
			assertGlueOutcome(t, err, outcomeError)
			assertAPICode(t, err, wsTestInvalid)
			if total != wsTestStored-1 {
				t.Errorf("total = %d; want %d", total, wsTestStored-1)
			}
			if _, ok := rowsByNativeID(t, st, k.typ)[k.arn("tagged")]; !ok {
				t.Errorf("%s from the page before the failure missing", k.typ)
			}
			if got := len(rowsByNativeID(t, st, TypeWorkSpacesWorkspaceIPGroup)); op != "DescribeIpGroups" && got != 2 {
				t.Errorf("IP groups (a later phase) = %d; want 2", got)
			}
			if n := calls.count("DescribeConnectionAliasPermissions"); n == 0 {
				t.Error("alias permissions (the last phase) not read")
			}
		})
	}
}

// A repeated token ends a listing as an error, keeping its pages.
func TestScanWorkSpaces_RepeatedTokenKeepsPages(t *testing.T) {
	st := newTestStore(t)
	calls := newWSTagCalls()
	handlers := wsTagHandlers(calls)
	k := wsKindOf(t, "DescribeWorkspaceImages")
	handlers[k.op] = func(in any) (any, error) {
		calls.record(k.op, in)
		suffix := "tagged"
		if bacField[*string](in, "NextToken") != nil {
			suffix = "bare"
		}
		return k.wrap(k.elem(k.arn(suffix), k.id(suffix), k.name(suffix)), sdkaws.String("same")), nil
	}
	total, _, err := wsTagScan(context.Background(), st, handlers)
	if !errors.Is(err, errRepeatedNextToken) {
		t.Fatalf("err = %v; want errRepeatedNextToken", err)
	}
	if total != wsTestStored {
		t.Errorf("total = %d; want %d", total, wsTestStored)
	}
	if n := calls.count(k.op); n != 2 {
		t.Errorf("%s calls = %d; want 2", k.op, n)
	}
	if got := nativeIDs(rowsByNativeID(t, st, k.typ)); !slices.Equal(got, []string{k.arn("bare"), k.arn("tagged")}) {
		t.Errorf("images = %v; want both pages'", got)
	}
}

// An element without the id its NativeID is built from (a pool: without its
// PoolArn) is not stored, is not sent to the tag op, is not read for
// children, and is warned about once under its listing, named by its name or
// by the fallback text when it has none.
func TestScanWorkSpaces_ElementWithoutIDWarns(t *testing.T) {
	for _, k := range wsKinds() {
		for _, name := range []string{"named", ""} {
			t.Run(k.op+"/"+cmp.Or(name, "nameless"), func(t *testing.T) {
				st := newTestStore(t)
				warns := rhWarnRecorder(st)
				calls := newWSTagCalls()
				handlers := wsTagHandlers(calls)
				elem := wsWithoutID(t, k.elem("", "", name))
				handlers[k.op] = func(any) (any, error) { return k.wrap(elem, nil), nil }

				total, _, err := wsTagScan(context.Background(), st, handlers)
				if err != nil {
					t.Fatalf("scan: %v", err)
				}
				// An alias not stored has no permission row either.
				want := wsTestStored - 2
				if k.op == "DescribeConnectionAliases" {
					want -= 2
				}
				if total != want {
					t.Errorf("total = %d; want %d", total, want)
				}
				key := cmp.Or(name, noIdentifier)
				w := warns()
				if len(w) != 1 || w[0].Service != "workspaces:"+k.op || !strings.Contains(w[0].Message, key) || w[0].Scope != testAccountID+"/"+testRegion {
					t.Fatalf("warnings = %v; want one under workspaces:%s naming %q", w, k.op, key)
				}
				if got := calls.sentTagIDs(); slices.Contains(got, "") || len(got) != wsTestListed-2 {
					t.Errorf("DescribeTags ResourceIds = %v; want the %d other rows' ids", got, wsTestListed-2)
				}
			})
		}
	}
}

// wsWithoutID is elem, a listed element, with the field its NativeID is built
// from cleared, and the label field cleared too when name is empty.
func wsWithoutID(t *testing.T, elem any) any {
	t.Helper()
	switch e := elem.(type) {
	case wstypes.Workspace:
		e.WorkspaceId = nil
		return e
	case wstypes.ConnectionAlias:
		e.AliasId = nil
		return e
	case wstypes.WorkspacesPool:
		e.PoolArn = nil
		return e
	case wstypes.WorkspaceDirectory:
		e.DirectoryId = nil
		return e
	case wstypes.WorkspaceBundle:
		e.BundleId = nil
		return e
	case wstypes.WorkspaceImage:
		e.ImageId = nil
		return e
	case wstypes.WorkspacesIpGroup:
		e.GroupId = nil
		return e
	default:
		t.Fatalf("no id field known for %T", elem)
		return nil
	}
}

// An AMAZON bundle is stored ManagedByProvider and has no tags read; the
// account's own bundles are read.
func TestScanWorkSpaces_AmazonBundleNotTagRead(t *testing.T) {
	st := newTestStore(t)
	calls := newWSTagCalls()
	handlers := wsTagHandlers(calls)
	k := wsKindOf(t, "DescribeWorkspaceBundles")
	own := handlers[k.op]
	handlers[k.op] = func(in any) (any, error) {
		if bacField[*string](in, "NextToken") == nil {
			return own(in)
		}
		calls.record(k.op, in)
		b := k.elem("", k.id("amazon"), k.name("amazon")).(wstypes.WorkspaceBundle)
		b.Owner = sdkaws.String("AMAZON")
		return k.wrap(b, nil), nil
	}
	if _, _, err := wsTagScan(context.Background(), st, handlers); err != nil {
		t.Fatalf("scan: %v", err)
	}
	rows := rowsByNativeID(t, st, k.typ)
	amazon, ok := rows[k.arn("amazon")]
	if !ok {
		t.Fatalf("AMAZON bundle %s not stored", k.arn("amazon"))
	}
	if !amazon.ManagedByProvider || amazon.TagsJSON != nil {
		t.Errorf("AMAZON bundle managed %v, tags %v; want managed, untagged", amazon.ManagedByProvider, amazon.TagsJSON)
	}
	tagged, ok := rows[k.arn("tagged")]
	if !ok {
		t.Fatalf("own bundle %s not stored", k.arn("tagged"))
	}
	if tagged.ManagedByProvider {
		t.Error("the account's own bundle is flagged managed")
	}
	ids := calls.sentTagIDs()
	if slices.Contains(ids, k.id("amazon")) || !slices.Contains(ids, k.id("tagged")) {
		t.Errorf("DescribeTags ResourceIds = %v; want %s read and %s not", ids, k.id("tagged"), k.id("amazon"))
	}
}

// An image or connection alias whose OwnerAccountId names another account is
// shared with this one: a denied or not-found tag read keeps its row
// untagged and silent, and its tags are stored when the read succeeds. One
// without OwnerAccountId is taken as the account's own: a denied read keeps
// it untagged with one warning naming it under the tag op, and a not-found
// read drops it. Only an alias the account owns is read for permissions; an
// alias without OwnerAccountId adds its own warning under the permission op.
func TestScanWorkSpaces_SharedInRowsReadUnderOwner(t *testing.T) {
	for _, op := range []string{"DescribeWorkspaceImages", "DescribeConnectionAliases"} {
		for _, c := range []struct {
			name       string
			owner      *string
			err        error
			tags       map[string]string
			wantStored bool
			wantTags   map[string]string
			wantWarns  []string // the warnings' Service, sorted
		}{
			{"other/denied", sdkaws.String(wsTestOther), wsDenied, nil, true, nil, nil},
			{"other/not found", sdkaws.String(wsTestOther), wsGone, nil, true, nil, nil},
			{"other/tagged", sdkaws.String(wsTestOther), nil, map[string]string{"k": "v"}, true, map[string]string{"k": "v"}, nil},
			{"none/denied", nil, wsDenied, nil, true, nil, []string{wsTestTagsOp}},
			{"none/not found", nil, wsGone, nil, false, nil, nil},
		} {
			t.Run(op+"/"+c.name, func(t *testing.T) {
				st := newTestStore(t)
				warns := rhWarnRecorder(st)
				calls := newWSTagCalls()
				handlers := wsTagHandlers(calls)
				k := wsKindOf(t, op)
				own := handlers[k.op]
				// Only the first page's ("tagged") element has its owner changed.
				handlers[k.op] = func(in any) (any, error) {
					out, err := own(in)
					if bacField[*string](in, "NextToken") != nil {
						return out, err
					}
					switch o := out.(type) {
					case *workspaces.DescribeWorkspaceImagesOutput:
						o.Images[0].OwnerAccountId = c.owner
					case *workspaces.DescribeConnectionAliasesOutput:
						o.ConnectionAliases[0].OwnerAccountId = c.owner
					}
					return out, err
				}
				id, arn := k.id("tagged"), k.arn("tagged")
				calls.errs[id] = c.err
				calls.tags[id] = c.tags
				wantWarns := c.wantWarns
				if op == "DescribeConnectionAliases" && c.owner == nil && c.wantStored {
					wantWarns = append(slices.Clone(wantWarns), wsDescribeAliasPermissionsOp)
					slices.Sort(wantWarns)
				}

				total, _, err := wsTagScan(context.Background(), st, handlers)
				if err != nil {
					t.Fatalf("scan: %v", err)
				}
				want := wsTestStored
				if !c.wantStored {
					want--
				}
				// The alias is not the account's own: it has no permission row.
				if op == "DescribeConnectionAliases" {
					want--
				}
				if total != want {
					t.Errorf("total = %d; want %d", total, want)
				}
				r, ok := rowsByNativeID(t, st, k.typ)[arn]
				if ok != c.wantStored {
					t.Fatalf("%s stored = %v; want %v", arn, ok, c.wantStored)
				}
				if ok && c.wantTags == nil && r.TagsJSON != nil {
					t.Errorf("TagsJSON = %q; want nil", *r.TagsJSON)
				} else if c.wantTags != nil {
					assertTagsMap(t, r.TagsJSON, c.wantTags)
				}
				w := warns()
				var got []string
				for _, x := range w {
					got = append(got, x.Service)
					if x.Service == wsTestTagsOp && !strings.Contains(x.Message, arn) {
						t.Errorf("%s warning %+v does not name %s", wsTestTagsOp, x, arn)
					}
				}
				slices.Sort(got)
				if !slices.Equal(got, wantWarns) {
					t.Errorf("warnings = %v; want services %v", w, wantWarns)
				}
				if got := calls.sent("DescribeConnectionAliasPermissions", "AliasId"); op == "DescribeConnectionAliases" && slices.Contains(got, id) {
					t.Errorf("permissions read for the alias %s not owned here: %v", id, got)
				}
			})
		}
	}
}

// The scan's real entry point in an aws-us-gov or aws-cn account stores its
// rows under the partition's ARNs (a pool under the PoolArn it lists) and
// sends DescribeTags each row's id; a shared image there whose read is
// denied stays silent.
func TestScanWorkSpaces_PartitionRows(t *testing.T) {
	for _, pf := range partitionFixtures {
		t.Run(pf.partition, func(t *testing.T) {
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			calls := newWSTagCalls()
			handlers := wsTagHandlersIn(calls, pf.partition, pf.region)
			images := wsKindOf(t, "DescribeWorkspaceImages")
			own := handlers[images.op]
			handlers[images.op] = func(in any) (any, error) {
				out, err := own(in)
				out.(*workspaces.DescribeWorkspaceImagesOutput).Images[0].OwnerAccountId = sdkaws.String(wsTestOther)
				return out, err
			}
			calls.errs[images.id("tagged")] = wsDenied
			acct := partitionAccount(pf.partition)
			acct.cfg = opStubConfig(handlers, wsDenied)
			if _, _, err := scanWorkSpaces(context.Background(), acct, pf.region, st, testScanID); err != nil {
				t.Fatalf("scan: %v", err)
			}
			for _, k := range wsKinds() {
				rows := rowsByNativeID(t, st, k.typ)
				for _, suffix := range []string{"tagged", "bare"} {
					if _, ok := rows[k.arnIn(pf.partition, pf.region, suffix)]; !ok {
						t.Errorf("%s rows %v lack %s", k.typ, nativeIDs(rows), k.arnIn(pf.partition, pf.region, suffix))
					}
				}
			}
			if got := calls.sentTagIDs(); !slices.Equal(got, wsTagReadIDs()) {
				t.Errorf("DescribeTags ResourceIds = %v\nwant %v", got, wsTagReadIDs())
			}
			if w := warns(); len(w) != 0 {
				t.Errorf("warnings = %v; want none", w)
			}
		})
	}
}

// Pools not offered here, or closed to this account, answer the first page
// with a canned denial that reads as an empty listing. The same answer on a
// later page follows a page the op served: the first page is kept and the
// denial is warned about under the listing.
func TestScanWorkSpaces_PoolsGap(t *testing.T) {
	gap := apiErr("AccessDeniedException", "You do not have the permissions required to perform this action. Refer to "+
		"https://docs.aws.amazon.com/workspaces/latest/adminguide/workspaces-access-control.html")
	k := wsKindOf(t, "DescribeWorkspacesPools")
	for _, c := range []struct {
		call      int
		wantPools []string
		wantWarns int
	}{
		{1, nil, 0},
		{2, []string{k.arn("tagged")}, 1},
	} {
		t.Run(fmt.Sprintf("page%d", c.call), func(t *testing.T) {
			st := newTestStore(t)
			warns := countOpWarnings(st, "workspaces:"+k.op)
			handlers := wsTagHandlers(newWSTagCalls())
			handlers[k.op] = failOnCall(c.call, gap, handlers[k.op])
			if _, _, err := wsTagScan(context.Background(), st, handlers); err != nil {
				t.Fatalf("err = %v; want nil", err)
			}
			if got := nativeIDs(rowsByNativeID(t, st, k.typ)); !slices.Equal(got, c.wantPools) {
				t.Errorf("pools = %v; want %v", got, c.wantPools)
			}
			if *warns != c.wantWarns {
				t.Errorf("warnings = %d; want %d", *warns, c.wantWarns)
			}
		})
	}
}

// Once ctx ends no further phase starts, and the stop is one member of the
// error, although the phase in flight saw it in its listing.
func TestScanWorkSpaces_ContextEndReportedOnce(t *testing.T) {
	st := newTestStore(t)
	calls := newWSTagCalls()
	handlers := wsTagHandlers(calls)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	k := wsKindOf(t, "DescribeWorkspaceDirectories")
	list := handlers[k.op]
	handlers[k.op] = func(in any) (any, error) {
		if bacField[*string](in, "NextToken") != nil {
			cancel()
			return nil, context.Canceled
		}
		return list(in)
	}

	_, _, err := wsTagScan(ctx, st, handlers)
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
	if n := calls.count("DescribeWorkspaceBundles"); n != 0 {
		t.Errorf("DescribeWorkspaceBundles called %d times after the stop; want 0", n)
	}
}

// An NXDOMAIN after rows were stored is a failure of this scan, not a region
// gap: the counts are kept. With nothing stored it stays region-unavailable.
func TestScanWorkSpaces_NXDOMAIN(t *testing.T) {
	nx := &net.DNSError{Err: "no such host", Name: "workspaces.us-east-1.amazonaws.com", IsNotFound: true}
	t.Run("a listing after others stored rows", func(t *testing.T) {
		st := newTestStore(t)
		handlers := wsTagHandlers(newWSTagCalls())
		handlers["DescribeWorkspaceImages"] = func(any) (any, error) { return nil, nx }
		total, _, err := wsTagScan(context.Background(), st, handlers)
		assertGlueOutcome(t, err, outcomeError)
		if total != wsTestStored-2 {
			t.Errorf("total = %d; want the %d other rows counted", total, wsTestStored-2)
		}
	})
	t.Run("tag read after the listing", func(t *testing.T) {
		st := newTestStore(t)
		calls := newWSTagCalls()
		calls.errs[wsKindOf(t, "DescribeWorkspaces").id("bare")] = nx
		_, _, err := wsTagScan(context.Background(), st, wsTagHandlers(calls))
		assertGlueOutcome(t, err, outcomeError)
	})
	t.Run("nothing stored", func(t *testing.T) {
		st := newTestStore(t)
		warns := rhWarnRecorder(st)
		acct := newTestAccount(testAccountID)
		acct.cfg = opStubConfig(nil, nx)
		_, _, err := scanWorkSpaces(context.Background(), acct, testRegion, st, testScanID)
		assertGlueOutcome(t, err, outcomeUnavailable)
		if w := warns(); len(w) != 0 {
			t.Errorf("warnings = %v; want none", w)
		}
	})
}
