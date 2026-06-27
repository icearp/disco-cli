package gcp

import (
	"encoding/json"
	"fmt"

	"codeberg.org/icearp/disco/internal/util"
	"codeberg.org/icearp/disco/store"
)

func init() { registerResolver(resolveDatabasesRelationships) }

// resolveDatabasesRelationships derives CMEK edges across the three
// database services:
//
//   - Bigtable cluster -[uses]-> cryptoKey via encryptionConfig.kmsKeyName
//   - Firestore database -[uses]-> cryptoKey via cmekConfig.kmsKeyName
//   - Spanner database -[uses]-> cryptoKey via encryptionConfig.kmsKeyName
//
// Cross-project key references skipped (FK-safe).
//
// Spanner `encryptionInfo[]` (per-database key version detail) deferred —
// it duplicates the encryptionConfig key reference for graph purposes.
// Bigtable instance-level CMEK doesn't exist (only cluster-level), so the
// resolver picks up encryption per-cluster naturally.
func resolveDatabasesRelationships(p *project, st *store.Store) error {
	keys, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"gcp"}, AccountID: p.ID, Types: []string{TypeKMSCryptoKey},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(keys) == 0 {
		return nil
	}
	keyIDByNative := make(map[string]string, len(keys))
	for _, k := range keys {
		keyIDByNative[k.NativeID] = k.ID
	}

	emit := func(fromID, rawKey string) error {
		key := stripCryptoKeyVersion(rawKey)
		if key == "" {
			return nil
		}
		keyID, ok := keyIDByNative[key]
		if !ok {
			return nil
		}
		if err := st.UpsertRelationship(fromID, keyID, store.RelUses, "directed", nil); err != nil {
			return fmt.Errorf("upsert db→cryptoKey: %w", err)
		}
		return nil
	}

	// Bigtable cluster.
	bcs, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"gcp"}, AccountID: p.ID, Types: []string{TypeBigtableCluster},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	for _, c := range bcs {
		var a struct {
			EncryptionConfig struct {
				KmsKeyName string `json:"kmsKeyName"`
			} `json:"encryptionConfig"`
		}
		if err := json.Unmarshal([]byte(c.AttributesJSON), &a); err != nil {
			continue
		}
		if err := emit(c.ID, a.EncryptionConfig.KmsKeyName); err != nil {
			return err
		}
	}

	// Firestore database.
	fdbs, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"gcp"}, AccountID: p.ID, Types: []string{TypeFirestoreDB},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	for _, d := range fdbs {
		var a struct {
			CmekConfig struct {
				KmsKeyName string `json:"kmsKeyName"`
			} `json:"cmekConfig"`
		}
		if err := json.Unmarshal([]byte(d.AttributesJSON), &a); err != nil {
			continue
		}
		if err := emit(d.ID, a.CmekConfig.KmsKeyName); err != nil {
			return err
		}
	}

	// Spanner database.
	sdbs, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"gcp"}, AccountID: p.ID, Types: []string{TypeSpannerDatabase},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	for _, d := range sdbs {
		var a struct {
			EncryptionConfig struct {
				KmsKeyName string `json:"kmsKeyName"`
			} `json:"encryptionConfig"`
		}
		if err := json.Unmarshal([]byte(d.AttributesJSON), &a); err != nil {
			continue
		}
		if err := emit(d.ID, a.EncryptionConfig.KmsKeyName); err != nil {
			return err
		}
	}
	return nil
}
