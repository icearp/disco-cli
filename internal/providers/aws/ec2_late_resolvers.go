package aws

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/icearp/disco-cli/internal/util"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerResolver(
		resolveFastSnapshotRestoreRelationships,
		EdgeDecl{TypeEC2FastSnapshotRestore, TypeEC2Snapshot, store.RelAttachedTo},
	)
	registerResolver(
		resolveFastLaunchImageRelationships,
		EdgeDecl{TypeEC2FastLaunchImage, TypeEC2Image, store.RelAttachedTo},
	)
	registerResolver(
		resolveReservedInstancesListingRelationships,
		EdgeDecl{TypeEC2ReservedInstancesListing, TypeEC2ReservedInstances, store.RelAttachedTo},
	)
	registerResolver(
		resolveServiceLinkVirtualInterfaceRelationships,
		EdgeDecl{TypeEC2ServiceLinkVirtualInterface, TypeEC2OutpostLag, store.RelAttachedTo},
		EdgeDecl{TypeEC2ServiceLinkVirtualInterface, TypeOutpostsOutpost, store.RelAttachedTo},
	)
	registerResolver(
		noopEC2LateHierarchy,
		EdgeDecl{TypeEC2Snapshot, TypeEC2SnapshotAttribute, store.RelContains},
		EdgeDecl{TypeEC2Image, TypeEC2ImageAttribute, store.RelContains},
		EdgeDecl{TypeEC2Snapshot, TypeEC2LockedSnapshot, store.RelContains},
		EdgeDecl{TypeEC2VerifiedAccessInstance, TypeEC2VerifiedAccessInstanceLoggingConfiguration, store.RelContains},
	)
	registerResolver(
		resolveRouteServerAssociationRelationships,
		EdgeDecl{TypeEC2RouteServer, TypeEC2RouteServerAssociation, store.RelContains},
		EdgeDecl{TypeEC2RouteServerAssociation, TypeEC2VPC, store.RelAttachedTo},
	)
	registerResolver(
		resolveRouteServerPropagationRelationships,
		EdgeDecl{TypeEC2RouteServer, TypeEC2RouteServerPropagation, store.RelContains},
		EdgeDecl{TypeEC2RouteServerPropagation, TypeEC2RouteTable, store.RelAttachedTo},
	)
}

// noopEC2LateHierarchy carries the EdgeDecls of the contains pairs the
// ec2_late_extra_scanners.go phases record themselves; it emits nothing.
func noopEC2LateHierarchy(_ *account, _ *store.Store) error {
	return nil
}

// ec2LateRefAttrs holds the reference fields the late-phase resolvers read;
// each source type carries only its own.
type ec2LateRefAttrs struct {
	SnapshotID          *string `json:"SnapshotId"`
	ImageID             *string `json:"ImageId"`
	ReservedInstancesID *string `json:"ReservedInstancesId"`
	OutpostLagID        *string `json:"OutpostLagId"`
	OutpostArn          *string `json:"OutpostArn"`
	VpcID               *string `json:"VpcId"`
	RouteTableID        *string `json:"RouteTableId"`
}

// ec2LateRef is one attached-to target of a source row: its type and the
// NativeID the row references in it, "" when the row references none.
type ec2LateRef struct {
	target   string
	nativeID func(region, accountID string, a ec2LateRefAttrs) string
}

// ec2ShortIDRef is a ref to an ec2ARN-keyed target named by its short id.
func ec2ShortIDRef(target, kind string, id func(ec2LateRefAttrs) *string) ec2LateRef {
	return ec2LateRef{target: target, nativeID: func(region, accountID string, a ec2LateRefAttrs) string {
		if sv(id(a)) == "" {
			return ""
		}
		return ec2ARN(region, accountID, kind, sv(id(a)))
	}}
}

func resolveFastSnapshotRestoreRelationships(acct *account, st *store.Store) error {
	return resolveEC2LateRefs(acct, st, TypeEC2FastSnapshotRestore,
		ec2ShortIDRef(TypeEC2Snapshot, "snapshot", func(a ec2LateRefAttrs) *string { return a.SnapshotID }))
}

func resolveFastLaunchImageRelationships(acct *account, st *store.Store) error {
	return resolveEC2LateRefs(acct, st, TypeEC2FastLaunchImage,
		ec2ShortIDRef(TypeEC2Image, "image", func(a ec2LateRefAttrs) *string { return a.ImageID }))
}

func resolveReservedInstancesListingRelationships(acct *account, st *store.Store) error {
	return resolveEC2LateRefs(acct, st, TypeEC2ReservedInstancesListing,
		ec2ShortIDRef(TypeEC2ReservedInstances, "reserved-instances", func(a ec2LateRefAttrs) *string { return a.ReservedInstancesID }))
}

func resolveServiceLinkVirtualInterfaceRelationships(acct *account, st *store.Store) error {
	return resolveEC2LateRefs(acct, st, TypeEC2ServiceLinkVirtualInterface,
		ec2ShortIDRef(TypeEC2OutpostLag, "outpost-lag", func(a ec2LateRefAttrs) *string { return a.OutpostLagID }),
		ec2LateRef{target: TypeOutpostsOutpost, nativeID: func(_, _ string, a ec2LateRefAttrs) string { return sv(a.OutpostArn) }})
}

// resolveRouteServerAssociationRelationships links each route server
// association to the scanned VPC it associates, in its own Region.
func resolveRouteServerAssociationRelationships(acct *account, st *store.Store) error {
	return resolveEC2LateRefs(acct, st, TypeEC2RouteServerAssociation,
		ec2ShortIDRef(TypeEC2VPC, "vpc", func(a ec2LateRefAttrs) *string { return a.VpcID }))
}

// resolveRouteServerPropagationRelationships links each route server
// propagation to the scanned route table it propagates into, in its own
// Region.
func resolveRouteServerPropagationRelationships(acct *account, st *store.Store) error {
	return resolveEC2LateRefs(acct, st, TypeEC2RouteServerPropagation,
		ec2ShortIDRef(TypeEC2RouteTable, "route-table", func(a ec2LateRefAttrs) *string { return a.RouteTableID }))
}

// resolveEC2LateRefs links each source row to every scanned target its refs
// name. A row whose attributes do not decode is reported and costs no other
// row its edges.
func resolveEC2LateRefs(acct *account, st *store.Store, source string, refs ...ec2LateRef) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{source}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	scanned := make([]map[string]bool, len(refs))
	for i, ref := range refs {
		if scanned[i], err = scannedIDSet(acct, st, ref.target); err != nil {
			return err
		}
	}
	var decodeErrs []error
	for _, r := range rows {
		var attrs ec2LateRefAttrs
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			decodeErrs = append(decodeErrs, fmt.Errorf("decode %s %s attributes: %w", source, r.NativeID, err))
			continue
		}
		for i, ref := range refs {
			nativeID := ref.nativeID(sv(r.Region), acct.ID, attrs)
			if nativeID == "" {
				continue
			}
			tgtID := store.ResourceID("aws", acct.ID, nativeID)
			if !scanned[i][tgtID] {
				continue
			}
			if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert %s→%s: %w", source, ref.target, err)
			}
		}
	}
	return errors.Join(decodeErrs...)
}
