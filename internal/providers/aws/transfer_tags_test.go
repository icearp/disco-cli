package aws

import (
	"context"
	"errors"
	"net"
	"slices"
	"strings"
	"sync"
	"testing"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/transfer"
	transfertypes "github.com/aws/aws-sdk-go-v2/service/transfer/types"
	"github.com/icearp/disco-cli/store"
)

const (
	transferTestARNPrefix = "arn:aws:transfer:" + testRegion + ":" + testAccountID + ":"
	// transferTestTagged is the rows whose tags are read: two servers, two
	// rows (one per page) of each per-server listing for each server, and two
	// of each other listing.
	transferTestTagged = 2 + 3*2*2 + 5*2
	// transferTestTotal adds the directory server's one access.
	transferTestTotal = transferTestTagged + 1
)

var (
	transferTestThrottle  = apiErr("ThrottlingException", "Rate exceeded")
	transferTestInvalid   = apiErr("InvalidRequestException", "bad")
	transferTestServerIDs = []string{"server-tagged", "server-bare"}
)

// transferKind is one listing whose rows have their tags read: the List op,
// the type it stores, the ARN resource type of its rows, whether it is listed
// per server, and page, which builds the op's output holding one element with
// arn and id, and next.
type transferKind struct {
	op, typ, arnType string
	perServer        bool
	page             func(arn, id, next *string) any
}

func (k transferKind) id(suffix string) string { return k.arnType + "-" + suffix }

// arn is the ARN of the row on page suffix; server is ignored for a listing
// that is not per server.
func (k transferKind) arn(server, suffix string) string {
	if k.perServer {
		return transferTestARNPrefix + k.arnType + "/" + server + "/" + k.id(suffix)
	}
	return transferTestARNPrefix + k.arnType + "/" + k.id(suffix)
}

func transferKinds() []transferKind {
	return []transferKind{
		{"ListServers", TypeTransferServer, "server", false, func(arn, id, next *string) any {
			// The first page's server is a directory server, so it has accesses.
			idp := transfertypes.IdentityProviderTypeServiceManaged
			if next != nil {
				idp = transfertypes.IdentityProviderTypeAwsDirectoryService
			}
			return &transfer.ListServersOutput{NextToken: next, Servers: []transfertypes.ListedServer{{Arn: arn, ServerId: id, IdentityProviderType: idp}}}
		}},
		{"ListAgreements", TypeTransferAgreement, "agreement", true, func(arn, id, next *string) any {
			return &transfer.ListAgreementsOutput{NextToken: next, Agreements: []transfertypes.ListedAgreement{{Arn: arn, AgreementId: id}}}
		}},
		{"ListUsers", TypeTransferUser, "user", true, func(arn, id, next *string) any {
			return &transfer.ListUsersOutput{NextToken: next, Users: []transfertypes.ListedUser{{Arn: arn, UserName: id}}}
		}},
		{"ListHostKeys", TypeTransferHostKey, "host-key", true, func(arn, id, next *string) any {
			return &transfer.ListHostKeysOutput{NextToken: next, HostKeys: []transfertypes.ListedHostKey{{Arn: arn, HostKeyId: id}}}
		}},
		{"ListCertificates", TypeTransferCertificate, "certificate", false, func(arn, id, next *string) any {
			return &transfer.ListCertificatesOutput{NextToken: next, Certificates: []transfertypes.ListedCertificate{{Arn: arn, CertificateId: id}}}
		}},
		{"ListConnectors", TypeTransferConnector, "connector", false, func(arn, id, next *string) any {
			return &transfer.ListConnectorsOutput{NextToken: next, Connectors: []transfertypes.ListedConnector{{Arn: arn, ConnectorId: id}}}
		}},
		{"ListProfiles", TypeTransferProfile, "profile", false, func(arn, id, next *string) any {
			return &transfer.ListProfilesOutput{NextToken: next, Profiles: []transfertypes.ListedProfile{{Arn: arn, ProfileId: id}}}
		}},
		{"ListWebApps", TypeTransferWebApp, "webapp", false, func(arn, id, next *string) any {
			return &transfer.ListWebAppsOutput{NextToken: next, WebApps: []transfertypes.ListedWebApp{{Arn: arn, WebAppId: id}}}
		}},
		{"ListWorkflows", TypeTransferWorkflow, "workflow", false, func(arn, id, next *string) any {
			return &transfer.ListWorkflowsOutput{NextToken: next, Workflows: []transfertypes.ListedWorkflow{{Arn: arn, WorkflowId: id}}}
		}},
	}
}

func transferKindOf(t *testing.T, op string) transferKind {
	t.Helper()
	for _, k := range transferKinds() {
		if k.op == op {
			return k
		}
	}
	t.Fatalf("%s is no tagged Transfer Family listing", op)
	return transferKind{}
}

// transferKindARNs is the ARN of every row k's listing returns in the
// fixture.
func transferKindARNs(k transferKind) []string {
	servers := []string{""}
	if k.perServer {
		servers = transferTestServerIDs
	}
	var arns []string
	for _, s := range servers {
		arns = append(arns, k.arn(s, "tagged"), k.arn(s, "bare"))
	}
	return arns
}

