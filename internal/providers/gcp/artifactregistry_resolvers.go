package gcp

import (
	"encoding/json"
	"fmt"

	"codeberg.org/icearp/disco/internal/store"
	"codeberg.org/icearp/disco/internal/util"
)

func init() { registerResolver(resolveArtifactRegistryRelationships) }

// resolveArtifactRegistryRelationships derives repository -[uses]-> cryptoKey
// CMEK edges via `kmsKeyName`. Reverse-direction edges (GKE / Cloud Run →
// repository pull) deferred — they require parsing image references out of
// container specs which neither workload scanner exposes today as
// structured fields.
func resolveArtifactRegistryRelationships(p *project, st *store.Store) error {
	repos, err := st.ListResources(store.ResourceFilter{
		Provider: "gcp", AccountID: p.ID, Types: []string{TypeArtifactRepository},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(repos) == 0 {
		return nil
	}
	keys, err := st.ListResources(store.ResourceFilter{
		Provider: "gcp", AccountID: p.ID, Types: []string{TypeKMSCryptoKey},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	keyIDByNative := make(map[string]string, len(keys))
	for _, k := range keys {
		keyIDByNative[k.NativeID] = k.ID
	}
	for _, r := range repos {
		var a struct {
			KmsKeyName string `json:"kmsKeyName"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &a); err != nil {
			continue
		}
		if a.KmsKeyName == "" {
			continue
		}
		keyID, ok := keyIDByNative[stripCryptoKeyVersion(a.KmsKeyName)]
		if !ok {
			continue
		}
		if err := st.UpsertRelationship(r.ID, keyID, store.RelUses, "directed", nil); err != nil {
			return fmt.Errorf("upsert repo→cryptoKey: %w", err)
		}
	}
	return nil
}
