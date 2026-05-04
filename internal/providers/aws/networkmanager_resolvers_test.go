package aws

import (
	"fmt"
	"testing"

	"codeberg.org/icearp/disco/internal/store"
)

// nmRegion is the home region NetworkManager scanner uses (us-west-2 per the
// scanner's region guard). Tests pin it for ARN-shape consistency.
const nmRegion = "us-west-2"

func TestResolveNMSiteRefs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	gnNID := nmGlobalNetworkID(acct.ID, "global-network-1")
	gnID := upsertTestResource(t, st, "aws", acct.ID, TypeNetworkManagerGlobalNetwork, gnNID, nmRegion, `{"GlobalNetworkId":"global-network-1"}`)
	siteNID := nmSiteID(acct.ID, "global-network-1", "site-1")
	siteID := upsertTestResource(t, st, "aws", acct.ID, TypeNetworkManagerSite, siteNID, nmRegion, `{"GlobalNetworkId":"global-network-1","SiteId":"site-1"}`)

	if err := resolveNMSiteRefs(acct, st); err != nil {
		t.Fatalf("resolveNMSiteRefs: %v", err)
	}
	rels, err := st.RelationshipsFrom(siteID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 1 {
		t.Fatalf("expected 1 relationship, got %d", len(rels))
	}
	assertRelationship(t, rels, siteID, gnID, store.RelAttachedTo)
}

func TestResolveNMSiteRefs_EmptyAttrs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	siteID := upsertTestResource(t, st, "aws", acct.ID, TypeNetworkManagerSite,
		nmSiteID(acct.ID, "global-network-1", "site-bare"), nmRegion, "{}")
	if err := resolveNMSiteRefs(acct, st); err != nil {
		t.Fatalf("resolveNMSiteRefs: %v", err)
	}
	rels, _ := st.RelationshipsFrom(siteID)
	if len(rels) != 0 {
		t.Errorf("expected 0 relationships, got %d", len(rels))
	}
}

func TestResolveNMSiteRefs_UnscannedGlobalNetworkSkipped(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	siteID := upsertTestResource(t, st, "aws", acct.ID, TypeNetworkManagerSite,
		nmSiteID(acct.ID, "missing-gn", "site-1"), nmRegion, `{"GlobalNetworkId":"missing-gn","SiteId":"site-1"}`)
	if err := resolveNMSiteRefs(acct, st); err != nil {
		t.Fatalf("resolveNMSiteRefs: %v", err)
	}
	rels, _ := st.RelationshipsFrom(siteID)
	if len(rels) != 0 {
		t.Errorf("expected 0 relationships, got %d", len(rels))
	}
}

func TestResolveNMDeviceRefs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	gnID := upsertTestResource(t, st, "aws", acct.ID, TypeNetworkManagerGlobalNetwork,
		nmGlobalNetworkID(acct.ID, "gn-1"), nmRegion, `{"GlobalNetworkId":"gn-1"}`)
	siteID := upsertTestResource(t, st, "aws", acct.ID, TypeNetworkManagerSite,
		nmSiteID(acct.ID, "gn-1", "site-1"), nmRegion, `{"GlobalNetworkId":"gn-1","SiteId":"site-1"}`)
	devID := upsertTestResource(t, st, "aws", acct.ID, TypeNetworkManagerDevice,
		nmDeviceID(acct.ID, "gn-1", "device-1"), nmRegion,
		`{"GlobalNetworkId":"gn-1","SiteId":"site-1","DeviceId":"device-1"}`)

	if err := resolveNMDeviceRefs(acct, st); err != nil {
		t.Fatalf("resolveNMDeviceRefs: %v", err)
	}
	rels, _ := st.RelationshipsFrom(devID)
	if len(rels) != 2 {
		t.Fatalf("expected 2 relationships, got %d", len(rels))
	}
	assertRelationship(t, rels, devID, gnID, store.RelAttachedTo)
	assertRelationship(t, rels, devID, siteID, store.RelAttachedTo)
}

func TestResolveNMDeviceRefs_EmptyAttrs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	devID := upsertTestResource(t, st, "aws", acct.ID, TypeNetworkManagerDevice,
		nmDeviceID(acct.ID, "gn-1", "device-bare"), nmRegion, "{}")
	if err := resolveNMDeviceRefs(acct, st); err != nil {
		t.Fatalf("resolveNMDeviceRefs: %v", err)
	}
	rels, _ := st.RelationshipsFrom(devID)
	if len(rels) != 0 {
		t.Errorf("expected 0 relationships, got %d", len(rels))
	}
}

