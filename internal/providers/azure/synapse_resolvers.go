package azure

import (
	"encoding/json"
	"fmt"
	"strings"

	"codeberg.org/icearp/disco/internal/util"
	"codeberg.org/icearp/disco/store"
)

func init() {
	registerResolver(resolveSynapseRelationships,
		EdgeDecl{Source: TypeSynapseWorkspace, Target: TypeStorageStorageAccount, Kind: store.RelUses},
	)
}

// resolveSynapseRelationships derives Synapse workspace -[uses]-> Storage
// account edge for the workspace's default ADLS Gen2 backing store via
// `properties.defaultDataLakeStorage.resourceId`. Match case-insensitive
// against per-sub Storage NativeID index.
func resolveSynapseRelationships(sub *subscription, st *store.Store) error {
	workspaces, err := st.ListResources(store.ResourceFilter{
		Provider: "azure", AccountID: sub.ID,
		Types: []string{TypeSynapseWorkspace},
		Limit: util.AllResources,
	})
	if err != nil || len(workspaces) == 0 {
		return err
	}
	storage, err := st.ListResources(store.ResourceFilter{
		Provider: "azure", AccountID: sub.ID,
		Types: []string{TypeStorageStorageAccount},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	storageIndex := make(map[string]string, len(storage))
	for _, s := range storage {
		storageIndex[strings.ToLower(s.NativeID)] = s.ID
	}

	for _, w := range workspaces {
		var attrs struct {
			Properties *struct {
				DefaultDataLakeStorage *struct {
					ResourceID *string `json:"resourceId"`
				} `json:"defaultDataLakeStorage"`
			} `json:"properties"`
		}
		if err := json.Unmarshal([]byte(w.AttributesJSON), &attrs); err != nil ||
			attrs.Properties == nil ||
			attrs.Properties.DefaultDataLakeStorage == nil ||
			attrs.Properties.DefaultDataLakeStorage.ResourceID == nil {
			continue
		}
		toID, ok := storageIndex[strings.ToLower(*attrs.Properties.DefaultDataLakeStorage.ResourceID)]
		if !ok {
			continue
		}
		if err := st.UpsertRelationship(w.ID, toID, store.RelUses, "directed", nil); err != nil {
			return fmt.Errorf("upsert synapse→storage: %w", err)
		}
	}
	return nil
}
