package aws

import (
	"fmt"
	"testing"

	"codeberg.org/icearp/disco/internal/store"
)

func nfFirewallARN(region, acct, name string) string {
	return fmt.Sprintf("arn:aws:network-firewall:%s:%s:firewall/%s", region, acct, name)
}

func nfPolicyARN(region, acct, name string) string {
	return fmt.Sprintf("arn:aws:network-firewall:%s:%s:firewall-policy/%s", region, acct, name)
}

func nfRuleGroupARN(region, acct, name string) string {
	return fmt.Sprintf("arn:aws:network-firewall:%s:%s:stateful-rulegroup/%s", region, acct, name)
}

// TestResolveNetworkFirewallFirewallRelationships exercises the full edge set:
// firewall → policy (uses), firewall → vpc (attached-to), firewall → subnets
// (attached-to).
func TestResolveNetworkFirewallFirewallRelationships(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	policyARN := nfPolicyARN(testRegion, acct.ID, "p1")
	vpcARN := ec2ARN(testRegion, acct.ID, "vpc", "vpc-aaa")
	subnet1ARN := ec2ARN(testRegion, acct.ID, "subnet", "subnet-111")
	subnet2ARN := ec2ARN(testRegion, acct.ID, "subnet", "subnet-222")

	policyID := upsertTestResource(t, st, "aws", acct.ID, TypeNetworkFirewallFirewallPolicy, policyARN, testRegion, `{}`)
	vpcID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2VPC, vpcARN, testRegion, `{}`)
	sn1ID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2Subnet, subnet1ARN, testRegion, `{}`)
	sn2ID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2Subnet, subnet2ARN, testRegion, `{}`)

	fwARN := nfFirewallARN(testRegion, acct.ID, "fw1")
	attrs := fmt.Sprintf(`{
		"Firewall": {
			"FirewallPolicyArn": %q,
			"VpcId": "vpc-aaa",
			"SubnetMappings": [
				{"SubnetId": "subnet-111"},
				{"SubnetId": "subnet-222"}
			]
		}
	}`, policyARN)
	fwID := upsertTestResource(t, st, "aws", acct.ID, TypeNetworkFirewallFirewall, fwARN, testRegion, attrs)

	if err := resolveNetworkFirewallFirewallRelationships(acct, st); err != nil {
		t.Fatalf("resolveNetworkFirewallFirewallRelationships: %v", err)
	}
	rels, err := st.RelationshipsFrom(fwID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	assertRelationship(t, rels, fwID, policyID, store.RelUses)
	assertRelationship(t, rels, fwID, vpcID, store.RelAttachedTo)
	assertRelationship(t, rels, fwID, sn1ID, store.RelAttachedTo)
	assertRelationship(t, rels, fwID, sn2ID, store.RelAttachedTo)
}

// TestResolveNetworkFirewallPolicyRelationships verifies policy → rule-group
// edges for both stateless + stateful references.
func TestResolveNetworkFirewallPolicyRelationships(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	statelessARN := nfRuleGroupARN(testRegion, acct.ID, "stateless-1")
	statefulARN := nfRuleGroupARN(testRegion, acct.ID, "stateful-1")
	statelessID := upsertTestResource(t, st, "aws", acct.ID, TypeNetworkFirewallRuleGroup, statelessARN, testRegion, `{}`)
	statefulID := upsertTestResource(t, st, "aws", acct.ID, TypeNetworkFirewallRuleGroup, statefulARN, testRegion, `{}`)

	policyARN := nfPolicyARN(testRegion, acct.ID, "p1")
	attrs := fmt.Sprintf(`{
		"FirewallPolicy": {
			"StatelessRuleGroupReferences": [{"ResourceArn": %q}],
			"StatefulRuleGroupReferences":  [{"ResourceArn": %q}]
		}
	}`, statelessARN, statefulARN)
	policyID := upsertTestResource(t, st, "aws", acct.ID, TypeNetworkFirewallFirewallPolicy, policyARN, testRegion, attrs)

	if err := resolveNetworkFirewallPolicyRelationships(acct, st); err != nil {
		t.Fatalf("resolveNetworkFirewallPolicyRelationships: %v", err)
	}
	rels, err := st.RelationshipsFrom(policyID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	assertRelationship(t, rels, policyID, statelessID, store.RelUses)
	assertRelationship(t, rels, policyID, statefulID, store.RelUses)
}

// TestResolveNetworkFirewallFirewall_UnscannedTargets verifies FK-safe skip
// when the referenced policy, VPC, and subnet are not in the store.
func TestResolveNetworkFirewallFirewall_UnscannedTargets(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	fwARN := nfFirewallARN(testRegion, acct.ID, "orphan")
	attrs := `{
		"Firewall": {
			"FirewallPolicyArn": "arn:aws:network-firewall:us-east-1:123456789012:firewall-policy/missing",
			"VpcId": "vpc-missing",
			"SubnetMappings": [{"SubnetId": "subnet-missing"}]
		}
	}`
	fwID := upsertTestResource(t, st, "aws", acct.ID, TypeNetworkFirewallFirewall, fwARN, testRegion, attrs)

	if err := resolveNetworkFirewallFirewallRelationships(acct, st); err != nil {
		t.Fatalf("resolveNetworkFirewallFirewallRelationships: %v", err)
	}
	rels, err := st.RelationshipsFrom(fwID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("unexpected edges for unscanned targets: %+v", rels)
	}
}

// TestResolveNetworkFirewall_EmptyAttrs verifies no panic and no edges when
// attributes are empty for both firewall and policy resolvers.
func TestResolveNetworkFirewall_EmptyAttrs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	fwARN := nfFirewallARN(testRegion, acct.ID, "empty")
	upsertTestResource(t, st, "aws", acct.ID, TypeNetworkFirewallFirewall, fwARN, testRegion, `{}`)
	policyARN := nfPolicyARN(testRegion, acct.ID, "empty")
	upsertTestResource(t, st, "aws", acct.ID, TypeNetworkFirewallFirewallPolicy, policyARN, testRegion, `{}`)

	if err := resolveNetworkFirewallFirewallRelationships(acct, st); err != nil {
		t.Fatalf("resolveNetworkFirewallFirewallRelationships empty: %v", err)
	}
	if err := resolveNetworkFirewallPolicyRelationships(acct, st); err != nil {
		t.Fatalf("resolveNetworkFirewallPolicyRelationships empty: %v", err)
	}
}

