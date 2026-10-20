package aws

import (
	"context"
	"errors"
	"io"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"

	"github.com/icearp/disco-cli/store"
)

const (
	elxSnap1       = "snap-lx-0001"
	elxSnap2       = "snap-lx-0002"
	elxSnap3       = "snap-lx-0003"
	elxSnapNoOwner = "snap-lx-noowner"
	elxImg1        = "ami-lx-0001"
	elxImg2        = "ami-lx-0002"
	elxImg3        = "ami-lx-0003"
	elxRS1         = "rs-lx-0001"
	elxRS2         = "rs-lx-0002"
	elxRS3         = "rs-lx-0003"
	elxVAI1        = "vai-lx-0001"
	elxVAI2        = "vai-lx-0002"
	elxCheck1      = "asc-lx-0001"
	elxCheck2      = "asc-lx-0002"
	elxVpc1        = "vpc-lx-0001"
	elxVpc2        = "vpc-lx-0002"
	elxRtb1        = "rtb-lx-0001"
	elxRtb2        = "rtb-lx-0002"
	elxPageNext    = "p2"
	elxOtherScan   = "scan-lx-other"
	elxOtherAcct   = "210987654321"
	elxOrgARN      = "arn:aws:organizations::123456789012:organization/o-lx"
)

var (
	elxDenied  = apiErr("UnauthorizedOperation", "You are not authorized to perform this operation. User: arn:aws:iam::123456789012:user/x is not authorized to perform: ec2:Describe")
	elxGap     = apiErr("InvalidAction", "The action is not valid for this web service")
	errElxTest = errors.New("ec2 late extra test failure")
	elxTime    = time.Date(2026, 3, 4, 5, 6, 7, 0, time.UTC)
)

func elxARN(kind, id string) string { return ec2ARN(testRegion, testAccountID, kind, id) }

func elxClient(handlers map[string]func(in any) (any, error)) ec2API {
	return ec2.NewFromConfig(opStubConfig(handlers, elxDenied))
}

func elxRun(run func(context.Context, ec2API, *account, string, *store.Store, string) (int, int, error),
	st *store.Store, handlers map[string]func(in any) (any, error),
) (int, int, error) {
	return run(context.Background(), elxClient(handlers), newTestAccount(testAccountID), testRegion, st, testScanID)
}

// elxSeedParents stores parents 1 and 2 of typ (kind) live, parent 3 with
// goneStatus when it is set, and two that must not be queried: one in another
// Region and one stored by another scan.
func elxSeedParents(t *testing.T, st *store.Store, typ, kind, goneStatus string, ids [3]string) {
	t.Helper()
	tgxSeedParent(t, st, typ, elxARN(kind, ids[0]), testRegion, "available", testScanID)
	tgxSeedParent(t, st, typ, elxARN(kind, ids[1]), testRegion, "pending", testScanID)
	if goneStatus != "" {
		tgxSeedParent(t, st, typ, elxARN(kind, ids[2]), testRegion, goneStatus, testScanID)
	}
	tgxSeedParent(t, st, typ, ec2ARN("eu-west-1", testAccountID, kind, ids[0]+"-eu"), "eu-west-1", "available", testScanID)
	insertTestScan(t, st, elxOtherScan, "completed")
	tgxSeedParent(t, st, typ, elxARN(kind, ids[0]+"-old"), testRegion, "available", elxOtherScan)
}

// elxFanPhase is one per-parent fan-out phase.
type elxFanPhase struct {
	name, op, sdkOp, typ string
	// goneCodes are the per-parent codes skipped with a warning.
	goneCodes []string
	seed      func(*testing.T, *store.Store)
	// extra stores a third live parent, so a skip is tallied across two.
	extra    func(*testing.T, *store.Store)
	handler  func(*parentCalls) func(any) (any, error)
	empty    any
	run      func(context.Context, ec2API, *account, string, *store.Store, string) (int, int, error)
	parentID func(in any) string
	// queried are the parents the phase must call; rows the NativeIDs it
	// stores, each under parents[i]; kept is the second live parent's row.
	queried []string
	rows    []string
	parents []string
	kept    string
	check   func(*testing.T, map[string]store.Resource)
	// rowPerParent marks an attribute phase, which stores a row for every
	// parent queried, whatever the attribute holds.
	rowPerParent bool
}

func elxSnapAttrID(id string) string {
	return snapshotAttributeNativeID(elxARN("snapshot", id), ec2types.SnapshotAttributeNameCreateVolumePermission)
}

func elxImgAttrID(id string) string {
	return imageAttributeNativeID(elxARN("image", id), ec2types.ImageAttributeNameLaunchPermission)
}

func elxAssocID(rs, vpc string) string {
	return routeServerAssociationNativeID(elxARN("route-server", rs), vpc)
}

func elxPropID(rs, rtb string) string {
	return routeServerPropagationNativeID(elxARN("route-server", rs), rtb)
}

func elxSnapHandler(calls *parentCalls) func(any) (any, error) {
	return func(in any) (any, error) {
		p := in.(*ec2.DescribeSnapshotAttributeInput)
		calls.add(sv(p.SnapshotId))
		if p.Attribute != ec2types.SnapshotAttributeNameCreateVolumePermission {
			return nil, apiErr("InvalidParameterValue", "attribute must be createVolumePermission")
		}
		out := &ec2.DescribeSnapshotAttributeOutput{SnapshotId: p.SnapshotId}
		if sv(p.SnapshotId) == elxSnap1 {
			out.CreateVolumePermissions = []ec2types.CreateVolumePermission{{Group: ec2types.PermissionGroupAll}, {UserId: ptrStr(elxOtherAcct)}}
			// Not asked for, so it must not reach the stored payload.
			out.ProductCodes = []ec2types.ProductCode{{ProductCodeId: ptrStr("pc-lx")}}
		}
		return out, nil
	}
}

func elxImgHandler(calls *parentCalls) func(any) (any, error) {
	return func(in any) (any, error) {
		p := in.(*ec2.DescribeImageAttributeInput)
		calls.add(sv(p.ImageId))
		if p.Attribute != ec2types.ImageAttributeNameLaunchPermission {
			return nil, apiErr("InvalidParameterValue", "attribute must be launchPermission")
		}
		out := &ec2.DescribeImageAttributeOutput{ImageId: p.ImageId}
		if sv(p.ImageId) == elxImg1 {
			out.LaunchPermissions = []ec2types.LaunchPermission{{UserId: ptrStr(elxOtherAcct)}, {OrganizationArn: ptrStr(elxOrgARN)}}
			// Not asked for, so it must not reach the stored payload.
			out.Description = &ec2types.AttributeValue{Value: ptrStr("lx")}
		}
		return out, nil
	}
}

