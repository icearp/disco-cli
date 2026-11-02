package aws

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"slices"
	"sort"
	"sync"
	"testing"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/codestarconnections"
	csctypes "github.com/aws/aws-sdk-go-v2/service/codestarconnections/types"
	"github.com/icearp/disco-cli/store"
)

const (
	cscTestLinkIDA = "aaaaaaaa-1111-2222-3333-444444444444"
	cscTestLinkIDB = "bbbbbbbb-1111-2222-3333-444444444444"
	cscTestLinkIDC = "cccccccc-1111-2222-3333-444444444444"
)

var (
	cscTestConnA = fmt.Sprintf("arn:aws:codestar-connections:%s:%s:connection/conn-aaaa", testRegion, testAccountID)
	cscTestConnB = fmt.Sprintf("arn:aws:codestar-connections:%s:%s:connection/conn-bbbb", testRegion, testAccountID)
	cscTestHostA = fmt.Sprintf("arn:aws:codestar-connections:%s:%s:host/host-aaaa", testRegion, testAccountID)
	cscTestHostB = fmt.Sprintf("arn:aws:codestar-connections:%s:%s:host/host-bbbb", testRegion, testAccountID)
	cscTestLinkA = fmt.Sprintf("arn:aws:codestar-connections:%s:%s:repository-link/%s", testRegion, testAccountID, cscTestLinkIDA)
	cscTestLinkB = fmt.Sprintf("arn:aws:codestar-connections:%s:%s:repository-link/%s", testRegion, testAccountID, cscTestLinkIDB)
	cscTestLinkC = fmt.Sprintf("arn:aws:codestar-connections:%s:%s:repository-link/%s", testRegion, testAccountID, cscTestLinkIDC)
	cscTestRole  = fmt.Sprintf("arn:aws:iam::%s:role/csc-sync", testAccountID)

	// The sync configurations' NativeIDs, built by hand from the scanner's
	// documented shape.
	cscTestSyncA  = fmt.Sprintf("arn:aws:codestar-connections:%s:%s:sync-configuration/%s/CFN_STACK_SYNC/stack-a", testRegion, testAccountID, cscTestLinkIDA)
	cscTestSyncB1 = fmt.Sprintf("arn:aws:codestar-connections:%s:%s:sync-configuration/%s/CFN_STACK_SYNC/stack-b1", testRegion, testAccountID, cscTestLinkIDB)
	cscTestSyncB2 = fmt.Sprintf("arn:aws:codestar-connections:%s:%s:sync-configuration/%s/CFN_STACK_SYNC/stack-b2", testRegion, testAccountID, cscTestLinkIDB)

	cscDenied   = apiErr("AccessDeniedException", "User: arn:aws:iam::123456789012:role/r is not authorized to perform: codestar-connections:List")
	cscThrottle = apiErr("ThrottlingException", "slow down")
	cscBadInput = apiErr("InvalidInputException", "bad input")
	cscGoneErr  = apiErr("ResourceNotFoundException", "gone")

	// cscTestTags are the tags ListTagsForResource returns; every other
	// resource has none.
	cscTestTags = map[string]map[string]string{
		cscTestConnA: {"env": "prod"},
		cscTestHostA: {"team": "ops"},
		cscTestLinkA: {"app": "web", "cost": "7"},
	}
)

// cscTestTotal is every row cscHandlers yields: 2 connections, 2 hosts, 3
// repository links and 3 sync configurations.
const cscTestTotal = 10

// cscCalls records the calls cscHandlers answers, and holds the tags and the
// errors its ListTagsForResource returns per ARN on top of cscTestTags, and
// the errors its ListSyncConfigurations returns per link id.
type cscCalls struct {
	// tagARNs is every ARN ListTagsForResource was sent, syncLinks every
	// link id ListSyncConfigurations was sent, lists every listing op called.
	tagARNs, syncLinks, lists parentCalls
	tags                      map[string]map[string]string
	tagErrs, syncErrs         map[string]error

	mu sync.Mutex
	// maxResults is the MaxResults of every listing call, per op;
	// syncTypes the SyncType of every ListSyncConfigurations call.
	maxResults map[string][]int32
	syncTypes  []csctypes.SyncConfigurationType
}

// listed records a listing call of op and reports whether it asks for the
// first page.
func (c *cscCalls) listed(op string, in any) bool {
	c.lists.add(op)
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.maxResults == nil {
		c.maxResults = map[string][]int32{}
	}
	c.maxResults[op] = append(c.maxResults[op], bacField[int32](in, "MaxResults"))
	return bacField[*string](in, "NextToken") == nil
}

// listTags answers ListTagsForResource from c.tagErrs, c.tags and
// cscTestTags, in that order.
func (c *cscCalls) listTags(in any) (any, error) {
	arn := sv(in.(*codestarconnections.ListTagsForResourceInput).ResourceArn)
	c.tagARNs.add(arn)
	if err, ok := c.tagErrs[arn]; ok {
		return nil, err
	}
	tags, ok := c.tags[arn]
	if !ok {
		tags = cscTestTags[arn]
	}
	out := &codestarconnections.ListTagsForResourceOutput{}
	for k, v := range tags {
		out.Tags = append(out.Tags, csctypes.Tag{Key: sdkaws.String(k), Value: sdkaws.String(v)})
	}
	return out, nil
}

// cscSync is a sync configuration of link named resource.
func cscSync(linkID, resource string) csctypes.SyncConfiguration {
	return csctypes.SyncConfiguration{
		RepositoryLinkId: sdkaws.String(linkID), ResourceName: sdkaws.String(resource),
		Branch: sdkaws.String("main"), RoleArn: &cscTestRole, SyncType: csctypes.SyncConfigurationTypeCfnStackSync,
		ConfigFile: sdkaws.String("deploy.yaml"), OwnerId: sdkaws.String("octo"), ProviderType: csctypes.ProviderTypeGithub,
		RepositoryName: sdkaws.String("repo"),
	}
}

