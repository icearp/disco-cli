package aws

import (
	"fmt"
	"testing"

	"github.com/icearp/disco-cli/store"
)

func TestResolveDAXClusterRefs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	pgARN := daxARN(testRegion, acct.ID, "parameter-group", "pg1")
	pgID := upsertTestResource(t, st, "aws", acct.ID, TypeDAXParameterGroup, pgARN, testRegion, "{}")
	sgARN := daxARN(testRegion, acct.ID, "subnet-group", "sg1")
	sgID := upsertTestResource(t, st, "aws", acct.ID, TypeDAXSubnetGroup, sgARN, testRegion, "{}")
	ec2sgARN := ec2ARN(testRegion, acct.ID, "security-group", "sg-aaa")
	ec2sgID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2SecurityGroup, ec2sgARN, testRegion, "{}")
	roleARN := fmt.Sprintf("arn:aws:iam::%s:role/dax", acct.ID)
	roleID := upsertTestResource(t, st, "aws", acct.ID, TypeIAMRole, roleARN, "", "{}")
	topicARN := fmt.Sprintf("arn:aws:sns:%s:%s:notify", testRegion, acct.ID)
	topicID := upsertTestResource(t, st, "aws", acct.ID, TypeSNSTopic, topicARN, testRegion, "{}")
	clARN := daxARN(testRegion, acct.ID, "cache", "cl1")
	attrs := fmt.Sprintf(`{"ParameterGroup":{"ParameterGroupName":"pg1"},"SubnetGroup":"sg1","SecurityGroups":[{"SecurityGroupIdentifier":"sg-aaa"}],"IamRoleArn":"%s","NotificationConfiguration":{"TopicArn":"%s"}}`, roleARN, topicARN)
	clID := upsertTestResource(t, st, "aws", acct.ID, TypeDAXCluster, clARN, testRegion, attrs)
	if err := resolveDAXClusterRefs(acct, st); err != nil {
		t.Fatalf("resolveDAXClusterRefs: %v", err)
	}
	rels, _ := st.RelationshipsFrom(clID)
	assertRelationship(t, rels, clID, pgID, store.RelUses)
	assertRelationship(t, rels, clID, sgID, store.RelAttachedTo)
	assertRelationship(t, rels, clID, ec2sgID, store.RelAttachedTo)
	assertRelationship(t, rels, clID, roleID, store.RelUses)
	assertRelationship(t, rels, clID, topicID, store.RelUses)
}

func TestResolveDAXSubnetGroupRefs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	vpcARN := ec2ARN(testRegion, acct.ID, "vpc", "vpc-1")
	vpcID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2VPC, vpcARN, testRegion, "{}")
	subARN := ec2ARN(testRegion, acct.ID, "subnet", "subnet-1")
	subID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2Subnet, subARN, testRegion, "{}")
	sgARN := daxARN(testRegion, acct.ID, "subnet-group", "sg1")
	sgID := upsertTestResource(t, st, "aws", acct.ID, TypeDAXSubnetGroup, sgARN, testRegion, `{"VpcId":"vpc-1","Subnets":[{"SubnetIdentifier":"subnet-1"}]}`)
	if err := resolveDAXSubnetGroupRefs(acct, st); err != nil {
		t.Fatalf("resolveDAXSubnetGroupRefs: %v", err)
	}
	rels, _ := st.RelationshipsFrom(sgID)
	assertRelationship(t, rels, sgID, vpcID, store.RelAttachedTo)
	assertRelationship(t, rels, sgID, subID, store.RelAttachedTo)
}