func elxAssocHandler(calls *parentCalls) func(any) (any, error) {
	return func(in any) (any, error) {
		p := in.(*ec2.GetRouteServerAssociationsInput)
		calls.add(sv(p.RouteServerId))
		if sv(p.RouteServerId) == elxRS1 {
			return &ec2.GetRouteServerAssociationsOutput{RouteServerAssociations: []ec2types.RouteServerAssociation{
				{RouteServerId: p.RouteServerId, VpcId: ptrStr(elxVpc1), State: ec2types.RouteServerAssociationStateAssociated},
				{RouteServerId: p.RouteServerId, State: ec2types.RouteServerAssociationStateAssociated},
			}}, nil
		}
		return &ec2.GetRouteServerAssociationsOutput{RouteServerAssociations: []ec2types.RouteServerAssociation{
			{RouteServerId: p.RouteServerId, VpcId: ptrStr(elxVpc2), State: ec2types.RouteServerAssociationStateAssociating},
		}}, nil
	}
}

func elxPropHandler(calls *parentCalls) func(any) (any, error) {
	return func(in any) (any, error) {
		p := in.(*ec2.GetRouteServerPropagationsInput)
		calls.add(sv(p.RouteServerId))
		if sv(p.RouteServerId) == elxRS1 {
			return &ec2.GetRouteServerPropagationsOutput{RouteServerPropagations: []ec2types.RouteServerPropagation{
				{RouteServerId: p.RouteServerId, RouteTableId: ptrStr(elxRtb1), State: ec2types.RouteServerPropagationStateAvailable},
				{RouteServerId: p.RouteServerId, State: ec2types.RouteServerPropagationStateAvailable},
			}}, nil
		}
		return &ec2.GetRouteServerPropagationsOutput{RouteServerPropagations: []ec2types.RouteServerPropagation{
			{RouteServerId: p.RouteServerId, RouteTableId: ptrStr(elxRtb2), State: ec2types.RouteServerPropagationStatePending},
		}}, nil
	}
}

func elxSeedSnapshots(t *testing.T, st *store.Store) {
	elxSeedParents(t, st, TypeEC2Snapshot, "snapshot", "error", [3]string{elxSnap1, elxSnap2, elxSnap3})
}

func elxSeedImages(t *testing.T, st *store.Store) {
	elxSeedParents(t, st, TypeEC2Image, "image", "deregistered", [3]string{elxImg1, elxImg2, elxImg3})
}

func elxSeedRouteServers(t *testing.T, st *store.Store) {
	elxSeedParents(t, st, TypeEC2RouteServer, "route-server", "deleted", [3]string{elxRS1, elxRS2, elxRS3})
}

