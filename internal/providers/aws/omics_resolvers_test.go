package aws

import (
	"fmt"
	"testing"

	"codeberg.org/icearp/disco/internal/store"
)

func TestResolveOmicsWorkflowVersionParent(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	wARN := fmt.Sprintf("arn:aws:omics:%s:%s:workflow/w1", testRegion, acct.ID)
	wID := upsertTestResource(t, st, "aws", acct.ID, TypeOmicsWorkflow, wARN, testRegion, `{"Id":"w1"}`)
	vARN := wARN + "/version/v1"
	vID := upsertTestResource(t, st, "aws", acct.ID, TypeOmicsWorkflowVersion, vARN, testRegion, `{"WorkflowId":"w1"}`)
	if err := resolveOmicsWorkflowVersionParent(acct, st); err != nil {
		t.Fatalf("resolveOmicsWorkflowVersionParent: %v", err)
	}
	rels, _ := st.RelationshipsFrom(vID)
	assertRelationship(t, rels, vID, wID, store.RelAttachedTo)
}

func TestResolveOmicsStoreKMS(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	keyARN := fmt.Sprintf("arn:aws:kms:%s:%s:key/omics-key", testRegion, acct.ID)
	keyID := upsertTestResource(t, st, "aws", acct.ID, TypeKMSKey, keyARN, testRegion, "{}")
	asARN := fmt.Sprintf("arn:aws:omics:%s:%s:annotationStore/as-1", testRegion, acct.ID)
	asAttrs := fmt.Sprintf(`{"SseConfig":{"KeyArn":%q}}`, keyARN)
	asID := upsertTestResource(t, st, "aws", acct.ID, TypeOmicsAnnotationStore, asARN, testRegion, asAttrs)
	rsARN := fmt.Sprintf("arn:aws:omics:%s:%s:referenceStore/rs-1", testRegion, acct.ID)
	rsID := upsertTestResource(t, st, "aws", acct.ID, TypeOmicsReferenceStore, rsARN, testRegion, asAttrs)
	if err := resolveOmicsStoreKMS(acct, st); err != nil {
		t.Fatalf("resolveOmicsStoreKMS: %v", err)
	}
	rels, _ := st.RelationshipsFrom(asID)
	assertRelationship(t, rels, asID, keyID, store.RelUses)
	rels, _ = st.RelationshipsFrom(rsID)
	assertRelationship(t, rels, rsID, keyID, store.RelUses)
}
