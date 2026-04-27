package azure

import (
	"encoding/json"
	"fmt"

	"codeberg.org/icearp/disco/internal/store"
	"codeberg.org/icearp/disco/internal/util"
)

func init() { registerResolver(resolvePostgreSQLRelationships) }

// resolvePostgreSQLRelationships derives PG flexible-server -[attached-to]->
// VNet via properties.network.delegatedSubnetResourceId. PG flexible servers
// integrate via subnet delegation (not standalone subnetId like Redis), so
// the parent VNet is recovered with vnetIDFromSubnetID.
func resolvePostgreSQLRelationships(sub *subscription, st *store.Store) error {
	servers, err := st.ListResources(store.ResourceFilter{
		Provider: "azure", AccountID: sub.ID,
		Types: []string{TypePostgreSQLFlexibleServer},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	for _, r := range servers {
		var attrs struct {
			Properties *struct {
				Network *struct {
					DelegatedSubnetResourceID *string `json:"delegatedSubnetResourceId"`
				} `json:"network"`
			} `json:"properties"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.Properties == nil || attrs.Properties.Network == nil || attrs.Properties.Network.DelegatedSubnetResourceID == nil {
			continue
		}
		vnetID := vnetIDFromSubnetID(*attrs.Properties.Network.DelegatedSubnetResourceID)
		if vnetID == "" {
			continue
		}
		vnetResourceID := store.ResourceID("azure", sub.ID, TypeNetworkVirtualNetwork, vnetID)
		if _, err := st.GetResource(vnetResourceID); err != nil {
			continue
		}
		if err := st.UpsertRelationship(r.ID, vnetResourceID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert pg→vnet: %w", err)
		}
	}
	return nil
}
