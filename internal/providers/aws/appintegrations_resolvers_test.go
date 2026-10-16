package aws

import (
	"testing"

	sdkaws "github.com/aws/aws-sdk-go-v2/aws"
	appintegrationstypes "github.com/aws/aws-sdk-go-v2/service/appintegrations/types"
	eventstypes "github.com/aws/aws-sdk-go-v2/service/eventbridge/types"
	"github.com/icearp/disco-cli/store"
)

const (
	appintResPartnerBus   = "aws.partner/salesforce.com/123/orders"
	appintResRuleOnBus    = "arn:aws:events:us-east-1:123456789012:rule/" + appintResPartnerBus + "/orders-rule"
	appintResRuleDefault  = "arn:aws:events:us-east-1:123456789012:rule/orders-rule"
	appintResRuleOtherReg = "arn:aws:events:us-west-2:123456789012:rule/" + appintResPartnerBus + "/orders-rule"
)

func appintEventIntegrationAttrs(name, bus string) string {
	return mustJSON(appintegrationstypes.EventIntegration{
		EventIntegrationArn: sdkaws.String(appintTestPrefix + "event-integration/" + name),
		Name:                sdkaws.String(name), EventBridgeBus: sdkaws.String(bus),
	})
}

func appintEventAssocAttrs(integration, rule string) string {
	return mustJSON(appintegrationstypes.EventIntegrationAssociation{
		EventIntegrationAssociationArn: sdkaws.String(appintTestEIAssoc),
		EventIntegrationName:           sdkaws.String(integration), EventBridgeRuleName: sdkaws.String(rule),
	})
}

func appintRuleAttrs(bus, name string) string {
	return eventBridgeRuleAttrs(eventstypes.Rule{Name: sdkaws.String(name), EventBusName: sdkaws.String(bus)})
}

// seedAIRules stores the rule the association names on the integration's bus,
// plus a same-named rule on the default bus and one in another region.
func seedAIRules(t *testing.T, st *store.Store) {
	t.Helper()
	upsertTestResource(t, st, "aws", testAccountID, TypeEventsRule, appintResRuleOnBus, "us-east-1", appintRuleAttrs(appintResPartnerBus, "orders-rule"))
	upsertTestResource(t, st, "aws", testAccountID, TypeEventsRule, appintResRuleDefault, "us-east-1", appintRuleAttrs("default", "orders-rule"))
	upsertTestResource(t, st, "aws", testAccountID, TypeEventsRule, appintResRuleOtherReg, "us-west-2", appintRuleAttrs(appintResPartnerBus, "orders-rule"))
}

func TestResolveAppIntegrationsAssociationRules_RuleOnIntegrationBus(t *testing.T) {
	for _, tc := range []struct {
		region, integration, assoc, wantRule string
	}{
		{"us-east-1", appintTestEI, appintTestEIAssoc, appintResRuleOnBus},
		{
			"us-west-2", "arn:aws:app-integrations:us-west-2:123456789012:event-integration/orders",
			"arn:aws:app-integrations:us-west-2:123456789012:event-integration-association/orders/e2", appintResRuleOtherReg,
		},
	} {
		t.Run(tc.region, func(t *testing.T) {
			st := newTestStore(t)
			seedAIRules(t, st)
			upsertTestResource(t, st, "aws", testAccountID, TypeAppIntegrationsEventIntegration, tc.integration, tc.region, appintEventIntegrationAttrs("orders", appintResPartnerBus))
			assocID := upsertTestResource(t, st, "aws", testAccountID, TypeAppIntegrationsEventIntegrationAssociation, tc.assoc, tc.region, appintEventAssocAttrs("orders", "orders-rule"))

			if err := resolveAppIntegrationsAssociationRules(newTestAccount(testAccountID), st); err != nil {
				t.Fatalf("resolve: %v", err)
			}
			rels, err := st.RelationshipsFrom(assocID, store.RelUses)
			if err != nil {
				t.Fatalf("RelationshipsFrom: %v", err)
			}
			assertRelationship(t, rels, assocID, store.ResourceID("aws", testAccountID, tc.wantRule), store.RelUses)
			if len(rels) != 1 {
				t.Errorf("edges = %d (%+v), want only the rule on the integration's bus in its region", len(rels), rels)
			}
		})
	}
}

// A rule whose EventBusName is the bus ARN still matches the integration's bus
// name.
func TestResolveAppIntegrationsAssociationRules_RuleBusAsARN(t *testing.T) {
	st := newTestStore(t)
	busARN := "arn:aws:events:us-east-1:123456789012:event-bus/" + appintResPartnerBus
	upsertTestResource(t, st, "aws", testAccountID, TypeEventsRule, appintResRuleOnBus, "us-east-1", appintRuleAttrs(busARN, "orders-rule"))
	upsertTestResource(t, st, "aws", testAccountID, TypeAppIntegrationsEventIntegration, appintTestEI, "us-east-1", appintEventIntegrationAttrs("orders", appintResPartnerBus))
	assocID := upsertTestResource(t, st, "aws", testAccountID, TypeAppIntegrationsEventIntegrationAssociation, appintTestEIAssoc, "us-east-1", appintEventAssocAttrs("orders", "orders-rule"))
	if err := resolveAppIntegrationsAssociationRules(newTestAccount(testAccountID), st); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	rels, err := st.RelationshipsFrom(assocID, store.RelUses)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	assertRelationship(t, rels, assocID, store.ResourceID("aws", testAccountID, appintResRuleOnBus), store.RelUses)
}