// cscHandlers answers a region with connection A on page 1 and B on page 2,
// host A on page 1 and B (no name or status) on page 2, repository link A on
// page 1 and B and C on page 2. Link A has one sync configuration, link B one
// on each of two pages, link C none. Connection A, host A and link A are
// tagged (cscTestTags).
func cscHandlers(calls *cscCalls) map[string]func(in any) (any, error) {
	page2 := "page-2"
	return map[string]func(in any) (any, error){
		"ListConnections": func(in any) (any, error) {
			if calls.listed("ListConnections", in) {
				return &codestarconnections.ListConnectionsOutput{
					Connections: []csctypes.Connection{{
						ConnectionArn: &cscTestConnA, ConnectionName: sdkaws.String("conn-a"), ConnectionStatus: csctypes.ConnectionStatusAvailable,
						ProviderType: csctypes.ProviderTypeBitbucket, OwnerAccountId: sdkaws.String("210987654321"),
					}},
					NextToken: &page2,
				}, nil
			}
			return &codestarconnections.ListConnectionsOutput{Connections: []csctypes.Connection{{
				ConnectionArn: &cscTestConnB, ConnectionName: sdkaws.String("conn-b"), ConnectionStatus: csctypes.ConnectionStatusPending,
				ProviderType: csctypes.ProviderTypeGithubEnterpriseServer, HostArn: &cscTestHostA,
			}}}, nil
		},
		"ListHosts": func(in any) (any, error) {
			if calls.listed("ListHosts", in) {
				return &codestarconnections.ListHostsOutput{
					Hosts: []csctypes.Host{{
						HostArn: &cscTestHostA, Name: sdkaws.String("host-a"), Status: sdkaws.String("AVAILABLE"),
						ProviderEndpoint: sdkaws.String("https://ghe.example.com"), ProviderType: csctypes.ProviderTypeGithubEnterpriseServer,
					}},
					NextToken: &page2,
				}, nil
			}
			return &codestarconnections.ListHostsOutput{Hosts: []csctypes.Host{{HostArn: &cscTestHostB}}}, nil
		},
		"ListRepositoryLinks": func(in any) (any, error) {
			if calls.listed("ListRepositoryLinks", in) {
				return &codestarconnections.ListRepositoryLinksOutput{
					RepositoryLinks: []csctypes.RepositoryLinkInfo{{
						RepositoryLinkArn: &cscTestLinkA, RepositoryLinkId: sdkaws.String(cscTestLinkIDA), RepositoryName: sdkaws.String("repo-a"),
						ConnectionArn: &cscTestConnA, OwnerId: sdkaws.String("octo"), ProviderType: csctypes.ProviderTypeGithub,
					}},
					NextToken: &page2,
				}, nil
			}
			return &codestarconnections.ListRepositoryLinksOutput{RepositoryLinks: []csctypes.RepositoryLinkInfo{
				{RepositoryLinkArn: &cscTestLinkB, RepositoryLinkId: sdkaws.String(cscTestLinkIDB), RepositoryName: sdkaws.String("repo-b")},
				{RepositoryLinkArn: &cscTestLinkC, RepositoryLinkId: sdkaws.String(cscTestLinkIDC), RepositoryName: sdkaws.String("repo-c")},
			}}, nil
		},
		"ListSyncConfigurations": func(in any) (any, error) {
			first := calls.listed("ListSyncConfigurations", in)
			sin := in.(*codestarconnections.ListSyncConfigurationsInput)
			link := sv(sin.RepositoryLinkId)
			calls.syncLinks.add(link)
			calls.mu.Lock()
			calls.syncTypes = append(calls.syncTypes, sin.SyncType)
			calls.mu.Unlock()
			if err, ok := calls.syncErrs[link]; ok {
				return nil, err
			}
			out := &codestarconnections.ListSyncConfigurationsOutput{}
			switch {
			case link == cscTestLinkIDA:
				out.SyncConfigurations = []csctypes.SyncConfiguration{cscSync(link, "stack-a")}
			case link == cscTestLinkIDB && first:
				out.SyncConfigurations = []csctypes.SyncConfiguration{cscSync(link, "stack-b1")}
				out.NextToken = &page2
			case link == cscTestLinkIDB:
				out.SyncConfigurations = []csctypes.SyncConfiguration{cscSync(link, "stack-b2")}
			}
			return out, nil
		},
		"ListTagsForResource": calls.listTags,
	}
}

// cscScan runs the service scan entry point against handlers; an op without
// a handler is denied.
func cscScan(ctx context.Context, st *store.Store, handlers map[string]func(in any) (any, error)) (int, int, error) {
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(handlers, cscDenied)
	return scanCodeStarConnections(ctx, acct, testRegion, st, testScanID)
}

func cscSorted(ids ...string) []string {
	sort.Strings(ids)
	return ids
}

// cscTaggedIDs is the NativeID of every connection, host and repository link
// cscHandlers yields, sorted: the ARNs sent to ListTagsForResource.
func cscTaggedIDs() []string {
	return cscSorted(cscTestConnA, cscTestConnB, cscTestHostA, cscTestHostB, cscTestLinkA, cscTestLinkB, cscTestLinkC)
}

// cscAllSyncs is every sync configuration cscHandlers yields, sorted.
func cscAllSyncs() []string {
	return cscSorted(cscTestSyncA, cscTestSyncB1, cscTestSyncB2)
}

