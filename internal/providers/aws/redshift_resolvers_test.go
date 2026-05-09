package aws

import (
	"fmt"
	"testing"

	"codeberg.org/icearp/disco/store"
)

const (
	testRedshiftClusterName = "analytics-prod"
	testRedshiftSubnetGroup = "data-subnets"
	testRedshiftVPC         = "vpc-aaaa1111"
	testRedshiftSubnetA     = "subnet-aaaa1111"
	testRedshiftSubnetB     = "subnet-bbbb2222"
	testRedshiftSG          = "sg-cccc3333"
	testRedshiftRoleName    = "RedshiftDataAccess"
	testRedshiftKMSKeyID    = "abcd1234-ef56-7890-abcd-ef1234567890"
)

// TestResolveRedshiftClusterTargets_HappyPath exercises every cluster
// edge: subnet-group, VPC, security group, IAM role, KMS key.
func TestResolveRedshiftClusterTargets_HappyPath(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	sgrpARN := redshiftSubnetGroupARN(testRegion, testAccountID, testRedshiftSubnetGroup)
	sgrpID := upsertTestResource(t, st, "aws", acct.ID, TypeRedshiftSubnetGroup, sgrpARN, testRegion, "{}")

	vpcARN := ec2ARN(testRegion, testAccountID, "vpc", testRedshiftVPC)
	vpcID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2VPC, vpcARN, testRegion, "{}")

	sgARN := ec2ARN(testRegion, testAccountID, "security-group", testRedshiftSG)
	sgID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2SecurityGroup, sgARN, testRegion, "{}")

	roleARN := fmt.Sprintf("arn:aws:iam::%s:role/%s", testAccountID, testRedshiftRoleName)
	roleID := upsertTestResource(t, st, "aws", acct.ID, TypeIAMRole, roleARN, "", "{}")

	keyARN := fmt.Sprintf("arn:aws:kms:%s:%s:key/%s", testRegion, testAccountID, testRedshiftKMSKeyID)
	kID := upsertTestResource(t, st, "aws", acct.ID, TypeKMSKey, keyARN, testRegion,
		fmt.Sprintf(`{"KeyId":%q,"Arn":%q}`, testRedshiftKMSKeyID, keyARN))

	clusterARN := redshiftClusterARN(testRegion, testAccountID, testRedshiftClusterName)
	clusterAttrs := fmt.Sprintf(`{"ClusterIdentifier":%q,"ClusterStatus":"available","ClusterSubnetGroupName":%q,"VpcId":%q,"VpcSecurityGroups":[{"VpcSecurityGroupId":%q,"Status":"active"}],"IamRoles":[{"IamRoleArn":%q,"ApplyStatus":"in-sync"}],"KmsKeyId":%q}`,
		testRedshiftClusterName, testRedshiftSubnetGroup, testRedshiftVPC,
		testRedshiftSG, roleARN, keyARN)
	clusterID := upsertTestResource(t, st, "aws", acct.ID, TypeRedshiftCluster, clusterARN, testRegion, clusterAttrs)

	if err := resolveRedshiftClusterTargets(acct, st); err != nil {
		t.Fatalf("resolveRedshiftClusterTargets: %v", err)
	}
	rels, err := st.RelationshipsFrom(clusterID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	assertRelationship(t, rels, clusterID, sgrpID, store.RelUses)
	assertRelationship(t, rels, clusterID, vpcID, store.RelAttachedTo)
	assertRelationship(t, rels, clusterID, sgID, store.RelUses)
	assertRelationship(t, rels, clusterID, roleID, store.RelAssumes)
	assertRelationship(t, rels, clusterID, kID, store.RelUses)
}

