package aws

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"net"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/pcaconnectorad"
	pcaadtypes "github.com/aws/aws-sdk-go-v2/service/pcaconnectorad/types"
	"github.com/icearp/disco-cli/store"
)

// pcaADTestTotal is every row of the fixture: two connectors and two
// directory registrations; two service principal names under each
// registration; two templates under each connector; two group access control
// entries under each of the four templates. Every listing returns one element
// per page, "tagged" on the first and "bare" on the second.
const pcaADTestTotal = 2 + 2 + 2*2 + 2*2 + 2*4

var (
	pcaADTestDenied   = apiErr("AccessDeniedException", "User: arn:aws:iam::123:user/x is not authorized to perform: pca-connector-ad:List")
	pcaADTestThrottle = apiErr("ThrottlingException", "Rate exceeded")
	pcaADTestBad      = apiErr("ValidationException", "bad")
	pcaADTestGone     = apiErr("ResourceNotFoundException", "gone")
	// pcaADTestTime is every listed element's CreatedAt, which the rows do
	// not carry.
	pcaADTestTime = time.Date(2026, 9, 1, 12, 0, 0, 0, time.UTC)
)

func pcaADTestARN(resource string) string {
	return "arn:aws:pca-connector-ad:" + testRegion + ":" + testAccountID + ":" + resource
}

// pcaADTestParent is the parent ARN a child listing's input sends; "" for a
// top-level listing.
func pcaADTestParent(in any) string {
	for _, f := range []string{"DirectoryRegistrationArn", "ConnectorArn", "TemplateArn"} {
		if p := bacField[*string](in, f); p != nil {
			return *p
		}
	}
	return ""
}

// pcaADKind is one listing: its op, the type it stores, the listing its
// parents come from ("" for none), whether its rows have their tags read,
// the Status its rows carry ("" for none), and, for the element suffix lists
// under parent (an ARN, "" for a top-level listing): native, the row's
// NativeID; name, its Name; attr, a field of the element and its value; and
// page, the op's output holding that one element and next.
type pcaADKind struct {
	op, typ, parentOp string
	tagged            bool
	status            string
	native, name      func(parent, suffix string) string
	attr              func(parent, suffix string) (field, value string)
	page              func(parent, suffix string, next *string) any
}

func pcaADConnectorARN(suffix string) string { return pcaADTestARN("connector/c-" + suffix) }
func pcaADDirRegARN(suffix string) string    { return pcaADTestARN("directory-registration/d-" + suffix) }
func pcaADACESID(suffix string) string       { return "S-1-5-" + suffix }

// pcaADACEDisplay is the display name of the first page's entry; the second
// page's entry has none, so its row is named by its SID.
func pcaADACEDisplay(suffix string) string {
	if suffix == "tagged" {
		return "group-" + suffix
	}
	return ""
}

func pcaADKinds() []pcaADKind {
	ownARN := func(native func(parent, suffix string) string) func(parent, suffix string) (string, string) {
		return func(parent, suffix string) (string, string) { return "Arn", native(parent, suffix) }
	}
	connector := func(_, s string) string { return pcaADConnectorARN(s) }
	dirReg := func(_, s string) string { return pcaADDirRegARN(s) }
	template := func(p, s string) string { return p + "/template/t-" + s }
	return []pcaADKind{
		{
			op: "ListConnectors", typ: TypePCAConnectorADConnector, tagged: true, status: "ACTIVE",
			native: connector, name: connector, attr: ownARN(connector),
			page: func(_, s string, next *string) any {
				return &pcaconnectorad.ListConnectorsOutput{NextToken: next, Connectors: []pcaadtypes.ConnectorSummary{{
					Arn: sdkaws.String(pcaADConnectorARN(s)), DirectoryId: sdkaws.String("d-" + s), Status: pcaadtypes.ConnectorStatusActive, CreatedAt: sdkaws.Time(pcaADTestTime),
				}}}
			},
		},
		{
			op: "ListDirectoryRegistrations", typ: TypePCAConnectorADDirectoryRegistration, tagged: true, status: "ACTIVE",
			native: dirReg, name: dirReg, attr: ownARN(dirReg),
			page: func(_, s string, next *string) any {
				return &pcaconnectorad.ListDirectoryRegistrationsOutput{NextToken: next, DirectoryRegistrations: []pcaadtypes.DirectoryRegistrationSummary{{
					Arn: sdkaws.String(pcaADDirRegARN(s)), DirectoryId: sdkaws.String("d-" + s), Status: pcaadtypes.DirectoryRegistrationStatusActive, CreatedAt: sdkaws.Time(pcaADTestTime),
				}}}
			},
		},
		{
			op: "ListServicePrincipalNames", typ: TypePCAConnectorADServicePrincipalName, parentOp: "ListDirectoryRegistrations", status: "ACTIVE",
			native: func(p, s string) string { return p + "/service-principal-name/" + pcaADConnectorARN(s) },
			name:   func(_, s string) string { return pcaADConnectorARN(s) },
			attr:   func(_, s string) (string, string) { return "ConnectorArn", pcaADConnectorARN(s) },
			page: func(p, s string, next *string) any {
				return &pcaconnectorad.ListServicePrincipalNamesOutput{NextToken: next, ServicePrincipalNames: []pcaadtypes.ServicePrincipalNameSummary{{
					DirectoryRegistrationArn: sdkaws.String(p), ConnectorArn: sdkaws.String(pcaADConnectorARN(s)), Status: pcaadtypes.ServicePrincipalNameStatusActive, CreatedAt: sdkaws.Time(pcaADTestTime),
				}}}
			},
		},
		{
			op: "ListTemplates", typ: TypePCAConnectorADTemplate, parentOp: "ListConnectors", tagged: true,
			native: template, name: func(_, s string) string { return "t-" + s }, attr: ownARN(template),
			page: func(p, s string, next *string) any {
				return &pcaconnectorad.ListTemplatesOutput{NextToken: next, Templates: []pcaadtypes.TemplateSummary{{
					Arn: sdkaws.String(template(p, s)), ConnectorArn: sdkaws.String(p), Name: sdkaws.String("t-" + s), Status: pcaadtypes.TemplateStatusActive, CreatedAt: sdkaws.Time(pcaADTestTime),
				}}}
			},
		},
		{
			op: "ListTemplateGroupAccessControlEntries", typ: TypePCAConnectorADTemplateGroupACE, parentOp: "ListTemplates",
			native: func(p, s string) string { return p + "/access-control-entry/" + pcaADACESID(s) },
			name: func(_, s string) string {
				if d := pcaADACEDisplay(s); d != "" {
					return d
				}
				return pcaADACESID(s)
			},
			attr: func(_, s string) (string, string) { return "GroupSecurityIdentifier", pcaADACESID(s) },
			page: func(p, s string, next *string) any {
				e := pcaadtypes.AccessControlEntrySummary{TemplateArn: sdkaws.String(p), GroupSecurityIdentifier: sdkaws.String(pcaADACESID(s)), CreatedAt: sdkaws.Time(pcaADTestTime)}
				if d := pcaADACEDisplay(s); d != "" {
					e.GroupDisplayName = &d
				}
				return &pcaconnectorad.ListTemplateGroupAccessControlEntriesOutput{NextToken: next, AccessControlEntries: []pcaadtypes.AccessControlEntrySummary{e}}
			},
		},
	}
}

