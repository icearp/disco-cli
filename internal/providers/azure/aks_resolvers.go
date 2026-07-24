package azure

import (
	"encoding/json"
	"fmt"

	"github.com/icearp/disco-cli/internal/util"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerResolver(resolveAKSRelationships,
		EdgeDecl{Source: TypeContainerServiceManagedCluster, Target: TypeNetworkVirtualNetwork, Kind: store.RelAttachedTo},
	)
}

func resolveAKSRelationships(sub *subscription, st *store.Store) error {
	clusters, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"azure"}, AccountID: sub.ID,
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
			vnetID := vnetIDFromSubnetID(*ap.VnetSubnetID)
			if vnetID == "" || seen[vnetID] {
				continue
			}
			seen[vnetID] = true
			vnetResourceID := store.ResourceID("azure", sub.ID, vnetID)
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
