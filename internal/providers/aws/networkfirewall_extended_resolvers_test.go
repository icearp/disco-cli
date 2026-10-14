package aws

import (
	"encoding/json"
	"fmt"
	"testing"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/networkfirewall"
	nfwtypes "github.com/aws/aws-sdk-go-v2/service/networkfirewall/types"
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

func nfContainerAssociationAttrs(t *testing.T, clusterARNs ...string) string {
	t.Helper()
	out := networkfirewall.DescribeContainerAssociationOutput{ContainerAssociationName: sdkaws.String("a")}
	for _, arn := range clusterARNs {
		out.ContainerMonitoringConfigurations = append(out.ContainerMonitoringConfigurations, nfwtypes.ContainerMonitoringConfiguration{ClusterArn: sdkaws.String(arn)})
	}
	b, err := json.Marshal(out)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestResolveNFContainerAssociationClusters_ECSAndEKS(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	ecsARN := fmt.Sprintf("arn:aws:ecs:%s:%s:cluster/web", testRegion, acct.ID)
	ecsID := upsertTestResource(t, st, "aws", acct.ID, TypeECSCluster, ecsARN, testRegion, "{}")
	eksARN := fmt.Sprintf("arn:aws:eks:%s:%s:cluster/k8s", testRegion, acct.ID)
	eksID := upsertTestResource(t, st, "aws", acct.ID, TypeEKSCluster, eksARN, testRegion, "{}")
	unscanned := fmt.Sprintf("arn:aws:eks:%s:%s:cluster/gone", testRegion, acct.ID)
	assocARN := fmt.Sprintf("arn:aws:network-firewall:%s:%s:container-association/a", testRegion, acct.ID)
	assocID := upsertTestResource(t, st, "aws", acct.ID, TypeNetworkFirewallContainerAssociation, assocARN, testRegion,
		nfContainerAssociationAttrs(t, ecsARN, eksARN, unscanned))
	if err := resolveNFContainerAssociationClusters(acct, st); err != nil {
		t.Fatalf("resolveNFContainerAssociationClusters: %v", err)
	}
	rels, err := st.RelationshipsFrom(assocID)
	if err != nil {
		t.Fatal(err)
	}
	assertRelationship(t, rels, assocID, ecsID, store.RelUses)
	assertRelationship(t, rels, assocID, eksID, store.RelUses)
	if len(rels) != 2 {
		t.Errorf("edges = %v; want only the two scanned clusters", rels)
	}
}

func TestResolveNFContainerAssociationClusters_NoAttrs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	upsertTestResource(t, st, "aws", acct.ID, TypeECSCluster, fmt.Sprintf("arn:aws:ecs:%s:%s:cluster/web", testRegion, acct.ID), testRegion, "{}")
	assocARN := fmt.Sprintf("arn:aws:network-firewall:%s:%s:container-association/a", testRegion, acct.ID)
	assocID := upsertTestResource(t, st, "aws", acct.ID, TypeNetworkFirewallContainerAssociation, assocARN, testRegion, "{}")
	if err := resolveNFContainerAssociationClusters(acct, st); err != nil {
		t.Fatalf("resolveNFContainerAssociationClusters: %v", err)
	}
	rels, err := st.RelationshipsFrom(assocID)
	if err != nil {
		t.Fatal(err)
	}
	if len(rels) != 0 {
		t.Errorf("edges = %v; want none", rels)
	}
}