// cscStoredIDs is the NativeID of every stored row of typs, sorted.
func cscStoredIDs(t *testing.T, st *store.Store, typs ...string) []string {
	t.Helper()
	var ids []string
	for _, typ := range typs {
		for id := range rowsByNativeID(t, st, typ) {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	return ids
}

// cscTaggedStored is the NativeID of every stored connection, host and
// repository link, sorted.
func cscTaggedStored(t *testing.T, st *store.Store) []string {
	t.Helper()
	return cscStoredIDs(t, st, TypeCodeStarConnectionsConnection, TypeCodeStarConnectionsHost, TypeCodeStarConnectionsRepositoryLink)
}

func cscSyncStored(t *testing.T, st *store.Store) []string {
	t.Helper()
	return cscStoredIDs(t, st, TypeCodeStarConnectionsSyncConfiguration)
}

// Every connection, host and repository link is stored from both its pages
// under its listed ARN, its attributes the listed element, with the tags
// ListTagsForResource returns (nil for none); the tag op is sent exactly the
// ARN of each such row, once, and never a sync configuration's. A connection
// whose OwnerAccountId names another account is read like any other: that id
// is the provider's owner, not an AWS account. Each link's sync
// configurations are stored from both pages under the built NativeID, named
// by their ResourceName, with no tags. Every listing sends MaxResults 100 and
// ListSyncConfigurations is sent each link id with CFN_STACK_SYNC. No error,
// no warning.
func TestScanCodeStarConnections_StoresEveryRow(t *testing.T) {
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	var calls cscCalls
	total, inserted, err := cscScan(context.Background(), st, cscHandlers(&calls))
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != cscTestTotal || inserted != cscTestTotal {
		t.Errorf("total, inserted = %d, %d; want %d, %d", total, inserted, cscTestTotal, cscTestTotal)
	}
	if got := calls.tagARNs.distinct(); !slices.Equal(got, cscTaggedIDs()) {
		t.Errorf("ListTagsForResource ARNs = %v; want %v", got, cscTaggedIDs())
	}
	if n := len(calls.tagARNs.ids); n != len(cscTaggedIDs()) {
		t.Errorf("ListTagsForResource calls = %d; want one per row, %d", n, len(cscTaggedIDs()))
	}
	if got, want := calls.syncLinks.distinct(), cscSorted(cscTestLinkIDA, cscTestLinkIDB, cscTestLinkIDC); !slices.Equal(got, want) {
		t.Errorf("ListSyncConfigurations links = %v; want %v", got, want)
	}
	for _, typ := range calls.syncTypes {
		if typ != csctypes.SyncConfigurationTypeCfnStackSync {
			t.Errorf("ListSyncConfigurations SyncType = %q; want CFN_STACK_SYNC", typ)
		}
	}
	// Two pages for each listing, and for link B's sync configurations,
	// plus one page each for links A and C.
	wantCalls := map[string]int{"ListConnections": 2, "ListHosts": 2, "ListRepositoryLinks": 2, "ListSyncConfigurations": 4}
	for op, n := range wantCalls {
		got := calls.maxResults[op]
		if len(got) != n {
			t.Errorf("%s calls = %d; want %d", op, len(got), n)
		}
		for _, m := range got {
			if m != 100 {
				t.Errorf("%s MaxResults = %d; want 100", op, m)
			}
		}
	}
	if len(calls.syncTypes) != wantCalls["ListSyncConfigurations"] {
		t.Errorf("SyncTypes recorded = %d; want %d", len(calls.syncTypes), wantCalls["ListSyncConfigurations"])
	}
	if w := warns(); len(w) != 0 {
		t.Errorf("warnings = %v; want none", w)
	}

	for _, c := range []struct {
		typ, id      string
		name, status string // "-": the row has none
		tags         map[string]string
		attr         func(string) bool // whether the attributes are the listed element
	}{
		{TypeCodeStarConnectionsConnection, cscTestConnA, "conn-a", "AVAILABLE", cscTestTags[cscTestConnA], func(a string) bool {
			var c csctypes.Connection
			return json.Unmarshal([]byte(a), &c) == nil && c.ProviderType == csctypes.ProviderTypeBitbucket && sv(c.OwnerAccountId) == "210987654321"
		}},
		{TypeCodeStarConnectionsConnection, cscTestConnB, "conn-b", "PENDING", nil, func(a string) bool {
			var c csctypes.Connection
			return json.Unmarshal([]byte(a), &c) == nil && sv(c.HostArn) == cscTestHostA
		}},
		{TypeCodeStarConnectionsHost, cscTestHostA, "host-a", "AVAILABLE", cscTestTags[cscTestHostA], func(a string) bool {
			var h csctypes.Host
			return json.Unmarshal([]byte(a), &h) == nil && sv(h.ProviderEndpoint) == "https://ghe.example.com"
		}},
		{TypeCodeStarConnectionsHost, cscTestHostB, "-", "", nil, nil},
		{TypeCodeStarConnectionsRepositoryLink, cscTestLinkA, "repo-a", "-", cscTestTags[cscTestLinkA], func(a string) bool {
			var l csctypes.RepositoryLinkInfo
			return json.Unmarshal([]byte(a), &l) == nil && sv(l.ConnectionArn) == cscTestConnA && sv(l.OwnerId) == "octo"
		}},
		{TypeCodeStarConnectionsRepositoryLink, cscTestLinkB, "repo-b", "-", nil, nil},
		{TypeCodeStarConnectionsRepositoryLink, cscTestLinkC, "repo-c", "-", nil, nil},
		{TypeCodeStarConnectionsSyncConfiguration, cscTestSyncA, "stack-a", "-", nil, func(a string) bool {
			var s csctypes.SyncConfiguration
			return json.Unmarshal([]byte(a), &s) == nil && sv(s.RoleArn) == cscTestRole && sv(s.Branch) == "main"
		}},
		{TypeCodeStarConnectionsSyncConfiguration, cscTestSyncB1, "stack-b1", "-", nil, nil},
		{TypeCodeStarConnectionsSyncConfiguration, cscTestSyncB2, "stack-b2", "-", nil, nil},
	} {
		t.Run(c.id, func(t *testing.T) {
			r, ok := rowsByNativeID(t, st, c.typ)[c.id]
			if !ok {
				t.Fatalf("%s %s not stored", c.typ, c.id)
			}
			switch {
			case c.name == "-" && r.Name != nil:
				t.Errorf("Name = %q; want none", *r.Name)
			case c.name != "-" && (r.Name == nil || *r.Name != c.name):
				t.Errorf("Name = %v; want %q", r.Name, c.name)
			}
			switch {
			case c.status == "-" && r.Status != nil:
				t.Errorf("Status = %q; want none", *r.Status)
			case c.status != "-" && (r.Status == nil || *r.Status != c.status):
				t.Errorf("Status = %v; want %q", r.Status, c.status)
			}
			if r.CreatedAt != nil {
				t.Errorf("CreatedAt = %v; want none (no listed element carries one)", r.CreatedAt)
			}
			if sv(r.Region) != testRegion {
				t.Errorf("Region = %q; want %q", sv(r.Region), testRegion)
			}
			if r.Provider != "aws" || r.AccountID != testAccountID || sv(r.AccountName) != newTestAccount(testAccountID).Name {
				t.Errorf("Provider, AccountID, AccountName = %q, %q, %q; want aws, %s, %q", r.Provider, r.AccountID, sv(r.AccountName), testAccountID, newTestAccount(testAccountID).Name)
			}
			if c.attr != nil && !c.attr(r.AttributesJSON) {
				t.Errorf("attributes = %s; want the listed element", r.AttributesJSON)
			}
			if c.tags == nil {
				if r.TagsJSON != nil {
					t.Errorf("TagsJSON = %q; want nil", *r.TagsJSON)
				}
				return
			}
			assertTagsMap(t, r.TagsJSON, c.tags)
		})
	}
}

// A rescan stores the same rows and inserts none: the tags read are the same.
func TestScanCodeStarConnections_RescanInsertsNone(t *testing.T) {
	st := newTestStore(t)
	var calls cscCalls
	if _, _, err := cscScan(context.Background(), st, cscHandlers(&calls)); err != nil {
		t.Fatalf("first scan: %v", err)
	}
	total, inserted, err := cscScan(context.Background(), st, cscHandlers(&calls))
	if err != nil {
		t.Fatalf("rescan: %v", err)
	}
	if total != cscTestTotal || inserted != 0 {
		t.Errorf("rescan total, inserted = %d, %d; want %d, 0", total, inserted, cscTestTotal)
	}
}

// A region with no connection, host or repository link stores nothing, reads
// no tags and lists no sync configuration.
func TestScanCodeStarConnections_Empty(t *testing.T) {
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	var calls cscCalls
	h := cscHandlers(&calls)
	h["ListConnections"] = func(any) (any, error) { return &codestarconnections.ListConnectionsOutput{}, nil }
	h["ListHosts"] = func(any) (any, error) { return &codestarconnections.ListHostsOutput{}, nil }
	h["ListRepositoryLinks"] = func(any) (any, error) { return &codestarconnections.ListRepositoryLinksOutput{}, nil }
	total, inserted, err := cscScan(context.Background(), st, h)
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != 0 || inserted != 0 {
		t.Errorf("total, inserted = %d, %d; want 0, 0", total, inserted)
	}
	if got := calls.tagARNs.distinct(); len(got) != 0 {
		t.Errorf("ListTagsForResource ARNs = %v; want none", got)
	}
	if got := calls.syncLinks.distinct(); len(got) != 0 {
		t.Errorf("ListSyncConfigurations links = %v; want none", got)
	}
	if w := warns(); len(w) != 0 {
		t.Errorf("warnings = %v; want none", w)
	}
}

// A failed tag read. ResourceNotFoundException is modeled, so a resource
// deleted since it was listed is dropped silently, and a dropped repository
// link has no sync configuration read. A denial keeps the row untagged and
// warns once under the tag op with the scan's scope; a throttle keeps it and
// is transient; any other error keeps it and is an error.
func TestScanCodeStarConnections_TagReadFailures(t *testing.T) {
	for _, c := range []struct {
		name      string
		arn, typ  string
		err       error
		outcome   serviceOutcome // -1: no error
		code      string
		wantTotal int
		wantKept  bool
		warn      bool
		wantSyncs []string
	}{
		{"connection/not found", cscTestConnA, TypeCodeStarConnectionsConnection, cscGoneErr, -1, "", cscTestTotal - 1, false, false, cscAllSyncs()},
		{"host/not found", cscTestHostB, TypeCodeStarConnectionsHost, cscGoneErr, -1, "", cscTestTotal - 1, false, false, cscAllSyncs()},
		{"repository link/not found", cscTestLinkB, TypeCodeStarConnectionsRepositoryLink, cscGoneErr, -1, "", cscTestTotal - 3, false, false, cscSorted(cscTestSyncA)},
		{"host/denied", cscTestHostA, TypeCodeStarConnectionsHost, cscDenied, -1, "", cscTestTotal, true, true, cscAllSyncs()},
		{"repository link/transient", cscTestLinkA, TypeCodeStarConnectionsRepositoryLink, cscThrottle, outcomeTransient, "ThrottlingException", cscTestTotal, true, false, cscAllSyncs()},
		{"connection/other error", cscTestConnB, TypeCodeStarConnectionsConnection, cscBadInput, outcomeError, "InvalidInputException", cscTestTotal, true, false, cscAllSyncs()},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			calls := cscCalls{tagErrs: map[string]error{c.arn: c.err}}
			total, _, err := cscScan(context.Background(), st, cscHandlers(&calls))
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
			r, ok := rowsByNativeID(t, st, c.typ)[c.arn]
			if ok != c.wantKept {
				t.Fatalf("row stored = %v; want %v", ok, c.wantKept)
			}
			if ok && r.TagsJSON != nil {
				t.Errorf("TagsJSON = %q; want nil", *r.TagsJSON)
			}
			if got := cscSyncStored(t, st); !slices.Equal(got, c.wantSyncs) {
				t.Errorf("sync configurations = %v; want %v", got, c.wantSyncs)
			}
			w := warns()
			var want []string
			if c.warn {
				want = []string{cscListTagsForResourceOp}
			}
			if got := amWarnings(w); !slices.Equal(got, want) {
				t.Fatalf("warnings = %v; want %v", w, want)
			}
			for _, x := range w {
				if x.Scope != testAccountID+"/"+testRegion {
					t.Errorf("warning Scope %q; want %s/%s", x.Scope, testAccountID, testRegion)
				}
			}
		})
	}
}

