package aws

import (
	"testing"

	"codeburg.org/icearp/disco/internal/store"
)

func TestResolveSubnetVPCRelationships(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	subnetARN := ec2ARN(testRegion, acct.ID, "subnet", "subnet-abc")
	subnetID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2Subnet, subnetARN, testRegion, `{"VpcId": "vpc-xyz"}`)
	vpcID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2VPC,
		ec2ARN(testRegion, acct.ID, "vpc", "vpc-xyz"), testRegion, "{}")

	if err := resolveSubnetVPCRelationships(acct, st); err != nil {
		t.Fatalf("resolveSubnetVPCRelationships: %v", err)
	}

	rels, err := st.RelationshipsFrom(subnetID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 1 {
		t.Fatalf("expected 1 relationship, got %d", len(rels))
	}
	assertRelationship(t, rels, subnetID, vpcID, store.RelAttachedTo)
}

func TestResolveIGWRelationships(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	igwARN := ec2ARN(testRegion, acct.ID, "internet-gateway", "igw-001")
	igwID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2InternetGateway, igwARN, testRegion,
		`{"Attachments": [{"VpcId": "vpc-abc"}]}`)
	vpcID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2VPC,
		ec2ARN(testRegion, acct.ID, "vpc", "vpc-abc"), testRegion, "{}")

	if err := resolveIGWRelationships(acct, st); err != nil {
		t.Fatalf("resolveIGWRelationships: %v", err)
	}

	rels, err := st.RelationshipsFrom(igwID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 1 {
		t.Fatalf("expected 1 relationship, got %d", len(rels))
	}
	assertRelationship(t, rels, igwID, vpcID, store.RelAttachedTo)
}

func TestResolveRouteTableRelationships(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	rtARN := ec2ARN(testRegion, acct.ID, "route-table", "rtb-001")
	rtID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2RouteTable, rtARN, testRegion, `{"VpcId":"vpc-001"}`)
	vpcID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2VPC, ec2ARN(testRegion, acct.ID, "vpc", "vpc-001"), testRegion, "{}")

	if err := resolveRouteTableRelationships(acct, st); err != nil {
		t.Fatalf("resolveRouteTableRelationships: %v", err)
	}
	rels, err := st.RelationshipsFrom(rtID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 1 {
		t.Fatalf("expected 1 relationship, got %d", len(rels))
	}
	assertRelationship(t, rels, rtID, vpcID, store.RelAttachedTo)
}

func TestResolveRouteTableRelationships_EmptyAttrs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	rtID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2RouteTable, ec2ARN(testRegion, acct.ID, "route-table", "rtb-bare"), testRegion, "{}")
	if err := resolveRouteTableRelationships(acct, st); err != nil {
		t.Fatalf("resolveRouteTableRelationships: %v", err)
	}
	rels, _ := st.RelationshipsFrom(rtID)
	if len(rels) != 0 {
		t.Errorf("expected 0 relationships, got %d", len(rels))
	}
}

func TestResolveNatGatewayRelationships(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	ngwARN := ec2ARN(testRegion, acct.ID, "natgateway", "nat-001")
	ngwID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2NatGateway, ngwARN, testRegion,
		`{"SubnetId":"subnet-001","VpcId":"vpc-001"}`)
	subnetID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2Subnet, ec2ARN(testRegion, acct.ID, "subnet", "subnet-001"), testRegion, "{}")
	vpcID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2VPC, ec2ARN(testRegion, acct.ID, "vpc", "vpc-001"), testRegion, "{}")

	if err := resolveNatGatewayRelationships(acct, st); err != nil {
		t.Fatalf("resolveNatGatewayRelationships: %v", err)
	}
	rels, err := st.RelationshipsFrom(ngwID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 2 {
		t.Fatalf("expected 2 relationships, got %d", len(rels))
	}
	assertRelationship(t, rels, ngwID, subnetID, store.RelAttachedTo)
	assertRelationship(t, rels, ngwID, vpcID, store.RelAttachedTo)
}

