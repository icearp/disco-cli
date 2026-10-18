package aws

import (
	"encoding/json"
	"errors"
	"testing"

	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"

	"github.com/icearp/disco-cli/store"
)

type lateTarget struct{ typ, nativeID string }

type lateResolverCase struct {
	name    string
	resolve func(*account, *store.Store) error
	source  string
	// nativeID builds a source row's NativeID; element builds its SDK element,
	// both from a distinguishing suffix; targets builds the targets it
	// references when it sits in region.
	nativeID func(suffix string) string
	element  func(suffix string) any
	targets  func(region, suffix string) []lateTarget
}

var lateResolverCases = []lateResolverCase{
	{
		name: "fast snapshot restore", resolve: resolveFastSnapshotRestoreRelationships, source: TypeEC2FastSnapshotRestore,
		nativeID: func(s string) string { return lateFSRID("snap-"+s, lateZoneA) },
		element: func(s string) any {
			return ec2types.DescribeFastSnapshotRestoreSuccessItem{SnapshotId: ptrStr("snap-" + s), AvailabilityZone: ptrStr(lateZoneA)}
		},
		targets: func(region, s string) []lateTarget {
			return []lateTarget{{TypeEC2Snapshot, ec2ARN(region, testAccountID, "snapshot", "snap-"+s)}}
		},
	},
	{
		name: "fast launch image", resolve: resolveFastLaunchImageRelationships, source: TypeEC2FastLaunchImage,
		nativeID: func(s string) string { return lateFLID("ami-" + s) },
		element:  func(s string) any { return ec2types.DescribeFastLaunchImagesSuccessItem{ImageId: ptrStr("ami-" + s)} },
		targets: func(region, s string) []lateTarget {
			return []lateTarget{{TypeEC2Image, ec2ARN(region, testAccountID, "image", "ami-"+s)}}
		},
	},
	{
		name: "reserved instances listing", resolve: resolveReservedInstancesListingRelationships, source: TypeEC2ReservedInstancesListing,
		nativeID: func(s string) string { return lateListingID("listing-" + s) },
		element: func(s string) any {
			return ec2types.ReservedInstancesListing{ReservedInstancesListingId: ptrStr("listing-" + s), ReservedInstancesId: ptrStr("ri-" + s)}
		},
		targets: func(region, s string) []lateTarget {
			return []lateTarget{{TypeEC2ReservedInstances, ec2ARN(region, testAccountID, "reserved-instances", "ri-"+s)}}
		},
	},
	{
		name: "service link virtual interface", resolve: resolveServiceLinkVirtualInterfaceRelationships, source: TypeEC2ServiceLinkVirtualInterface,
		nativeID: func(s string) string {
			return "arn:aws:ec2:us-east-1:123456789012:service-link-virtual-interface/sl-vif-" + s
		},
		element: func(s string) any {
			return ec2types.ServiceLinkVirtualInterface{
				OutpostLagId: ptrStr("outpost-lag-" + s),
				OutpostArn:   ptrStr("arn:aws:outposts:us-east-1:123456789012:outpost/op-" + s),
			}
		},
		targets: func(region, s string) []lateTarget {
			return []lateTarget{
				{TypeEC2OutpostLag, ec2ARN(region, testAccountID, "outpost-lag", "outpost-lag-"+s)},
				{TypeOutpostsOutpost, "arn:aws:outposts:us-east-1:123456789012:outpost/op-" + s},
			}
		},
	},
	{
		name: "route server association", resolve: resolveRouteServerAssociationRelationships, source: TypeEC2RouteServerAssociation,
		nativeID: func(s string) string {
			return routeServerAssociationNativeID(ec2ARN(testRegion, testAccountID, "route-server", "rs-"+s), "vpc-"+s)
		},
		element: func(s string) any {
			return ec2types.RouteServerAssociation{RouteServerId: ptrStr("rs-" + s), VpcId: ptrStr("vpc-" + s)}
		},
		targets: func(region, s string) []lateTarget {
			return []lateTarget{{TypeEC2VPC, ec2ARN(region, testAccountID, "vpc", "vpc-"+s)}}
		},
	},
	{
		name: "route server propagation", resolve: resolveRouteServerPropagationRelationships, source: TypeEC2RouteServerPropagation,
		nativeID: func(s string) string {
			return routeServerPropagationNativeID(ec2ARN(testRegion, testAccountID, "route-server", "rs-"+s), "rtb-"+s)
		},
		element: func(s string) any {
			return ec2types.RouteServerPropagation{RouteServerId: ptrStr("rs-" + s), RouteTableId: ptrStr("rtb-" + s)}
		},
		targets: func(region, s string) []lateTarget {
			return []lateTarget{{TypeEC2RouteTable, ec2ARN(region, testAccountID, "route-table", "rtb-"+s)}}
		},
	},
}