func pcaADKindOf(t *testing.T, op string) pcaADKind {
	t.Helper()
	for _, k := range pcaADKinds() {
		if k.op == op {
			return k
		}
	}
	t.Fatalf("%s is no PCA Connector for AD listing", op)
	return pcaADKind{}
}

// pcaADListed is one element of the fixture: its parent's ARN and suffix.
type pcaADListed struct{ parent, suffix string }

// listedPCAAD is every element k's listing returns in the fixture.
func (k pcaADKind) listedPCAAD(t *testing.T) []pcaADListed {
	parents := []string{""}
	if k.parentOp != "" {
		parents = pcaADKindOf(t, k.parentOp).nativesPCAAD(t)
	}
	var out []pcaADListed
	for _, p := range parents {
		out = append(out, pcaADListed{p, "tagged"}, pcaADListed{p, "bare"})
	}
	return out
}

// nativesPCAAD is the NativeID of every row k's listing stores in the fixture.
func (k pcaADKind) nativesPCAAD(t *testing.T) []string {
	var ids []string
	for _, l := range k.listedPCAAD(t) {
		ids = append(ids, k.native(l.parent, l.suffix))
	}
	return ids
}

// pcaADCalls records the calls of a scan and answers the tag reads, by ARN,
// with tags or errs.
type pcaADCalls struct {
	mu         sync.Mutex
	maxResults map[string][]int32
	parents    map[string][]string
	tagARNs    []string
	tags       map[string]map[string]string
	errs       map[string]error
}

// newPCAADCalls tags every first-page row of a tagged kind with its type.
func newPCAADCalls(t *testing.T) *pcaADCalls {
	c := &pcaADCalls{maxResults: map[string][]int32{}, parents: map[string][]string{}, tags: map[string]map[string]string{}, errs: map[string]error{}}
	for _, k := range pcaADKinds() {
		if !k.tagged {
			continue
		}
		for _, l := range k.listedPCAAD(t) {
			if l.suffix == "tagged" {
				c.tags[k.native(l.parent, l.suffix)] = map[string]string{"type": k.typ}
			}
		}
	}
	return c
}

// recordPCAAD notes op's MaxResults (0 when unset) and, on a first page, the
// parent its input names.
func (c *pcaADCalls) recordPCAAD(op string, in any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	n := bacField[*int32](in, "MaxResults")
	if n == nil {
		n = sdkaws.Int32(0)
	}
	c.maxResults[op] = append(c.maxResults[op], *n)
	if bacField[*string](in, "NextToken") == nil {
		c.parents[op] = append(c.parents[op], pcaADTestParent(in))
	}
}

func (c *pcaADCalls) calledPCAAD(op string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.maxResults[op])
}

// parentsPCAAD is the parents op was called for, sorted.
func (c *pcaADCalls) parentsPCAAD(op string) []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Sorted(slices.Values(c.parents[op]))
}

func (c *pcaADCalls) tagARNsPCAAD() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Sorted(slices.Values(c.tagARNs))
}

func (c *pcaADCalls) listTagsPCAAD(in any) (any, error) {
	arn := sv(in.(*pcaconnectorad.ListTagsForResourceInput).ResourceArn)
	c.mu.Lock()
	defer c.mu.Unlock()
	c.tagARNs = append(c.tagARNs, arn)
	if err := c.errs[arn]; err != nil {
		return nil, err
	}
	return &pcaconnectorad.ListTagsForResourceOutput{Tags: c.tags[arn]}, nil
}

