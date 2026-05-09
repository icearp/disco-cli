package aws

import (
	"fmt"
	"testing"

	"codeberg.org/icearp/disco/store"
)

// vlARN builds a VPC Lattice resource ARN for a top-level resource (service,
// servicenetwork, targetgroup, resourcegateway, resourceconfiguration).
func vlARN(region, acctID, kind, id string) string {
	return fmt.Sprintf("arn:aws:vpc-lattice:%s:%s:%s/%s", region, acctID, kind, id)
}

// vlListenerARN builds an `arn:...:service/{svc}/listener/{lst}` ARN.
func vlListenerARN(region, acctID, svcID, lstID string) string {
	return fmt.Sprintf("arn:aws:vpc-lattice:%s:%s:service/%s/listener/%s", region, acctID, svcID, lstID)
}

// vlRuleARN builds an `arn:...:service/{svc}/listener/{lst}/rule/{rule}` ARN.
func vlRuleARN(region, acctID, svcID, lstID, ruleID string) string {
	return fmt.Sprintf("arn:aws:vpc-lattice:%s:%s:service/%s/listener/%s/rule/%s",
		region, acctID, svcID, lstID, ruleID)
}

func TestResolveVPCLatticeSNVA(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	netARN := vlARN(testRegion, acct.ID, "servicenetwork", "sn-001")
	netID := upsertTestResource(t, st, "aws", acct.ID, TypeVpcLatticeServiceNetwork, netARN, testRegion, "{}")
	vpcID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2VPC,
		ec2ARN(testRegion, acct.ID, "vpc", "vpc-001"), testRegion, "{}")
	assocARN := vlARN(testRegion, acct.ID, "servicenetworkvpcassociation", "snva-001")
	attrs := fmt.Sprintf(`{"ServiceNetworkArn":%q,"VpcId":"vpc-001"}`, netARN)
	assocID := upsertTestResource(t, st, "aws", acct.ID, TypeVpcLatticeServiceNetworkVpcAssociation, assocARN, testRegion, attrs)

	if err := resolveVPCLatticeSNVA(acct, st); err != nil {
		t.Fatalf("resolveVPCLatticeSNVA: %v", err)
	}
	rels, err := st.RelationshipsFrom(assocID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 2 {
		t.Fatalf("expected 2 relationships, got %d", len(rels))
	}
	assertRelationship(t, rels, assocID, netID, store.RelAttachedTo)
	assertRelationship(t, rels, assocID, vpcID, store.RelAttachedTo)
}

func TestResolveVPCLatticeSNVA_EmptyAttrs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	assocID := upsertTestResource(t, st, "aws", acct.ID, TypeVpcLatticeServiceNetworkVpcAssociation,
		vlARN(testRegion, acct.ID, "servicenetworkvpcassociation", "bare"), testRegion, "{}")
	if err := resolveVPCLatticeSNVA(acct, st); err != nil {
		t.Fatalf("resolveVPCLatticeSNVA: %v", err)
	}
	rels, _ := st.RelationshipsFrom(assocID)
	if len(rels) != 0 {
		t.Errorf("expected 0 relationships, got %d", len(rels))
	}
}

func TestResolveVPCLatticeSNVA_UnscannedTargetSkipped(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	missingNetARN := vlARN(testRegion, acct.ID, "servicenetwork", "sn-missing")
	assocID := upsertTestResource(t, st, "aws", acct.ID, TypeVpcLatticeServiceNetworkVpcAssociation,
		vlARN(testRegion, acct.ID, "servicenetworkvpcassociation", "snva-x"), testRegion,
		fmt.Sprintf(`{"ServiceNetworkArn":%q,"VpcId":"vpc-missing"}`, missingNetARN))
	if err := resolveVPCLatticeSNVA(acct, st); err != nil {
		t.Fatalf("resolveVPCLatticeSNVA: %v", err)
	}
	rels, _ := st.RelationshipsFrom(assocID)
	if len(rels) != 0 {
		t.Errorf("expected 0 relationships (all targets unscanned), got %d", len(rels))
	}
}