func TestResolveNMLinkRefs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	gnID := upsertTestResource(t, st, "aws", acct.ID, TypeNetworkManagerGlobalNetwork,
		nmGlobalNetworkID(acct.ID, "gn-1"), nmRegion, `{"GlobalNetworkId":"gn-1"}`)
	siteID := upsertTestResource(t, st, "aws", acct.ID, TypeNetworkManagerSite,
		nmSiteID(acct.ID, "gn-1", "site-1"), nmRegion, `{"GlobalNetworkId":"gn-1","SiteId":"site-1"}`)
	linkID := upsertTestResource(t, st, "aws", acct.ID, TypeNetworkManagerLink,
		nmLinkID(acct.ID, "gn-1", "link-1"), nmRegion,
		`{"GlobalNetworkId":"gn-1","SiteId":"site-1","LinkId":"link-1"}`)

	if err := resolveNMLinkRefs(acct, st); err != nil {
		t.Fatalf("resolveNMLinkRefs: %v", err)
	}
	rels, _ := st.RelationshipsFrom(linkID)
	if len(rels) != 2 {
		t.Fatalf("expected 2 relationships, got %d", len(rels))
	}
	assertRelationship(t, rels, linkID, gnID, store.RelAttachedTo)
	assertRelationship(t, rels, linkID, siteID, store.RelAttachedTo)
}

func TestResolveNMLinkRefs_UnscannedSiteSkipped(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	gnID := upsertTestResource(t, st, "aws", acct.ID, TypeNetworkManagerGlobalNetwork,
		nmGlobalNetworkID(acct.ID, "gn-1"), nmRegion, `{"GlobalNetworkId":"gn-1"}`)
	linkID := upsertTestResource(t, st, "aws", acct.ID, TypeNetworkManagerLink,
		nmLinkID(acct.ID, "gn-1", "link-1"), nmRegion,
		`{"GlobalNetworkId":"gn-1","SiteId":"missing","LinkId":"link-1"}`)

	if err := resolveNMLinkRefs(acct, st); err != nil {
		t.Fatalf("resolveNMLinkRefs: %v", err)
	}
	rels, _ := st.RelationshipsFrom(linkID)
	if len(rels) != 1 {
		t.Fatalf("expected 1 relationship (gn only), got %d", len(rels))
	}
	assertRelationship(t, rels, linkID, gnID, store.RelAttachedTo)
}

func TestResolveNMLinkAssociationRefs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	devID := upsertTestResource(t, st, "aws", acct.ID, TypeNetworkManagerDevice,
		nmDeviceID(acct.ID, "gn-1", "device-1"), nmRegion, `{}`)
	linkID := upsertTestResource(t, st, "aws", acct.ID, TypeNetworkManagerLink,
		nmLinkID(acct.ID, "gn-1", "link-1"), nmRegion, `{}`)
	assocNID := fmt.Sprintf("arn:aws:networkmanager::%s:global-network/gn-1/link-association/device-1/link-1", acct.ID)
	assocID := upsertTestResource(t, st, "aws", acct.ID, TypeNetworkManagerLinkAssociation, assocNID, nmRegion,
		`{"GlobalNetworkId":"gn-1","DeviceId":"device-1","LinkId":"link-1"}`)

	if err := resolveNMLinkAssociationRefs(acct, st); err != nil {
		t.Fatalf("resolveNMLinkAssociationRefs: %v", err)
	}
	rels, _ := st.RelationshipsFrom(assocID)
	if len(rels) != 2 {
		t.Fatalf("expected 2 relationships, got %d", len(rels))
	}
	assertRelationship(t, rels, assocID, devID, store.RelAttachedTo)
	assertRelationship(t, rels, assocID, linkID, store.RelAttachedTo)
}

func TestResolveNMLinkAssociationRefs_EmptyAttrs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	assocID := upsertTestResource(t, st, "aws", acct.ID, TypeNetworkManagerLinkAssociation,
		fmt.Sprintf("arn:aws:networkmanager::%s:global-network/gn/link-association/d/l", acct.ID),
		nmRegion, "{}")
	if err := resolveNMLinkAssociationRefs(acct, st); err != nil {
		t.Fatalf("resolveNMLinkAssociationRefs: %v", err)
	}
	rels, _ := st.RelationshipsFrom(assocID)
	if len(rels) != 0 {
		t.Errorf("expected 0 relationships, got %d", len(rels))
	}
}