func TestEventBusNameOf(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"default", "default"},
		{appintResPartnerBus, appintResPartnerBus},
		{"arn:aws:events:us-east-1:123456789012:event-bus/default", "default"},
		{"arn:aws:events:us-east-1:123456789012:event-bus/" + appintResPartnerBus, appintResPartnerBus},
		{"arn:aws:events:us-east-1:123456789012:event-bus/team/event-bus/x", "team/event-bus/x"},
	} {
		if got := eventBusNameOf(tc.in); got != tc.want {
			t.Errorf("eventBusNameOf(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// An association whose integration, bus or rule is not scanned, or whose
// attributes carry no names, emits no edge and no error.
func TestResolveAppIntegrationsAssociationRules_NoTarget(t *testing.T) {
	for _, tc := range []struct {
		name, integrationAttrs, assocAttrs string
	}{
		{"no attrs", "{}", "{}"},
		{"integration not scanned", appintEventIntegrationAttrs("other", appintResPartnerBus), appintEventAssocAttrs("orders", "orders-rule")},
		{"integration without bus", appintEventIntegrationAttrs("orders", ""), appintEventAssocAttrs("orders", "orders-rule")},
		{"rule not scanned", appintEventIntegrationAttrs("orders", appintResPartnerBus), appintEventAssocAttrs("orders", "missing-rule")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			seedAIRules(t, st)
			upsertTestResource(t, st, "aws", testAccountID, TypeAppIntegrationsEventIntegration, appintTestEI, "us-east-1", tc.integrationAttrs)
			assocID := upsertTestResource(t, st, "aws", testAccountID, TypeAppIntegrationsEventIntegrationAssociation, appintTestEIAssoc, "us-east-1", tc.assocAttrs)
			if err := resolveAppIntegrationsAssociationRules(newTestAccount(testAccountID), st); err != nil {
				t.Fatalf("resolve: %v", err)
			}
			rels, err := st.RelationshipsFrom(assocID, store.RelUses)
			if err != nil {
				t.Fatalf("RelationshipsFrom: %v", err)
			}
			if len(rels) != 0 {
				t.Errorf("edges = %+v, want none", rels)
			}
		})
	}
}

// Undecodable attributes on any row the resolver reads fail it rather than
// silently dropping that row's edge.
func TestResolveAppIntegrationsAssociationRules_DecodeError(t *testing.T) {
	for _, tc := range []struct {
		name, typ, nativeID string
	}{
		{"association", TypeAppIntegrationsEventIntegrationAssociation, appintTestPrefix + "event-integration-association/orders/bad"},
		{"event integration", TypeAppIntegrationsEventIntegration, appintTestPrefix + "event-integration/bad"},
		{"rule", TypeEventsRule, "arn:aws:events:us-east-1:123456789012:rule/bad"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := newTestStore(t)
			upsertTestResource(t, st, "aws", testAccountID, TypeAppIntegrationsEventIntegrationAssociation, appintTestEIAssoc, "us-east-1", appintEventAssocAttrs("orders", "orders-rule"))
			upsertTestResource(t, st, "aws", testAccountID, tc.typ, tc.nativeID, "us-east-1", "not json")
			if err := resolveAppIntegrationsAssociationRules(newTestAccount(testAccountID), st); err == nil {
				t.Error("err = nil, want the decode error")
			}
		})
	}
}

func TestResolveAppIntegrationsRelationships_EventIntegrationBus(t *testing.T) {
	st := newTestStore(t)
	busARN := "arn:aws:events:us-east-1:123456789012:event-bus/" + appintResPartnerBus
	busID := upsertTestResource(t, st, "aws", testAccountID, TypeEventsEventBus, busARN, "us-east-1", "{}")
	eiID := upsertTestResource(t, st, "aws", testAccountID, TypeAppIntegrationsEventIntegration, appintTestEI, "us-east-1", appintEventIntegrationAttrs("orders", appintResPartnerBus))
	if err := resolveAppIntegrationsRelationships(newTestAccount(testAccountID), st); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	rels, err := st.RelationshipsFrom(eiID, store.RelUses)
	if err != nil {
		t.Fatalf("RelationshipsFrom: %v", err)
	}
	assertRelationship(t, rels, eiID, busID, store.RelUses)
}

func TestResolveAppIntegrationsRelationships_NoAttrsAndDecodeError(t *testing.T) {
	st := newTestStore(t)
	eiID := upsertTestResource(t, st, "aws", testAccountID, TypeAppIntegrationsEventIntegration, appintTestEI, "us-east-1", "{}")
	if err := resolveAppIntegrationsRelationships(newTestAccount(testAccountID), st); err != nil {
		t.Fatalf("resolve with no attrs: %v", err)
	}
	if rels, err := st.RelationshipsFrom(eiID, store.RelUses); err != nil || len(rels) != 0 {
		t.Errorf("edges = %+v (err %v), want none", rels, err)
	}
	upsertTestResource(t, st, "aws", testAccountID, TypeAppIntegrationsEventIntegration, appintTestEINoName, "us-east-1", "not json")
	if err := resolveAppIntegrationsRelationships(newTestAccount(testAccountID), st); err == nil {
		t.Error("err = nil, want the decode error")
	}
}
