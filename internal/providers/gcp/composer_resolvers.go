package gcp

import (
	"encoding/json"
	"fmt"

	"codeberg.org/icearp/disco/internal/util"
	"codeberg.org/icearp/disco/store"
)

func init() { registerResolver(resolveComposerRelationships) }

// resolveComposerRelationships derives:
//
//   - environment -[uses]-> cryptoKey via config.encryptionConfig.kmsKeyName
//   - environment -[uses]-> service-account via config.nodeConfig.serviceAccount
//
// Composer's underlying GKE / network resources (composer-{n}-* GKE cluster,
// internal VPC) aren't direct attribute references on the environment, so
// they're omitted here; CMEK + node SA are the security-meaningful pivots.
func resolveComposerRelationships(p *project, st *store.Store) error {
	envs, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"gcp"}, AccountID: p.ID, Types: []string{TypeComposerEnv},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(envs) == 0 {
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

	saByEmail, err := buildSAEmailIndex(p, st)
	if err != nil {
		return err
	}

	for _, e := range envs {
		var a struct {
			Config struct {
				EncryptionConfig struct {
					KmsKeyName string `json:"kmsKeyName"`
				} `json:"encryptionConfig"`
				NodeConfig struct {
					ServiceAccount string `json:"serviceAccount"`
				} `json:"nodeConfig"`
			} `json:"config"`
		}
		if err := json.Unmarshal([]byte(e.AttributesJSON), &a); err != nil {
			continue
		}
		if a.Config.EncryptionConfig.KmsKeyName != "" {
			if keyID, ok := keyIDByNative[stripCryptoKeyVersion(a.Config.EncryptionConfig.KmsKeyName)]; ok {
				if err := st.UpsertRelationship(e.ID, keyID, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert env→cryptoKey: %w", err)
				}
			}
		}
		if a.Config.NodeConfig.ServiceAccount != "" {
			if saID, ok := saByEmail[a.Config.NodeConfig.ServiceAccount]; ok {
				if err := st.UpsertRelationship(e.ID, saID, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert env→SA: %w", err)
				}
			}
		}
	}
	return nil
}
