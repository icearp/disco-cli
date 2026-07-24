package aws

import (
	"fmt"
	"testing"

	"github.com/icearp/disco-cli/store"
)

func TestResolveOSSCollectionRefs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	keyARN := fmt.Sprintf("arn:aws:kms:%s:%s:key/k-1", testRegion, acct.ID)
	keyID := upsertTestResource(t, st, "aws", acct.ID, TypeKMSKey, keyARN, testRegion, "{}")
	cgARN := fmt.Sprintf("arn:aws:aoss:%s:%s:collection-group/grp1", testRegion, acct.ID)
	cgID := upsertTestResource(t, st, "aws", acct.ID, TypeOSSCollectionGroup, cgARN, testRegion, `{"Name":"grp1"}`)
	cARN := fmt.Sprintf("arn:aws:aoss:%s:%s:collection/c1", testRegion, acct.ID)
	attrs := fmt.Sprintf(`{"KmsKeyArn":"%s","CollectionGroupName":"grp1"}`, keyARN)
	cID := upsertTestResource(t, st, "aws", acct.ID, TypeOSSCollection, cARN, testRegion, attrs)
	if err := resolveOSSCollectionRefs(acct, st); err != nil {
		t.Fatalf("resolveOSSCollectionRefs: %v", err)
	}
	rels, _ := st.RelationshipsFrom(cID)
	assertRelationship(t, rels, cID, keyID, store.RelUses)
	assertRelationship(t, rels, cID, cgID, store.RelAttachedTo)
}

func TestResolveOSSVpcEndpointRefs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	vpceARN := "arn:aws:aoss:us-east-1:" + testAccountID + ":vpce/v-1"
	vpcARN := ec2ARN(testRegion, acct.ID, "vpc", "vpc-1")
	snARN := ec2ARN(testRegion, acct.ID, "subnet", "subnet-1")
	sgARN := ec2ARN(testRegion, acct.ID, "security-group", "sg-1")
	attrs := `{"VpcId":"vpc-1","SubnetIds":["subnet-1"],"SecurityGroupIds":["sg-1"]}`

	vID := upsertTestResource(t, st, "aws", acct.ID, TypeOSSVpcEndpoint, vpceARN, testRegion, attrs)
	v2 := upsertTestResource(t, st, "aws", acct.ID, TypeEC2VPC, vpcARN, testRegion, "{}")
	snID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2Subnet, snARN, testRegion, "{}")
	sgID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2SecurityGroup, sgARN, testRegion, "{}")

	if err := resolveOSSVpcEndpointRefs(acct, st); err != nil {
		t.Fatalf("resolveOSSVpcEndpointRefs: %v", err)
	}
	rels, _ := st.RelationshipsFrom(vID)
	assertRelationship(t, rels, vID, v2, store.RelAttachedTo)
	assertRelationship(t, rels, vID, snID, store.RelAttachedTo)
	assertRelationship(t, rels, vID, sgID, store.RelAttachedTo)
}
