package aws

import (
	"testing"

	"codeburg.org/icearp/disco/internal/store"
)

// TestResolveInstanceRelationships verifies that an EC2 instance's JSON attributes
// are correctly parsed to produce VPC, subnet, security-group, and volume relationships.
// This test catches wrong JSON field names in instanceAttrs — bugs that are otherwise
// silent (zero relationships, no error).
func TestResolveInstanceRelationships(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount("123456789012")
	region := "us-east-1"

	// Insert the instance with a full attribute blob and region set.
	instanceARN := ec2ARN(region, acct.ID, "instance", "i-abc123")
	attrsJSON := `{
		"InstanceId": "i-abc123",
		"VpcId":      "vpc-111",
		"SubnetId":   "subnet-222",
		"SecurityGroups": [{"GroupId": "sg-333"}],
		"BlockDeviceMappings": [{"Ebs": {"VolumeId": "vol-444"}}]
	}`
	instID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2Instance, instanceARN, region, attrsJSON)

	// Insert the referenced resources — their native IDs must match what the resolver computes
	// using ec2ARN(region, ...). Region must be set on the instance for the resolver to build
	// the correct ARN, so we insert these with the same region.
	vpcID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2VPC,
		ec2ARN(region, acct.ID, "vpc", "vpc-111"), region, "{}")
	subnetID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2Subnet,
		ec2ARN(region, acct.ID, "subnet", "subnet-222"), region, "{}")
	sgID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2SecurityGroup,
		ec2ARN(region, acct.ID, "security-group", "sg-333"), region, "{}")
	volID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2Volume,
		ec2ARN(region, acct.ID, "volume", "vol-444"), region, "{}")

	if err := resolveInstanceRelationships(acct, st); err != nil {
		t.Fatalf("resolveInstanceRelationships: %v", err)
	}

	rels, err := st.RelationshipsFrom(instID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 4 {
		t.Errorf("expected 4 relationships, got %d", len(rels))
	}

	assertRelationship(t, rels, instID, vpcID, store.RelAttachedTo)
	assertRelationship(t, rels, instID, subnetID, store.RelAttachedTo)
	assertRelationship(t, rels, instID, sgID, store.RelUses)
	assertRelationship(t, rels, instID, volID, store.RelAttachedTo)
}

// TestResolveInstanceRelationships_EmptyAttrs verifies that an instance with
// no network attributes produces no relationships and no error.
func TestResolveInstanceRelationships_EmptyAttrs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount("123456789012")
	region := "us-east-1"

	instanceARN := ec2ARN(region, acct.ID, "instance", "i-bare")
	instID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2Instance, instanceARN, region, "{}")

	if err := resolveInstanceRelationships(acct, st); err != nil {
		t.Fatalf("resolveInstanceRelationships: %v", err)
	}

	rels, err := st.RelationshipsFrom(instID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("expected 0 relationships for instance with empty attrs, got %d", len(rels))
	}
}

// TestResolveSubnetVPCRelationships verifies that subnets produce an attached-to
// relationship pointing to their parent VPC.
func TestResolveSubnetVPCRelationships(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount("123456789012")
	region := "us-east-1"

	subnetARN := ec2ARN(region, acct.ID, "subnet", "subnet-abc")
	attrsJSON := `{"VpcId": "vpc-xyz"}`
	subnetID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2Subnet, subnetARN, region, attrsJSON)
	vpcID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2VPC,
		ec2ARN(region, acct.ID, "vpc", "vpc-xyz"), region, "{}")

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
	if rels[0].ToID != vpcID || rels[0].Kind != store.RelAttachedTo {
		t.Errorf("expected subnet -[attached-to]-> vpc, got %+v", rels[0])
	}
}

// TestResolveIGWRelationships verifies that an internet gateway produces an
// attached-to relationship for each VPC in its Attachments list.
func TestResolveIGWRelationships(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount("123456789012")
	region := "us-east-1"

	igwARN := ec2ARN(region, acct.ID, "internet-gateway", "igw-001")
	attrsJSON := `{"Attachments": [{"VpcId": "vpc-abc"}]}`
	igwID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2InternetGateway, igwARN, region, attrsJSON)
	vpcID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2VPC,
		ec2ARN(region, acct.ID, "vpc", "vpc-abc"), region, "{}")

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
	if rels[0].ToID != vpcID || rels[0].Kind != store.RelAttachedTo {
		t.Errorf("expected igw -[attached-to]-> vpc, got %+v", rels[0])
	}
}

