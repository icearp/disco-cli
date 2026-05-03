package aws

import (
	"fmt"
	"testing"

	"codeberg.org/icearp/disco/internal/store"
)

const testConnectInstance = "i123"

func connectQueueARN(region, acct, instance, id string) string {
	return fmt.Sprintf("arn:aws:connect:%s:%s:instance/%s/queue/%s", region, acct, instance, id)
}

func connectHopARN(region, acct, instance, id string) string {
	return fmt.Sprintf("arn:aws:connect:%s:%s:instance/%s/hours-of-operation/%s", region, acct, instance, id)
}

func connectFlowARN(region, acct, instance, id string) string {
	return fmt.Sprintf("arn:aws:connect:%s:%s:instance/%s/contact-flow/%s", region, acct, instance, id)
}

func connectProfileARN(region, acct, instance, id string) string {
	return fmt.Sprintf("arn:aws:connect:%s:%s:instance/%s/routing-profile/%s", region, acct, instance, id)
}

func TestConnectInstanceIDFromARN(t *testing.T) {
	got := connectInstanceIDFromARN(connectQueueARN("us-east-1", "111", "inst-A", "q-1"))
	if got != "inst-A" {
		t.Fatalf("expected inst-A, got %q", got)
	}
	if connectInstanceIDFromARN("arn:aws:connect:us-east-1:111:phone-number/p-1") != "" {
		t.Fatal("phone-number ARN should not contain instance segment")
	}
}

func TestResolveConnectQueueRefs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	hopID := upsertTestResource(t, st, "aws", acct.ID, TypeConnectHoursOfOperation,
		connectHopARN(testRegion, acct.ID, testConnectInstance, "h-1"), testRegion, "{}")
	phoneARN := connectAccountResourceARN(testRegion, acct.ID, "phone-number", "p-1")
	phoneID := upsertTestResource(t, st, "aws", acct.ID, TypeConnectPhoneNumber, phoneARN, testRegion, "{}")
	flowID := upsertTestResource(t, st, "aws", acct.ID, TypeConnectContactFlow,
		connectFlowARN(testRegion, acct.ID, testConnectInstance, "f-1"), testRegion, "{}")

	queueARN := connectQueueARN(testRegion, acct.ID, testConnectInstance, "q-1")
	attrs := `{"Queue":{
		"HoursOfOperationId":"h-1",
		"OutboundCallerConfig":{"OutboundCallerIdNumberId":"p-1","OutboundFlowId":"f-1"}
	}}`
	queueID := upsertTestResource(t, st, "aws", acct.ID, TypeConnectQueue, queueARN, testRegion, attrs)

	if err := resolveConnectQueueRefs(acct, st); err != nil {
		t.Fatalf("resolveConnectQueueRefs: %v", err)
	}
	rels, err := st.RelationshipsFrom(queueID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 3 {
		t.Fatalf("expected 3 edges, got %d", len(rels))
	}
	assertRelationship(t, rels, queueID, hopID, store.RelAttachedTo)
	assertRelationship(t, rels, queueID, phoneID, store.RelUses)
	assertRelationship(t, rels, queueID, flowID, store.RelUses)
}

func TestResolveConnectQueueRefs_EmptyAttrs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	queueID := upsertTestResource(t, st, "aws", acct.ID, TypeConnectQueue,
		connectQueueARN(testRegion, acct.ID, testConnectInstance, "q-bare"), testRegion, "{}")
	if err := resolveConnectQueueRefs(acct, st); err != nil {
		t.Fatalf("resolveConnectQueueRefs: %v", err)
	}
	rels, _ := st.RelationshipsFrom(queueID)
	if len(rels) != 0 {
		t.Errorf("expected 0 edges, got %d", len(rels))
	}
}

func TestResolveConnectRoutingProfileRefs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)
	q1 := upsertTestResource(t, st, "aws", acct.ID, TypeConnectQueue,
		connectQueueARN(testRegion, acct.ID, testConnectInstance, "q-1"), testRegion, "{}")
	q2 := upsertTestResource(t, st, "aws", acct.ID, TypeConnectQueue,
		connectQueueARN(testRegion, acct.ID, testConnectInstance, "q-2"), testRegion, "{}")

	profARN := connectProfileARN(testRegion, acct.ID, testConnectInstance, "rp-1")
	attrs := `{"RoutingProfile":{
		"AssociatedQueueIds":["q-1","q-2"],
		"DefaultOutboundQueueId":"q-1"
	}}`
	profID := upsertTestResource(t, st, "aws", acct.ID, TypeConnectRoutingProfile, profARN, testRegion, attrs)

	if err := resolveConnectRoutingProfileRefs(acct, st); err != nil {
		t.Fatalf("resolveConnectRoutingProfileRefs: %v", err)
	}
	rels, err := st.RelationshipsFrom(profID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 2 {
		t.Fatalf("expected 2 edges (deduped q-1), got %d", len(rels))
	}
	assertRelationship(t, rels, profID, q1, store.RelAttachedTo)
	assertRelationship(t, rels, profID, q2, store.RelAttachedTo)
}