// Denied tag reads in every phase send one warning for the whole scan; the
// rows are kept.
func TestScanCodeStarConnections_TagDenialsWarnOncePerScan(t *testing.T) {
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	calls := cscCalls{tagErrs: map[string]error{cscTestConnB: cscDenied, cscTestHostA: cscDenied, cscTestLinkC: cscDenied}}
	total, _, err := cscScan(context.Background(), st, cscHandlers(&calls))
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != cscTestTotal {
		t.Errorf("total = %d; want %d", total, cscTestTotal)
	}
	if got := amWarnings(warns()); !slices.Equal(got, []string{cscListTagsForResourceOp}) {
		t.Errorf("warnings = %v; want one under %s", got, cscListTagsForResourceOp)
	}
}

// A throttled tag read beside a hard failure of the same op is demoted to
// one warning, so the hard error decides the outcome.
func TestScanCodeStarConnections_TagThrottleDemotedBesideHardError(t *testing.T) {
	st := newTestStore(t)
	warns := countOpWarnings(st, cscListTagsForResourceOp)
	calls := cscCalls{tagErrs: map[string]error{cscTestConnA: cscThrottle, cscTestLinkC: cscBadInput}}
	_, _, err := cscScan(context.Background(), st, cscHandlers(&calls))
	assertGlueOutcome(t, err, outcomeError)
	assertAPICode(t, err, "InvalidInputException")
	if *warns != 1 {
		t.Errorf("%s warnings = %d; want 1", cscListTagsForResourceOp, *warns)
	}
}