// twoPagesPCAAD answers k's op with "tagged" on the first page and "bare" on
// the second, under the parent its input names.
func (c *pcaADCalls) twoPagesPCAAD(k pcaADKind) func(any) (any, error) {
	return func(in any) (any, error) {
		c.recordPCAAD(k.op, in)
		suffix, next := "tagged", sdkaws.String("p2")
		if bacField[*string](in, "NextToken") != nil {
			suffix, next = "bare", nil
		}
		return k.page(pcaADTestParent(in), suffix, next), nil
	}
}

func pcaADHandlers(c *pcaADCalls) map[string]func(in any) (any, error) {
	h := map[string]func(in any) (any, error){"ListTagsForResource": c.listTagsPCAAD}
	for _, k := range pcaADKinds() {
		h[k.op] = c.twoPagesPCAAD(k)
	}
	return h
}

// pcaADScanIn runs the scan's real entry point for acct in region against
// handlers; every op they do not answer is denied.
func pcaADScanIn(ctx context.Context, st *store.Store, acct *account, region string, handlers map[string]func(in any) (any, error)) (int, error) {
	acct.cfg = opStubConfig(handlers, pcaADTestDenied)
	return scanPCAConnectorAD(ctx, acct, region, st, testScanID)
}

func pcaADScan(ctx context.Context, st *store.Store, handlers map[string]func(in any) (any, error)) (int, error) {
	return pcaADScanIn(ctx, st, newTestAccount(testAccountID), testRegion, handlers)
}

func pcaADWarnOps(w []store.ScanWarning) []string {
	var ops []string
	for _, x := range w {
		ops = append(ops, x.Service)
	}
	return ops
}

// failPCAAD makes op fail with err for the calls whose input names parent,
// recording them; the others are answered as before.
func failPCAAD(calls *pcaADCalls, handlers map[string]func(in any) (any, error), op, parent string, err error) {
	answer := handlers[op]
	handlers[op] = func(in any) (any, error) {
		if pcaADTestParent(in) == parent {
			calls.recordPCAAD(op, in)
			return nil, err
		}
		return answer(in)
	}
}

// pcaADChildOps are the per-parent listings.
var pcaADChildOps = []string{"ListServicePrincipalNames", "ListTemplates", "ListTemplateGroupAccessControlEntries"}

// Every listing stores both its pages, for every parent stored this scan,
// each row under its NativeID with its Name and Status, no CreatedAt, its
// attributes the listed element, and, for connectors, directory registrations
// and templates, the tags its tag read returns (nil for none). Service
// principal names and group access control entries have no tags read.
// ListTagsForResource is sent exactly the listed ARNs of the tagged kinds;
// every listing is sent the model's MaxResults cap of 1000. No error, no
// warning.
func TestScanPCAConnectorAD_StoresEveryRowWithItsTags(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	calls := newPCAADCalls(t)

	counted, upserts := countUpserts(st)
	total, err := pcaADScan(context.Background(), counted, pcaADHandlers(calls))
	inserted := upserts()
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != pcaADTestTotal || inserted != pcaADTestTotal {
		t.Errorf("total, inserted = %d, %d; want %d, %d", total, inserted, pcaADTestTotal, pcaADTestTotal)
	}
	if w := warns(); len(w) != 0 {
		t.Errorf("warnings = %v; want none", w)
	}
	var wantTagARNs []string
	for _, k := range pcaADKinds() {
		rows := rowsByNativeID(t, st, k.typ)
		listed := k.listedPCAAD(t)
		if len(rows) != len(listed) {
			t.Errorf("%s rows = %d; want %d", k.typ, len(rows), len(listed))
		}
		for _, l := range listed {
			id := k.native(l.parent, l.suffix)
			r, ok := rows[id]
			if !ok {
				t.Errorf("%s %s missing", k.typ, id)
				continue
			}
			if want := k.name(l.parent, l.suffix); sv(r.Name) != want || sv(r.Region) != testRegion || r.CreatedAt != nil {
				t.Errorf("%s Name, Region, CreatedAt = %q, %q, %v; want %q, %q, nil", id, sv(r.Name), sv(r.Region), r.CreatedAt, want, testRegion)
			}
			if (k.status == "") != (r.Status == nil) || sv(r.Status) != k.status {
				t.Errorf("%s Status = %v; want %q (nil when empty)", id, r.Status, k.status)
			}
			var attrs map[string]any
			field, want := k.attr(l.parent, l.suffix)
			if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil || attrs[field] != want {
				t.Errorf("%s attrs = %s (%v); want the listed element", id, r.AttributesJSON, err)
			}
			switch {
			case k.tagged && l.suffix == "tagged":
				assertTagsMap(t, r.TagsJSON, map[string]string{"type": k.typ})
			case r.TagsJSON != nil:
				t.Errorf("%s TagsJSON = %q; want nil", id, *r.TagsJSON)
			}
			if k.tagged {
				wantTagARNs = append(wantTagARNs, id)
			}
		}
	}
	slices.Sort(wantTagARNs)
	if got := calls.tagARNsPCAAD(); !slices.Equal(got, wantTagARNs) {
		t.Errorf("ListTagsForResource ARNs = %v\nwant %v", got, wantTagARNs)
	}
	for _, op := range pcaADChildOps {
		k := pcaADKindOf(t, op)
		want := slices.Sorted(slices.Values(pcaADKindOf(t, k.parentOp).nativesPCAAD(t)))
		if got := calls.parentsPCAAD(op); !slices.Equal(got, want) {
			t.Errorf("%s parents = %v; want %v", op, got, want)
		}
	}
	for _, k := range pcaADKinds() {
		if calls.calledPCAAD(k.op) == 0 {
			t.Errorf("%s not called", k.op)
		}
		for _, n := range calls.maxResults[k.op] {
			if n != 1000 {
				t.Errorf("%s MaxResults = %d; want 1000", k.op, n)
			}
		}
	}
}

