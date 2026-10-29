package aws

import (
	"context"
	"errors"
	"net"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/ssm"
	ssmtypes "github.com/aws/aws-sdk-go-v2/service/ssm/types"
	"github.com/icearp/disco-cli/store"
)

var (
	ssmThrottle = apiErr("ThrottlingException", "Rate exceeded")
	ssmGone     = apiErr("InvalidResourceId", "The resource ID isn't valid")
	ssmBadType  = apiErr("InvalidResourceType", "bad type")
	ssmCreated  = time.Date(2026, 9, 1, 8, 0, 0, 0, time.UTC)
)

const ssmCreatedText = "2026-09-01T08:00:00Z"

// ssmListing is one account-wide listing of the fixture: its op, the type of
// its rows, the tag type its rows are read as ("" when no tag op reads
// them), and, for its element x ("x1" or "x2"), the page holding it, the
// NativeID of its row (in c's partition and region), the id the tag op is
// sent, the row's stored Name, Status (none when status is nil) and CreatedAt
// ("" for none), and a field its stored attributes hold.
type ssmListing struct {
	op, typ, tagType string
	maxResults       int32
	out              func(c *ssmCalls, next *string, x string) any
	nativeID         func(c *ssmCalls, x string) string
	tagID            func(x string) string
	name             func(x string) string
	status           func(x string) string // nil for no Status
	created          string
	attrs            func(x string) string
}

func ssmSuffix(prefix string) func(string) string {
	return func(x string) string { return prefix + x }
}

func ssmSame(s string) func(string) string {
	return func(string) string { return s }
}

var ssmListings = []ssmListing{
	{
		op: "DescribeParameters", typ: TypeSSMParameter, tagType: "Parameter", maxResults: 50,
		out: func(_ *ssmCalls, next *string, x string) any {
			return &ssm.DescribeParametersOutput{NextToken: next, Parameters: []ssmtypes.ParameterMetadata{{
				Name: sdkaws.String("/app/param-" + x), Type: ssmtypes.ParameterTypeSecureString, KeyId: sdkaws.String("alias/k-" + x),
			}}}
		},
		nativeID: func(c *ssmCalls, x string) string { return c.arn("parameter/app/param-" + x) },
		tagID:    ssmSuffix("/app/param-"), name: ssmSuffix("/app/param-"), status: ssmSame("SecureString"),
		attrs: func(x string) string { return `"KeyId":"alias/k-` + x + `"` },
	},
	{
		op: "ListDocuments", typ: TypeSSMDocument, maxResults: 50,
		out: func(_ *ssmCalls, next *string, x string) any {
			d := ssmtypes.DocumentIdentifier{Name: sdkaws.String("document-" + x), DocumentType: ssmtypes.DocumentTypeCommand, CreatedDate: &ssmCreated}
			if x == "x1" {
				d.Tags = []ssmtypes.Tag{{Key: sdkaws.String("type"), Value: sdkaws.String(TypeSSMDocument)}}
			}
			return &ssm.ListDocumentsOutput{NextToken: next, DocumentIdentifiers: []ssmtypes.DocumentIdentifier{d}}
		},
		nativeID: func(c *ssmCalls, x string) string { return c.arn("document/document-" + x) },
		name:     ssmSuffix("document-"), status: ssmSame("Command"), created: ssmCreatedText,
		// The row's attributes are its DescribeDocument body.
		attrs: func(x string) string { return `"Description":"` + ssmDocumentDescription("document-"+x) + `"` },
	},
	{
		op: "DescribePatchBaselines", typ: TypeSSMPatchBaseline, tagType: "PatchBaseline", maxResults: 100,
		out: func(_ *ssmCalls, next *string, x string) any {
			return &ssm.DescribePatchBaselinesOutput{NextToken: next, BaselineIdentities: []ssmtypes.PatchBaselineIdentity{{
				BaselineId: sdkaws.String("pb-" + x), BaselineName: sdkaws.String("baseline-" + x), OperatingSystem: ssmtypes.OperatingSystemWindows,
			}}}
		},
		nativeID: func(c *ssmCalls, x string) string { return c.arn("patchbaseline/pb-" + x) },
		tagID:    ssmSuffix("pb-"), name: ssmSuffix("baseline-"),
		attrs: ssmSame(`"OperatingSystem":"WINDOWS"`),
	},
	{
		op: "ListAssociations", typ: TypeSSMAssociation, tagType: "Association", maxResults: 50,
		out: func(_ *ssmCalls, next *string, x string) any {
			a := ssmtypes.Association{AssociationId: sdkaws.String("assoc-" + x), Name: sdkaws.String("AWS-RunPatchBaseline")}
			if x == "x1" {
				a.AssociationName = sdkaws.String("nightly-" + x)
			}
			return &ssm.ListAssociationsOutput{NextToken: next, Associations: []ssmtypes.Association{a}}
		},
		nativeID: func(c *ssmCalls, x string) string { return c.arn("association/assoc-" + x) },
		tagID:    ssmSuffix("assoc-"),
		// x2 has no AssociationName, so its id is its Name.
		name:  func(x string) string { return pick(x == "x1", "nightly-x1", "assoc-x2") },
		attrs: ssmSame(`"Name":"AWS-RunPatchBaseline"`),
	},
	{
		op: "DescribeMaintenanceWindows", typ: TypeSSMMaintenanceWindow, tagType: "MaintenanceWindow", maxResults: 100,
		out: func(_ *ssmCalls, next *string, x string) any {
			return &ssm.DescribeMaintenanceWindowsOutput{NextToken: next, WindowIdentities: []ssmtypes.MaintenanceWindowIdentity{{
				WindowId: sdkaws.String("mw-" + x), Name: sdkaws.String("window-" + x), Duration: sdkaws.Int32(3),
			}}}
		},
		nativeID: func(c *ssmCalls, x string) string { return c.arn("maintenancewindow/mw-" + x) },
		tagID:    ssmSuffix("mw-"), name: ssmSuffix("window-"),
		attrs: ssmSame(`"Duration":3`),
	},
	{
		op: "ListResourceDataSync", typ: TypeSSMResourceDataSync, maxResults: 50,
		out: func(_ *ssmCalls, next *string, x string) any {
			return &ssm.ListResourceDataSyncOutput{NextToken: next, ResourceDataSyncItems: []ssmtypes.ResourceDataSyncItem{{
				SyncName: sdkaws.String("sync-" + x), SyncType: sdkaws.String("SyncToDestination"),
			}}}
		},
		nativeID: func(c *ssmCalls, x string) string { return c.arn("resource-data-sync/sync-" + x) },
		name:     ssmSuffix("sync-"),
		attrs:    ssmSame(`"SyncType":"SyncToDestination"`),
	},
	{
		op: "DescribeInstanceInformation", typ: TypeSSMManagedInstance, tagType: "ManagedInstance", maxResults: 50,
		out: func(_ *ssmCalls, next *string, x string) any {
			in := ssmtypes.InstanceInformation{InstanceId: sdkaws.String("mi-" + x), PingStatus: ssmtypes.PingStatusOnline}
			if x == "x1" {
				in.ComputerName = sdkaws.String("host-" + x)
			}
			return &ssm.DescribeInstanceInformationOutput{NextToken: next, InstanceInformationList: []ssmtypes.InstanceInformation{in}}
		},
		nativeID: func(c *ssmCalls, x string) string { return c.arn("managed-instance/mi-" + x) },
		tagID:    ssmSuffix("mi-"),
		name:     func(x string) string { return pick(x == "x1", "host-x1", "mi-x2") }, status: ssmSame("Online"),
		attrs: ssmSame(`"PingStatus":"Online"`),
	},
	{
		op: "ListOpsMetadata", typ: TypeSSMOpsMetadata, tagType: "OpsMetadata", maxResults: 50,
		out: func(c *ssmCalls, next *string, x string) any {
			return &ssm.ListOpsMetadataOutput{NextToken: next, OpsMetadataList: []ssmtypes.OpsMetadata{{
				OpsMetadataArn: sdkaws.String(c.arn("opsmetadata/aws/ssm/app-" + x + "/appmanager")), ResourceId: sdkaws.String("app-" + x),
			}}}
		},
		nativeID: func(c *ssmCalls, x string) string { return c.arn("opsmetadata/aws/ssm/app-" + x + "/appmanager") },
		tagID:    func(x string) string { return "aws/ssm/app-" + x + "/appmanager" }, name: ssmSuffix("app-"),
		attrs: func(x string) string { return `"ResourceId":"app-` + x + `"` },
	},
	{
		op: "DescribeActivations", typ: TypeSSMActivation, maxResults: 50,
		out: func(_ *ssmCalls, next *string, x string) any {
			a := ssmtypes.Activation{ActivationId: sdkaws.String("act-" + x), CreatedDate: &ssmCreated, IamRole: sdkaws.String("SSMServiceRole")}
			if x == "x1" {
				a.Tags = []ssmtypes.Tag{{Key: sdkaws.String("type"), Value: sdkaws.String(TypeSSMActivation)}}
			} else {
				a.Expired = true
			}
			return &ssm.DescribeActivationsOutput{NextToken: next, ActivationList: []ssmtypes.Activation{a}}
		},
		nativeID: func(c *ssmCalls, x string) string { return c.arn("activation/act-" + x) },
		name:     ssmSuffix("act-"), created: ssmCreatedText,
		status: func(x string) string { return pick(x == "x1", "active", "expired") },
		attrs:  ssmSame(`"IamRole":"SSMServiceRole"`),
	},
	{
		op: "ListCloudConnectors", typ: TypeSSMCloudConnector, tagType: "CloudConnector", maxResults: 10,
		out: func(_ *ssmCalls, next *string, x string) any {
			cc := ssmtypes.CloudConnectorSummary{CloudConnectorId: sdkaws.String("cc-" + x), CreatedAt: &ssmCreated, RoleArn: sdkaws.String("role-" + x)}
			if x == "x1" {
				cc.DisplayName = sdkaws.String("azure-" + x)
			}
			return &ssm.ListCloudConnectorsOutput{NextToken: next, CloudConnectors: []ssmtypes.CloudConnectorSummary{cc}}
		},
		nativeID: func(c *ssmCalls, x string) string { return c.arn("cloud-connector/cc-" + x) },
		tagID:    ssmSuffix("cc-"),
		name:     func(x string) string { return pick(x == "x1", "azure-x1", "cc-x2") }, created: ssmCreatedText,
		attrs: func(x string) string { return `"RoleArn":"role-` + x + `"` },
	},
}

