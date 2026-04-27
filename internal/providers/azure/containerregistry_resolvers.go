package azure

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"codeberg.org/icearp/disco/internal/store"
	"codeberg.org/icearp/disco/internal/util"
)

func init() { registerResolver(resolveContainerRegistryRelationships) }

// resolveContainerRegistryRelationships derives ACR -[uses]-> Key Vault edges
// for registries that opt into customer-managed key encryption. The CMEK
// reference on a registry is a Key Vault key URI (https://{vault}.vault.azure.net/keys/{name}/{ver}),
// so the resolver parses the host to recover the vault name and matches it
// against a per-sub vault-name index.
func resolveContainerRegistryRelationships(sub *subscription, st *store.Store) error {
	registries, err := st.ListResources(store.ResourceFilter{
		Provider: "azure", AccountID: sub.ID,
		Types: []string{TypeContainerRegistryRegistry},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(registries) == 0 {
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

	for _, r := range registries {
		var attrs struct {
			Properties *struct {
				Encryption *struct {
					KeyVaultProperties *struct {
						KeyIdentifier *string `json:"keyIdentifier"`
					} `json:"keyVaultProperties"`
				} `json:"encryption"`
			} `json:"properties"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
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
		if err := st.UpsertRelationship(r.ID, toID, store.RelUses, "directed", nil); err != nil {
			return fmt.Errorf("upsert acr→keyvault: %w", err)
		}
	}
	return nil
}

// vaultNameFromKeyURI extracts the vault name from a Key Vault key URI of the
// form "https://{vault}.vault.azure.net/keys/{name}[/{version}]". Returns ""
// if the host shape does not match. Reused by any resolver mapping a Key
// Vault key/secret URI back to a vault resource.
func vaultNameFromKeyURI(uri string) string {
	u, err := url.Parse(uri)
	if err != nil || u.Host == "" {
		return ""
	}
	host := strings.ToLower(u.Host)
	// Match the public, US-government, China, and Germany suffixes.
	for _, suffix := range []string{".vault.azure.net", ".vault.usgovcloudapi.net", ".vault.azure.cn", ".vault.microsoftazure.de"} {
		if strings.HasSuffix(host, suffix) {
			return host[:len(host)-len(suffix)]
		}
	}
	return ""
}