// transferCalls records the calls of a scan and answers the tag reads, by
// ARN, with tags or errs.
type transferCalls struct {
	mu         sync.Mutex
	maxResults map[string][]int32
	tagARNs    []string
	webAppIDs  []string
	servers    map[string][]string
	tags       map[string]map[string]string
	errs       map[string]error
}

// newTransferCalls tags every "tagged" row with its type.
func newTransferCalls() *transferCalls {
	c := &transferCalls{maxResults: map[string][]int32{}, servers: map[string][]string{}, tags: map[string]map[string]string{}, errs: map[string]error{}}
	for _, k := range transferKinds() {
		for _, arn := range transferKindARNs(k) {
			if strings.HasSuffix(arn, "-tagged") {
				c.tags[arn] = map[string]string{"type": k.typ}
			}
		}
	}
	return c
}

func (c *transferCalls) record(op string, in any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if n := bacField[*int32](in, "MaxResults"); n != nil {
		c.maxResults[op] = append(c.maxResults[op], *n)
	} else {
		c.maxResults[op] = append(c.maxResults[op], 0)
	}
	if id := bacField[*string](in, "ServerId"); id != nil && bacField[*string](in, "NextToken") == nil {
		c.servers[op] = append(c.servers[op], *id)
	}
}

func (c *transferCalls) count(op string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.maxResults[op])
}

// listedServers is the servers op was called for, sorted.
func (c *transferCalls) listedServers(op string) []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Sorted(slices.Values(c.servers[op]))
}

func (c *transferCalls) lookup(arn string) ([]transfertypes.Tag, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.errs[arn]; err != nil {
		return nil, err
	}
	var list []transfertypes.Tag
	for k, v := range c.tags[arn] {
		list = append(list, transfertypes.Tag{Key: sdkaws.String(k), Value: sdkaws.String(v)})
	}
	return list, nil
}

func (c *transferCalls) listTags(in any) (any, error) {
	arn := sv(in.(*transfer.ListTagsForResourceInput).Arn)
	c.record("ListTagsForResource", in)
	c.mu.Lock()
	c.tagARNs = append(c.tagARNs, arn)
	c.mu.Unlock()
	tags, err := c.lookup(arn)
	if err != nil {
		return nil, err
	}
	return &transfer.ListTagsForResourceOutput{Arn: &arn, Tags: tags}, nil
}

func (c *transferCalls) describeWebApp(in any) (any, error) {
	id := sv(in.(*transfer.DescribeWebAppInput).WebAppId)
	c.record("DescribeWebApp", in)
	c.mu.Lock()
	c.webAppIDs = append(c.webAppIDs, id)
	c.mu.Unlock()
	arn := transferTestARNPrefix + "webapp/" + id
	tags, err := c.lookup(arn)
	if err != nil {
		return nil, err
	}
	return &transfer.DescribeWebAppOutput{WebApp: &transfertypes.DescribedWebApp{Arn: &arn, WebAppId: &id, Tags: tags}}, nil
}

// twoPages answers k's op with "tagged" on the first page and "bare" on the
// second, per server for a per-server listing.
func (c *transferCalls) twoPages(k transferKind) func(any) (any, error) {
	return func(in any) (any, error) {
		c.record(k.op, in)
		suffix, next := "tagged", sdkaws.String("p2")
		if bacField[*string](in, "NextToken") != nil {
			suffix, next = "bare", nil
		}
		server := sv(bacField[*string](in, "ServerId"))
		arn, id := k.arn(server, suffix), k.id(suffix)
		return k.page(&arn, &id, next), nil
	}
}

// transferHandlers answers every op the scan calls: each tagged listing in
// two pages, the tag reads from c, and one access of the directory server.
func transferHandlers(c *transferCalls) map[string]func(in any) (any, error) {
	var accessCalls parentCalls
	h := transferAccessHandlers(&accessCalls, map[string][][]string{"server-tagged": {{"S-1-5-21-1"}}}, nil, nil)
	listAccesses := h["ListAccesses"]
	h["ListAccesses"] = func(in any) (any, error) {
		c.record("ListAccesses", in)
		return listAccesses(in)
	}
	h["ListTagsForResource"] = c.listTags
	h["DescribeWebApp"] = c.describeWebApp
	for _, k := range transferKinds() {
		h[k.op] = c.twoPages(k)
	}
	return h
}

// transferScan runs the scan's real entry point against handlers; every op
// they do not answer is denied.
func transferScan(ctx context.Context, st *store.Store, handlers map[string]func(in any) (any, error)) (int, int, error) {
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(handlers, transferDenied)
	return scanTransfer(ctx, acct, testRegion, st, testScanID)
}

// transferTagReadARNs is the ARN of every row ListTagsForResource reads.
func transferTagReadARNs() []string {
	var arns []string
	for _, k := range transferKinds() {
		if k.typ != TypeTransferWebApp {
			arns = append(arns, transferKindARNs(k)...)
		}
	}
	slices.Sort(arns)
	return arns
}

// transferWarnings returns the recorded warnings' services.
func transferWarnings(w []store.ScanWarning) []string {
	var ops []string
	for _, x := range w {
		ops = append(ops, x.Service)
	}
	return ops
}