// No connector listed: none stored, no template listed, no error; the
// directory registrations and their service principal names are still
// stored. An empty listing warns nothing; a denied one warns once under its
// op.
func TestScanPCAConnectorAD_NoConnectors(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name      string
		err       error
		wantWarns []string
	}{
		{"empty", nil, nil},
		{"denied", pcaADTestDenied, []string{"pcaconnectorad:ListConnectors"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			calls := newPCAADCalls(t)
			handlers := pcaADHandlers(calls)
			handlers["ListConnectors"] = func(in any) (any, error) {
				calls.recordPCAAD("ListConnectors", in)
				if c.err != nil {
					return nil, c.err
				}
				return &pcaconnectorad.ListConnectorsOutput{}, nil
			}

			total, err := pcaADScan(context.Background(), st, handlers)
			if err != nil {
				t.Fatalf("scan: %v", err)
			}
			if want := 2 + 4; total != want {
				t.Errorf("total = %d; want %d (the directory registrations and their names)", total, want)
			}
			if got := pcaADWarnOps(warns()); !slices.Equal(got, c.wantWarns) {
				t.Errorf("warnings = %v; want %v", got, c.wantWarns)
			}
			for _, op := range []string{"ListTemplates", "ListTemplateGroupAccessControlEntries"} {
				if n := calls.calledPCAAD(op); n != 0 {
					t.Errorf("%s called %d times; want 0", op, n)
				}
			}
			if n := len(rowsByNativeID(t, st, TypePCAConnectorADServicePrincipalName)); n != 4 {
				t.Errorf("service principal names = %d; want 4", n)
			}
		})
	}
}

// Nothing listed anywhere: nothing stored, no child call or tag read, no
// error, no warning.
func TestScanPCAConnectorAD_EmptyListings(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	calls := newPCAADCalls(t)
	handlers := pcaADHandlers(calls)
	handlers["ListConnectors"] = func(any) (any, error) { return &pcaconnectorad.ListConnectorsOutput{}, nil }
	handlers["ListDirectoryRegistrations"] = func(any) (any, error) {
		return &pcaconnectorad.ListDirectoryRegistrationsOutput{}, nil
	}

	counted, upserts := countUpserts(st)
	total, err := pcaADScan(context.Background(), counted, handlers)
	inserted := upserts()
	if err != nil || total != 0 || inserted != 0 {
		t.Fatalf("scan = %d, %d, %v; want 0, 0, nil", total, inserted, err)
	}
	if w := warns(); len(w) != 0 {
		t.Errorf("warnings = %v; want none", w)
	}
	for _, op := range pcaADChildOps {
		if n := calls.calledPCAAD(op); n != 0 {
			t.Errorf("%s called %d times; want 0", op, n)
		}
	}
	if got := calls.tagARNsPCAAD(); len(got) != 0 {
		t.Errorf("ListTagsForResource ARNs = %v; want none", got)
	}
}

