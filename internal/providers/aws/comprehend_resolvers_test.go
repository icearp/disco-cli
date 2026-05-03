package aws

import (
	"fmt"
	"testing"

	"codeberg.org/icearp/disco/internal/store"
)

func TestResolveComprehendDocumentClassifierRefs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	roleARN := fmt.Sprintf("arn:aws:iam::%s:role/comp", acct.ID)
	roleID := upsertTestResource(t, st, "aws", acct.ID, TypeIAMRole, roleARN, "", "{}")
	kARN := fmt.Sprintf("arn:aws:kms:%s:%s:key/abc", testRegion, acct.ID)
	kID := upsertTestResource(t, st, "aws", acct.ID, TypeKMSKey, kARN, testRegion, "{}")
	subARN := ec2ARN(testRegion, acct.ID, "subnet", "subnet-1")
	subID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2Subnet, subARN, testRegion, "{}")
	sgARN := ec2ARN(testRegion, acct.ID, "security-group", "sg-1")
	sgID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2SecurityGroup, sgARN, testRegion, "{}")
	dcARN := fmt.Sprintf("arn:aws:comprehend:%s:%s:document-classifier/dc1", testRegion, acct.ID)
	attrs := fmt.Sprintf(`{"DataAccessRoleArn":"%s","ModelKmsKeyId":"%s","VpcConfig":{"Subnets":["subnet-1"],"SecurityGroupIds":["sg-1"]}}`, roleARN, kARN)
	dcID := upsertTestResource(t, st, "aws", acct.ID, TypeComprehendDocumentClassifier, dcARN, testRegion, attrs)
	if err := resolveComprehendDocumentClassifierRefs(acct, st); err != nil {
		t.Fatalf("resolveComprehendDocumentClassifierRefs: %v", err)
	}
	rels, _ := st.RelationshipsFrom(dcID)
	assertRelationship(t, rels, dcID, roleID, store.RelUses)
	assertRelationship(t, rels, dcID, kID, store.RelUses)
	assertRelationship(t, rels, dcID, subID, store.RelAttachedTo)
	assertRelationship(t, rels, dcID, sgID, store.RelAttachedTo)
}

func TestResolveComprehendFlywheelRefs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	dcARN := fmt.Sprintf("arn:aws:comprehend:%s:%s:document-classifier/dc1", testRegion, acct.ID)
	dcID := upsertTestResource(t, st, "aws", acct.ID, TypeComprehendDocumentClassifier, dcARN, testRegion, "{}")
	fwARN := fmt.Sprintf("arn:aws:comprehend:%s:%s:flywheel/fw1", testRegion, acct.ID)
	fwID := upsertTestResource(t, st, "aws", acct.ID, TypeComprehendFlywheel, fwARN, testRegion, fmt.Sprintf(`{"ActiveModelArn":"%s"}`, dcARN))
	if err := resolveComprehendFlywheelRefs(acct, st); err != nil {
		t.Fatalf("resolveComprehendFlywheelRefs: %v", err)
	}
	rels, _ := st.RelationshipsFrom(fwID)
	assertRelationship(t, rels, fwID, dcID, store.RelUses)
}