func TestResolveVPCLatticeSNSA(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	netARN := vlARN(testRegion, acct.ID, "servicenetwork", "sn-001")
	netID := upsertTestResource(t, st, "aws", acct.ID, TypeVpcLatticeServiceNetwork, netARN, testRegion, "{}")
	svcARN := vlARN(testRegion, acct.ID, "service", "svc-001")
	svcID := upsertTestResource(t, st, "aws", acct.ID, TypeVpcLatticeService, svcARN, testRegion, "{}")
	assocARN := vlARN(testRegion, acct.ID, "servicenetworkserviceassociation", "snsa-001")
	attrs := fmt.Sprintf(`{"ServiceNetworkArn":%q,"ServiceArn":%q}`, netARN, svcARN)
	assocID := upsertTestResource(t, st, "aws", acct.ID, TypeVpcLatticeServiceNetworkServiceAssociation, assocARN, testRegion, attrs)

	if err := resolveVPCLatticeSNSA(acct, st); err != nil {
		t.Fatalf("resolveVPCLatticeSNSA: %v", err)
	}
	rels, _ := st.RelationshipsFrom(assocID)
	if len(rels) != 2 {
		t.Fatalf("expected 2, got %d", len(rels))
	}
	assertRelationship(t, rels, assocID, netID, store.RelAttachedTo)
	assertRelationship(t, rels, assocID, svcID, store.RelAttachedTo)
}

func TestResolveVPCLatticeSNSA_EmptyAttrs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	assocID := upsertTestResource(t, st, "aws", acct.ID, TypeVpcLatticeServiceNetworkServiceAssociation,
		vlARN(testRegion, acct.ID, "servicenetworkserviceassociation", "bare"), testRegion, "{}")
	if err := resolveVPCLatticeSNSA(acct, st); err != nil {
		t.Fatalf("resolveVPCLatticeSNSA: %v", err)
	}
	rels, _ := st.RelationshipsFrom(assocID)
	if len(rels) != 0 {
		t.Errorf("expected 0, got %d", len(rels))
	}
}

func TestResolveVPCLatticeSNSA_UnscannedTargetSkipped(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	missingNet := vlARN(testRegion, acct.ID, "servicenetwork", "missing")
	missingSvc := vlARN(testRegion, acct.ID, "service", "missing")
	assocID := upsertTestResource(t, st, "aws", acct.ID, TypeVpcLatticeServiceNetworkServiceAssociation,
		vlARN(testRegion, acct.ID, "servicenetworkserviceassociation", "x"), testRegion,
		fmt.Sprintf(`{"ServiceNetworkArn":%q,"ServiceArn":%q}`, missingNet, missingSvc))
	if err := resolveVPCLatticeSNSA(acct, st); err != nil {
		t.Fatalf("resolveVPCLatticeSNSA: %v", err)
	}
	rels, _ := st.RelationshipsFrom(assocID)
	if len(rels) != 0 {
		t.Errorf("expected 0, got %d", len(rels))
	}
}

func TestResolveVPCLatticeSNRA(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	netARN := vlARN(testRegion, acct.ID, "servicenetwork", "sn-001")
	netID := upsertTestResource(t, st, "aws", acct.ID, TypeVpcLatticeServiceNetwork, netARN, testRegion, "{}")
	rcARN := vlARN(testRegion, acct.ID, "resourceconfiguration", "rc-001")
	rcID := upsertTestResource(t, st, "aws", acct.ID, TypeVpcLatticeResourceConfiguration, rcARN, testRegion, "{}")
	assocARN := vlARN(testRegion, acct.ID, "servicenetworkresourceassociation", "snra-001")
	attrs := fmt.Sprintf(`{"ServiceNetworkArn":%q,"ResourceConfigurationArn":%q}`, netARN, rcARN)
	assocID := upsertTestResource(t, st, "aws", acct.ID, TypeVpcLatticeServiceNetworkResourceAssociation, assocARN, testRegion, attrs)

	if err := resolveVPCLatticeSNRA(acct, st); err != nil {
		t.Fatalf("resolveVPCLatticeSNRA: %v", err)
	}
	rels, _ := st.RelationshipsFrom(assocID)
	if len(rels) != 2 {
		t.Fatalf("expected 2, got %d", len(rels))
	}
	assertRelationship(t, rels, assocID, netID, store.RelAttachedTo)
	assertRelationship(t, rels, assocID, rcID, store.RelAttachedTo)
}

