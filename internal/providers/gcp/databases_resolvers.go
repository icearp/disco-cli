package gcp

import (
	"encoding/json"
	"fmt"

	"github.com/icearp/disco-cli/internal/util"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerResolver(resolveDatabasesRelationships,
		EdgeDecl{TypeBigtableCluster, TypeKMSCryptoKey, store.RelUses},
		EdgeDecl{TypeFirestoreDB, TypeKMSCryptoKey, store.RelUses},
		EdgeDecl{TypeSpannerDatabase, TypeKMSCryptoKey, store.RelUses},
		EdgeDecl{TypeSpannerBackupSchedule, TypeKMSCryptoKey, store.RelUses},
	)
}

// resolveDatabasesRelationships derives CMEK edges across the three
// database services:
//
//   - Bigtable cluster -[uses]-> cryptoKey via encryptionConfig.kmsKeyName
//   - Firestore database -[uses]-> cryptoKey via cmekConfig.kmsKeyName
//   - Spanner database -[uses]-> cryptoKey via encryptionConfig.{kmsKeyName,kmsKeyNames[]}
//     (kmsKeyNames is the multi-region form — one key per covered region)
//   - Spanner backup schedule -[uses]-> cryptoKey, same encryptionConfig
//     shape as Spanner database above (Resolver Wave R25) — Firestore's own
//     BackupSchedule sibling stays `Leaf: true`, it has no such field.
//
// Cross-project key references skipped (FK-safe).
//
// Spanner `encryptionInfo[]` (per-database key-version detail) deferred —
// duplicates the encryptionConfig key ref for graph purposes.
// No Bigtable instance-level CMEK (cluster-level only) — resolver picks up
// encryption per-cluster naturally.
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
				KmsKeyName  string   `json:"kmsKeyName"`
				KmsKeyNames []string `json:"kmsKeyNames"`
			} `json:"encryptionConfig"`
		}
		if err := json.Unmarshal([]byte(d.AttributesJSON), &a); err != nil {
			continue
		}
		if err := emit(d.ID, a.EncryptionConfig.KmsKeyName); err != nil {
			return err
		}
		for _, key := range a.EncryptionConfig.KmsKeyNames {
			if err := emit(d.ID, key); err != nil {
				return err
			}
		}
	}

	// Spanner backup schedule — same encryptionConfig shape as Spanner
	// database above (Resolver Wave R25).
	sbss, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"gcp"}, AccountID: p.ID, Types: []string{TypeSpannerBackupSchedule},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	for _, s := range sbss {
		var a struct {
			EncryptionConfig struct {
				KmsKeyName  string   `json:"kmsKeyName"`
				KmsKeyNames []string `json:"kmsKeyNames"`
			} `json:"encryptionConfig"`
		}
		if err := json.Unmarshal([]byte(s.AttributesJSON), &a); err != nil {
			continue
		}
		if err := emit(s.ID, a.EncryptionConfig.KmsKeyName); err != nil {
			return err
		}
		for _, key := range a.EncryptionConfig.KmsKeyNames {
			if err := emit(s.ID, key); err != nil {
				return err
			}
		}
	}
	return nil
}
