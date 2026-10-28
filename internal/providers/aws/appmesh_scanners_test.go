package aws

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"net"
	"slices"
	"strings"
	"sync"
	"testing"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/appmesh"
	amtypes "github.com/aws/aws-sdk-go-v2/service/appmesh/types"
	"github.com/icearp/disco-cli/store"
)

const (
	// amTestTotal is every row of the fixture: two meshes; two virtual
	// gateways, nodes, routers and services (one per page) in each mesh; two
	// routes in each of the four routers and two gateway routes in each of
	// the four gateways.
	amTestTotal = 2 + 4*2*2 + 2*4*2
	// amTestOtherAccount owns the mesh another account shares with this one.
	amTestOtherAccount = "999999999999"
)

var (
	amTestDenied   = apiErr("ForbiddenException", "User: arn:aws:iam::123:user/x is not authorized to perform: appmesh:List")
	amTestThrottle = apiErr("TooManyRequestsException", "Rate exceeded")
	amTestBad      = apiErr("BadRequestException", "bad")
	amTestGone     = apiErr("NotFoundException", "gone")
	amTestMeshes   = []string{"mesh-bare", "mesh-tagged"}
)

// amTestMeshARN is the ARN of mesh, owned by owner.
func amTestMeshARN(owner, mesh string) string {
	return "arn:aws:appmesh:" + testRegion + ":" + owner + ":mesh/" + mesh
}

// amKind is one listing whose rows have their tags read: the List op, the
// type it stores, the ARN segment of its rows ("" for a mesh), the short name
// its rows are named by, the listing its parent comes from ("" for none), and
// page, which builds the op's output holding one element and next.
type amKind struct {
	op, typ, seg, short, parentOp string
	page                          func(arn, name, mesh, owner, next *string) any
}

func amKinds() []amKind {
	return []amKind{
		{"ListMeshes", TypeAppMeshMesh, "", "mesh", "", func(arn, name, _, owner, next *string) any {
			return &appmesh.ListMeshesOutput{NextToken: next, Meshes: []amtypes.MeshRef{{Arn: arn, MeshName: name, MeshOwner: owner, ResourceOwner: owner, Version: sdkaws.Int64(1)}}}
		}},
		{"ListVirtualGateways", TypeAppMeshVirtualGateway, "virtualGateway", "vg", "ListMeshes", func(arn, name, mesh, owner, next *string) any {
			return &appmesh.ListVirtualGatewaysOutput{NextToken: next, VirtualGateways: []amtypes.VirtualGatewayRef{{Arn: arn, VirtualGatewayName: name, MeshName: mesh, MeshOwner: owner, ResourceOwner: owner, Version: sdkaws.Int64(1)}}}
		}},
		{"ListVirtualNodes", TypeAppMeshVirtualNode, "virtualNode", "vn", "ListMeshes", func(arn, name, mesh, owner, next *string) any {
			return &appmesh.ListVirtualNodesOutput{NextToken: next, VirtualNodes: []amtypes.VirtualNodeRef{{Arn: arn, VirtualNodeName: name, MeshName: mesh, MeshOwner: owner, ResourceOwner: owner, Version: sdkaws.Int64(1)}}}
		}},
		{"ListVirtualRouters", TypeAppMeshVirtualRouter, "virtualRouter", "vr", "ListMeshes", func(arn, name, mesh, owner, next *string) any {
			return &appmesh.ListVirtualRoutersOutput{NextToken: next, VirtualRouters: []amtypes.VirtualRouterRef{{Arn: arn, VirtualRouterName: name, MeshName: mesh, MeshOwner: owner, ResourceOwner: owner, Version: sdkaws.Int64(1)}}}
		}},
		{"ListVirtualServices", TypeAppMeshVirtualService, "virtualService", "vs", "ListMeshes", func(arn, name, mesh, owner, next *string) any {
			return &appmesh.ListVirtualServicesOutput{NextToken: next, VirtualServices: []amtypes.VirtualServiceRef{{Arn: arn, VirtualServiceName: name, MeshName: mesh, MeshOwner: owner, ResourceOwner: owner, Version: sdkaws.Int64(1)}}}
		}},
		{"ListRoutes", TypeAppMeshRoute, "route", "rt", "ListVirtualRouters", func(arn, name, mesh, owner, next *string) any {
			return &appmesh.ListRoutesOutput{NextToken: next, Routes: []amtypes.RouteRef{{Arn: arn, RouteName: name, MeshName: mesh, MeshOwner: owner, ResourceOwner: owner, Version: sdkaws.Int64(1)}}}
		}},
		{"ListGatewayRoutes", TypeAppMeshGatewayRoute, "gatewayRoute", "gr", "ListVirtualGateways", func(arn, name, mesh, owner, next *string) any {
			return &appmesh.ListGatewayRoutesOutput{NextToken: next, GatewayRoutes: []amtypes.GatewayRouteRef{{Arn: arn, GatewayRouteName: name, MeshName: mesh, MeshOwner: owner, ResourceOwner: owner, Version: sdkaws.Int64(1)}}}
		}},
	}
}

func amKindOf(t *testing.T, op string) amKind {
	t.Helper()
	for _, k := range amKinds() {
		if k.op == op {
			return k
		}
	}
	t.Fatalf("%s is no App Mesh listing", op)
	return amKind{}
}

func (k amKind) name(suffix string) string { return k.short + "-" + suffix }

// arn is the ARN of the row on page suffix of the listing of parent (an ARN;
// for a mesh, the owner's mesh ARN prefix is built from owner instead).
func (k amKind) arn(parent, suffix string) string {
	if k.seg == "" {
		return amTestMeshARN(testAccountID, k.name(suffix))
	}
	return parent + "/" + k.seg + "/" + k.name(suffix)
}

