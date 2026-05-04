package aws

import (
	"fmt"
	"testing"

	"codeberg.org/icearp/disco/internal/store"
)

func TestResolveEMRChildrenToCluster(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	clARN := emrARN(testRegion, acct.ID, "cluster", "j-ABC")
	clID := upsertTestResource(t, st, "aws", acct.ID, TypeEMRCluster, clARN, testRegion, "{}")
	stepARN := clARN + "/step/s-1"
	stepID := upsertTestResource(t, st, "aws", acct.ID, TypeEMRStep, stepARN, testRegion, "{}")
	ifARN := clARN + "/instance-fleet/if-1"
	ifID := upsertTestResource(t, st, "aws", acct.ID, TypeEMRInstanceFleet, ifARN, testRegion, "{}")
	igARN := clARN + "/instance-group/ig-1"
	igID := upsertTestResource(t, st, "aws", acct.ID, TypeEMRInstanceGroup, igARN, testRegion, "{}")

	if err := resolveEMRChildrenToCluster(acct, st); err != nil {
		t.Fatalf("resolveEMRChildrenToCluster: %v", err)
	}
	for _, child := range []string{stepID, ifID, igID} {
		rels, _ := st.RelationshipsFrom(child)
		assertRelationship(t, rels, child, clID, store.RelAttachedTo)
	}
}

func TestResolveEMRStudioSessionMappingToStudio(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	stARN := emrARN(testRegion, acct.ID, "studio", "es-1")
	stID := upsertTestResource(t, st, "aws", acct.ID, TypeEMRStudio, stARN, testRegion, "{}")
	smARN := fmt.Sprintf("%s/identity/u-1", stARN)
	smID := upsertTestResource(t, st, "aws", acct.ID, TypeEMRStudioSessionMapping, smARN, testRegion, "{}")
	if err := resolveEMRStudioSessionMappingToStudio(acct, st); err != nil {
		t.Fatalf("resolveEMRStudioSessionMappingToStudio: %v", err)
	}
	rels, _ := st.RelationshipsFrom(smID)
	assertRelationship(t, rels, smID, stID, store.RelAttachedTo)
}

func TestResolveEMRStudioVPC(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	vpcARN := ec2ARN(testRegion, acct.ID, "vpc", "vpc-1")
	vpcID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2VPC, vpcARN, testRegion, "{}")
	stARN := emrARN(testRegion, acct.ID, "studio", "es-1")
	stID := upsertTestResource(t, st, "aws", acct.ID, TypeEMRStudio, stARN, testRegion, `{"VpcId":"vpc-1"}`)
	if err := resolveEMRStudioVPC(acct, st); err != nil {
		t.Fatalf("resolveEMRStudioVPC: %v", err)
	}
	rels, _ := st.RelationshipsFrom(stID)
	assertRelationship(t, rels, stID, vpcID, store.RelAttachedTo)
}

func TestResolveEMRClusterRefs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	clARN := emrARN(testRegion, acct.ID, "cluster", "j-XYZ")
	roleARN := "arn:aws:iam::" + testAccountID + ":role/EMR_DefaultRole"
	keyARN := "arn:aws:kms:us-east-1:" + testAccountID + ":key/k-emr"
	snARN := ec2ARN(testRegion, acct.ID, "subnet", "subnet-1")
	sgARN := ec2ARN(testRegion, acct.ID, "security-group", "sg-1")
	attrs := `{"ServiceRole":"EMR_DefaultRole","LogEncryptionKmsKeyId":"` + keyARN +
		`","Ec2InstanceAttributes":{"Ec2KeyName":"emrkey","Ec2SubnetId":"subnet-1","EmrManagedMasterSecurityGroup":"sg-1"}}`

	cID := upsertTestResource(t, st, "aws", acct.ID, TypeEMRCluster, clARN, testRegion, attrs)
	rID := upsertTestResource(t, st, "aws", acct.ID, TypeIAMRole, roleARN, testRegion, "{}")
	kID := upsertTestResource(t, st, "aws", acct.ID, TypeKMSKey, keyARN, testRegion, "{}")
	snID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2Subnet, snARN, testRegion, "{}")
	sgID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2SecurityGroup, sgARN, testRegion, "{}")

	kpName := "emrkey"
	kpRegion := testRegion
	kpARN := ec2ARN(testRegion, acct.ID, "key-pair", "key-emr")
	if _, err := st.UpsertResource(&store.Resource{
		Provider: "aws", AccountID: acct.ID, Type: TypeEC2KeyPair, NativeID: kpARN,
		Region: &kpRegion, Name: &kpName, AttributesJSON: "{}", DiscoveredBy: testScanID,
	}); err != nil {
		t.Fatalf("upsert keypair: %v", err)
	}
	kpID := store.ResourceID("aws", acct.ID, TypeEC2KeyPair, kpARN)

	if err := resolveEMRClusterRefs(acct, st); err != nil {
		t.Fatalf("resolveEMRClusterRefs: %v", err)
	}
	rels, _ := st.RelationshipsFrom(cID)
	assertRelationship(t, rels, cID, rID, store.RelAssumes)
	assertRelationship(t, rels, cID, kID, store.RelUses)
	assertRelationship(t, rels, cID, snID, store.RelAttachedTo)
	assertRelationship(t, rels, cID, sgID, store.RelAttachedTo)
	assertRelationship(t, rels, cID, kpID, store.RelUses)
}
