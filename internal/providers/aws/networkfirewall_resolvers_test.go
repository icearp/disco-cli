package aws

import (
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/networkfirewall"
	nfwtypes "github.com/aws/aws-sdk-go-v2/service/networkfirewall/types"
	"github.com/icearp/disco-cli/store"
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

// nfDecodeCase is a Network Firewall resolver whose rows of srcType link to
// target (of targetType, by rel) when their attributes are goodAttrs, and
// whose badAttrs give a field the wrong JSON type.
type nfDecodeCase struct {
	name, srcType, srcKind string
	resolve                func(*account, *store.Store) error
	target, targetType     string
	rel                    string
	goodAttrs, badAttrs    string
	noEdge                 bool // goodAttrs, as stored, carry no reference
}

// run pins that a row whose attributes do not decode is reported as a
// *json.UnmarshalTypeError and costs the other rows none of their edges, and
// that a row with no references gets none.
func (c nfDecodeCase) run(t *testing.T) {
	t.Helper()
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	tgtID := upsertTestResource(t, st, "aws", acct.ID, c.targetType, c.target, testRegion, "{}")
	// Named so ListResources (ordered by name) reads a good row on each side
	// of the bad one: an early return on the bad row loses the second.
	row := func(name, attrs string) string {
		return upsertTestResourceNamed(t, st, c.srcType, nfARN(c.srcKind, name), testRegion, attrs, name)
	}
	goodBefore := row("a-good", c.goodAttrs)
	bad := row("b-bad", c.badAttrs)
	goodAfter := row("c-good", c.goodAttrs)
	empty := row("d-empty", "{}")
	err := c.resolve(acct, st)
	var typeErr *json.UnmarshalTypeError
	if !errors.As(err, &typeErr) {
		t.Fatalf("err = %v, want a *json.UnmarshalTypeError", err)
	}
	quiet := []string{bad, empty}
	if c.noEdge {
		quiet = append(quiet, goodBefore, goodAfter)
	} else {
		for _, good := range []string{goodBefore, goodAfter} {
			assertRelationship(t, ibRelsFrom(t, st, good), good, tgtID, c.rel)
		}
	}
	for _, id := range quiet {
		if rels := ibRelsFrom(t, st, id); len(rels) != 0 {
			t.Errorf("row %s has edges %v, want none", id, rels)
		}
	}
}

func TestNetworkFirewallResolvers_DecodeErrorKeepsOtherRows(t *testing.T) {
	keyARN := "arn:aws:kms:" + testRegion + ":" + testAccountID + ":key/k1"
	rgARN := nfRuleGroupARN(testRegion, testAccountID, "rg")
	encryption := &nfwtypes.EncryptionConfiguration{KeyId: &keyARN, Type: nfwtypes.EncryptionTypeCustomerKms}
	for _, c := range []nfDecodeCase{
		{
			name: "firewall", srcType: TypeNetworkFirewallFirewall, srcKind: "firewall", resolve: resolveNetworkFirewallFirewallRelationships,
			target: ec2ARN(testRegion, testAccountID, "vpc", "vpc-1"), targetType: TypeEC2VPC, rel: store.RelAttachedTo,
			goodAttrs: mustJSON(networkfirewall.DescribeFirewallOutput{Firewall: &nfwtypes.Firewall{VpcId: sdkaws.String("vpc-1")}}),
			badAttrs:  `{"Firewall":7}`,
		},
		{
			name: "firewall policy", srcType: TypeNetworkFirewallFirewallPolicy, srcKind: "firewall-policy", resolve: resolveNetworkFirewallPolicyRelationships,
			target: rgARN, targetType: TypeNetworkFirewallRuleGroup, rel: store.RelUses,
			goodAttrs: mustJSON(networkfirewall.DescribeFirewallPolicyOutput{FirewallPolicy: &nfwtypes.FirewallPolicy{
				StatefulRuleGroupReferences: []nfwtypes.StatefulRuleGroupReference{{ResourceArn: &rgARN}},
			}}),
			badAttrs: `{"FirewallPolicy":7}`,
		},
		{
			name: "rule group", srcType: TypeNetworkFirewallRuleGroup, srcKind: "stateful-rulegroup", resolve: resolveNetworkFirewallRuleGroupKMS,
			target: keyARN, targetType: TypeKMSKey, rel: store.RelUses,
			goodAttrs: mustJSON(networkfirewall.DescribeRuleGroupOutput{RuleGroupResponse: &nfwtypes.RuleGroupResponse{EncryptionConfiguration: encryption}}),
			badAttrs:  `{"RuleGroupResponse":7}`,
		},
		{
			name: "TLS inspection configuration", srcType: TypeNetworkFirewallTLSInspectionConfiguration, srcKind: "tls-configuration",
			resolve: resolveNetworkFirewallTLSInspectionRefs, target: keyARN, targetType: TypeKMSKey, rel: store.RelUses,
			goodAttrs: mustJSON(networkfirewall.DescribeTLSInspectionConfigurationOutput{
				TLSInspectionConfigurationResponse: &nfwtypes.TLSInspectionConfigurationResponse{EncryptionConfiguration: encryption},
			}),
			badAttrs: `{"TLSInspectionConfigurationResponse":7}`,
		},
	} {
		t.Run(c.name, c.run)
	}
}