func TestResolveVPCLatticeSNRA_EmptyAttrs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	assocID := upsertTestResource(t, st, "aws", acct.ID, TypeVpcLatticeServiceNetworkResourceAssociation,
		vlARN(testRegion, acct.ID, "servicenetworkresourceassociation", "bare"), testRegion, "{}")
	if err := resolveVPCLatticeSNRA(acct, st); err != nil {
		t.Fatalf("resolveVPCLatticeSNRA: %v", err)
	}
	rels, _ := st.RelationshipsFrom(assocID)
	if len(rels) != 0 {
		t.Errorf("expected 0, got %d", len(rels))
	}
}

func TestResolveVPCLatticeSNRA_UnscannedTargetSkipped(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	missingNet := vlARN(testRegion, acct.ID, "servicenetwork", "missing")
	missingRC := vlARN(testRegion, acct.ID, "resourceconfiguration", "missing")
	assocID := upsertTestResource(t, st, "aws", acct.ID, TypeVpcLatticeServiceNetworkResourceAssociation,
		vlARN(testRegion, acct.ID, "servicenetworkresourceassociation", "x"), testRegion,
		fmt.Sprintf(`{"ServiceNetworkArn":%q,"ResourceConfigurationArn":%q}`, missingNet, missingRC))
	if err := resolveVPCLatticeSNRA(acct, st); err != nil {
		t.Fatalf("resolveVPCLatticeSNRA: %v", err)
	}
	rels, _ := st.RelationshipsFrom(assocID)
	if len(rels) != 0 {
		t.Errorf("expected 0, got %d", len(rels))
	}
}

func TestResolveVPCLatticeTargetGroup(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	vpcID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2VPC,
		ec2ARN(testRegion, acct.ID, "vpc", "vpc-001"), testRegion, "{}")
	svcARN := vlARN(testRegion, acct.ID, "service", "svc-001")
	svcID := upsertTestResource(t, st, "aws", acct.ID, TypeVpcLatticeService, svcARN, testRegion, "{}")
	tgARN := vlARN(testRegion, acct.ID, "targetgroup", "tg-001")
	attrs := fmt.Sprintf(`{"VpcIdentifier":"vpc-001","ServiceArns":[%q]}`, svcARN)
	tgID := upsertTestResource(t, st, "aws", acct.ID, TypeVpcLatticeTargetGroup, tgARN, testRegion, attrs)

	if err := resolveVPCLatticeTargetGroup(acct, st); err != nil {
		t.Fatalf("resolveVPCLatticeTargetGroup: %v", err)
	}
	rels, _ := st.RelationshipsFrom(tgID)
	if len(rels) != 2 {
		t.Fatalf("expected 2, got %d", len(rels))
	}
	assertRelationship(t, rels, tgID, vpcID, store.RelAttachedTo)
	assertRelationship(t, rels, tgID, svcID, store.RelAttachedTo)
}

func TestResolveVPCLatticeTargetGroup_EmptyAttrs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	tgID := upsertTestResource(t, st, "aws", acct.ID, TypeVpcLatticeTargetGroup,
		vlARN(testRegion, acct.ID, "targetgroup", "bare"), testRegion, "{}")
	if err := resolveVPCLatticeTargetGroup(acct, st); err != nil {
		t.Fatalf("resolveVPCLatticeTargetGroup: %v", err)
	}
	rels, _ := st.RelationshipsFrom(tgID)
	if len(rels) != 0 {
		t.Errorf("expected 0, got %d", len(rels))
	}
}

func TestResolveVPCLatticeTargetGroup_UnscannedTargetSkipped(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	missingSvc := vlARN(testRegion, acct.ID, "service", "missing")
	tgID := upsertTestResource(t, st, "aws", acct.ID, TypeVpcLatticeTargetGroup,
		vlARN(testRegion, acct.ID, "targetgroup", "x"), testRegion,
		fmt.Sprintf(`{"VpcIdentifier":"vpc-missing","ServiceArns":[%q]}`, missingSvc))
	if err := resolveVPCLatticeTargetGroup(acct, st); err != nil {
		t.Fatalf("resolveVPCLatticeTargetGroup: %v", err)
	}
	rels, _ := st.RelationshipsFrom(tgID)
	if len(rels) != 0 {
		t.Errorf("expected 0, got %d", len(rels))
	}
}