// A tag read that finds the resource gone (the modeled
// ResourceNotFoundException) drops its row silently, and a dropped parent has
// no children listed. Any other failure keeps the row untagged: a denial
// warns once under the tag op with the scan's scope; a throttle is transient;
// any other error is returned.
func TestScanPCAConnectorAD_TagReadFailures(t *testing.T) {
	t.Parallel()
	connector := pcaADConnectorARN("tagged")
	dirReg := pcaADDirRegARN("tagged")
	template := pcaADKindOf(t, "ListTemplates").native(pcaADConnectorARN("bare"), "tagged")
	for _, c := range []struct {
		name     string
		arn, typ string
		err      error
		outcome  serviceOutcome // -1: no error
		code     string
		warn     bool
		// lost is the rows not stored: the row dropped and its descendants.
		lost int
	}{
		{"connector/denied", connector, TypePCAConnectorADConnector, pcaADTestDenied, -1, "", true, 0},
		{"connector/transient", connector, TypePCAConnectorADConnector, pcaADTestThrottle, outcomeTransient, "ThrottlingException", false, 0},
		{"connector/other error", connector, TypePCAConnectorADConnector, pcaADTestBad, outcomeError, "ValidationException", false, 0},
		{"connector/gone", connector, TypePCAConnectorADConnector, pcaADTestGone, -1, "", false, 1 + 2 + 4},
		{"directory registration/denied", dirReg, TypePCAConnectorADDirectoryRegistration, pcaADTestDenied, -1, "", true, 0},
		{"directory registration/other error", dirReg, TypePCAConnectorADDirectoryRegistration, pcaADTestBad, outcomeError, "ValidationException", false, 0},
		{"directory registration/gone", dirReg, TypePCAConnectorADDirectoryRegistration, pcaADTestGone, -1, "", false, 1 + 2},
		{"template/denied", template, TypePCAConnectorADTemplate, pcaADTestDenied, -1, "", true, 0},
		{"template/transient", template, TypePCAConnectorADTemplate, pcaADTestThrottle, outcomeTransient, "ThrottlingException", false, 0},
		{"template/gone", template, TypePCAConnectorADTemplate, pcaADTestGone, -1, "", false, 1 + 2},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			calls := newPCAADCalls(t)
			calls.errs[c.arn] = c.err

			total, err := pcaADScan(context.Background(), st, pcaADHandlers(calls))
			if c.outcome < 0 {
				if err != nil {
					t.Fatalf("err = %v; want nil", err)
				}
			} else {
				assertGlueOutcome(t, err, c.outcome)
				assertAPICode(t, err, c.code)
			}
			if want := pcaADTestTotal - c.lost; total != want {
				t.Errorf("total = %d; want %d", total, want)
			}
			r, ok := rowsByNativeID(t, st, c.typ)[c.arn]
			switch {
			case c.lost > 0 && ok:
				t.Fatal("row stored; want it dropped as deleted")
			case c.lost == 0 && !ok:
				t.Fatal("row not stored; want it kept")
			case ok && r.TagsJSON != nil:
				t.Errorf("TagsJSON = %q; want nil", *r.TagsJSON)
			}
			for _, op := range pcaADChildOps {
				if c.lost > 0 && slices.Contains(calls.parentsPCAAD(op), c.arn) {
					t.Errorf("%s called for the dropped %s", op, c.arn)
				}
			}
			w := warns()
			var want []string
			if c.warn {
				want = []string{pcaADTagsOp}
			}
			if got := pcaADWarnOps(w); !slices.Equal(got, want) {
				t.Fatalf("warnings = %v; want %v", w, want)
			}
			for _, x := range w {
				if x.Scope != testAccountID+"/"+testRegion || !strings.Contains(x.Message, c.arn) {
					t.Errorf("warning Scope %q, message %q; want %s/%s naming %s", x.Scope, x.Message, testAccountID, testRegion, c.arn)
				}
			}
		})
	}
}

// Denied tag reads in all three tagged phases send one warning for the whole
// scan; the rows are kept.
func TestScanPCAConnectorAD_TagDenialsWarnOncePerScan(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	calls := newPCAADCalls(t)
	for _, op := range []string{"ListConnectors", "ListDirectoryRegistrations", "ListTemplates"} {
		for _, id := range pcaADKindOf(t, op).nativesPCAAD(t) {
			calls.errs[id] = pcaADTestDenied
		}
	}

	total, err := pcaADScan(context.Background(), st, pcaADHandlers(calls))
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != pcaADTestTotal {
		t.Errorf("total = %d; want %d", total, pcaADTestTotal)
	}
	if got := pcaADWarnOps(warns()); !slices.Equal(got, []string{pcaADTagsOp}) {
		t.Errorf("warnings = %v; want one under %s", got, pcaADTagsOp)
	}
}

// A throttled tag read beside a hard failure of the same op is demoted to
// one warning, so the hard error decides the outcome.
func TestScanPCAConnectorAD_TagThrottleDemotedBesideHardError(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	warns := countOpWarnings(st, pcaADTagsOp)
	calls := newPCAADCalls(t)
	calls.errs[pcaADDirRegARN("bare")] = pcaADTestThrottle
	calls.errs[pcaADKindOf(t, "ListTemplates").nativesPCAAD(t)[3]] = pcaADTestBad

	_, err := pcaADScan(context.Background(), st, pcaADHandlers(calls))
	assertGlueOutcome(t, err, outcomeError)
	assertAPICode(t, err, "ValidationException")
	if *warns != 1 {
		t.Errorf("%s warnings = %d; want 1", pcaADTagsOp, *warns)
	}
}

// A failed write stops the scan: no warning about the rows it lost (a denied
// tag read among them), no later phase runs, and the earlier phase's rows
// stay stored.
func TestScanPCAConnectorAD_FailedWriteStopsScan(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	refuseStoreWrites(t, st, TypePCAConnectorADDirectoryRegistration)
	calls := newPCAADCalls(t)
	calls.errs[pcaADDirRegARN("tagged")] = pcaADTestDenied

	total, err := pcaADScan(context.Background(), st, pcaADHandlers(calls))
	if !errors.Is(err, store.ErrStoreWrite) {
		t.Fatalf("err = %v; want store.ErrStoreWrite", err)
	}
	if w := warns(); len(w) != 0 {
		t.Errorf("warnings = %v; want none", w)
	}
	for _, op := range pcaADChildOps {
		if n := calls.calledPCAAD(op); n != 0 {
			t.Errorf("%s called %d times; want 0 after the failed write", op, n)
		}
	}
	if n := len(rowsByNativeID(t, st, TypePCAConnectorADConnector)); total != 2 || n != 2 {
		t.Errorf("total = %d, connectors stored = %d; want 2, 2", total, n)
	}
}

