package aws

import (
	"testing"

	"codeberg.org/icearp/disco/store"
)

func TestResolveHealthLakeDatastoreRefs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	dsARN := "arn:aws:healthlake:us-east-1:" + testAccountID + ":datastore/fhir/d1"
	keyARN := "arn:aws:kms:us-east-1:" + testAccountID + ":key/k-hl"
	lambdaARN := "arn:aws:lambda:us-east-1:" + testAccountID + ":function:idp"
	attrs := `{"SseConfiguration":{"KmsEncryptionConfig":{"KmsKeyId":"` + keyARN + `"}},"IdentityProviderConfiguration":{"IdpLambdaArn":"` + lambdaARN + `"}}`

	dID := upsertTestResource(t, st, "aws", acct.ID, TypeHealthLakeFHIRDatastore, dsARN, testRegion, attrs)
	kID := upsertTestResource(t, st, "aws", acct.ID, TypeKMSKey, keyARN, testRegion, "{}")
	lID := upsertTestResource(t, st, "aws", acct.ID, TypeLambdaFunction, lambdaARN, testRegion, "{}")

	if err := resolveHealthLakeDatastoreRefs(acct, st); err != nil {
		t.Fatalf("resolveHealthLakeDatastoreRefs: %v", err)
	}
	rels, _ := st.RelationshipsFrom(dID)
	assertRelationship(t, rels, dID, kID, store.RelUses)
	assertRelationship(t, rels, dID, lID, store.RelUses)
}
