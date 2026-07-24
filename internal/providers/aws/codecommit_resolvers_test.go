package aws

import (
	"testing"

	"github.com/icearp/disco-cli/store"
)

func TestResolveCodeCommitRepoKMS(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	repoARN := "arn:aws:codecommit:us-east-1:" + testAccountID + ":myrepo"
	keyARN := "arn:aws:kms:us-east-1:" + testAccountID + ":key/k-cc"
	attrs := `{"KmsKeyId":"` + keyARN + `"}`

	rID := upsertTestResource(t, st, "aws", acct.ID, TypeCodeCommitRepository, repoARN, testRegion, attrs)
	kID := upsertTestResource(t, st, "aws", acct.ID, TypeKMSKey, keyARN, testRegion, "{}")

	if err := resolveCodeCommitRepoKMS(acct, st); err != nil {
		t.Fatalf("resolveCodeCommitRepoKMS: %v", err)
	}
	rels, _ := st.RelationshipsFrom(rID)
	assertRelationship(t, rels, rID, kID, store.RelUses)
}
