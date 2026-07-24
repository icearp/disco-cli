package azure

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/icearp/disco-cli/internal/util"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerResolver(
		resolveCognitiveServicesRelationships,
		EdgeDecl{Source: TypeCognitiveServicesAccount, Target: TypeKeyVaultVault, Kind: store.RelUses},
	)
}

// resolveCognitiveServicesRelationships derives Cognitive Services account
// -[uses]-> Key Vault edges for CMK-encrypted accounts. The CMK reference
// exposes the vault DNS root (properties.encryption.keyVaultProperties.keyVaultUri,
// "https://{vault}.vault.azure.net/"), matched against a per-sub vault-name
// index. Identity → MSI and private-endpoint edges resolve centrally.
func resolveCognitiveServicesRelationships(sub *subscription, st *store.Store) error {
	accounts, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"azure"}, AccountID: sub.ID,
		Types: []string{TypeCognitiveServicesAccount},
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
						KeyVaultURI *string `json:"keyVaultUri"`
					} `json:"keyVaultProperties"`
				} `json:"encryption"`
			} `json:"properties"`
		}
		if err := json.Unmarshal([]byte(a.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.Properties == nil || attrs.Properties.Encryption == nil ||
			attrs.Properties.Encryption.KeyVaultProperties == nil ||
			attrs.Properties.Encryption.KeyVaultProperties.KeyVaultURI == nil {
			continue
		}
		vaultName := vaultNameFromVaultURI(*attrs.Properties.Encryption.KeyVaultProperties.KeyVaultURI)
		if vaultName == "" {
			continue
		}
		toID, ok := vaultByName[strings.ToLower(vaultName)]
		if !ok {
			continue
		}
		if err := st.UpsertRelationship(a.ID, toID, store.RelUses, "directed", nil); err != nil {
			return fmt.Errorf("upsert cognitiveservices→keyvault: %w", err)
		}
	}
	return nil
}

// vaultNameIndex builds a lowercased vault-name → resource-ID index for the
// subscription's Key Vaults. Shared by CMK resolvers recovering a vault from
// a key/vault URI.
func vaultNameIndex(sub *subscription, st *store.Store) (map[string]string, error) {
	vaults, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"azure"}, AccountID: sub.ID,
		Types: []string{TypeKeyVaultVault},
		Limit: util.AllResources,
	})
	if err != nil {
		return nil, err
	}
	idx := make(map[string]string, len(vaults))
	for _, v := range vaults {
		idx[strings.ToLower(nameFromID(v.NativeID))] = v.ID
	}
	return idx, nil
}