// parents is the ARN of every parent k's listing is called for in the
// fixture; one "" for the mesh listing.
func (k amKind) parents(t *testing.T) []string {
	if k.parentOp == "" {
		return []string{""}
	}
	var arns []string
	for _, p := range amKindOf(t, k.parentOp).parents(t) {
		pk := amKindOf(t, k.parentOp)
		arns = append(arns, pk.arn(p, "tagged"), pk.arn(p, "bare"))
	}
	return arns
}

// arns is the ARN of every row k's listing returns in the fixture.
func (k amKind) arns(t *testing.T) []string {
	var arns []string
	for _, p := range k.parents(t) {
		arns = append(arns, k.arn(p, "tagged"), k.arn(p, "bare"))
	}
	return arns
}

// amCalls records the calls of a scan and answers the tag reads, by ARN,
// with tags or errs.
type amCalls struct {
	mu      sync.Mutex
	limits  map[string][]int32
	owners  map[string][]string
	parents map[string][]string
	tagARNs []string
	tags    map[string]map[string]string
	errs    map[string]error
}

// newAMCalls tags every "-tagged" row with its type.
func newAMCalls(t *testing.T) *amCalls {
	c := &amCalls{limits: map[string][]int32{}, owners: map[string][]string{}, parents: map[string][]string{}, tags: map[string]map[string]string{}, errs: map[string]error{}}
	for _, k := range amKinds() {
		for _, arn := range k.arns(t) {
			if strings.HasSuffix(arn, "-tagged") {
				c.tags[arn] = map[string]string{"type": k.typ}
			}
		}
	}
	return c
}

// record notes op's Limit and MeshOwner, and on a first page the parent it
// lists: mesh, mesh/router or mesh/gateway.
func (c *amCalls) record(op string, in any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if n := bacField[*int32](in, "Limit"); n != nil {
		c.limits[op] = append(c.limits[op], *n)
	} else {
		c.limits[op] = append(c.limits[op], 0)
	}
	c.owners[op] = append(c.owners[op], sv(bacField[*string](in, "MeshOwner")))
	if bacField[*string](in, "NextToken") == nil {
		parent := sv(bacField[*string](in, "MeshName"))
		for _, f := range []string{"VirtualRouterName", "VirtualGatewayName"} {
			if n := bacField[*string](in, f); n != nil {
				parent += "/" + *n
			}
		}
		c.parents[op] = append(c.parents[op], parent)
	}
}

func (c *amCalls) count(op string) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.limits[op])
}

// listedParents is the parents op was called for, sorted.
func (c *amCalls) listedParents(op string) []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Sorted(slices.Values(c.parents[op]))
}

func (c *amCalls) sortedTagARNs() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Sorted(slices.Values(c.tagARNs))
}

func (c *amCalls) listTags(in any) (any, error) {
	arn := sv(in.(*appmesh.ListTagsForResourceInput).ResourceArn)
	c.record("ListTagsForResource", in)
	c.mu.Lock()
	defer c.mu.Unlock()
	c.tagARNs = append(c.tagARNs, arn)
	if err := c.errs[arn]; err != nil {
		return nil, err
	}
	out := &appmesh.ListTagsForResourceOutput{Tags: []amtypes.TagRef{}}
	for k, v := range c.tags[arn] {
		out.Tags = append(out.Tags, amtypes.TagRef{Key: sdkaws.String(k), Value: sdkaws.String(v)})
	}
	return out, nil
}

// amTestParentARN is the ARN of the parent a child listing's input names,
// in the account its MeshOwner names (this one when it names none).
func amTestParentARN(in any) string {
	owner := cmp.Or(sv(bacField[*string](in, "MeshOwner")), testAccountID)
	arn := amTestMeshARN(owner, sv(bacField[*string](in, "MeshName")))
	if vr := bacField[*string](in, "VirtualRouterName"); vr != nil {
		arn += "/virtualRouter/" + *vr
	}
	if vg := bacField[*string](in, "VirtualGatewayName"); vg != nil {
		arn += "/virtualGateway/" + *vg
	}
	return arn
}

// twoPages answers k's op with "tagged" on the first page and "bare" on the
// second, under the parent its input names.
func (c *amCalls) twoPages(k amKind) func(any) (any, error) {
	return func(in any) (any, error) {
		c.record(k.op, in)
		suffix, next := "tagged", sdkaws.String("p2")
		if bacField[*string](in, "NextToken") != nil {
			suffix, next = "bare", nil
		}
		owner := cmp.Or(sv(bacField[*string](in, "MeshOwner")), testAccountID)
		name := k.name(suffix)
		if k.seg == "" {
			arn := amTestMeshARN(owner, name)
			return k.page(&arn, &name, &name, &owner, next), nil
		}
		arn := k.arn(amTestParentARN(in), suffix)
		mesh := sv(bacField[*string](in, "MeshName"))
		return k.page(&arn, &name, &mesh, &owner, next), nil
	}
}

// amHandlers answers every op the scan calls: each listing in two pages and
// the tag reads from c.
func amHandlers(c *amCalls) map[string]func(in any) (any, error) {
	h := map[string]func(in any) (any, error){"ListTagsForResource": c.listTags}
	for _, k := range amKinds() {
		h[k.op] = c.twoPages(k)
	}
	return h
}

// amScan runs the scan's real entry point against handlers; every op they
// do not answer is denied.
func amScan(ctx context.Context, st *store.Store, handlers map[string]func(in any) (any, error)) (int, int, error) {
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(handlers, amTestDenied)
	return scanAppMesh(ctx, acct, testRegion, st, testScanID)
}