// Every tagged listing stores both its pages (each per-server listing for
// both servers), each row under its ARN and named by its id, with the tags
// its tag read returns, nil for a resource without tags. ListTagsForResource
// is sent exactly the ARNs of the rows other than web apps, DescribeWebApp
// each web app's id; every listing and ListTagsForResource are sent the
// model's page cap of 1000. Only the directory server's accesses are listed.
// No error, no warning.
func TestScanTransfer_StoresTagsOfEveryType(t *testing.T) {
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	calls := newTransferCalls()

	total, inserted, err := transferScan(context.Background(), st, transferHandlers(calls))
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != transferTestTotal || inserted != transferTestTotal {
		t.Errorf("total, inserted = %d, %d; want %d, %d", total, inserted, transferTestTotal, transferTestTotal)
	}
	if w := warns(); len(w) != 0 {
		t.Errorf("warnings = %v; want none", w)
	}
	for _, k := range transferKinds() {
		rows := rowsByNativeID(t, st, k.typ)
		if len(rows) != len(transferKindARNs(k)) {
			t.Errorf("%s rows = %d; want %d", k.typ, len(rows), len(transferKindARNs(k)))
		}
		for _, arn := range transferKindARNs(k) {
			r, ok := rows[arn]
			if !ok {
				t.Errorf("%s %s missing", k.typ, arn)
				continue
			}
			wantName := arn[strings.LastIndex(arn, "/")+1:]
			if sv(r.Name) != wantName || sv(r.Region) != testRegion || !strings.Contains(r.AttributesJSON, `"`+arn+`"`) {
				t.Errorf("%s Name, Region, attrs = %q, %q, %s; want %q, %q, the listed element", arn, sv(r.Name), sv(r.Region), r.AttributesJSON, wantName, testRegion)
			}
			if strings.HasSuffix(arn, "-tagged") {
				assertTagsMap(t, r.TagsJSON, map[string]string{"type": k.typ})
			} else if r.TagsJSON != nil {
				t.Errorf("%s TagsJSON = %q; want nil", arn, *r.TagsJSON)
			}
		}
	}
	if got := slices.Sorted(slices.Values(calls.tagARNs)); !slices.Equal(got, transferTagReadARNs()) {
		t.Errorf("ListTagsForResource ARNs = %v\nwant %v", got, transferTagReadARNs())
	}
	if got, want := slices.Sorted(slices.Values(calls.webAppIDs)), []string{"webapp-bare", "webapp-tagged"}; !slices.Equal(got, want) {
		t.Errorf("DescribeWebApp ids = %v; want %v", got, want)
	}
	for _, k := range transferKinds() {
		if k.perServer {
			if got, want := calls.listedServers(k.op), []string{"server-bare", "server-tagged"}; !slices.Equal(got, want) {
				t.Errorf("%s servers = %v; want %v", k.op, got, want)
			}
		}
	}
	if got := calls.listedServers("ListAccesses"); !slices.Equal(got, []string{"server-tagged"}) {
		t.Errorf("ListAccesses servers = %v; want only the directory server", got)
	}
	access := transferTestServerARN("server-tagged") + "/access/S-1-5-21-1"
	if r, ok := rowsByNativeID(t, st, TypeTransferAccess)[access]; !ok || r.TagsJSON != nil {
		t.Errorf("access %s stored %v with TagsJSON %v; want stored untagged", access, ok, r.TagsJSON)
	}
	assertContainsNative(t, st, transferTestServerARN("server-tagged"), access)
	for _, op := range []string{"ListServers", "ListAgreements", "ListUsers", "ListHostKeys", "ListCertificates", "ListConnectors", "ListProfiles", "ListWebApps", "ListWorkflows", "ListTagsForResource"} {
		if calls.count(op) == 0 {
			t.Errorf("%s not called", op)
		}
		for _, n := range calls.maxResults[op] {
			if n != 1000 {
				t.Errorf("%s MaxResults = %d; want 1000", op, n)
			}
		}
	}
}

// A rescan stores the same rows without inserting any.
func TestScanTransfer_RescanInsertsNone(t *testing.T) {
	st := newTestStore(t)
	if _, _, err := transferScan(context.Background(), st, transferHandlers(newTransferCalls())); err != nil {
		t.Fatalf("first scan: %v", err)
	}
	total, inserted, err := transferScan(context.Background(), st, transferHandlers(newTransferCalls()))
	if err != nil {
		t.Fatalf("rescan: %v", err)
	}
	if total != transferTestTotal || inserted != 0 {
		t.Errorf("rescan total, inserted = %d, %d; want %d, 0", total, inserted, transferTestTotal)
	}
}

