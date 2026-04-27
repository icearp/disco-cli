package azure

import (
	"encoding/json"
	"fmt"
	"strings"

	"codeberg.org/icearp/disco/internal/store"
	"codeberg.org/icearp/disco/internal/util"
)

func init() { registerResolver(resolveCosmosRelationships) }

// resolveCosmosRelationships derives Cosmos DB account -[uses]-> Key Vault
// edges via the keyVaultKeyUri CMEK reference. Reuses vaultNameFromKeyURI
// from containerregistry_resolvers.go.
func resolveCosmosRelationships(sub *subscription, st *store.Store) error {
	accounts, err := st.ListResources(store.ResourceFilter{
		Provider: "azure", AccountID: sub.ID,
		Types: []string{TypeCosmosDatabaseAccount},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(accounts) == 0 {
		return nil
	}

	vaults, err := st.ListResources(store.ResourceFilter{
		Provider: "azure", AccountID: sub.ID,
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

	for _, r := range accounts {
		var attrs struct {
			Properties *struct {
				KeyVaultKeyURI *string `json:"keyVaultKeyUri"`
			} `json:"properties"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.Properties == nil || attrs.Properties.KeyVaultKeyURI == nil {
			continue
		}
		vaultName := vaultNameFromKeyURI(*attrs.Properties.KeyVaultKeyURI)
		if vaultName == "" {
			continue
		}
		toID, ok := vaultByName[strings.ToLower(vaultName)]
		if !ok {
			continue
		}
		if err := st.UpsertRelationship(r.ID, toID, store.RelUses, "directed", nil); err != nil {
			return fmt.Errorf("upsert cosmos→keyvault: %w", err)
		}
	}
	return nil
}