var elxFanPhases = []elxFanPhase{
	{
		name: "snapshot attributes", op: ec2SnapshotAttributeOp, sdkOp: "DescribeSnapshotAttribute", typ: TypeEC2SnapshotAttribute,
		goneCodes: []string{"InvalidSnapshot.NotFound", "IncorrectState"},
		seed:      elxSeedSnapshots,
		extra: func(t *testing.T, st *store.Store) {
			tgxSeedParent(t, st, TypeEC2Snapshot, elxARN("snapshot", "snap-lx-0009"), testRegion, "completed", testScanID)
		},
		handler: elxSnapHandler, empty: &ec2.DescribeSnapshotAttributeOutput{}, run: scanEC2SnapshotAttributes, rowPerParent: true,
		parentID: func(in any) string { return sv(in.(*ec2.DescribeSnapshotAttributeInput).SnapshotId) },
		queried:  []string{elxSnap1, elxSnap2},
		rows:     []string{elxSnapAttrID(elxSnap1), elxSnapAttrID(elxSnap2)},
		parents:  []string{elxARN("snapshot", elxSnap1), elxARN("snapshot", elxSnap2)},
		kept:     elxSnapAttrID(elxSnap2),
		check: func(t *testing.T, rows map[string]store.Resource) {
			attrs := ipxAttrs(t, rows[elxSnapAttrID(elxSnap1)])
			perms, _ := attrs["CreateVolumePermissions"].([]any)
			if len(perms) != 2 || perms[0].(map[string]any)["UserId"] != elxOtherAcct || perms[1].(map[string]any)["Group"] != "all" {
				t.Errorf("snapshot 1 CreateVolumePermissions = %v, want %s then group all (sorted)", perms, elxOtherAcct)
			}
			elxAssertKeys(t, attrs, "SnapshotId", "CreateVolumePermissions")
			if perms, _ := ipxAttrs(t, rows[elxSnapAttrID(elxSnap2)])["CreateVolumePermissions"].([]any); len(perms) != 0 {
				t.Errorf("snapshot 2 CreateVolumePermissions = %v, want none (row kept unshared)", perms)
			}
		},
	},
	{
		name: "image attributes", op: ec2ImageAttributeOp, sdkOp: "DescribeImageAttribute", typ: TypeEC2ImageAttribute,
		goneCodes: []string{"InvalidAMIID.NotFound", "InvalidAMIID.Unavailable"},
		seed:      elxSeedImages,
		extra: func(t *testing.T, st *store.Store) {
			tgxSeedParent(t, st, TypeEC2Image, elxARN("image", "ami-lx-0009"), testRegion, "available", testScanID)
		},
		handler: elxImgHandler, empty: &ec2.DescribeImageAttributeOutput{}, run: scanEC2ImageAttributes, rowPerParent: true,
		parentID: func(in any) string { return sv(in.(*ec2.DescribeImageAttributeInput).ImageId) },
		queried:  []string{elxImg1, elxImg2},
		rows:     []string{elxImgAttrID(elxImg1), elxImgAttrID(elxImg2)},
		parents:  []string{elxARN("image", elxImg1), elxARN("image", elxImg2)},
		kept:     elxImgAttrID(elxImg2),
		check: func(t *testing.T, rows map[string]store.Resource) {
			attrs := ipxAttrs(t, rows[elxImgAttrID(elxImg1)])
			perms, _ := attrs["LaunchPermissions"].([]any)
			if len(perms) != 2 || perms[0].(map[string]any)["OrganizationArn"] != elxOrgARN || perms[1].(map[string]any)["UserId"] != elxOtherAcct {
				t.Errorf("AMI 1 LaunchPermissions = %v, want %s then %s (sorted)", perms, elxOrgARN, elxOtherAcct)
			}
			elxAssertKeys(t, attrs, "ImageId", "LaunchPermissions")
		},
	},
	{
		name: "route server associations", op: ec2RouteServerAssociationsOp, sdkOp: "GetRouteServerAssociations", typ: TypeEC2RouteServerAssociation,
		goneCodes: []string{"InvalidRouteServerId.NotFound", "IncorrectState"},
		seed:      elxSeedRouteServers,
		extra: func(t *testing.T, st *store.Store) {
			tgxSeedParent(t, st, TypeEC2RouteServer, elxARN("route-server", "rs-lx-0009"), testRegion, "available", testScanID)
		},
		handler: elxAssocHandler, empty: &ec2.GetRouteServerAssociationsOutput{}, run: scanEC2RouteServerAssociations,
		parentID: func(in any) string { return sv(in.(*ec2.GetRouteServerAssociationsInput).RouteServerId) },
		queried:  []string{elxRS1, elxRS2},
		rows:     []string{elxAssocID(elxRS1, elxVpc1), elxAssocID(elxRS2, elxVpc2)},
		parents:  []string{elxARN("route-server", elxRS1), elxARN("route-server", elxRS2)},
		kept:     elxAssocID(elxRS2, elxVpc2),
		check: func(t *testing.T, rows map[string]store.Resource) {
			r := rows[elxAssocID(elxRS1, elxVpc1)]
			if sv(r.Name) != elxVpc1 || sv(r.Status) != "associated" || ipxAttrs(t, r)["VpcId"] != elxVpc1 {
				t.Errorf("association name/status/VpcId = %q/%q/%v, want %s/associated/%s", sv(r.Name), sv(r.Status), ipxAttrs(t, r)["VpcId"], elxVpc1, elxVpc1)
			}
		},
	},
	{
		name: "route server propagations", op: ec2RouteServerPropagationsOp, sdkOp: "GetRouteServerPropagations", typ: TypeEC2RouteServerPropagation,
		goneCodes: []string{"InvalidRouteServerId.NotFound", "IncorrectState"},
		seed:      elxSeedRouteServers,
		extra: func(t *testing.T, st *store.Store) {
			tgxSeedParent(t, st, TypeEC2RouteServer, elxARN("route-server", "rs-lx-0009"), testRegion, "available", testScanID)
		},
		handler: elxPropHandler, empty: &ec2.GetRouteServerPropagationsOutput{}, run: scanEC2RouteServerPropagations,
		parentID: func(in any) string { return sv(in.(*ec2.GetRouteServerPropagationsInput).RouteServerId) },
		queried:  []string{elxRS1, elxRS2},
		rows:     []string{elxPropID(elxRS1, elxRtb1), elxPropID(elxRS2, elxRtb2)},
		parents:  []string{elxARN("route-server", elxRS1), elxARN("route-server", elxRS2)},
		kept:     elxPropID(elxRS2, elxRtb2),
		check: func(t *testing.T, rows map[string]store.Resource) {
			r := rows[elxPropID(elxRS1, elxRtb1)]
			if sv(r.Name) != elxRtb1 || sv(r.Status) != "available" || ipxAttrs(t, r)["RouteTableId"] != elxRtb1 {
				t.Errorf("propagation name/status/RouteTableId = %q/%q/%v, want %s/available/%s", sv(r.Name), sv(r.Status), ipxAttrs(t, r)["RouteTableId"], elxRtb1, elxRtb1)
			}
		},
	},
}

func TestEC2LateExtraNativeIDShapes(t *testing.T) {
	cases := map[string]string{
		elxSnapAttrID(elxSnap1):                                            "arn:aws:ec2:us-east-1:123456789012:snapshot/snap-lx-0001/attribute/createVolumePermission",
		elxImgAttrID(elxImg1):                                              "arn:aws:ec2:us-east-1:123456789012:image/ami-lx-0001/attribute/launchPermission",
		elxAssocID(elxRS1, elxVpc1):                                        "arn:aws:ec2:us-east-1:123456789012:route-server/rs-lx-0001/association/vpc-lx-0001",
		elxPropID(elxRS1, elxRtb1):                                         "arn:aws:ec2:us-east-1:123456789012:route-server/rs-lx-0001/propagation/rtb-lx-0001",
		lockedSnapshotNativeID(testRegion, testAccountID, elxSnap1):        "arn:aws:ec2:us-east-1:123456789012:snapshot/snap-lx-0001/lock",
		vaLoggingConfigurationNativeID(testRegion, testAccountID, elxVAI1): "arn:aws:ec2:us-east-1:123456789012:verified-access-instance/vai-lx-0001/logging-configuration",
	}
	for got, want := range cases {
		if got != want {
			t.Errorf("NativeID = %q, want %q", got, want)
		}
	}
}

// Each fan-out phase queries only the live parents this scan stored in the
// Region and stores their rows under them.
func TestScanEC2LateExtraFanOut_StoresRowsUnderParents(t *testing.T) {
	for _, ph := range elxFanPhases {
		t.Run(ph.name, func(t *testing.T) {
			st := newTestStore(t)
			ph.seed(t, st)
			var calls parentCalls
			h := map[string]func(any) (any, error){ph.sdkOp: ph.handler(&calls)}
			total, inserted, err := elxRun(ph.run, st, h)
			if err != nil {
				t.Fatalf("run: %v", err)
			}
			if total != 2 || inserted != 2 {
				t.Errorf("total, inserted = %d, %d; want 2, 2", total, inserted)
			}
			total, inserted, err = elxRun(ph.run, st, h)
			if err != nil || total != 2 || inserted != 0 {
				t.Errorf("re-run total, inserted, err = %d, %d, %v; want 2, 0, nil", total, inserted, err)
			}
			if got := calls.distinct(); !slices.Equal(got, ph.queried) {
				t.Errorf("parents queried = %v, want %v (gone, other-Region and other-scan parents skipped)", got, ph.queried)
			}
			rows := rowsByNativeID(t, st, ph.typ)
			if got := nativeIDs(rows); !slices.Equal(got, ph.rows) {
				t.Fatalf("rows = %v, want %v", got, ph.rows)
			}
			for i, id := range ph.rows {
				if r := rows[id]; r.Type != ph.typ || sv(r.Region) != testRegion || r.ManagedByProvider {
					t.Errorf("%s type/region/managed = %s/%s/%v, want %s/%s/false", id, r.Type, sv(r.Region), r.ManagedByProvider, ph.typ, testRegion)
				}
				assertContainsNative(t, st, ph.parents[i], id)
			}
			ph.check(t, rows)
			assertContainsEdgesDeclared(t, st)
		})
	}
}