// A throttled sync configuration listing beside a hard tag error is demoted
// to one warning keyed by its own op, so the hard error decides the outcome.
func TestScanCodeStarConnections_SyncThrottleDemotedBesideHardError(t *testing.T) {
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	calls := cscCalls{
		tagErrs:  map[string]error{cscTestConnB: cscBadInput},
		syncErrs: map[string]error{cscTestLinkIDA: cscThrottle},
	}
	_, _, err := cscScan(context.Background(), st, cscHandlers(&calls))
	assertGlueOutcome(t, err, outcomeError)
	assertAPICode(t, err, "InvalidInputException")
	if got, want := amWarnings(warns()), []string{cscListSyncConfigurationsOp}; !slices.Equal(got, want) {
		t.Errorf("warnings = %v; want %v", got, want)
	}
}

// A listing whose second page fails keeps its first page, and the later
// phases still run; a repository link on the lost page has no sync
// configuration read.
func TestScanCodeStarConnections_ListFailureKeepsPagesAndLaterPhases(t *testing.T) {
	for _, tc := range []struct {
		op        string
		kept      []string // every connection, host and link stored
		wantSyncs []string
	}{
		{"ListConnections", cscSorted(cscTestConnA, cscTestHostA, cscTestHostB, cscTestLinkA, cscTestLinkB, cscTestLinkC), cscAllSyncs()},
		{"ListHosts", cscSorted(cscTestConnA, cscTestConnB, cscTestHostA, cscTestLinkA, cscTestLinkB, cscTestLinkC), cscAllSyncs()},
		{"ListRepositoryLinks", cscSorted(cscTestConnA, cscTestConnB, cscTestHostA, cscTestHostB, cscTestLinkA), cscSorted(cscTestSyncA)},
	} {
		t.Run(tc.op, func(t *testing.T) {
			st := newTestStore(t)
			var calls cscCalls
			h := cscHandlers(&calls)
			list := h[tc.op]
			h[tc.op] = func(in any) (any, error) {
				if bacField[*string](in, "NextToken") != nil {
					return nil, cscBadInput
				}
				return list(in)
			}
			total, _, err := cscScan(context.Background(), st, h)
			assertGlueOutcome(t, err, outcomeError)
			assertAPICode(t, err, "InvalidInputException")
			if want := len(tc.kept) + len(tc.wantSyncs); total != want {
				t.Errorf("total = %d, want %d", total, want)
			}
			if got := cscTaggedStored(t, st); !slices.Equal(got, tc.kept) {
				t.Errorf("stored = %v; want %v", got, tc.kept)
			}
			if got := cscSyncStored(t, st); !slices.Equal(got, tc.wantSyncs) {
				t.Errorf("sync configurations = %v; want %v", got, tc.wantSyncs)
			}
		})
	}
}

// A denied listing warns once under its op and is not an error; its first
// page, when it was read, is kept, and the later phases still run.
func TestScanCodeStarConnections_ListDeniedWarnsAndContinues(t *testing.T) {
	for _, tc := range []struct {
		op        string
		page      int // the page denied
		kept      []string
		wantSyncs []string
	}{
		{"ListConnections", 1, cscSorted(cscTestHostA, cscTestHostB, cscTestLinkA, cscTestLinkB, cscTestLinkC), cscAllSyncs()},
		{"ListHosts", 2, cscSorted(cscTestConnA, cscTestConnB, cscTestHostA, cscTestLinkA, cscTestLinkB, cscTestLinkC), cscAllSyncs()},
		{"ListRepositoryLinks", 2, cscSorted(cscTestConnA, cscTestConnB, cscTestHostA, cscTestHostB, cscTestLinkA), cscSorted(cscTestSyncA)},
	} {
		t.Run(tc.op, func(t *testing.T) {
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			var calls cscCalls
			h := cscHandlers(&calls)
			list := h[tc.op]
			h[tc.op] = func(in any) (any, error) {
				if (bacField[*string](in, "NextToken") == nil) == (tc.page == 1) {
					return nil, cscDenied
				}
				return list(in)
			}
			if _, _, err := cscScan(context.Background(), st, h); err != nil {
				t.Fatalf("scan: %v", err)
			}
			if got := cscTaggedStored(t, st); !slices.Equal(got, tc.kept) {
				t.Errorf("stored = %v; want %v", got, tc.kept)
			}
			if got := cscSyncStored(t, st); !slices.Equal(got, tc.wantSyncs) {
				t.Errorf("sync configurations = %v; want %v", got, tc.wantSyncs)
			}
			if got, want := amWarnings(warns()), []string{"codestar-connections:" + tc.op}; !slices.Equal(got, want) {
				t.Errorf("warnings = %v; want %v", got, want)
			}
		})
	}
}

