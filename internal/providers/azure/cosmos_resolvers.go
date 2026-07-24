package azure

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/icearp/disco-cli/internal/util"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerResolver(resolveCosmosRelationships,
		EdgeDecl{Source: TypeCosmosDatabaseAccount, Target: TypeKeyVaultVault, Kind: store.RelUses},
	)
}

// resolveCosmosRelationships derives Cosmos DB account -[uses]-> Key Vault
// edges via the keyVaultKeyUri CMEK reference. Reuses vaultNameFromKeyURI
// from containerregistry_resolvers.go.
func resolveCosmosRelationships(sub *subscription, st *store.Store) error {
	accounts, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"azure"}, AccountID: sub.ID,
		Types: []string{TypeCosmosDatabaseAccount},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(accounts) == 0 {
		return nil
	}

	vaultByName, err := vaultNameIndex(sub, st)
	if err != nil {
		return err
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
