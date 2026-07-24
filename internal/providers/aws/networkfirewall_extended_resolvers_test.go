package aws

import (
	"fmt"
	"testing"

	"github.com/icearp/disco-cli/store"
)

func TestResolveNFLoggingConfigToFirewall(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	fwARN := fmt.Sprintf("arn:aws:network-firewall:%s:%s:firewall/fw1", testRegion, acct.ID)
	fwID := upsertTestResource(t, st, "aws", acct.ID, TypeNetworkFirewallFirewall, fwARN, testRegion, "{}")
	lcARN := fwARN + "/logging-configuration"
	lcID := upsertTestResource(t, st, "aws", acct.ID, TypeNetworkFirewallLoggingConfiguration, lcARN, testRegion, "{}")
	if err := resolveNFLoggingConfigToFirewall(acct, st); err != nil {
		t.Fatalf("resolveNFLoggingConfigToFirewall: %v", err)
	}
	rels, _ := st.RelationshipsFrom(lcID)
	assertRelationship(t, rels, lcID, fwID, store.RelAttachedTo)
}

func TestResolveNFVpcEndpointAssociationRefs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	fwARN := fmt.Sprintf("arn:aws:network-firewall:%s:%s:firewall/fw1", testRegion, acct.ID)
	fwID := upsertTestResource(t, st, "aws", acct.ID, TypeNetworkFirewallFirewall, fwARN, testRegion, "{}")
	vpcARN := ec2ARN(testRegion, acct.ID, "vpc", "vpc-1")
	vpcID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2VPC, vpcARN, testRegion, "{}")
	subARN := ec2ARN(testRegion, acct.ID, "subnet", "subnet-1")
	subID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2Subnet, subARN, testRegion, "{}")
	veaARN := fmt.Sprintf("arn:aws:network-firewall:%s:%s:vpc-endpoint-association/vea-1", testRegion, acct.ID)
	attrs := fmt.Sprintf(`{"FirewallArn":"%s","VpcId":"vpc-1","SubnetMapping":{"SubnetId":"subnet-1"}}`, fwARN)
	veaID := upsertTestResource(t, st, "aws", acct.ID, TypeNetworkFirewallVpcEndpointAssociation, veaARN, testRegion, attrs)
	if err := resolveNFVpcEndpointAssociationRefs(acct, st); err != nil {
		t.Fatalf("resolveNFVpcEndpointAssociationRefs: %v", err)
	}
	rels, _ := st.RelationshipsFrom(veaID)
	assertRelationship(t, rels, veaID, fwID, store.RelAttachedTo)
	assertRelationship(t, rels, veaID, vpcID, store.RelAttachedTo)
	assertRelationship(t, rels, veaID, subID, store.RelAttachedTo)
}