func TestResolveNetworkFirewallRuleGroupKMS(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	rgARN := "arn:aws:network-firewall:us-east-1:" + testAccountID + ":stateful-rulegroup/rg1"
	keyARN := "arn:aws:kms:us-east-1:" + testAccountID + ":key/k-rg"
	attrs := `{"RuleGroupResponse":{"EncryptionConfiguration":{"KeyId":"` + keyARN + `"}}}`
	rgID := upsertTestResource(t, st, "aws", acct.ID, TypeNetworkFirewallRuleGroup, rgARN, testRegion, attrs)
	kID := upsertTestResource(t, st, "aws", acct.ID, TypeKMSKey, keyARN, testRegion, "{}")

	if err := resolveNetworkFirewallRuleGroupKMS(acct, st); err != nil {
		t.Fatalf("resolveNetworkFirewallRuleGroupKMS: %v", err)
	}
	rels, _ := st.RelationshipsFrom(rgID)
	assertRelationship(t, rels, rgID, kID, store.RelUses)
}

func TestResolveNetworkFirewallTLSInspectionRefs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	tlsARN := "arn:aws:network-firewall:us-east-1:" + testAccountID + ":tls-configuration/t1"
	keyARN := "arn:aws:kms:us-east-1:" + testAccountID + ":key/k-tls"
	caARN := "arn:aws:acm:us-east-1:" + testAccountID + ":certificate/abcd-ca"
	cARN := "arn:aws:acm:us-east-1:" + testAccountID + ":certificate/abcd-c1"
	attrs := `{"TLSInspectionConfigurationResponse":{"EncryptionConfiguration":{"KeyId":"` + keyARN +
		`"},"CertificateAuthority":{"CertificateArn":"` + caARN +
		`"},"Certificates":[{"CertificateArn":"` + cARN + `"}]}}`
	tID := upsertTestResource(t, st, "aws", acct.ID, TypeNetworkFirewallTLSInspectionConfiguration, tlsARN, testRegion, attrs)
	kID := upsertTestResource(t, st, "aws", acct.ID, TypeKMSKey, keyARN, testRegion, "{}")
	caID := upsertTestResource(t, st, "aws", acct.ID, TypeACMCertificate, caARN, testRegion, "{}")
	cID := upsertTestResource(t, st, "aws", acct.ID, TypeACMCertificate, cARN, testRegion, "{}")

	if err := resolveNetworkFirewallTLSInspectionRefs(acct, st); err != nil {
		t.Fatalf("resolveNetworkFirewallTLSInspectionRefs: %v", err)
	}
	rels, _ := st.RelationshipsFrom(tID)
	assertRelationship(t, rels, tID, kID, store.RelUses)
	assertRelationship(t, rels, tID, caID, store.RelUses)
	assertRelationship(t, rels, tID, cID, store.RelUses)
}
