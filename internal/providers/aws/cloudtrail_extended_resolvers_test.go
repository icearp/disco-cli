package aws

import (
	"fmt"
	"testing"

	"codeberg.org/icearp/disco/internal/store"
)

func TestResolveCloudTrailResourcePolicyToParent(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	tARN := fmt.Sprintf("arn:aws:cloudtrail:%s:%s:trail/main", testRegion, acct.ID)
	tID := upsertTestResource(t, st, "aws", acct.ID, TypeCloudTrailTrail, tARN, testRegion, "{}")
	rpTrailID := upsertTestResource(t, st, "aws", acct.ID, TypeCloudTrailResourcePolicy, tARN+"/policy", testRegion, "{}")

	edsARN := fmt.Sprintf("arn:aws:cloudtrail:%s:%s:eventdatastore/uuid-1", testRegion, acct.ID)
	edsID := upsertTestResource(t, st, "aws", acct.ID, TypeCloudTrailEventDataStore, edsARN, testRegion, "{}")
	rpEDSID := upsertTestResource(t, st, "aws", acct.ID, TypeCloudTrailResourcePolicy, edsARN+"/policy", testRegion, "{}")

	chARN := fmt.Sprintf("arn:aws:cloudtrail:%s:%s:channel/c1", testRegion, acct.ID)
	chID := upsertTestResource(t, st, "aws", acct.ID, TypeCloudTrailChannel, chARN, testRegion, "{}")
	rpChID := upsertTestResource(t, st, "aws", acct.ID, TypeCloudTrailResourcePolicy, chARN+"/policy", testRegion, "{}")

	if err := resolveCloudTrailResourcePolicyToParent(acct, st); err != nil {
		t.Fatalf("resolveCloudTrailResourcePolicyToParent: %v", err)
	}
	rels, _ := st.RelationshipsFrom(rpTrailID)
	assertRelationship(t, rels, rpTrailID, tID, store.RelAttachedTo)
	rels, _ = st.RelationshipsFrom(rpEDSID)
	assertRelationship(t, rels, rpEDSID, edsID, store.RelAttachedTo)
	rels, _ = st.RelationshipsFrom(rpChID)
	assertRelationship(t, rels, rpChID, chID, store.RelAttachedTo)
}
