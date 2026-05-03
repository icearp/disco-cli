package aws

import (
	"fmt"
	"testing"

	"codeberg.org/icearp/disco/internal/store"
)

func TestResolveFDDetectorEventType(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	etARN := fdARN(testRegion, acct.ID, "event-type", "purchase")
	etID := upsertTestResource(t, st, "aws", acct.ID, TypeFraudDetectorEventType, etARN, testRegion, "{}")
	dARN := fdARN(testRegion, acct.ID, "detector", "buyer-detector")
	attrs := `{"EventTypeName":"purchase"}`
	dID := upsertTestResource(t, st, "aws", acct.ID, TypeFraudDetectorDetector, dARN, testRegion, attrs)
	if err := resolveFDDetectorEventType(acct, st); err != nil {
		t.Fatalf("resolveFDDetectorEventType: %v", err)
	}
	rels, _ := st.RelationshipsFrom(dID)
	assertRelationship(t, rels, dID, etID, store.RelUses)
}

func TestResolveFDEventTypeRefs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	enARN := fdARN(testRegion, acct.ID, "entity-type", "buyer")
	enID := upsertTestResource(t, st, "aws", acct.ID, TypeFraudDetectorEntityType, enARN, testRegion, "{}")
	lARN := fdARN(testRegion, acct.ID, "label", "fraud")
	lID := upsertTestResource(t, st, "aws", acct.ID, TypeFraudDetectorLabel, lARN, testRegion, "{}")
	vARN := fdARN(testRegion, acct.ID, "variable", "ip_address")
	vID := upsertTestResource(t, st, "aws", acct.ID, TypeFraudDetectorVariable, vARN, testRegion, "{}")
	etARN := fdARN(testRegion, acct.ID, "event-type", "purchase")
	attrs := fmt.Sprintf(`{"EntityTypes":["buyer"],"Labels":["fraud"],"EventVariables":["ip_address"]}`)
	etID := upsertTestResource(t, st, "aws", acct.ID, TypeFraudDetectorEventType, etARN, testRegion, attrs)
	if err := resolveFDEventTypeRefs(acct, st); err != nil {
		t.Fatalf("resolveFDEventTypeRefs: %v", err)
	}
	rels, _ := st.RelationshipsFrom(etID)
	assertRelationship(t, rels, etID, enID, store.RelUses)
	assertRelationship(t, rels, etID, lID, store.RelUses)
	assertRelationship(t, rels, etID, vID, store.RelUses)
}
