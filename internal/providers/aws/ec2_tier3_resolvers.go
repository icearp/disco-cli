package aws

import (
	"encoding/json"
	"fmt"
	"strings"

	"codeberg.org/icearp/disco/internal/store"
	"codeberg.org/icearp/disco/internal/util"
)

func init() {
	registerResolver(resolveEC2TGWRouteParent,
		EdgeDecl{TypeEC2TransitGatewayRoute, TypeEC2TransitGatewayRouteTable, store.RelAttachedTo},
	)
	registerResolver(resolveEC2TGWRTBAssociation,
		EdgeDecl{TypeEC2TransitGatewayRouteTableAssociation, TypeEC2TransitGatewayRouteTable, store.RelAttachedTo},
		EdgeDecl{TypeEC2TransitGatewayRouteTableAssociation, TypeEC2TransitGatewayAttachment, store.RelAttachedTo},
	)
	registerResolver(resolveEC2TGWRTBPropagation,
		EdgeDecl{TypeEC2TransitGatewayRouteTablePropagation, TypeEC2TransitGatewayRouteTable, store.RelAttachedTo},
		EdgeDecl{TypeEC2TransitGatewayRouteTablePropagation, TypeEC2TransitGatewayAttachment, store.RelAttachedTo},
	)
	registerResolver(resolveEC2TGWMulticastDomainAssoc,
		EdgeDecl{TypeEC2TransitGatewayMulticastDomainAssociation, TypeEC2TransitGatewayMulticastDomain, store.RelAttachedTo},
		EdgeDecl{TypeEC2TransitGatewayMulticastDomainAssociation, TypeEC2Subnet, store.RelAttachedTo},
	)
	registerResolver(resolveEC2TGWMulticastGroup,
		EdgeDecl{TypeEC2TransitGatewayMulticastGroupMember, TypeEC2TransitGatewayMulticastDomain, store.RelAttachedTo},
		EdgeDecl{TypeEC2TransitGatewayMulticastGroupMember, TypeEC2NetworkInterface, store.RelAttachedTo},
		EdgeDecl{TypeEC2TransitGatewayMulticastGroupSource, TypeEC2TransitGatewayMulticastDomain, store.RelAttachedTo},
		EdgeDecl{TypeEC2TransitGatewayMulticastGroupSource, TypeEC2NetworkInterface, store.RelAttachedTo},
	)
	registerResolver(resolveEC2LocalGatewayRouteParent,
		EdgeDecl{TypeEC2LocalGatewayRoute, TypeEC2LocalGatewayRouteTable, store.RelAttachedTo},
	)
	registerResolver(resolveEC2LocalGatewayVIToVIG,
		EdgeDecl{TypeEC2LocalGatewayVirtualInterface, TypeEC2LocalGatewayVirtualInterfaceGroup, store.RelAttachedTo},
	)
	registerResolver(resolveEC2IPAMAllocationToPool,
		EdgeDecl{TypeEC2IPAMAllocation, TypeEC2IPAMPool, store.RelAttachedTo},
	)
	registerResolver(resolveEC2IPAMPoolCIDRToPool,
		EdgeDecl{TypeEC2IPAMPoolCIDR, TypeEC2IPAMPool, store.RelAttachedTo},
	)
	registerResolver(resolveEC2IPAMPLRTargetToResolver,
		EdgeDecl{TypeEC2IPAMPrefixListResolverTarget, TypeEC2IPAMPrefixListResolver, store.RelAttachedTo},
	)
	registerResolver(resolveEC2RouteServerEndpointToServer,
		EdgeDecl{TypeEC2RouteServerEndpoint, TypeEC2RouteServer, store.RelAttachedTo},
	)
	registerResolver(resolveEC2RouteServerPeerToEndpoint,
		EdgeDecl{TypeEC2RouteServerPeer, TypeEC2RouteServerEndpoint, store.RelAttachedTo},
	)
}

// ec2NIDSegmentParts splits the post-`<kind>/` tail of an EC2 NativeID built
// via ec2ARN. Returns the slash-separated id components after the kind.
//
// Example: "arn:aws:ec2:r:a:tgw-rtb-assoc/{rtID}/{attID}" with kind
// "tgw-rtb-assoc" → ["{rtID}", "{attID}"].
func ec2NIDSegmentParts(arn, kind string) []string {
	seg := ":" + kind + "/"
	i := strings.Index(arn, seg)
	if i < 0 {
		return nil
	}
	return strings.Split(arn[i+len(seg):], "/")
}

