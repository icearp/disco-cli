package aws

import (
	"fmt"
	"testing"

	"codeberg.org/icearp/disco/internal/store"
)

func TestResolveEventBridgeRelationships(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	busARN := fmt.Sprintf("arn:aws:events:%s:%s:event-bus/my-bus", testRegion, acct.ID)
	ruleARN := fmt.Sprintf("arn:aws:events:%s:%s:rule/my-bus/my-rule", testRegion, acct.ID)
	lambdaARN := fmt.Sprintf("arn:aws:lambda:%s:%s:function:my-fn", testRegion, acct.ID)
	snsARN := fmt.Sprintf("arn:aws:sns:%s:%s:my-topic", testRegion, acct.ID)

	ruleAttrs := fmt.Sprintf(
		`{"Rule":{"EventBusArn":%q},"Targets":[{"Arn":%q},{"Arn":%q}]}`,
		busARN, lambdaARN, snsARN,
	)

	ruleID := upsertTestResource(t, st, "aws", acct.ID, TypeEventsRule, ruleARN, testRegion, ruleAttrs)
	busID := upsertTestResource(t, st, "aws", acct.ID, TypeEventsEventBus, busARN, testRegion, "{}")
	lambdaID := upsertTestResource(t, st, "aws", acct.ID, TypeLambdaFunction, lambdaARN, testRegion, "{}")
	snsID := upsertTestResource(t, st, "aws", acct.ID, TypeSNSTopic, snsARN, testRegion, "{}")

	if err := resolveEventBridgeRelationships(acct, st); err != nil {
		t.Fatalf("resolveEventBridgeRelationships: %v", err)
	}

	rels, err := st.RelationshipsFrom(ruleID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	assertRelationship(t, rels, ruleID, busID, store.RelAttachedTo)
	assertRelationship(t, rels, ruleID, lambdaID, store.RelRoutesTo)
	assertRelationship(t, rels, ruleID, snsID, store.RelRoutesTo)
}

func TestResolveEventBridgeRelationships_DefaultBus(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	// Default bus: EventBusArn is empty, EventBusName is "default"
	ruleARN := fmt.Sprintf("arn:aws:events:%s:%s:rule/default-rule", testRegion, acct.ID)
	busARN := fmt.Sprintf("arn:aws:events:%s:%s:event-bus/default", testRegion, acct.ID)

	ruleAttrs := `{"Rule":{"EventBusName":"default"},"Targets":[]}`
	ruleID := upsertTestResource(t, st, "aws", acct.ID, TypeEventsRule, ruleARN, testRegion, ruleAttrs)
	busID := upsertTestResource(t, st, "aws", acct.ID, TypeEventsEventBus, busARN, testRegion, "{}")

	if err := resolveEventBridgeRelationships(acct, st); err != nil {
		t.Fatalf("resolveEventBridgeRelationships: %v", err)
	}

	rels, err := st.RelationshipsFrom(ruleID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	assertRelationship(t, rels, ruleID, busID, store.RelAttachedTo)
}

func TestResolveEventBridgeRelationships_NoAttrs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	ruleARN := fmt.Sprintf("arn:aws:events:%s:%s:rule/bare-rule", testRegion, acct.ID)
	ruleID := upsertTestResource(t, st, "aws", acct.ID, TypeEventsRule, ruleARN, testRegion, "{}")

	if err := resolveEventBridgeRelationships(acct, st); err != nil {
		t.Fatalf("resolveEventBridgeRelationships: %v", err)
	}

	rels, err := st.RelationshipsFrom(ruleID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("expected 0 relationships, got %d", len(rels))
	}
}
