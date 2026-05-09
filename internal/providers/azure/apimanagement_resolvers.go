package azure

import (
	"encoding/json"
	"fmt"

	"codeberg.org/icearp/disco/store"
	"codeberg.org/icearp/disco/internal/util"
)

func init() { registerResolver(resolveAPIManagementRelationships) }

// resolveAPIManagementRelationships derives APIM service -[attached-to]-> VNet
// edge for VNet-injected (Internal/External mode) instances via
// `properties.virtualNetworkConfiguration.subnetResourceId`. Reuses
// vnetIDFromSubnetID. Identity → MSI edges covered by generic consumer
// resolver. KeyVault edges (named-values that reference vault secrets) live
// under sub-resources and are deferred.
func resolveAPIManagementRelationships(sub *subscription, st *store.Store) error {
	services, err := st.ListResources(store.ResourceFilter{
		Provider: "azure", AccountID: sub.ID,
		Types: []string{TypeAPIManagementService},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	for _, r := range services {
		var attrs struct {
			Properties *struct {
				VirtualNetworkConfiguration *struct {
					SubnetResourceID *string `json:"subnetResourceId"`
				} `json:"virtualNetworkConfiguration"`
			} `json:"properties"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil ||
			attrs.Properties == nil ||
			attrs.Properties.VirtualNetworkConfiguration == nil ||
			attrs.Properties.VirtualNetworkConfiguration.SubnetResourceID == nil {
			continue
		}
		vnetID := vnetIDFromSubnetID(*attrs.Properties.VirtualNetworkConfiguration.SubnetResourceID)
		if vnetID == "" {
			continue
		}
		vnetResourceID := store.ResourceID("azure", sub.ID, TypeNetworkVirtualNetwork, vnetID)
		if _, err := st.GetResource(vnetResourceID); err != nil {
			continue
		}
		if err := st.UpsertRelationship(r.ID, vnetResourceID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert apim→vnet: %w", err)
		}
	}
	return nil
}
