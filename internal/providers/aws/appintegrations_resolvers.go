package aws

import (
	"encoding/json"
	"fmt"

	"codeberg.org/icearp/disco/internal/util"
	"codeberg.org/icearp/disco/store"
)

func init() {
	registerResolver(
		resolveAppIntegrationsRelationships,
		EdgeDecl{TypeAppIntegrationsEventIntegration, TypeEventsEventBus, store.RelUses},
	)
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
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeAppIntegrationsEventIntegration},
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
			continue
		}
		busName := sv(a.EventBridgeBus)
		if busName == "" {
			continue
		}
		region := sv(e.Region)
		busARN := fmt.Sprintf("arn:aws:events:%s:%s:event-bus/%s", region, acct.ID, busName)
		busID := store.ResourceID("aws", acct.ID, TypeEventsEventBus, busARN)
		if _, ok := busIDs[busID]; ok {
			if err := st.UpsertRelationship(e.ID, busID, store.RelUses, "directed", nil); err != nil {
				return fmt.Errorf("upsert appintegrations-event→bus: %w", err)
			}
		}
	}
	return nil
}