// A sync configuration listing fails per link: a link deleted since it was
// listed (ResourceNotFoundException, modeled) is skipped silently; a denied
// link warns once for the op; InvalidInputException (modeled) and an
// unmodeled ValidationException are errors. Every other link's rows are
// stored, the earlier pages of a link that fails mid-listing included.
func TestScanCodeStarConnections_SyncListingErrors(t *testing.T) {
	validation := apiErr("ValidationException", "bad")
	for _, c := range []struct {
		name      string
		errs      map[string]error
		outcome   serviceOutcome // -1: no error
		code      string
		warn      bool
		wantSyncs []string
	}{
		{"not found", map[string]error{cscTestLinkIDA: cscGoneErr}, -1, "", false, cscSorted(cscTestSyncB1, cscTestSyncB2)},
		{"denied", map[string]error{cscTestLinkIDA: cscDenied, cscTestLinkIDC: cscDenied}, -1, "", true, cscSorted(cscTestSyncB1, cscTestSyncB2)},
		{"transient", map[string]error{cscTestLinkIDA: cscThrottle}, outcomeTransient, "ThrottlingException", false, cscSorted(cscTestSyncB1, cscTestSyncB2)},
		{"invalid input", map[string]error{cscTestLinkIDA: cscBadInput}, outcomeError, "InvalidInputException", false, cscSorted(cscTestSyncB1, cscTestSyncB2)},
		{"unmodeled validation", map[string]error{cscTestLinkIDC: validation}, outcomeError, "ValidationException", false, cscAllSyncs()},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			calls := cscCalls{syncErrs: c.errs}
			total, _, err := cscScan(context.Background(), st, cscHandlers(&calls))
			if c.outcome == -1 {
				if err != nil {
					t.Fatalf("err = %v; want nil", err)
				}
			} else {
				assertGlueOutcome(t, err, c.outcome)
				assertAPICode(t, err, c.code)
			}
			if want := 7 + len(c.wantSyncs); total != want {
				t.Errorf("total = %d; want %d", total, want)
			}
			if got := cscSyncStored(t, st); !slices.Equal(got, c.wantSyncs) {
				t.Errorf("sync configurations = %v; want %v", got, c.wantSyncs)
			}
			var want []string
			if c.warn {
				want = []string{cscListSyncConfigurationsOp}
			}
			if got := amWarnings(warns()); !slices.Equal(got, want) {
				t.Errorf("warnings = %v; want %v", got, want)
			}
		})
	}
	t.Run("second page fails", func(t *testing.T) {
		st := newTestStore(t)
		var calls cscCalls
		h := cscHandlers(&calls)
		list := h["ListSyncConfigurations"]
		h["ListSyncConfigurations"] = func(in any) (any, error) {
			if bacField[*string](in, "NextToken") != nil {
				return nil, cscBadInput
			}
			return list(in)
		}
		_, _, err := cscScan(context.Background(), st, h)
		assertAPICode(t, err, "InvalidInputException")
		if got, want := cscSyncStored(t, st), cscSorted(cscTestSyncA, cscTestSyncB1); !slices.Equal(got, want) {
			t.Errorf("sync configurations = %v; want %v", got, want)
		}
	})
}

// A failed write stops the scan in each phase: no warning about the rows it
// lost (a denied tag read, or for sync configurations a denied link, among
// them), no later phase runs, and the earlier phases' rows stay counted.
func TestScanCodeStarConnections_FailedWriteStopsScan(t *testing.T) {
	for _, c := range []struct {
		typ        string
		tagErrs    map[string]error
		syncErrs   map[string]error
		laterLists []string // listings that must not be called
		wantTotal  int
	}{
		{TypeCodeStarConnectionsConnection, map[string]error{cscTestConnA: cscDenied}, nil, []string{"ListHosts", "ListRepositoryLinks", "ListSyncConfigurations"}, 0},
		{TypeCodeStarConnectionsHost, map[string]error{cscTestHostA: cscDenied}, nil, []string{"ListRepositoryLinks", "ListSyncConfigurations"}, 2},
		{TypeCodeStarConnectionsRepositoryLink, map[string]error{cscTestLinkA: cscDenied}, nil, []string{"ListSyncConfigurations"}, 2 + 2},
		{TypeCodeStarConnectionsSyncConfiguration, nil, map[string]error{cscTestLinkIDC: cscDenied}, nil, 2 + 2 + 3},
	} {
		t.Run(c.typ, func(t *testing.T) {
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			refuseStoreWrites(t, st, c.typ)
			calls := cscCalls{tagErrs: c.tagErrs, syncErrs: c.syncErrs}
			total, _, err := cscScan(context.Background(), st, cscHandlers(&calls))
			if !errors.Is(err, store.ErrStoreWrite) {
				t.Fatalf("err = %v; want store.ErrStoreWrite", err)
			}
			if w := warns(); len(w) != 0 {
				t.Errorf("warnings = %v; want none", w)
			}
			for _, op := range c.laterLists {
				if slices.Contains(calls.lists.distinct(), op) {
					t.Errorf("%s called after the failed write", op)
				}
			}
			if total != c.wantTotal {
				t.Errorf("total = %d; want %d", total, c.wantTotal)
			}
		})
	}
}