// ssmTestTotal is every listing's two rows, one target and one task per
// window and one sharing permission per document.
const ssmTestTotal = 10*2 + 2 + 2 + 2

func ssmListingOf(t *testing.T, op string) ssmListing {
	t.Helper()
	for _, l := range ssmListings {
		if l.op == op {
			return l
		}
	}
	t.Fatalf("%s is not a fixture listing", op)
	return ssmListing{}
}

// ssmTagKey names a tag read: the resource type and id it was sent.
func ssmTagKey(typ, id string) string { return typ + "/" + id }

// ssmCalls records a scan's calls and answers its tag reads, Describes and
// window listings with tags or errors.
type ssmCalls struct {
	mu                sync.Mutex
	partition, region string
	inputs            map[string][]any
	tagReads          []string
	tags              map[string]map[string]string // by ssmTagKey
	tagErrs           map[string]error             // by ssmTagKey
	describeErrs      map[string]error             // DescribeDocument, by name
	windowErrs        map[string]error             // window children, by op+" "+window id
}

// newSSMCalls tags each tag-read listing's x1 element with its type.
func newSSMCalls() *ssmCalls {
	c := &ssmCalls{
		partition: "aws", region: testRegion,
		inputs: map[string][]any{}, tags: map[string]map[string]string{}, tagErrs: map[string]error{},
		describeErrs: map[string]error{}, windowErrs: map[string]error{},
	}
	for _, l := range ssmListings {
		if l.tagType != "" {
			c.tags[ssmTagKey(l.tagType, l.tagID("x1"))] = map[string]string{"type": l.typ}
		}
	}
	return c
}

func (c *ssmCalls) arn(resource string) string {
	return "arn:" + c.partition + ":ssm:" + c.region + ":" + testAccountID + ":" + resource
}

func (c *ssmCalls) record(op string, in any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.inputs[op] = append(c.inputs[op], in)
}

func (c *ssmCalls) count(op string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.inputs[op])
}

func (c *ssmCalls) calls(op string) []any {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Clone(c.inputs[op])
}

func (c *ssmCalls) readKeys() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Sorted(slices.Values(c.tagReads))
}

// pages answers l's listing: the first page holds element x1 and names a
// next page, which holds x2 and ends the listing.
func (c *ssmCalls) pages(l ssmListing) func(any) (any, error) {
	return func(in any) (any, error) {
		c.record(l.op, in)
		first := bacField[*string](in, "NextToken") == nil
		var next *string
		if first {
			next = sdkaws.String("p2")
		}
		return l.out(c, next, pick(first, "x1", "x2")), nil
	}
}

func (c *ssmCalls) listTags(in any) (any, error) {
	li := in.(*ssm.ListTagsForResourceInput)
	key := ssmTagKey(string(li.ResourceType), sv(li.ResourceId))
	c.mu.Lock()
	defer c.mu.Unlock()
	c.tagReads = append(c.tagReads, key)
	if err := c.tagErrs[key]; err != nil {
		return nil, err
	}
	var tl []ssmtypes.Tag
	for k, v := range c.tags[key] {
		tl = append(tl, ssmtypes.Tag{Key: sdkaws.String(k), Value: sdkaws.String(v)})
	}
	return &ssm.ListTagsForResourceOutput{TagList: tl}, nil
}