// TestResolveRedshiftClusterTargets_FKSafe verifies missing targets emit
// no edges (cross-account / unscanned).
func TestResolveRedshiftClusterTargets_FKSafe(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	clusterARN := redshiftClusterARN(testRegion, testAccountID, testRedshiftClusterName)
	clusterAttrs := `{"ClusterSubnetGroupName":"ghost","VpcId":"vpc-ghost","VpcSecurityGroups":[{"VpcSecurityGroupId":"sg-ghost"}],"IamRoles":[{"IamRoleArn":"arn:aws:iam::999:role/ghost"}],"KmsKeyId":"alias/ghost"}`
	clusterID := upsertTestResource(t, st, "aws", acct.ID, TypeRedshiftCluster, clusterARN, testRegion, clusterAttrs)

	if err := resolveRedshiftClusterTargets(acct, st); err != nil {
		t.Fatalf("resolveRedshiftClusterTargets: %v", err)
	}
	rels, err := st.RelationshipsFrom(clusterID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("expected zero edges, got %d", len(rels))
	}
}

// TestResolveRedshiftSubnetGroupTargets_HappyPath verifies subnet-group
// → VPC + subnet-group → subnet edges.
func TestResolveRedshiftSubnetGroupTargets_HappyPath(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	vpcARN := ec2ARN(testRegion, testAccountID, "vpc", testRedshiftVPC)
	vpcID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2VPC, vpcARN, testRegion, "{}")

	subAARN := ec2ARN(testRegion, testAccountID, "subnet", testRedshiftSubnetA)
	subBARN := ec2ARN(testRegion, testAccountID, "subnet", testRedshiftSubnetB)
	subAID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2Subnet, subAARN, testRegion, "{}")
	subBID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2Subnet, subBARN, testRegion, "{}")

	sgrpARN := redshiftSubnetGroupARN(testRegion, testAccountID, testRedshiftSubnetGroup)
	sgrpAttrs := fmt.Sprintf(`{"ClusterSubnetGroupName":%q,"VpcId":%q,"Subnets":[{"SubnetIdentifier":%q},{"SubnetIdentifier":%q}]}`,
		testRedshiftSubnetGroup, testRedshiftVPC, testRedshiftSubnetA, testRedshiftSubnetB)
	sgrpID := upsertTestResource(t, st, "aws", acct.ID, TypeRedshiftSubnetGroup, sgrpARN, testRegion, sgrpAttrs)

	if err := resolveRedshiftSubnetGroupTargets(acct, st); err != nil {
		t.Fatalf("resolveRedshiftSubnetGroupTargets: %v", err)
	}
	rels, err := st.RelationshipsFrom(sgrpID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	assertRelationship(t, rels, sgrpID, vpcID, store.RelAttachedTo)
	assertRelationship(t, rels, sgrpID, subAID, store.RelContains)
	assertRelationship(t, rels, sgrpID, subBID, store.RelContains)
}

func TestResolveRedshiftIntegrationRefs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	intARN := fmt.Sprintf("arn:aws:redshift:%s:%s:integration:int-1", testRegion, acct.ID)
	rdsARN := fmt.Sprintf("arn:aws:rds:%s:%s:cluster:src", testRegion, acct.ID)
	nsARN := fmt.Sprintf("arn:aws:redshift-serverless:%s:%s:namespace/ns-1", testRegion, acct.ID)
	kARN := fmt.Sprintf("arn:aws:kms:%s:%s:key/abc-123", testRegion, acct.ID)
	attrs := fmt.Sprintf(`{"SourceArn":%q,"TargetArn":%q,"KMSKeyId":%q}`, rdsARN, nsARN, kARN)

	iID := upsertTestResource(t, st, "aws", acct.ID, TypeRedshiftIntegration, intARN, testRegion, attrs)
	rdsID := upsertTestResource(t, st, "aws", acct.ID, TypeRDSDBCluster, rdsARN, testRegion, "{}")
	nsID := upsertTestResource(t, st, "aws", acct.ID, TypeRedshiftServerlessNamespace, nsARN, testRegion, "{}")
	kID := upsertTestResource(t, st, "aws", acct.ID, TypeKMSKey, kARN, testRegion, "{}")

	if err := resolveRedshiftIntegrationRefs(acct, st); err != nil {
		t.Fatalf("resolveRedshiftIntegrationRefs: %v", err)
	}
	rels, _ := st.RelationshipsFrom(iID)
	assertRelationship(t, rels, iID, rdsID, store.RelUses)
	assertRelationship(t, rels, iID, nsID, store.RelUses)
	assertRelationship(t, rels, iID, kID, store.RelUses)
}
