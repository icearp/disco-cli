package azure

import (
	"encoding/json"

	"codeberg.org/icearp/disco/internal/util"
	"codeberg.org/icearp/disco/store"
)

func init() { registerResolver(resolveDataMigrationRelationships) }

// resolveDataMigrationRelationships wires a DMS instance to the VNet it is
// joined to via properties.virtualSubnetId (a Microsoft.Network/.../subnets
// ARM ID). The sibling virtualNicId is intentionally not resolved — the NIC is
// itself placed in that subnet, so the subnet edge already captures the
// network placement.
func resolveDataMigrationRelationships(sub *subscription, st *store.Store) error {
	svcs, err := st.ListResources(store.ResourceFilter{
		Provider: "azure", AccountID: sub.ID,
		Types: []string{TypeDataMigrationService},
		Limit: util.AllResources,
	})
	if err != nil || len(svcs) == 0 {
		return err
	}
	vnetByID, err := nativeIDIndex(sub, st, TypeNetworkVirtualNetwork)
	if err != nil {
		return err
	}
	for _, s := range svcs {
		var attrs struct {
			Properties *struct {
				VirtualSubnetID *string `json:"virtualSubnetId"`
			} `json:"properties"`
		}
		if err := json.Unmarshal([]byte(s.AttributesJSON), &attrs); err != nil || attrs.Properties == nil {
			continue
		}
		if attrs.Properties.VirtualSubnetID != nil {
			if err := upsertVNetAttachment(st, s.ID, *attrs.Properties.VirtualSubnetID, vnetByID); err != nil {
				return err
			}
		}
	}
	return nil
}