// ssmDocumentDescription is the only text a stored document row carries
// when its attributes are its DescribeDocument body.
func ssmDocumentDescription(name string) string { return "body of " + name }

func (c *ssmCalls) describeDocument(in any) (any, error) {
	c.record("DescribeDocument", in)
	name := sv(in.(*ssm.DescribeDocumentInput).Name)
	c.mu.Lock()
	err := c.describeErrs[name]
	c.mu.Unlock()
	if err != nil {
		return nil, err
	}
	return &ssm.DescribeDocumentOutput{Document: &ssmtypes.DocumentDescription{Name: &name, Description: sdkaws.String(ssmDocumentDescription(name))}}, nil
}

// windowChild answers op for a window with its one child, named
// <prefix><window id>, or with the window's error.
func (c *ssmCalls) windowChild(op string, out func(wid string) any) func(any) (any, error) {
	return func(in any) (any, error) {
		c.record(op, in)
		wid := sv(bacField[*string](in, "WindowId"))
		c.mu.Lock()
		err := c.windowErrs[op+" "+wid]
		c.mu.Unlock()
		if err != nil {
			return nil, err
		}
		return out(wid), nil
	}
}

func ssmHandlers(c *ssmCalls) map[string]func(in any) (any, error) {
	h := map[string]func(in any) (any, error){
		"ListTagsForResource": c.listTags,
		"DescribeDocument":    c.describeDocument,
		"DescribeMaintenanceWindowTargets": c.windowChild("DescribeMaintenanceWindowTargets", func(wid string) any {
			return &ssm.DescribeMaintenanceWindowTargetsOutput{Targets: []ssmtypes.MaintenanceWindowTarget{{
				WindowId: &wid, WindowTargetId: sdkaws.String("wt-" + wid), ResourceType: ssmtypes.MaintenanceWindowResourceTypeInstance,
			}}}
		}),
		"DescribeMaintenanceWindowTasks": c.windowChild("DescribeMaintenanceWindowTasks", func(wid string) any {
			return &ssm.DescribeMaintenanceWindowTasksOutput{Tasks: []ssmtypes.MaintenanceWindowTask{{
				WindowId: &wid, WindowTaskId: sdkaws.String("wtk-" + wid), Name: sdkaws.String("patch-" + wid),
			}}}
		}),
		"DescribeDocumentPermission": func(in any) (any, error) {
			c.record("DescribeDocumentPermission", in)
			return &ssm.DescribeDocumentPermissionOutput{}, nil
		},
	}
	for _, l := range ssmListings {
		h[l.op] = c.pages(l)
	}
	return h
}

// ssmTaggedReadKeys is every tag read the fixture's scan sends.
func ssmTaggedReadKeys() []string {
	var keys []string
	for _, l := range ssmListings {
		if l.tagType != "" {
			keys = append(keys, ssmTagKey(l.tagType, l.tagID("x1")), ssmTagKey(l.tagType, l.tagID("x2")))
		}
	}
	slices.Sort(keys)
	return keys
}

// ssmScan runs the scan's real entry point against handlers; every op they
// do not answer is denied.
func ssmScan(ctx context.Context, st *store.Store, handlers map[string]func(in any) (any, error)) (int, int, error) {
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(handlers, ssmDenied)
	return scanSSM(ctx, acct, testRegion, st, testScanID)
}

func ssmWarningOps(w []store.ScanWarning) []string {
	var ops []string
	for _, x := range w {
		ops = append(ops, x.Service)
	}
	return ops
}

// Every listing stores both its pages, each row with its NativeID, listed
// name, region, status, creation time and attributes. Parameters, patch
// baselines, associations, maintenance windows, on-premises managed nodes,
// OpsMetadata objects and cloud connectors have their tags read with
// ListTagsForResource, sent exactly each row's type and id; documents and
// activations take theirs from the listed element; nil for a resource
// without tags. Documents store their DescribeDocument body. Each window's
// target and task, with its region and attributes, and each document's
// permission are stored. Every listing page is sent its op's MaxResults cap;
// documents and patch baselines are listed Owner=Self. No error, no warning.
func TestScanSSM_StoresTagsOfEveryType(t *testing.T) {
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	calls := newSSMCalls()

	total, inserted, err := ssmScan(context.Background(), st, ssmHandlers(calls))
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != ssmTestTotal || inserted != ssmTestTotal {
		t.Errorf("total, inserted = %d, %d; want %d, %d", total, inserted, ssmTestTotal, ssmTestTotal)
	}
	if w := warns(); len(w) != 0 {
		t.Errorf("warnings = %v; want none", w)
	}
	for _, l := range ssmListings {
		rows := rowsByNativeID(t, st, l.typ)
		if len(rows) != 2 {
			t.Errorf("%s rows = %d; want 2", l.typ, len(rows))
		}
		for _, x := range []string{"x1", "x2"} {
			id := l.nativeID(calls, x)
			r, ok := rows[id]
			if !ok {
				t.Errorf("%s %s missing", l.typ, id)
				continue
			}
			var status *string
			if l.status != nil {
				status = sdkaws.String(l.status(x))
			}
			if sv(r.Name) != l.name(x) || sv(r.Region) != testRegion || (r.Status == nil) != (status == nil) || sv(r.Status) != sv(status) || sv(r.CreatedAt) != l.created {
				t.Errorf("%s name, region, status, created = %q, %q, %v, %q; want %q, %q, %v, %q",
					id, sv(r.Name), sv(r.Region), r.Status, sv(r.CreatedAt), l.name(x), testRegion, status, l.created)
			}
			if !strings.Contains(r.AttributesJSON, l.attrs(x)) {
				t.Errorf("%s attrs = %s; want %s", id, r.AttributesJSON, l.attrs(x))
			}
			if x == "x1" && l.typ != TypeSSMResourceDataSync {
				assertTagsMap(t, r.TagsJSON, map[string]string{"type": l.typ})
			} else if r.TagsJSON != nil {
				t.Errorf("%s TagsJSON = %q; want nil", id, *r.TagsJSON)
			}
		}
		for i, in := range calls.calls(l.op) {
			if got := sdkaws.ToInt32(bacField[*int32](in, "MaxResults")); got != l.maxResults {
				t.Errorf("%s call %d MaxResults = %d; want %d", l.op, i, got, l.maxResults)
			}
		}
	}
	if got, want := calls.readKeys(), ssmTaggedReadKeys(); !slices.Equal(got, want) {
		t.Errorf("ListTagsForResource reads = %v\nwant %v", got, want)
	}
	for _, in := range calls.calls("ListDocuments") {
		f := in.(*ssm.ListDocumentsInput).Filters
		if len(f) != 1 || sv(f[0].Key) != "Owner" || !slices.Equal(f[0].Values, []string{"Self"}) {
			t.Errorf("ListDocuments filters = %+v; want Owner=Self only", f)
		}
	}
	for _, in := range calls.calls("DescribePatchBaselines") {
		f := in.(*ssm.DescribePatchBaselinesInput).Filters
		if len(f) != 1 || sv(f[0].Key) != "OWNER" || !slices.Equal(f[0].Values, []string{"Self"}) {
			t.Errorf("DescribePatchBaselines filters = %+v; want OWNER=Self only", f)
		}
	}
	for _, c := range []struct{ op, typ, prefix, kind, name string }{
		{"DescribeMaintenanceWindowTargets", TypeSSMMaintenanceWindowTarget, "wt-", "windowtarget", "wt-"},
		{"DescribeMaintenanceWindowTasks", TypeSSMMaintenanceWindowTask, "wtk-", "windowtask", "patch-"},
	} {
		rows := rowsByNativeID(t, st, c.typ)
		for _, wid := range []string{"mw-x1", "mw-x2"} {
			id := calls.arn(c.kind + "/" + wid + "/" + c.prefix + wid)
			attrs := `"WindowId":"` + wid + `"`
			if r, ok := rows[id]; !ok || sv(r.Name) != c.name+wid || sv(r.Region) != testRegion || !strings.Contains(r.AttributesJSON, attrs) || r.TagsJSON != nil {
				t.Errorf("%s %s = %+v (stored %v); want Name %s%s, Region %s, attrs holding %s, no tags", c.typ, id, r, ok, c.name, wid, testRegion, attrs)
			}
		}
		var listed []string
		for _, in := range calls.calls(c.op) {
			if n := sdkaws.ToInt32(bacField[*int32](in, "MaxResults")); n != 100 {
				t.Errorf("%s MaxResults = %d; want 100", c.op, n)
			}
			listed = append(listed, sv(bacField[*string](in, "WindowId")))
		}
		if slices.Sort(listed); !slices.Equal(listed, []string{"mw-x1", "mw-x2"}) {
			t.Errorf("%s windows = %v; want [mw-x1 mw-x2]", c.op, listed)
		}
	}
	if got := len(rowsByNativeID(t, st, TypeSSMDocumentPermission)); got != 2 {
		t.Errorf("document permissions = %d; want 2", got)
	}
}

