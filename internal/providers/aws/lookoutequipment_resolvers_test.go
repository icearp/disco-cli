package aws

import (
	"fmt"
	"testing"

	"codeberg.org/icearp/disco/internal/store"
)

func TestResolveLookoutEquipmentSchedulerRefs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	schARN := fmt.Sprintf("arn:aws:lookoutequipment:%s:%s:inference-scheduler/sch1", testRegion, acct.ID)
	roleARN := fmt.Sprintf("arn:aws:iam::%s:role/le-sched", acct.ID)
	keyARN := fmt.Sprintf("arn:aws:kms:%s:%s:key/abc-123", testRegion, acct.ID)
	inBucket := "le-input"
	outBucket := "le-output"
	attrs := fmt.Sprintf(`{
		"RoleArn":%q,
		"ServerSideKmsKeyId":%q,
		"DataInputConfiguration":{"S3InputConfiguration":{"Bucket":%q}},
		"DataOutputConfiguration":{"KmsKeyId":%q,"S3OutputConfiguration":{"Bucket":%q}}
	}`, roleARN, keyARN, inBucket, keyARN, outBucket)

	sID := upsertTestResource(t, st, "aws", acct.ID, TypeLookoutEquipmentInferenceScheduler, schARN, testRegion, attrs)
	rID := upsertTestResource(t, st, "aws", acct.ID, TypeIAMRole, roleARN, testRegion, "{}")
	kID := upsertTestResource(t, st, "aws", acct.ID, TypeKMSKey, keyARN, testRegion, "{}")
	bIn := upsertTestResource(t, st, "aws", acct.ID, TypeS3Bucket, "arn:aws:s3:::"+inBucket, testRegion, "{}")
	bOut := upsertTestResource(t, st, "aws", acct.ID, TypeS3Bucket, "arn:aws:s3:::"+outBucket, testRegion, "{}")

	if err := resolveLookoutEquipmentSchedulerRefs(acct, st); err != nil {
		t.Fatalf("resolveLookoutEquipmentSchedulerRefs: %v", err)
	}
	rels, _ := st.RelationshipsFrom(sID)
	assertRelationship(t, rels, sID, rID, store.RelAssumes)
	assertRelationship(t, rels, sID, kID, store.RelUses)
	assertRelationship(t, rels, sID, bIn, store.RelUses)
	assertRelationship(t, rels, sID, bOut, store.RelUses)
}
