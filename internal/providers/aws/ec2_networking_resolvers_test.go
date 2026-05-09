package aws

import (
	"testing"

	"codeberg.org/icearp/disco/store"
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
	attrs := `{"VpcId":"vpc-001","Associations":[{"SubnetId":"subnet-001"},{"Main":true},{"SubnetId":"subnet-unscanned"}]}`
	rtID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2RouteTable, rtARN, testRegion, attrs)
	vpcID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2VPC, ec2ARN(testRegion, acct.ID, "vpc", "vpc-001"), testRegion, "{}")
	subnetID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2Subnet, ec2ARN(testRegion, acct.ID, "subnet", "subnet-001"), testRegion, "{}")

	if err := resolveRouteTableRelationships(acct, st); err != nil {
		t.Fatalf("resolveRouteTableRelationships: %v", err)
	}
	rels, err := st.RelationshipsFrom(rtID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 2 {
		t.Fatalf("expected 2 relationships, got %d", len(rels))
	}
	assertRelationship(t, rels, rtID, vpcID, store.RelAttachedTo)
	assertRelationship(t, rels, rtID, subnetID, store.RelAttachedTo)
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

func TestResolveRouteTableRoutes(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	rtARN := ec2ARN(testRegion, acct.ID, "route-table", "rtb-001")
	attrs := `{"Routes":[
		{"GatewayId":"local"},
		{"GatewayId":"igw-001","DestinationCidrBlock":"0.0.0.0/0"},
		{"NatGatewayId":"nat-001"},
		{"TransitGatewayId":"tgw-001"},
		{"VpcPeeringConnectionId":"pcx-001"},
		{"GatewayId":"vpce-001"},
		{"InstanceId":"i-001"}
	]}`
	rtID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2RouteTable, rtARN, testRegion, attrs)
	igwID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2InternetGateway, ec2ARN(testRegion, acct.ID, "internet-gateway", "igw-001"), testRegion, "{}")
	natID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2NatGateway, ec2ARN(testRegion, acct.ID, "natgateway", "nat-001"), testRegion, "{}")
	tgwID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2TransitGateway, ec2ARN(testRegion, acct.ID, "transit-gateway", "tgw-001"), testRegion, "{}")
	pcxID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2VPCPeeringConnection, ec2ARN(testRegion, acct.ID, "vpc-peering-connection", "pcx-001"), testRegion, "{}")
	vpceID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2VPCEndpoint, ec2ARN(testRegion, acct.ID, "vpc-endpoint", "vpce-001"), testRegion, "{}")
	instID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2Instance, ec2ARN(testRegion, acct.ID, "instance", "i-001"), testRegion, "{}")

	if err := resolveRouteTableRoutes(acct, st); err != nil {
		t.Fatalf("resolveRouteTableRoutes: %v", err)
	}
	rels, err := st.RelationshipsFrom(rtID, store.RelRoutesTo)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 6 {
		t.Fatalf("expected 6 routes-to edges, got %d", len(rels))
	}
	for _, want := range []string{igwID, natID, tgwID, pcxID, vpceID, instID} {
		assertRelationship(t, rels, rtID, want, store.RelRoutesTo)
	}
}

func TestResolveRouteTableRoutes_UnscannedTargetsSkipped(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	rtARN := ec2ARN(testRegion, acct.ID, "route-table", "rtb-001")
	rtID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2RouteTable, rtARN, testRegion,
		`{"Routes":[{"GatewayId":"igw-missing"},{"NatGatewayId":"nat-missing"}]}`)
	if err := resolveRouteTableRoutes(acct, st); err != nil {
		t.Fatalf("resolveRouteTableRoutes: %v", err)
	}
	rels, _ := st.RelationshipsFrom(rtID, store.RelRoutesTo)
	if len(rels) != 0 {
		t.Errorf("expected 0 routes-to edges (FK-safe skip), got %d", len(rels))
	}
}

func TestResolveSecurityGroupVPC(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	sgARN := ec2ARN(testRegion, acct.ID, "security-group", "sg-001")
	sgID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2SecurityGroup, sgARN, testRegion, `{"VpcId":"vpc-001"}`)
	vpcID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2VPC, ec2ARN(testRegion, acct.ID, "vpc", "vpc-001"), testRegion, "{}")

	if err := resolveSecurityGroupVPC(acct, st); err != nil {
		t.Fatalf("resolveSecurityGroupVPC: %v", err)
	}
	rels, err := st.RelationshipsFrom(sgID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 1 {
		t.Fatalf("expected 1 relationship, got %d", len(rels))
	}
	assertRelationship(t, rels, sgID, vpcID, store.RelAttachedTo)
}

func TestResolveSecurityGroupVPC_EmptyAttrs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	sgID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2SecurityGroup, ec2ARN(testRegion, acct.ID, "security-group", "sg-bare"), testRegion, "{}")
	if err := resolveSecurityGroupVPC(acct, st); err != nil {
		t.Fatalf("resolveSecurityGroupVPC: %v", err)
	}
	rels, _ := st.RelationshipsFrom(sgID)
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
	attrs := `{"VpcId":"vpc-001","Associations":[{"SubnetId":"subnet-001"},{"SubnetId":"subnet-002"}]}`
	naclID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2NetworkACL, naclARN, testRegion, attrs)
	vpcID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2VPC, ec2ARN(testRegion, acct.ID, "vpc", "vpc-001"), testRegion, "{}")
	sub1 := upsertTestResource(t, st, "aws", acct.ID, TypeEC2Subnet, ec2ARN(testRegion, acct.ID, "subnet", "subnet-001"), testRegion, "{}")
	sub2 := upsertTestResource(t, st, "aws", acct.ID, TypeEC2Subnet, ec2ARN(testRegion, acct.ID, "subnet", "subnet-002"), testRegion, "{}")

	if err := resolveNetworkACLRelationships(acct, st); err != nil {
		t.Fatalf("resolveNetworkACLRelationships: %v", err)
	}
	rels, err := st.RelationshipsFrom(naclID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 3 {
		t.Fatalf("expected 3 relationships (vpc + 2 subnets), got %d", len(rels))
	}
	assertRelationship(t, rels, naclID, vpcID, store.RelAttachedTo)
	assertRelationship(t, rels, naclID, sub1, store.RelAttachedTo)
	assertRelationship(t, rels, naclID, sub2, store.RelAttachedTo)
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

func TestResolveCarrierGatewayRelationships(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	vpcARN := ec2ARN(testRegion, acct.ID, "vpc", "vpc-aaa")
	vpcID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2VPC, vpcARN, testRegion, "{}")

	cgwARN := ec2ARN(testRegion, acct.ID, "carrier-gateway", "cagw-bbb")
	cgwID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2CarrierGateway, cgwARN, testRegion,
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
	acct := newTestAccount(testAccountID)

	id := upsertTestResource(t, st, "aws", acct.ID, TypeEC2CarrierGateway,
		ec2ARN(testRegion, acct.ID, "carrier-gateway", "bare"), testRegion, "{}")

	if err := resolveCarrierGatewayRelationships(acct, st); err != nil {
		t.Fatalf("resolveCarrierGatewayRelationships: %v", err)
	}

	rels, _ := st.RelationshipsFrom(id)
	if len(rels) != 0 {
		t.Errorf("expected 0 relationships, got %d", len(rels))
	}
}