// Empty listings store nothing and warn about nothing: no tag read,
// Describe or per-parent listing is sent, and each listing is asked once.
func TestScanSSM_EmptyListings(t *testing.T) {
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	calls := newSSMCalls()
	handlers := ssmHandlers(calls)
	for _, l := range ssmListings {
		out := l.out(calls, nil, "x1")
		handlers[l.op] = func(in any) (any, error) {
			calls.record(l.op, in)
			return ssmEmptied(t, out), nil
		}
	}

	total, inserted, err := ssmScan(context.Background(), st, handlers)
	if err != nil || total != 0 || inserted != 0 {
		t.Fatalf("scan = %d, %d, %v; want 0, 0, nil", total, inserted, err)
	}
	if w := warns(); len(w) != 0 {
		t.Errorf("warnings = %v; want none", w)
	}
	for _, l := range ssmListings {
		if n := calls.count(l.op); n != 1 {
			t.Errorf("%s called %d times; want 1", l.op, n)
		}
	}
	for _, op := range []string{"DescribeDocument", "DescribeMaintenanceWindowTargets", "DescribeMaintenanceWindowTasks", "DescribeDocumentPermission"} {
		if n := calls.count(op); n != 0 {
			t.Errorf("%s called %d times; want 0", op, n)
		}
	}
	if got := calls.readKeys(); len(got) != 0 {
		t.Errorf("ListTagsForResource reads = %v; want none", got)
	}
}

// ssmEmptied is a listing output with its element list emptied.
func ssmEmptied(t *testing.T, out any) any {
	t.Helper()
	switch o := out.(type) {
	case *ssm.DescribeParametersOutput:
		o.Parameters = nil
	case *ssm.ListDocumentsOutput:
		o.DocumentIdentifiers = nil
	case *ssm.DescribePatchBaselinesOutput:
		o.BaselineIdentities = nil
	case *ssm.ListAssociationsOutput:
		o.Associations = nil
	case *ssm.DescribeMaintenanceWindowsOutput:
		o.WindowIdentities = nil
	case *ssm.ListResourceDataSyncOutput:
		o.ResourceDataSyncItems = nil
	case *ssm.DescribeInstanceInformationOutput:
		o.InstanceInformationList = nil
	case *ssm.ListOpsMetadataOutput:
		o.OpsMetadataList = nil
	case *ssm.DescribeActivationsOutput:
		o.ActivationList = nil
	case *ssm.ListCloudConnectorsOutput:
		o.CloudConnectors = nil
	default:
		t.Fatalf("no emptied form for %T", out)
	}
	return out
}

// Parameters, which an account can hold by the ten thousand, are written a
// page at a time: the first page's row is stored, its tags read, before the
// second page is asked for.
func TestScanSSM_ParametersWrittenPerPage(t *testing.T) {
	st := newTestStore(t)
	calls := newSSMCalls()
	handlers := ssmHandlers(calls)
	list := handlers["DescribeParameters"]
	first := calls.arn("parameter/app/param-x1")
	var stored bool
	var tags *string
	handlers["DescribeParameters"] = func(in any) (any, error) {
		if bacField[*string](in, "NextToken") != nil {
			var r store.Resource
			r, stored = rowsByNativeID(t, st, TypeSSMParameter)[first]
			tags = r.TagsJSON
		}
		return list(in)
	}

	if _, _, err := ssmScan(context.Background(), st, handlers); err != nil {
		t.Fatalf("scan: %v", err)
	}
	if !stored {
		t.Fatalf("%s not stored when the second page was asked for", first)
	}
	assertTagsMap(t, tags, map[string]string{"type": TypeSSMParameter})
}

// A rescan stores the same rows without inserting any.
func TestScanSSM_RescanInsertsNone(t *testing.T) {
	st := newTestStore(t)
	if _, _, err := ssmScan(context.Background(), st, ssmHandlers(newSSMCalls())); err != nil {
		t.Fatalf("first scan: %v", err)
	}
	total, inserted, err := ssmScan(context.Background(), st, ssmHandlers(newSSMCalls()))
	if err != nil || total != ssmTestTotal || inserted != 0 {
		t.Errorf("rescan = %d, %d, %v; want %d, 0, nil", total, inserted, err, ssmTestTotal)
	}
}

