package azure

import (
	"encoding/json"
	"fmt"
	"strings"

	"codeberg.org/icearp/disco/internal/util"
	"codeberg.org/icearp/disco/store"
)

func init() { registerResolver(resolveNetAppRelationships) }

// resolveNetAppRelationships derives NetApp account -[uses]-> Key Vault edges
// for accounts encrypted with a customer-managed key. NetApp exposes the
// vault's full ARM resource ID (encryption.keyVaultProperties.keyVaultResourceId),
// matched case-insensitively against a per-sub Key Vault NativeID index.
func resolveNetAppRelationships(sub *subscription, st *store.Store) error {
	accounts, err := st.ListResources(store.ResourceFilter{
		Provider: "azure", AccountID: sub.ID,
		Types: []string{TypeNetAppAccount},
		Limit: util.AllResources,
	})
	if err != nil || len(accounts) == 0 {
		return err
	}
	vaultByID, err := nativeIDIndex(sub, st, TypeKeyVaultVault)
	if err != nil {
		return err
	}
	for _, a := range accounts {
		var attrs struct {
			Properties *struct {
				Encryption *struct {
					KeyVaultProperties *struct {
						KeyVaultResourceID *string `json:"keyVaultResourceId"`
					} `json:"keyVaultProperties"`
				} `json:"encryption"`
			} `json:"properties"`
		}
		if err := json.Unmarshal([]byte(a.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.Properties == nil || attrs.Properties.Encryption == nil ||
			attrs.Properties.Encryption.KeyVaultProperties == nil ||
			attrs.Properties.Encryption.KeyVaultProperties.KeyVaultResourceID == nil {
			continue
		}
		toID, ok := vaultByID[strings.ToLower(*attrs.Properties.Encryption.KeyVaultProperties.KeyVaultResourceID)]
		if !ok {
			continue
		}
		if err := st.UpsertRelationship(a.ID, toID, store.RelUses, "directed", nil); err != nil {
			return fmt.Errorf("upsert netapp→keyvault: %w", err)
		}
	}
	return nil
}
