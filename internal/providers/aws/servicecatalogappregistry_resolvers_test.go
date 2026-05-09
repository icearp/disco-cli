package aws

import (
	"fmt"
	"testing"

	"codeberg.org/icearp/disco/store"
)

func TestResolveSCARAttributeGroupAssocRefs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	appARN := fmt.Sprintf("arn:aws:servicecatalog:%s:%s:/applications/app-1", testRegion, acct.ID)
	appID := upsertTestResource(t, st, "aws", acct.ID, TypeSCARApplication, appARN, testRegion, "{}")
	agARN := fmt.Sprintf("arn:aws:servicecatalog:%s:%s:/attribute-groups/ag-1", testRegion, acct.ID)
	agID := upsertTestResource(t, st, "aws", acct.ID, TypeSCARAttributeGroup, agARN, testRegion, `{"Id":"ag-1"}`)

	assocARN := appARN + "/attribute-group-association/ag-1"
	assocAttrs := `{"Id":"ag-1"}`
	assocID := upsertTestResource(t, st, "aws", acct.ID, TypeSCARAttributeGroupAssociation, assocARN, testRegion, assocAttrs)

	if err := resolveSCARAttributeGroupAssocRefs(acct, st); err != nil {
		t.Fatalf("resolveSCARAttributeGroupAssocRefs: %v", err)
	}
	rels, _ := st.RelationshipsFrom(assocID)
	assertRelationship(t, rels, assocID, appID, store.RelAttachedTo)
	assertRelationship(t, rels, assocID, agID, store.RelAttachedTo)
}

func TestResolveSCARResourceAssocApplication(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	appARN := fmt.Sprintf("arn:aws:servicecatalog:%s:%s:/applications/app-1", testRegion, acct.ID)
	appID := upsertTestResource(t, st, "aws", acct.ID, TypeSCARApplication, appARN, testRegion, "{}")
	resARN := appARN + "/resource-association/arn:aws:s3:::my-bucket"
	resID := upsertTestResource(t, st, "aws", acct.ID, TypeSCARResourceAssociation, resARN, testRegion, "{}")

	if err := resolveSCARResourceAssocApplication(acct, st); err != nil {
		t.Fatalf("resolveSCARResourceAssocApplication: %v", err)
	}
	rels, _ := st.RelationshipsFrom(resID)
	assertRelationship(t, rels, resID, appID, store.RelAttachedTo)
}