// A failed tag read. InvalidResourceId, the op's modeled code for an id it
// cannot find, drops the row silently. A denial keeps the row untagged and
// warns once under the tag op; a throttle keeps it and is transient; any
// other error keeps it and is an error.
func TestScanSSM_TagReadFailures(t *testing.T) {
	for _, op := range []string{"DescribeParameters", "DescribeMaintenanceWindows", "ListOpsMetadata"} {
		for _, c := range []struct {
			name    string
			err     error
			outcome serviceOutcome // -1: no error
			code    string
			lost    int
			warnOp  string
		}{
			{"not found", ssmGone, -1, "", 1, ""},
			{"denied", ssmDenied, -1, "", 0, ssmTagsOp},
			{"transient", ssmThrottle, outcomeTransient, "ThrottlingException", 0, ""},
			{"other error", ssmBadType, outcomeError, "InvalidResourceType", 0, ""},
		} {
			t.Run(op+"/"+c.name, func(t *testing.T) {
				st := newTestStore(t)
				warns := rhWarnRecorder(st)
				calls := newSSMCalls()
				l := ssmListingOf(t, op)
				id := l.nativeID(calls, "x1")
				calls.tagErrs[ssmTagKey(l.tagType, l.tagID("x1"))] = c.err
				lost := c.lost
				if op == "DescribeMaintenanceWindows" {
					// A window dropped as deleted has no target or task listed.
					lost *= 3
				}

				total, _, err := ssmScan(context.Background(), st, ssmHandlers(calls))
				if c.outcome < 0 {
					if err != nil {
						t.Fatalf("err = %v; want nil", err)
					}
				} else {
					assertGlueOutcome(t, err, c.outcome)
					assertAPICode(t, err, c.code)
				}
				if want := ssmTestTotal - lost; total != want {
					t.Errorf("total = %d; want %d", total, want)
				}
				r, ok := rowsByNativeID(t, st, l.typ)[id]
				if ok != (c.lost == 0) {
					t.Fatalf("row stored = %v; want %v", ok, c.lost == 0)
				}
				if ok && r.TagsJSON != nil {
					t.Errorf("TagsJSON = %q; want nil", *r.TagsJSON)
				}
				w := warns()
				var want []string
				if c.warnOp != "" {
					want = []string{c.warnOp}
				}
				if got := ssmWarningOps(w); !slices.Equal(got, want) {
					t.Fatalf("warnings = %v; want %v", w, want)
				}
				for _, x := range w {
					if x.Scope != testAccountID+"/"+testRegion || !strings.Contains(x.Message, id) {
						t.Errorf("warning Scope %q, message %q; want %s/%s naming %s", x.Scope, x.Message, testAccountID, testRegion, id)
					}
				}
			})
		}
	}
}

// InvalidResourceId drops a row as deleted only for the types whose id form
// AddTagsToResource documents. For associations and cloud connectors it may
// instead mean the id form sent is wrong, so the row is kept untagged and the
// code is returned.
func TestScanSSM_InvalidResourceIdDropsOnlyDocumentedIDForms(t *testing.T) {
	for _, c := range []struct {
		op      string
		dropped bool
	}{
		{"DescribeParameters", true},
		{"DescribePatchBaselines", true},
		{"ListAssociations", false},
		{"DescribeMaintenanceWindows", true},
		{"DescribeInstanceInformation", true},
		{"ListOpsMetadata", true},
		{"ListCloudConnectors", false},
	} {
		t.Run(c.op, func(t *testing.T) {
			st := newTestStore(t)
			calls := newSSMCalls()
			l := ssmListingOf(t, c.op)
			calls.tagErrs[ssmTagKey(l.tagType, l.tagID("x1"))] = ssmGone

			_, _, err := ssmScan(context.Background(), st, ssmHandlers(calls))
			if c.dropped {
				if err != nil {
					t.Fatalf("err = %v; want nil", err)
				}
			} else {
				assertGlueOutcome(t, err, outcomeError)
				assertAPICode(t, err, "InvalidResourceId")
			}
			r, ok := rowsByNativeID(t, st, l.typ)[l.nativeID(calls, "x1")]
			if ok == c.dropped {
				t.Fatalf("row stored = %v; want %v", ok, !c.dropped)
			}
			if ok && r.TagsJSON != nil {
				t.Errorf("TagsJSON = %q; want nil", *r.TagsJSON)
			}
		})
	}
}

// DescribeInstanceInformation lists EC2 instances beside on-premises nodes.
// An EC2 instance's row is stored untagged and its id is never sent to the
// tag op; the on-premises node beside it is read as a ManagedInstance.
func TestScanSSM_EC2ManagedNodeNotTagRead(t *testing.T) {
	st := newTestStore(t)
	calls := newSSMCalls()
	handlers := ssmHandlers(calls)
	handlers["DescribeInstanceInformation"] = func(in any) (any, error) {
		calls.record("DescribeInstanceInformation", in)
		return &ssm.DescribeInstanceInformationOutput{InstanceInformationList: []ssmtypes.InstanceInformation{
			{InstanceId: sdkaws.String("mi-x1"), ResourceType: ssmtypes.ResourceTypeManagedInstance},
			{InstanceId: sdkaws.String("i-0ec2"), ResourceType: ssmtypes.ResourceTypeEc2Instance},
		}}, nil
	}

	if _, _, err := ssmScan(context.Background(), st, handlers); err != nil {
		t.Fatalf("scan: %v", err)
	}
	var reads []string
	for _, k := range calls.readKeys() {
		if strings.HasPrefix(k, "ManagedInstance/") {
			reads = append(reads, k)
		}
	}
	if want := []string{ssmTagKey("ManagedInstance", "mi-x1")}; !slices.Equal(reads, want) {
		t.Errorf("ManagedInstance tag reads = %v; want %v", reads, want)
	}
	rows := rowsByNativeID(t, st, TypeSSMManagedInstance)
	ec2 := calls.arn("managed-instance/i-0ec2")
	if r, ok := rows[ec2]; !ok || r.TagsJSON != nil {
		t.Errorf("row %s stored = %v, tags %v; want stored untagged", ec2, ok, r.TagsJSON)
	}
	assertTagsMap(t, rows[calls.arn("managed-instance/mi-x1")].TagsJSON, map[string]string{"type": TypeSSMManagedInstance})
}

// A DescribeDocument that answers without a document keeps the list element
// as the row's attributes.
func TestScanSSM_DescribeDocumentWithoutBody(t *testing.T) {
	st := newTestStore(t)
	calls := newSSMCalls()
	handlers := ssmHandlers(calls)
	handlers["DescribeDocument"] = func(any) (any, error) { return &ssm.DescribeDocumentOutput{}, nil }

	if _, _, err := ssmScan(context.Background(), st, handlers); err != nil {
		t.Fatalf("scan: %v", err)
	}
	id := calls.arn("document/document-x1")
	if r, ok := rowsByNativeID(t, st, TypeSSMDocument)[id]; !ok || !strings.Contains(r.AttributesJSON, `"DocumentType":"Command"`) {
		t.Errorf("%s stored = %v, attrs %s; want the list element", id, ok, r.AttributesJSON)
	}
}

// Denied tag reads in several phases send one warning for the whole scan;
// the rows are kept.
func TestScanSSM_TagDenialsWarnOncePerScan(t *testing.T) {
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	calls := newSSMCalls()
	for _, op := range []string{"DescribeParameters", "ListAssociations", "ListCloudConnectors"} {
		l := ssmListingOf(t, op)
		calls.tagErrs[ssmTagKey(l.tagType, l.tagID("x2"))] = ssmDenied
	}

	total, _, err := ssmScan(context.Background(), st, ssmHandlers(calls))
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != ssmTestTotal {
		t.Errorf("total = %d; want %d", total, ssmTestTotal)
	}
	if got := ssmWarningOps(warns()); !slices.Equal(got, []string{ssmTagsOp}) {
		t.Errorf("warnings = %v; want one under %s", got, ssmTagsOp)
	}
}

