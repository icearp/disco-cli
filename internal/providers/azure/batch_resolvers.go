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
		resolveBatchRelationships,
		EdgeDecl{Source: TypeBatchAccount, Target: TypeStorageStorageAccount, Kind: store.RelUses},
		EdgeDecl{Source: TypeBatchAccount, Target: TypeKeyVaultVault, Kind: store.RelUses},
	)
}

// resolveBatchRelationships derives Batch account edges:
//   - account -[uses]-> storage account via properties.autoStorage.storageAccountId
//   - account -[uses]-> key vault via properties.keyVaultReference.id
//
// Both references are full ARM resource IDs (not URIs), matched
// case-insensitively against per-sub NativeID indexes.
func resolveBatchRelationships(sub *subscription, st *store.Store) error {
	accounts, err := st.ListResources(store.ResourceFilter{
		Provider: "azure", AccountID: sub.ID,
		Types: []string{TypeBatchAccount},
		Limit: util.AllResources,
	})
	if err != nil || len(accounts) == 0 {
		return err
	}
	storageByID, err := nativeIDIndex(sub, st, TypeStorageStorageAccount)
	if err != nil {
		return err
	}
	vaultByID, err := nativeIDIndex(sub, st, TypeKeyVaultVault)
	if err != nil {
		return err
	}
	for _, a := range accounts {
		var attrs struct {
			Properties *struct {
				AutoStorage *struct {
					StorageAccountID *string `json:"storageAccountId"`
				} `json:"autoStorage"`
				KeyVaultReference *struct {
					ID *string `json:"id"`
				} `json:"keyVaultReference"`
			} `json:"properties"`
		}
		if err := json.Unmarshal([]byte(a.AttributesJSON), &attrs); err != nil || attrs.Properties == nil {
			continue
		}
		if p := attrs.Properties; p.AutoStorage != nil && p.AutoStorage.StorageAccountID != nil {
			if toID, ok := storageByID[strings.ToLower(*p.AutoStorage.StorageAccountID)]; ok {
				if err := st.UpsertRelationship(a.ID, toID, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert batch→storage: %w", err)
				}
			}
		}
		if p := attrs.Properties; p.KeyVaultReference != nil && p.KeyVaultReference.ID != nil {
			if toID, ok := vaultByID[strings.ToLower(*p.KeyVaultReference.ID)]; ok {
				if err := st.UpsertRelationship(a.ID, toID, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert batch→keyvault: %w", err)
				}
			}
		}
	}
	return nil
}

// nativeIDIndex builds a lowercased NativeID → resource-ID index for one type
// in the subscription. Use when a reference field carries a full ARM resource
// ID (case-insensitive) rather than a name or URI.
func nativeIDIndex(sub *subscription, st *store.Store, rtype string) (map[string]string, error) {
	rows, err := st.ListResources(store.ResourceFilter{
		Provider: "azure", AccountID: sub.ID,
		Types: []string{rtype},
		Limit: util.AllResources,
	})
	if err != nil {
		return nil, err
	}
	idx := make(map[string]string, len(rows))
	for _, r := range rows {
		idx[strings.ToLower(r.NativeID)] = r.ID
	}
	return idx, nil
}