// amAllARNs is the ARN of every row of the fixture, sorted.
func amAllARNs(t *testing.T) []string {
	var arns []string
	for _, k := range amKinds() {
		arns = append(arns, k.arns(t)...)
	}
	slices.Sort(arns)
	return arns
}

func amWarnings(w []store.ScanWarning) []string {
	var ops []string
	for _, x := range w {
		ops = append(ops, x.Service)
	}
	return ops
}

// Every listing stores both its pages, for every parent, each row under its
// ARN, named by its name, its attributes the listed element, with no Status
// or CreatedAt, and with the tags its tag read returns (nil for none).
// ListTagsForResource is sent exactly the ARNs of every row; every call is
// sent the model's limit cap of 100 and every child listing this account as
// MeshOwner. Routes are listed per router and gateway routes per gateway.
// No error, no warning.
func TestScanAppMesh_StoresTagsOfEveryType(t *testing.T) {
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	calls := newAMCalls(t)

	total, inserted, err := amScan(context.Background(), st, amHandlers(calls))
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != amTestTotal || inserted != amTestTotal {
		t.Errorf("total, inserted = %d, %d; want %d, %d", total, inserted, amTestTotal, amTestTotal)
	}
	if w := warns(); len(w) != 0 {
		t.Errorf("warnings = %v; want none", w)
	}
	for _, k := range amKinds() {
		rows := rowsByNativeID(t, st, k.typ)
		if len(rows) != len(k.arns(t)) {
			t.Errorf("%s rows = %d; want %d", k.typ, len(rows), len(k.arns(t)))
		}
		for _, arn := range k.arns(t) {
			r, ok := rows[arn]
			if !ok {
				t.Errorf("%s %s missing", k.typ, arn)
				continue
			}
			wantName := arn[strings.LastIndex(arn, "/")+1:]
			if sv(r.Name) != wantName || sv(r.Region) != testRegion || r.Status != nil || r.CreatedAt != nil {
				t.Errorf("%s Name, Region, Status, CreatedAt = %q, %q, %v, %v; want %q, %q, nil, nil", arn, sv(r.Name), sv(r.Region), r.Status, r.CreatedAt, wantName, testRegion)
			}
			var attrs struct {
				Arn       string
				MeshOwner string
				Version   int64
			}
			if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil || attrs.Arn != arn || attrs.MeshOwner != testAccountID || attrs.Version != 1 {
				t.Errorf("%s attrs = %s (%v); want the listed element", arn, r.AttributesJSON, err)
			}
			if strings.HasSuffix(arn, "-tagged") {
				assertTagsMap(t, r.TagsJSON, map[string]string{"type": k.typ})
			} else if r.TagsJSON != nil {
				t.Errorf("%s TagsJSON = %q; want nil", arn, *r.TagsJSON)
			}
		}
	}
	if got := calls.sortedTagARNs(); !slices.Equal(got, amAllARNs(t)) {
		t.Errorf("ListTagsForResource ARNs = %v\nwant %v", got, amAllARNs(t))
	}
	perRouter := []string{"mesh-bare/vr-bare", "mesh-bare/vr-tagged", "mesh-tagged/vr-bare", "mesh-tagged/vr-tagged"}
	perGateway := []string{"mesh-bare/vg-bare", "mesh-bare/vg-tagged", "mesh-tagged/vg-bare", "mesh-tagged/vg-tagged"}
	for op, want := range map[string][]string{
		"ListVirtualGateways": amTestMeshes, "ListVirtualNodes": amTestMeshes,
		"ListVirtualRouters": amTestMeshes, "ListVirtualServices": amTestMeshes,
		"ListRoutes": perRouter, "ListGatewayRoutes": perGateway,
	} {
		if got := calls.listedParents(op); !slices.Equal(got, want) {
			t.Errorf("%s parents = %v; want %v", op, got, want)
		}
		for _, o := range calls.owners[op] {
			if o != testAccountID {
				t.Errorf("%s MeshOwner = %q; want %s", op, o, testAccountID)
			}
		}
	}
	for _, op := range []string{"ListMeshes", "ListVirtualGateways", "ListVirtualNodes", "ListVirtualRouters", "ListVirtualServices", "ListRoutes", "ListGatewayRoutes", "ListTagsForResource"} {
		if calls.count(op) == 0 {
			t.Errorf("%s not called", op)
		}
		for _, n := range calls.limits[op] {
			if n != 100 {
				t.Errorf("%s Limit = %d; want 100", op, n)
			}
		}
	}
}

// A rescan stores the same rows without inserting any.
func TestScanAppMesh_RescanInsertsNone(t *testing.T) {
	st := newTestStore(t)
	if _, _, err := amScan(context.Background(), st, amHandlers(newAMCalls(t))); err != nil {
		t.Fatalf("first scan: %v", err)
	}
	total, inserted, err := amScan(context.Background(), st, amHandlers(newAMCalls(t)))
	if err != nil {
		t.Fatalf("rescan: %v", err)
	}
	if total != amTestTotal || inserted != 0 {
		t.Errorf("rescan total, inserted = %d, %d; want %d, 0", total, inserted, amTestTotal)
	}
}

