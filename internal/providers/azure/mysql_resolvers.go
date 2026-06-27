package azure

import (
	"encoding/json"
	"fmt"
	"strings"

	"codeberg.org/icearp/disco/internal/util"
	"codeberg.org/icearp/disco/store"
)

func init() {
	registerResolver(resolveMySQLRelationships,
		EdgeDecl{Source: TypeMySQLFlexibleServer, Target: TypeNetworkVirtualNetwork, Kind: store.RelAttachedTo},
		EdgeDecl{Source: TypeMySQLFlexibleServer, Target: TypeKeyVaultVault, Kind: store.RelUses},
	)
}

// resolveMySQLRelationships derives MySQL flexible-server edges:
//   - server -[attached-to]-> VNet via properties.network.delegatedSubnetResourceId
//   - server -[uses]-> KeyVault via properties.dataEncryption.primaryKeyUri (CMEK)
//
// Uses vnetNameFromKeyURI / vnetIDFromSubnetID helpers from sibling resolvers.
func resolveMySQLRelationships(sub *subscription, st *store.Store) error {
	servers, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"azure"}, AccountID: sub.ID,
		Types: []string{TypeMySQLFlexibleServer},
		Limit: util.AllResources,
	})
	if err != nil || len(servers) == 0 {
		return err
	}
	vaults, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"azure"}, AccountID: sub.ID,
		Types: []string{TypeKeyVaultVault},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	vaultByName := make(map[string]string, len(vaults))
	for _, v := range vaults {
		vaultByName[strings.ToLower(nameFromID(v.NativeID))] = v.ID
	}

	for _, r := range servers {
		var attrs struct {
			Properties *struct {
				Network *struct {
					DelegatedSubnetResourceID *string `json:"delegatedSubnetResourceId"`
				} `json:"network"`
				DataEncryption *struct {
					PrimaryKeyURI *string `json:"primaryKeyUri"`
				} `json:"dataEncryption"`
			} `json:"properties"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil || attrs.Properties == nil {
			continue
		}
		if attrs.Properties.Network != nil && attrs.Properties.Network.DelegatedSubnetResourceID != nil {
			if vnetID := vnetIDFromSubnetID(*attrs.Properties.Network.DelegatedSubnetResourceID); vnetID != "" {
				vnetResourceID := store.ResourceID("azure", sub.ID, TypeNetworkVirtualNetwork, vnetID)
				if _, err := st.GetResource(vnetResourceID); err == nil {
					if err := st.UpsertRelationship(r.ID, vnetResourceID, store.RelAttachedTo, "directed", nil); err != nil {
						return fmt.Errorf("upsert mysql→vnet: %w", err)
					}
				}
			}
		}
		if attrs.Properties.DataEncryption != nil && attrs.Properties.DataEncryption.PrimaryKeyURI != nil {
			if vaultName := vaultNameFromKeyURI(*attrs.Properties.DataEncryption.PrimaryKeyURI); vaultName != "" {
				if toID, ok := vaultByName[strings.ToLower(vaultName)]; ok {
					if err := st.UpsertRelationship(r.ID, toID, store.RelUses, "directed", nil); err != nil {
						return fmt.Errorf("upsert mysql→keyvault: %w", err)
					}
				}
			}
		}
	}
	return nil
}
