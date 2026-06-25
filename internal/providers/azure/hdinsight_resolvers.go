package azure

import (
	"encoding/json"
	"strings"

	"codeberg.org/icearp/disco/internal/util"
	"codeberg.org/icearp/disco/store"
)

func init() {
	registerResolver(
		resolveHDInsightRelationships,
		EdgeDecl{Source: TypeHDInsightCluster, Target: TypeNetworkVirtualNetwork, Kind: store.RelAttachedTo},
	)
}

// resolveHDInsightRelationships derives HDInsight cluster -[attached-to]-> VNet
// edges via the per-role VNet-injection subnet references
// (computeProfile.roles[].virtualNetworkProfile.subnet).
func resolveHDInsightRelationships(sub *subscription, st *store.Store) error {
	clusters, err := st.ListResources(store.ResourceFilter{
		Provider: "azure", AccountID: sub.ID,
		Types: []string{TypeHDInsightCluster},
		Limit: util.AllResources,
	})
	if err != nil || len(clusters) == 0 {
		return err
	}
	vnetByID, err := nativeIDIndex(sub, st, TypeNetworkVirtualNetwork)
	if err != nil {
		return err
	}
	for _, c := range clusters {
		var attrs struct {
			Properties *struct {
				ComputeProfile *struct {
					Roles []struct {
						VirtualNetworkProfile *struct {
							Subnet *string `json:"subnet"`
						} `json:"virtualNetworkProfile"`
					} `json:"roles"`
				} `json:"computeProfile"`
			} `json:"properties"`
		}
		if err := json.Unmarshal([]byte(c.AttributesJSON), &attrs); err != nil || attrs.Properties == nil || attrs.Properties.ComputeProfile == nil {
			continue
		}
		seen := map[string]bool{}
		for _, role := range attrs.Properties.ComputeProfile.Roles {
			if role.VirtualNetworkProfile == nil || role.VirtualNetworkProfile.Subnet == nil {
				continue
			}
			vnetID := vnetIDFromSubnetID(*role.VirtualNetworkProfile.Subnet)
			if vnetID == "" || seen[strings.ToLower(vnetID)] {
				continue
			}
			seen[strings.ToLower(vnetID)] = true
			if err := upsertVNetAttachment(st, c.ID, *role.VirtualNetworkProfile.Subnet, vnetByID); err != nil {
				return err
			}
		}
	}
	return nil
}
