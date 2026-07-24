package aws

import (
	"testing"

	"github.com/icearp/disco-cli/store"
)

func TestResolveTGWConnectRelationships(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount("123456789012")
	region := "us-east-1"

	tgwARN := "arn:aws:ec2:us-east-1:123456789012:transit-gateway/tgw-aaa"
	tgwID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2TransitGateway, tgwARN, region, "{}")

	connectARN := ec2ARN(region, acct.ID, "transit-gateway-connect", "tgw-attach-bbb")
	attrsJSON := `{"TransitGatewayId": "tgw-aaa"}`
	connectID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2TransitGatewayConnect, connectARN, region, attrsJSON)

	if err := resolveTGWConnectRelationships(acct, st); err != nil {
		t.Fatalf("resolveTGWConnectRelationships: %v", err)
	}

	rels, err := st.RelationshipsFrom(connectID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 1 {
		t.Errorf("expected 1 relationship, got %d", len(rels))
	}
	assertRelationship(t, rels, connectID, tgwID, store.RelAttachedTo)
}

func TestResolveTGWConnectRelationships_Empty(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount("123456789012")

	id := upsertTestResource(t, st, "aws", acct.ID, TypeEC2TransitGatewayConnect,
		ec2ARN("us-east-1", acct.ID, "transit-gateway-connect", "bare"), "us-east-1", "{}")

	if err := resolveTGWConnectRelationships(acct, st); err != nil {
		t.Fatalf("resolveTGWConnectRelationships: %v", err)
	}

	rels, err := st.RelationshipsFrom(id)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("expected 0 relationships, got %d", len(rels))
	}
}

func TestResolveTGWVPCAttachmentRelationships(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount("123456789012")
	region := "us-east-1"

	tgwARN := "arn:aws:ec2:us-east-1:123456789012:transit-gateway/tgw-aaa"
	tgwID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2TransitGateway, tgwARN, region, "{}")

	vpcARN := ec2ARN(region, acct.ID, "vpc", "vpc-ccc")
	vpcID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2VPC, vpcARN, region, "{}")

	attachARN := "arn:aws:ec2:us-east-1:123456789012:transit-gateway-attachment/tgw-attach-ddd"
	attrsJSON := `{"TransitGatewayId": "tgw-aaa", "VpcId": "vpc-ccc"}`
	attachID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2TransitGatewayVPCAttachment, attachARN, region, attrsJSON)

	if err := resolveTGWVPCAttachmentRelationships(acct, st); err != nil {
		t.Fatalf("resolveTGWVPCAttachmentRelationships: %v", err)
	}

	rels, err := st.RelationshipsFrom(attachID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 2 {
		t.Errorf("expected 2 relationships, got %d", len(rels))
	}
	assertRelationship(t, rels, attachID, tgwID, store.RelAttachedTo)
	assertRelationship(t, rels, attachID, vpcID, store.RelAttachedTo)
}
