package aws

import (
	"fmt"
	"testing"

	"codeberg.org/icearp/disco/internal/store"
)

func TestResolveConfigRelationships(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	roleARN := fmt.Sprintf("arn:aws:iam::%s:role/ConfigRole", acct.ID)
	recARN := fmt.Sprintf("arn:aws:config:%s:%s:config-recorder/default", testRegion, acct.ID)
	recAttrs := fmt.Sprintf(`{"Name":"default","RoleARN":%q}`, roleARN)

	bucket := "config-bucket"
	kmsARN := fmt.Sprintf("arn:aws:kms:%s:%s:key/key-1", testRegion, acct.ID)
	snsARN := fmt.Sprintf("arn:aws:sns:%s:%s:config-topic", testRegion, acct.ID)
	dcARN := fmt.Sprintf("arn:aws:config:%s:%s:delivery-channel/default", testRegion, acct.ID)
	dcAttrs := fmt.Sprintf(`{"Name":"default","S3BucketName":%q,"S3KmsKeyArn":%q,"SnsTopicARN":%q}`, bucket, kmsARN, snsARN)

	lambdaARN := fmt.Sprintf("arn:aws:lambda:%s:%s:function:custom-rule", testRegion, acct.ID)
	ruleARN := fmt.Sprintf("arn:aws:config:%s:%s:config-rule/config-rule-abc", testRegion, acct.ID)
	ruleAttrs := fmt.Sprintf(`{"ConfigRuleArn":%q,"Source":{"Owner":"CUSTOM_LAMBDA","SourceIdentifier":%q}}`, ruleARN, lambdaARN)

	recID := upsertTestResource(t, st, "aws", acct.ID, TypeConfigRecorder, recARN, testRegion, recAttrs)
	roleID := upsertTestResource(t, st, "aws", acct.ID, TypeIAMRole, roleARN, "", "{}")
	dcID := upsertTestResource(t, st, "aws", acct.ID, TypeConfigDeliveryChannel, dcARN, testRegion, dcAttrs)
	bID := upsertTestResource(t, st, "aws", acct.ID, TypeS3Bucket, "arn:aws:s3:::"+bucket, "", "{}")
	kID := upsertTestResource(t, st, "aws", acct.ID, TypeKMSKey, kmsARN, testRegion, "{}")
	sID := upsertTestResource(t, st, "aws", acct.ID, TypeSNSTopic, snsARN, testRegion, "{}")
	rID := upsertTestResource(t, st, "aws", acct.ID, TypeConfigRule, ruleARN, testRegion, ruleAttrs)
	lID := upsertTestResource(t, st, "aws", acct.ID, TypeLambdaFunction, lambdaARN, testRegion, "{}")

	if err := resolveConfigRelationships(acct, st); err != nil {
		t.Fatalf("resolveConfigRelationships: %v", err)
	}

	recRels, _ := st.RelationshipsFrom(recID)
	assertRelationship(t, recRels, recID, roleID, store.RelAssumes)

	dcRels, _ := st.RelationshipsFrom(dcID)
	assertRelationship(t, dcRels, dcID, bID, store.RelUses)
	assertRelationship(t, dcRels, dcID, kID, store.RelUses)
	assertRelationship(t, dcRels, dcID, sID, store.RelUses)

	ruleRels, _ := st.RelationshipsFrom(rID)
	assertRelationship(t, ruleRels, rID, lID, store.RelUses)
}

func TestResolveConfigRelationships_ManagedRuleNoEdge(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	ruleARN := fmt.Sprintf("arn:aws:config:%s:%s:config-rule/managed", testRegion, acct.ID)
	attrs := fmt.Sprintf(`{"ConfigRuleArn":%q,"Source":{"Owner":"AWS","SourceIdentifier":"S3_BUCKET_PUBLIC_READ_PROHIBITED"}}`, ruleARN)
	rID := upsertTestResource(t, st, "aws", acct.ID, TypeConfigRule, ruleARN, testRegion, attrs)

	if err := resolveConfigRelationships(acct, st); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	rels, _ := st.RelationshipsFrom(rID)
	if len(rels) != 0 {
		t.Errorf("unexpected rels: %+v", rels)
	}
}
