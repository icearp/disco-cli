package azure

import (
	"fmt"
	"strings"

	"codeberg.org/icearp/disco/internal/util"
	"codeberg.org/icearp/disco/store"
)

func init() { registerResolver(resolveFunctionAppRelationships) }

// resolveFunctionAppRelationships derives Functions-specific edges from the
// per-subscription app-settings sidecar populated by the AppService scanner:
//   - function-app -[uses]-> Storage account (via the AzureWebJobsStorage
//     connection string — both classic key-form `AccountName=foo;AccountKey=…`
//     and identity-form `AzureWebJobsStorage__accountName=foo` are recognised).
//   - function-app -[uses]-> Key Vault (via any setting value matching the
//     KV reference shape `@Microsoft.KeyVault(SecretUri=https://VAULT.vault…/…)`
//     or `@Microsoft.KeyVault(VaultName=VAULT;…)` introduced for slot
//     deployments). Sanitizer's `isReferenceURI` allowlist (R3.22) preserves
//     the URI verbatim through scrub.
//
// Application Insights edges deferred — `APPINSIGHTS_INSTRUMENTATIONKEY` /
// `APPLICATIONINSIGHTS_CONNECTION_STRING` carry the AI key/connection string
// but no ARM ID, and AI components (`Microsoft.Insights/components`) are not
// yet scanned. Add when AI scanner lands.
func resolveFunctionAppRelationships(sub *subscription, st *store.Store) error {
	settings := loadFunctionAppSettings(sub.ID)
	if len(settings) == 0 {
		return nil
	}

	storageByName, err := storageAccountNameIndex(sub, st)
	if err != nil {
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

	for siteDiscoID, kv := range settings {
		seenStorage := map[string]bool{}
		seenVault := map[string]bool{}
		for k, v := range kv {
			lk := strings.ToLower(k)
			// AzureWebJobsStorage classic connection-string form.
			if lk == "azurewebjobsstorage" {
				if name := storageAccountNameFromConnString(v); name != "" {
					if toID, ok := storageByName[strings.ToLower(name)]; ok && !seenStorage[toID] {
						seenStorage[toID] = true
						if err := st.UpsertRelationship(siteDiscoID, toID, store.RelUses, "directed", nil); err != nil {
							return fmt.Errorf("upsert function-app→storage: %w", err)
						}
					}
				}
			}
			// AzureWebJobsStorage__accountName identity form.
			if lk == "azurewebjobsstorage__accountname" && v != "" {
				if toID, ok := storageByName[strings.ToLower(v)]; ok && !seenStorage[toID] {
					seenStorage[toID] = true
					if err := st.UpsertRelationship(siteDiscoID, toID, store.RelUses, "directed", nil); err != nil {
						return fmt.Errorf("upsert function-app→storage (msi): %w", err)
					}
				}
			}
			// Any setting value carrying a KV reference URI.
			if vaultName := vaultNameFromKeyVaultReference(v); vaultName != "" {
				lvn := strings.ToLower(vaultName)
				if seenVault[lvn] {
					continue
				}
				if toID, ok := vaultByName[lvn]; ok {
					seenVault[lvn] = true
					if err := st.UpsertRelationship(siteDiscoID, toID, store.RelUses, "directed", nil); err != nil {
						return fmt.Errorf("upsert function-app→vault: %w", err)
					}
				}
			}
		}
	}
	return nil
}

// storageAccountNameIndex builds a per-sub case-insensitive map from storage
// account name → disco resource ID. Used by Functions resolver and any
// future resolver matching by storage-account name (App Service config refs,
// AKS storage-class, etc.).
func storageAccountNameIndex(sub *subscription, st *store.Store) (map[string]string, error) {
	storage, err := st.ListResources(store.ResourceFilter{
		Provider: "azure", AccountID: sub.ID,
		Types: []string{TypeStorageStorageAccount},
		Limit: util.AllResources,
	})
	if err != nil {
		return nil, err
	}
	idx := make(map[string]string, len(storage))
	for _, s := range storage {
		idx[strings.ToLower(nameFromID(s.NativeID))] = s.ID
	}
	return idx, nil
}

// storageAccountNameFromConnString parses an Azure storage connection string
// and returns the AccountName field, or "" if not found. Connection strings
// are semicolon-delimited key=value pairs (`DefaultEndpointsProtocol=https;
// AccountName=foo;AccountKey=...;EndpointSuffix=core.windows.net`).
func storageAccountNameFromConnString(s string) string {
	for _, part := range strings.Split(s, ";") {
		k, v, ok := strings.Cut(part, "=")
		if !ok {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(k), "AccountName") {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// vaultNameFromKeyVaultReference extracts the vault name from an App Service
// Key Vault reference of either form:
//
//	@Microsoft.KeyVault(SecretUri=https://VAULT.vault.azure.net/secrets/NAME[/VER])
//	@Microsoft.KeyVault(VaultName=VAULT;SecretName=NAME[;SecretVersion=VER])
//
// Returns "" if s is not a Key Vault reference. The full surrounding string
// must start with `@Microsoft.KeyVault(` so plain URIs in unrelated settings
// don't accidentally produce edges.
func vaultNameFromKeyVaultReference(s string) string {
	const prefix = "@Microsoft.KeyVault("
	if !strings.HasPrefix(s, prefix) {
		return ""
	}
	body := strings.TrimSuffix(s[len(prefix):], ")")
	for _, part := range strings.Split(body, ";") {
		k, v, ok := strings.Cut(part, "=")
		if !ok {
			continue
		}
		switch strings.TrimSpace(k) {
		case "SecretUri":
			return vaultNameFromKeyURI(strings.TrimSpace(v))
		case "VaultName":
			return strings.TrimSpace(v)
		}
	}
	return ""
}