func TestResolveVPCLatticeListenerService(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	svcARN := vlARN(testRegion, acct.ID, "service", "svc-001")
	svcID := upsertTestResource(t, st, "aws", acct.ID, TypeVpcLatticeService, svcARN, testRegion, "{}")
	lstARN := vlListenerARN(testRegion, acct.ID, "svc-001", "lst-001")
	lstID := upsertTestResource(t, st, "aws", acct.ID, TypeVpcLatticeListener, lstARN, testRegion, "{}")

	if err := resolveVPCLatticeListenerService(acct, st); err != nil {
		t.Fatalf("resolveVPCLatticeListenerService: %v", err)
	}
	rels, _ := st.RelationshipsFrom(lstID)
	if len(rels) != 1 {
		t.Fatalf("expected 1, got %d", len(rels))
	}
	assertRelationship(t, rels, lstID, svcID, store.RelAttachedTo)
}

func TestResolveVPCLatticeListenerService_EmptyAttrs(t *testing.T) {
	// Listener with malformed ARN (no `/listener/` segment) → no edge.
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	lstID := upsertTestResource(t, st, "aws", acct.ID, TypeVpcLatticeListener,
		"arn:aws:vpc-lattice:us-east-1:123456789012:malformed", testRegion, "{}")
	if err := resolveVPCLatticeListenerService(acct, st); err != nil {
		t.Fatalf("resolveVPCLatticeListenerService: %v", err)
	}
	rels, _ := st.RelationshipsFrom(lstID)
	if len(rels) != 0 {
		t.Errorf("expected 0, got %d", len(rels))
	}
}

func TestResolveVPCLatticeListenerService_UnscannedTargetSkipped(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	lstID := upsertTestResource(t, st, "aws", acct.ID, TypeVpcLatticeListener,
		vlListenerARN(testRegion, acct.ID, "svc-missing", "lst-x"), testRegion, "{}")
	if err := resolveVPCLatticeListenerService(acct, st); err != nil {
		t.Fatalf("resolveVPCLatticeListenerService: %v", err)
	}
	rels, _ := st.RelationshipsFrom(lstID)
	if len(rels) != 0 {
		t.Errorf("expected 0, got %d", len(rels))
	}
}

func TestResolveVPCLatticeRuleListener(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	lstARN := vlListenerARN(testRegion, acct.ID, "svc-001", "lst-001")
	lstID := upsertTestResource(t, st, "aws", acct.ID, TypeVpcLatticeListener, lstARN, testRegion, "{}")
	ruleARN := vlRuleARN(testRegion, acct.ID, "svc-001", "lst-001", "rule-001")
	ruleID := upsertTestResource(t, st, "aws", acct.ID, TypeVpcLatticeRule, ruleARN, testRegion, "{}")

	if err := resolveVPCLatticeRuleListener(acct, st); err != nil {
		t.Fatalf("resolveVPCLatticeRuleListener: %v", err)
	}
	rels, _ := st.RelationshipsFrom(ruleID)
	if len(rels) != 1 {
		t.Fatalf("expected 1, got %d", len(rels))
	}
	assertRelationship(t, rels, ruleID, lstID, store.RelAttachedTo)
}

func TestResolveVPCLatticeRuleListener_EmptyAttrs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	ruleID := upsertTestResource(t, st, "aws", acct.ID, TypeVpcLatticeRule,
		"arn:aws:vpc-lattice:us-east-1:123456789012:malformed", testRegion, "{}")
	if err := resolveVPCLatticeRuleListener(acct, st); err != nil {
		t.Fatalf("resolveVPCLatticeRuleListener: %v", err)
	}
	rels, _ := st.RelationshipsFrom(ruleID)
	if len(rels) != 0 {
		t.Errorf("expected 0, got %d", len(rels))
	}
}

