package aws

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/icearp/disco-cli/internal/util"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerResolver(
		resolveAppIntegrationsRelationships,
		EdgeDecl{TypeAppIntegrationsEventIntegration, TypeEventsEventBus, store.RelUses},
	)
	registerResolver(
		resolveAppIntegrationsAssociationRules,
		EdgeDecl{TypeAppIntegrationsEventIntegrationAssociation, TypeEventsRule, store.RelUses},
	)
	registerResolver(
		noopAppIntegrationsHierarchy,
		EdgeDecl{TypeAppIntegrationsApplication, TypeAppIntegrationsApplicationAssociation, store.RelContains},
		EdgeDecl{TypeAppIntegrationsDataIntegration, TypeAppIntegrationsDataIntegrationAssociation, store.RelContains},
		EdgeDecl{TypeAppIntegrationsEventIntegration, TypeAppIntegrationsEventIntegrationAssociation, store.RelContains},
	)
}

// noopAppIntegrationsHierarchy carries the EdgeDecl metadata for the
// parent → association contains edges the scanner records through childFanOut.
func noopAppIntegrationsHierarchy(_ *account, _ *store.Store) error {
	return nil
}

// resolveAppIntegrationsRelationships emits event-integration → EventBridge bus
// edges. EventBridgeBus is a bus name (not an ARN); reconstruct the bus ARN
// per region/account and FK-safe via scanned bus id set.
//
// Application + DataIntegration carry no cross-resource ARNs that point at
// scanned disco resources (DataIntegration.SourceURI is a vendor-specific
// connection string — Salesforce, Zendesk, etc., not an AWS ARN).
func resolveAppIntegrationsRelationships(acct *account, st *store.Store) error {
	events, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeAppIntegrationsEventIntegration},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(events) == 0 {
		return nil
	}
	busIDs, err := scannedIDSet(acct, st, TypeEventsEventBus)
	if err != nil {
		return err
	}
	type attrs struct {
		EventBridgeBus *string `json:"EventBridgeBus"`
	}
	for _, e := range events {
		var a attrs
		if err := json.Unmarshal([]byte(e.AttributesJSON), &a); err != nil {
			return fmt.Errorf("decode appintegrations event-integration %s: %w", e.NativeID, err)
		}
		busName := sv(a.EventBridgeBus)
		if busName == "" {
			continue
		}
		region := sv(e.Region)
		busARN := fmt.Sprintf("arn:aws:events:%s:%s:event-bus/%s", region, acct.ID, busName)
		busID := store.ResourceID("aws", acct.ID, busARN)
		if _, ok := busIDs[busID]; ok {
			if err := st.UpsertRelationship(e.ID, busID, store.RelUses, "directed", nil); err != nil {
				return fmt.Errorf("upsert appintegrations-event→bus: %w", err)
			}
		}
	}
	return nil
}

type appIntegrationsKey struct{ region, name string }

type eventBridgeRuleKey struct{ region, bus, name string }

// resolveAppIntegrationsAssociationRules emits event-integration association
// → EventBridge rule edges. The association names its rule
// (EventBridgeRuleName) and its event integration (EventIntegrationName) but
// not the bus, so the rule is looked up by name on that event integration's
// bus among the rules scanned in the same region. That the rule lives on the
// integration's bus is inferred from the API shape, not observed live; a
// mismatch emits no edge, never one to an unscanned rule.
func resolveAppIntegrationsAssociationRules(acct *account, st *store.Store) error {
	assocs, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeAppIntegrationsEventIntegrationAssociation},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(assocs) == 0 {
		return nil
	}
	busByIntegration, err := appIntegrationsBusByIntegration(acct, st)
	if err != nil {
		return err
	}
	ruleIDs, err := eventBridgeRuleIDsByBusAndName(acct, st)
	if err != nil {
		return err
	}
	for _, r := range assocs {
		var a struct {
			EventIntegrationName *string `json:"EventIntegrationName"`
			EventBridgeRuleName  *string `json:"EventBridgeRuleName"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &a); err != nil {
			return fmt.Errorf("decode appintegrations event-integration-association %s: %w", r.NativeID, err)
		}
		region := sv(r.Region)
		bus, ok := busByIntegration[appIntegrationsKey{region, sv(a.EventIntegrationName)}]
		if !ok {
			continue
		}
		ruleID, ok := ruleIDs[eventBridgeRuleKey{region, bus, sv(a.EventBridgeRuleName)}]
		if !ok {
			continue
		}
		if err := st.UpsertRelationship(r.ID, ruleID, store.RelUses, "directed", nil); err != nil {
			return fmt.Errorf("upsert appintegrations-event-association→rule: %w", err)
		}
	}
	return nil
}

// appIntegrationsBusByIntegration maps each scanned event integration, by
// region and name, to its EventBridge bus name.
func appIntegrationsBusByIntegration(acct *account, st *store.Store) (map[appIntegrationsKey]string, error) {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeAppIntegrationsEventIntegration},
		Limit: util.AllResources,
	})
	if err != nil {
		return nil, err
	}
	out := make(map[appIntegrationsKey]string, len(rows))
	for _, r := range rows {
		var a struct {
			Name           *string `json:"Name"`
			EventBridgeBus *string `json:"EventBridgeBus"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &a); err != nil {
			return nil, fmt.Errorf("decode appintegrations event-integration %s: %w", r.NativeID, err)
		}
		if bus := sv(a.EventBridgeBus); bus != "" && sv(a.Name) != "" {
			out[appIntegrationsKey{sv(r.Region), sv(a.Name)}] = bus
		}
	}
	return out, nil
}

// eventBridgeRuleIDsByBusAndName maps each scanned EventBridge rule, by
// region, bus name and rule name, to its ResourceID. Rule rows are stored as
// {"Rule": <SDK Rule>, "Targets": [...]} (eventbridge_scanners.go).
func eventBridgeRuleIDsByBusAndName(acct *account, st *store.Store) (map[eventBridgeRuleKey]string, error) {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeEventsRule},
		Limit: util.AllResources,
	})
	if err != nil {
		return nil, err
	}
	out := make(map[eventBridgeRuleKey]string, len(rows))
	for _, r := range rows {
		var a struct {
			Rule struct {
				Name         *string `json:"Name"`
				EventBusName *string `json:"EventBusName"`
			} `json:"Rule"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &a); err != nil {
			return nil, fmt.Errorf("decode events rule %s: %w", r.NativeID, err)
		}
		if name := sv(a.Rule.Name); name != "" {
			out[eventBridgeRuleKey{sv(r.Region), eventBusNameOf(sv(a.Rule.EventBusName)), name}] = r.ID
		}
	}
	return out, nil
}

// eventBusNameOf returns the bus name of Rule.EventBusName, which the SDK
// documents as "The name or ARN of the event bus". The name starts after the
// ARN's ":event-bus/" resource type, not after the last "event-bus/": a bus
// name may itself contain "/" and that text.
func eventBusNameOf(nameOrARN string) string {
	if !strings.HasPrefix(nameOrARN, "arn:") {
		return nameOrARN
	}
	if _, name, ok := strings.Cut(nameOrARN, ":event-bus/"); ok {
		return name
	}
	return nameOrARN
}
