package aws

import (
	"testing"

	"github.com/icearp/disco-cli/store"
)

func TestResolveHealthImagingDatastoreRefs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	dsARN := "arn:aws:medical-imaging:us-east-1:" + testAccountID + ":datastore/d1"
	keyARN := "arn:aws:kms:us-east-1:" + testAccountID + ":key/k-hi"
	lambdaARN := "arn:aws:lambda:us-east-1:" + testAccountID + ":function:auth"
	attrs := `{"KmsKeyArn":"` + keyARN + `","LambdaAuthorizerArn":"` + lambdaARN + `"}`

	dID := upsertTestResource(t, st, "aws", acct.ID, TypeHealthImagingDatastore, dsARN, testRegion, attrs)
	kID := upsertTestResource(t, st, "aws", acct.ID, TypeKMSKey, keyARN, testRegion, "{}")
	lID := upsertTestResource(t, st, "aws", acct.ID, TypeLambdaFunction, lambdaARN, testRegion, "{}")

	if err := resolveHealthImagingDatastoreRefs(acct, st); err != nil {
		t.Fatalf("resolveHealthImagingDatastoreRefs: %v", err)
	}
	rels, _ := st.RelationshipsFrom(dID)
	assertRelationship(t, rels, dID, kID, store.RelUses)
	assertRelationship(t, rels, dID, lID, store.RelUses)
}