// A listing's failed later page keeps the pages read before it, and its
// error does not stop the later phases. A parent not stored has no children
// read.
func TestScanPCAConnectorAD_ListFailureKeepsPagesAndLaterPhases(t *testing.T) {
	t.Parallel()
	templates := pcaADKindOf(t, "ListTemplates")
	for _, c := range []struct {
		op, parent string
		kept, lost string
		// wantTotal is the rows stored; wantACEs those of the last phase.
		wantTotal, wantACEs int
	}{
		// The connectors' second page: c-bare, its 2 templates and their 4
		// entries are lost; the directory registrations are still read.
		{"ListConnectors", "", pcaADConnectorARN("tagged"), pcaADConnectorARN("bare"), pcaADTestTotal - 7, 4},
		// One connector's second page of templates: t-bare and its 2 entries
		// are lost.
		{"ListTemplates", pcaADConnectorARN("tagged"), templates.native(pcaADConnectorARN("tagged"), "tagged"), templates.native(pcaADConnectorARN("tagged"), "bare"), pcaADTestTotal - 3, 6},
	} {
		t.Run(c.op, func(t *testing.T) {
			st := newTestStore(t)
			calls := newPCAADCalls(t)
			handlers := pcaADHandlers(calls)
			list := handlers[c.op]
			handlers[c.op] = func(in any) (any, error) {
				if bacField[*string](in, "NextToken") != nil && pcaADTestParent(in) == c.parent {
					calls.recordPCAAD(c.op, in)
					return nil, pcaADTestBad
				}
				return list(in)
			}

			total, err := pcaADScan(context.Background(), st, handlers)
			assertGlueOutcome(t, err, outcomeError)
			assertAPICode(t, err, "ValidationException")
			if total != c.wantTotal {
				t.Errorf("total = %d; want %d", total, c.wantTotal)
			}
			rows := rowsByNativeID(t, st, pcaADKindOf(t, c.op).typ)
			if _, ok := rows[c.kept]; !ok {
				t.Errorf("%s, read before the failure, missing", c.kept)
			}
			if _, ok := rows[c.lost]; ok {
				t.Errorf("%s stored; its page failed", c.lost)
			}
			if got := len(rowsByNativeID(t, st, TypePCAConnectorADTemplateGroupACE)); got != c.wantACEs {
				t.Errorf("template group ACEs (the last phase) = %d; want %d", got, c.wantACEs)
			}
		})
	}
}

// A repeated NextToken ends the listing as an error, keeping its pages; the
// later phases still run.
func TestScanPCAConnectorAD_RepeatedTokenKeepsPages(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	calls := newPCAADCalls(t)
	handlers := pcaADHandlers(calls)
	twoPages := handlers["ListTemplates"]
	handlers["ListTemplates"] = func(in any) (any, error) {
		out, err := twoPages(in)
		out.(*pcaconnectorad.ListTemplatesOutput).NextToken = sdkaws.String("same")
		return out, err
	}

	total, err := pcaADScan(context.Background(), st, handlers)
	if !errors.Is(err, errRepeatedNextToken) {
		t.Fatalf("err = %v; want errRepeatedNextToken", err)
	}
	if total != pcaADTestTotal {
		t.Errorf("total = %d; want %d", total, pcaADTestTotal)
	}
	if n := len(rowsByNativeID(t, st, TypePCAConnectorADTemplate)); n != 4 {
		t.Errorf("templates = %d; want 4", n)
	}
}

// One parent's failed child listing costs the other parents none of their
// rows: a parent deleted since it was listed (the modeled
// ResourceNotFoundException) is skipped silently, a denial warns once under
// the op, and any other error is returned.
func TestScanPCAConnectorAD_PerParentFailures(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		op, parent string
		// lost is the rows the failed parent's listing would have stored,
		// with their descendants.
		lost int
	}{
		{"ListServicePrincipalNames", pcaADDirRegARN("tagged"), 2},
		{"ListTemplates", pcaADConnectorARN("tagged"), 2 + 4},
		{"ListTemplateGroupAccessControlEntries", pcaADKindOf(t, "ListTemplates").native(pcaADConnectorARN("bare"), "tagged"), 2},
	} {
		for _, e := range []struct {
			name      string
			err       error
			code      string
			wantWarns int
		}{
			{"parent gone", pcaADTestGone, "", 0},
			{"denied", pcaADTestDenied, "", 1},
			{"other error", pcaADTestBad, "ValidationException", 0},
		} {
			t.Run(c.op+"/"+e.name, func(t *testing.T) {
				st := newTestStore(t)
				warns := rhWarnRecorder(st)
				calls := newPCAADCalls(t)
				handlers := pcaADHandlers(calls)
				failPCAAD(calls, handlers, c.op, c.parent, e.err)

				total, err := pcaADScan(context.Background(), st, handlers)
				if e.code == "" {
					if err != nil {
						t.Fatalf("err = %v; want nil", err)
					}
				} else {
					assertGlueOutcome(t, err, outcomeError)
					assertAPICode(t, err, e.code)
				}
				if want := pcaADTestTotal - c.lost; total != want {
					t.Errorf("total = %d; want %d", total, want)
				}
				w := warns()
				if len(w) != e.wantWarns || (len(w) == 1 && (w[0].Service != "pcaconnectorad:"+c.op || !strings.Contains(w[0].Message, c.parent))) {
					t.Errorf("warnings = %v; want %d under pcaconnectorad:%s naming %s", w, e.wantWarns, c.op, c.parent)
				}
			})
		}
	}
}

