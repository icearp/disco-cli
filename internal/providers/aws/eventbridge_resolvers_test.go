package aws

import (
	"fmt"
	"testing"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	eventstypes "github.com/aws/aws-sdk-go-v2/service/eventbridge/types"
	"github.com/icearp/disco-cli/store"
)

func TestResolveEventBridgeRelationships(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	busARN := fmt.Sprintf("arn:aws:events:%s:%s:event-bus/my-bus", testRegion, acct.ID)
	ruleARN := fmt.Sprintf("arn:aws:events:%s:%s:rule/my-bus/my-rule", testRegion, acct.ID)
	lambdaARN := fmt.Sprintf("arn:aws:lambda:%s:%s:function:my-fn", testRegion, acct.ID)
	snsARN := fmt.Sprintf("arn:aws:sns:%s:%s:my-topic", testRegion, acct.ID)

	// SDK Rule has no EventBusArn field — resolver synthesizes the bus ARN from
	// EventBusName+region+account. Fixture mirrors production shape (EventBusName only).
	ruleAttrs := eventBridgeRuleAttrs(
		eventstypes.Rule{EventBusName: sdkaws.String("my-bus")},
		eventstypes.Target{Arn: sdkaws.String(lambdaARN)},
		eventstypes.Target{Arn: sdkaws.String(snsARN)},
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

func TestResolveEventBridgeRelationships_SFNAndFirehoseTargets(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	busARN := fmt.Sprintf("arn:aws:events:%s:%s:event-bus/default", testRegion, acct.ID)
	ruleARN := fmt.Sprintf("arn:aws:events:%s:%s:rule/r", testRegion, acct.ID)
	smARN := fmt.Sprintf("arn:aws:states:%s:%s:stateMachine:wf", testRegion, acct.ID)
	fhARN := fmt.Sprintf("arn:aws:firehose:%s:%s:deliverystream/ds", testRegion, acct.ID)

	_ = busARN
	ruleAttrs := eventBridgeRuleAttrs(
		eventstypes.Rule{EventBusName: sdkaws.String("default")},
		eventstypes.Target{Arn: sdkaws.String(smARN)},
		eventstypes.Target{Arn: sdkaws.String(fhARN)},
	)
	ruleID := upsertTestResource(t, st, "aws", acct.ID, TypeEventsRule, ruleARN, testRegion, ruleAttrs)
	_ = upsertTestResource(t, st, "aws", acct.ID, TypeEventsEventBus, busARN, testRegion, "{}")
	smID := upsertTestResource(t, st, "aws", acct.ID, TypeSFNStateMachine, smARN, testRegion, "{}")
	fhID := upsertTestResource(t, st, "aws", acct.ID, TypeFirehoseDeliveryStream, fhARN, testRegion, "{}")

	if err := resolveEventBridgeRelationships(acct, st); err != nil {
		t.Fatalf("resolveEventBridgeRelationships: %v", err)
	}
	rels, err := st.RelationshipsFrom(ruleID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	assertRelationship(t, rels, ruleID, smID, store.RelRoutesTo)
	assertRelationship(t, rels, ruleID, fhID, store.RelRoutesTo)
}

func TestResolveEventBridgeRelationships_DefaultBus(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	// Default bus: EventBusArn is empty, EventBusName is "default"
	ruleARN := fmt.Sprintf("arn:aws:events:%s:%s:rule/default-rule", testRegion, acct.ID)
	busARN := fmt.Sprintf("arn:aws:events:%s:%s:event-bus/default", testRegion, acct.ID)

	ruleAttrs := eventBridgeRuleAttrs(eventstypes.Rule{EventBusName: sdkaws.String("default")})
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

func TestResolveEventBridgeRelationships_APIDestinationTarget(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	busARN := fmt.Sprintf("arn:aws:events:%s:%s:event-bus/default", testRegion, acct.ID)
	ruleARN := fmt.Sprintf("arn:aws:events:%s:%s:rule/r", testRegion, acct.ID)
	destARN := fmt.Sprintf("arn:aws:events:%s:%s:api-destination/foo/uuid", testRegion, acct.ID)

	_ = busARN
	ruleAttrs := eventBridgeRuleAttrs(
		eventstypes.Rule{EventBusName: sdkaws.String("default")},
		eventstypes.Target{Arn: sdkaws.String(destARN)},
	)
	ruleID := upsertTestResource(t, st, "aws", acct.ID, TypeEventsRule, ruleARN, testRegion, ruleAttrs)
	_ = upsertTestResource(t, st, "aws", acct.ID, TypeEventsEventBus, busARN, testRegion, "{}")
	destID := upsertTestResource(t, st, "aws", acct.ID, TypeEventsAPIDestination, destARN, testRegion, "{}")

	if err := resolveEventBridgeRelationships(acct, st); err != nil {
		t.Fatalf("resolveEventBridgeRelationships: %v", err)
	}
	rels, err := st.RelationshipsFrom(ruleID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	assertRelationship(t, rels, ruleID, destID, store.RelRoutesTo)
}

func TestResolveEventBridgeAPIDestinationConnection(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	connARN := fmt.Sprintf("arn:aws:events:%s:%s:connection/c/uuid", testRegion, acct.ID)
	destARN := fmt.Sprintf("arn:aws:events:%s:%s:api-destination/d/uuid", testRegion, acct.ID)

	connID := upsertTestResource(t, st, "aws", acct.ID, TypeEventsConnection, connARN, testRegion, "{}")
	destAttrs := fmt.Sprintf(`{"ConnectionArn":%q}`, connARN)
	destID := upsertTestResource(t, st, "aws", acct.ID, TypeEventsAPIDestination, destARN, testRegion, destAttrs)

	if err := resolveEventBridgeAPIDestinationConnection(acct, st); err != nil {
		t.Fatalf("resolveEventBridgeAPIDestinationConnection: %v", err)
	}
	rels, err := st.RelationshipsFrom(destID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	assertRelationship(t, rels, destID, connID, store.RelUses)
}

func TestResolveEventBridgeAPIDestinationConnection_Missing(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	// ConnectionArn points at a connection that was never scanned (cross-account, etc.).
	missingConn := fmt.Sprintf("arn:aws:events:%s:999999999999:connection/x/uuid", testRegion)
	destARN := fmt.Sprintf("arn:aws:events:%s:%s:api-destination/d/uuid", testRegion, acct.ID)
	destAttrs := fmt.Sprintf(`{"ConnectionArn":%q}`, missingConn)
	destID := upsertTestResource(t, st, "aws", acct.ID, TypeEventsAPIDestination, destARN, testRegion, destAttrs)

	if err := resolveEventBridgeAPIDestinationConnection(acct, st); err != nil {
		t.Fatalf("resolveEventBridgeAPIDestinationConnection: %v", err)
	}
	rels, err := st.RelationshipsFrom(destID)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	if len(rels) != 0 {
		t.Errorf("expected no edge for missing connection, got %d", len(rels))
	}
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

func TestResolveEventBridgeBusRefs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	busARN := "arn:aws:events:us-east-1:" + testAccountID + ":event-bus/my-bus"
	keyARN := "arn:aws:kms:us-east-1:" + testAccountID + ":key/k-bus"
	dlqARN := "arn:aws:sqs:us-east-1:" + testAccountID + ":dlq"
	attrs := `{"KmsKeyIdentifier":"` + keyARN + `","DeadLetterConfig":{"Arn":"` + dlqARN + `"}}`

	bID := upsertTestResource(t, st, "aws", acct.ID, TypeEventsEventBus, busARN, testRegion, attrs)
	kID := upsertTestResource(t, st, "aws", acct.ID, TypeKMSKey, keyARN, testRegion, "{}")
	qID := upsertTestResource(t, st, "aws", acct.ID, TypeSQSQueue, dlqARN, testRegion, "{}")

	if err := resolveEventBridgeBusRefs(acct, st); err != nil {
		t.Fatalf("resolveEventBridgeBusRefs: %v", err)
	}
	rels, _ := st.RelationshipsFrom(bID)
	assertRelationship(t, rels, bID, kID, store.RelUses)
	assertRelationship(t, rels, bID, qID, store.RelRoutesTo)
}

func TestResolveEventBridgeConnectionRefs(t *testing.T) {
	st := newTestStore(t)
	acct := newTestAccount(testAccountID)

	connARN := "arn:aws:events:us-east-1:" + testAccountID + ":connection/c1/abc"
	keyARN := "arn:aws:kms:us-east-1:" + testAccountID + ":key/k-conn"
	secARN := "arn:aws:secretsmanager:us-east-1:" + testAccountID + ":secret:eb-conn-abc"
	attrs := `{"KmsKeyIdentifier":"` + keyARN + `","SecretArn":"` + secARN + `"}`

	cID := upsertTestResource(t, st, "aws", acct.ID, TypeEventsConnection, connARN, testRegion, attrs)
	kID := upsertTestResource(t, st, "aws", acct.ID, TypeKMSKey, keyARN, testRegion, "{}")
	sID := upsertTestResource(t, st, "aws", acct.ID, TypeSecretsManagerSecret, secARN, testRegion, "{}")

	if err := resolveEventBridgeConnectionRefs(acct, st); err != nil {
		t.Fatalf("resolveEventBridgeConnectionRefs: %v", err)
	}
	rels, _ := st.RelationshipsFrom(cID)
	assertRelationship(t, rels, cID, kID, store.RelUses)
	assertRelationship(t, rels, cID, sID, store.RelUses)
}
