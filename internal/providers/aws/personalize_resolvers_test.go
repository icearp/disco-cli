package aws

import (
	"testing"

	"codeberg.org/icearp/disco/store"
)

func TestResolvePersonalizeChildrenToDatasetGroup(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	dgARN := "arn:aws:personalize:" + testRegion + ":" + acct.ID + ":dataset-group/dg1"
	dgID := upsertTestResource(t, st, "aws", acct.ID, TypePersonalizeDatasetGroup, dgARN, testRegion, "{}")

	filterARN := "arn:aws:personalize:" + testRegion + ":" + acct.ID + ":filter/f1"
	fID := upsertTestResource(t, st, "aws", acct.ID, TypePersonalizeFilter, filterARN, testRegion,
		`{"FilterArn":"`+filterARN+`","DatasetGroupArn":"`+dgARN+`"}`)

	recARN := "arn:aws:personalize:" + testRegion + ":" + acct.ID + ":recommender/r1"
	rID := upsertTestResource(t, st, "aws", acct.ID, TypePersonalizeRecommender, recARN, testRegion,
		`{"RecommenderArn":"`+recARN+`","DatasetGroupArn":"`+dgARN+`"}`)

	if err := resolvePersonalizeChildrenToDatasetGroup(acct, st); err != nil {
		t.Fatalf("resolvePersonalizeChildrenToDatasetGroup: %v", err)
	}

	frels, _ := st.RelationshipsFrom(fID)
	assertRelationship(t, frels, fID, dgID, store.RelAttachedTo)
	rrels, _ := st.RelationshipsFrom(rID)
	assertRelationship(t, rrels, rID, dgID, store.RelAttachedTo)
}

func TestResolvePersonalizeChildrenToDatasetGroup_NoAttrs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	dgARN := "arn:aws:personalize:" + testRegion + ":" + acct.ID + ":dataset-group/dg1"
	upsertTestResource(t, st, "aws", acct.ID, TypePersonalizeDatasetGroup, dgARN, testRegion, "{}")

	filterARN := "arn:aws:personalize:" + testRegion + ":" + acct.ID + ":filter/f1"
	fID := upsertTestResource(t, st, "aws", acct.ID, TypePersonalizeFilter, filterARN, testRegion, "{}")

	if err := resolvePersonalizeChildrenToDatasetGroup(acct, st); err != nil {
		t.Fatalf("resolvePersonalizeChildrenToDatasetGroup: %v", err)
	}
	if rels, _ := st.RelationshipsFrom(fID); len(rels) != 0 {
		t.Errorf("expected no edges for filter without DatasetGroupArn, got %d", len(rels))
	}
}
