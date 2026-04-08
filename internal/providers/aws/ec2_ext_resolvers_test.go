package aws

import (
	"testing"

	"codeburg.org/icearp/disco/internal/store"
)

func TestResolveCarrierGatewayRelationships(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount("123456789012")
	region := "us-east-1"

	vpcARN := ec2ARN(region, acct.ID, "vpc", "vpc-aaa")
	vpcID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2VPC, vpcARN, region, "{}")

	cgwARN := ec2ARN(region, acct.ID, "carrier-gateway", "cagw-bbb")
	cgwID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2CarrierGateway, cgwARN, region,
		`{"VpcId": "vpc-aaa"}`)

	if err := resolveCarrierGatewayRelationships(acct, st); err != nil {
		t.Fatalf("resolveCarrierGatewayRelationships: %v", err)
	}

	rels, err := st.RelationshipsFrom(cgwID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 1 {
		t.Errorf("expected 1 relationship, got %d", len(rels))
	}
	assertRelationship(t, rels, cgwID, vpcID, store.RelAttachedTo)
}

func TestResolveCarrierGatewayRelationships_Empty(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount("123456789012")

	id := upsertTestResource(t, st, "aws", acct.ID, TypeEC2CarrierGateway,
		ec2ARN("us-east-1", acct.ID, "carrier-gateway", "bare"), "us-east-1", "{}")

	if err := resolveCarrierGatewayRelationships(acct, st); err != nil {
		t.Fatalf("resolveCarrierGatewayRelationships: %v", err)
	}

	rels, err := st.RelationshipsFrom(id)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("expected 0 relationships, got %d", len(rels))
	}
}

func TestResolveSecurityGroupRuleRelationships(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount("123456789012")
	region := "us-east-1"

	sgARN := ec2ARN(region, acct.ID, "security-group", "sg-aaa")
	sgID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2SecurityGroup, sgARN, region, "{}")

	ingressARN := ec2ARN(region, acct.ID, "security-group-rule", "sgr-ingress")
	ingressID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2SecurityGroupIngress, ingressARN, region,
		`{"GroupId": "sg-aaa"}`)

	egressARN := ec2ARN(region, acct.ID, "security-group-rule", "sgr-egress")
	egressID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2SecurityGroupEgress, egressARN, region,
		`{"GroupId": "sg-aaa"}`)

	if err := resolveSecurityGroupRuleRelationships(acct, st); err != nil {
		t.Fatalf("resolveSecurityGroupRuleRelationships: %v", err)
	}

	for _, id := range []string{ingressID, egressID} {
		rels, err := st.RelationshipsFrom(id)
		if err != nil {
			t.Fatalf("RelationshipsFrom(%s): %v", id, err)
		}
		if len(rels) != 1 {
			t.Errorf("expected 1 relationship, got %d", len(rels))
		}
		assertRelationship(t, rels, id, sgID, store.RelAttachedTo)
	}
}

func TestResolveVolumeAttachmentRelationships(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount("123456789012")
	region := "us-east-1"

	volARN := ec2ARN(region, acct.ID, "volume", "vol-aaa")
	volID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2Volume, volARN, region, "{}")

	instARN := ec2ARN(region, acct.ID, "instance", "i-bbb")
	instID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2Instance, instARN, region, "{}")

	attARN := ec2ARN(region, acct.ID, "volume-attachment", "vol-aaa/i-bbb")
	attID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2VolumeAttachment, attARN, region,
		`{"VolumeId": "vol-aaa", "InstanceId": "i-bbb"}`)

	if err := resolveVolumeAttachmentRelationships(acct, st); err != nil {
		t.Fatalf("resolveVolumeAttachmentRelationships: %v", err)
	}

	rels, err := st.RelationshipsFrom(attID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 2 {
		t.Errorf("expected 2 relationships, got %d", len(rels))
	}
	assertRelationship(t, rels, attID, volID, store.RelAttachedTo)
	assertRelationship(t, rels, attID, instID, store.RelAttachedTo)
}