func TestResolveNatGatewayRelationships_EmptyAttrs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	ngwID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2NatGateway, ec2ARN(testRegion, acct.ID, "natgateway", "nat-bare"), testRegion, "{}")
	if err := resolveNatGatewayRelationships(acct, st); err != nil {
		t.Fatalf("resolveNatGatewayRelationships: %v", err)
	}
	rels, _ := st.RelationshipsFrom(ngwID)
	if len(rels) != 0 {
		t.Errorf("expected 0 relationships, got %d", len(rels))
	}
}

func TestResolveEIPRelationships(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	eipARN := ec2ARN(testRegion, acct.ID, "elastic-ip", "eipalloc-001")
	eipID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2EIP, eipARN, testRegion,
		`{"InstanceId":"i-001","NetworkInterfaceId":"eni-001"}`)
	instID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2Instance, ec2ARN(testRegion, acct.ID, "instance", "i-001"), testRegion, "{}")
	eniID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2NetworkInterface, ec2ARN(testRegion, acct.ID, "network-interface", "eni-001"), testRegion, "{}")

	if err := resolveEIPRelationships(acct, st); err != nil {
		t.Fatalf("resolveEIPRelationships: %v", err)
	}
	rels, err := st.RelationshipsFrom(eipID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 2 {
		t.Fatalf("expected 2 relationships, got %d", len(rels))
	}
	assertRelationship(t, rels, eipID, instID, store.RelAttachedTo)
	assertRelationship(t, rels, eipID, eniID, store.RelAttachedTo)
}

func TestResolveEIPRelationships_EmptyAttrs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	eipID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2EIP, ec2ARN(testRegion, acct.ID, "elastic-ip", "eipalloc-bare"), testRegion, "{}")
	if err := resolveEIPRelationships(acct, st); err != nil {
		t.Fatalf("resolveEIPRelationships: %v", err)
	}
	rels, _ := st.RelationshipsFrom(eipID)
	if len(rels) != 0 {
		t.Errorf("expected 0 relationships, got %d", len(rels))
	}
}

func TestResolveNetworkInterfaceRelationships(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	eniARN := ec2ARN(testRegion, acct.ID, "network-interface", "eni-001")
	eniID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2NetworkInterface, eniARN, testRegion,
		`{"SubnetId":"subnet-001","VpcId":"vpc-001","Groups":[{"GroupId":"sg-001"}]}`)
	subnetID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2Subnet, ec2ARN(testRegion, acct.ID, "subnet", "subnet-001"), testRegion, "{}")
	vpcID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2VPC, ec2ARN(testRegion, acct.ID, "vpc", "vpc-001"), testRegion, "{}")
	sgID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2SecurityGroup, ec2ARN(testRegion, acct.ID, "security-group", "sg-001"), testRegion, "{}")

	if err := resolveNetworkInterfaceRelationships(acct, st); err != nil {
		t.Fatalf("resolveNetworkInterfaceRelationships: %v", err)
	}
	rels, err := st.RelationshipsFrom(eniID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 3 {
		t.Fatalf("expected 3 relationships, got %d", len(rels))
	}
	assertRelationship(t, rels, eniID, subnetID, store.RelAttachedTo)
	assertRelationship(t, rels, eniID, vpcID, store.RelAttachedTo)
	assertRelationship(t, rels, eniID, sgID, store.RelUses)
}

func TestResolveNetworkInterfaceRelationships_EmptyAttrs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	eniID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2NetworkInterface, ec2ARN(testRegion, acct.ID, "network-interface", "eni-bare"), testRegion, "{}")
	if err := resolveNetworkInterfaceRelationships(acct, st); err != nil {
		t.Fatalf("resolveNetworkInterfaceRelationships: %v", err)
	}
	rels, _ := st.RelationshipsFrom(eniID)
	if len(rels) != 0 {
		t.Errorf("expected 0 relationships, got %d", len(rels))
	}
}

