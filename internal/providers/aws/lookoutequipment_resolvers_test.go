package aws

import (
	"fmt"
	"testing"

	"codeberg.org/icearp/disco/store"
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

func TestResolveLookoutEquipmentModelVersionRefs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	modelARN := fmt.Sprintf("arn:aws:lookoutequipment:%s:%s:model/m1", testRegion, acct.ID)
	mvNativeID := modelARN + "/version/3"
	attrs := fmt.Sprintf(`{"ModelArn":%q,"ModelName":"m1","ModelVersion":3}`, modelARN)

	mvID := upsertTestResource(t, st, "aws", acct.ID, TypeLookoutEquipmentModelVersion, mvNativeID, testRegion, attrs)
	mID := upsertTestResource(t, st, "aws", acct.ID, TypeLookoutEquipmentModel, modelARN, testRegion, "{}")

	if err := resolveLookoutEquipmentModelVersionRefs(acct, st); err != nil {
		t.Fatalf("resolveLookoutEquipmentModelVersionRefs: %v", err)
	}
	rels, _ := st.RelationshipsFrom(mvID)
	assertRelationship(t, rels, mvID, mID, store.RelAttachedTo)
}

// TestResolveLookoutEquipmentModelVersionRefs_NoAttrs guards the nil/empty
// attrs path: a model version with no ModelArn emits no edge and never panics.
func TestResolveLookoutEquipmentModelVersionRefs_NoAttrs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	mvNativeID := fmt.Sprintf("arn:aws:lookoutequipment:%s:%s:model/m1/version/1", testRegion, acct.ID)
	mvID := upsertTestResource(t, st, "aws", acct.ID, TypeLookoutEquipmentModelVersion, mvNativeID, testRegion, "{}")

	if err := resolveLookoutEquipmentModelVersionRefs(acct, st); err != nil {
		t.Fatalf("resolveLookoutEquipmentModelVersionRefs: %v", err)
	}
	rels, _ := st.RelationshipsFrom(mvID)
	if len(rels) != 0 {
		t.Fatalf("expected no relationships, got %d", len(rels))
	}
}
