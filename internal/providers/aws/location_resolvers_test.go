package aws

import (
	"fmt"
	"testing"

	"codeberg.org/icearp/disco/store"
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

func TestResolveLocationKMSRefs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	keyARN := fmt.Sprintf("arn:aws:kms:%s:%s:key/abc-123", testRegion, acct.ID)
	keyID := upsertTestResource(t, st, "aws", acct.ID, TypeKMSKey, keyARN, testRegion, fmt.Sprintf(`{"KeyId":"abc-123","Arn":%q}`, keyARN))
	gcARN := locARN(testRegion, acct.ID, "geofence-collection", "g1")
	gcAttrs := fmt.Sprintf(`{"KmsKeyId":%q}`, keyARN)
	gcID := upsertTestResource(t, st, "aws", acct.ID, TypeLocationGeofenceCollection, gcARN, testRegion, gcAttrs)
	tARN := locARN(testRegion, acct.ID, "tracker", "t1")
	tAttrs := fmt.Sprintf(`{"KmsKeyId":%q}`, keyARN)
	tID := upsertTestResource(t, st, "aws", acct.ID, TypeLocationTracker, tARN, testRegion, tAttrs)
	if err := resolveLocationKMSRefs(acct, st); err != nil {
		t.Fatalf("resolveLocationKMSRefs: %v", err)
	}
	for _, src := range []string{gcID, tID} {
		rels, _ := st.RelationshipsFrom(src)
		assertRelationship(t, rels, src, keyID, store.RelUses)
	}
}

func TestResolveLocationAPIKeyResources(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	keyARN := locARN(testRegion, acct.ID, "api-key", "k1")
	tARN := locARN(testRegion, acct.ID, "tracker", "tr1")
	mARN := locARN(testRegion, acct.ID, "map", "m1")
	pARN := locARN(testRegion, acct.ID, "place-index", "p1")
	rcARN := locARN(testRegion, acct.ID, "route-calculator", "rc1")
	gARN := locARN(testRegion, acct.ID, "geofence-collection", "g1")
	attrs := fmt.Sprintf(`{"Restrictions":{"AllowResources":[%q,%q,%q,%q,%q]}}`, tARN, mARN, pARN, rcARN, gARN)

	kID := upsertTestResource(t, st, "aws", acct.ID, TypeLocationAPIKey, keyARN, testRegion, attrs)
	tID := upsertTestResource(t, st, "aws", acct.ID, TypeLocationTracker, tARN, testRegion, "{}")
	mID := upsertTestResource(t, st, "aws", acct.ID, TypeLocationMap, mARN, testRegion, "{}")
	pID := upsertTestResource(t, st, "aws", acct.ID, TypeLocationPlaceIndex, pARN, testRegion, "{}")
	rcID := upsertTestResource(t, st, "aws", acct.ID, TypeLocationRouteCalculator, rcARN, testRegion, "{}")
	gID := upsertTestResource(t, st, "aws", acct.ID, TypeLocationGeofenceCollection, gARN, testRegion, "{}")

	if err := resolveLocationAPIKeyResources(acct, st); err != nil {
		t.Fatalf("resolveLocationAPIKeyResources: %v", err)
	}
	rels, _ := st.RelationshipsFrom(kID)
	for _, target := range []string{tID, mID, pID, rcID, gID} {
		assertRelationship(t, rels, kID, target, store.RelUses)
	}
}
