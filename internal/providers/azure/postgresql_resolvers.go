package azure

import (
	"encoding/json"
	"fmt"

	"github.com/icearp/disco-cli/internal/util"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerResolver(resolvePostgreSQLRelationships,
		EdgeDecl{Source: TypePostgreSQLFlexibleServer, Target: TypeNetworkVirtualNetwork, Kind: store.RelAttachedTo},
	)
}

// resolvePostgreSQLRelationships derives PG flexible-server -[attached-to]->
// VNet via properties.network.delegatedSubnetResourceId. Servers use subnet
// delegation (not standalone subnetId like Redis), so vnetIDFromSubnetID
// recovers the parent VNet.
func resolvePostgreSQLRelationships(sub *subscription, st *store.Store) error {
	servers, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"azure"}, AccountID: sub.ID,
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
		vnetResourceID := store.ResourceID("azure", sub.ID, vnetID)
		if _, err := st.GetResource(vnetResourceID); err != nil {
			continue
		}
		if err := st.UpsertRelationship(r.ID, vnetResourceID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert pg→vnet: %w", err)
		}
	}
	return nil
}
