package aws

import (
	"testing"

	"codeburg.org/icearp/disco/internal/store"
)

func TestResolveLocalGatewayRouteTableVPCAssociationRelationships(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount("123456789012")
	region := "us-east-1"

	rtARN := "arn:aws:ec2:us-east-1:123456789012:local-gateway-route-table/lgw-rtb-aaa"
	rtID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2LocalGatewayRouteTable, rtARN, region, "{}")

	vpcARN := ec2ARN(region, acct.ID, "vpc", "vpc-bbb")
	vpcID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2VPC, vpcARN, region, "{}")

	assocARN := ec2ARN(region, acct.ID, "local-gateway-route-table-vpc-association", "lgw-vpc-assoc-ccc")
	attrsJSON := `{
		"LocalGatewayRouteTableArn": "arn:aws:ec2:us-east-1:123456789012:local-gateway-route-table/lgw-rtb-aaa",
		"VpcId": "vpc-bbb"
	}`
	assocID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2LocalGatewayRouteTableVPCAssociation, assocARN, region, attrsJSON)

	if err := resolveLocalGatewayRouteTableVPCAssociationRelationships(acct, st); err != nil {
		t.Fatalf("resolveLocalGatewayRouteTableVPCAssociationRelationships: %v", err)
	}

	rels, err := st.RelationshipsFrom(assocID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 2 {
		t.Errorf("expected 2 relationships, got %d", len(rels))
	}
	assertRelationship(t, rels, assocID, rtID, store.RelAttachedTo)
	assertRelationship(t, rels, assocID, vpcID, store.RelAttachedTo)
}

func TestResolveLocalGatewayRouteTableVPCAssociationRelationships_Empty(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount("123456789012")

	id := upsertTestResource(t, st, "aws", acct.ID, TypeEC2LocalGatewayRouteTableVPCAssociation,
		ec2ARN("us-east-1", acct.ID, "local-gateway-route-table-vpc-association", "bare"), "us-east-1", "{}")

	if err := resolveLocalGatewayRouteTableVPCAssociationRelationships(acct, st); err != nil {
		t.Fatalf("resolveLocalGatewayRouteTableVPCAssociationRelationships: %v", err)
	}

	rels, err := st.RelationshipsFrom(id)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("expected 0 relationships, got %d", len(rels))
	}
}

func TestResolveLocalGatewayRouteTableVIGAssociationRelationships(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount("123456789012")
	region := "us-east-1"

	rtARN := "arn:aws:ec2:us-east-1:123456789012:local-gateway-route-table/lgw-rtb-aaa"
	rtID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2LocalGatewayRouteTable, rtARN, region, "{}")

	vigARN := ec2ARN(region, acct.ID, "local-gateway-virtual-interface-group", "lgw-vif-grp-bbb")
	vigID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2LocalGatewayVirtualInterfaceGroup, vigARN, region, "{}")

	assocARN := ec2ARN(region, acct.ID, "local-gateway-route-table-virtual-interface-group-association", "lgw-vig-assoc-ccc")
	attrsJSON := `{
		"LocalGatewayRouteTableArn": "arn:aws:ec2:us-east-1:123456789012:local-gateway-route-table/lgw-rtb-aaa",
		"LocalGatewayVirtualInterfaceGroupId": "lgw-vif-grp-bbb"
	}`
	assocID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2LocalGatewayRouteTableVIGAssociation, assocARN, region, attrsJSON)

	if err := resolveLocalGatewayRouteTableVIGAssociationRelationships(acct, st); err != nil {
		t.Fatalf("resolveLocalGatewayRouteTableVIGAssociationRelationships: %v", err)
	}

	rels, err := st.RelationshipsFrom(assocID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 2 {
		t.Errorf("expected 2 relationships, got %d", len(rels))
	}
	assertRelationship(t, rels, assocID, rtID, store.RelAttachedTo)
	assertRelationship(t, rels, assocID, vigID, store.RelAttachedTo)
}