func TestResolveNMCoreNetworkRefs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	gnID := upsertTestResource(t, st, "aws", acct.ID, TypeNetworkManagerGlobalNetwork,
		nmGlobalNetworkID(acct.ID, "gn-1"), nmRegion, `{"GlobalNetworkId":"gn-1"}`)
	cnID := upsertTestResource(t, st, "aws", acct.ID, TypeNetworkManagerCoreNetwork,
		nmCoreNetworkID(acct.ID, "cn-1"), nmRegion, `{"GlobalNetworkId":"gn-1","CoreNetworkId":"cn-1"}`)

	if err := resolveNMCoreNetworkRefs(acct, st); err != nil {
		t.Fatalf("resolveNMCoreNetworkRefs: %v", err)
	}
	rels, _ := st.RelationshipsFrom(cnID)
	if len(rels) != 1 {
		t.Fatalf("expected 1 relationship, got %d", len(rels))
	}
	assertRelationship(t, rels, cnID, gnID, store.RelAttachedTo)
}

func TestResolveNMCoreNetworkRefs_EmptyAttrs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	cnID := upsertTestResource(t, st, "aws", acct.ID, TypeNetworkManagerCoreNetwork,
		nmCoreNetworkID(acct.ID, "cn-bare"), nmRegion, "{}")
	if err := resolveNMCoreNetworkRefs(acct, st); err != nil {
		t.Fatalf("resolveNMCoreNetworkRefs: %v", err)
	}
	rels, _ := st.RelationshipsFrom(cnID)
	if len(rels) != 0 {
		t.Errorf("expected 0 relationships, got %d", len(rels))
	}
}

func TestResolveNMTGWRegistrationRefs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	gnID := upsertTestResource(t, st, "aws", acct.ID, TypeNetworkManagerGlobalNetwork,
		nmGlobalNetworkID(acct.ID, "gn-1"), nmRegion, `{"GlobalNetworkId":"gn-1"}`)
	tgwARN := ec2ARN("us-east-1", acct.ID, "transit-gateway", "tgw-aaa")
	tgwID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2TransitGateway, tgwARN, "us-east-1", "{}")
	regNID := fmt.Sprintf("arn:aws:networkmanager::%s:global-network/gn-1/tgw-registration/%s", acct.ID, tgwARN)
	regID := upsertTestResource(t, st, "aws", acct.ID, TypeNetworkManagerTransitGatewayRegistration, regNID, nmRegion,
		fmt.Sprintf(`{"GlobalNetworkId":"gn-1","TransitGatewayArn":"%s"}`, tgwARN))

	if err := resolveNMTGWRegistrationRefs(acct, st); err != nil {
		t.Fatalf("resolveNMTGWRegistrationRefs: %v", err)
	}
	rels, _ := st.RelationshipsFrom(regID)
	if len(rels) != 2 {
		t.Fatalf("expected 2 relationships, got %d", len(rels))
	}
	assertRelationship(t, rels, regID, gnID, store.RelAttachedTo)
	assertRelationship(t, rels, regID, tgwID, store.RelAttachedTo)
}

func TestResolveNMTGWRegistrationRefs_UnscannedTGWSkipped(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	gnID := upsertTestResource(t, st, "aws", acct.ID, TypeNetworkManagerGlobalNetwork,
		nmGlobalNetworkID(acct.ID, "gn-1"), nmRegion, `{"GlobalNetworkId":"gn-1"}`)
	regID := upsertTestResource(t, st, "aws", acct.ID, TypeNetworkManagerTransitGatewayRegistration,
		fmt.Sprintf("arn:aws:networkmanager::%s:global-network/gn-1/tgw-registration/missing", acct.ID), nmRegion,
		fmt.Sprintf(`{"GlobalNetworkId":"gn-1","TransitGatewayArn":"%s"}`,
			ec2ARN("us-east-1", acct.ID, "transit-gateway", "tgw-missing")))

	if err := resolveNMTGWRegistrationRefs(acct, st); err != nil {
		t.Fatalf("resolveNMTGWRegistrationRefs: %v", err)
	}
	rels, _ := st.RelationshipsFrom(regID)
	if len(rels) != 1 {
		t.Fatalf("expected 1 relationship (gn only), got %d", len(rels))
	}
	assertRelationship(t, rels, regID, gnID, store.RelAttachedTo)
}