// A failed tag read. ListTagsForResource models no not-found code, so a
// ResourceNotFoundException keeps the row and is an error; DescribeWebApp
// models it, so a web app it no longer finds is dropped silently. A denial
// keeps the row untagged and warns once under the tag op; a throttle keeps
// it and is transient; any other error keeps it and is an error.
func TestScanTransfer_TagReadFailures(t *testing.T) {
	type tagCase struct {
		name      string
		arn, typ  string
		err       error
		outcome   serviceOutcome // -1: no error
		code      string
		wantTotal int
		wantKept  bool
		warnOp    string
	}
	user := transferKindOf(t, "ListUsers").arn("server-bare", "tagged")
	cert := transferKindOf(t, "ListCertificates").arn("", "bare")
	webApp := transferKindOf(t, "ListWebApps").arn("", "tagged")
	var cases []tagCase
	for _, x := range []struct{ arn, typ string }{{user, TypeTransferUser}, {cert, TypeTransferCertificate}} {
		cases = append(cases,
			tagCase{x.typ + "/unmodeled not found", x.arn, x.typ, apiErr("ResourceNotFoundException", "gone"), outcomeError, "ResourceNotFoundException", transferTestTotal, true, ""},
			tagCase{x.typ + "/denied", x.arn, x.typ, transferDenied, -1, "", transferTestTotal, true, transferTagsOp},
			tagCase{x.typ + "/transient", x.arn, x.typ, transferTestThrottle, outcomeTransient, "ThrottlingException", transferTestTotal, true, ""},
			tagCase{x.typ + "/other error", x.arn, x.typ, transferTestInvalid, outcomeError, "InvalidRequestException", transferTestTotal, true, ""},
		)
	}
	cases = append(cases,
		tagCase{"web app/not found", webApp, TypeTransferWebApp, apiErr("ResourceNotFoundException", "gone"), -1, "", transferTestTotal - 1, false, ""},
		tagCase{"web app/denied", webApp, TypeTransferWebApp, transferDenied, -1, "", transferTestTotal, true, "transfer:DescribeWebApp"},
		tagCase{"web app/transient", webApp, TypeTransferWebApp, transferTestThrottle, outcomeTransient, "ThrottlingException", transferTestTotal, true, ""},
		tagCase{"web app/other error", webApp, TypeTransferWebApp, transferTestInvalid, outcomeError, "InvalidRequestException", transferTestTotal, true, ""},
	)
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			calls := newTransferCalls()
			calls.errs[c.arn] = c.err

			total, _, err := transferScan(context.Background(), st, transferHandlers(calls))
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
			w := warns()
			var want []string
			if c.warnOp != "" {
				want = []string{c.warnOp}
			}
			if got := transferWarnings(w); !slices.Equal(got, want) {
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

// A DescribeWebApp answer without a WebApp, and a listed web app without a
// WebAppId, give no tags: the row is kept untagged and the read is an error.
// The id-less one is never described.
func TestScanTransfer_WebAppTagReadWithoutBodyOrID(t *testing.T) {
	webApps := transferKindOf(t, "ListWebApps")
	t.Run("no WebApp in the answer", func(t *testing.T) {
		st := newTestStore(t)
		handlers := transferHandlers(newTransferCalls())
		describe := handlers["DescribeWebApp"]
		handlers["DescribeWebApp"] = func(in any) (any, error) {
			if sv(in.(*transfer.DescribeWebAppInput).WebAppId) == "webapp-tagged" {
				return &transfer.DescribeWebAppOutput{}, nil
			}
			return describe(in)
		}
		total, _, err := transferScan(context.Background(), st, handlers)
		assertGlueOutcome(t, err, outcomeError)
		if total != transferTestTotal {
			t.Errorf("total = %d; want %d", total, transferTestTotal)
		}
		if r, ok := rowsByNativeID(t, st, TypeTransferWebApp)[webApps.arn("", "tagged")]; !ok || r.TagsJSON != nil {
			t.Errorf("web app stored %v with TagsJSON %v; want stored untagged", ok, r.TagsJSON)
		}
	})
	t.Run("no WebAppId", func(t *testing.T) {
		st := newTestStore(t)
		calls := newTransferCalls()
		handlers := transferHandlers(calls)
		arn := webApps.arn("", "noid")
		handlers["ListWebApps"] = func(in any) (any, error) {
			calls.record("ListWebApps", in)
			return &transfer.ListWebAppsOutput{WebApps: []transfertypes.ListedWebApp{{Arn: &arn}}}, nil
		}
		total, _, err := transferScan(context.Background(), st, handlers)
		assertGlueOutcome(t, err, outcomeError)
		if total != transferTestTotal-1 {
			t.Errorf("total = %d; want %d", total, transferTestTotal-1)
		}
		r, ok := rowsByNativeID(t, st, TypeTransferWebApp)[arn]
		if !ok || r.TagsJSON != nil || sv(r.Name) != arn {
			t.Errorf("web app stored %v, Name %q, TagsJSON %v; want stored untagged under its ARN as name", ok, sv(r.Name), r.TagsJSON)
		}
		if len(calls.webAppIDs) != 0 {
			t.Errorf("DescribeWebApp ids = %v; want none", calls.webAppIDs)
		}
	})
}

// Each web app on a page of several, and on a later page, is described by
// its own WebAppId and stored with its own tags.
func TestScanTransfer_WebAppsDescribedByOwnIDAcrossPages(t *testing.T) {
	st := newTestStore(t)
	calls := newTransferCalls()
	handlers := transferHandlers(calls)
	webApps := transferKindOf(t, "ListWebApps")
	pages := [][]string{{"webapp-a", "webapp-b"}, {"webapp-c"}}
	for _, id := range []string{"webapp-a", "webapp-b", "webapp-c"} {
		calls.tags[transferTestARNPrefix+"webapp/"+id] = map[string]string{"id": id}
	}
	handlers[webApps.op] = func(in any) (any, error) {
		calls.record(webApps.op, in)
		page, next := pages[0], sdkaws.String("p2")
		if bacField[*string](in, "NextToken") != nil {
			page, next = pages[1], nil
		}
		out := &transfer.ListWebAppsOutput{NextToken: next}
		for _, id := range page {
			out.WebApps = append(out.WebApps, transfertypes.ListedWebApp{Arn: sdkaws.String(transferTestARNPrefix + "webapp/" + id), WebAppId: sdkaws.String(id)})
		}
		return out, nil
	}

	if _, _, err := transferScan(context.Background(), st, handlers); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if got, want := slices.Sorted(slices.Values(calls.webAppIDs)), []string{"webapp-a", "webapp-b", "webapp-c"}; !slices.Equal(got, want) {
		t.Errorf("DescribeWebApp ids = %v; want %v", got, want)
	}
	rows := rowsByNativeID(t, st, TypeTransferWebApp)
	for _, id := range []string{"webapp-a", "webapp-b", "webapp-c"} {
		assertTagsMap(t, rows[transferTestARNPrefix+"webapp/"+id].TagsJSON, map[string]string{"id": id})
	}
}

// ListTagsForResource pages are all read; a repeated NextToken fails the
// read, keeping the row untagged.
func TestScanTransfer_TagPages(t *testing.T) {
	arn := transferKindOf(t, "ListConnectors").arn("", "tagged")
	pageOf := func(next *string, k, v string) *transfer.ListTagsForResourceOutput {
		return &transfer.ListTagsForResourceOutput{NextToken: next, Tags: []transfertypes.Tag{{Key: sdkaws.String(k), Value: sdkaws.String(v)}}}
	}
	for _, c := range []struct {
		name     string
		second   *string
		wantTags map[string]string
	}{
		{"every page read", nil, map[string]string{"a": "1", "b": "2"}},
		{"repeated token", sdkaws.String("t2"), nil},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			calls := newTransferCalls()
			handlers := transferHandlers(calls)
			handlers["ListTagsForResource"] = func(in any) (any, error) {
				req := in.(*transfer.ListTagsForResourceInput)
				if sv(req.Arn) != arn {
					return calls.listTags(in)
				}
				if req.NextToken == nil {
					return pageOf(sdkaws.String("t2"), "a", "1"), nil
				}
				return pageOf(c.second, "b", "2"), nil
			}
			_, _, err := transferScan(context.Background(), st, handlers)
			r := rowsByNativeID(t, st, TypeTransferConnector)[arn]
			if c.wantTags == nil {
				if !errors.Is(err, errRepeatedNextToken) {
					t.Fatalf("err = %v; want errRepeatedNextToken", err)
				}
				if r.TagsJSON != nil {
					t.Errorf("TagsJSON = %q; want nil", *r.TagsJSON)
				}
				return
			}
			if err != nil {
				t.Fatalf("scan: %v", err)
			}
			assertTagsMap(t, r.TagsJSON, c.wantTags)
		})
	}
}