// With no stored parent nothing is called; a parent with nothing to list
// stores nothing, except an attribute phase, which keeps one row per parent.
func TestScanEC2LateExtraFanOut_Empty(t *testing.T) {
	for _, ph := range elxFanPhases {
		t.Run(ph.name+"/no parents", func(t *testing.T) {
			st := newTestStore(t)
			total, _, err := elxRun(ph.run, st, map[string]func(any) (any, error){
				ph.sdkOp: func(any) (any, error) { return nil, errElxTest },
			})
			if err != nil || total != 0 {
				t.Fatalf("total, err = %d, %v; want 0, nil", total, err)
			}
		})
	}
	for _, ph := range elxFanPhases {
		if ph.rowPerParent {
			continue
		}
		t.Run(ph.name+"/no children", func(t *testing.T) {
			st := newTestStore(t)
			ph.seed(t, st)
			total, _, err := elxRun(ph.run, st, map[string]func(any) (any, error){
				ph.sdkOp: func(any) (any, error) { return ph.empty, nil },
			})
			if err != nil || total != 0 {
				t.Fatalf("total, err = %d, %v; want 0, nil", total, err)
			}
			if n := len(rowsByNativeID(t, st, ph.typ)); n != 0 {
				t.Errorf("rows = %d, want 0", n)
			}
		})
	}
}

// elxFailAfterKept answers the second live parent with handler, and every
// other parent with failErr once the second has answered, so a hard failure
// cannot stop the fan-out before the kept row is listed.
func elxFailAfterKept(ph elxFanPhase, calls *parentCalls, failErr error) func(any) (any, error) {
	inner := ph.handler(calls)
	kept := make(chan struct{})
	var once sync.Once
	return func(in any) (any, error) {
		if ph.parentID(in) == ph.queried[1] {
			defer once.Do(func() { close(kept) })
			return inner(in)
		}
		select {
		case <-kept:
			return nil, failErr
		case <-time.After(5 * time.Second):
			return nil, errors.New("gate: kept parent never called")
		}
	}
}

// Per-parent errors: each skip code and a transient failure skip the parent
// with one warning, keeping the other parent's row; a denial warns once for
// every parent; anything else fails the phase.
func TestScanEC2LateExtraFanOut_PerParentErrors(t *testing.T) {
	for _, ph := range elxFanPhases {
		type tc struct {
			name     string
			failErr  error
			wantErr  error
			wantWarn int
		}
		cases := []tc{
			{"transient", io.EOF, nil, 1},
			{"denied", elxDenied, nil, 1},
			{"other", errElxTest, errElxTest, 0},
		}
		for _, code := range ph.goneCodes {
			cases = append(cases, tc{code, apiErr(code, "The parent is gone"), nil, 1})
		}
		for _, c := range cases {
			t.Run(ph.name+"/"+c.name, func(t *testing.T) {
				st := newTestStore(t)
				ph.seed(t, st)
				ph.extra(t, st)
				var calls parentCalls
				warnings := countOpWarnings(st, ph.op)
				total, _, err := elxRun(ph.run, st, map[string]func(any) (any, error){ph.sdkOp: elxFailAfterKept(ph, &calls, c.failErr)})
				if c.wantErr == nil && err != nil {
					t.Fatalf("err = %v, want nil", err)
				}
				if c.wantErr != nil && !errors.Is(err, c.wantErr) {
					t.Fatalf("err = %v, want %v", err, c.wantErr)
				}
				if *warnings != c.wantWarn {
					t.Errorf("warnings = %d, want %d", *warnings, c.wantWarn)
				}
				if _, ok := rowsByNativeID(t, st, ph.typ)[ph.kept]; !ok {
					t.Errorf("row %s missing, want it kept", ph.kept)
				}
				if total != 1 {
					t.Errorf("total = %d, want 1 (the kept parent's row)", total)
				}
			})
		}
	}
}

// elxWidePhase is one region-wide listing phase. page1 and page2 answer the
// first and second call; page1 must carry NextToken elxPageNext.
type elxWidePhase struct {
	name, op, sdkOp, typ string
	pageSize             int32
	seed                 func(*testing.T, *store.Store)
	page1, page2, empty  any
	maxResults           func(in any) (int32, *string)
	run                  func(context.Context, ec2API, *account, string, *store.Store, string) (int, int, error)
	rows                 []string
	// contained maps a row to the parent NativeID it must sit under.
	contained map[string]string
	check     func(*testing.T, map[string]store.Resource)
}

func elxLockID(id string) string { return lockedSnapshotNativeID(testRegion, testAccountID, id) }

func elxVALogID(id string) string {
	return vaLoggingConfigurationNativeID(testRegion, testAccountID, id)
}