func TestResolveNMCGWAssociationRefs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	gnID := upsertTestResource(t, st, "aws", acct.ID, TypeNetworkManagerGlobalNetwork,
		nmGlobalNetworkID(acct.ID, "gn-1"), nmRegion, `{"GlobalNetworkId":"gn-1"}`)
	cgwARN := ec2ARN("us-east-1", acct.ID, "customer-gateway", "cgw-aaa")
	cgwID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2CustomerGateway, cgwARN, "us-east-1", "{}")
	assocID := upsertTestResource(t, st, "aws", acct.ID, TypeNetworkManagerCustomerGatewayAssociation,
		fmt.Sprintf("arn:aws:networkmanager::%s:global-network/gn-1/cgw-association/%s", acct.ID, cgwARN), nmRegion,
		fmt.Sprintf(`{"GlobalNetworkId":"gn-1","CustomerGatewayArn":"%s"}`, cgwARN))

	if err := resolveNMCGWAssociationRefs(acct, st); err != nil {
		t.Fatalf("resolveNMCGWAssociationRefs: %v", err)
	}
	rels, _ := st.RelationshipsFrom(assocID)
	if len(rels) != 2 {
		t.Fatalf("expected 2 relationships, got %d", len(rels))
	}
	assertRelationship(t, rels, assocID, gnID, store.RelAttachedTo)
	assertRelationship(t, rels, assocID, cgwID, store.RelAttachedTo)
}

func TestResolveNMCGWAssociationRefs_EmptyAttrs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	assocID := upsertTestResource(t, st, "aws", acct.ID, TypeNetworkManagerCustomerGatewayAssociation,
		fmt.Sprintf("arn:aws:networkmanager::%s:global-network/gn/cgw-association/x", acct.ID), nmRegion, "{}")
	if err := resolveNMCGWAssociationRefs(acct, st); err != nil {
		t.Fatalf("resolveNMCGWAssociationRefs: %v", err)
	}
	rels, _ := st.RelationshipsFrom(assocID)
	if len(rels) != 0 {
		t.Errorf("expected 0 relationships, got %d", len(rels))
	}
}

func TestResolveNMVpcAttachmentRefs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	cnID := upsertTestResource(t, st, "aws", acct.ID, TypeNetworkManagerCoreNetwork,
		nmCoreNetworkID(acct.ID, "cn-1"), nmRegion, `{"GlobalNetworkId":"gn-1","CoreNetworkId":"cn-1"}`)
	vpcARN := ec2ARN("us-east-1", acct.ID, "vpc", "vpc-1")
	vpcID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2VPC, vpcARN, "us-east-1", "{}")
	subARN := ec2ARN("us-east-1", acct.ID, "subnet", "subnet-1")
	subID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2Subnet, subARN, "us-east-1", "{}")
	attNID := fmt.Sprintf("arn:aws:networkmanager::%s:attachment/attach-1", acct.ID)
	attID := upsertTestResource(t, st, "aws", acct.ID, TypeNetworkManagerVpcAttachment, attNID, nmRegion,
		fmt.Sprintf(`{"AttachmentId":"attach-1","CoreNetworkId":"cn-1","ResourceArn":"%s","SubnetArns":["%s"]}`, vpcARN, subARN))

	if err := resolveNMVpcAttachmentRefs(acct, st); err != nil {
		t.Fatalf("resolveNMVpcAttachmentRefs: %v", err)
	}
	rels, _ := st.RelationshipsFrom(attID)
	if len(rels) != 3 {
		t.Fatalf("expected 3 relationships, got %d: %+v", len(rels), rels)
	}
	assertRelationship(t, rels, attID, cnID, store.RelAttachedTo)
	assertRelationship(t, rels, attID, vpcID, store.RelAttachedTo)
	assertRelationship(t, rels, attID, subID, store.RelAttachedTo)
}