func TestResolveSubnetRouteTableAssociationRelationships(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount("123456789012")
	region := "us-east-1"

	subnetARN := ec2ARN(region, acct.ID, "subnet", "subnet-aaa")
	subnetID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2Subnet, subnetARN, region, "{}")

	rtARN := ec2ARN(region, acct.ID, "route-table", "rtb-bbb")
	rtID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2RouteTable, rtARN, region, "{}")

	assocID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2SubnetRouteTableAssociation,
		"rtbassoc-ccc", region,
		`{"SubnetId": "subnet-aaa", "RouteTableId": "rtb-bbb"}`)

	if err := resolveSubnetRouteTableAssociationRelationships(acct, st); err != nil {
		t.Fatalf("resolveSubnetRouteTableAssociationRelationships: %v", err)
	}

	rels, err := st.RelationshipsFrom(assocID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 2 {
		t.Errorf("expected 2 relationships, got %d", len(rels))
	}
	assertRelationship(t, rels, assocID, subnetID, store.RelAttachedTo)
	assertRelationship(t, rels, assocID, rtID, store.RelAttachedTo)
}

func TestResolveSubnetNetworkACLAssociationRelationships(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount("123456789012")
	region := "us-east-1"

	subnetARN := ec2ARN(region, acct.ID, "subnet", "subnet-aaa")
	subnetID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2Subnet, subnetARN, region, "{}")

	naclARN := ec2ARN(region, acct.ID, "network-acl", "acl-bbb")
	naclID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2NetworkACL, naclARN, region, "{}")

	assocID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2SubnetNetworkACLAssociation,
		"aclassoc-ccc", region,
		`{"SubnetId": "subnet-aaa", "NetworkAclId": "acl-bbb"}`)

	if err := resolveSubnetNetworkACLAssociationRelationships(acct, st); err != nil {
		t.Fatalf("resolveSubnetNetworkACLAssociationRelationships: %v", err)
	}

	rels, err := st.RelationshipsFrom(assocID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 2 {
		t.Errorf("expected 2 relationships, got %d", len(rels))
	}
	assertRelationship(t, rels, assocID, subnetID, store.RelAttachedTo)
	assertRelationship(t, rels, assocID, naclID, store.RelAttachedTo)
}

func TestResolveVPCDHCPOptionsAssociationRelationships(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount("123456789012")
	region := "us-east-1"

	vpcARN := ec2ARN(region, acct.ID, "vpc", "vpc-aaa")
	vpcID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2VPC, vpcARN, region, "{}")

	dhcpARN := ec2ARN(region, acct.ID, "dhcp-options", "dopt-bbb")
	dhcpID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2DHCPOptions, dhcpARN, region, "{}")

	assocARN := ec2ARN(region, acct.ID, "vpc-dhcp-options-association", "vpc-aaa/dopt-bbb")
	assocID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2VPCDHCPOptionsAssociation, assocARN, region,
		`{"VpcId": "vpc-aaa", "DhcpOptionsId": "dopt-bbb"}`)

	if err := resolveVPCDHCPOptionsAssociationRelationships(acct, st); err != nil {
		t.Fatalf("resolveVPCDHCPOptionsAssociationRelationships: %v", err)
	}

	rels, err := st.RelationshipsFrom(assocID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 2 {
		t.Errorf("expected 2 relationships, got %d", len(rels))
	}
	assertRelationship(t, rels, assocID, vpcID, store.RelAttachedTo)
	assertRelationship(t, rels, assocID, dhcpID, store.RelAttachedTo)
}

func TestResolveVPCGatewayAttachmentRelationships(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount("123456789012")
	region := "us-east-1"

	vpcARN := ec2ARN(region, acct.ID, "vpc", "vpc-aaa")
	vpcID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2VPC, vpcARN, region, "{}")

	attARN := ec2ARN(region, acct.ID, "vpc-gateway-attachment", "igw-bbb/vpc-aaa")
	attID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2VPCGatewayAttachment, attARN, region,
		`{"VpcId": "vpc-aaa"}`)

	if err := resolveVPCGatewayAttachmentRelationships(acct, st); err != nil {
		t.Fatalf("resolveVPCGatewayAttachmentRelationships: %v", err)
	}

	rels, err := st.RelationshipsFrom(attID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 1 {
		t.Errorf("expected 1 relationship, got %d", len(rels))
	}
	assertRelationship(t, rels, attID, vpcID, store.RelAttachedTo)
}