// An element without the field its NativeID is built from is not stored, is
// not sent to the tag op, has no children read, and is warned about once
// under its listing, naming it.
func TestScanPCAConnectorAD_ElementWithoutID(t *testing.T) {
	t.Parallel()
	onlyUnder := func(parent string, in any, out, empty any) any {
		if pcaADTestParent(in) != parent {
			return empty
		}
		return out
	}
	for _, c := range []struct {
		name, op, key string
		answer        func(in any) any
		wantTotal     int
		typ           string
		// childOps are the ops the element's children are read by.
		childOps []string
	}{
		{
			"connector without an ARN", "ListConnectors", "d-x",
			func(any) any {
				return &pcaconnectorad.ListConnectorsOutput{Connectors: []pcaadtypes.ConnectorSummary{{DirectoryId: sdkaws.String("d-x")}}}
			},
			// The directory registrations and their names.
			2 + 4, TypePCAConnectorADConnector,
			[]string{"ListTemplates"},
		},
		{
			"directory registration without an ARN", "ListDirectoryRegistrations", "d-x",
			func(any) any {
				return &pcaconnectorad.ListDirectoryRegistrationsOutput{DirectoryRegistrations: []pcaadtypes.DirectoryRegistrationSummary{{DirectoryId: sdkaws.String("d-x")}}}
			},
			// The connectors, their templates and the templates' entries.
			2 + 4 + 8, TypePCAConnectorADDirectoryRegistration,
			[]string{"ListServicePrincipalNames"},
		},
		{
			"template without an ARN", "ListTemplates", "t-x",
			func(in any) any {
				return onlyUnder(pcaADConnectorARN("tagged"), in,
					&pcaconnectorad.ListTemplatesOutput{Templates: []pcaadtypes.TemplateSummary{{Name: sdkaws.String("t-x")}}},
					&pcaconnectorad.ListTemplatesOutput{})
			},
			// The connectors, directory registrations and their names.
			2 + 2 + 4, TypePCAConnectorADTemplate,
			[]string{"ListTemplateGroupAccessControlEntries"},
		},
		{
			"service principal name without a connector ARN", "ListServicePrincipalNames", pcaADDirRegARN("tagged"),
			func(in any) any {
				return onlyUnder(pcaADDirRegARN("tagged"), in,
					&pcaconnectorad.ListServicePrincipalNamesOutput{ServicePrincipalNames: []pcaadtypes.ServicePrincipalNameSummary{{Status: pcaadtypes.ServicePrincipalNameStatusActive}}},
					&pcaconnectorad.ListServicePrincipalNamesOutput{})
			},
			pcaADTestTotal - 4, TypePCAConnectorADServicePrincipalName, nil,
		},
		{
			"entry without a SID", "ListTemplateGroupAccessControlEntries", "group-x",
			func(any) any {
				return &pcaconnectorad.ListTemplateGroupAccessControlEntriesOutput{AccessControlEntries: []pcaadtypes.AccessControlEntrySummary{{GroupDisplayName: sdkaws.String("group-x")}}}
			},
			pcaADTestTotal - 8, TypePCAConnectorADTemplateGroupACE, nil,
		},
		{
			// With no display name either, the warning names the entry's template.
			"entry without a SID or a display name", "ListTemplateGroupAccessControlEntries", pcaADConnectorARN("tagged") + "/template/t-tagged",
			func(in any) any {
				return onlyUnder(pcaADConnectorARN("tagged")+"/template/t-tagged", in,
					&pcaconnectorad.ListTemplateGroupAccessControlEntriesOutput{AccessControlEntries: []pcaadtypes.AccessControlEntrySummary{{}}},
					&pcaconnectorad.ListTemplateGroupAccessControlEntriesOutput{})
			},
			pcaADTestTotal - 8, TypePCAConnectorADTemplateGroupACE, nil,
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			calls := newPCAADCalls(t)
			handlers := pcaADHandlers(calls)
			handlers[c.op] = func(in any) (any, error) {
				calls.recordPCAAD(c.op, in)
				return c.answer(in), nil
			}

			total, err := pcaADScan(context.Background(), st, handlers)
			if err != nil {
				t.Fatalf("scan: %v", err)
			}
			if total != c.wantTotal {
				t.Errorf("total = %d; want %d", total, c.wantTotal)
			}
			w := warns()
			if len(w) != 1 || w[0].Service != "pcaconnectorad:"+c.op || !strings.Contains(w[0].Message, c.key) {
				t.Fatalf("warnings = %v; want one under pcaconnectorad:%s naming %s", w, c.op, c.key)
			}
			if slices.Contains(calls.tagARNsPCAAD(), "") {
				t.Error("an empty ARN was sent to ListTagsForResource")
			}
			for _, op := range c.childOps {
				if n := calls.calledPCAAD(op); n != 0 {
					t.Errorf("%s calls = %d, parents %v; want none", op, n, calls.parentsPCAAD(op))
				}
			}
			if rows := rowsByNativeID(t, st, c.typ); len(rows) != 0 {
				t.Errorf("%s rows = %v; want none", c.typ, slices.Sorted(maps.Keys(rows)))
			}
		})
	}
}