// Each listing writes a page before it reads the next: the first page's row
// is stored when the second page is requested.
func TestScanCodeStarConnections_WritesEachPageBeforeTheNext(t *testing.T) {
	st := newTestStore(t)
	var calls cscCalls
	h := cscHandlers(&calls)
	pageOne := map[string]string{"ListConnections": cscTestConnA, "ListHosts": cscTestHostA, "ListRepositoryLinks": cscTestLinkA}
	lookups := map[string]error{} // per listing: GetResource of its page-1 row, when page 2 was requested
	for op, arn := range pageOne {
		list := h[op]
		h[op] = func(in any) (any, error) {
			if bacField[*string](in, "NextToken") != nil {
				_, lookups[op] = st.GetResource(store.ResourceID("aws", testAccountID, arn))
			}
			return list(in)
		}
	}
	if _, _, err := cscScan(context.Background(), st, h); err != nil {
		t.Fatalf("scan: %v", err)
	}
	for op, arn := range pageOne {
		err, asked := lookups[op]
		if !asked {
			t.Errorf("%s: page 2 never requested", op)
			continue
		}
		if err != nil {
			t.Errorf("%s: %s not stored before page 2 was read: %v", op, arn, err)
		}
	}
}

// A repeated NextToken ends each listing as an error, keeping its pages; the
// later phases still run.
func TestScanCodeStarConnections_RepeatedTokenKeepsPages(t *testing.T) {
	for _, op := range []string{"ListConnections", "ListHosts", "ListRepositoryLinks", "ListSyncConfigurations"} {
		t.Run(op, func(t *testing.T) {
			st := newTestStore(t)
			var calls cscCalls
			h := cscHandlers(&calls)
			twoPages := h[op]
			h[op] = func(in any) (any, error) {
				out, err := twoPages(in)
				// Of the sync listings only link B's has two pages; the
				// others would repeat their one page.
				if sin, ok := in.(*codestarconnections.ListSyncConfigurationsInput); ok && sv(sin.RepositoryLinkId) != cscTestLinkIDB {
					return out, err
				}
				switch o := out.(type) {
				case *codestarconnections.ListConnectionsOutput:
					o.NextToken = sdkaws.String("same")
				case *codestarconnections.ListHostsOutput:
					o.NextToken = sdkaws.String("same")
				case *codestarconnections.ListRepositoryLinksOutput:
					o.NextToken = sdkaws.String("same")
				case *codestarconnections.ListSyncConfigurationsOutput:
					o.NextToken = sdkaws.String("same")
				}
				return out, err
			}
			total, _, err := cscScan(context.Background(), st, h)
			if !errors.Is(err, errRepeatedNextToken) {
				t.Fatalf("err = %v; want errRepeatedNextToken", err)
			}
			if total != cscTestTotal {
				t.Errorf("total = %d; want %d", total, cscTestTotal)
			}
			if got := cscTaggedStored(t, st); !slices.Equal(got, cscTaggedIDs()) {
				t.Errorf("stored = %v; want %v", got, cscTaggedIDs())
			}
			if got := cscSyncStored(t, st); !slices.Equal(got, cscAllSyncs()) {
				t.Errorf("sync configurations = %v; want %v", got, cscAllSyncs())
			}
		})
	}
}

// A listed element without the field its NativeID needs is not stored, and
// each listing warns once under its op; its siblings are stored and have
// their tags read. A repository link without a RepositoryLinkId is stored,
// warned about once, and has no sync configuration read.
func TestScanCodeStarConnections_ElementWithoutIdentifier(t *testing.T) {
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	var calls cscCalls
	h := cscHandlers(&calls)
	h["ListConnections"] = func(any) (any, error) {
		return &codestarconnections.ListConnectionsOutput{Connections: []csctypes.Connection{{ConnectionName: sdkaws.String("no-arn")}, {ConnectionArn: &cscTestConnA}}}, nil
	}
	h["ListHosts"] = func(any) (any, error) {
		return &codestarconnections.ListHostsOutput{Hosts: []csctypes.Host{{}, {HostArn: &cscTestHostA}}}, nil
	}
	h["ListRepositoryLinks"] = func(any) (any, error) {
		return &codestarconnections.ListRepositoryLinksOutput{RepositoryLinks: []csctypes.RepositoryLinkInfo{
			{RepositoryName: sdkaws.String("no-arn"), RepositoryLinkId: sdkaws.String(cscTestLinkIDC)},
			{RepositoryLinkArn: &cscTestLinkB},
			{RepositoryLinkArn: &cscTestLinkA, RepositoryLinkId: sdkaws.String(cscTestLinkIDA)},
		}}, nil
	}
	list := h["ListSyncConfigurations"]
	h["ListSyncConfigurations"] = func(in any) (any, error) {
		out, err := list(in)
		if o, ok := out.(*codestarconnections.ListSyncConfigurationsOutput); ok {
			o.SyncConfigurations = append(o.SyncConfigurations, csctypes.SyncConfiguration{Branch: sdkaws.String("orphan")})
		}
		return out, err
	}
	if _, _, err := cscScan(context.Background(), st, h); err != nil {
		t.Fatalf("scan: %v", err)
	}
	want := cscSorted(cscTestConnA, cscTestHostA, cscTestLinkA, cscTestLinkB)
	if got := cscTaggedStored(t, st); !slices.Equal(got, want) {
		t.Errorf("stored = %v; want %v", got, want)
	}
	if got := calls.tagARNs.distinct(); !slices.Equal(got, want) {
		t.Errorf("ListTagsForResource ARNs = %v; want %v", got, want)
	}
	if got := calls.syncLinks.distinct(); !slices.Equal(got, []string{cscTestLinkIDA}) {
		t.Errorf("ListSyncConfigurations links = %v; want only %s", got, cscTestLinkIDA)
	}
	if got := cscSyncStored(t, st); !slices.Equal(got, []string{cscTestSyncA}) {
		t.Errorf("sync configurations = %v; want %s", got, cscTestSyncA)
	}
	w := warns()
	// The links listing warns twice: once for the element without an ARN,
	// once for the stored link without an id.
	wantOps := []string{cscListConnectionsOp, cscListHostsOp, cscListRepositoryLinksOp, cscListRepositoryLinksOp, cscListSyncConfigurationsOp}
	if got := amWarnings(w); !slices.Equal(got, wantOps) {
		t.Fatalf("warnings = %v; want %v", w, wantOps)
	}
}