func TestResolveVPCLatticeRuleListener_UnscannedTargetSkipped(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	ruleID := upsertTestResource(t, st, "aws", acct.ID, TypeVpcLatticeRule,
		vlRuleARN(testRegion, acct.ID, "svc-x", "lst-missing", "rule-x"), testRegion, "{}")
	if err := resolveVPCLatticeRuleListener(acct, st); err != nil {
		t.Fatalf("resolveVPCLatticeRuleListener: %v", err)
	}
	rels, _ := st.RelationshipsFrom(ruleID)
	if len(rels) != 0 {
		t.Errorf("expected 0, got %d", len(rels))
	}
}

func TestResolveVPCLatticeAuthPolicyParent(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	svcARN := vlARN(testRegion, acct.ID, "service", "svc-001")
	svcID := upsertTestResource(t, st, "aws", acct.ID, TypeVpcLatticeService, svcARN, testRegion, "{}")
	netARN := vlARN(testRegion, acct.ID, "servicenetwork", "sn-001")
	netID := upsertTestResource(t, st, "aws", acct.ID, TypeVpcLatticeServiceNetwork, netARN, testRegion, "{}")

	authSvcARN := svcARN + "/auth-policy"
	authSvcID := upsertTestResource(t, st, "aws", acct.ID, TypeVpcLatticeAuthPolicy, authSvcARN, testRegion, "{}")
	authNetARN := netARN + "/auth-policy"
	authNetID := upsertTestResource(t, st, "aws", acct.ID, TypeVpcLatticeAuthPolicy, authNetARN, testRegion, "{}")

	if err := resolveVPCLatticeAuthPolicyParent(acct, st); err != nil {
		t.Fatalf("resolveVPCLatticeAuthPolicyParent: %v", err)
	}
	relsSvc, _ := st.RelationshipsFrom(authSvcID)
	if len(relsSvc) != 1 {
		t.Fatalf("auth-policy on service: expected 1, got %d", len(relsSvc))
	}
	assertRelationship(t, relsSvc, authSvcID, svcID, store.RelAttachedTo)
	relsNet, _ := st.RelationshipsFrom(authNetID)
	if len(relsNet) != 1 {
		t.Fatalf("auth-policy on service-network: expected 1, got %d", len(relsNet))
	}
	assertRelationship(t, relsNet, authNetID, netID, store.RelAttachedTo)
}

func TestResolveVPCLatticeAuthPolicyParent_EmptyAttrs(t *testing.T) {
	// Empty attrs are irrelevant — resolver reads NativeID. Use an ARN
	// without a recognizable `/service/` or `/servicenetwork/` segment to
	// hit the default-skip branch.
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	authID := upsertTestResource(t, st, "aws", acct.ID, TypeVpcLatticeAuthPolicy,
		"arn:aws:vpc-lattice:us-east-1:123456789012:weird/x/auth-policy", testRegion, "{}")
	if err := resolveVPCLatticeAuthPolicyParent(acct, st); err != nil {
		t.Fatalf("resolveVPCLatticeAuthPolicyParent: %v", err)
	}
	rels, _ := st.RelationshipsFrom(authID)
	if len(rels) != 0 {
		t.Errorf("expected 0, got %d", len(rels))
	}
}

func TestResolveVPCLatticeAuthPolicyParent_UnscannedTargetSkipped(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	missingSvc := vlARN(testRegion, acct.ID, "service", "missing")
	authID := upsertTestResource(t, st, "aws", acct.ID, TypeVpcLatticeAuthPolicy,
		missingSvc+"/auth-policy", testRegion, "{}")
	if err := resolveVPCLatticeAuthPolicyParent(acct, st); err != nil {
		t.Fatalf("resolveVPCLatticeAuthPolicyParent: %v", err)
	}
	rels, _ := st.RelationshipsFrom(authID)
	if len(rels) != 0 {
		t.Errorf("expected 0, got %d", len(rels))
	}
}

