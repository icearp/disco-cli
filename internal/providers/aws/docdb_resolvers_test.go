package aws

import (
	"fmt"
	"testing"

	"codeberg.org/icearp/disco/store"
)

const (
	testDocDBCluster = "docs-prod"
	testDocDBSG      = "sg-docdb1111"
	testDocDBKMSID   = "aaaa1111-bbbb-2222-cccc-3333dddd4444"
	testDocDBInst    = "docs-prod-1"
)

// TestResolveDocDBClusterTargets verifies cluster → KMS + SG edges.
func TestResolveDocDBClusterTargets(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	sgARN := ec2ARN(testRegion, testAccountID, "security-group", testDocDBSG)
	sgID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2SecurityGroup, sgARN, testRegion, "{}")

	keyARN := fmt.Sprintf("arn:aws:kms:%s:%s:key/%s", testRegion, testAccountID, testDocDBKMSID)
	kID := upsertTestResource(t, st, "aws", acct.ID, TypeKMSKey, keyARN, testRegion,
		fmt.Sprintf(`{"KeyId":%q,"Arn":%q}`, testDocDBKMSID, keyARN))

	clusterARN := fmt.Sprintf("arn:aws:rds:%s:%s:cluster:%s", testRegion, testAccountID, testDocDBCluster)
	clusterAttrs := fmt.Sprintf(`{"DBClusterIdentifier":%q,"Engine":"docdb","KmsKeyId":%q,"VpcSecurityGroups":[{"VpcSecurityGroupId":%q,"Status":"active"}]}`,
		testDocDBCluster, keyARN, testDocDBSG)
	clusterID := upsertTestResource(t, st, "aws", acct.ID, TypeDocDBCluster, clusterARN, testRegion, clusterAttrs)

	if err := resolveDocDBClusterTargets(acct, st); err != nil {
		t.Fatalf("resolveDocDBClusterTargets: %v", err)
	}
	rels, err := st.RelationshipsFrom(clusterID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	assertRelationship(t, rels, clusterID, kID, store.RelUses)
	assertRelationship(t, rels, clusterID, sgID, store.RelUses)
}

// TestResolveDocDBInstanceCluster verifies instance → cluster closure
// when the cluster is also scanned. Instance NativeID must persist with
// Name set (cluster lookup is name-keyed) — bypass upsertTestResource
// to populate Name.
func TestResolveDocDBInstanceCluster(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	clusterARN := fmt.Sprintf("arn:aws:rds:%s:%s:cluster:%s", testRegion, testAccountID, testDocDBCluster)
	clusterName := testDocDBCluster
	region := testRegion
	cR := &store.Resource{
		Provider: "aws", AccountID: acct.ID, Type: TypeDocDBCluster,
		NativeID: clusterARN, Region: &region, Name: &clusterName,
		AttributesJSON: `{}`, DiscoveredBy: testScanID,
	}
	if _, err := st.UpsertResource(cR); err != nil {
		t.Fatalf("upsert cluster: %v", err)
	}
	clusterID := store.ResourceID("aws", acct.ID, clusterARN)

	instARN := fmt.Sprintf("arn:aws:rds:%s:%s:db:%s", testRegion, testAccountID, testDocDBInst)
	instAttrs := fmt.Sprintf(`{"DBInstanceIdentifier":%q,"DBClusterIdentifier":%q,"Engine":"docdb"}`,
		testDocDBInst, testDocDBCluster)
	instID := upsertTestResource(t, st, "aws", acct.ID, TypeDocDBInstance, instARN, testRegion, instAttrs)

	// Seed the cluster self-entry so its ancestry chain exists before the
	// resolver wires the instance in (RecordHierarchyBatch inherits
	// ancestors from the parent's existing closure rows).
	if err := st.RecordHierarchy(clusterID, clusterID); err != nil {
		t.Fatalf("seed cluster closure: %v", err)
	}

	if err := resolveDocDBInstanceCluster(acct, st); err != nil {
		t.Fatalf("resolveDocDBInstanceCluster: %v", err)
	}
	desc, err := st.DescendantsOf(clusterID, store.ResourceFilter{})
	if err != nil {
		t.Fatalf("DescendantsOf: %v", err)
	}
	found := false
	for _, d := range desc {
		if d.ID == instID {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected cluster %s to contain instance %s in closure", clusterID, instID)
	}
}
