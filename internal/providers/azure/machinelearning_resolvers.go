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
		resolveMachineLearningRelationships,
		EdgeDecl{Source: TypeMachineLearningWorkspace, Target: TypeStorageStorageAccount, Kind: store.RelUses},
		EdgeDecl{Source: TypeMachineLearningWorkspace, Target: TypeKeyVaultVault, Kind: store.RelUses},
		EdgeDecl{Source: TypeMachineLearningWorkspace, Target: TypeContainerRegistryRegistry, Kind: store.RelUses},
	)
}

// resolveMachineLearningRelationships derives Azure ML workspace -[uses]->
// edges to its bound storage account, key vault, and container registry. Each
// is a full ARM resource ID on the workspace properties, matched
// case-insensitively against the per-sub NativeID index for that type.
// (applicationInsights points at microsoft.insights/components, which disco
// does not scan, so no edge is emitted for it.)
func resolveMachineLearningRelationships(sub *subscription, st *store.Store) error {
	workspaces, err := st.ListResources(store.ResourceFilter{
		Provider: "azure", AccountID: sub.ID,
		Types: []string{TypeMachineLearningWorkspace},
		Limit: util.AllResources,
	})
	if err != nil || len(workspaces) == 0 {
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
	acrByID, err := nativeIDIndex(sub, st, TypeContainerRegistryRegistry)
	if err != nil {
		return err
	}
	for _, w := range workspaces {
		var attrs struct {
			Properties *struct {
				StorageAccount    *string `json:"storageAccount"`
				KeyVault          *string `json:"keyVault"`
				ContainerRegistry *string `json:"containerRegistry"`
			} `json:"properties"`
		}
		if err := json.Unmarshal([]byte(w.AttributesJSON), &attrs); err != nil || attrs.Properties == nil {
			continue
		}
		for _, ref := range []struct {
			id  *string
			idx map[string]string
		}{
			{attrs.Properties.StorageAccount, storageByID},
			{attrs.Properties.KeyVault, vaultByID},
			{attrs.Properties.ContainerRegistry, acrByID},
		} {
			if ref.id == nil {
				continue
			}
			toID, ok := ref.idx[strings.ToLower(*ref.id)]
			if !ok {
				continue
			}
			if err := st.UpsertRelationship(w.ID, toID, store.RelUses, "directed", nil); err != nil {
				return fmt.Errorf("upsert ml-workspace→dependency: %w", err)
			}
		}
	}
	return nil
}