// resolveEC2TGWRouteParent wires each TGW route to its parent route-table by
// parsing the NativeID `transit-gateway-route/{rtID}/{cidr}`.
func resolveEC2TGWRouteParent(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeEC2TransitGatewayRoute}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	rtSet, err := scannedIDSet(acct, st, TypeEC2TransitGatewayRouteTable)
	if err != nil {
		return err
	}
	for _, r := range rows {
		parts := ec2NIDSegmentParts(r.NativeID, "transit-gateway-route")
		if len(parts) < 1 {
			continue
		}
		rtARN := ec2ARN(sv(r.Region), acct.ID, "transit-gateway-route-table", parts[0])
		tgtID := store.ResourceID("aws", acct.ID, TypeEC2TransitGatewayRouteTable, rtARN)
		if !rtSet[tgtID] {
			continue
		}
		if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert ec2 tgw-route→rtb: %w", err)
		}
	}
	return nil
}

func resolveEC2TGWRTBAssociation(acct *account, st *store.Store) error {
	return resolveEC2TGWRTBJoin(acct, st, TypeEC2TransitGatewayRouteTableAssociation, "tgw-rtb-assoc")
}

func resolveEC2TGWRTBPropagation(acct *account, st *store.Store) error {
	return resolveEC2TGWRTBJoin(acct, st, TypeEC2TransitGatewayRouteTablePropagation, "tgw-rtb-prop")
}

// resolveEC2TGWRTBJoin handles association/propagation rows whose NativeID is
// `<kind>/{rtID}/{attID}` — wires both endpoints (route-table + attachment).
func resolveEC2TGWRTBJoin(acct *account, st *store.Store, sourceType, kind string) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{sourceType}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	rtSet, err := scannedIDSet(acct, st, TypeEC2TransitGatewayRouteTable)
	if err != nil {
		return err
	}
	attSet, err := scannedIDSet(acct, st, TypeEC2TransitGatewayAttachment)
	if err != nil {
		return err
	}
	for _, r := range rows {
		parts := ec2NIDSegmentParts(r.NativeID, kind)
		if len(parts) < 2 {
			continue
		}
		region := sv(r.Region)
		rtARN := ec2ARN(region, acct.ID, "transit-gateway-route-table", parts[0])
		if rtID := store.ResourceID("aws", acct.ID, TypeEC2TransitGatewayRouteTable, rtARN); rtSet[rtID] {
			if err := st.UpsertRelationship(r.ID, rtID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert ec2 %s→rtb: %w", kind, err)
			}
		}
		attARN := ec2ARN(region, acct.ID, "transit-gateway-attachment", parts[1])
		if attID := store.ResourceID("aws", acct.ID, TypeEC2TransitGatewayAttachment, attARN); attSet[attID] {
			if err := st.UpsertRelationship(r.ID, attID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert ec2 %s→att: %w", kind, err)
			}
		}
	}
	return nil
}

// resolveEC2TGWMulticastDomainAssoc wires association rows to the parent
// multicast-domain and the participating subnet. NativeID shape:
// `transit-gateway-multicast-domain-association/{domainID}/{attID}/{subnetID}`.
func resolveEC2TGWMulticastDomainAssoc(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeEC2TransitGatewayMulticastDomainAssociation},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	domSet, err := scannedIDSet(acct, st, TypeEC2TransitGatewayMulticastDomain)
	if err != nil {
		return err
	}
	subSet, err := scannedIDSet(acct, st, TypeEC2Subnet)
	if err != nil {
		return err
	}
	for _, r := range rows {
		parts := ec2NIDSegmentParts(r.NativeID, "transit-gateway-multicast-domain-association")
		if len(parts) < 3 {
			continue
		}
		region := sv(r.Region)
		domARN := ec2ARN(region, acct.ID, "transit-gateway-multicast-domain", parts[0])
		if domID := store.ResourceID("aws", acct.ID, TypeEC2TransitGatewayMulticastDomain, domARN); domSet[domID] {
			if err := st.UpsertRelationship(r.ID, domID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert ec2 mcast-assoc→domain: %w", err)
			}
		}
		subARN := ec2ARN(region, acct.ID, "subnet", parts[2])
		if subID := store.ResourceID("aws", acct.ID, TypeEC2Subnet, subARN); subSet[subID] {
			if err := st.UpsertRelationship(r.ID, subID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert ec2 mcast-assoc→subnet: %w", err)
			}
		}
	}
	return nil
}