// Denied tag reads in two phases send one warning for the whole scan; the
// rows are kept.
func TestScanTransfer_TagDenialsWarnOncePerScan(t *testing.T) {
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	calls := newTransferCalls()
	for _, op := range []string{"ListUsers", "ListWorkflows"} {
		for _, arn := range transferKindARNs(transferKindOf(t, op)) {
			calls.errs[arn] = transferDenied
		}
	}

	total, _, err := transferScan(context.Background(), st, transferHandlers(calls))
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != transferTestTotal {
		t.Errorf("total = %d; want %d", total, transferTestTotal)
	}
	if got := transferWarnings(warns()); !slices.Equal(got, []string{transferTagsOp}) {
		t.Errorf("warnings = %v; want one under %s", got, transferTagsOp)
	}
}

// A throttled tag read beside a hard failure of the same op is demoted to
// one warning, so the hard error decides the outcome.
func TestScanTransfer_TagThrottleDemotedBesideHardError(t *testing.T) {
	st := newTestStore(t)
	warns := countOpWarnings(st, transferTagsOp)
	calls := newTransferCalls()
	calls.errs[transferKindOf(t, "ListCertificates").arn("", "tagged")] = transferTestThrottle
	calls.errs[transferKindOf(t, "ListWorkflows").arn("", "bare")] = transferTestInvalid

	_, _, err := transferScan(context.Background(), st, transferHandlers(calls))
	assertGlueOutcome(t, err, outcomeError)
	assertAPICode(t, err, "InvalidRequestException")
	if *warns != 1 {
		t.Errorf("%s warnings = %d; want 1", transferTagsOp, *warns)
	}
}