var elxWidePhases = []elxWidePhase{
	{
		name: "locked snapshots", op: ec2LockedSnapshotsOp, sdkOp: "DescribeLockedSnapshots", typ: TypeEC2LockedSnapshot,
		pageSize: ec2LockedSnapshotsPageSize,
		seed: func(t *testing.T, st *store.Store) {
			tgxSeedParent(t, st, TypeEC2Snapshot, elxARN("snapshot", elxSnap1), testRegion, "completed", testScanID)
		},
		page1: &ec2.DescribeLockedSnapshotsOutput{NextToken: ptrStr(elxPageNext), Snapshots: []ec2types.LockedSnapshotsInfo{{
			SnapshotId: ptrStr(elxSnap1), OwnerId: ptrStr(testAccountID), LockState: ec2types.LockStateComplianceCooloff,
			LockCreatedOn: &elxTime, LockDuration: ptrInt32(7),
		}}},
		page2: &ec2.DescribeLockedSnapshotsOutput{Snapshots: []ec2types.LockedSnapshotsInfo{
			{SnapshotId: ptrStr(elxSnap2), OwnerId: ptrStr(testAccountID), LockState: ec2types.LockStateGovernance},
			{SnapshotId: ptrStr(elxSnap3), OwnerId: ptrStr(elxOtherAcct), LockState: ec2types.LockStateGovernance},
			{OwnerId: ptrStr(testAccountID), LockState: ec2types.LockStateGovernance},
			{SnapshotId: ptrStr(elxSnapNoOwner), LockState: ec2types.LockStateCompliance},
		}},
		empty: &ec2.DescribeLockedSnapshotsOutput{},
		maxResults: func(in any) (int32, *string) {
			p := in.(*ec2.DescribeLockedSnapshotsInput)
			return sv32(p.MaxResults), p.NextToken
		},
		run:       scanEC2LockedSnapshots,
		rows:      []string{elxLockID(elxSnap1), elxLockID(elxSnap2), elxLockID(elxSnapNoOwner)},
		contained: map[string]string{elxLockID(elxSnap1): elxARN("snapshot", elxSnap1)},
		check: func(t *testing.T, rows map[string]store.Resource) {
			r := rows[elxLockID(elxSnap1)]
			if sv(r.Status) != "compliance-cooloff" || sv(r.CreatedAt) != "2026-03-04T05:06:07Z" || ipxAttrs(t, r)["LockDuration"] != float64(7) {
				t.Errorf("lock status/createdAt/LockDuration = %q/%q/%v, want compliance-cooloff/2026-03-04T05:06:07Z/7",
					sv(r.Status), sv(r.CreatedAt), ipxAttrs(t, r)["LockDuration"])
			}
		},
	},
	{
		name: "verified access logging configurations", op: ec2VALoggingConfigurationsOp,
		sdkOp: "DescribeVerifiedAccessInstanceLoggingConfigurations", typ: TypeEC2VerifiedAccessInstanceLoggingConfiguration,
		pageSize: ec2VALoggingConfigurationsPageSize,
		seed: func(t *testing.T, st *store.Store) {
			tgxSeedParent(t, st, TypeEC2VerifiedAccessInstance, elxARN("verified-access-instance", elxVAI1), testRegion, "", testScanID)
		},
		page1: &ec2.DescribeVerifiedAccessInstanceLoggingConfigurationsOutput{
			NextToken: ptrStr(elxPageNext),
			LoggingConfigurations: []ec2types.VerifiedAccessInstanceLoggingConfiguration{{
				VerifiedAccessInstanceId: ptrStr(elxVAI1),
				AccessLogs: &ec2types.VerifiedAccessLogs{S3: &ec2types.VerifiedAccessLogS3Destination{
					Enabled: ptrBool(true), BucketName: ptrStr("lx-logs"),
				}},
			}},
		},
		page2: &ec2.DescribeVerifiedAccessInstanceLoggingConfigurationsOutput{
			LoggingConfigurations: []ec2types.VerifiedAccessInstanceLoggingConfiguration{
				{VerifiedAccessInstanceId: ptrStr(elxVAI2)},
				{},
			},
		},
		empty: &ec2.DescribeVerifiedAccessInstanceLoggingConfigurationsOutput{},
		maxResults: func(in any) (int32, *string) {
			p := in.(*ec2.DescribeVerifiedAccessInstanceLoggingConfigurationsInput)
			return sv32(p.MaxResults), p.NextToken
		},
		run:       scanEC2VerifiedAccessLoggingConfigurations,
		rows:      []string{elxVALogID(elxVAI1), elxVALogID(elxVAI2)},
		contained: map[string]string{elxVALogID(elxVAI1): elxARN("verified-access-instance", elxVAI1)},
		check: func(t *testing.T, rows map[string]store.Resource) {
			r := rows[elxVALogID(elxVAI1)]
			if r.ManagedByProvider {
				t.Errorf("logging configuration ManagedByProvider = true, want false: it is customer posture")
			}
			logs, _ := ipxAttrs(t, r)["AccessLogs"].(map[string]any)
			if s3, _ := logs["S3"].(map[string]any); s3["BucketName"] != "lx-logs" {
				t.Errorf("attrs AccessLogs = %v, want S3 bucket lx-logs", logs)
			}
		},
	},
	{
		name: "application status checks", op: ec2ApplicationStatusChecksOp, sdkOp: "DescribeApplicationStatusChecks", typ: TypeEC2ApplicationStatusCheck,
		pageSize: ec2ApplicationStatusChecksPageSize,
		seed:     func(*testing.T, *store.Store) {},
		page1: &ec2.DescribeApplicationStatusChecksOutput{NextToken: ptrStr(elxPageNext), ApplicationStatusChecks: []ec2types.ApplicationStatusCheckResponseObject{{
			ApplicationStatusCheckId: ptrStr(elxCheck1), CreationTime: &elxTime, Port: ptrInt32(8080),
			Tags: []ec2types.Tag{{Key: ptrStr("Name"), Value: ptrStr("web-health")}},
		}}},
		page2: &ec2.DescribeApplicationStatusChecksOutput{ApplicationStatusChecks: []ec2types.ApplicationStatusCheckResponseObject{
			{ApplicationStatusCheckId: ptrStr(elxCheck2)},
			{Port: ptrInt32(80)},
		}},
		empty: &ec2.DescribeApplicationStatusChecksOutput{},
		maxResults: func(in any) (int32, *string) {
			p := in.(*ec2.DescribeApplicationStatusChecksInput)
			return sv32(p.MaxResults), p.NextToken
		},
		run:  scanEC2ApplicationStatusChecks,
		rows: []string{elxARN("application-status-check", elxCheck1), elxARN("application-status-check", elxCheck2)},
		check: func(t *testing.T, rows map[string]store.Resource) {
			r := rows[elxARN("application-status-check", elxCheck1)]
			if sv(r.Name) != "web-health" || sv(r.CreatedAt) != "2026-03-04T05:06:07Z" || ipxAttrs(t, r)["Port"] != float64(8080) {
				t.Errorf("check name/createdAt/Port = %q/%q/%v, want web-health/2026-03-04T05:06:07Z/8080", sv(r.Name), sv(r.CreatedAt), ipxAttrs(t, r)["Port"])
			}
			if r.TagsJSON == nil || *r.TagsJSON != `{"Name":"web-health"}` {
				t.Errorf("check tags = %v, want the Name tag", r.TagsJSON)
			}
		},
	},
}