func TestResolveNMVpcAttachmentRefs_EmptyAttrs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	attID := upsertTestResource(t, st, "aws", acct.ID, TypeNetworkManagerVpcAttachment,
		fmt.Sprintf("arn:aws:networkmanager::%s:attachment/attach-bare", acct.ID), nmRegion, "{}")
	if err := resolveNMVpcAttachmentRefs(acct, st); err != nil {
		t.Fatalf("resolveNMVpcAttachmentRefs: %v", err)
	}
	rels, _ := st.RelationshipsFrom(attID)
	if len(rels) != 0 {
		t.Errorf("expected 0 relationships, got %d", len(rels))
	}
}

func TestResolveNMVpcAttachmentRefs_UnscannedTargetsSkipped(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	attID := upsertTestResource(t, st, "aws", acct.ID, TypeNetworkManagerVpcAttachment,
		fmt.Sprintf("arn:aws:networkmanager::%s:attachment/attach-1", acct.ID), nmRegion,
		fmt.Sprintf(`{"AttachmentId":"attach-1","CoreNetworkId":"missing","ResourceArn":"%s"}`,
			ec2ARN("us-east-1", acct.ID, "vpc", "missing")))
	if err := resolveNMVpcAttachmentRefs(acct, st); err != nil {
		t.Fatalf("resolveNMVpcAttachmentRefs: %v", err)
	}
	rels, _ := st.RelationshipsFrom(attID)
	if len(rels) != 0 {
		t.Errorf("expected 0 relationships when targets unscanned, got %d", len(rels))
	}
}

func TestResolveNMSimpleAttachments(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	cnID := upsertTestResource(t, st, "aws", acct.ID, TypeNetworkManagerCoreNetwork,
		nmCoreNetworkID(acct.ID, "cn-1"), nmRegion, `{"CoreNetworkId":"cn-1"}`)

	cases := []struct {
		rtype   string
		fn      func(*account, *store.Store) error
		nidPath string
	}{
		{TypeNetworkManagerConnectAttachment, resolveNMConnectAttachmentRefs, "connect"},
		{TypeNetworkManagerSiteToSiteVpnAttachment, resolveNMSiteToSiteVpnAttachmentRefs, "s2s"},
		{TypeNetworkManagerDirectConnectGatewayAttachment, resolveNMDirectConnectGatewayAttachmentRefs, "dxgw"},
		{TypeNetworkManagerTransitGatewayRouteTableAttachment, resolveNMTGWRouteTableAttachmentRefs, "tgwrt"},
	}
	for _, tc := range cases {
		t.Run(tc.rtype, func(t *testing.T) {
			attNID := fmt.Sprintf("arn:aws:networkmanager::%s:attachment/%s", acct.ID, tc.nidPath)
			attID := upsertTestResource(t, st, "aws", acct.ID, tc.rtype, attNID, nmRegion,
				`{"CoreNetworkId":"cn-1"}`)
			if err := tc.fn(acct, st); err != nil {
				t.Fatalf("%s resolver: %v", tc.rtype, err)
			}
			rels, _ := st.RelationshipsFrom(attID)
			if len(rels) != 1 {
				t.Fatalf("%s: expected 1 relationship, got %d", tc.rtype, len(rels))
			}
			assertRelationship(t, rels, attID, cnID, store.RelAttachedTo)
		})
	}
}

func TestResolveNMSimpleAttachments_EmptyAttrs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	attID := upsertTestResource(t, st, "aws", acct.ID, TypeNetworkManagerConnectAttachment,
		fmt.Sprintf("arn:aws:networkmanager::%s:attachment/empty", acct.ID), nmRegion, "{}")
	if err := resolveNMConnectAttachmentRefs(acct, st); err != nil {
		t.Fatalf("resolveNMConnectAttachmentRefs: %v", err)
	}
	rels, _ := st.RelationshipsFrom(attID)
	if len(rels) != 0 {
		t.Errorf("expected 0 relationships, got %d", len(rels))
	}
}

