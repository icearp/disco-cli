package azure

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/icearp/disco-cli/internal/util"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerResolver(resolveAutomationRelationships,
		EdgeDecl{Source: TypeAutomationAccount, Target: TypeKeyVaultVault, Kind: store.RelUses},
	)
}

// resolveAutomationRelationships derives Automation account -[uses]-> Key Vault
// edges for accounts encrypted with a customer-managed key. The CMK reference
// exposes the vault DNS root (encryption.keyVaultProperties.keyvaultUri — note
// the lowercase 'v'), matched against a per-sub vault-name index.
func resolveAutomationRelationships(sub *subscription, st *store.Store) error {
	accounts, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"azure"}, AccountID: sub.ID,
		Types: []string{TypeAutomationAccount},
		Limit: util.AllResources,
	})
	if err != nil || len(accounts) == 0 {
		return err
	}
	vaultByName, err := vaultNameIndex(sub, st)
	if err != nil {
		return err
	}
	if len(vaultByName) == 0 {
		return nil
	}
	for _, a := range accounts {
		var attrs struct {
			Properties *struct {
				Encryption *struct {
					KeyVaultProperties *struct {
						KeyvaultURI *string `json:"keyvaultUri"`
					} `json:"keyVaultProperties"`
				} `json:"encryption"`
			} `json:"properties"`
		}
		if err := json.Unmarshal([]byte(a.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.Properties == nil || attrs.Properties.Encryption == nil ||
			attrs.Properties.Encryption.KeyVaultProperties == nil ||
			attrs.Properties.Encryption.KeyVaultProperties.KeyvaultURI == nil {
			continue
		}
		name := vaultNameFromVaultURI(*attrs.Properties.Encryption.KeyVaultProperties.KeyvaultURI)
		if name == "" {
			continue
		}
		toID, ok := vaultByName[strings.ToLower(name)]
		if !ok {
			continue
		}
		if err := st.UpsertRelationship(a.ID, toID, store.RelUses, "directed", nil); err != nil {
			return fmt.Errorf("upsert automation→keyvault: %w", err)
		}
	}
	return nil
}
