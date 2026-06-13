package azure

import (
	"encoding/json"
	"fmt"

	"codeberg.org/icearp/disco/internal/util"
	"codeberg.org/icearp/disco/store"
)

func init() { registerResolver(resolveAKSRelationships) }

func resolveAKSRelationships(sub *subscription, st *store.Store) error {
	clusters, err := st.ListResources(store.ResourceFilter{
		Provider: "azure", AccountID: sub.ID,
		Types: []string{TypeContainerServiceManagedCluster},
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
			vnetResourceID := store.ResourceID("azure", sub.ID, TypeNetworkVirtualNetwork, vnetID)
			if _, err := st.GetResource(vnetResourceID); err != nil {
				continue // VNet not in store (network service not yet scanned)
			}
			if err := st.UpsertRelationship(r.ID, vnetResourceID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert aks→vnet relationship: %w", err)
			}
		}
	}
	return nil
}