func lateMarshal(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal %T: %v", v, err)
	}
	return string(b)
}

func lateEdgesFrom(t *testing.T, st *store.Store, fromID string) []store.Relationship {
	t.Helper()
	rels, err := st.RelationshipsFrom(fromID, store.RelAttachedTo)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	return rels
}

func TestResolveEC2LateRefs_LinksScannedTargets(t *testing.T) {
	for _, c := range lateResolverCases {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			for _, tgt := range c.targets(testRegion, "a") {
				upsertTestResource(t, st, "aws", testAccountID, tgt.typ, tgt.nativeID, testRegion, "{}")
			}
			src := upsertTestResource(t, st, "aws", testAccountID, c.source, c.nativeID("a"), testRegion, lateMarshal(t, c.element("a")))
			// Row b references targets that were never scanned.
			unscanned := upsertTestResource(t, st, "aws", testAccountID, c.source, c.nativeID("b"), testRegion, lateMarshal(t, c.element("b")))
			// Row c references nothing.
			bare := upsertTestResource(t, st, "aws", testAccountID, c.source, c.nativeID("c"), testRegion, "{}")
			if err := c.resolve(newTestAccount(testAccountID), st); err != nil {
				t.Fatalf("resolve: %v", err)
			}
			rels := lateEdgesFrom(t, st, src)
			if len(rels) != len(c.targets(testRegion, "a")) {
				t.Errorf("edges from linked row = %d, want %d", len(rels), len(c.targets(testRegion, "a")))
			}
			for _, tgt := range c.targets(testRegion, "a") {
				assertRelationship(t, rels, src, store.ResourceID("aws", testAccountID, tgt.nativeID), store.RelAttachedTo)
			}
			// Row w sits in another Region: its targets are looked up there.
			for _, tgt := range c.targets(lateOtherZone, "w") {
				upsertTestResource(t, st, "aws", testAccountID, tgt.typ, tgt.nativeID, lateOtherZone, "{}")
			}
			west := upsertTestResource(t, st, "aws", testAccountID, c.source, c.nativeID("w"), lateOtherZone, lateMarshal(t, c.element("w")))
			if err := c.resolve(newTestAccount(testAccountID), st); err != nil {
				t.Fatalf("resolve: %v", err)
			}
			westRels := lateEdgesFrom(t, st, west)
			for _, tgt := range c.targets(lateOtherZone, "w") {
				assertRelationship(t, westRels, west, store.ResourceID("aws", testAccountID, tgt.nativeID), store.RelAttachedTo)
			}
			for _, id := range []string{unscanned, bare} {
				if n := len(lateEdgesFrom(t, st, id)); n != 0 {
					t.Errorf("edges from %s = %d, want 0", id, n)
				}
			}
		})
	}
}

// A row whose attributes do not decode is reported and costs no other row its
// edges. The good row is named so that it sorts after the unnamed bad row.
func TestResolveEC2LateRefs_DecodeErrorKeepsOtherRows(t *testing.T) {
	for _, c := range lateResolverCases {
		t.Run(c.name, func(t *testing.T) {
			st := newTestStore(t)
			for _, tgt := range c.targets(testRegion, "a") {
				upsertTestResource(t, st, "aws", testAccountID, tgt.typ, tgt.nativeID, testRegion, "{}")
			}
			upsertTestResource(t, st, "aws", testAccountID, c.source, c.nativeID("bad"), testRegion, `{"SnapshotId":1,"ImageId":1,"ReservedInstancesId":1,"OutpostLagId":1}`)
			good := upsertTestResourceNamed(t, st, c.source, c.nativeID("a"), testRegion, lateMarshal(t, c.element("a")), "zz-good")
			err := c.resolve(newTestAccount(testAccountID), st)
			var typeErr *json.UnmarshalTypeError
			if !errors.As(err, &typeErr) {
				t.Fatalf("err = %v, want the bad row's decode error", err)
			}
			if n := len(lateEdgesFrom(t, st, good)); n != len(c.targets(testRegion, "a")) {
				t.Errorf("edges from the good row = %d, want %d", n, len(c.targets(testRegion, "a")))
			}
		})
	}
}