// A throttled tag read beside a hard failure of the same op is demoted to
// one warning, so the hard error decides the outcome.
func TestScanSSM_TagThrottleDemotedBesideHardError(t *testing.T) {
	st := newTestStore(t)
	warns := countOpWarnings(st, ssmTagsOp)
	calls := newSSMCalls()
	calls.tagErrs[ssmTagKey("PatchBaseline", "pb-x1")] = ssmThrottle
	calls.tagErrs[ssmTagKey("Association", "assoc-x2")] = ssmBadType

	_, _, err := ssmScan(context.Background(), st, ssmHandlers(calls))
	assertGlueOutcome(t, err, outcomeError)
	assertAPICode(t, err, "InvalidResourceType")
	if *warns != 1 {
		t.Errorf("%s warnings = %d; want 1", ssmTagsOp, *warns)
	}
}

// An OpsMetadata ARN without the opsmetadata/ resource holds no id to read
// its tags with: the row is kept untagged, nothing is sent, and the read
// fails with errSSMNoTagID.
func TestScanSSM_OpsMetadataARNWithoutID(t *testing.T) {
	st := newTestStore(t)
	calls := newSSMCalls()
	handlers := ssmHandlers(calls)
	odd := calls.arn("opsmetadata")
	handlers["ListOpsMetadata"] = func(in any) (any, error) {
		calls.record("ListOpsMetadata", in)
		return &ssm.ListOpsMetadataOutput{OpsMetadataList: []ssmtypes.OpsMetadata{{OpsMetadataArn: &odd}}}, nil
	}

	total, _, err := ssmScan(context.Background(), st, handlers)
	if !errors.Is(err, errSSMNoTagID) {
		t.Fatalf("err = %v; want errSSMNoTagID", err)
	}
	if want := ssmTestTotal - 1; total != want {
		t.Errorf("total = %d; want %d", total, want)
	}
	if r, ok := rowsByNativeID(t, st, TypeSSMOpsMetadata)[odd]; !ok || r.TagsJSON != nil {
		t.Errorf("row %s stored = %v, tags %v; want stored untagged", odd, ok, r.TagsJSON)
	}
	for _, k := range calls.readKeys() {
		if strings.HasPrefix(k, "OpsMetadata/") {
			t.Errorf("tag read %s sent; want none for OpsMetadata", k)
		}
	}
}

// A failed DescribeDocument. InvalidDocument, which the op models, drops the
// document: no row and no sharing permission. Any other failure keeps the
// list element as the row, its tags included, and its permission is read: a
// denial warns once under the Describe; a throttle is transient; any other
// error is an error.
func TestScanSSM_DescribeDocumentFailures(t *testing.T) {
	for _, c := range []struct {
		name    string
		err     error
		outcome serviceOutcome // -1: no error
		code    string
		warnOp  string
	}{
		{"not found", apiErr("InvalidDocument", "gone"), -1, "", ""},
		{"denied", ssmDenied, -1, "", ssmDescribeDocumentOp},
		{"transient", ssmThrottle, outcomeTransient, "ThrottlingException", ""},
		{"other error", apiErr("InvalidDocumentVersion", "bad"), outcomeError, "InvalidDocumentVersion", ""},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			calls := newSSMCalls()
			calls.describeErrs["document-x1"] = c.err
			gone := c.warnOp == "" && c.outcome < 0
			id := calls.arn("document/document-x1")

			total, _, err := ssmScan(context.Background(), st, ssmHandlers(calls))
			if c.outcome < 0 {
				if err != nil {
					t.Fatalf("err = %v; want nil", err)
				}
			} else {
				assertGlueOutcome(t, err, c.outcome)
				assertAPICode(t, err, c.code)
			}
			lost := 0
			if gone {
				lost = 2
			}
			if want := ssmTestTotal - lost; total != want {
				t.Errorf("total = %d; want %d", total, want)
			}
			r, ok := rowsByNativeID(t, st, TypeSSMDocument)[id]
			if ok == gone {
				t.Fatalf("row stored = %v; want %v", ok, !gone)
			}
			if ok {
				if strings.Contains(r.AttributesJSON, ssmDocumentDescription("document-x1")) || !strings.Contains(r.AttributesJSON, `"DocumentType":"Command"`) {
					t.Errorf("attrs = %s; want the list element", r.AttributesJSON)
				}
				assertTagsMap(t, r.TagsJSON, map[string]string{"type": TypeSSMDocument})
			}
			if _, ok := rowsByNativeID(t, st, TypeSSMDocumentPermission)[ssmDocumentPermissionNativeID(id)]; ok == gone {
				t.Errorf("permission stored = %v; want %v", ok, !gone)
			}
			var want []string
			if c.warnOp != "" {
				want = []string{c.warnOp}
			}
			if got := ssmWarningOps(warns()); !slices.Equal(got, want) {
				t.Errorf("warnings = %v; want %v", got, want)
			}
		})
	}
}

// A failed write stops the scan: no warning about the rows it lost (a denied
// tag read among them), no later phase runs, and the earlier phases' rows
// stay stored.
func TestScanSSM_FailedWriteStopsScan(t *testing.T) {
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	refuseStoreWrites(t, st, TypeSSMPatchBaseline)
	calls := newSSMCalls()
	calls.tagErrs[ssmTagKey("PatchBaseline", "pb-x1")] = ssmDenied

	total, _, err := ssmScan(context.Background(), st, ssmHandlers(calls))
	if !errors.Is(err, store.ErrStoreWrite) {
		t.Fatalf("err = %v; want store.ErrStoreWrite", err)
	}
	if w := warns(); len(w) != 0 {
		t.Errorf("warnings = %v; want none", w)
	}
	if n := calls.count("DescribePatchBaselines"); n != 1 {
		t.Errorf("DescribePatchBaselines called %d times; want 1, none after the failed write", n)
	}
	if n := calls.count("ListAssociations"); n != 0 {
		t.Errorf("ListAssociations called %d times; want 0 after the failed write", n)
	}
	if total != 4 {
		t.Errorf("total = %d; want 4 (the parameters and documents)", total)
	}
}

