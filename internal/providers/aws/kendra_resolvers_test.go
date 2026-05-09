package aws

import (
	"fmt"
	"testing"

	"codeberg.org/icearp/disco/store"
)

func TestResolveKendraChildToIndex(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	idxARN := fmt.Sprintf("arn:aws:kendra:%s:%s:index/i-1", testRegion, acct.ID)
	idxID := upsertTestResource(t, st, "aws", acct.ID, TypeKendraIndex, idxARN, testRegion, "{}")
	dsARN := idxARN + "/data-source/d-1"
	dsID := upsertTestResource(t, st, "aws", acct.ID, TypeKendraDataSource, dsARN, testRegion, "{}")
	faqARN := idxARN + "/faq/f-1"
	faqID := upsertTestResource(t, st, "aws", acct.ID, TypeKendraFaq, faqARN, testRegion, "{}")
	if err := resolveKendraChildToIndex(acct, st); err != nil {
		t.Fatalf("resolveKendraChildToIndex: %v", err)
	}
	dsRels, _ := st.RelationshipsFrom(dsID)
	assertRelationship(t, dsRels, dsID, idxID, store.RelAttachedTo)
	faqRels, _ := st.RelationshipsFrom(faqID)
	assertRelationship(t, faqRels, faqID, idxID, store.RelAttachedTo)
}

func TestResolveKendraIndexRefs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	idxARN := "arn:aws:kendra:us-east-1:" + testAccountID + ":index/i-1"
	roleARN := "arn:aws:iam::" + testAccountID + ":role/kendra"
	keyARN := "arn:aws:kms:us-east-1:" + testAccountID + ":key/k-kdr"
	attrs := `{"RoleArn":"` + roleARN + `","ServerSideEncryptionConfiguration":{"KmsKeyId":"` + keyARN + `"}}`

	iID := upsertTestResource(t, st, "aws", acct.ID, TypeKendraIndex, idxARN, testRegion, attrs)
	rID := upsertTestResource(t, st, "aws", acct.ID, TypeIAMRole, roleARN, testRegion, "{}")
	kID := upsertTestResource(t, st, "aws", acct.ID, TypeKMSKey, keyARN, testRegion, "{}")

	if err := resolveKendraIndexRefs(acct, st); err != nil {
		t.Fatalf("resolveKendraIndexRefs: %v", err)
	}
	rels, _ := st.RelationshipsFrom(iID)
	assertRelationship(t, rels, iID, rID, store.RelAssumes)
	assertRelationship(t, rels, iID, kID, store.RelUses)
}
