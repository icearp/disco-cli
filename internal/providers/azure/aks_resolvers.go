package azure

import (
	"encoding/json"
	"fmt"

	"codeburg.org/icearp/disco/internal/store"
	"codeburg.org/icearp/disco/internal/util"
)

func resolveAKSRelationships(sub *subscription, st *store.Store) error {
	clusters, err := st.ListResources(store.ResourceFilter{
		Provider: "azure", AccountID: sub.ID,
		Types: []string{"azure:containerservice:managed-cluster"},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	for _, r := range clusters {
		var attrs struct {
			Properties *struct {
				AgentPoolProfiles []struct {
					VnetSubnetID *string `json:"vnetSubnetID"`
				} `json:"agentPoolProfiles"`
			} `json:"properties"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.Properties == nil {
			continue
		}
		seen := map[string]bool{}
		for _, ap := range attrs.Properties.AgentPoolProfiles {
			if ap.VnetSubnetID == nil {
				continue
			}
			// Extract the VNet ID from the subnet ID.
			vnetID := vnetIDFromSubnetID(*ap.VnetSubnetID)
			if vnetID == "" || seen[vnetID] {
				continue
			}
			seen[vnetID] = true
			vnetResourceID := store.ResourceID("azure", sub.ID, "azure:network:virtual-network", vnetID)
			if err := st.UpsertRelationship(r.ID, vnetResourceID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert aks→vnet relationship: %w", err)
			}
		}
	}
	return nil
}