// No mesh: nothing stored, no child listing or tag read, no error, no
// warning.
func TestScanAppMesh_NoMeshes(t *testing.T) {
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	calls := newAMCalls(t)
	handlers := amHandlers(calls)
	handlers["ListMeshes"] = func(in any) (any, error) {
		calls.record("ListMeshes", in)
		return &appmesh.ListMeshesOutput{}, nil
	}

	total, inserted, err := amScan(context.Background(), st, handlers)
	if err != nil || total != 0 || inserted != 0 {
		t.Fatalf("scan = %d, %d, %v; want 0, 0, nil", total, inserted, err)
	}
	if w := warns(); len(w) != 0 {
		t.Errorf("warnings = %v; want none", w)
	}
	for _, op := range []string{"ListVirtualGateways", "ListVirtualNodes", "ListVirtualRouters", "ListVirtualServices", "ListRoutes", "ListGatewayRoutes", "ListTagsForResource"} {
		if n := calls.count(op); n != 0 {
			t.Errorf("%s called %d times; want 0", op, n)
		}
	}
}

// A failed tag read. NotFoundException is modeled, so a resource deleted
// since it was listed is dropped silently (a mesh with its children, which
// are not listed). A denial keeps the row untagged and warns once under the
// tag op with the scan's scope; a throttle keeps it and is transient; any
// other error keeps it and is an error.
func TestScanAppMesh_TagReadFailures(t *testing.T) {
	route := amKindOf(t, "ListRoutes").arns(t)[0]
	mesh := amTestMeshARN(testAccountID, "mesh-bare")
	// mesh-bare's children: four per-mesh listings of two, and two routes and
	// two gateway routes under each of its two routers and gateways.
	meshAndChildren := 1 + 4*2 + 2*2*2
	for _, c := range []struct {
		name      string
		arn, typ  string
		err       error
		outcome   serviceOutcome // -1: no error
		code      string
		wantTotal int
		wantKept  bool
		warn      bool
	}{
		{"route/not found", route, TypeAppMeshRoute, amTestGone, -1, "", amTestTotal - 1, false, false},
		{"route/denied", route, TypeAppMeshRoute, amTestDenied, -1, "", amTestTotal, true, true},
		{"route/transient", route, TypeAppMeshRoute, amTestThrottle, outcomeTransient, "TooManyRequestsException", amTestTotal, true, false},
		{"route/other error", route, TypeAppMeshRoute, amTestBad, outcomeError, "BadRequestException", amTestTotal, true, false},
		{"mesh/not found", mesh, TypeAppMeshMesh, amTestGone, -1, "", amTestTotal - meshAndChildren, false, false},
		{"mesh/denied", mesh, TypeAppMeshMesh, amTestDenied, -1, "", amTestTotal, true, true},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			calls := newAMCalls(t)
			calls.errs[c.arn] = c.err

			total, _, err := amScan(context.Background(), st, amHandlers(calls))
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
			if c.warn {
				want = []string{amTagsOp}
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

// ListTagsForResource pages are all read; a repeated nextToken fails the
// read, keeping the row untagged.
func TestScanAppMesh_TagPages(t *testing.T) {
	arn := amKindOf(t, "ListVirtualNodes").arns(t)[1]
	pageOf := func(next *string, k, v string) *appmesh.ListTagsForResourceOutput {
		return &appmesh.ListTagsForResourceOutput{NextToken: next, Tags: []amtypes.TagRef{{Key: sdkaws.String(k), Value: sdkaws.String(v)}}}
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
			calls := newAMCalls(t)
			handlers := amHandlers(calls)
			handlers["ListTagsForResource"] = func(in any) (any, error) {
				req := in.(*appmesh.ListTagsForResourceInput)
				if sv(req.ResourceArn) != arn {
					return calls.listTags(in)
				}
				if req.NextToken == nil {
					return pageOf(sdkaws.String("t2"), "a", "1"), nil
				}
				return pageOf(c.second, "b", "2"), nil
			}
			total, _, err := amScan(context.Background(), st, handlers)
			if total != amTestTotal {
				t.Errorf("total = %d; want %d", total, amTestTotal)
			}
			r := rowsByNativeID(t, st, TypeAppMeshVirtualNode)[arn]
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
func TestScanAppMesh_TagDenialsWarnOncePerScan(t *testing.T) {
	st := newTestStore(t)
	warns := rhWarnRecorder(st)
	calls := newAMCalls(t)
	for _, op := range []string{"ListVirtualRouters", "ListGatewayRoutes"} {
		for _, arn := range amKindOf(t, op).arns(t) {
			calls.errs[arn] = amTestDenied
		}
	}

	total, _, err := amScan(context.Background(), st, amHandlers(calls))
	if err != nil {
		t.Fatalf("scan: %v", err)
	}
	if total != amTestTotal {
		t.Errorf("total = %d; want %d", total, amTestTotal)
	}
	if got := amWarnings(warns()); !slices.Equal(got, []string{amTagsOp}) {
		t.Errorf("warnings = %v; want one under %s", got, amTagsOp)
	}
}

// A throttled tag read beside a hard failure of the same op is demoted to
// one warning, so the hard error decides the outcome.
func TestScanAppMesh_TagThrottleDemotedBesideHardError(t *testing.T) {
	st := newTestStore(t)
	warns := countOpWarnings(st, amTagsOp)
	calls := newAMCalls(t)
	calls.errs[amKindOf(t, "ListVirtualServices").arns(t)[0]] = amTestThrottle
	calls.errs[amKindOf(t, "ListGatewayRoutes").arns(t)[3]] = amTestBad

	_, _, err := amScan(context.Background(), st, amHandlers(calls))
	assertGlueOutcome(t, err, outcomeError)
	assertAPICode(t, err, "BadRequestException")
	if *warns != 1 {
		t.Errorf("%s warnings = %d; want 1", amTagsOp, *warns)
	}
}

// A failed write stops the scan: no warning about the rows it lost (a denied
// tag read among them), no later phase runs, and the earlier phases' rows
// stay stored. A failed mesh write lists no mesh's children.
func TestScanAppMesh_FailedWriteStopsScan(t *testing.T) {
	t.Run("virtual routers", func(t *testing.T) {
		st := newTestStore(t)
		warns := rhWarnRecorder(st)
		refuseStoreWrites(t, st, TypeAppMeshVirtualRouter)
		calls := newAMCalls(t)
		calls.errs[amKindOf(t, "ListVirtualRouters").arns(t)[0]] = amTestDenied

		total, _, err := amScan(context.Background(), st, amHandlers(calls))
		if !errors.Is(err, store.ErrStoreWrite) {
			t.Fatalf("err = %v; want store.ErrStoreWrite", err)
		}
		if w := warns(); len(w) != 0 {
			t.Errorf("warnings = %v; want none", w)
		}
		for _, op := range []string{"ListVirtualServices", "ListRoutes", "ListGatewayRoutes"} {
			if n := calls.count(op); n != 0 {
				t.Errorf("%s called %d times; want 0 after the failed write", op, n)
			}
		}
		// The meshes, and each mesh's two virtual gateways and nodes.
		if want := 2 + 2*2*2; total != want {
			t.Errorf("total = %d; want %d", total, want)
		}
	})
	t.Run("meshes", func(t *testing.T) {
		st := newTestStore(t)
		refuseStoreWrites(t, st, TypeAppMeshMesh)
		calls := newAMCalls(t)

		_, _, err := amScan(context.Background(), st, amHandlers(calls))
		if !errors.Is(err, store.ErrStoreWrite) {
			t.Fatalf("err = %v; want store.ErrStoreWrite", err)
		}
		for _, op := range []string{"ListVirtualGateways", "ListVirtualNodes", "ListVirtualRouters", "ListVirtualServices"} {
			if n := calls.count(op); n != 0 {
				t.Errorf("%s called %d times after the failed mesh write", op, n)
			}
		}
	})
}

// A listing's failed later page keeps the pages read before it, and its
// error does not stop the later phases. A mesh not stored has no children
// read.
func TestScanAppMesh_ListFailureKeepsPagesAndLaterPhases(t *testing.T) {
	for _, c := range []struct {
		op, mesh            string
		kept, lost          string
		wantTotal, wantLast int
	}{
		// mesh-tagged's second page of virtual services.
		{
			"ListVirtualServices", "mesh-tagged",
			amTestMeshARN(testAccountID, "mesh-tagged") + "/virtualService/vs-tagged",
			amTestMeshARN(testAccountID, "mesh-tagged") + "/virtualService/vs-bare",
			amTestTotal - 1, 8,
		},
		// The meshes' second page: mesh-bare and its children are lost.
		{
			"ListMeshes", "",
			amTestMeshARN(testAccountID, "mesh-tagged"), amTestMeshARN(testAccountID, "mesh-bare"),
			amTestTotal - 1 - 4*2 - 2*2*2, 4,
		},
	} {
		t.Run(c.op, func(t *testing.T) {
			st := newTestStore(t)
			calls := newAMCalls(t)
			handlers := amHandlers(calls)
			list := handlers[c.op]
			handlers[c.op] = func(in any) (any, error) {
				if bacField[*string](in, "NextToken") != nil && sv(bacField[*string](in, "MeshName")) == c.mesh {
					calls.record(c.op, in)
					return nil, amTestBad
				}
				return list(in)
			}

			total, _, err := amScan(context.Background(), st, handlers)
			assertGlueOutcome(t, err, outcomeError)
			assertAPICode(t, err, "BadRequestException")
			if total != c.wantTotal {
				t.Errorf("total = %d; want %d", total, c.wantTotal)
			}
			rows := rowsByNativeID(t, st, amKindOf(t, c.op).typ)
			if _, ok := rows[c.kept]; !ok {
				t.Errorf("%s, read before the failure, missing", c.kept)
			}
			if _, ok := rows[c.lost]; ok {
				t.Errorf("%s stored; its page failed", c.lost)
			}
			if got := len(rowsByNativeID(t, st, TypeAppMeshGatewayRoute)); got != c.wantLast {
				t.Errorf("gateway routes (the last phase) = %d; want %d", got, c.wantLast)
			}
		})
	}
}

// A repeated NextToken ends the listing as an error, keeping its pages.
func TestScanAppMesh_RepeatedTokenKeepsPages(t *testing.T) {
	st := newTestStore(t)
	calls := newAMCalls(t)
	handlers := amHandlers(calls)
	k := amKindOf(t, "ListRoutes")
	twoPages := handlers[k.op]
	handlers[k.op] = func(in any) (any, error) {
		out, err := twoPages(in)
		out.(*appmesh.ListRoutesOutput).NextToken = sdkaws.String("same")
		return out, err
	}

	total, _, err := amScan(context.Background(), st, handlers)
	if !errors.Is(err, errRepeatedNextToken) {
		t.Fatalf("err = %v; want errRepeatedNextToken", err)
	}
	if total != amTestTotal {
		t.Errorf("total = %d; want %d", total, amTestTotal)
	}
	if n := len(rowsByNativeID(t, st, TypeAppMeshRoute)); n != len(k.arns(t)) {
		t.Errorf("routes = %d; want %d", n, len(k.arns(t)))
	}
}

// One parent's failed child listing costs the other parents none of their
// rows: a parent deleted since it was listed is skipped silently, a denial
// warns once under the op, and any other error is returned.
func TestScanAppMesh_PerParentListingFailures(t *testing.T) {
	for _, c := range []struct {
		op, parentField, parent string
	}{
		{"ListVirtualNodes", "MeshName", "mesh-tagged"},
		{"ListVirtualRouters", "MeshName", "mesh-tagged"},
		{"ListRoutes", "VirtualRouterName", "vr-tagged"},
		{"ListGatewayRoutes", "VirtualGatewayName", "vg-bare"},
	} {
		for _, e := range []struct {
			name      string
			err       error
			code      string
			wantWarns int
		}{
			{"parent deleted", amTestGone, "", 0},
			{"denied", amTestDenied, "", 1},
			{"other error", amTestBad, "BadRequestException", 0},
		} {
			t.Run(c.op+"/"+e.name, func(t *testing.T) {
				st := newTestStore(t)
				warns := countOpWarnings(st, "appmesh:"+c.op)
				calls := newAMCalls(t)
				handlers := amHandlers(calls)
				list := handlers[c.op]
				handlers[c.op] = func(in any) (any, error) {
					if sv(bacField[*string](in, c.parentField)) == c.parent {
						calls.record(c.op, in)
						return nil, e.err
					}
					return list(in)
				}

				total, _, err := amScan(context.Background(), st, handlers)
				if e.code == "" {
					if err != nil {
						t.Fatalf("err = %v; want nil", err)
					}
				} else {
					assertGlueOutcome(t, err, outcomeError)
					assertAPICode(t, err, e.code)
				}
				k := amKindOf(t, c.op)
				var lost []string
				for _, arn := range k.arns(t) {
					if strings.Contains(arn, "/"+c.parent+"/") {
						lost = append(lost, arn)
					}
				}
				rows := rowsByNativeID(t, st, k.typ)
				if len(rows) != len(k.arns(t))-len(lost) {
					t.Errorf("%s rows = %d; want %d", k.typ, len(rows), len(k.arns(t))-len(lost))
				}
				for _, arn := range lost {
					if _, ok := rows[arn]; ok {
						t.Errorf("%s stored; its parent failed", arn)
					}
				}
				wantTotal := amTestTotal - len(lost)
				if c.op == "ListVirtualRouters" {
					// The failed mesh's routers' routes are not listed either.
					wantTotal -= 2 * 2
				}
				if total != wantTotal {
					t.Errorf("total = %d; want %d", total, wantTotal)
				}
				if *warns != e.wantWarns {
					t.Errorf("warnings = %d; want %d", *warns, e.wantWarns)
				}
			})
		}
	}
}

// An element without an ARN is not stored, is not sent to the tag op, and is
// warned about once under its listing, keyed by mesh/name or by the fallback
// text when it has no name. A mesh, router or gateway without a name is
// stored under its ARN (a mesh named by it) without its children read, with
// one warning naming the empty field.
func TestScanAppMesh_ElementWithoutARNOrName(t *testing.T) {
	noNameMesh := amTestMeshARN(testAccountID, "x")
	noNameRouter := amTestMeshARN(testAccountID, "mesh-tagged") + "/virtualRouter/x"
	noNameGateway := amTestMeshARN(testAccountID, "mesh-tagged") + "/virtualGateway/x"
	for _, c := range []struct {
		name, op, key string
		answer        func(in any) any
		lost          int
		stored        string
		storedName    string
		storedType    string
		// field is the empty name field a parent's warning names.
		field string
	}{
		{
			"node", "ListVirtualNodes", "mesh-tagged/vn-x",
			func(in any) any {
				if sv(bacField[*string](in, "MeshName")) != "mesh-tagged" {
					return &appmesh.ListVirtualNodesOutput{}
				}
				return &appmesh.ListVirtualNodesOutput{VirtualNodes: []amtypes.VirtualNodeRef{{MeshName: sdkaws.String("mesh-tagged"), VirtualNodeName: sdkaws.String("vn-x")}}}
			}, 4, "", "", "", "",
		},
		{
			"node with no name", "ListVirtualNodes", amNoIdentifier,
			func(in any) any {
				if sv(bacField[*string](in, "MeshName")) != "mesh-tagged" {
					return &appmesh.ListVirtualNodesOutput{}
				}
				return &appmesh.ListVirtualNodesOutput{VirtualNodes: []amtypes.VirtualNodeRef{{}}}
			}, 4, "", "", "", "",
		},
		{
			"mesh without a name", "ListMeshes", noNameMesh,
			func(any) any {
				return &appmesh.ListMeshesOutput{Meshes: []amtypes.MeshRef{{Arn: sdkaws.String(noNameMesh)}}}
			},
			// Replaces both meshes: one stored, no children read.
			amTestTotal - 1, noNameMesh, noNameMesh, TypeAppMeshMesh, "MeshName",
		},
		{
			"router without a name", "ListVirtualRouters", noNameRouter,
			func(in any) any {
				if sv(bacField[*string](in, "MeshName")) != "mesh-tagged" {
					return &appmesh.ListVirtualRoutersOutput{}
				}
				return &appmesh.ListVirtualRoutersOutput{VirtualRouters: []amtypes.VirtualRouterRef{{Arn: sdkaws.String(noNameRouter)}}}
			},
			// Replaces the four routers and their eight routes: one stored.
			4 + 8 - 1, noNameRouter, "", TypeAppMeshVirtualRouter, "VirtualRouterName",
		},
		{
			"gateway without a name", "ListVirtualGateways", noNameGateway,
			func(in any) any {
				if sv(bacField[*string](in, "MeshName")) != "mesh-tagged" {
					return &appmesh.ListVirtualGatewaysOutput{}
				}
				return &appmesh.ListVirtualGatewaysOutput{VirtualGateways: []amtypes.VirtualGatewayRef{{Arn: sdkaws.String(noNameGateway)}}}
			},
			// Replaces the four gateways and their eight gateway routes: one stored.
			4 + 8 - 1, noNameGateway, "", TypeAppMeshVirtualGateway, "VirtualGatewayName",
		},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			calls := newAMCalls(t)
			handlers := amHandlers(calls)
			handlers[c.op] = func(in any) (any, error) {
				calls.record(c.op, in)
				return c.answer(in), nil
			}

			total, _, err := amScan(context.Background(), st, handlers)
			if err != nil {
				t.Fatalf("scan: %v", err)
			}
			if want := amTestTotal - c.lost; total != want {
				t.Errorf("total = %d; want %d", total, want)
			}
			w := warns()
			if len(w) != 1 || w[0].Service != "appmesh:"+c.op || !strings.Contains(w[0].Message, c.key) {
				t.Fatalf("warnings = %v; want one under appmesh:%s naming %s", w, c.op, c.key)
			}
			if !strings.Contains(w[0].Message, c.field) {
				t.Errorf("warning %q; want it to name the empty %s", w[0].Message, c.field)
			}
			if c.stored == "" {
				if n := len(rowsByNativeID(t, st, TypeAppMeshVirtualNode)); n != 0 {
					t.Errorf("virtual nodes stored = %d; want 0", n)
				}
			} else {
				r, ok := rowsByNativeID(t, st, c.storedType)[c.stored]
				if !ok || sv(r.Name) != c.storedName {
					t.Errorf("%s stored %v, Name %q; want stored named %q", c.stored, ok, sv(r.Name), c.storedName)
				}
				if !slices.Contains(calls.sortedTagARNs(), c.stored) {
					t.Errorf("tag ARNs lack %s", c.stored)
				}
			}
			if slices.Contains(calls.sortedTagARNs(), "") {
				t.Error("an empty ARN was sent to ListTagsForResource")
			}
		})
	}
}

// A mesh another account shares with this one: its child listings are sent
// its MeshOwner, and a tag read of its rows that is denied or not found
// keeps them untagged without a warning, while this account's rows are read
// as usual.
func TestScanAppMesh_SharedMesh(t *testing.T) {
	shared := amTestMeshARN(amTestOtherAccount, "mesh-shared")
	for _, c := range []struct {
		name string
		err  error
	}{
		{"denied", amTestDenied},
		{"not found", amTestGone},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			calls := newAMCalls(t)
			handlers := amHandlers(calls)
			owner := amTestOtherAccount
			name := "mesh-shared"
			handlers["ListMeshes"] = func(in any) (any, error) {
				calls.record("ListMeshes", in)
				return &appmesh.ListMeshesOutput{Meshes: []amtypes.MeshRef{{Arn: &shared, MeshName: &name, MeshOwner: &owner, ResourceOwner: &owner}}}, nil
			}
			list := handlers["ListTagsForResource"]
			handlers["ListTagsForResource"] = func(in any) (any, error) {
				if strings.HasPrefix(sv(in.(*appmesh.ListTagsForResourceInput).ResourceArn), shared) {
					calls.record("ListTagsForResource", in)
					return nil, c.err
				}
				return list(in)
			}

			total, _, err := amScan(context.Background(), st, handlers)
			if err != nil {
				t.Fatalf("scan: %v", err)
			}
			// The mesh, its 8 per-mesh children, 4 routes and 4 gateway routes.
			if want := 1 + 8 + 4 + 4; total != want {
				t.Errorf("total = %d; want %d", total, want)
			}
			if w := warns(); len(w) != 0 {
				t.Errorf("warnings = %v; want none", w)
			}
			for _, op := range []string{"ListVirtualGateways", "ListVirtualNodes", "ListVirtualRouters", "ListVirtualServices", "ListRoutes", "ListGatewayRoutes"} {
				for _, o := range calls.owners[op] {
					if o != amTestOtherAccount {
						t.Errorf("%s MeshOwner = %q; want %s", op, o, amTestOtherAccount)
					}
				}
			}
			r, ok := rowsByNativeID(t, st, TypeAppMeshRoute)[shared+"/virtualRouter/vr-tagged/route/rt-tagged"]
			if !ok || r.TagsJSON != nil || r.AccountID != testAccountID {
				t.Errorf("shared route stored %v, account %q, TagsJSON %v; want stored untagged under this account", ok, r.AccountID, r.TagsJSON)
			}
		})
	}
}

// A virtual node this account owns (ResourceOwner) in a mesh another account
// shares, its ARN naming the mesh owner's account, is read as this account's
// own: a denial keeps it untagged and warns once, a not-found drops it. The
// shared mesh's other rows, owned by the mesh owner, stay silent.
func TestScanAppMesh_OwnRowInSharedMesh(t *testing.T) {
	shared := amTestMeshARN(amTestOtherAccount, "mesh-shared")
	node := shared + "/virtualNode/vn-own"
	for _, c := range []struct {
		name      string
		err       error
		wantKept  bool
		wantWarns []string
	}{
		{"denied", amTestDenied, true, []string{amTagsOp}},
		{"not found", amTestGone, false, nil},
	} {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			warns := rhWarnRecorder(st)
			calls := newAMCalls(t)
			handlers := amHandlers(calls)
			owner, name, mesh := amTestOtherAccount, "mesh-shared", "mesh-shared"
			handlers["ListMeshes"] = func(in any) (any, error) {
				calls.record("ListMeshes", in)
				return &appmesh.ListMeshesOutput{Meshes: []amtypes.MeshRef{{Arn: &shared, MeshName: &name, MeshOwner: &owner, ResourceOwner: &owner}}}, nil
			}
			handlers["ListVirtualNodes"] = func(in any) (any, error) {
				calls.record("ListVirtualNodes", in)
				return &appmesh.ListVirtualNodesOutput{VirtualNodes: []amtypes.VirtualNodeRef{{
					Arn: sdkaws.String(node), VirtualNodeName: sdkaws.String("vn-own"), MeshName: &mesh,
					MeshOwner: &owner, ResourceOwner: sdkaws.String(testAccountID),
				}}}, nil
			}
			list := handlers["ListTagsForResource"]
			handlers["ListTagsForResource"] = func(in any) (any, error) {
				if strings.HasPrefix(sv(in.(*appmesh.ListTagsForResourceInput).ResourceArn), shared) {
					calls.record("ListTagsForResource", in)
					return nil, c.err
				}
				return list(in)
			}

			if _, _, err := amScan(context.Background(), st, handlers); err != nil {
				t.Fatalf("scan: %v", err)
			}
			w := warns()
			if got := amWarnings(w); !slices.Equal(got, c.wantWarns) {
				t.Fatalf("warnings = %v; want %v", w, c.wantWarns)
			}
			if len(w) == 1 && !strings.Contains(w[0].Message, node) {
				t.Errorf("warning %q; want it to name %s", w[0].Message, node)
			}
			r, ok := rowsByNativeID(t, st, TypeAppMeshVirtualNode)[node]
			if ok != c.wantKept || (ok && r.TagsJSON != nil) {
				t.Errorf("own node stored %v, TagsJSON %v; want stored %v, untagged", ok, r.TagsJSON, c.wantKept)
			}
		})
	}
}

