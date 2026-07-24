package aws

import (
	"fmt"
	"testing"

	"github.com/icearp/disco-cli/store"
)

func TestResolveEntityResolutionPolicyStatementToParent(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	mwARN := fmt.Sprintf("arn:aws:entityresolution:%s:%s:matchingworkflow/mw-1", testRegion, acct.ID)
	mwID := upsertTestResource(t, st, "aws", acct.ID, TypeEntityResolutionMatchingWorkflow, mwARN, testRegion, "{}")
	imwARN := fmt.Sprintf("arn:aws:entityresolution:%s:%s:idmappingworkflow/im-1", testRegion, acct.ID)
	imwID := upsertTestResource(t, st, "aws", acct.ID, TypeEntityResolutionIDMappingWorkflow, imwARN, testRegion, "{}")

	psMW := upsertTestResource(t, st, "aws", acct.ID, TypeEntityResolutionPolicyStatement, mwARN+"/policy", testRegion, "{}")
	psIMW := upsertTestResource(t, st, "aws", acct.ID, TypeEntityResolutionPolicyStatement, imwARN+"/policy", testRegion, "{}")

	if err := resolveEntityResolutionPolicyStatementToParent(acct, st); err != nil {
		t.Fatalf("resolveEntityResolutionPolicyStatementToParent: %v", err)
	}
	rels, _ := st.RelationshipsFrom(psMW)
	assertRelationship(t, rels, psMW, mwID, store.RelAttachedTo)
	rels, _ = st.RelationshipsFrom(psIMW)
	assertRelationship(t, rels, psIMW, imwID, store.RelAttachedTo)
}

func TestResolveEntityResolutionMatchingWorkflowRefs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	mwARN := fmt.Sprintf("arn:aws:entityresolution:%s:%s:matchingworkflow/mw1", testRegion, acct.ID)
	roleARN := fmt.Sprintf("arn:aws:iam::%s:role/er", acct.ID)
	tableARN := fmt.Sprintf("arn:aws:glue:%s:%s:table/db1/customers", testRegion, acct.ID)
	keyARN := fmt.Sprintf("arn:aws:kms:%s:%s:key/abc-123", testRegion, acct.ID)
	bName := "er-output"
	bARN := "arn:aws:s3:::" + bName
	attrs := fmt.Sprintf(`{"RoleArn":%q,"InputSourceConfig":[{"InputSourceARN":%q}],"OutputSourceConfig":[{"OutputS3Path":"s3://%s/path","KMSArn":%q}]}`, roleARN, tableARN, bName, keyARN)

	mwID := upsertTestResource(t, st, "aws", acct.ID, TypeEntityResolutionMatchingWorkflow, mwARN, testRegion, attrs)
	rID := upsertTestResource(t, st, "aws", acct.ID, TypeIAMRole, roleARN, testRegion, "{}")
	tID := upsertTestResource(t, st, "aws", acct.ID, TypeGlueTable, tableARN, testRegion, "{}")
	kID := upsertTestResource(t, st, "aws", acct.ID, TypeKMSKey, keyARN, testRegion, "{}")
	bID := upsertTestResource(t, st, "aws", acct.ID, TypeS3Bucket, bARN, testRegion, "{}")

	if err := resolveEntityResolutionMatchingWorkflowRefs(acct, st); err != nil {
		t.Fatalf("resolveEntityResolutionMatchingWorkflowRefs: %v", err)
	}
	rels, _ := st.RelationshipsFrom(mwID)
	assertRelationship(t, rels, mwID, rID, store.RelAssumes)
	assertRelationship(t, rels, mwID, tID, store.RelUses)
	assertRelationship(t, rels, mwID, kID, store.RelUses)
	assertRelationship(t, rels, mwID, bID, store.RelUses)
}
