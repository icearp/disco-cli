package aws

import (
	"fmt"
	"testing"

	"codeberg.org/icearp/disco/internal/store"
)

func TestResolveLocationTrackerConsumerRefs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	tName := "fleet-tracker"
	tARN := locARN(testRegion, acct.ID, "tracker", tName)
	tID := upsertTestResource(t, st, "aws", acct.ID, TypeLocationTracker, tARN, testRegion, "{}")
	gcARN := locARN(testRegion, acct.ID, "geofence-collection", "yard")
	gcID := upsertTestResource(t, st, "aws", acct.ID, TypeLocationGeofenceCollection, gcARN, testRegion, "{}")
	tcARN := tARN + "/consumer/yard"
	tcAttrs := fmt.Sprintf(`{"TrackerName":%q,"ConsumerArn":%q}`, tName, gcARN)
	tcID := upsertTestResource(t, st, "aws", acct.ID, TypeLocationTrackerConsumer, tcARN, testRegion, tcAttrs)
	if err := resolveLocationTrackerConsumerRefs(acct, st); err != nil {
		t.Fatalf("resolveLocationTrackerConsumerRefs: %v", err)
	}
	rels, _ := st.RelationshipsFrom(tcID)
	assertRelationship(t, rels, tcID, tID, store.RelAttachedTo)
	assertRelationship(t, rels, tcID, gcID, store.RelUses)
}