// elxWideHandler answers page1 then page2, checking the page size; fail2, when
// set, answers the second call instead.
func elxWideHandler(ph elxWidePhase, calls *int, fail2 error) func(any) (any, error) {
	return func(in any) (any, error) {
		*calls++
		size, token := ph.maxResults(in)
		if size != ph.pageSize {
			return nil, apiErr("InvalidParameterValue", "MaxResults must be set to the page cap")
		}
		if token == nil {
			return ph.page1, nil
		}
		if fail2 != nil {
			return nil, fail2
		}
		return ph.page2, nil
	}
}

// Each region-wide phase reads both pages, drops id-less (and, for locks,
// other-owner) elements and links a row under its parent only when this scan
// stored the parent.
func TestScanEC2LateExtraWide_StoresBothPages(t *testing.T) {
	for _, ph := range elxWidePhases {
		t.Run(ph.name, func(t *testing.T) {
			st := newTestStore(t)
			ph.seed(t, st)
			calls := 0
			h := map[string]func(any) (any, error){ph.sdkOp: elxWideHandler(ph, &calls, nil)}
			total, inserted, err := elxRun(ph.run, st, h)
			if err != nil {
				t.Fatalf("run: %v", err)
			}
			if n := len(ph.rows); total != n || inserted != n || calls != 2 {
				t.Errorf("total, inserted, calls = %d, %d, %d; want %d, %d, 2", total, inserted, calls, n, n)
			}
			rows := rowsByNativeID(t, st, ph.typ)
			if got := nativeIDs(rows); !slices.Equal(got, ph.rows) {
				t.Fatalf("rows = %v, want %v", got, ph.rows)
			}
			for _, id := range ph.rows {
				if r := rows[id]; r.Type != ph.typ || sv(r.Region) != testRegion {
					t.Errorf("%s type/region = %s/%s, want %s/%s", id, r.Type, sv(r.Region), ph.typ, testRegion)
				}
			}
			for child, parent := range ph.contained {
				assertContainsNative(t, st, parent, child)
			}
			ph.check(t, rows)
			assertContainsEdgesDeclared(t, st)
		})
	}
}

// Errors: a denial or a feature gap on the first page stores nothing and
// returns nil (the denial warns once); after a page, a denial keeps that page
// and warns, and any other error, a gap code included, keeps it and fails.
func TestScanEC2LateExtraWide_Errors(t *testing.T) {
	cases := []struct {
		name            string
		first, second   error
		wantErr         error
		wantRows, warns int
	}{
		{"denied first page", elxDenied, nil, nil, 0, 1},
		{"gap first page", elxGap, nil, nil, 0, 0},
		{"denied second page", nil, elxDenied, nil, 1, 1},
		{"gap second page", nil, elxGap, elxGap, 1, 0},
		{"other second page", nil, errElxTest, errElxTest, 1, 0},
	}
	for _, ph := range elxWidePhases {
		for _, c := range cases {
			t.Run(ph.name+"/"+c.name, func(t *testing.T) {
				st := newTestStore(t)
				ph.seed(t, st)
				warnings := countOpWarnings(st, ph.op)
				calls := 0
				inner := elxWideHandler(ph, &calls, c.second)
				_, _, err := elxRun(ph.run, st, map[string]func(any) (any, error){ph.sdkOp: func(in any) (any, error) {
					if c.first != nil {
						return nil, c.first
					}
					return inner(in)
				}})
				if c.wantErr == nil && err != nil {
					t.Fatalf("err = %v, want nil", err)
				}
				if c.wantErr != nil && !errors.Is(err, c.wantErr) {
					t.Fatalf("err = %v, want %v", err, c.wantErr)
				}
				if n := len(rowsByNativeID(t, st, ph.typ)); n != c.wantRows {
					t.Errorf("rows = %d, want %d", n, c.wantRows)
				}
				if *warnings != c.warns {
					t.Errorf("warnings = %d, want %d", *warnings, c.warns)
				}
			})
		}
	}
}

// An empty listing stores nothing; a repeated NextToken ends the loop.
func TestScanEC2LateExtraWide_EmptyAndRepeatedToken(t *testing.T) {
	for _, ph := range elxWidePhases {
		t.Run(ph.name+"/empty", func(t *testing.T) {
			st := newTestStore(t)
			total, _, err := elxRun(ph.run, st, map[string]func(any) (any, error){ph.sdkOp: func(any) (any, error) { return ph.empty, nil }})
			if err != nil || total != 0 {
				t.Fatalf("total, err = %d, %v; want 0, nil", total, err)
			}
		})
		t.Run(ph.name+"/repeated token", func(t *testing.T) {
			st := newTestStore(t)
			calls := 0
			_, _, err := elxRun(ph.run, st, map[string]func(any) (any, error){ph.sdkOp: func(any) (any, error) {
				calls++
				if calls > 3 {
					return nil, errElxTest
				}
				return ph.page1, nil
			}})
			if err != nil {
				t.Fatalf("err = %v, want nil", err)
			}
			if calls != 2 {
				t.Errorf("calls = %d, want 2 (stop when the token repeats)", calls)
			}
		})
	}
}