func TestResolveNMTransitGatewayPeeringRefs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	cnID := upsertTestResource(t, st, "aws", acct.ID, TypeNetworkManagerCoreNetwork,
		nmCoreNetworkID(acct.ID, "cn-1"), nmRegion, `{"CoreNetworkId":"cn-1"}`)
	tgwARN := ec2ARN("us-east-1", acct.ID, "transit-gateway", "tgw-peer")
	tgwID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2TransitGateway, tgwARN, "us-east-1", "{}")
	peerID := upsertTestResource(t, st, "aws", acct.ID, TypeNetworkManagerTransitGatewayPeering,
		fmt.Sprintf("arn:aws:networkmanager::%s:peering/peer-1", acct.ID), nmRegion,
		fmt.Sprintf(`{"PeeringId":"peer-1","CoreNetworkId":"cn-1","ResourceArn":"%s"}`, tgwARN))

	if err := resolveNMTransitGatewayPeeringRefs(acct, st); err != nil {
		t.Fatalf("resolveNMTransitGatewayPeeringRefs: %v", err)
	}
	rels, _ := st.RelationshipsFrom(peerID)
	if len(rels) != 2 {
		t.Fatalf("expected 2 relationships, got %d", len(rels))
	}
	assertRelationship(t, rels, peerID, cnID, store.RelAttachedTo)
	assertRelationship(t, rels, peerID, tgwID, store.RelAttachedTo)
}

func TestResolveNMTransitGatewayPeeringRefs_EmptyAttrs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	peerID := upsertTestResource(t, st, "aws", acct.ID, TypeNetworkManagerTransitGatewayPeering,
		fmt.Sprintf("arn:aws:networkmanager::%s:peering/empty", acct.ID), nmRegion, "{}")
	if err := resolveNMTransitGatewayPeeringRefs(acct, st); err != nil {
		t.Fatalf("resolveNMTransitGatewayPeeringRefs: %v", err)
	}
	rels, _ := st.RelationshipsFrom(peerID)
	if len(rels) != 0 {
		t.Errorf("expected 0 relationships, got %d", len(rels))
	}
}

func TestResolveNMConnectPeerRefs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	cnID := upsertTestResource(t, st, "aws", acct.ID, TypeNetworkManagerCoreNetwork,
		nmCoreNetworkID(acct.ID, "cn-1"), nmRegion, `{"CoreNetworkId":"cn-1"}`)
	cpID := upsertTestResource(t, st, "aws", acct.ID, TypeNetworkManagerConnectPeer,
		fmt.Sprintf("arn:aws:networkmanager::%s:connect-peer/cp-1", acct.ID), nmRegion,
		`{"ConnectPeerId":"cp-1","CoreNetworkId":"cn-1"}`)

	if err := resolveNMConnectPeerRefs(acct, st); err != nil {
		t.Fatalf("resolveNMConnectPeerRefs: %v", err)
	}
	rels, _ := st.RelationshipsFrom(cpID)
	if len(rels) != 1 {
		t.Fatalf("expected 1 relationship, got %d", len(rels))
	}
	assertRelationship(t, rels, cpID, cnID, store.RelAttachedTo)
}

func TestResolveNMConnectPeerRefs_EmptyAttrs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	cpID := upsertTestResource(t, st, "aws", acct.ID, TypeNetworkManagerConnectPeer,
		fmt.Sprintf("arn:aws:networkmanager::%s:connect-peer/empty", acct.ID), nmRegion, "{}")
	if err := resolveNMConnectPeerRefs(acct, st); err != nil {
		t.Fatalf("resolveNMConnectPeerRefs: %v", err)
	}
	rels, _ := st.RelationshipsFrom(cpID)
	if len(rels) != 0 {
		t.Errorf("expected 0 relationships, got %d", len(rels))
	}
}

func TestResolveNMCorePLAssocRefs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	cnARN := fmt.Sprintf("arn:aws:networkmanager::%s:core-network/core-1", acct.ID)
	cnID := upsertTestResource(t, st, "aws", acct.ID, TypeNetworkManagerCoreNetwork, cnARN, testRegion, "{}")
	plARN := fmt.Sprintf("arn:aws:ec2:%s:%s:prefix-list/pl-1", testRegion, acct.ID)
	plID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2PrefixList, plARN, testRegion, "{}")
	paARN := fmt.Sprintf("%s/prefix-list-association/%s", cnARN, plARN)
	paID := upsertTestResource(t, st, "aws", acct.ID, TypeNetworkManagerCoreNetworkPrefixListAssociation, paARN, testRegion,
		fmt.Sprintf(`{"PrefixListArn":"%s"}`, plARN))
	if err := resolveNMCorePLAssocRefs(acct, st); err != nil {
		t.Fatalf("resolveNMCorePLAssocRefs: %v", err)
	}
	rels, _ := st.RelationshipsFrom(paID)
	assertRelationship(t, rels, paID, cnID, store.RelAttachedTo)
	assertRelationship(t, rels, paID, plID, store.RelAttachedTo)
}