func TestResolveNetworkACLRelationships(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	naclARN := ec2ARN(testRegion, acct.ID, "network-acl", "acl-001")
	naclID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2NetworkACL, naclARN, testRegion, `{"VpcId":"vpc-001"}`)
	vpcID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2VPC, ec2ARN(testRegion, acct.ID, "vpc", "vpc-001"), testRegion, "{}")

	if err := resolveNetworkACLRelationships(acct, st); err != nil {
		t.Fatalf("resolveNetworkACLRelationships: %v", err)
	}
	rels, err := st.RelationshipsFrom(naclID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 1 {
		t.Fatalf("expected 1 relationship, got %d", len(rels))
	}
	assertRelationship(t, rels, naclID, vpcID, store.RelAttachedTo)
}

func TestResolveNetworkACLRelationships_EmptyAttrs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	naclID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2NetworkACL, ec2ARN(testRegion, acct.ID, "network-acl", "acl-bare"), testRegion, "{}")
	if err := resolveNetworkACLRelationships(acct, st); err != nil {
		t.Fatalf("resolveNetworkACLRelationships: %v", err)
	}
	rels, _ := st.RelationshipsFrom(naclID)
	if len(rels) != 0 {
		t.Errorf("expected 0 relationships, got %d", len(rels))
	}
}

func TestResolveVPCEndpointRelationships(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	epARN := ec2ARN(testRegion, acct.ID, "vpc-endpoint", "vpce-001")
	epID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2VPCEndpoint, epARN, testRegion, `{"VpcId":"vpc-001"}`)
	vpcID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2VPC, ec2ARN(testRegion, acct.ID, "vpc", "vpc-001"), testRegion, "{}")

	if err := resolveVPCEndpointRelationships(acct, st); err != nil {
		t.Fatalf("resolveVPCEndpointRelationships: %v", err)
	}
	rels, err := st.RelationshipsFrom(epID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 1 {
		t.Fatalf("expected 1 relationship, got %d", len(rels))
	}
	assertRelationship(t, rels, epID, vpcID, store.RelAttachedTo)
}

func TestResolveVPCEndpointRelationships_EmptyAttrs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	epID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2VPCEndpoint, ec2ARN(testRegion, acct.ID, "vpc-endpoint", "vpce-bare"), testRegion, "{}")
	if err := resolveVPCEndpointRelationships(acct, st); err != nil {
		t.Fatalf("resolveVPCEndpointRelationships: %v", err)
	}
	rels, _ := st.RelationshipsFrom(epID)
	if len(rels) != 0 {
		t.Errorf("expected 0 relationships, got %d", len(rels))
	}
}

func TestResolveVPCPeeringRelationships(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	pcARN := ec2ARN(testRegion, acct.ID, "vpc-peering-connection", "pcx-001")
	pcID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2VPCPeeringConnection, pcARN, testRegion,
		`{"RequesterVpcInfo":{"VpcId":"vpc-req","OwnerId":"`+testAccountID+`","Region":"`+testRegion+`"},`+
			`"AccepterVpcInfo":{"VpcId":"vpc-acc","OwnerId":"`+testAccountID+`","Region":"`+testRegion+`"}}`)
	reqVpcID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2VPC, ec2ARN(testRegion, acct.ID, "vpc", "vpc-req"), testRegion, "{}")
	accVpcID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2VPC, ec2ARN(testRegion, acct.ID, "vpc", "vpc-acc"), testRegion, "{}")

	if err := resolveVPCPeeringRelationships(acct, st); err != nil {
		t.Fatalf("resolveVPCPeeringRelationships: %v", err)
	}
	rels, err := st.RelationshipsFrom(pcID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 2 {
		t.Fatalf("expected 2 relationships, got %d", len(rels))
	}
	assertRelationship(t, rels, pcID, reqVpcID, store.RelPeer)
	assertRelationship(t, rels, pcID, accVpcID, store.RelPeer)
}

func TestResolveVPCPeeringRelationships_EmptyAttrs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	pcID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2VPCPeeringConnection, ec2ARN(testRegion, acct.ID, "vpc-peering-connection", "pcx-bare"), testRegion, "{}")
	if err := resolveVPCPeeringRelationships(acct, st); err != nil {
		t.Fatalf("resolveVPCPeeringRelationships: %v", err)
	}
	rels, _ := st.RelationshipsFrom(pcID)
	if len(rels) != 0 {
		t.Errorf("expected 0 relationships, got %d", len(rels))
	}
}
