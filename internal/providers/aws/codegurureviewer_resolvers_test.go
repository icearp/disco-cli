package aws

import (
	"testing"

	"codeberg.org/icearp/disco/internal/store"
)

func TestResolveCodeGuruReviewerAssociationRefs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	assocARN := "arn:aws:codeguru-reviewer:us-east-1:" + testAccountID + ":association:abc"
	connARN := "arn:aws:codestar-connections:us-east-1:" + testAccountID + ":connection/abcd"
	attrs := `{"ConnectionArn":"` + connARN + `"}`

	aID := upsertTestResource(t, st, "aws", acct.ID, TypeCodeGuruReviewerRepositoryAssociation, assocARN, testRegion, attrs)
	cID := upsertTestResource(t, st, "aws", acct.ID, TypeCodeStarConnectionsConnection, connARN, testRegion, "{}")

	if err := resolveCodeGuruReviewerAssociationRefs(acct, st); err != nil {
		t.Fatalf("resolveCodeGuruReviewerAssociationRefs: %v", err)
	}
	rels, _ := st.RelationshipsFrom(aID)
	assertRelationship(t, rels, aID, cID, store.RelUses)
}
