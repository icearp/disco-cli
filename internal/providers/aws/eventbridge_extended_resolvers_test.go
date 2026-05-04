package aws

import (
	"fmt"
	"testing"

	"codeberg.org/icearp/disco/internal/store"
)

func TestResolveEBArchiveBus(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	bARN := fmt.Sprintf("arn:aws:events:%s:%s:event-bus/default", testRegion, acct.ID)
	bID := upsertTestResource(t, st, "aws", acct.ID, TypeEventsEventBus, bARN, testRegion, "{}")
	aARN := fmt.Sprintf("arn:aws:events:%s:%s:archive/myarchive", testRegion, acct.ID)
	aID := upsertTestResource(t, st, "aws", acct.ID, TypeEventsArchive, aARN, testRegion,
		fmt.Sprintf(`{"EventSourceArn":"%s"}`, bARN))
	if err := resolveEBArchiveBus(acct, st); err != nil {
		t.Fatalf("resolveEBArchiveBus: %v", err)
	}
	rels, _ := st.RelationshipsFrom(aID)
	assertRelationship(t, rels, aID, bID, store.RelAttachedTo)
}

func TestResolveEBEndpointBuses(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	bARN := fmt.Sprintf("arn:aws:events:%s:%s:event-bus/default", testRegion, acct.ID)
	bID := upsertTestResource(t, st, "aws", acct.ID, TypeEventsEventBus, bARN, testRegion, "{}")
	roleARN := fmt.Sprintf("arn:aws:iam::%s:role/eb-role", acct.ID)
	roleID := upsertTestResource(t, st, "aws", acct.ID, TypeIAMRole, roleARN, "", "{}")
	eARN := fmt.Sprintf("arn:aws:events:%s:%s:endpoint/ep1", testRegion, acct.ID)
	attrs := fmt.Sprintf(`{"EventBuses":[{"EventBusArn":"%s"}],"RoleArn":"%s"}`, bARN, roleARN)
	eID := upsertTestResource(t, st, "aws", acct.ID, TypeEventsEndpoint, eARN, testRegion, attrs)
	if err := resolveEBEndpointBuses(acct, st); err != nil {
		t.Fatalf("resolveEBEndpointBuses: %v", err)
	}
	rels, _ := st.RelationshipsFrom(eID)
	assertRelationship(t, rels, eID, bID, store.RelAttachedTo)
	assertRelationship(t, rels, eID, roleID, store.RelUses)
}

func TestResolveEBEventBusPolicyToBus(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	bARN := fmt.Sprintf("arn:aws:events:%s:%s:event-bus/default", testRegion, acct.ID)
	bID := upsertTestResource(t, st, "aws", acct.ID, TypeEventsEventBus, bARN, testRegion, "{}")
	pARN := bARN + "/policy"
	pID := upsertTestResource(t, st, "aws", acct.ID, TypeEventsEventBusPolicy, pARN, testRegion, "{}")
	if err := resolveEBEventBusPolicyToBus(acct, st); err != nil {
		t.Fatalf("resolveEBEventBusPolicyToBus: %v", err)
	}
	rels, _ := st.RelationshipsFrom(pID)
	assertRelationship(t, rels, pID, bID, store.RelAttachedTo)
}
