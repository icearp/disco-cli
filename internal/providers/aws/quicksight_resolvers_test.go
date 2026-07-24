package aws

import (
	"fmt"
	"testing"

	"github.com/icearp/disco-cli/store"
)

func TestResolveQuickSightVPCConnectionRefs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	vARN := ec2ARN(testRegion, acct.ID, "vpc", "vpc-1")
	vID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2VPC, vARN, testRegion, "{}")
	subARN := ec2ARN(testRegion, acct.ID, "subnet", "subnet-1")
	subID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2Subnet, subARN, testRegion, "{}")
	sgARN := ec2ARN(testRegion, acct.ID, "security-group", "sg-1")
	sgID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2SecurityGroup, sgARN, testRegion, "{}")
	roleARN := fmt.Sprintf("arn:aws:iam::%s:role/QS", acct.ID)
	rID := upsertTestResource(t, st, "aws", acct.ID, TypeIAMRole, roleARN, testRegion, "{}")
	vcARN := fmt.Sprintf("arn:aws:quicksight:%s:%s:vpcConnection/vc1", testRegion, acct.ID)
	attrs := fmt.Sprintf(`{"VPCId":"vpc-1","SecurityGroupIds":["sg-1"],"RoleArn":%q,"NetworkInterfaces":[{"SubnetId":"subnet-1"}]}`, roleARN)
	vcID := upsertTestResource(t, st, "aws", acct.ID, TypeQuickSightVPCConnection, vcARN, testRegion, attrs)
	if err := resolveQuickSightVPCConnectionRefs(acct, st); err != nil {
		t.Fatalf("resolveQuickSightVPCConnectionRefs: %v", err)
	}
	rels, _ := st.RelationshipsFrom(vcID)
	assertRelationship(t, rels, vcID, vID, store.RelAttachedTo)
	assertRelationship(t, rels, vcID, subID, store.RelAttachedTo)
	assertRelationship(t, rels, vcID, sgID, store.RelUses)
	assertRelationship(t, rels, vcID, rID, store.RelAssumes)
}

func TestResolveQuickSightRefreshScheduleParent(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	dsARN := fmt.Sprintf("arn:aws:quicksight:%s:%s:dataset/ds1", testRegion, acct.ID)
	dsID := upsertTestResource(t, st, "aws", acct.ID, TypeQuickSightDataSet, dsARN, testRegion, "{}")
	rsARN := dsARN + "/refresh-schedule/sch1"
	rsID := upsertTestResource(t, st, "aws", acct.ID, TypeQuickSightRefreshSchedule, rsARN, testRegion, "{}")
	if err := resolveQuickSightRefreshScheduleParent(acct, st); err != nil {
		t.Fatalf("resolveQuickSightRefreshScheduleParent: %v", err)
	}
	rels, _ := st.RelationshipsFrom(rsID)
	assertRelationship(t, rels, rsID, dsID, store.RelAttachedTo)
}

func TestResolveQSDataSourceRefs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	dsARN := "arn:aws:quicksight:us-east-1:" + testAccountID + ":datasource/ds-1"
	secARN := "arn:aws:secretsmanager:us-east-1:" + testAccountID + ":secret:qs-rds"
	vcARN := "arn:aws:quicksight:us-east-1:" + testAccountID + ":vpcConnection/vc-1"
	attrs := `{"SecretArn":"` + secARN + `","VpcConnectionProperties":{"VpcConnectionArn":"` + vcARN + `"}}`

	dID := upsertTestResource(t, st, "aws", acct.ID, TypeQuickSightDataSource, dsARN, testRegion, attrs)
	sID := upsertTestResource(t, st, "aws", acct.ID, TypeSecretsManagerSecret, secARN, testRegion, "{}")
	vID := upsertTestResource(t, st, "aws", acct.ID, TypeQuickSightVPCConnection, vcARN, testRegion, "{}")

	if err := resolveQSDataSourceRefs(acct, st); err != nil {
		t.Fatalf("resolveQSDataSourceRefs: %v", err)
	}
	rels, _ := st.RelationshipsFrom(dID)
	assertRelationship(t, rels, dID, sID, store.RelUses)
	assertRelationship(t, rels, dID, vID, store.RelAttachedTo)
}

func TestResolveQuickSightNamespaceMembers(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	nsARN := fmt.Sprintf("arn:aws:quicksight:%s:%s:namespace/default", testRegion, acct.ID)
	nsID := upsertTestResource(t, st, "aws", acct.ID, TypeQuickSightNamespace, nsARN, testRegion, "{}")

	grpARN := fmt.Sprintf("arn:aws:quicksight:%s:%s:group/default/analysts", testRegion, acct.ID)
	grpID := upsertTestResource(t, st, "aws", acct.ID, TypeQuickSightGroup, grpARN, testRegion, "{}")
	usrARN := fmt.Sprintf("arn:aws:quicksight:%s:%s:user/default/alice", testRegion, acct.ID)
	usrID := upsertTestResource(t, st, "aws", acct.ID, TypeQuickSightUser, usrARN, testRegion, "{}")
	asgARN := nsARN + "/assignment/policy1"
	asgID := upsertTestResource(t, st, "aws", acct.ID, TypeQuickSightAssignment, asgARN, testRegion, "{}")

	if err := resolveQuickSightNamespaceMembers(acct, st); err != nil {
		t.Fatalf("resolveQuickSightNamespaceMembers: %v", err)
	}
	rels, _ := st.RelationshipsFrom(grpID)
	assertRelationship(t, rels, grpID, nsID, store.RelAttachedTo)
	rels, _ = st.RelationshipsFrom(usrID)
	assertRelationship(t, rels, usrID, nsID, store.RelAttachedTo)
	rels, _ = st.RelationshipsFrom(asgID)
	assertRelationship(t, rels, asgID, nsID, store.RelAttachedTo)
}

// TestResolveQuickSightNamespaceMembers_NoRows guards the empty/no-attrs case.
func TestResolveQuickSightNamespaceMembers_NoRows(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	if err := resolveQuickSightNamespaceMembers(acct, st); err != nil {
		t.Fatalf("resolveQuickSightNamespaceMembers empty: %v", err)
	}
}