// A failed write stops the scan: no warning about the rows it lost (a denied
// tag read among them), no later phase runs, and the earlier phases' rows
// stay stored. A failed server write lists no server's children.
func TestScanTransfer_FailedWriteStopsScan(t *testing.T) {
	t.Run("certificates", func(t *testing.T) {
		st := newTestStore(t)
		warns := rhWarnRecorder(st)
		refuseStoreWrites(t, st, TypeTransferCertificate)
		calls := newTransferCalls()
		calls.errs[transferKindOf(t, "ListCertificates").arn("", "tagged")] = transferDenied

		total, _, err := transferScan(context.Background(), st, transferHandlers(calls))
		if !errors.Is(err, store.ErrStoreWrite) {
			t.Fatalf("err = %v; want store.ErrStoreWrite", err)
		}
		if w := warns(); len(w) != 0 {
			t.Errorf("warnings = %v; want none", w)
		}
		if n := calls.count("ListCertificates"); n != 1 {
			t.Errorf("ListCertificates called %d times; want 1, none after the failed write", n)
		}
		if n := calls.count("ListConnectors"); n != 0 {
			t.Errorf("ListConnectors called %d times; want 0 after the failed write", n)
		}
		if want := 2 + 3*2*2; total != want {
			t.Errorf("total = %d; want %d (the servers and their children)", total, want)
		}
	})
	t.Run("servers", func(t *testing.T) {
		st := newTestStore(t)
		refuseStoreWrites(t, st, TypeTransferServer)
		calls := newTransferCalls()

		_, _, err := transferScan(context.Background(), st, transferHandlers(calls))
		if !errors.Is(err, store.ErrStoreWrite) {
			t.Fatalf("err = %v; want store.ErrStoreWrite", err)
		}
		for _, op := range []string{"ListAgreements", "ListUsers", "ListHostKeys", "ListAccesses"} {
			if n := calls.count(op); n != 0 {
				t.Errorf("%s called %d times after the failed server write", op, n)
			}
		}
	})
}

// A listing's failed page keeps the pages read before it, and its error does
// not stop the later phases. A server not stored has no children read.
func TestScanTransfer_ListFailureKeepsPagesAndLaterPhases(t *testing.T) {
	for _, c := range []struct {
		op        string
		wantTotal int
	}{
		{"ListCertificates", transferTestTotal - 1},
		// The second server and its six children are lost.
		{"ListServers", transferTestTotal - 1 - 6},
	} {
		t.Run(c.op, func(t *testing.T) {
			st := newTestStore(t)
			handlers := transferHandlers(newTransferCalls())
			k := transferKindOf(t, c.op)
			handlers[c.op] = failOnCall(2, transferTestInvalid, handlers[c.op])

			total, _, err := transferScan(context.Background(), st, handlers)
			assertGlueOutcome(t, err, outcomeError)
			assertAPICode(t, err, "InvalidRequestException")
			if total != c.wantTotal {
				t.Errorf("total = %d; want %d", total, c.wantTotal)
			}
			if _, ok := rowsByNativeID(t, st, k.typ)[k.arn("", "tagged")]; !ok {
				t.Errorf("%s row from the page before the failure missing", k.typ)
			}
			if got := len(rowsByNativeID(t, st, TypeTransferWorkflow)); got != 2 {
				t.Errorf("workflows (a later phase) = %d; want 2", got)
			}
		})
	}
}

// A repeated NextToken ends the listing as an error, keeping its pages.
func TestScanTransfer_RepeatedTokenKeepsPages(t *testing.T) {
	st := newTestStore(t)
	handlers := transferHandlers(newTransferCalls())
	k := transferKindOf(t, "ListProfiles")
	handlers[k.op] = func(in any) (any, error) {
		suffix := "tagged"
		if bacField[*string](in, "NextToken") != nil {
			suffix = "bare"
		}
		arn, id := k.arn("", suffix), k.id(suffix)
		return k.page(&arn, &id, sdkaws.String("same")), nil
	}

	total, _, err := transferScan(context.Background(), st, handlers)
	if !errors.Is(err, errRepeatedNextToken) {
		t.Fatalf("err = %v; want errRepeatedNextToken", err)
	}
	if total != transferTestTotal {
		t.Errorf("total = %d; want %d", total, transferTestTotal)
	}
}

// One server's failed child listing costs the other server none of its
// rows: a server deleted since it was listed is skipped silently, a denial
// warns once under the op, and any other error is returned.
func TestScanTransfer_PerServerListingFailures(t *testing.T) {
	for _, op := range []string{"ListAgreements", "ListUsers", "ListHostKeys"} {
		for _, c := range []struct {
			name      string
			err       error
			code      string
			wantWarns int
		}{
			{"server deleted", apiErr("ResourceNotFoundException", "gone"), "", 0},
			{"denied", transferDenied, "", 1},
			{"other error", transferTestInvalid, "InvalidRequestException", 0},
		} {
			t.Run(op+"/"+c.name, func(t *testing.T) {
				st := newTestStore(t)
				warns := countOpWarnings(st, "transfer:"+op)
				handlers := transferHandlers(newTransferCalls())
				list := handlers[op]
				handlers[op] = func(in any) (any, error) {
					if sv(bacField[*string](in, "ServerId")) == "server-tagged" {
						return nil, c.err
					}
					return list(in)
				}

				total, _, err := transferScan(context.Background(), st, handlers)
				if c.code == "" {
					if err != nil {
						t.Fatalf("err = %v; want nil", err)
					}
				} else {
					assertGlueOutcome(t, err, outcomeError)
					assertAPICode(t, err, c.code)
				}
				if want := transferTestTotal - 2; total != want {
					t.Errorf("total = %d; want %d", total, want)
				}
				k := transferKindOf(t, op)
				rows := rowsByNativeID(t, st, k.typ)
				if _, ok := rows[k.arn("server-bare", "bare")]; !ok || len(rows) != 2 {
					t.Errorf("%s rows = %v; want only the other server's two", k.typ, rows)
				}
				if *warns != c.wantWarns {
					t.Errorf("warnings = %d; want %d", *warns, c.wantWarns)
				}
			})
		}
	}
}

