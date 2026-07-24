package aws

import (
	"testing"

	"github.com/icearp/disco-cli/store"
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

func TestResolveDocDBElasticSnapshotRefs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	clARN := "arn:aws:docdb-elastic:us-east-1:" + testAccountID + ":cluster/c1"
	snapARN := "arn:aws:docdb-elastic:us-east-1:" + testAccountID + ":cluster-snapshot/s1"
	keyARN := "arn:aws:kms:us-east-1:" + testAccountID + ":key/k-de"
	attrs := `{"ClusterArn":"` + clARN + `","KmsKeyId":"` + keyARN + `"}`

	cID := upsertTestResource(t, st, "aws", acct.ID, TypeDocDBElasticCluster, clARN, testRegion, "{}")
	kID := upsertTestResource(t, st, "aws", acct.ID, TypeKMSKey, keyARN, testRegion, "{}")
	sID := upsertTestResource(t, st, "aws", acct.ID, TypeDocDBElasticClusterSnapshot, snapARN, testRegion, attrs)

	if err := resolveDocDBElasticSnapshotRefs(acct, st); err != nil {
		t.Fatalf("resolveDocDBElasticSnapshotRefs: %v", err)
	}
	rels, _ := st.RelationshipsFrom(sID)
	assertRelationship(t, rels, sID, cID, store.RelAttachedTo)
	assertRelationship(t, rels, sID, kID, store.RelUses)
}

func TestResolveDocDBElasticSnapshotRefs_NoAttrs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	snapARN := "arn:aws:docdb-elastic:us-east-1:" + testAccountID + ":cluster-snapshot/s1"
	sID := upsertTestResource(t, st, "aws", acct.ID, TypeDocDBElasticClusterSnapshot, snapARN, testRegion, "{}")
	if err := resolveDocDBElasticSnapshotRefs(acct, st); err != nil {
		t.Fatalf("resolveDocDBElasticSnapshotRefs: %v", err)
	}
	if rels, _ := st.RelationshipsFrom(sID); len(rels) != 0 {
		t.Errorf("emitted %d edges, want 0", len(rels))
	}
}
