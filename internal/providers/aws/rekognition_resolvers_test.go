package aws

import (
	"fmt"
	"testing"

	"github.com/icearp/disco-cli/store"
)

func TestResolveRekProjectChildren(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	projARN := fmt.Sprintf("arn:aws:rekognition:%s:%s:project/myproj/1700000000", testRegion, acct.ID)
	projID := upsertTestResource(t, st, "aws", acct.ID, TypeRekognitionProject, projARN, testRegion, "{}")

	pvARN := fmt.Sprintf("arn:aws:rekognition:%s:%s:project/myproj/version/v1/1700000001", testRegion, acct.ID)
	pvID := upsertTestResource(t, st, "aws", acct.ID, TypeRekognitionProjectVersion, pvARN, testRegion,
		fmt.Sprintf(`{"ProjectVersionArn":"%s","ProjectArn":"%s"}`, pvARN, projARN))

	dsARN := fmt.Sprintf("arn:aws:rekognition:%s:%s:project/myproj/dataset/train/1700000002", testRegion, acct.ID)
	dsID := upsertTestResource(t, st, "aws", acct.ID, TypeRekognitionDataset, dsARN, testRegion,
		fmt.Sprintf(`{"DatasetArn":"%s","ProjectArn":"%s"}`, dsARN, projARN))

	if err := resolveRekProjectChildren(acct, st); err != nil {
		t.Fatalf("resolveRekProjectChildren: %v", err)
	}
	pvRels, _ := st.RelationshipsFrom(pvID)
	assertRelationship(t, pvRels, pvID, projID, store.RelAttachedTo)
	dsRels, _ := st.RelationshipsFrom(dsID)
	assertRelationship(t, dsRels, dsID, projID, store.RelAttachedTo)
}

func TestResolveRekProjectChildren_NoAttrs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	pvARN := fmt.Sprintf("arn:aws:rekognition:%s:%s:project/myproj/version/v1/1700000001", testRegion, acct.ID)
	pvID := upsertTestResource(t, st, "aws", acct.ID, TypeRekognitionProjectVersion, pvARN, testRegion, "{}")
	if err := resolveRekProjectChildren(acct, st); err != nil {
		t.Fatalf("resolveRekProjectChildren (no attrs): %v", err)
	}
	rels, _ := st.RelationshipsFrom(pvID)
	if len(rels) != 0 {
		t.Fatalf("expected no relationships, got %d", len(rels))
	}
}