// An element without an ARN is not stored, is not sent to the tag op, and is
// warned about once under its listing, keyed by its id or by the fallback
// text when it has none. A server without one has no children read. A server
// with an ARN but no ServerId is stored under its ARN, named by it, without
// its children read, with one warning.
func TestScanTransfer_ElementWithoutARNOrID(t *testing.T) {
	certs := transferKindOf(t, "ListCertificates")
	servers := transferKindOf(t, "ListServers")
	noIDServer := servers.arn("", "noid")
	for _, c := range []struct {
		name, op, key string
		answer        any
		lost          int
		stored        string
	}{
		{
			"certificate", "ListCertificates", "certificate-x",
			&transfer.ListCertificatesOutput{Certificates: []transfertypes.ListedCertificate{{CertificateId: sdkaws.String("certificate-x")}}}, 2, "",
		},
		{
			"certificate with no id", "ListCertificates", transferNoIdentifier,
			&transfer.ListCertificatesOutput{Certificates: []transfertypes.ListedCertificate{{}}}, 2, "",
		},
		{
			"server", "ListServers", "server-x",
			&transfer.ListServersOutput{Servers: []transfertypes.ListedServer{{ServerId: sdkaws.String("server-x"), IdentityProviderType: transfertypes.IdentityProviderTypeAwsDirectoryService}}},
			// Both servers, their twelve children and the one access.
			2 + 12 + 1, "",
		},
		{
			"server without ServerId", "ListServers", noIDServer,
			&transfer.ListServersOutput{Servers: []transfertypes.ListedServer{{Arn: &noIDServer, IdentityProviderType: transfertypes.IdentityProviderTypeAwsDirectoryService}}},
			// Replaces both servers: one stored, their children not read.
			2 + 12 + 1 - 1, noIDServer,
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			calls := newTransferCalls()
			handlers := transferHandlers(calls)
			handlers[c.op] = func(in any) (any, error) {
				calls.record(c.op, in)
				return c.answer, nil
			}

			total, _, err := transferScan(context.Background(), st, handlers)
			if err != nil {
				t.Fatalf("scan: %v", err)
			}
			if want := transferTestTotal - c.lost; total != want {
				t.Errorf("total = %d; want %d", total, want)
			}
			w := warns()
			if len(w) != 1 || w[0].Service != "transfer:"+c.op || !strings.Contains(w[0].Message, c.key) {
				t.Fatalf("warnings = %v; want one under transfer:%s naming %s", w, c.op, c.key)
			}
			if c.op == "ListServers" {
				for _, op := range []string{"ListAgreements", "ListUsers", "ListHostKeys", "ListAccesses"} {
					if n := calls.count(op); n != 0 {
						t.Errorf("%s called %d times; want none", op, n)
					}
				}
			} else if n := len(rowsByNativeID(t, st, certs.typ)); n != 0 {
				t.Errorf("certificates stored = %d; want 0", n)
			}
			if c.stored != "" {
				r, ok := rowsByNativeID(t, st, TypeTransferServer)[c.stored]
				if !ok || sv(r.Name) != c.stored {
					t.Errorf("server %s stored %v, Name %q; want stored named by its ARN", c.stored, ok, sv(r.Name))
				}
				if !slices.Contains(calls.tagARNs, c.stored) {
					t.Errorf("tag ARNs %v lack %s", calls.tagARNs, c.stored)
				}
			}
			for _, arn := range calls.tagARNs {
				if arn == "" {
					t.Error("an empty ARN was sent to ListTagsForResource")
				}
			}
		})
	}
}

// A GovCloud or China ARN reaches the tag op unchanged.
func TestScanTransfer_TagReadsKeepPartitionARNs(t *testing.T) {
	for _, arn := range []string{
		"arn:aws-us-gov:transfer:us-gov-west-1:" + testAccountID + ":server/s-gov",
		"arn:aws-cn:transfer:cn-north-1:" + testAccountID + ":server/s-cn",
	} {
		t.Run(arn, func(t *testing.T) {
			st := newTestStore(t)
			calls := newTransferCalls()
			calls.tags[arn] = map[string]string{"env": "prod"}
			handlers := transferHandlers(calls)
			handlers["ListServers"] = func(in any) (any, error) {
				calls.record("ListServers", in)
				return &transfer.ListServersOutput{Servers: []transfertypes.ListedServer{{Arn: sdkaws.String(arn), ServerId: sdkaws.String("s-x")}}}, nil
			}

			if _, _, err := transferScan(context.Background(), st, handlers); err != nil {
				t.Fatalf("scan: %v", err)
			}
			if !slices.Contains(calls.tagARNs, arn) {
				t.Fatalf("ListTagsForResource ARNs = %v; want %s among them", calls.tagARNs, arn)
			}
			assertTagsMap(t, rowsByNativeID(t, st, TypeTransferServer)[arn].TagsJSON, map[string]string{"env": "prod"})
		})
	}
}