// elxTopLevelHandlers answers the parent listings of every new phase, the new
// phases' own ops, and nothing else: scanEC2 runs every other EC2 op against
// the denial.
func elxTopLevelHandlers() map[string]func(in any) (any, error) {
	h := map[string]func(in any) (any, error){
		"DescribeSnapshots": func(any) (any, error) {
			return &ec2.DescribeSnapshotsOutput{Snapshots: []ec2types.Snapshot{{SnapshotId: ptrStr(elxSnap1), State: ec2types.SnapshotStateCompleted}}}, nil
		},
		"DescribeImages": func(any) (any, error) {
			return &ec2.DescribeImagesOutput{Images: []ec2types.Image{{ImageId: ptrStr(elxImg1), State: ec2types.ImageStateAvailable}}}, nil
		},
		"DescribeRouteServers": func(any) (any, error) {
			return &ec2.DescribeRouteServersOutput{RouteServers: []ec2types.RouteServer{{RouteServerId: ptrStr(elxRS1), State: ec2types.RouteServerStateAvailable}}}, nil
		},
		"DescribeVerifiedAccessInstances": func(any) (any, error) {
			return &ec2.DescribeVerifiedAccessInstancesOutput{VerifiedAccessInstances: []ec2types.VerifiedAccessInstance{{VerifiedAccessInstanceId: ptrStr(elxVAI1)}}}, nil
		},
	}
	var calls parentCalls
	for _, ph := range elxFanPhases {
		h[ph.sdkOp] = ph.handler(&calls)
	}
	for _, ph := range elxWidePhases {
		n := 0
		h[ph.sdkOp] = elxWideHandler(ph, &n, nil)
	}
	return h
}

func elxScanEC2(st *store.Store, handlers map[string]func(in any) (any, error)) error {
	acct := newTestAccount(testAccountID)
	acct.cfg = opStubConfig(handlers, elxDenied)
	_, _, err := scanEC2(context.Background(), acct, testRegion, st, testScanID)
	return err
}

// scanEC2 stores the new rows under the parents its own categories stored
// this scan.
func TestScanEC2_StoresLateExtraRows(t *testing.T) {
	st := newTestStore(t)
	if err := elxScanEC2(st, elxTopLevelHandlers()); err != nil {
		t.Fatalf("scanEC2: %v", err)
	}
	want := map[string]int{
		TypeEC2SnapshotAttribute: 1, TypeEC2ImageAttribute: 1, TypeEC2RouteServerAssociation: 1, TypeEC2RouteServerPropagation: 1,
		TypeEC2LockedSnapshot: 3, TypeEC2VerifiedAccessInstanceLoggingConfiguration: 2, TypeEC2ApplicationStatusCheck: 2,
	}
	for typ, n := range want {
		if got := len(rowsByNativeID(t, st, typ)); got != n {
			t.Errorf("%s rows = %d, want %d", typ, got, n)
		}
	}
	assertContainsNative(t, st, elxARN("snapshot", elxSnap1), elxSnapAttrID(elxSnap1))
	assertContainsNative(t, st, elxARN("snapshot", elxSnap1), elxLockID(elxSnap1))
	assertContainsNative(t, st, elxARN("image", elxImg1), elxImgAttrID(elxImg1))
	assertContainsNative(t, st, elxARN("route-server", elxRS1), elxAssocID(elxRS1, elxVpc1))
	assertContainsNative(t, st, elxARN("route-server", elxRS1), elxPropID(elxRS1, elxRtb1))
	assertContainsNative(t, st, elxARN("verified-access-instance", elxVAI1), elxVALogID(elxVAI1))
}

// The new phases run after every older category: a hard failure of one
// leaves an older category's slow call uncancelled, does not stop the other
// new phases, and reaches the caller.
func TestScanEC2_LateExtraPhasesAfterOlderCategories(t *testing.T) {
	type phase struct{ sdkOp, typ string }
	var phases []phase
	for _, ph := range elxFanPhases {
		phases = append(phases, phase{ph.sdkOp, ph.typ})
	}
	for _, ph := range elxWidePhases {
		phases = append(phases, phase{ph.sdkOp, ph.typ})
	}
	for _, failed := range phases {
		t.Run(failed.sdkOp+" fails", func(t *testing.T) {
			st := newTestStore(t)
			h := elxTopLevelHandlers()
			h[failed.sdkOp] = func(any) (any, error) { return nil, errElxTest }
			ctxHandlers := ignoreCtx(h)
			// DescribeImages answers only after a pause, and gives up if its
			// context is cancelled first, as a failing sibling inside
			// runScanners would cancel it.
			ctxHandlers["DescribeImages"] = func(ctx context.Context, in any) (any, error) {
				select {
				case <-ctx.Done():
					return nil, ctx.Err()
				case <-time.After(200 * time.Millisecond):
					return h["DescribeImages"](in)
				}
			}
			acct := newTestAccount(testAccountID)
			acct.cfg = opStubConfigCtx(ctxHandlers, elxDenied)
			_, _, err := scanEC2(context.Background(), acct, testRegion, st, testScanID)
			if !errors.Is(err, errElxTest) {
				t.Fatalf("err = %v, want the %s error", err, failed.sdkOp)
			}
			if _, ok := rowsByNativeID(t, st, TypeEC2Image)[elxARN("image", elxImg1)]; !ok {
				t.Errorf("image row missing: the %s failure cancelled an older category", failed.sdkOp)
			}
			for _, other := range phases {
				if other.sdkOp != failed.sdkOp && len(rowsByNativeID(t, st, other.typ)) == 0 {
					t.Errorf("%s rows missing after the %s failure", other.typ, failed.sdkOp)
				}
			}
		})
	}
}

// A transient older category is demoted to one warning, so it cannot make the
// dispatcher read a new phase's hard error as transient, and a transient new
// phase does not mask an older category's hard error. The region-wide phases
// are used: they are called whether or not an older category stored parents.
func TestScanEC2_LateExtraTransientDoesNotMaskHardError(t *testing.T) {
	type tc struct{ name, transientOp, hardOp, warnOp string }
	var cases []tc
	for _, ph := range elxWidePhases {
		cases = append(cases,
			tc{"transient older category, " + ph.name + " fails", "DescribeSnapshots", ph.sdkOp, "ec2"},
			tc{"transient " + ph.name, ph.sdkOp, "DescribeSnapshots", ph.op})
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			h := elxTopLevelHandlers()
			h[c.transientOp] = func(any) (any, error) { return nil, io.EOF }
			h[c.hardOp] = func(any) (any, error) { return nil, errElxTest }
			warnings := countOpWarnings(st, c.warnOp)
			err := elxScanEC2(st, h)
			if !errors.Is(err, errElxTest) {
				t.Fatalf("err = %v, want the %s error", err, c.hardOp)
			}
			if got := classifyServiceError(err); got != outcomeError {
				t.Errorf("classifyServiceError = %v, want outcomeError", got)
			}
			if *warnings != 1 {
				t.Errorf("%s warnings = %d, want 1", c.warnOp, *warnings)
			}
		})
	}
}