// A GovCloud or China ARN reaches the tag op unchanged.
func TestScanAppMesh_TagReadsKeepPartitionARNs(t *testing.T) {
	for _, arn := range []string{
		"arn:aws-us-gov:appmesh:us-gov-west-1:" + testAccountID + ":mesh/m-gov",
		"arn:aws-cn:appmesh:cn-north-1:" + testAccountID + ":mesh/m-cn",
	} {
		t.Run(arn, func(t *testing.T) {
			st := newTestStore(t)
			calls := newAMCalls(t)
			calls.tags[arn] = map[string]string{"env": "prod"}
			handlers := amHandlers(calls)
			handlers["ListMeshes"] = func(in any) (any, error) {
				calls.record("ListMeshes", in)
				return &appmesh.ListMeshesOutput{Meshes: []amtypes.MeshRef{{Arn: sdkaws.String(arn), MeshName: sdkaws.String("m"), MeshOwner: sdkaws.String(testAccountID)}}}, nil
			}

			if _, _, err := amScan(context.Background(), st, handlers); err != nil {
				t.Fatalf("scan: %v", err)
			}
			if !slices.Contains(calls.sortedTagARNs(), arn) {
				t.Fatalf("ListTagsForResource ARNs = %v; want %s among them", calls.sortedTagARNs(), arn)
			}
			assertTagsMap(t, rowsByNativeID(t, st, TypeAppMeshMesh)[arn].TagsJSON, map[string]string{"env": "prod"})
		})
	}
}

