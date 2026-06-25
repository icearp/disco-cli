package azure

import (
	"encoding/json"
	"fmt"
	"strings"

	"codeberg.org/icearp/disco/internal/util"
	"codeberg.org/icearp/disco/store"
)

func init() {
	registerResolver(resolveRecoveryServicesRelationships,
		EdgeDecl{Source: TypeRecoveryServicesVault, Target: TypeKeyVaultVault, Kind: store.RelUses},
	)
}

// resolveRecoveryServicesRelationships derives Recovery Services vault
// -[uses]-> Key Vault edges for vaults encrypted with a customer-managed key.
// The CMK reference is a Key Vault key URI
// (properties.encryption.keyVaultProperties.keyUri), parsed back to a vault
// name and matched against a per-sub vault-name index.
func resolveRecoveryServicesRelationships(sub *subscription, st *store.Store) error {
	vaults, err := st.ListResources(store.ResourceFilter{
		Provider: "azure", AccountID: sub.ID,
		Types: []string{TypeRecoveryServicesVault},
		Limit: util.AllResources,
	})
	if err != nil || len(vaults) == 0 {
		return err
	}
	vaultByName, err := vaultNameIndex(sub, st)
	if err != nil {
		return err
	}
	if len(vaultByName) == 0 {
		return nil
	}
	for _, v := range vaults {
		var attrs struct {
			Properties *struct {
				Encryption *struct {
					KeyVaultProperties *struct {
						KeyURI *string `json:"keyUri"`
					} `json:"keyVaultProperties"`
				} `json:"encryption"`
			} `json:"properties"`
		}
		if err := json.Unmarshal([]byte(v.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.Properties == nil || attrs.Properties.Encryption == nil ||
			attrs.Properties.Encryption.KeyVaultProperties == nil ||
			attrs.Properties.Encryption.KeyVaultProperties.KeyURI == nil {
			continue
		}
		vaultName := vaultNameFromKeyURI(*attrs.Properties.Encryption.KeyVaultProperties.KeyURI)
		if vaultName == "" {
			continue
		}
		toID, ok := vaultByName[strings.ToLower(vaultName)]
		if !ok {
			continue
		}
		if err := st.UpsertRelationship(v.ID, toID, store.RelUses, "directed", nil); err != nil {
			return fmt.Errorf("upsert recoveryservices→keyvault: %w", err)
		}
	}
	return nil
}
