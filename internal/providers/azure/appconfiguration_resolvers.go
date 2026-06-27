package azure

import (
	"encoding/json"
	"fmt"
	"strings"

	"codeberg.org/icearp/disco/internal/util"
	"codeberg.org/icearp/disco/store"
)

func init() {
	registerResolver(resolveAppConfigurationRelationships,
		EdgeDecl{Source: TypeAppConfigurationStore, Target: TypeKeyVaultVault, Kind: store.RelUses},
	)
}

// resolveAppConfigurationRelationships derives App Configuration store
// -[uses]-> Key Vault edges for stores using customer-managed key encryption.
// The CMK reference is a Key Vault key URI
// (properties.encryption.keyVaultProperties.keyIdentifier), parsed back to a
// vault name and matched against a per-sub vault-name index.
func resolveAppConfigurationRelationships(sub *subscription, st *store.Store) error {
	stores, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"azure"}, AccountID: sub.ID,
		Types: []string{TypeAppConfigurationStore},
		Limit: util.AllResources,
	})
	if err != nil || len(stores) == 0 {
		return err
	}
	vaultByName, err := vaultNameIndex(sub, st)
	if err != nil {
		return err
	}
	if len(vaultByName) == 0 {
		return nil
	}
	for _, s := range stores {
		var attrs struct {
			Properties *struct {
				Encryption *struct {
					KeyVaultProperties *struct {
						KeyIdentifier *string `json:"keyIdentifier"`
					} `json:"keyVaultProperties"`
				} `json:"encryption"`
			} `json:"properties"`
		}
		if err := json.Unmarshal([]byte(s.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.Properties == nil || attrs.Properties.Encryption == nil ||
			attrs.Properties.Encryption.KeyVaultProperties == nil ||
			attrs.Properties.Encryption.KeyVaultProperties.KeyIdentifier == nil {
			continue
		}
		vaultName := vaultNameFromKeyURI(*attrs.Properties.Encryption.KeyVaultProperties.KeyIdentifier)
		if vaultName == "" {
			continue
		}
		toID, ok := vaultByName[strings.ToLower(vaultName)]
		if !ok {
			continue
		}
		if err := st.UpsertRelationship(s.ID, toID, store.RelUses, "directed", nil); err != nil {
			return fmt.Errorf("upsert appconfiguration→keyvault: %w", err)
		}
	}
	return nil
}