// A listing's failed later page keeps the page read before it, and its
// error does not stop the later phases: a window on the lost page has no
// target or task listed.
func TestScanSSM_ListFailureKeepsPagesAndLaterPhases(t *testing.T) {
	for _, c := range []struct {
		op   string
		lost int
	}{
		{"DescribeParameters", 1},
		{"ListDocuments", 2},
		{"DescribeMaintenanceWindows", 3},
		{"DescribeInstanceInformation", 1},
		{"DescribeActivations", 1},
	} {
		t.Run(c.op, func(t *testing.T) {
			st := newTestStore(t)
			calls := newSSMCalls()
			handlers := ssmHandlers(calls)
			handlers[c.op] = failOnCall(2, ssmBadType, handlers[c.op])
			l := ssmListingOf(t, c.op)

			total, _, err := ssmScan(context.Background(), st, handlers)
			assertGlueOutcome(t, err, outcomeError)
			assertAPICode(t, err, "InvalidResourceType")
			if total != ssmTestTotal-c.lost {
				t.Errorf("total = %d; want %d", total, ssmTestTotal-c.lost)
			}
			if _, ok := rowsByNativeID(t, st, l.typ)[l.nativeID(calls, "x1")]; !ok {
				t.Errorf("%s row from the page before the failure missing", l.typ)
			}
			if got := len(rowsByNativeID(t, st, TypeSSMCloudConnector)); got != 2 {
				t.Errorf("cloud connectors (a later phase) = %d; want 2", got)
			}
		})
	}
}

// A denied listing is warned about once under its op and does not stop the
// later phases: documents and patch baselines are still listed after a
// denied DescribeParameters, and a denied ListDocuments or
// DescribeActivations is warned about.
func TestScanSSM_DeniedListingWarnsAndContinues(t *testing.T) {
	for _, op := range []string{"DescribeParameters", "ListDocuments", "DescribePatchBaselines", "DescribeActivations"} {
		t.Run(op, func(t *testing.T) {
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			calls := newSSMCalls()
			handlers := ssmHandlers(calls)
			delete(handlers, op)
			lost := 2
			if op == "ListDocuments" {
				lost = 4 // and their permissions
			}

			total, _, err := ssmScan(context.Background(), st, handlers)
			if err != nil {
				t.Fatalf("scan: %v", err)
			}
			if total != ssmTestTotal-lost {
				t.Errorf("total = %d; want %d", total, ssmTestTotal-lost)
			}
			if got := ssmWarningOps(warns()); !slices.Equal(got, []string{"ssm:" + op}) {
				t.Errorf("warnings = %v; want one under ssm:%s", got, op)
			}
		})
	}
}

// A repeated NextToken ends the listing as an error, keeping its pages.
func TestScanSSM_RepeatedTokenKeepsPages(t *testing.T) {
	for _, op := range []string{"ListAssociations", "DescribeActivations"} {
		t.Run(op, func(t *testing.T) {
			st := newTestStore(t)
			calls := newSSMCalls()
			handlers := ssmHandlers(calls)
			l := ssmListingOf(t, op)
			handlers[op] = func(in any) (any, error) {
				calls.record(op, in)
				return l.out(calls, sdkaws.String("same"), pick(bacField[*string](in, "NextToken") == nil, "x1", "x2")), nil
			}

			total, _, err := ssmScan(context.Background(), st, handlers)
			if !errors.Is(err, errRepeatedNextToken) {
				t.Fatalf("err = %v; want errRepeatedNextToken", err)
			}
			if total != ssmTestTotal {
				t.Errorf("total = %d; want %d", total, ssmTestTotal)
			}
			if n := calls.count(op); n != 2 {
				t.Errorf("%s called %d times; want 2", op, n)
			}
		})
	}
}

// An element without the id or name its row is keyed by is not stored, not
// sent to the tag op, and is warned about once under its listing, the
// warning naming the element by what else it carries (noIdentifier when
// nothing).
func TestScanSSM_ElementWithoutID(t *testing.T) {
	for _, c := range []struct {
		op   string
		out  any
		key  string
		lost int
	}{
		{"DescribeParameters", &ssm.DescribeParametersOutput{Parameters: []ssmtypes.ParameterMetadata{{ARN: sdkaws.String("arn-of-nameless")}}}, "arn-of-nameless", 2},
		{"DescribeParameters", &ssm.DescribeParametersOutput{Parameters: []ssmtypes.ParameterMetadata{{}}}, noIdentifier, 2},
		{"ListDocuments", &ssm.ListDocumentsOutput{DocumentIdentifiers: []ssmtypes.DocumentIdentifier{{DisplayName: sdkaws.String("nameless-doc")}}}, "nameless-doc", 4},
		{"DescribePatchBaselines", &ssm.DescribePatchBaselinesOutput{BaselineIdentities: []ssmtypes.PatchBaselineIdentity{{BaselineName: sdkaws.String("idless-baseline")}}}, "idless-baseline", 2},
		{"ListAssociations", &ssm.ListAssociationsOutput{Associations: []ssmtypes.Association{{AssociationName: sdkaws.String("idless-assoc")}}}, "idless-assoc", 2},
		{"DescribeMaintenanceWindows", &ssm.DescribeMaintenanceWindowsOutput{WindowIdentities: []ssmtypes.MaintenanceWindowIdentity{{Name: sdkaws.String("idless-window")}}}, "idless-window", 6},
		{"DescribeInstanceInformation", &ssm.DescribeInstanceInformationOutput{InstanceInformationList: []ssmtypes.InstanceInformation{{ComputerName: sdkaws.String("idless-host")}}}, "idless-host", 2},
		{"ListOpsMetadata", &ssm.ListOpsMetadataOutput{OpsMetadataList: []ssmtypes.OpsMetadata{{ResourceId: sdkaws.String("arnless-app")}}}, "arnless-app", 2},
		{"DescribeActivations", &ssm.DescribeActivationsOutput{ActivationList: []ssmtypes.Activation{{DefaultInstanceName: sdkaws.String("idless-activation")}}}, "idless-activation", 2},
		{"ListCloudConnectors", &ssm.ListCloudConnectorsOutput{CloudConnectors: []ssmtypes.CloudConnectorSummary{{DisplayName: sdkaws.String("idless-connector")}}}, "idless-connector", 2},
		{"ListResourceDataSync", &ssm.ListResourceDataSyncOutput{ResourceDataSyncItems: []ssmtypes.ResourceDataSyncItem{{}}}, noIdentifier, 2},
	} {
		t.Run(c.op+"/"+c.key, func(t *testing.T) {
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			calls := newSSMCalls()
			handlers := ssmHandlers(calls)
			handlers[c.op] = func(in any) (any, error) {
				calls.record(c.op, in)
				return c.out, nil
			}

			total, _, err := ssmScan(context.Background(), st, handlers)
			if err != nil {
				t.Fatalf("scan: %v", err)
			}
			if total != ssmTestTotal-c.lost {
				t.Errorf("total = %d; want %d", total, ssmTestTotal-c.lost)
			}
			w := warns()
			if len(w) != 1 || w[0].Service != "ssm:"+c.op || w[0].Scope != testAccountID+"/"+testRegion || !strings.Contains(w[0].Message, c.key) {
				t.Fatalf("warnings = %v; want one under ssm:%s naming %s", w, c.op, c.key)
			}
			l := ssmListingOf(t, c.op)
			for _, k := range calls.readKeys() {
				if l.tagType != "" && strings.HasPrefix(k, l.tagType+"/") {
					t.Errorf("tag read %s sent; want none for %s", k, l.typ)
				}
			}
		})
	}
}