// Once ctx ends, the stop is one member of the error, although the phase in
// flight saw it in its listing and its tag reads, and the later phases are
// not run.
func TestScanAppMesh_ContextEndReportedOnce(t *testing.T) {
	st := newTestStore(t)
	calls := newAMCalls(t)
	handlers := amHandlers(calls)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	list := handlers["ListVirtualNodes"]
	handlers["ListVirtualNodes"] = func(in any) (any, error) {
		if bacField[*string](in, "NextToken") != nil {
			cancel()
			return nil, context.Canceled
		}
		return list(in)
	}

	_, _, err := amScan(ctx, st, handlers)
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
	if n := calls.count("ListVirtualRouters"); n != 0 {
		t.Errorf("ListVirtualRouters called %d times after ctx ended", n)
	}
}

// An NXDOMAIN after rows were stored is a failure of this scan, not a region
// gap: the counts are kept. With nothing stored it stays region-unavailable.
func TestScanAppMesh_NXDOMAIN(t *testing.T) {
	nx := &net.DNSError{Err: "no such host", Name: "appmesh.us-east-1.amazonaws.com", IsNotFound: true}
	t.Run("one listing after others stored rows", func(t *testing.T) {
		st := newTestStore(t)
		handlers := amHandlers(newAMCalls(t))
		handlers["ListGatewayRoutes"] = func(any) (any, error) { return nil, nx }
		total, _, err := amScan(context.Background(), st, handlers)
		assertGlueOutcome(t, err, outcomeError)
		if want := amTestTotal - 8; total != want {
			t.Errorf("total = %d; want the %d other rows counted", total, want)
		}
	})
	t.Run("tag read after the listing", func(t *testing.T) {
		st := newTestStore(t)
		calls := newAMCalls(t)
		calls.errs[amKindOf(t, "ListVirtualGateways").arns(t)[2]] = nx
		_, _, err := amScan(context.Background(), st, amHandlers(calls))
		assertGlueOutcome(t, err, outcomeError)
	})
	t.Run("nothing stored", func(t *testing.T) {
		st := newTestStore(t)
		warns := rhWarnRecorder(st)
		acct := newTestAccount(testAccountID)
		acct.cfg = opStubConfig(nil, nx)
		_, _, err := scanAppMesh(context.Background(), acct, testRegion, st, testScanID)
		assertGlueOutcome(t, err, outcomeUnavailable)
		if w := warns(); len(w) != 0 {
			t.Errorf("warnings = %v; want none", w)
		}
	})
}
