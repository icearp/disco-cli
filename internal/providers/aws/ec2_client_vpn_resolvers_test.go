package aws

import (
	"testing"

	"codeberg.org/icearp/disco/internal/store"
)

func TestResolveClientVPNEndpointRelationships(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount("123456789012")
	region := "us-east-1"

	vpcARN := ec2ARN(region, acct.ID, "vpc", "vpc-aaa")
	vpcID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2VPC, vpcARN, region, "{}")

	epARN := ec2ARN(region, acct.ID, "client-vpn-endpoint", "cvpn-endpoint-bbb")
	attrsJSON := `{"VpcId": "vpc-aaa"}`
	epID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2ClientVPNEndpoint, epARN, region, attrsJSON)

	if err := resolveClientVPNEndpointRelationships(acct, st); err != nil {
		t.Fatalf("resolveClientVPNEndpointRelationships: %v", err)
	}

	rels, err := st.RelationshipsFrom(epID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 1 {
		t.Errorf("expected 1 relationship, got %d", len(rels))
	}
	assertRelationship(t, rels, epID, vpcID, store.RelAttachedTo)
}

func TestResolveClientVPNEndpointRelationships_Empty(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount("123456789012")

	id := upsertTestResource(t, st, "aws", acct.ID, TypeEC2ClientVPNEndpoint,
		ec2ARN("us-east-1", acct.ID, "client-vpn-endpoint", "bare"), "us-east-1", "{}")

	if err := resolveClientVPNEndpointRelationships(acct, st); err != nil {
		t.Fatalf("resolveClientVPNEndpointRelationships: %v", err)
	}

	rels, err := st.RelationshipsFrom(id)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("expected 0 relationships, got %d", len(rels))
	}
}

func TestResolveClientVPNTargetNetworkAssociationRelationships(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount("123456789012")
	region := "us-east-1"

	epARN := ec2ARN(region, acct.ID, "client-vpn-endpoint", "cvpn-endpoint-bbb")
	epID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2ClientVPNEndpoint, epARN, region, "{}")

	subnetARN := ec2ARN(region, acct.ID, "subnet", "subnet-ccc")
	subnetID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2Subnet, subnetARN, region, "{}")

	assocARN := ec2ARN(region, acct.ID, "client-vpn-target-network-association", "cvpn-assoc-ddd")
	attrsJSON := `{"ClientVpnEndpointId": "cvpn-endpoint-bbb", "TargetNetworkId": "subnet-ccc"}`
	assocID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2ClientVPNTargetNetworkAssociation, assocARN, region, attrsJSON)

	if err := resolveClientVPNTargetNetworkAssociationRelationships(acct, st); err != nil {
		t.Fatalf("resolveClientVPNTargetNetworkAssociationRelationships: %v", err)
	}

	rels, err := st.RelationshipsFrom(assocID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 2 {
		t.Errorf("expected 2 relationships, got %d", len(rels))
	}
	assertRelationship(t, rels, assocID, epID, store.RelAttachedTo)
	assertRelationship(t, rels, assocID, subnetID, store.RelAttachedTo)
}