// resolveEC2TGWMulticastGroup wires multicast group member/source rows to
// their parent multicast-domain and ENI. NativeID:
// `tgw-mcast-group-{member,source}/{domainID}/{groupIP}/{eniID}`.
func resolveEC2TGWMulticastGroup(acct *account, st *store.Store) error {
	for _, c := range []struct {
		ttyp string
		kind string
	}{
		{TypeEC2TransitGatewayMulticastGroupMember, "tgw-mcast-group-member"},
		{TypeEC2TransitGatewayMulticastGroupSource, "tgw-mcast-group-source"},
	} {
		if err := resolveEC2TGWMulticastGroupOne(acct, st, c.ttyp, c.kind); err != nil {
			return err
		}
	}
	return nil
}

func resolveEC2TGWMulticastGroupOne(acct *account, st *store.Store, sourceType, kind string) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{sourceType}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	domSet, err := scannedIDSet(acct, st, TypeEC2TransitGatewayMulticastDomain)
	if err != nil {
		return err
	}
	eniSet, err := scannedIDSet(acct, st, TypeEC2NetworkInterface)
	if err != nil {
		return err
	}
	for _, r := range rows {
		parts := ec2NIDSegmentParts(r.NativeID, kind)
		if len(parts) < 3 {
			continue
		}
		region := sv(r.Region)
		domARN := ec2ARN(region, acct.ID, "transit-gateway-multicast-domain", parts[0])
		if domID := store.ResourceID("aws", acct.ID, TypeEC2TransitGatewayMulticastDomain, domARN); domSet[domID] {
			if err := st.UpsertRelationship(r.ID, domID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert ec2 %s→domain: %w", kind, err)
			}
		}
		eniARN := ec2ARN(region, acct.ID, "network-interface", parts[2])
		if eniID := store.ResourceID("aws", acct.ID, TypeEC2NetworkInterface, eniARN); eniSet[eniID] {
			if err := st.UpsertRelationship(r.ID, eniID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert ec2 %s→eni: %w", kind, err)
			}
		}
	}
	return nil
}

// resolveEC2LocalGatewayRouteParent wires LG route to LG route-table via
// NativeID `local-gateway-route/{rtID}/{dest}`.
func resolveEC2LocalGatewayRouteParent(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeEC2LocalGatewayRoute}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	rtSet, err := scannedIDSet(acct, st, TypeEC2LocalGatewayRouteTable)
	if err != nil {
		return err
	}
	for _, r := range rows {
		parts := ec2NIDSegmentParts(r.NativeID, "local-gateway-route")
		if len(parts) < 1 {
			continue
		}
		rtARN := ec2ARN(sv(r.Region), acct.ID, "local-gateway-route-table", parts[0])
		tgtID := store.ResourceID("aws", acct.ID, TypeEC2LocalGatewayRouteTable, rtARN)
		if !rtSet[tgtID] {
			continue
		}
		if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert ec2 lg-route→rtb: %w", err)
		}
	}
	return nil
}