// Once ctx ends, the stop is one member of the error, although the phase in
// flight saw it in its listing and its tag reads, and the later phases are
// not run.
func TestScanTransfer_ContextEndReportedOnce(t *testing.T) {
	st := newTestStore(t)
	calls := newTransferCalls()
	handlers := transferHandlers(calls)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	list := handlers["ListConnectors"]
	handlers["ListConnectors"] = func(in any) (any, error) {
		if bacField[*string](in, "NextToken") != nil {
			cancel()
			return nil, context.Canceled
		}
		return list(in)
	}

	_, _, err := transferScan(ctx, st, handlers)
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
	if n := calls.count("ListProfiles"); n != 0 {
		t.Errorf("ListProfiles called %d times after ctx ended", n)
	}
}

// An NXDOMAIN after rows were stored is a failure of this scan, not a region
// gap: the counts are kept. With nothing stored it stays region-unavailable.
func TestScanTransfer_NXDOMAIN(t *testing.T) {
	nx := &net.DNSError{Err: "no such host", Name: "transfer.us-east-1.amazonaws.com", IsNotFound: true}
	t.Run("one listing after others stored rows", func(t *testing.T) {
		st := newTestStore(t)
		handlers := transferHandlers(newTransferCalls())
		handlers["ListProfiles"] = func(any) (any, error) { return nil, nx }
		total, _, err := transferScan(context.Background(), st, handlers)
		assertGlueOutcome(t, err, outcomeError)
		if total != transferTestTotal-2 {
			t.Errorf("total = %d; want the %d other rows counted", total, transferTestTotal-2)
		}
	})
	t.Run("tag read after the listing", func(t *testing.T) {
		st := newTestStore(t)
		calls := newTransferCalls()
		calls.errs[transferKindOf(t, "ListHostKeys").arn("server-bare", "bare")] = nx
		_, _, err := transferScan(context.Background(), st, transferHandlers(calls))
		assertGlueOutcome(t, err, outcomeError)
	})
	t.Run("nothing stored", func(t *testing.T) {
		st := newTestStore(t)
		warns := rhWarnRecorder(st)
		acct := newTestAccount(testAccountID)
		acct.cfg = opStubConfig(nil, nx)
		_, _, err := scanTransfer(context.Background(), acct, testRegion, st, testScanID)
		assertGlueOutcome(t, err, outcomeUnavailable)
		if w := warns(); len(w) != 0 {
			t.Errorf("warnings = %v; want none", w)
		}
	})
}

// The access phase runs last: when ListAccesses is called, every other
// listing has already been called. Its hard error is returned with every
// other phase's rows counted.
func TestScanTransfer_AccessPhaseRunsLast(t *testing.T) {
	st := newTestStore(t)
	calls := newTransferCalls()
	handlers := transferHandlers(calls)
	var notYet []string
	handlers["ListAccesses"] = func(any) (any, error) {
		for _, k := range transferKinds() {
			if calls.count(k.op) == 0 {
				notYet = append(notYet, k.op)
			}
		}
		return nil, apiErr("ConflictException", "conflict")
	}

	total, _, err := transferScan(context.Background(), st, handlers)
	if !isAPIErrorCode(err, "ConflictException") {
		t.Fatalf("err = %v; want ConflictException", err)
	}
	if len(notYet) != 0 {
		t.Errorf("listings not yet called when ListAccesses ran: %v", notYet)
	}
	if total != transferTestTagged {
		t.Errorf("total = %d; want %d", total, transferTestTagged)
	}
}

func TestTransferRow(t *testing.T) {
	acct := newTestAccount(testAccountID)
	element := transfertypes.ListedProfile{ProfileId: sdkaws.String("p-1")}
	arn := transferTestARNPrefix + "profile/p-1"
	for _, c := range []struct {
		name         string
		arn, id      *string
		wantName     string
		why, wantKey string
	}{
		{"ARN and id", &arn, sdkaws.String("p-1"), "p-1", "", ""},
		{"ARN without id", &arn, nil, arn, "", ""},
		{"id without ARN", nil, sdkaws.String("p-1"), "", "profiles have no ARN", "p-1"},
		{"neither", sdkaws.String(""), nil, "", "profiles have no ARN", transferNoIdentifier},
	} {
		t.Run(c.name, func(t *testing.T) {
			r, why, key := transferRow(acct, testRegion, testScanID, TypeTransferProfile, "profiles", c.arn, c.id, element)
			if why != c.why || key != c.wantKey {
				t.Errorf("why, key = %q, %q; want %q, %q", why, key, c.why, c.wantKey)
			}
			if c.wantName == "" {
				if r != nil {
					t.Errorf("row = %+v; want none", r)
				}
				return
			}
			if r == nil {
				t.Fatal("row = nil; want one")
			}
			if r.NativeID != arn || sv(r.Name) != c.wantName || r.Type != TypeTransferProfile || sv(r.Region) != testRegion || r.DiscoveredBy != testScanID || r.AccountID != testAccountID {
				t.Errorf("row = %+v; want NativeID %s, Name %s", r, arn, c.wantName)
			}
			if r.AttributesJSON != mustJSON(element) {
				t.Errorf("attrs = %s; want the element verbatim", r.AttributesJSON)
			}
		})
	}
}