func TestResolveVPCLatticeResourcePolicyParent(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	netARN := vlARN(testRegion, acct.ID, "servicenetwork", "sn-001")
	netID := upsertTestResource(t, st, "aws", acct.ID, TypeVpcLatticeServiceNetwork, netARN, testRegion, "{}")
	rpARN := netARN + "/resource-policy"
	rpID := upsertTestResource(t, st, "aws", acct.ID, TypeVpcLatticeResourcePolicy, rpARN, testRegion, "{}")

	if err := resolveVPCLatticeResourcePolicyParent(acct, st); err != nil {
		t.Fatalf("resolveVPCLatticeResourcePolicyParent: %v", err)
	}
	rels, _ := st.RelationshipsFrom(rpID)
	if len(rels) != 1 {
		t.Fatalf("expected 1, got %d", len(rels))
	}
	assertRelationship(t, rels, rpID, netID, store.RelAttachedTo)
}

func TestResolveVPCLatticeResourcePolicyParent_EmptyAttrs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	rpID := upsertTestResource(t, st, "aws", acct.ID, TypeVpcLatticeResourcePolicy,
		"arn:aws:vpc-lattice:us-east-1:123456789012:weird/x/resource-policy", testRegion, "{}")
	if err := resolveVPCLatticeResourcePolicyParent(acct, st); err != nil {
		t.Fatalf("resolveVPCLatticeResourcePolicyParent: %v", err)
	}
	rels, _ := st.RelationshipsFrom(rpID)
	if len(rels) != 0 {
		t.Errorf("expected 0, got %d", len(rels))
	}
}

func TestResolveVPCLatticeResourcePolicyParent_UnscannedTargetSkipped(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	missingSvc := vlARN(testRegion, acct.ID, "service", "missing")
	rpID := upsertTestResource(t, st, "aws", acct.ID, TypeVpcLatticeResourcePolicy,
		missingSvc+"/resource-policy", testRegion, "{}")
	if err := resolveVPCLatticeResourcePolicyParent(acct, st); err != nil {
		t.Fatalf("resolveVPCLatticeResourcePolicyParent: %v", err)
	}
	rels, _ := st.RelationshipsFrom(rpID)
	if len(rels) != 0 {
		t.Errorf("expected 0, got %d", len(rels))
	}
}

func TestResolveVPCLatticeResourceGateway(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	vpcID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2VPC,
		ec2ARN(testRegion, acct.ID, "vpc", "vpc-001"), testRegion, "{}")
	subnetID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2Subnet,
		ec2ARN(testRegion, acct.ID, "subnet", "subnet-001"), testRegion, "{}")
	sgID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2SecurityGroup,
		ec2ARN(testRegion, acct.ID, "security-group", "sg-001"), testRegion, "{}")
	gwARN := vlARN(testRegion, acct.ID, "resourcegateway", "rgw-001")
	attrs := `{"VpcIdentifier":"vpc-001","SubnetIds":["subnet-001"],"SecurityGroupIds":["sg-001"]}`
	gwID := upsertTestResource(t, st, "aws", acct.ID, TypeVpcLatticeResourceGateway, gwARN, testRegion, attrs)

	if err := resolveVPCLatticeResourceGateway(acct, st); err != nil {
		t.Fatalf("resolveVPCLatticeResourceGateway: %v", err)
	}
	rels, _ := st.RelationshipsFrom(gwID)
	if len(rels) != 3 {
		t.Fatalf("expected 3, got %d", len(rels))
	}
	assertRelationship(t, rels, gwID, vpcID, store.RelAttachedTo)
	assertRelationship(t, rels, gwID, subnetID, store.RelAttachedTo)
	assertRelationship(t, rels, gwID, sgID, store.RelAttachedTo)
}

func TestResolveVPCLatticeResourceGateway_EmptyAttrs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	gwID := upsertTestResource(t, st, "aws", acct.ID, TypeVpcLatticeResourceGateway,
		vlARN(testRegion, acct.ID, "resourcegateway", "bare"), testRegion, "{}")
	if err := resolveVPCLatticeResourceGateway(acct, st); err != nil {
		t.Fatalf("resolveVPCLatticeResourceGateway: %v", err)
	}
	rels, _ := st.RelationshipsFrom(gwID)
	if len(rels) != 0 {
		t.Errorf("expected 0, got %d", len(rels))
	}
}

