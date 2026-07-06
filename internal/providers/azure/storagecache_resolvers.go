package azure

import (
	"encoding/json"
	"fmt"
	"strings"

	"codeberg.org/icearp/disco/internal/util"
	"codeberg.org/icearp/disco/store"
)

func init() {
	registerResolver(
		resolveStorageCacheRelationships,
		EdgeDecl{Source: TypeStorageCacheCache, Target: TypeNetworkVirtualNetwork, Kind: store.RelAttachedTo},
		EdgeDecl{Source: TypeStorageCacheCache, Target: TypeKeyVaultVault, Kind: store.RelUses},
	)
}

// resolveStorageCacheRelationships derives Azure HPC Cache edges:
//   - cache -[attached-to]-> VNet via properties.subnet
//   - cache -[uses]-> Key Vault via the CMK
//     properties.encryptionSettings.keyEncryptionKey.sourceVault.id (full vault ARM ID).
//     sourceVault is SDK-required whenever a CMK is set, so the ARM-ID match
//     suffices — no keyUrl fallback needed.
func resolveStorageCacheRelationships(sub *subscription, st *store.Store) error {
	caches, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"azure"}, AccountID: sub.ID,
		Types: []string{TypeStorageCacheCache},
		Limit: util.AllResources,
	})
	if err != nil || len(caches) == 0 {
		return err
	}
	vnetByID, err := nativeIDIndex(sub, st, TypeNetworkVirtualNetwork)
	if err != nil {
		return err
	}
	vaultByID, err := nativeIDIndex(sub, st, TypeKeyVaultVault)
	if err != nil {
		return err
	}
	for _, c := range caches {
		var attrs struct {
			Properties *struct {
				Subnet             *string `json:"subnet"`
				EncryptionSettings *struct {
					KeyEncryptionKey *struct {
						SourceVault *struct {
							ID *string `json:"id"`
						} `json:"sourceVault"`
					} `json:"keyEncryptionKey"`
				} `json:"encryptionSettings"`
			} `json:"properties"`
		}
		if err := json.Unmarshal([]byte(c.AttributesJSON), &attrs); err != nil || attrs.Properties == nil {
			continue
		}
		if attrs.Properties.Subnet != nil {
			if err := upsertVNetAttachment(st, c.ID, *attrs.Properties.Subnet, vnetByID); err != nil {
				return err
			}
		}
		if es := attrs.Properties.EncryptionSettings; es != nil && es.KeyEncryptionKey != nil &&
			es.KeyEncryptionKey.SourceVault != nil && es.KeyEncryptionKey.SourceVault.ID != nil {
			if toID, ok := vaultByID[strings.ToLower(*es.KeyEncryptionKey.SourceVault.ID)]; ok {
				if err := st.UpsertRelationship(c.ID, toID, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert storagecache→keyvault: %w", err)
				}
			}
		}
	}
	return nil
}