// A GovCloud or China ARN reaches the tag op unchanged, and a sync
// configuration's NativeID is built in the account's partition.
func TestScanCodeStarConnections_PartitionARNs(t *testing.T) {
	for _, pf := range partitionFixtures {
		t.Run(pf.partition, func(t *testing.T) {
			arns := map[string]string{
				"ListConnections":     partitionARN(pf.partition, "codestar-connections", pf.region, "connection/conn-gov"),
				"ListHosts":           partitionARN(pf.partition, "codestar-connections", pf.region, "host/host-gov"),
				"ListRepositoryLinks": partitionARN(pf.partition, "codestar-connections", pf.region, "repository-link/"+cscTestLinkIDA),
			}
			calls := cscCalls{tags: map[string]map[string]string{}}
			for _, arn := range arns {
				calls.tags[arn] = map[string]string{"env": pf.partition}
			}
			h := cscHandlers(&calls)
			h["ListConnections"] = func(any) (any, error) {
				return &codestarconnections.ListConnectionsOutput{Connections: []csctypes.Connection{{ConnectionArn: sdkaws.String(arns["ListConnections"])}}}, nil
			}
			h["ListHosts"] = func(any) (any, error) {
				return &codestarconnections.ListHostsOutput{Hosts: []csctypes.Host{{HostArn: sdkaws.String(arns["ListHosts"])}}}, nil
			}
			h["ListRepositoryLinks"] = func(any) (any, error) {
				return &codestarconnections.ListRepositoryLinksOutput{RepositoryLinks: []csctypes.RepositoryLinkInfo{
					{RepositoryLinkArn: sdkaws.String(arns["ListRepositoryLinks"]), RepositoryLinkId: sdkaws.String(cscTestLinkIDA)},
				}}, nil
			}
			st := newTestStore(t)
			acct := partitionAccount(pf.partition)
			acct.cfg = opStubConfig(h, cscDenied)
			if _, _, err := scanCodeStarConnections(context.Background(), acct, pf.region, st, testScanID); err != nil {
				t.Fatalf("scan: %v", err)
			}
			want := cscSorted(arns["ListConnections"], arns["ListHosts"], arns["ListRepositoryLinks"])
			if got := calls.tagARNs.distinct(); !slices.Equal(got, want) {
				t.Fatalf("ListTagsForResource ARNs = %v; want %v", got, want)
			}
			for typ, arn := range map[string]string{
				TypeCodeStarConnectionsConnection:     arns["ListConnections"],
				TypeCodeStarConnectionsHost:           arns["ListHosts"],
				TypeCodeStarConnectionsRepositoryLink: arns["ListRepositoryLinks"],
			} {
				assertTagsMap(t, rowsByNativeID(t, st, typ)[arn].TagsJSON, map[string]string{"env": pf.partition})
			}
			wantSync := partitionARN(pf.partition, "codestar-connections", pf.region, "sync-configuration/"+cscTestLinkIDA+"/CFN_STACK_SYNC/stack-a")
			if got := cscSyncStored(t, st); !slices.Equal(got, []string{wantSync}) {
				t.Errorf("sync configurations = %v; want %s", got, wantSync)
			}
		})
	}
}

// Once ctx ends, the stop is one member of the error, although the phase in
// flight saw it in its listing, and the later phases are not run.
func TestScanCodeStarConnections_ContextEndReportedOnce(t *testing.T) {
	st := newTestStore(t)
	var calls cscCalls
	h := cscHandlers(&calls)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	list := h["ListHosts"]
	h["ListHosts"] = func(in any) (any, error) {
		if bacField[*string](in, "NextToken") != nil {
			cancel()
			return nil, context.Canceled
		}
		return list(in)
	}
	_, _, err := cscScan(ctx, st, h)
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
	for _, op := range []string{"ListRepositoryLinks", "ListSyncConfigurations"} {
		if slices.Contains(calls.lists.distinct(), op) {
			t.Errorf("%s called after ctx ended", op)
		}
	}
}

// An NXDOMAIN after rows were stored is a failure of this scan, not a region
// gap: the counts are kept. With nothing stored it stays region-unavailable.
func TestScanCodeStarConnections_NXDOMAIN(t *testing.T) {
	nx := &net.DNSError{Err: "no such host", Name: "codestar-connections.us-east-1.amazonaws.com", IsNotFound: true}
	t.Run("one listing after others stored rows", func(t *testing.T) {
		st := newTestStore(t)
		var calls cscCalls
		h := cscHandlers(&calls)
		h["ListHosts"] = func(any) (any, error) { return nil, nx }
		total, _, err := cscScan(context.Background(), st, h)
		assertGlueOutcome(t, err, outcomeError)
		if want := cscTestTotal - 2; total != want {
			t.Errorf("total = %d; want the %d other rows counted", total, want)
		}
	})
	t.Run("tag read after the listing", func(t *testing.T) {
		st := newTestStore(t)
		calls := cscCalls{tagErrs: map[string]error{cscTestLinkC: nx}}
		_, _, err := cscScan(context.Background(), st, cscHandlers(&calls))
		assertGlueOutcome(t, err, outcomeError)
	})
	t.Run("nothing stored", func(t *testing.T) {
		st := newTestStore(t)
		warns := rhWarnRecorder(st)
		acct := newTestAccount(testAccountID)
		acct.cfg = opStubConfig(nil, nx)
		_, _, err := scanCodeStarConnections(context.Background(), acct, testRegion, st, testScanID)
		assertGlueOutcome(t, err, outcomeUnavailable)
		if w := warns(); len(w) != 0 {
			t.Errorf("warnings = %v; want none", w)
		}
	})
}