func TestResolveVPCLatticeResourceGateway_UnscannedTargetSkipped(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	gwID := upsertTestResource(t, st, "aws", acct.ID, TypeVpcLatticeResourceGateway,
		vlARN(testRegion, acct.ID, "resourcegateway", "x"), testRegion,
		`{"VpcIdentifier":"vpc-missing","SubnetIds":["subnet-missing"],"SecurityGroupIds":["sg-missing"]}`)
	if err := resolveVPCLatticeResourceGateway(acct, st); err != nil {
		t.Fatalf("resolveVPCLatticeResourceGateway: %v", err)
	}
	rels, _ := st.RelationshipsFrom(gwID)
	if len(rels) != 0 {
		t.Errorf("expected 0, got %d", len(rels))
	}
}

func TestResolveVPCLatticeResourceConfigurationGateway(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	gwARN := vlARN(testRegion, acct.ID, "resourcegateway", "rgw-001")
	gwID := upsertTestResource(t, st, "aws", acct.ID, TypeVpcLatticeResourceGateway, gwARN, testRegion, "{}")
	rcARN := vlARN(testRegion, acct.ID, "resourceconfiguration", "rc-001")
	rcID := upsertTestResource(t, st, "aws", acct.ID, TypeVpcLatticeResourceConfiguration, rcARN, testRegion,
		`{"ResourceGatewayId":"rgw-001"}`)

	if err := resolveVPCLatticeResourceConfigurationGateway(acct, st); err != nil {
		t.Fatalf("resolveVPCLatticeResourceConfigurationGateway: %v", err)
	}
	rels, _ := st.RelationshipsFrom(rcID)
	if len(rels) != 1 {
		t.Fatalf("expected 1, got %d", len(rels))
	}
	assertRelationship(t, rels, rcID, gwID, store.RelAttachedTo)
}

func TestResolveVPCLatticeResourceConfigurationGateway_EmptyAttrs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	rcID := upsertTestResource(t, st, "aws", acct.ID, TypeVpcLatticeResourceConfiguration,
		vlARN(testRegion, acct.ID, "resourceconfiguration", "bare"), testRegion, "{}")
	if err := resolveVPCLatticeResourceConfigurationGateway(acct, st); err != nil {
		t.Fatalf("resolveVPCLatticeResourceConfigurationGateway: %v", err)
	}
	rels, _ := st.RelationshipsFrom(rcID)
	if len(rels) != 0 {
		t.Errorf("expected 0, got %d", len(rels))
	}
}

func TestResolveVPCLatticeResourceConfigurationGateway_UnscannedTargetSkipped(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	rcID := upsertTestResource(t, st, "aws", acct.ID, TypeVpcLatticeResourceConfiguration,
		vlARN(testRegion, acct.ID, "resourceconfiguration", "x"), testRegion,
		`{"ResourceGatewayId":"rgw-missing"}`)
	if err := resolveVPCLatticeResourceConfigurationGateway(acct, st); err != nil {
		t.Fatalf("resolveVPCLatticeResourceConfigurationGateway: %v", err)
	}
	rels, _ := st.RelationshipsFrom(rcID)
	if len(rels) != 0 {
		t.Errorf("expected 0, got %d", len(rels))
	}
}

func TestResolveVPCLatticeServiceCert(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	svcARN := "arn:aws:vpc-lattice:us-east-1:" + testAccountID + ":service/svc-1"
	caARN := "arn:aws:acm:us-east-1:" + testAccountID + ":certificate/abcd"
	attrs := `{"CertificateArn":"` + caARN + `"}`
	sID := upsertTestResource(t, st, "aws", acct.ID, TypeVpcLatticeService, svcARN, testRegion, attrs)
	cID := upsertTestResource(t, st, "aws", acct.ID, TypeACMCertificate, caARN, testRegion, "{}")

	if err := resolveVPCLatticeServiceCert(acct, st); err != nil {
		t.Fatalf("resolveVPCLatticeServiceCert: %v", err)
	}
	rels, _ := st.RelationshipsFrom(sID)
	assertRelationship(t, rels, sID, cID, store.RelUses)
}
