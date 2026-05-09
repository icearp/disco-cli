package aws

import (
	"fmt"
	"testing"

	"codeberg.org/icearp/disco/store"
)

func upsertMemDBNamed(t *testing.T, st *store.Store, acct *account, rtype, arn, name, region, attrs string) string {
	t.Helper()
	n := name
	r := &store.Resource{
		Provider: "aws", AccountID: acct.ID, Type: rtype,
		NativeID: arn, Name: &n, Region: &region,
		AttributesJSON: attrs, DiscoveredBy: testScanID,
	}
	if _, err := st.UpsertResource(r); err != nil {
		t.Fatalf("upsert %s: %v", rtype, err)
	}
	return store.ResourceID("aws", acct.ID, rtype, arn)
}

func TestResolveMemoryDBClusterRefs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	keyARN := fmt.Sprintf("arn:aws:kms:%s:%s:key/mdb-key", testRegion, acct.ID)
	keyID := upsertTestResource(t, st, "aws", acct.ID, TypeKMSKey, keyARN, testRegion, "{}")
	aclARN := fmt.Sprintf("arn:aws:memorydb:%s:%s:acl/admin-acl", testRegion, acct.ID)
	aclID := upsertMemDBNamed(t, st, acct, TypeMemoryDBACL, aclARN, "admin-acl", testRegion, "{}")
	sgARN := fmt.Sprintf("arn:aws:memorydb:%s:%s:subnetgroup/sg-1", testRegion, acct.ID)
	sgID := upsertMemDBNamed(t, st, acct, TypeMemoryDBSubnetGroup, sgARN, "sg-1", testRegion, "{}")
	pgARN := fmt.Sprintf("arn:aws:memorydb:%s:%s:parametergroup/pg-1", testRegion, acct.ID)
	pgID := upsertMemDBNamed(t, st, acct, TypeMemoryDBParameterGroup, pgARN, "pg-1", testRegion, "{}")
	ec2sgARN := ec2ARN(testRegion, acct.ID, "security-group", "sg-aaa")
	ec2sgID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2SecurityGroup, ec2sgARN, testRegion, "{}")
	topicARN := fmt.Sprintf("arn:aws:sns:%s:%s:mdb-topic", testRegion, acct.ID)
	topicID := upsertTestResource(t, st, "aws", acct.ID, TypeSNSTopic, topicARN, testRegion, "{}")
	cARN := fmt.Sprintf("arn:aws:memorydb:%s:%s:cluster/c-1", testRegion, acct.ID)
	cAttrs := fmt.Sprintf(`{"KmsKeyId":%q,"ACLName":"admin-acl","SubnetGroupName":"sg-1","ParameterGroupName":"pg-1","SnsTopicArn":%q,"SecurityGroups":[{"SecurityGroupId":"sg-aaa"}]}`, keyARN, topicARN)
	cID := upsertMemDBNamed(t, st, acct, TypeMemoryDBCluster, cARN, "c-1", testRegion, cAttrs)

	if err := resolveMemoryDBClusterRefs(acct, st); err != nil {
		t.Fatalf("resolveMemoryDBClusterRefs: %v", err)
	}
	rels, _ := st.RelationshipsFrom(cID)
	assertRelationship(t, rels, cID, keyID, store.RelUses)
	assertRelationship(t, rels, cID, aclID, store.RelUses)
	assertRelationship(t, rels, cID, sgID, store.RelAttachedTo)
	assertRelationship(t, rels, cID, pgID, store.RelUses)
	assertRelationship(t, rels, cID, ec2sgID, store.RelUses)
	assertRelationship(t, rels, cID, topicID, store.RelUses)
}

func TestResolveMemoryDBSubnetGroupRefs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	vpcARN := ec2ARN(testRegion, acct.ID, "vpc", "vpc-1")
	vpcID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2VPC, vpcARN, testRegion, "{}")
	subARN := ec2ARN(testRegion, acct.ID, "subnet", "subnet-1")
	subID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2Subnet, subARN, testRegion, "{}")
	sgARN := fmt.Sprintf("arn:aws:memorydb:%s:%s:subnetgroup/sg-1", testRegion, acct.ID)
	sgAttrs := `{"VpcId":"vpc-1","Subnets":[{"Identifier":"subnet-1"}]}`
	sgID := upsertMemDBNamed(t, st, acct, TypeMemoryDBSubnetGroup, sgARN, "sg-1", testRegion, sgAttrs)
	if err := resolveMemoryDBSubnetGroupRefs(acct, st); err != nil {
		t.Fatalf("resolveMemoryDBSubnetGroupRefs: %v", err)
	}
	rels, _ := st.RelationshipsFrom(sgID)
	assertRelationship(t, rels, sgID, vpcID, store.RelAttachedTo)
	assertRelationship(t, rels, sgID, subID, store.RelAttachedTo)
}

func TestResolveMemoryDBACLUsers(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	uARN := fmt.Sprintf("arn:aws:memorydb:%s:%s:user/admin", testRegion, acct.ID)
	uID := upsertMemDBNamed(t, st, acct, TypeMemoryDBUser, uARN, "admin", testRegion, "{}")
	aclARN := fmt.Sprintf("arn:aws:memorydb:%s:%s:acl/acl-1", testRegion, acct.ID)
	aclAttrs := `{"UserNames":["admin"]}`
	aclID := upsertMemDBNamed(t, st, acct, TypeMemoryDBACL, aclARN, "acl-1", testRegion, aclAttrs)
	if err := resolveMemoryDBACLUsers(acct, st); err != nil {
		t.Fatalf("resolveMemoryDBACLUsers: %v", err)
	}
	rels, _ := st.RelationshipsFrom(aclID)
	assertRelationship(t, rels, aclID, uID, store.RelContains)
}
