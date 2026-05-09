package aws

import (
	"fmt"
	"testing"

	"codeberg.org/icearp/disco/store"
)

func TestResolveCleanRoomsMembershipCollaboration(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	cARN := fmt.Sprintf("arn:aws:cleanrooms:%s:%s:collaboration/c1", testRegion, acct.ID)
	cID := upsertTestResource(t, st, "aws", acct.ID, TypeCleanRoomsCollaboration, cARN, testRegion, "{}")
	mARN := fmt.Sprintf("arn:aws:cleanrooms:%s:%s:membership/m1", testRegion, acct.ID)
	attrs := fmt.Sprintf(`{"CollaborationArn":%q}`, cARN)
	mID := upsertTestResource(t, st, "aws", acct.ID, TypeCleanRoomsMembership, mARN, testRegion, attrs)
	if err := resolveCleanRoomsMembershipCollaboration(acct, st); err != nil {
		t.Fatalf("resolveCleanRoomsMembershipCollaboration: %v", err)
	}
	rels, _ := st.RelationshipsFrom(mID)
	assertRelationship(t, rels, mID, cID, store.RelAttachedTo)
}

func TestResolveCleanRoomsChildToMembership(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	cARN := fmt.Sprintf("arn:aws:cleanrooms:%s:%s:collaboration/c1", testRegion, acct.ID)
	cID := upsertTestResource(t, st, "aws", acct.ID, TypeCleanRoomsCollaboration, cARN, testRegion, "{}")
	mARN := fmt.Sprintf("arn:aws:cleanrooms:%s:%s:membership/m1", testRegion, acct.ID)
	mID := upsertTestResource(t, st, "aws", acct.ID, TypeCleanRoomsMembership, mARN, testRegion, "{}")
	atARN := fmt.Sprintf("arn:aws:cleanrooms:%s:%s:membership/m1/analysis-template/at1", testRegion, acct.ID)
	attrs := fmt.Sprintf(`{"MembershipArn":%q,"CollaborationArn":%q}`, mARN, cARN)
	atID := upsertTestResource(t, st, "aws", acct.ID, TypeCleanRoomsAnalysisTemplate, atARN, testRegion, attrs)
	if err := resolveCleanRoomsChildToMembership(acct, st); err != nil {
		t.Fatalf("resolveCleanRoomsChildToMembership: %v", err)
	}
	rels, _ := st.RelationshipsFrom(atID)
	assertRelationship(t, rels, atID, mID, store.RelAttachedTo)
	assertRelationship(t, rels, atID, cID, store.RelAttachedTo)
}

func TestResolveCleanRoomsConfiguredTableAssocToTable(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	tARN := fmt.Sprintf("arn:aws:cleanrooms:%s:%s:configuredtable/t1", testRegion, acct.ID)
	tID := upsertTestResource(t, st, "aws", acct.ID, TypeCleanRoomsConfiguredTable, tARN, testRegion, "{}")
	aARN := fmt.Sprintf("arn:aws:cleanrooms:%s:%s:membership/m1/configuredtableassociation/cta1", testRegion, acct.ID)
	attrs := fmt.Sprintf(`{"ConfiguredTableArn":%q}`, tARN)
	aID := upsertTestResource(t, st, "aws", acct.ID, TypeCleanRoomsConfiguredTableAssociation, aARN, testRegion, attrs)
	if err := resolveCleanRoomsConfiguredTableAssocToTable(acct, st); err != nil {
		t.Fatalf("resolveCleanRoomsConfiguredTableAssocToTable: %v", err)
	}
	rels, _ := st.RelationshipsFrom(aID)
	assertRelationship(t, rels, aID, tID, store.RelAttachedTo)
}
