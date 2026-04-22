package azure

import (
	"fmt"
	"strings"

	"codeberg.org/icearp/disco/internal/store"
	"codeberg.org/icearp/disco/internal/util"
)

func init() { registerResolver(resolveSubnetVNetRelationships) }

func resolveSubnetVNetRelationships(sub *subscription, st *store.Store) error {
	subnets, err := st.ListResources(store.ResourceFilter{
		Provider: "azure", AccountID: sub.ID,
		Types: []string{TypeNetworkSubnet},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	for _, r := range subnets {
		// Subnet NativeID is /subscriptions/{sub}/resourceGroups/{rg}/providers/
		// Microsoft.Network/virtualNetworks/{vnet}/subnets/{subnet}.
		// The VNet ID is the parent path up to /subnets/.
		vnetID := vnetIDFromSubnetID(r.NativeID)
		if vnetID != "" {
			vnetResourceID := store.ResourceID("azure", sub.ID, TypeNetworkVirtualNetwork, vnetID)
			if err := st.UpsertRelationship(r.ID, vnetResourceID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert subnet→vnet relationship: %w", err)
			}
		}
	}
	return nil
}

// vnetIDFromSubnetID extracts the VNet resource ID from a subnet resource ID.
func vnetIDFromSubnetID(subnetID string) string {
	lower := strings.ToLower(subnetID)
	idx := strings.Index(lower, "/subnets/")
	if idx < 0 {
		return ""
	}
	return subnetID[:idx]
}
