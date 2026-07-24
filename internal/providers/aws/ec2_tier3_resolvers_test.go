package aws

import (
	"fmt"
	"testing"

	"github.com/icearp/disco-cli/store"
)

func TestResolveEC2TGWRouteParent(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	rtARN := ec2ARN(testRegion, acct.ID, "transit-gateway-route-table", "tgw-rtb-1")
	rtID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2TransitGatewayRouteTable, rtARN, testRegion, "{}")
	rARN := ec2ARN(testRegion, acct.ID, "transit-gateway-route", "tgw-rtb-1/10.0.0.0/16")
	rID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2TransitGatewayRoute, rARN, testRegion, "{}")
	if err := resolveEC2TGWRouteParent(acct, st); err != nil {
		t.Fatalf("resolveEC2TGWRouteParent: %v", err)
	}
	rels, _ := st.RelationshipsFrom(rID)
	assertRelationship(t, rels, rID, rtID, store.RelAttachedTo)
}

func TestResolveEC2TGWRTBAssociation(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	rtARN := ec2ARN(testRegion, acct.ID, "transit-gateway-route-table", "tgw-rtb-1")
	rtID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2TransitGatewayRouteTable, rtARN, testRegion, "{}")
	attARN := ec2ARN(testRegion, acct.ID, "transit-gateway-attachment", "tgw-att-1")
	attID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2TransitGatewayAttachment, attARN, testRegion, "{}")
	aARN := ec2ARN(testRegion, acct.ID, "tgw-rtb-assoc", "tgw-rtb-1/tgw-att-1")
	aID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2TransitGatewayRouteTableAssociation, aARN, testRegion, "{}")
	if err := resolveEC2TGWRTBAssociation(acct, st); err != nil {
		t.Fatalf("resolveEC2TGWRTBAssociation: %v", err)
	}
	rels, _ := st.RelationshipsFrom(aID)
	assertRelationship(t, rels, aID, rtID, store.RelAttachedTo)
	assertRelationship(t, rels, aID, attID, store.RelAttachedTo)
}

func TestResolveEC2TGWMulticastDomainAssoc(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	dARN := ec2ARN(testRegion, acct.ID, "transit-gateway-multicast-domain", "tgw-mcast-d1")
	dID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2TransitGatewayMulticastDomain, dARN, testRegion, "{}")
	subARN := ec2ARN(testRegion, acct.ID, "subnet", "subnet-1")
	subID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2Subnet, subARN, testRegion, "{}")
	aARN := ec2ARN(testRegion, acct.ID, "transit-gateway-multicast-domain-association", "tgw-mcast-d1/tgw-att-1/subnet-1")
	aID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2TransitGatewayMulticastDomainAssociation, aARN, testRegion, "{}")
	if err := resolveEC2TGWMulticastDomainAssoc(acct, st); err != nil {
		t.Fatalf("resolveEC2TGWMulticastDomainAssoc: %v", err)
	}
	rels, _ := st.RelationshipsFrom(aID)
	assertRelationship(t, rels, aID, dID, store.RelAttachedTo)
	assertRelationship(t, rels, aID, subID, store.RelAttachedTo)
}

func TestResolveEC2TGWMulticastGroup(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	dARN := ec2ARN(testRegion, acct.ID, "transit-gateway-multicast-domain", "tgw-mcast-d1")
	dID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2TransitGatewayMulticastDomain, dARN, testRegion, "{}")
	eniARN := ec2ARN(testRegion, acct.ID, "network-interface", "eni-1")
	eniID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2NetworkInterface, eniARN, testRegion, "{}")
	mARN := ec2ARN(testRegion, acct.ID, "tgw-mcast-group-member", "tgw-mcast-d1/239.0.0.1/eni-1")
	mID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2TransitGatewayMulticastGroupMember, mARN, testRegion, "{}")
	if err := resolveEC2TGWMulticastGroup(acct, st); err != nil {
		t.Fatalf("resolveEC2TGWMulticastGroup: %v", err)
	}
	rels, _ := st.RelationshipsFrom(mID)
	assertRelationship(t, rels, mID, dID, store.RelAttachedTo)
	assertRelationship(t, rels, mID, eniID, store.RelAttachedTo)
}

func TestResolveEC2LocalGatewayRouteParent(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	rtARN := ec2ARN(testRegion, acct.ID, "local-gateway-route-table", "lgw-rtb-1")
	rtID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2LocalGatewayRouteTable, rtARN, testRegion, "{}")
	rARN := ec2ARN(testRegion, acct.ID, "local-gateway-route", "lgw-rtb-1/10.0.0.0/16")
	rID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2LocalGatewayRoute, rARN, testRegion, "{}")
	if err := resolveEC2LocalGatewayRouteParent(acct, st); err != nil {
		t.Fatalf("resolveEC2LocalGatewayRouteParent: %v", err)
	}
	rels, _ := st.RelationshipsFrom(rID)
	assertRelationship(t, rels, rID, rtID, store.RelAttachedTo)
}

