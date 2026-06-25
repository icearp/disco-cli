package azure

import (
	"encoding/json"
	"fmt"

	"codeberg.org/icearp/disco/internal/util"
	"codeberg.org/icearp/disco/store"
)

func init() {
	registerResolver(resolveRedisRelationships,
		EdgeDecl{Source: TypeRedisCache, Target: TypeNetworkVirtualNetwork, Kind: store.RelAttachedTo},
	)
}

// resolveRedisRelationships derives Redis cache -[attached-to]-> VNet edges
// for VNet-injected Premium-tier instances. The cache references its target
// subnet via properties.subnetId; the parent VNet ID is recovered with
// vnetIDFromSubnetID (precedent: aks_resolvers.go). Multiple caches in the
// same VNet collapse to one edge per cache.
func resolveRedisRelationships(sub *subscription, st *store.Store) error {
	caches, err := st.ListResources(store.ResourceFilter{
		Provider: "azure", AccountID: sub.ID,
		Types: []string{TypeRedisCache},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	for _, r := range caches {
		var attrs struct {
			Properties *struct {
				SubnetID *string `json:"subnetId"`
			} `json:"properties"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.Properties == nil || attrs.Properties.SubnetID == nil {
			continue
		}
		vnetID := vnetIDFromSubnetID(*attrs.Properties.SubnetID)
		if vnetID == "" {
			continue
		}
		vnetResourceID := store.ResourceID("azure", sub.ID, TypeNetworkVirtualNetwork, vnetID)
		if _, err := st.GetResource(vnetResourceID); err != nil {
			continue
		}
		if err := st.UpsertRelationship(r.ID, vnetResourceID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert redis→vnet: %w", err)
		}
	}
	return nil
}
