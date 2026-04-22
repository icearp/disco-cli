package aws

import (
	"testing"

	"codeberg.org/icearp/disco/internal/store"
)

func TestResolveVPNConnectionRelationships(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	vpnARN := ec2ARN(testRegion, acct.ID, "vpn-connection", "vpn-001")
	vpnID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2VPNConnection, vpnARN, testRegion,
		`{"VpnGatewayId":"vgw-001","CustomerGatewayId":"cgw-001"}`)
	vgwID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2VPNGateway, ec2ARN(testRegion, acct.ID, "vpn-gateway", "vgw-001"), testRegion, "{}")
	cgwID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2CustomerGateway, ec2ARN(testRegion, acct.ID, "customer-gateway", "cgw-001"), testRegion, "{}")

	if err := resolveVPNConnectionRelationships(acct, st); err != nil {
		t.Fatalf("resolveVPNConnectionRelationships: %v", err)
	}
	rels, err := st.RelationshipsFrom(vpnID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 2 {
		t.Fatalf("expected 2 relationships, got %d", len(rels))
	}
	assertRelationship(t, rels, vpnID, vgwID, store.RelAttachedTo)
	assertRelationship(t, rels, vpnID, cgwID, store.RelAttachedTo)
}

func TestResolveVPNConnectionRelationships_EmptyAttrs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	vpnID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2VPNConnection, ec2ARN(testRegion, acct.ID, "vpn-connection", "vpn-bare"), testRegion, "{}")
	if err := resolveVPNConnectionRelationships(acct, st); err != nil {
		t.Fatalf("resolveVPNConnectionRelationships: %v", err)
	}
	rels, _ := st.RelationshipsFrom(vpnID)
	if len(rels) != 0 {
		t.Errorf("expected 0 relationships, got %d", len(rels))
	}
}

func TestResolveTGWAttachmentRelationships(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	tgwARN := ec2ARN(testRegion, acct.ID, "transit-gateway", "tgw-001")
	tgwID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2TransitGateway, tgwARN, testRegion, "{}")
	vpcID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2VPC, ec2ARN(testRegion, acct.ID, "vpc", "vpc-001"), testRegion, "{}")

	attARN := ec2ARN(testRegion, acct.ID, "transit-gateway-attachment", "tgw-attach-001")
	attID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2TransitGatewayAttachment, attARN, testRegion,
		`{"TransitGatewayArn":"`+tgwARN+`","ResourceType":"vpc","ResourceId":"vpc-001"}`)

	if err := resolveTGWAttachmentRelationships(acct, st); err != nil {
		t.Fatalf("resolveTGWAttachmentRelationships: %v", err)
	}
	rels, err := st.RelationshipsFrom(attID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 2 {
		t.Fatalf("expected 2 relationships, got %d", len(rels))
	}
	assertRelationship(t, rels, attID, tgwID, store.RelAttachedTo)
	assertRelationship(t, rels, attID, vpcID, store.RelAttachedTo)
}

func TestResolveTGWAttachmentRelationships_EmptyAttrs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	attID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2TransitGatewayAttachment, ec2ARN(testRegion, acct.ID, "transit-gateway-attachment", "tgw-attach-bare"), testRegion, "{}")
	if err := resolveTGWAttachmentRelationships(acct, st); err != nil {
		t.Fatalf("resolveTGWAttachmentRelationships: %v", err)
	}
	rels, _ := st.RelationshipsFrom(attID)
	if len(rels) != 0 {
		t.Errorf("expected 0 relationships, got %d", len(rels))
	}
}
