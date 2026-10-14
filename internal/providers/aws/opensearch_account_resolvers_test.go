package aws

import (
	"testing"

	opensearchtypes "github.com/aws/aws-sdk-go-v2/service/opensearch/types"
	"github.com/icearp/disco-cli/store"
)

// osEdgesFrom returns the edges from a row, failing the test on a read error.
func osEdgesFrom(t *testing.T, st *store.Store, id string) []store.Relationship {
	t.Helper()
	rels, err := st.RelationshipsFrom(id)
	if err != nil {
		t.Fatalf("RelationshipsFrom %s: %v", id, err)
	}
	return rels
}

func TestResolveOpenSearchConnections_LinksEachSideToItsDomain(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	domID := upsertTestResource(t, st, "aws", acct.ID, TypeOpenSearchDomain, osDomainARN, testRegion, "{}")
	outID := upsertTestResource(t, st, "aws", acct.ID, TypeOpenSearchOutboundConnection, osDomainARN+"/outbound-connection/c1", testRegion, "{}")
	inID := upsertTestResource(t, st, "aws", acct.ID, TypeOpenSearchInboundConnection, osDomainARN+"/inbound-connection/c2", testRegion, "{}")
	orphanID := upsertTestResource(t, st, "aws", acct.ID, TypeOpenSearchOutboundConnection,
		"arn:aws:es:us-east-1:123456789012:domain/absent/outbound-connection/c3", testRegion, "{}")
	if err := resolveOpenSearchConnections(acct, st); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	assertRelationship(t, osEdgesFrom(t, st, outID), outID, domID, store.RelAttachedTo)
	assertRelationship(t, osEdgesFrom(t, st, inID), inID, domID, store.RelAttachedTo)
	if rels := osEdgesFrom(t, st, orphanID); len(rels) != 0 {
		t.Errorf("connection under an unscanned domain emitted %v; want none", rels)
	}
}

func TestResolveOpenSearchVpcEndpoints_LinksDomainAndNetwork(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	domID := upsertTestResource(t, st, "aws", acct.ID, TypeOpenSearchDomain, osDomainARN, testRegion, "{}")
	vpcID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2VPC, ec2ARN(testRegion, acct.ID, "vpc", "vpc-1"), testRegion, "{}")
	subnetID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2Subnet, ec2ARN(testRegion, acct.ID, "subnet", "subnet-1"), testRegion, "{}")
	sgID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2SecurityGroup, ec2ARN(testRegion, acct.ID, "security-group", "sg-1"), testRegion, "{}")
	epID := upsertTestResource(t, st, "aws", acct.ID, TypeOpenSearchVpcEndpoint, osVpcEndpointID("aos-ep1"), testRegion, mustJSON(opensearchtypes.VpcEndpoint{
		VpcEndpointId: ptrStr("aos-ep1"), DomainArn: ptrStr(osDomainARN),
		VpcOptions: &opensearchtypes.VPCDerivedInfo{VPCId: ptrStr("vpc-1"), SubnetIds: []string{"subnet-1", "subnet-unscanned"}, SecurityGroupIds: []string{"sg-1"}},
	}))
	if err := resolveOpenSearchVpcEndpoints(acct, st); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	rels := osEdgesFrom(t, st, epID)
	assertRelationship(t, rels, epID, domID, store.RelAttachedTo)
	assertRelationship(t, rels, epID, vpcID, store.RelAttachedTo)
	assertRelationship(t, rels, epID, subnetID, store.RelUses)
	assertRelationship(t, rels, epID, sgID, store.RelUses)
	if len(rels) != 4 {
		t.Errorf("edges = %v; want exactly 4 (the unscanned subnet skipped)", rels)
	}
}

// An endpoint to another account's domain, with no VPC options, emits nothing.
func TestResolveOpenSearchVpcEndpoints_CrossAccountDomainNoOptions(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	upsertTestResource(t, st, "aws", acct.ID, TypeOpenSearchDomain, osDomainARN, testRegion, "{}")
	epID := upsertTestResource(t, st, "aws", acct.ID, TypeOpenSearchVpcEndpoint, osVpcEndpointID("aos-ep2"), testRegion, mustJSON(opensearchtypes.VpcEndpoint{
		VpcEndpointId: ptrStr("aos-ep2"), DomainArn: ptrStr("arn:aws:es:us-east-1:210987654321:domain/logs"),
	}))
	if err := resolveOpenSearchVpcEndpoints(acct, st); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if rels := osEdgesFrom(t, st, epID); len(rels) != 0 {
		t.Errorf("edges = %v; want none", rels)
	}
}

func TestResolveOpenSearchVpcEndpoints_BadAttrsFails(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	upsertTestResource(t, st, "aws", acct.ID, TypeOpenSearchVpcEndpoint, osVpcEndpointID("aos-ep3"), testRegion, `{"DomainArn":7}`)
	if err := resolveOpenSearchVpcEndpoints(acct, st); err == nil {
		t.Error("resolve = nil; want a decode error")
	}
}

func TestResolveOpenSearchDataSourceAttachments_LinksDomainOnly(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	domID := upsertTestResource(t, st, "aws", acct.ID, TypeOpenSearchDomain, osDomainARN, testRegion, "{}")
	toDomain := upsertTestResource(t, st, "aws", acct.ID, TypeOpenSearchDataSourceAttachment, osApp1ARN+"/data-source-attachment/att1", testRegion,
		mustJSON(opensearchtypes.DataSourceAttachmentSummary{AttachmentId: ptrStr("att1"), DataSourceArn: ptrStr(osDomainARN)}))
	toCollection := upsertTestResource(t, st, "aws", acct.ID, TypeOpenSearchDataSourceAttachment, osApp1ARN+"/data-source-attachment/att2", testRegion,
		mustJSON(opensearchtypes.DataSourceAttachmentSummary{AttachmentId: ptrStr("att2"), DataSourceArn: ptrStr("arn:aws:aoss:us-east-1:123456789012:collection/c1")}))
	bare := upsertTestResource(t, st, "aws", acct.ID, TypeOpenSearchDataSourceAttachment, osApp1ARN+"/data-source-attachment/att3", testRegion, "{}")
	if err := resolveOpenSearchDataSourceAttachments(acct, st); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	rels := osEdgesFrom(t, st, toDomain)
	assertRelationship(t, rels, toDomain, domID, store.RelUses)
	if len(rels) != 1 {
		t.Errorf("att1 edges = %v; want only the domain", rels)
	}
	for _, id := range []string{toCollection, bare} {
		if rels := osEdgesFrom(t, st, id); len(rels) != 0 {
			t.Errorf("%s edges = %v; want none", id, rels)
		}
	}
}
