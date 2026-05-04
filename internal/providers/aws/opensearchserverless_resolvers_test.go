package aws

import (
	"fmt"
	"testing"

	"codeberg.org/icearp/disco/internal/store"
)

func TestResolveOSSCollectionRefs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	keyARN := fmt.Sprintf("arn:aws:kms:%s:%s:key/k-1", testRegion, acct.ID)
	keyID := upsertTestResource(t, st, "aws", acct.ID, TypeKMSKey, keyARN, testRegion, "{}")
	cgARN := fmt.Sprintf("arn:aws:aoss:%s:%s:collection-group/grp1", testRegion, acct.ID)
	cgID := upsertTestResource(t, st, "aws", acct.ID, TypeOSSCollectionGroup, cgARN, testRegion, `{"Name":"grp1"}`)
	cARN := fmt.Sprintf("arn:aws:aoss:%s:%s:collection/c1", testRegion, acct.ID)
	attrs := fmt.Sprintf(`{"KmsKeyArn":"%s","CollectionGroupName":"grp1"}`, keyARN)
	cID := upsertTestResource(t, st, "aws", acct.ID, TypeOSSCollection, cARN, testRegion, attrs)
	if err := resolveOSSCollectionRefs(acct, st); err != nil {
		t.Fatalf("resolveOSSCollectionRefs: %v", err)
	}
	rels, _ := st.RelationshipsFrom(cID)
	assertRelationship(t, rels, cID, keyID, store.RelUses)
	assertRelationship(t, rels, cID, cgID, store.RelAttachedTo)
}
