package aws

import (
	"testing"

	"codeberg.org/icearp/disco/store"
)

func TestResolveDocDBElasticClusterRefs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	clARN := "arn:aws:docdb-elastic:us-east-1:" + testAccountID + ":cluster/c1"
	keyARN := "arn:aws:kms:us-east-1:" + testAccountID + ":key/k-de"
	subnetARN := ec2ARN(testRegion, acct.ID, "subnet", "subnet-1")
	sgARN := ec2ARN(testRegion, acct.ID, "security-group", "sg-1")
	attrs := `{"KmsKeyId":"` + keyARN + `","SubnetIds":["subnet-1"],"VpcSecurityGroupIds":["sg-1"]}`

	cID := upsertTestResource(t, st, "aws", acct.ID, TypeDocDBElasticCluster, clARN, testRegion, attrs)
	kID := upsertTestResource(t, st, "aws", acct.ID, TypeKMSKey, keyARN, testRegion, "{}")
	snID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2Subnet, subnetARN, testRegion, "{}")
	sgID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2SecurityGroup, sgARN, testRegion, "{}")

	if err := resolveDocDBElasticClusterRefs(acct, st); err != nil {
		t.Fatalf("resolveDocDBElasticClusterRefs: %v", err)
	}
	rels, _ := st.RelationshipsFrom(cID)
	assertRelationship(t, rels, cID, kID, store.RelUses)
	assertRelationship(t, rels, cID, snID, store.RelAttachedTo)
	assertRelationship(t, rels, cID, sgID, store.RelAttachedTo)
}
