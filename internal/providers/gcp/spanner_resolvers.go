package gcp

import (
	"encoding/json"
	"fmt"

	"codeberg.org/icearp/disco/internal/util"
	"codeberg.org/icearp/disco/store"
)

// Resolver Wave R13 of the resolver-implementation backlog (ROADMAP.md
// "Resolver buildout"): Cloud Spanner. Instance/InstanceConfig/
// InstancePartition/Backup's cross-references, all full spanner/v1
// resource-name strings matching each target's own NativeID convention
// (same-API-family fields, exact match reliable per this backlog's rule)
// or, for Backup's CMEK edge, the existing cloudkms helpers
// (loadKMSCryptoKeyIndex/stripCryptoKeyVersion). Parent-child containment
// (Database→Instance, Backup→Instance, BackupSchedule/DatabaseRole→
// Database, InstancePartition→Instance) is already covered by the
// scanner's RecordHierarchyBatch/upsertWithParent closures — these
// resolvers only add edges the hierarchy walk doesn't produce. Database's
// own CMEK edge (`encryptionConfig.{kmsKeyName,kmsKeyNames[]}`) is NOT
// here — it's already owned by resolveDatabasesRelationships
// (databases_resolvers.go), which covers Bigtable/Firestore/Spanner CMEK
// in one place; this wave extended that resolver's Spanner block to also
// cover the multi-region kmsKeyNames[] form instead of adding a second,
// duplicate resolver for the same edge.
func init() {
	registerResolver(resolveSpannerInstanceRelationships,
		EdgeDecl{TypeSpannerInstance, TypeSpannerInstanceConfig, store.RelUses},
	)
	registerResolver(resolveSpannerInstanceConfigRelationships,
		EdgeDecl{TypeSpannerInstanceConfig, TypeSpannerInstanceConfig, store.RelUses},
	)
	registerResolver(resolveSpannerInstancePartitionRelationships,
		EdgeDecl{TypeSpannerInstancePartition, TypeSpannerInstanceConfig, store.RelUses},
	)
	registerResolver(resolveSpannerBackupRelationships,
		EdgeDecl{TypeSpannerBackup, TypeSpannerDatabase, store.RelUses},
		EdgeDecl{TypeSpannerBackup, TypeKMSCryptoKey, store.RelUses},
	)
}

// resolveSpannerInstanceRelationships wires Instance -> its InstanceConfig
// (`config`, full "projects/{p}/instanceConfigs/{id}" resource name).
func resolveSpannerInstanceRelationships(p *project, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"gcp"}, AccountID: p.ID, Types: []string{TypeSpannerInstance},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	scanned, err := scannedIDSet(p, st, TypeSpannerInstanceConfig)
	if err != nil {
		return err
	}
	if len(scanned) == 0 {
		return nil
	}
	for _, r := range rows {
		var attrs struct {
			Config string `json:"config"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if err := upsertIfScanned(st, scanned, r.ID, "gcp", p.ID, TypeSpannerInstanceConfig, attrs.Config, store.RelUses); err != nil {
			return err
		}
	}
	return nil
}

// resolveSpannerInstanceConfigRelationships wires a user-managed
// InstanceConfig -> the Google-managed InstanceConfig it's based on
// (`baseConfig`, full resource name; empty for Google-managed configs).
func resolveSpannerInstanceConfigRelationships(p *project, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"gcp"}, AccountID: p.ID, Types: []string{TypeSpannerInstanceConfig},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	scanned, err := scannedIDSet(p, st, TypeSpannerInstanceConfig)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			BaseConfig string `json:"baseConfig"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if err := upsertIfScanned(st, scanned, r.ID, "gcp", p.ID, TypeSpannerInstanceConfig, attrs.BaseConfig, store.RelUses); err != nil {
			return err
		}
	}
	return nil
}

// resolveSpannerInstancePartitionRelationships wires InstancePartition ->
// its InstanceConfig (`config`, full resource name — same field/format as
// Instance.Config).
func resolveSpannerInstancePartitionRelationships(p *project, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"gcp"}, AccountID: p.ID, Types: []string{TypeSpannerInstancePartition},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	scanned, err := scannedIDSet(p, st, TypeSpannerInstanceConfig)
	if err != nil {
		return err
	}
	if len(scanned) == 0 {
		return nil
	}
	for _, r := range rows {
		var attrs struct {
			Config string `json:"config"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if err := upsertIfScanned(st, scanned, r.ID, "gcp", p.ID, TypeSpannerInstanceConfig, attrs.Config, store.RelUses); err != nil {
			return err
		}
	}
	return nil
}

// resolveSpannerBackupRelationships wires Backup -> the Database it was
// taken from (`database`, full "projects/{p}/instances/{i}/databases/{d}"
// resource name) and -> any CMEK CryptoKey protecting it
// (`encryptionInfo.kmsKeyVersion` / `encryptionInformation[].kmsKeyVersion`,
// via the shared loadKMSCryptoKeyIndex/stripCryptoKeyVersion helpers).
func resolveSpannerBackupRelationships(p *project, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"gcp"}, AccountID: p.ID, Types: []string{TypeSpannerBackup},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	scannedDBs, err := scannedIDSet(p, st, TypeSpannerDatabase)
	if err != nil {
		return err
	}
	keyIDByNative, err := loadKMSCryptoKeyIndex(p, st)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			Database       string `json:"database"`
			EncryptionInfo *struct {
				KmsKeyVersion string `json:"kmsKeyVersion"`
			} `json:"encryptionInfo"`
			EncryptionInformation []struct {
				KmsKeyVersion string `json:"kmsKeyVersion"`
			} `json:"encryptionInformation"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if len(scannedDBs) > 0 {
			if err := upsertIfScanned(st, scannedDBs, r.ID, "gcp", p.ID, TypeSpannerDatabase, attrs.Database, store.RelUses); err != nil {
				return err
			}
		}
		kmsKeyVersions := make([]string, 0, len(attrs.EncryptionInformation)+1)
		if attrs.EncryptionInfo != nil {
			kmsKeyVersions = append(kmsKeyVersions, attrs.EncryptionInfo.KmsKeyVersion)
		}
		for _, ei := range attrs.EncryptionInformation {
			kmsKeyVersions = append(kmsKeyVersions, ei.KmsKeyVersion)
		}
		for _, kv := range kmsKeyVersions {
			if kv == "" {
				continue
			}
			if toID, ok := keyIDByNative[stripCryptoKeyVersion(kv)]; ok {
				if err := st.UpsertRelationship(r.ID, toID, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert backup→cryptoKey: %w", err)
				}
			}
		}
	}
	return nil
}