// In GovCloud and China the listed ARNs reach the tag op unchanged, and the
// children's NativeIDs are built from them.
func TestScanPCAConnectorAD_PartitionARNs(t *testing.T) {
	t.Parallel()
	for _, pf := range partitionFixtures {
		t.Run(pf.partition, func(t *testing.T) {
			connectorARN := partitionARN(pf.partition, "pca-connector-ad", pf.region, "connector/c")
			dirRegARN := partitionARN(pf.partition, "pca-connector-ad", pf.region, "directory-registration/d")
			templateARN := connectorARN + "/template/t"
			st := newTestStore(t)
			calls := newPCAADCalls(t)
			calls.tags[connectorARN] = map[string]string{"env": "prod"}
			handlers := pcaADHandlers(calls)
			handlers["ListConnectors"] = func(any) (any, error) {
				return &pcaconnectorad.ListConnectorsOutput{Connectors: []pcaadtypes.ConnectorSummary{{Arn: sdkaws.String(connectorARN)}}}, nil
			}
			handlers["ListDirectoryRegistrations"] = func(any) (any, error) {
				return &pcaconnectorad.ListDirectoryRegistrationsOutput{DirectoryRegistrations: []pcaadtypes.DirectoryRegistrationSummary{{Arn: sdkaws.String(dirRegARN)}}}, nil
			}
			handlers["ListServicePrincipalNames"] = func(any) (any, error) {
				return &pcaconnectorad.ListServicePrincipalNamesOutput{ServicePrincipalNames: []pcaadtypes.ServicePrincipalNameSummary{{ConnectorArn: sdkaws.String(connectorARN)}}}, nil
			}
			handlers["ListTemplates"] = func(any) (any, error) {
				return &pcaconnectorad.ListTemplatesOutput{Templates: []pcaadtypes.TemplateSummary{{Arn: sdkaws.String(templateARN)}}}, nil
			}
			handlers["ListTemplateGroupAccessControlEntries"] = func(any) (any, error) {
				return &pcaconnectorad.ListTemplateGroupAccessControlEntriesOutput{AccessControlEntries: []pcaadtypes.AccessControlEntrySummary{{GroupSecurityIdentifier: sdkaws.String("S-1")}}}, nil
			}

			if _, err := pcaADScanIn(context.Background(), st, partitionAccount(pf.partition), pf.region, handlers); err != nil {
				t.Fatalf("scan: %v", err)
			}
			want := []string{connectorARN, templateARN, dirRegARN}
			slices.Sort(want)
			if got := calls.tagARNsPCAAD(); !slices.Equal(got, want) {
				t.Fatalf("ListTagsForResource ARNs = %v; want %v", got, want)
			}
			assertTagsMap(t, rowsByNativeID(t, st, TypePCAConnectorADConnector)[connectorARN].TagsJSON, map[string]string{"env": "prod"})
			if _, ok := rowsByNativeID(t, st, TypePCAConnectorADServicePrincipalName)[dirRegARN+"/service-principal-name/"+connectorARN]; !ok {
				t.Error("service principal name not stored under its listed parent ARN")
			}
			if _, ok := rowsByNativeID(t, st, TypePCAConnectorADTemplateGroupACE)[templateARN+"/access-control-entry/S-1"]; !ok {
				t.Error("template group ACE not stored under its listed parent ARN")
			}
		})
	}
}

// Once ctx ends, the stop is one member of the error, although the phase in
// flight saw it in its listing and its tag reads, and the later phases are
// not run.
func TestScanPCAConnectorAD_ContextEndReportedOnce(t *testing.T) {
	t.Parallel()
	st := newTestStore(t)
	calls := newPCAADCalls(t)
	handlers := pcaADHandlers(calls)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	list := handlers["ListDirectoryRegistrations"]
	handlers["ListDirectoryRegistrations"] = func(in any) (any, error) {
		if bacField[*string](in, "NextToken") != nil {
			cancel()
			return nil, context.Canceled
		}
		return list(in)
	}

	_, err := pcaADScan(ctx, st, handlers)
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
	for _, op := range pcaADChildOps {
		if n := calls.calledPCAAD(op); n != 0 {
			t.Errorf("%s called %d times after ctx ended", op, n)
		}
	}
}

// An NXDOMAIN after rows were stored is a failure of this scan, not a region
// gap: the counts are kept. With nothing stored it stays region-unavailable.
func TestScanPCAConnectorAD_NXDOMAIN(t *testing.T) {
	t.Parallel()
	nx := &net.DNSError{Err: "no such host", Name: "pca-connector-ad.us-east-1.amazonaws.com", IsNotFound: true}
	t.Run("last phase after others stored rows", func(t *testing.T) {
		st := newTestStore(t)
		handlers := pcaADHandlers(newPCAADCalls(t))
		handlers["ListTemplateGroupAccessControlEntries"] = func(any) (any, error) { return nil, nx }
		total, err := pcaADScan(context.Background(), st, handlers)
		assertGlueOutcome(t, err, outcomeError)
		if want := pcaADTestTotal - 8; total != want {
			t.Errorf("total = %d; want the %d other rows counted", total, want)
		}
	})
	t.Run("tag read after the listing", func(t *testing.T) {
		st := newTestStore(t)
		calls := newPCAADCalls(t)
		calls.errs[pcaADConnectorARN("bare")] = nx
		_, err := pcaADScan(context.Background(), st, pcaADHandlers(calls))
		assertGlueOutcome(t, err, outcomeError)
	})
	t.Run("nothing stored", func(t *testing.T) {
		st := newTestStore(t)
		warns := rhWarnRecorder(st)
		acct := newTestAccount(testAccountID)
		acct.cfg = opStubConfig(nil, nx)
		_, err := scanPCAConnectorAD(context.Background(), acct, testRegion, st, testScanID)
		assertGlueOutcome(t, err, outcomeUnavailable)
		if w := warns(); len(w) != 0 {
			t.Errorf("warnings = %v; want none", w)
		}
	})
}