// resolveEC2LocalGatewayVIToVIG wires LG virtual-interface to its parent VIG
// via the `LocalGatewayVirtualInterfaceGroupId` SDK attribute.
func resolveEC2LocalGatewayVIToVIG(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeEC2LocalGatewayVirtualInterface},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	vigSet, err := scannedIDSet(acct, st, TypeEC2LocalGatewayVirtualInterfaceGroup)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			LocalGatewayVirtualInterfaceGroupId *string `json:"LocalGatewayVirtualInterfaceGroupId"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		gid := sv(attrs.LocalGatewayVirtualInterfaceGroupId)
		if gid == "" {
			continue
		}
		gARN := ec2ARN(sv(r.Region), acct.ID, "local-gateway-virtual-interface-group", gid)
		tgtID := store.ResourceID("aws", acct.ID, TypeEC2LocalGatewayVirtualInterfaceGroup, gARN)
		if !vigSet[tgtID] {
			continue
		}
		if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert ec2 lg-vi→vig: %w", err)
		}
	}
	return nil
}

// resolveEC2IPAMAllocationToPool wires ipam-allocation → ipam-pool via
// NativeID `ipam-allocation/{poolID}/{allocID}`.
func resolveEC2IPAMAllocationToPool(acct *account, st *store.Store) error {
	return resolveEC2IPAMChildToPool(acct, st, TypeEC2IPAMAllocation, "ipam-allocation")
}

// resolveEC2IPAMPoolCIDRToPool wires ipam-pool-cidr → ipam-pool via
// NativeID `ipam-pool-cidr/{poolID}/{cidr}`.
func resolveEC2IPAMPoolCIDRToPool(acct *account, st *store.Store) error {
	return resolveEC2IPAMChildToPool(acct, st, TypeEC2IPAMPoolCIDR, "ipam-pool-cidr")
}

func resolveEC2IPAMChildToPool(acct *account, st *store.Store, sourceType, kind string) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{sourceType}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	poolSet, err := scannedIDSet(acct, st, TypeEC2IPAMPool)
	if err != nil {
		return err
	}
	for _, r := range rows {
		parts := ec2NIDSegmentParts(r.NativeID, kind)
		if len(parts) < 1 {
			continue
		}
		pARN := ec2ARN(sv(r.Region), acct.ID, "ipam-pool", parts[0])
		tgtID := store.ResourceID("aws", acct.ID, TypeEC2IPAMPool, pARN)
		if !poolSet[tgtID] {
			continue
		}
		if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert ec2 %s→pool: %w", kind, err)
		}
	}
	return nil
}

// resolveEC2IPAMPLRTargetToResolver wires prefix-list-resolver-target to its
// parent prefix-list-resolver via the SDK `IpamPrefixListResolverId` attr.
func resolveEC2IPAMPLRTargetToResolver(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeEC2IPAMPrefixListResolverTarget},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	plrSet, err := scannedIDSet(acct, st, TypeEC2IPAMPrefixListResolver)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			IpamPrefixListResolverId *string `json:"IpamPrefixListResolverId"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		id := sv(attrs.IpamPrefixListResolverId)
		if id == "" {
			continue
		}
		pARN := ec2ARN(sv(r.Region), acct.ID, "ipam-prefix-list-resolver", id)
		tgtID := store.ResourceID("aws", acct.ID, TypeEC2IPAMPrefixListResolver, pARN)
		if !plrSet[tgtID] {
			continue
		}
		if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert ec2 plr-target→plr: %w", err)
		}
	}
	return nil
}

// resolveEC2RouteServerEndpointToServer wires route-server-endpoint → its
// route-server via the `RouteServerId` attr.
func resolveEC2RouteServerEndpointToServer(acct *account, st *store.Store) error {
	return resolveEC2RouteServerAttrEdge(acct, st,
		TypeEC2RouteServerEndpoint, "RouteServerId",
		TypeEC2RouteServer, "route-server",
	)
}

// resolveEC2RouteServerPeerToEndpoint wires route-server-peer → its parent
// route-server-endpoint via `RouteServerEndpointId`.
func resolveEC2RouteServerPeerToEndpoint(acct *account, st *store.Store) error {
	return resolveEC2RouteServerAttrEdge(acct, st,
		TypeEC2RouteServerPeer, "RouteServerEndpointId",
		TypeEC2RouteServerEndpoint, "route-server-endpoint",
	)
}

func resolveEC2RouteServerAttrEdge(acct *account, st *store.Store, sourceType, fieldName, parentType, parentKind string) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{sourceType}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	parentSet, err := scannedIDSet(acct, st, parentType)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var raw map[string]any
		if err := json.Unmarshal([]byte(r.AttributesJSON), &raw); err != nil {
			continue
		}
		v, ok := raw[fieldName].(string)
		if !ok || v == "" {
			continue
		}
		pARN := ec2ARN(sv(r.Region), acct.ID, parentKind, v)
		tgtID := store.ResourceID("aws", acct.ID, parentType, pARN)
		if !parentSet[tgtID] {
			continue
		}
		if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert ec2 %s→%s: %w", sourceType, parentType, err)
		}
	}
	return nil
}
