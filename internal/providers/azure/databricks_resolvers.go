package azure

import (
	"encoding/json"
	"fmt"
	"strings"

	"codeberg.org/icearp/disco/internal/util"
	"codeberg.org/icearp/disco/store"
)

func init() {
	registerResolver(
		resolveDatabricksRelationships,
		EdgeDecl{Source: TypeDatabricksWorkspace, Target: TypeNetworkVirtualNetwork, Kind: store.RelAttachedTo},
	)
}

// resolveDatabricksRelationships derives Databricks workspace
// -[attached-to]-> VNet edge for VNet-injected ("VNet-injected workspaces"
// Premium tier) deployments. The custom VNet ID lives in
// properties.parameters.customVirtualNetworkId.value.
func resolveDatabricksRelationships(sub *subscription, st *store.Store) error {
	workspaces, err := st.ListResources(store.ResourceFilter{
		Provider: "azure", AccountID: sub.ID,
		Types: []string{TypeDatabricksWorkspace},
		Limit: util.AllResources,
	})
	if err != nil || len(workspaces) == 0 {
		return err
	}
	vnets, err := st.ListResources(store.ResourceFilter{
		Provider: "azure", AccountID: sub.ID,
		Types: []string{TypeNetworkVirtualNetwork},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	vnetIndex := make(map[string]string, len(vnets))
	for _, v := range vnets {
		vnetIndex[strings.ToLower(v.NativeID)] = v.ID
	}

	for _, w := range workspaces {
		var attrs struct {
			Properties *struct {
				Parameters *struct {
					CustomVirtualNetworkID *struct {
						Value *string `json:"value"`
					} `json:"customVirtualNetworkId"`
				} `json:"parameters"`
			} `json:"properties"`
		}
		if err := json.Unmarshal([]byte(w.AttributesJSON), &attrs); err != nil || attrs.Properties == nil || attrs.Properties.Parameters == nil || attrs.Properties.Parameters.CustomVirtualNetworkID == nil || attrs.Properties.Parameters.CustomVirtualNetworkID.Value == nil {
			continue
		}
		toID, ok := vnetIndex[strings.ToLower(*attrs.Properties.Parameters.CustomVirtualNetworkID.Value)]
		if !ok {
			continue
		}
		if err := st.UpsertRelationship(w.ID, toID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert databricks→vnet: %w", err)
		}
	}
	return nil
}
