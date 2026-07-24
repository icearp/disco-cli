package aws

import (
	"testing"

	"github.com/icearp/disco-cli/store"
)

func TestResolvePipesPipeRefs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	pipeARN := "arn:aws:pipes:us-east-1:" + testAccountID + ":pipe/myPipe"
	roleARN := "arn:aws:iam::" + testAccountID + ":role/pipe"
	keyARN := "arn:aws:kms:us-east-1:" + testAccountID + ":key/k-pipe"
	srcARN := "arn:aws:sqs:us-east-1:" + testAccountID + ":src-q"
	tgtARN := "arn:aws:sns:us-east-1:" + testAccountID + ":tgt-topic"
	enrichARN := "arn:aws:lambda:us-east-1:" + testAccountID + ":function:enrich"
	attrs := `{"RoleArn":"` + roleARN + `","KmsKeyIdentifier":"` + keyARN +
		`","Source":"` + srcARN + `","Target":"` + tgtARN + `","Enrichment":"` + enrichARN + `"}`

	pID := upsertTestResource(t, st, "aws", acct.ID, TypePipesPipe, pipeARN, testRegion, attrs)
	rID := upsertTestResource(t, st, "aws", acct.ID, TypeIAMRole, roleARN, testRegion, "{}")
	kID := upsertTestResource(t, st, "aws", acct.ID, TypeKMSKey, keyARN, testRegion, "{}")
	sID := upsertTestResource(t, st, "aws", acct.ID, TypeSQSQueue, srcARN, testRegion, "{}")
	tID := upsertTestResource(t, st, "aws", acct.ID, TypeSNSTopic, tgtARN, testRegion, "{}")
	eID := upsertTestResource(t, st, "aws", acct.ID, TypeLambdaFunction, enrichARN, testRegion, "{}")

	if err := resolvePipesPipeRefs(acct, st); err != nil {
		t.Fatalf("resolvePipesPipeRefs: %v", err)
	}
	rels, _ := st.RelationshipsFrom(pID)
	assertRelationship(t, rels, pID, rID, store.RelAssumes)
	assertRelationship(t, rels, pID, kID, store.RelUses)
	assertRelationship(t, rels, pID, sID, store.RelRoutesTo)
	assertRelationship(t, rels, pID, tID, store.RelRoutesTo)
	assertRelationship(t, rels, pID, eID, store.RelUses)
}
