package azure

import (
	"encoding/json"
	"fmt"
	"strings"

	"codeberg.org/icearp/disco/internal/util"
	"codeberg.org/icearp/disco/store"
)

func init() { registerResolver(resolvePolicyRelationships) }

// resolvePolicyRelationships derives two edge classes per policy assignment:
//   - assignment -[uses]-> policy-definition OR policy-set-definition (FK on policyDefinitionId)
//   - assignment -[attached-to]-> scoped-resource (FK on scope when it matches a known resource)
//
// Mirrors the RBAC pattern from authorization_resolvers.go. Identity → MSI
// edges (DINE/deployIfNotExists assignments often carry a managed identity)
// covered by the generic consumer resolver.
func resolvePolicyRelationships(sub *subscription, st *store.Store) error {
	assignments, err := st.ListResources(store.ResourceFilter{
		Provider: "azure", AccountID: sub.ID,
		Types: []string{TypePolicyAssignment},
		Limit: util.AllResources,
	})
	if err != nil || len(assignments) == 0 {
		return err
	}

	// Per-sub lowercased index of every Azure resource — used both to resolve
	// scope (any resource type) and to FK to policy-definition / policy-set-definition.
	all, err := st.ListResources(store.ResourceFilter{
		Provider: "azure", AccountID: sub.ID, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	resourceIndex := make(map[string]string, len(all))
	for _, r := range all {
		resourceIndex[strings.ToLower(r.NativeID)] = r.ID
	}

	for _, r := range assignments {
		var attrs struct {
			Properties *struct {
				PolicyDefinitionID *string `json:"policyDefinitionId"`
				Scope              *string `json:"scope"`
			} `json:"properties"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil || attrs.Properties == nil {
			continue
		}

		if attrs.Properties.PolicyDefinitionID != nil {
			if toID, ok := resourceIndex[strings.ToLower(*attrs.Properties.PolicyDefinitionID)]; ok && toID != r.ID {
				if err := st.UpsertRelationship(r.ID, toID, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert policy-assignment→definition: %w", err)
				}
			}
		}

		if attrs.Properties.Scope != nil {
			if toID, ok := resourceIndex[strings.ToLower(*attrs.Properties.Scope)]; ok && toID != r.ID {
				if err := st.UpsertRelationship(r.ID, toID, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert policy-assignment→scope: %w", err)
				}
			}
		}
	}
	return nil
}
