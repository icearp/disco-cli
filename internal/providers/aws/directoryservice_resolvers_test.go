package aws

import (
	"fmt"
	"testing"

	"github.com/icearp/disco-cli/store"
)

func TestResolveDirectoryServiceVpcRefs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	vpcARN := ec2ARN(testRegion, acct.ID, "vpc", "vpc-1")
	vpcID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2VPC, vpcARN, testRegion, "{}")
	subARN := ec2ARN(testRegion, acct.ID, "subnet", "subnet-1")
	subID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2Subnet, subARN, testRegion, "{}")
	sgARN := ec2ARN(testRegion, acct.ID, "security-group", "sg-1")
	sgID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2SecurityGroup, sgARN, testRegion, "{}")
	dARN := fmt.Sprintf("arn:aws:ds:%s:%s:directory/d-1", testRegion, acct.ID)
	attrs := `{"VpcSettings":{"VpcId":"vpc-1","SubnetIds":["subnet-1"],"SecurityGroupId":"sg-1"}}`
	dID := upsertTestResource(t, st, "aws", acct.ID, TypeDSMicrosoftAD, dARN, testRegion, attrs)
	if err := resolveDirectoryServiceVpcRefs(acct, st); err != nil {
		t.Fatalf("resolveDirectoryServiceVpcRefs: %v", err)
	}
	rels, _ := st.RelationshipsFrom(dID)
	assertRelationship(t, rels, dID, vpcID, store.RelAttachedTo)
	assertRelationship(t, rels, dID, subID, store.RelAttachedTo)
	assertRelationship(t, rels, dID, sgID, store.RelAttachedTo)
}
