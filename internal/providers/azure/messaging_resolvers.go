package azure

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"codeberg.org/icearp/disco/internal/util"
	"codeberg.org/icearp/disco/store"
)

func init() { registerResolver(resolveMessagingRelationships) }

// resolveMessagingRelationships derives Event Hubs + Service Bus namespace
// -[uses]-> Key Vault edges for CMEK-enabled namespaces. Both services
// expose CMEK as `properties.encryption.keyVaultProperties[]` with each entry
// holding a separate `keyVaultUri` field (a vault DNS root, NOT a full key
// URI), so the resolver parses the URI host directly rather than reusing the
// key-URI helper. Multiple keyVaultProperties entries on one namespace
// collapse to one edge per (namespace, vault).
func resolveMessagingRelationships(sub *subscription, st *store.Store) error {
	namespaces, err := st.ListResources(store.ResourceFilter{
		Provider: "azure", AccountID: sub.ID,
		Types: []string{TypeEventHubNamespace, TypeServiceBusNamespace},
		Limit: util.AllResources,
	})
	if err != nil || len(namespaces) == 0 {
		return err
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
	if len(vaultByName) == 0 {
		return nil
	}

	for _, r := range namespaces {
		var attrs struct {
			Properties *struct {
				Encryption *struct {
					KeyVaultProperties []struct {
						KeyVaultURI *string `json:"keyVaultUri"`
					} `json:"keyVaultProperties"`
				} `json:"encryption"`
			} `json:"properties"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil || attrs.Properties == nil || attrs.Properties.Encryption == nil {
			continue
		}
		seen := map[string]bool{}
		for _, kvp := range attrs.Properties.Encryption.KeyVaultProperties {
			if kvp.KeyVaultURI == nil {
				continue
			}
			vaultName := vaultNameFromVaultURI(*kvp.KeyVaultURI)
			if vaultName == "" || seen[vaultName] {
				continue
			}
			seen[vaultName] = true
			toID, ok := vaultByName[strings.ToLower(vaultName)]
			if !ok {
				continue
			}
			if err := st.UpsertRelationship(r.ID, toID, store.RelUses, "directed", nil); err != nil {
				return fmt.Errorf("upsert messaging→keyvault: %w", err)
			}
		}
	}
	return nil
}

// vaultNameFromVaultURI extracts the vault subdomain from a Key Vault DNS root
// URI like "https://{vault}.vault.azure.net/" (no key/secret path).
// Counterpart to vaultNameFromKeyURI which parses full key URIs; both share
// the same DNS-suffix list.
func vaultNameFromVaultURI(uri string) string {
	u, err := url.Parse(uri)
	if err != nil || u.Host == "" {
		return ""
	}
	host := strings.ToLower(u.Host)
	for _, suffix := range []string{".vault.azure.net", ".vault.usgovcloudapi.net", ".vault.azure.cn", ".vault.microsoftazure.de"} {
		if strings.HasSuffix(host, suffix) {
			return host[:len(host)-len(suffix)]
		}
	}
	return ""
}