// A window's failed target or task listing costs the other window nothing:
// a window deleted since it was listed is skipped silently, a denial warns
// once under the op, any other error is returned. A child without an id is
// not stored and is warned about, naming its window.
func TestScanSSM_WindowChildFailures(t *testing.T) {
	for _, op := range []string{"DescribeMaintenanceWindowTargets", "DescribeMaintenanceWindowTasks"} {
		for _, c := range []struct {
			name   string
			err    error
			code   string
			warnOp string
		}{
			{"window gone", apiErr("DoesNotExistException", "gone"), "", ""},
			{"denied", ssmDenied, "", "ssm:" + op},
			{"other error", apiErr("InternalServerError", "boom"), "InternalServerError", ""},
		} {
			t.Run(op+"/"+c.name, func(t *testing.T) {
				st := newTestStore(t)
				warns := rhWarnRecorder(st)
				calls := newSSMCalls()
				calls.windowErrs[op+" mw-x1"] = c.err

				total, _, err := ssmScan(context.Background(), st, ssmHandlers(calls))
				if c.code == "" {
					if err != nil {
						t.Fatalf("err = %v; want nil", err)
					}
				} else {
					assertAPICode(t, err, c.code)
				}
				if total != ssmTestTotal-1 {
					t.Errorf("total = %d; want %d", total, ssmTestTotal-1)
				}
				var want []string
				if c.warnOp != "" {
					want = []string{c.warnOp}
				}
				if got := ssmWarningOps(warns()); !slices.Equal(got, want) {
					t.Errorf("warnings = %v; want %v", got, want)
				}
			})
		}
	}
	for _, c := range []struct {
		op   string
		idle any // window mw-x1's one child, without an id, named "idless"
	}{
		{"DescribeMaintenanceWindowTargets", &ssm.DescribeMaintenanceWindowTargetsOutput{Targets: []ssmtypes.MaintenanceWindowTarget{{Name: sdkaws.String("idless")}}}},
		{"DescribeMaintenanceWindowTasks", &ssm.DescribeMaintenanceWindowTasksOutput{Tasks: []ssmtypes.MaintenanceWindowTask{{Name: sdkaws.String("idless")}}}},
	} {
		t.Run(c.op+"/child without id", func(t *testing.T) {
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			calls := newSSMCalls()
			handlers := ssmHandlers(calls)
			list := handlers[c.op]
			handlers[c.op] = func(in any) (any, error) {
				if sv(bacField[*string](in, "WindowId")) == "mw-x1" {
					return c.idle, nil
				}
				return list(in)
			}

			total, _, err := ssmScan(context.Background(), st, handlers)
			if err != nil {
				t.Fatalf("scan: %v", err)
			}
			if total != ssmTestTotal-1 {
				t.Errorf("total = %d; want %d", total, ssmTestTotal-1)
			}
			w := warns()
			if len(w) != 1 || w[0].Service != "ssm:"+c.op || !strings.Contains(w[0].Message, "idless of window mw-x1") {
				t.Errorf("warnings = %v; want one under ssm:%s naming idless of window mw-x1", w, c.op)
			}
		})
	}
}

// A GovCloud or China account's rows are keyed by ARNs in its partition,
// and the ids sent to the tag op, the OpsMetadata id cut from its partition
// ARN among them, are those of the commercial scan.
func TestScanSSM_TagReadsInOtherPartitions(t *testing.T) {
	for _, pf := range partitionFixtures {
		t.Run(pf.partition, func(t *testing.T) {
			st := newTestStore(t)
			calls := newSSMCalls()
			calls.partition, calls.region = pf.partition, pf.region
			acct := partitionAccount(pf.partition)
			acct.cfg = opStubConfig(ssmHandlers(calls), ssmDenied)

			if _, _, err := scanSSM(context.Background(), acct, pf.region, st, testScanID); err != nil {
				t.Fatalf("scan: %v", err)
			}
			if got, want := calls.readKeys(), ssmTaggedReadKeys(); !slices.Equal(got, want) {
				t.Errorf("ListTagsForResource reads = %v\nwant %v", got, want)
			}
			for _, l := range ssmListings {
				id := l.nativeID(calls, "x1")
				r, ok := rowsByNativeID(t, st, l.typ)[id]
				if !ok {
					t.Errorf("%s %s missing", l.typ, id)
					continue
				}
				if l.tagType != "" {
					assertTagsMap(t, r.TagsJSON, map[string]string{"type": l.typ})
				}
			}
		})
	}
}

// Once ctx ends, the stop is one member of the error, although the phase in
// flight saw it in its listing, and the later phases are not run.
func TestScanSSM_ContextEndReportedOnce(t *testing.T) {
	st := newTestStore(t)
	calls := newSSMCalls()
	handlers := ssmHandlers(calls)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	list := handlers["ListAssociations"]
	handlers["ListAssociations"] = func(in any) (any, error) {
		if bacField[*string](in, "NextToken") != nil {
			cancel()
			return nil, context.Canceled
		}
		return list(in)
	}

	_, _, err := ssmScan(ctx, st, handlers)
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
	if n := calls.count("DescribeMaintenanceWindows"); n != 0 {
		t.Errorf("DescribeMaintenanceWindows called %d times after ctx ended", n)
	}
}

// NXDOMAIN. With nothing stored, the first listing's NXDOMAIN reports the
// region unavailable and nothing more is called. Once rows were stored, an
// NXDOMAIN from a tag read is a failure of this scan, counts kept.
func TestScanSSM_NXDOMAIN(t *testing.T) {
	nx := &net.DNSError{Err: "no such host", Name: "ssm.us-east-1.amazonaws.com", IsNotFound: true}
	t.Run("region unavailable", func(t *testing.T) {
		st := newTestStore(t)
		calls := newSSMCalls()
		handlers := ssmHandlers(calls)
		handlers["DescribeParameters"] = func(in any) (any, error) {
			calls.record("DescribeParameters", in)
			return nil, nx
		}

		total, _, err := ssmScan(context.Background(), st, handlers)
		if got := classifyServiceError(err); got != outcomeUnavailable || total != 0 {
			t.Errorf("classify = %v, total = %d; want outcomeUnavailable, 0 (err %v)", got, total, err)
		}
		if n := calls.count("ListDocuments"); n != 0 {
			t.Errorf("ListDocuments called %d times; want 0", n)
		}
	})
	t.Run("tag read after rows", func(t *testing.T) {
		st := newTestStore(t)
		calls := newSSMCalls()
		calls.tagErrs[ssmTagKey("Parameter", "/app/param-x1")] = nx

		total, _, err := ssmScan(context.Background(), st, ssmHandlers(calls))
		assertGlueOutcome(t, err, outcomeError)
		if total != ssmTestTotal {
			t.Errorf("total = %d; want %d", total, ssmTestTotal)
		}
	})
}
