package aws

import (
	"fmt"
	"testing"

	"codeberg.org/icearp/disco/store"
)

func TestResolveCodePipelineWebhookToPipeline(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	pARN := fmt.Sprintf("arn:aws:codepipeline:%s:%s:p1", testRegion, acct.ID)
	pID := upsertTestResource(t, st, "aws", acct.ID, TypeCodePipelinePipeline, pARN, testRegion, "{}")
	whARN := fmt.Sprintf("arn:aws:codepipeline:%s:%s:webhook:wh1", testRegion, acct.ID)
	whID := upsertTestResource(t, st, "aws", acct.ID, TypeCodePipelineWebhook, whARN, testRegion, `{"Definition":{"TargetPipeline":"p1"}}`)
	if err := resolveCodePipelineWebhookToPipeline(acct, st); err != nil {
		t.Fatalf("resolveCodePipelineWebhookToPipeline: %v", err)
	}
	rels, _ := st.RelationshipsFrom(whID)
	assertRelationship(t, rels, whID, pID, store.RelAttachedTo)
}

func TestResolveCodePipelinePipelineRefs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	pARN := fmt.Sprintf("arn:aws:codepipeline:%s:%s:p2", testRegion, acct.ID)
	roleARN := "arn:aws:iam::" + testAccountID + ":role/cp"
	bucketARN := "arn:aws:s3:::cp-artifacts"
	keyARN := "arn:aws:kms:us-east-1:" + testAccountID + ":key/k-cp"
	attrs := `{"Pipeline":{"RoleArn":"` + roleARN + `","ArtifactStore":{"Location":"cp-artifacts","EncryptionKey":{"Id":"` + keyARN + `"}}}}`

	pID := upsertTestResource(t, st, "aws", acct.ID, TypeCodePipelinePipeline, pARN, testRegion, attrs)
	rID := upsertTestResource(t, st, "aws", acct.ID, TypeIAMRole, roleARN, testRegion, "{}")
	bID := upsertTestResource(t, st, "aws", acct.ID, TypeS3Bucket, bucketARN, testRegion, "{}")
	kID := upsertTestResource(t, st, "aws", acct.ID, TypeKMSKey, keyARN, testRegion, "{}")

	if err := resolveCodePipelinePipelineRefs(acct, st); err != nil {
		t.Fatalf("resolveCodePipelinePipelineRefs: %v", err)
	}
	rels, _ := st.RelationshipsFrom(pID)
	assertRelationship(t, rels, pID, rID, store.RelAssumes)
	assertRelationship(t, rels, pID, bID, store.RelUses)
	assertRelationship(t, rels, pID, kID, store.RelUses)
}
