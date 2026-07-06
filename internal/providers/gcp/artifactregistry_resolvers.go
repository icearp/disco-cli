package gcp

import (
	"encoding/json"
	"fmt"

	"codeberg.org/icearp/disco/internal/util"
	"codeberg.org/icearp/disco/store"
)

func init() { registerResolver(resolveArtifactRegistryRelationships) }

// resolveArtifactRegistryRelationships derives repository -[uses]-> cryptoKey
// CMEK edges via `kmsKeyName`. Reverse edges (GKE / Cloud Run → repository
// pull) deferred — they need image-ref parsing from container specs, which
// neither workload scanner exposes as structured fields today.
func resolveArtifactRegistryRelationships(p *project, st *store.Store) error {
	repos, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"gcp"}, AccountID: p.ID, Types: []string{TypeArtifactRepository},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(repos) == 0 {
		return nil
	}
	keys, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"gcp"}, AccountID: p.ID, Types: []string{TypeKMSCryptoKey},
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
