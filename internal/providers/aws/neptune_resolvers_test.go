package aws

import (
	"fmt"
	"testing"

	"github.com/icearp/disco-cli/store"
)

const (
	testNeptuneCluster = "graph-prod"
	testNeptuneSG      = "sg-neptune1111"
	testNeptuneKMSID   = "1111aaaa-2222-3333-4444-5555bbbb6666"
	testNeptuneInst    = "graph-prod-1"
)

// TestResolveNeptuneClusterTargets verifies cluster → KMS + SG edges.
func TestResolveNeptuneClusterTargets(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	sgARN := ec2ARN(testRegion, testAccountID, "security-group", testNeptuneSG)
	sgID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2SecurityGroup, sgARN, testRegion, "{}")

	keyARN := fmt.Sprintf("arn:aws:kms:%s:%s:key/%s", testRegion, testAccountID, testNeptuneKMSID)
	kID := upsertTestResource(t, st, "aws", acct.ID, TypeKMSKey, keyARN, testRegion,
		fmt.Sprintf(`{"KeyId":%q,"Arn":%q}`, testNeptuneKMSID, keyARN))

	clusterARN := fmt.Sprintf("arn:aws:rds:%s:%s:cluster:%s", testRegion, testAccountID, testNeptuneCluster)
	clusterAttrs := fmt.Sprintf(`{"DBClusterIdentifier":%q,"Engine":"neptune","KmsKeyId":%q,"VpcSecurityGroups":[{"VpcSecurityGroupId":%q,"Status":"active"}]}`,
		testNeptuneCluster, keyARN, testNeptuneSG)
	clusterID := upsertTestResource(t, st, "aws", acct.ID, TypeNeptuneCluster, clusterARN, testRegion, clusterAttrs)

	if err := resolveNeptuneClusterTargets(acct, st); err != nil {
		t.Fatalf("resolveNeptuneClusterTargets: %v", err)
	}
	rels, err := st.RelationshipsFrom(clusterID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	assertRelationship(t, rels, clusterID, kID, store.RelUses)
	assertRelationship(t, rels, clusterID, sgID, store.RelUses)
}

// TestResolveNeptuneInstanceCluster verifies instance → cluster closure
// when both are scanned.
func TestResolveNeptuneInstanceCluster(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	clusterARN := fmt.Sprintf("arn:aws:rds:%s:%s:cluster:%s", testRegion, testAccountID, testNeptuneCluster)
	clusterName := testNeptuneCluster
	region := testRegion
	cR := &store.Resource{
		Provider: "aws", AccountID: acct.ID, Type: TypeNeptuneCluster,
		NativeID: clusterARN, Region: &region, Name: &clusterName,
		AttributesJSON: `{}`, DiscoveredBy: testScanID,
	}
	if _, err := st.UpsertResource(cR); err != nil {
		t.Fatalf("upsert cluster: %v", err)
	}
	clusterID := store.ResourceID("aws", acct.ID, clusterARN)

	instARN := fmt.Sprintf("arn:aws:rds:%s:%s:db:%s", testRegion, testAccountID, testNeptuneInst)
	instAttrs := fmt.Sprintf(`{"DBInstanceIdentifier":%q,"DBClusterIdentifier":%q,"Engine":"neptune"}`,
		testNeptuneInst, testNeptuneCluster)
	instID := upsertTestResource(t, st, "aws", acct.ID, TypeNeptuneInstance, instARN, testRegion, instAttrs)

	if err := st.RecordHierarchy(clusterID, clusterID); err != nil {
		t.Fatalf("seed cluster closure: %v", err)
	}

	if err := resolveNeptuneInstanceCluster(acct, st); err != nil {
		t.Fatalf("resolveNeptuneInstanceCluster: %v", err)
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
