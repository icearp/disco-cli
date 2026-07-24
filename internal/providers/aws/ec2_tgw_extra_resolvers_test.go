package aws

import (
	"testing"

	"github.com/icearp/disco-cli/store"
)

func TestResolveTGWPolicyTableRelationships(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	region := "us-east-1"

	tgwARN := ec2ARN(region, acct.ID, "transit-gateway", "tgw-1")
	tgwID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2TransitGateway, tgwARN, region, "{}")
	ptARN := ec2ARN(region, acct.ID, "transit-gateway-policy-table", "tgw-ptb-1")
	ptID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2TransitGatewayPolicyTable, ptARN, region, `{"TransitGatewayId":"tgw-1"}`)

	if err := resolveTGWPolicyTableRelationships(acct, st); err != nil {
		t.Fatalf("resolveTGWPolicyTableRelationships: %v", err)
	}
	rels, _ := st.RelationshipsFrom(ptID)
	assertRelationship(t, rels, ptID, tgwID, store.RelAttachedTo)
}

func TestResolveTGWPolicyTableRelationships_NoAttrs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	region := "us-east-1"
	ptARN := ec2ARN(region, acct.ID, "transit-gateway-policy-table", "tgw-ptb-1")
	ptID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2TransitGatewayPolicyTable, ptARN, region, "{}")
	if err := resolveTGWPolicyTableRelationships(acct, st); err != nil {
		t.Fatalf("empty: %v", err)
	}
	if rels, _ := st.RelationshipsFrom(ptID); len(rels) != 0 {
		t.Errorf("emitted %d edges, want 0", len(rels))
	}
}

func TestResolveTGWRouteTableAnnouncementRelationships(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	region := "us-east-1"

	tgwID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2TransitGateway,
		ec2ARN(region, acct.ID, "transit-gateway", "tgw-1"), region, "{}")
	rtID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2TransitGatewayRouteTable,
		ec2ARN(region, acct.ID, "transit-gateway-route-table", "tgw-rtb-1"), region, "{}")
	peerID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2TransitGatewayPeeringAttachment,
		ec2ARN(region, acct.ID, "transit-gateway-peering-attachment", "tgw-attach-1"), region, "{}")
	anARN := ec2ARN(region, acct.ID, "transit-gateway-route-table-announcement", "tgw-rtba-1")
	attrs := `{"TransitGatewayId":"tgw-1","TransitGatewayRouteTableId":"tgw-rtb-1","PeeringAttachmentId":"tgw-attach-1"}`
	anID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2TransitGatewayRouteTableAnnouncement, anARN, region, attrs)

	if err := resolveTGWRouteTableAnnouncementRelationships(acct, st); err != nil {
		t.Fatalf("resolveTGWRouteTableAnnouncementRelationships: %v", err)
	}
	rels, _ := st.RelationshipsFrom(anID)
	assertRelationship(t, rels, anID, tgwID, store.RelAttachedTo)
	assertRelationship(t, rels, anID, rtID, store.RelAttachedTo)
	assertRelationship(t, rels, anID, peerID, store.RelAttachedTo)
}

func TestResolveTGWRouteTableAnnouncementRelationships_NoAttrs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	region := "us-east-1"
	anARN := ec2ARN(region, acct.ID, "transit-gateway-route-table-announcement", "tgw-rtba-1")
	anID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2TransitGatewayRouteTableAnnouncement, anARN, region, "{}")
	if err := resolveTGWRouteTableAnnouncementRelationships(acct, st); err != nil {
		t.Fatalf("empty: %v", err)
	}
	if rels, _ := st.RelationshipsFrom(anID); len(rels) != 0 {
		t.Errorf("emitted %d edges, want 0", len(rels))
	}
}