func TestResolveEC2LocalGatewayVIToVIG(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	gARN := ec2ARN(testRegion, acct.ID, "local-gateway-virtual-interface-group", "lgw-vig-1")
	gID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2LocalGatewayVirtualInterfaceGroup, gARN, testRegion, "{}")
	viARN := ec2ARN(testRegion, acct.ID, "local-gateway-virtual-interface", "lgw-vi-1")
	viID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2LocalGatewayVirtualInterface, viARN, testRegion, `{"LocalGatewayVirtualInterfaceGroupId":"lgw-vig-1"}`)
	if err := resolveEC2LocalGatewayVIToVIG(acct, st); err != nil {
		t.Fatalf("resolveEC2LocalGatewayVIToVIG: %v", err)
	}
	rels, _ := st.RelationshipsFrom(viID)
	assertRelationship(t, rels, viID, gID, store.RelAttachedTo)
}

func TestResolveEC2IPAMAllocationToPool(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	pARN := ec2ARN(testRegion, acct.ID, "ipam-pool", "ipam-pool-1")
	pID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2IPAMPool, pARN, testRegion, "{}")
	aARN := ec2ARN(testRegion, acct.ID, "ipam-allocation", "ipam-pool-1/alloc-1")
	aID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2IPAMAllocation, aARN, testRegion, "{}")
	if err := resolveEC2IPAMAllocationToPool(acct, st); err != nil {
		t.Fatalf("resolveEC2IPAMAllocationToPool: %v", err)
	}
	rels, _ := st.RelationshipsFrom(aID)
	assertRelationship(t, rels, aID, pID, store.RelAttachedTo)
}

func TestResolveEC2IPAMPoolCIDRToPool(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	pARN := ec2ARN(testRegion, acct.ID, "ipam-pool", "ipam-pool-1")
	pID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2IPAMPool, pARN, testRegion, "{}")
	cARN := ec2ARN(testRegion, acct.ID, "ipam-pool-cidr", "ipam-pool-1/10.0.0.0/16")
	cID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2IPAMPoolCIDR, cARN, testRegion, "{}")
	if err := resolveEC2IPAMPoolCIDRToPool(acct, st); err != nil {
		t.Fatalf("resolveEC2IPAMPoolCIDRToPool: %v", err)
	}
	rels, _ := st.RelationshipsFrom(cID)
	assertRelationship(t, rels, cID, pID, store.RelAttachedTo)
}

func TestResolveEC2IPAMPLRTargetToResolver(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	plrARN := ec2ARN(testRegion, acct.ID, "ipam-prefix-list-resolver", "iplr-1")
	plrID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2IPAMPrefixListResolver, plrARN, testRegion, "{}")
	tARN := fmt.Sprintf("arn:aws:ec2:%s:%s:ipam-prefix-list-resolver-target/iplrt-1", testRegion, acct.ID)
	tID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2IPAMPrefixListResolverTarget, tARN, testRegion, `{"IpamPrefixListResolverId":"iplr-1"}`)
	if err := resolveEC2IPAMPLRTargetToResolver(acct, st); err != nil {
		t.Fatalf("resolveEC2IPAMPLRTargetToResolver: %v", err)
	}
	rels, _ := st.RelationshipsFrom(tID)
	assertRelationship(t, rels, tID, plrID, store.RelAttachedTo)
}

func TestResolveEC2RouteServerEndpointToServer(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	rsARN := ec2ARN(testRegion, acct.ID, "route-server", "rs-1")
	rsID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2RouteServer, rsARN, testRegion, "{}")
	eARN := ec2ARN(testRegion, acct.ID, "route-server-endpoint", "rse-1")
	eID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2RouteServerEndpoint, eARN, testRegion, `{"RouteServerId":"rs-1"}`)
	if err := resolveEC2RouteServerEndpointToServer(acct, st); err != nil {
		t.Fatalf("resolveEC2RouteServerEndpointToServer: %v", err)
	}
	rels, _ := st.RelationshipsFrom(eID)
	assertRelationship(t, rels, eID, rsID, store.RelAttachedTo)
}

func TestResolveEC2RouteServerPeerToEndpoint(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	eARN := ec2ARN(testRegion, acct.ID, "route-server-endpoint", "rse-1")
	eID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2RouteServerEndpoint, eARN, testRegion, "{}")
	pARN := ec2ARN(testRegion, acct.ID, "route-server-peer", "rsp-1")
	pID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2RouteServerPeer, pARN, testRegion, `{"RouteServerEndpointId":"rse-1"}`)
	if err := resolveEC2RouteServerPeerToEndpoint(acct, st); err != nil {
		t.Fatalf("resolveEC2RouteServerPeerToEndpoint: %v", err)
	}
	rels, _ := st.RelationshipsFrom(pID)
	assertRelationship(t, rels, pID, eID, store.RelAttachedTo)
}