// assertRelationship fails the test if no relationship with the given
// (from, to, kind) exists in the rels slice.
func assertRelationship(t *testing.T, rels []store.Relationship, fromID, toID, kind string) {
	t.Helper()
	for _, r := range rels {
		if r.FromID == fromID && r.ToID == toID && r.Kind == kind {
			return
		}
	}
	t.Errorf("missing relationship: %s -[%s]-> %s", fromID, kind, toID)
}

// — Route Table —

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

// — NAT Gateway —

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

// — EIP —

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

// — Network Interface —

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

// — Network ACL —

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

// — VPC Endpoint —

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

// — VPC Peering —

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

// — VPN Connection —

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

// — Transit Gateway Attachment —

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

// — Flow Log —

func TestResolveFlowLogRelationships(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	flARN := ec2ARN(testRegion, acct.ID, "vpc-flow-log", "fl-001")
	flID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2FlowLog, flARN, testRegion,
		`{"ResourceId":"vpc-001","ResourceType":"VPC"}`)
	vpcID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2VPC, ec2ARN(testRegion, acct.ID, "vpc", "vpc-001"), testRegion, "{}")

	if err := resolveFlowLogRelationships(acct, st); err != nil {
		t.Fatalf("resolveFlowLogRelationships: %v", err)
	}
	rels, err := st.RelationshipsFrom(flID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 1 {
		t.Fatalf("expected 1 relationship, got %d", len(rels))
	}
	assertRelationship(t, rels, flID, vpcID, store.RelAttachedTo)
}

func TestResolveFlowLogRelationships_EmptyAttrs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	flID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2FlowLog, ec2ARN(testRegion, acct.ID, "vpc-flow-log", "fl-bare"), testRegion, "{}")
	if err := resolveFlowLogRelationships(acct, st); err != nil {
		t.Fatalf("resolveFlowLogRelationships: %v", err)
	}
	rels, _ := st.RelationshipsFrom(flID)
	if len(rels) != 0 {
		t.Errorf("expected 0 relationships, got %d", len(rels))
	}
}

// — Instance Connect Endpoint —

func TestResolveInstanceConnectEndpointRelationships(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	iceARN := "arn:aws:ec2:" + testRegion + ":" + testAccountID + ":instance-connect-endpoint/eice-001"
	iceID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2InstanceConnectEndpoint, iceARN, testRegion,
		`{"SubnetId":"subnet-001","VpcId":"vpc-001"}`)
	subnetID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2Subnet, ec2ARN(testRegion, acct.ID, "subnet", "subnet-001"), testRegion, "{}")
	vpcID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2VPC, ec2ARN(testRegion, acct.ID, "vpc", "vpc-001"), testRegion, "{}")

	if err := resolveInstanceConnectEndpointRelationships(acct, st); err != nil {
		t.Fatalf("resolveInstanceConnectEndpointRelationships: %v", err)
	}
	rels, err := st.RelationshipsFrom(iceID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 2 {
		t.Fatalf("expected 2 relationships, got %d", len(rels))
	}
	assertRelationship(t, rels, iceID, subnetID, store.RelAttachedTo)
	assertRelationship(t, rels, iceID, vpcID, store.RelAttachedTo)
}

func TestResolveInstanceConnectEndpointRelationships_EmptyAttrs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	iceID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2InstanceConnectEndpoint,
		"arn:aws:ec2:"+testRegion+":"+testAccountID+":instance-connect-endpoint/eice-bare", testRegion, "{}")
	if err := resolveInstanceConnectEndpointRelationships(acct, st); err != nil {
		t.Fatalf("resolveInstanceConnectEndpointRelationships: %v", err)
	}
	rels, _ := st.RelationshipsFrom(iceID)
	if len(rels) != 0 {
		t.Errorf("expected 0 relationships, got %d", len(rels))
	}
}