// elxAssertKeys fails unless attrs has exactly keys: the stored payload holds
// the attribute asked for and the id, not the output envelope or the unset
// fields of other attributes.
func elxAssertKeys(t *testing.T, attrs map[string]any, keys ...string) {
	t.Helper()
	got := make([]string, 0, len(attrs))
	for k := range attrs {
		got = append(got, k)
	}
	slices.Sort(got)
	slices.Sort(keys)
	if !slices.Equal(got, keys) {
		t.Errorf("attrs keys = %v, want %v", got, keys)
	}
}

// The stored permission list is the same whatever order EC2 returns it in.
func TestScanEC2LateExtraAttributes_PermissionOrderIgnored(t *testing.T) {
	// Each comparator key has a pair of entries that differ in that key only,
	// so a comparator missing any key leaves that pair in input order.
	// PermissionGroup has one value, so the Group pair is "all" against unset.
	const user2 = "333333333333"
	volume := []ec2types.CreateVolumePermission{
		{Group: ec2types.PermissionGroupAll, UserId: ptrStr(elxOtherAcct)},
		{UserId: ptrStr(elxOtherAcct)},
		{UserId: ptrStr(user2)},
	}
	launch := []ec2types.LaunchPermission{
		{Group: ec2types.PermissionGroupAll, UserId: ptrStr(elxOtherAcct)},
		{UserId: ptrStr(elxOtherAcct)},
		{UserId: ptrStr(user2)},
		{OrganizationArn: ptrStr(elxOrgARN)},
		{OrganizationArn: ptrStr(elxOrgARN + "-2")},
		{OrganizationalUnitArn: ptrStr(elxOrgARN + "/ou-lx1")},
		{OrganizationalUnitArn: ptrStr(elxOrgARN + "/ou-lx2")},
	}
	cases := []struct {
		name, sdkOp, typ, nativeID string
		seed                       func(*testing.T, *store.Store)
		run                        func(context.Context, ec2API, *account, string, *store.Store, string) (int, int, error)
		answer                     func(reversed bool) any
	}{
		{
			name: "snapshot", sdkOp: "DescribeSnapshotAttribute", typ: TypeEC2SnapshotAttribute, nativeID: elxSnapAttrID(elxSnap1),
			seed: func(t *testing.T, st *store.Store) {
				tgxSeedParent(t, st, TypeEC2Snapshot, elxARN("snapshot", elxSnap1), testRegion, "completed", testScanID)
			},
			run: scanEC2SnapshotAttributes,
			answer: func(reversed bool) any {
				perms := slices.Clone(volume)
				if reversed {
					slices.Reverse(perms)
				}
				return &ec2.DescribeSnapshotAttributeOutput{SnapshotId: ptrStr(elxSnap1), CreateVolumePermissions: perms}
			},
		},
		{
			name: "image", sdkOp: "DescribeImageAttribute", typ: TypeEC2ImageAttribute, nativeID: elxImgAttrID(elxImg1),
			seed: func(t *testing.T, st *store.Store) {
				tgxSeedParent(t, st, TypeEC2Image, elxARN("image", elxImg1), testRegion, "available", testScanID)
			},
			run: scanEC2ImageAttributes,
			answer: func(reversed bool) any {
				perms := slices.Clone(launch)
				if reversed {
					slices.Reverse(perms)
				}
				return &ec2.DescribeImageAttributeOutput{ImageId: ptrStr(elxImg1), LaunchPermissions: perms}
			},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var stored [2]string
			for i, reversed := range []bool{false, true} {
				st := newTestStore(t)
				c.seed(t, st)
				if _, _, err := elxRun(c.run, st, map[string]func(any) (any, error){
					c.sdkOp: func(any) (any, error) { return c.answer(reversed), nil },
				}); err != nil {
					t.Fatalf("run: %v", err)
				}
				stored[i] = rowsByNativeID(t, st, c.typ)[c.nativeID].AttributesJSON
			}
			if stored[0] == "" || stored[0] != stored[1] {
				t.Errorf("attributes differ by input order:\n%s\n%s", stored[0], stored[1])
			}
		})
	}
}

// With no lock listed, the stored snapshots are not read: a stored snapshot
// whose NativeID would fail that read costs nothing.
func TestScanEC2LockedSnapshots_NoLocksReadsNoParents(t *testing.T) {
	st := newTestStore(t)
	tgxSeedParent(t, st, TypeEC2Snapshot, "snap-bare", testRegion, "completed", testScanID)
	total, _, err := elxRun(scanEC2LockedSnapshots, st, map[string]func(any) (any, error){
		"DescribeLockedSnapshots": func(any) (any, error) { return &ec2.DescribeLockedSnapshotsOutput{}, nil },
	})
	if err != nil || total != 0 {
		t.Fatalf("total, err = %d, %v; want 0, nil", total, err)
	}
	// Once a lock is listed, the bad parent row fails the phase.
	_, _, err = elxRun(scanEC2LockedSnapshots, st, map[string]func(any) (any, error){
		"DescribeLockedSnapshots": func(any) (any, error) {
			return &ec2.DescribeLockedSnapshotsOutput{Snapshots: []ec2types.LockedSnapshotsInfo{{SnapshotId: ptrStr(elxSnap1)}}}, nil
		},
	})
	if !errors.Is(err, errEC2ParentNativeID) {
		t.Fatalf("err = %v, want errEC2ParentNativeID", err)
	}
	// A page-2 list failure is reported with the parent read failure, not
	// lost behind it.
	calls := 0
	_, _, err = elxRun(scanEC2LockedSnapshots, st, map[string]func(any) (any, error){
		"DescribeLockedSnapshots": func(any) (any, error) {
			if calls++; calls == 2 {
				return nil, errElxTest
			}
			return &ec2.DescribeLockedSnapshotsOutput{
				NextToken: ptrStr(elxPageNext),
				Snapshots: []ec2types.LockedSnapshotsInfo{{SnapshotId: ptrStr(elxSnap1)}},
			}, nil
		},
	})
	if !errors.Is(err, errEC2ParentNativeID) || !errors.Is(err, errElxTest) {
		t.Fatalf("err = %v, want both errEC2ParentNativeID and the page-2 error", err)
	}
}
