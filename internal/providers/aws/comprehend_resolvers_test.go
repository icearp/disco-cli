package aws

import (
	"encoding/json"
	"fmt"
	"testing"

	comprehendtypes "github.com/aws/aws-sdk-go-v2/service/comprehend/types"
	"github.com/icearp/disco-cli/store"
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

func TestResolveComprehendEntityRecognizerRefs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	roleARN := fmt.Sprintf("arn:aws:iam::%s:role/comp", acct.ID)
	roleID := upsertTestResource(t, st, "aws", acct.ID, TypeIAMRole, roleARN, "", "{}")
	modelKARN := fmt.Sprintf("arn:aws:kms:%s:%s:key/model", testRegion, acct.ID)
	modelKID := upsertTestResource(t, st, "aws", acct.ID, TypeKMSKey, modelKARN, testRegion, "{}")
	volKARN := fmt.Sprintf("arn:aws:kms:%s:%s:key/vol", testRegion, acct.ID)
	volKID := upsertTestResource(t, st, "aws", acct.ID, TypeKMSKey, volKARN, testRegion, "{}")
	subID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2Subnet, ec2ARN(testRegion, acct.ID, "subnet", "subnet-1"), testRegion, "{}")
	sgID := upsertTestResource(t, st, "aws", acct.ID, TypeEC2SecurityGroup, ec2ARN(testRegion, acct.ID, "security-group", "sg-1"), testRegion, "{}")
	fwARN := fmt.Sprintf("arn:aws:comprehend:%s:%s:flywheel/fw1", testRegion, acct.ID)
	fwID := upsertTestResource(t, st, "aws", acct.ID, TypeComprehendFlywheel, fwARN, testRegion, "{}")

	erARN := fmt.Sprintf("arn:aws:comprehend:%s:%s:entity-recognizer/er1", testRegion, acct.ID)
	erBody, _ := json.Marshal(comprehendtypes.EntityRecognizerProperties{
		EntityRecognizerArn: ptrStr(erARN),
		DataAccessRoleArn:   ptrStr(roleARN),
		ModelKmsKeyId:       ptrStr(modelKARN),
		VolumeKmsKeyId:      ptrStr(volKARN),
		FlywheelArn:         ptrStr(fwARN),
		VpcConfig:           &comprehendtypes.VpcConfig{Subnets: []string{"subnet-1"}, SecurityGroupIds: []string{"sg-1"}},
	})
	erID := upsertTestResource(t, st, "aws", acct.ID, TypeComprehendEntityRecognizer, erARN, testRegion, string(erBody))
	if err := resolveComprehendEntityRecognizerRefs(acct, st); err != nil {
		t.Fatalf("resolveComprehendEntityRecognizerRefs: %v", err)
	}
	rels, _ := st.RelationshipsFrom(erID)
	assertRelationship(t, rels, erID, roleID, store.RelUses)
	assertRelationship(t, rels, erID, modelKID, store.RelUses) // ModelKmsKeyId
	assertRelationship(t, rels, erID, volKID, store.RelUses)   // VolumeKmsKeyId
	assertRelationship(t, rels, erID, subID, store.RelAttachedTo)
	assertRelationship(t, rels, erID, sgID, store.RelAttachedTo)
	assertRelationship(t, rels, erID, fwID, store.RelAttachedTo)
}

func TestResolveComprehendEntityRecognizerRefs_NoAttrs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	erARN := fmt.Sprintf("arn:aws:comprehend:%s:%s:entity-recognizer/er1", testRegion, acct.ID)
	erID := upsertTestResource(t, st, "aws", acct.ID, TypeComprehendEntityRecognizer, erARN, testRegion, "{}")
	if err := resolveComprehendEntityRecognizerRefs(acct, st); err != nil {
		t.Fatalf("resolveComprehendEntityRecognizerRefs: %v", err)
	}
	if rels, _ := st.RelationshipsFrom(erID); len(rels) != 0 {
		t.Errorf("emitted %d edges, want 0", len(rels))
	}
}

func TestResolveComprehendEndpointRefs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	dcARN := fmt.Sprintf("arn:aws:comprehend:%s:%s:document-classifier/dc1", testRegion, acct.ID)
	dcID := upsertTestResource(t, st, "aws", acct.ID, TypeComprehendDocumentClassifier, dcARN, testRegion, "{}")
	erARN := fmt.Sprintf("arn:aws:comprehend:%s:%s:entity-recognizer/er1", testRegion, acct.ID)
	erID := upsertTestResource(t, st, "aws", acct.ID, TypeComprehendEntityRecognizer, erARN, testRegion, "{}")

	roleARN := fmt.Sprintf("arn:aws:iam::%s:role/ep", acct.ID)
	roleID := upsertTestResource(t, st, "aws", acct.ID, TypeIAMRole, roleARN, "", "{}")

	dcEpARN := fmt.Sprintf("arn:aws:comprehend:%s:%s:document-classifier-endpoint/ep1", testRegion, acct.ID)
	dcEpBody, _ := json.Marshal(comprehendtypes.EndpointProperties{EndpointArn: ptrStr(dcEpARN), ModelArn: ptrStr(dcARN), DataAccessRoleArn: ptrStr(roleARN)})
	dcEpID := upsertTestResource(t, st, "aws", acct.ID, TypeComprehendDocumentClassifierEndpoint, dcEpARN, testRegion, string(dcEpBody))

	erEpARN := fmt.Sprintf("arn:aws:comprehend:%s:%s:entity-recognizer-endpoint/ep2", testRegion, acct.ID)
	erEpBody, _ := json.Marshal(comprehendtypes.EndpointProperties{EndpointArn: ptrStr(erEpARN), ModelArn: ptrStr(erARN)})
	erEpID := upsertTestResource(t, st, "aws", acct.ID, TypeComprehendEntityRecognizerEndpoint, erEpARN, testRegion, string(erEpBody))

	if err := resolveComprehendEndpointRefs(acct, st); err != nil {
		t.Fatalf("resolveComprehendEndpointRefs: %v", err)
	}
	dcRels, _ := st.RelationshipsFrom(dcEpID)
	assertRelationship(t, dcRels, dcEpID, dcID, store.RelUses)   // ModelArn → document-classifier
	assertRelationship(t, dcRels, dcEpID, roleID, store.RelUses) // DataAccessRoleArn → role
	erRels, _ := st.RelationshipsFrom(erEpID)
	assertRelationship(t, erRels, erEpID, erID, store.RelUses) // ModelArn → entity-recognizer
}

func TestResolveComprehendEndpointRefs_NoAttrs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	epARN := fmt.Sprintf("arn:aws:comprehend:%s:%s:document-classifier-endpoint/ep1", testRegion, acct.ID)
	epID := upsertTestResource(t, st, "aws", acct.ID, TypeComprehendDocumentClassifierEndpoint, epARN, testRegion, "{}")
	if err := resolveComprehendEndpointRefs(acct, st); err != nil {
		t.Fatalf("resolveComprehendEndpointRefs: %v", err)
	}
	if rels, _ := st.RelationshipsFrom(epID); len(rels) != 0 {
		t.Errorf("emitted %d edges, want 0", len(rels))
	}
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
