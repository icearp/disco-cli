package aws

import (
	"fmt"
	"testing"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	omicstypes "github.com/aws/aws-sdk-go-v2/service/omics/types"
	"github.com/icearp/disco-cli/store"
)

func TestResolveOmicsAnnotationStoreVersionParent(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	storeID := "as-1"
	asARN := fmt.Sprintf("arn:aws:omics:%s:%s:annotationStore/%s", testRegion, acct.ID, storeID)
	asAttrs := mustJSON(omicstypes.AnnotationStoreItem{Id: &storeID, StoreArn: &asARN})
	asResID := upsertTestResource(t, st, "aws", acct.ID, TypeOmicsAnnotationStore, asARN, testRegion, asAttrs)
	vARN := asARN + "/version/v1"
	vAttrs := mustJSON(omicstypes.AnnotationStoreVersionItem{VersionArn: &vARN, StoreId: &storeID})
	vID := upsertTestResource(t, st, "aws", acct.ID, TypeOmicsAnnotationStoreVersion, vARN, testRegion, vAttrs)
	if err := resolveOmicsAnnotationStoreVersionParent(acct, st); err != nil {
		t.Fatalf("resolveOmicsAnnotationStoreVersionParent: %v", err)
	}
	rels, _ := st.RelationshipsFrom(vID)
	assertRelationship(t, rels, vID, asResID, store.RelAttachedTo)
}

func TestResolveOmicsAnnotationStoreVersionParent_NoAttrs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	vARN := fmt.Sprintf("arn:aws:omics:%s:%s:annotationStore/as-1/version/v1", testRegion, acct.ID)
	vID := upsertTestResource(t, st, "aws", acct.ID, TypeOmicsAnnotationStoreVersion, vARN, testRegion, "{}")
	if err := resolveOmicsAnnotationStoreVersionParent(acct, st); err != nil {
		t.Fatalf("resolveOmicsAnnotationStoreVersionParent: %v", err)
	}
	if rels, _ := st.RelationshipsFrom(vID); len(rels) != 0 {
		t.Errorf("expected no relationships, got %d", len(rels))
	}
}

func TestResolveOmicsReferenceParent(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	storeID := "rs-1"
	rsARN := fmt.Sprintf("arn:aws:omics:%s:%s:referenceStore/%s", testRegion, acct.ID, storeID)
	rsAttrs := mustJSON(omicstypes.ReferenceStoreDetail{Id: &storeID, Arn: &rsARN})
	rsResID := upsertTestResource(t, st, "aws", acct.ID, TypeOmicsReferenceStore, rsARN, testRegion, rsAttrs)
	refARN := rsARN + "/reference/ref-1"
	refAttrs := mustJSON(omicstypes.ReferenceListItem{Arn: &refARN, ReferenceStoreId: &storeID})
	refID := upsertTestResource(t, st, "aws", acct.ID, TypeOmicsReference, refARN, testRegion, refAttrs)
	if err := resolveOmicsReferenceParent(acct, st); err != nil {
		t.Fatalf("resolveOmicsReferenceParent: %v", err)
	}
	rels, _ := st.RelationshipsFrom(refID)
	assertRelationship(t, rels, refID, rsResID, store.RelAttachedTo)
}

func TestResolveOmicsReferenceParent_NoAttrs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	refARN := fmt.Sprintf("arn:aws:omics:%s:%s:referenceStore/rs-1/reference/ref-1", testRegion, acct.ID)
	refID := upsertTestResource(t, st, "aws", acct.ID, TypeOmicsReference, refARN, testRegion, "{}")
	if err := resolveOmicsReferenceParent(acct, st); err != nil {
		t.Fatalf("resolveOmicsReferenceParent: %v", err)
	}
	if rels, _ := st.RelationshipsFrom(refID); len(rels) != 0 {
		t.Errorf("expected no relationships, got %d", len(rels))
	}
}

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

func TestResolveOmicsShareResource(t *testing.T) {
	acct := newTestAccount(testAccountID)
	arnOf := func(kind, id string) string {
		return fmt.Sprintf("arn:aws:omics:%s:%s:%s/%s", testRegion, acct.ID, kind, id)
	}
	wfARN, vsARN, asARN := arnOf("workflow", "1234"), arnOf("variantStore", "vs1"), arnOf("annotationStore", "as1")
	wfAttrs := mustJSON(omicstypes.WorkflowListItem{Id: sdkaws.String("1234"), Arn: &wfARN})
	vsAttrs := mustJSON(omicstypes.VariantStoreItem{Id: sdkaws.String("vs1"), StoreArn: &vsARN})
	asAttrs := mustJSON(omicstypes.AnnotationStoreItem{Id: sdkaws.String("as1"), StoreArn: &asARN})
	shareAttrs := func(resourceARN string) string {
		return mustJSON(omicstypes.ShareDetails{ShareId: sdkaws.String("s1"), ResourceArn: &resourceARN})
	}
	tests := []struct {
		name                   string
		targetType, targetARN  string
		targetAttrs, shareAttr string
		scanTarget, wantEdge   bool
	}{
		{"shared workflow", TypeOmicsWorkflow, wfARN, wfAttrs, shareAttrs(wfARN), true, true},
		{"shared variant store", TypeOmicsVariantStore, vsARN, vsAttrs, shareAttrs(vsARN), true, true},
		{"shared annotation store", TypeOmicsAnnotationStore, asARN, asAttrs, shareAttrs(asARN), true, true},
		{"shared workflow not scanned", TypeOmicsWorkflow, wfARN, wfAttrs, shareAttrs(wfARN), false, false},
		{"no attrs", TypeOmicsWorkflow, wfARN, wfAttrs, "{}", true, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			targetID := store.ResourceID("aws", acct.ID, tc.targetARN)
			if tc.scanTarget {
				targetID = upsertTestResource(t, st, "aws", acct.ID, tc.targetType, tc.targetARN, testRegion, tc.targetAttrs)
			}
			shareID := upsertTestResource(t, st, "aws", acct.ID, TypeOmicsShare, tc.targetARN+"/share/s1", testRegion, tc.shareAttr)
			if err := resolveOmicsShareResource(acct, st); err != nil {
				t.Fatalf("resolveOmicsShareResource: %v", err)
			}
			rels, _ := st.RelationshipsFrom(shareID)
			if tc.wantEdge {
				assertRelationship(t, rels, shareID, targetID, store.RelAttachedTo)
				return
			}
			if len(rels) != 0 {
				t.Errorf("expected no relationships, got %d", len(rels))
			}
		})
	}
}
