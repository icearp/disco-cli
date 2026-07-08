package gcp

import (
	"encoding/json"
	"fmt"
	"strings"

	"codeberg.org/icearp/disco/internal/util"
	"codeberg.org/icearp/disco/store"
)

// Resolver Wave R15 of the resolver-implementation backlog (ROADMAP.md
// "Resolver buildout"): Cloud Bigtable's secondary resources. Instance/
// Cluster CMEK is already owned by resolveDatabasesRelationships
// (databases_resolvers.go) — checked first per the Wave R13 lesson, no
// overlap here.
func init() {
	registerResolver(resolveBigtableAppProfileRelationships,
		EdgeDecl{TypeBigtableAppProfile, TypeBigtableCluster, store.RelUses},
	)
	registerResolver(resolveBigtableBackupRelationships,
		EdgeDecl{TypeBigtableBackup, TypeBigtableTable, store.RelUses},
		EdgeDecl{TypeBigtableBackup, TypeBigtableBackup, store.RelUses},
	)
	registerResolver(resolveBigtableTableRelationships,
		EdgeDecl{TypeBigtableTable, TypeBigtableBackup, store.RelUses},
	)
	registerResolver(resolveBigtableHotTabletRelationships,
		EdgeDecl{TypeBigtableHotTablet, TypeBigtableTable, store.RelAttachedTo},
	)
}

// resolveBigtableAppProfileRelationships wires AppProfile -> the Cluster(s)
// its routing policy targets: `singleClusterRouting.clusterId` (bare cluster
// ID) or `multiClusterRoutingUseAny.clusterIds[]` (bare cluster IDs). Unlike
// bareNameIndex's usual project-wide-unique targets (networks, SQL
// instances), Bigtable cluster IDs are only unique *within their instance*
// (SDK doc: "the ID to be used when referring to the new cluster within its
// instance") — two instances in the same project may both have a cluster
// named "c1". So the full instance-scoped cluster name is reconstructed
// from the AppProfile's own NativeID (which embeds its owning instance) and
// matched via an exact NativeID index, not the bare-name index.
func resolveBigtableAppProfileRelationships(p *project, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"gcp"}, AccountID: p.ID, Types: []string{TypeBigtableAppProfile},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	clusterIDByNative, err := nativeIDIndex(p, st, TypeBigtableCluster)
	if err != nil {
		return err
	}
	if len(clusterIDByNative) == 0 {
		return nil
	}
	for _, r := range rows {
		instanceNative, _, ok := strings.Cut(r.NativeID, "/appProfiles/")
		if !ok {
			continue
		}
		var attrs struct {
			SingleClusterRouting struct {
				ClusterID string `json:"clusterId"`
			} `json:"singleClusterRouting"`
			MultiClusterRoutingUseAny struct {
				ClusterIDs []string `json:"clusterIds"`
			} `json:"multiClusterRoutingUseAny"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		clusterIDs := attrs.MultiClusterRoutingUseAny.ClusterIDs
		if attrs.SingleClusterRouting.ClusterID != "" {
			clusterIDs = append(clusterIDs, attrs.SingleClusterRouting.ClusterID)
		}
		for _, cid := range clusterIDs {
			clusterNative := instanceNative + "/clusters/" + cid
			toID, ok := clusterIDByNative[clusterNative]
			if !ok {
				continue
			}
			if err := st.UpsertRelationship(r.ID, toID, store.RelUses, "directed", nil); err != nil {
				return fmt.Errorf("upsert appProfile→cluster: %w", err)
			}
		}
	}
	return nil
}

// resolveBigtableBackupRelationships wires Backup -> the Table it was taken
// from (`sourceTable`, full resource name) and -> the Backup it was copied
// from, if any (`sourceBackup`, full resource name, self-referential).
func resolveBigtableBackupRelationships(p *project, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"gcp"}, AccountID: p.ID, Types: []string{TypeBigtableBackup},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	scannedTables, err := scannedIDSet(p, st, TypeBigtableTable)
	if err != nil {
		return err
	}
	scannedBackups, err := scannedIDSet(p, st, TypeBigtableBackup)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			SourceTable  string `json:"sourceTable"`
			SourceBackup string `json:"sourceBackup"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if len(scannedTables) > 0 {
			if err := upsertIfScanned(st, scannedTables, r.ID, "gcp", p.ID, TypeBigtableTable, attrs.SourceTable, store.RelUses); err != nil {
				return err
			}
		}
		if len(scannedBackups) > 0 {
			if err := upsertIfScanned(st, scannedBackups, r.ID, "gcp", p.ID, TypeBigtableBackup, attrs.SourceBackup, store.RelUses); err != nil {
				return err
			}
		}
	}
	return nil
}

// resolveBigtableTableRelationships wires a restored Table -> the Backup it
// was restored from (`restoreInfo.backupInfo.backup`, full resource name;
// the backup may no longer exist, so a missing target is expected, not an
// error).
func resolveBigtableTableRelationships(p *project, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"gcp"}, AccountID: p.ID, Types: []string{TypeBigtableTable},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	scannedBackups, err := scannedIDSet(p, st, TypeBigtableBackup)
	if err != nil {
		return err
	}
	if len(scannedBackups) == 0 {
		return nil
	}
	for _, r := range rows {
		var attrs struct {
			RestoreInfo *struct {
				BackupInfo *struct {
					Backup string `json:"backup"`
				} `json:"backupInfo"`
			} `json:"restoreInfo"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.RestoreInfo == nil || attrs.RestoreInfo.BackupInfo == nil {
			continue
		}
		if err := upsertIfScanned(st, scannedBackups, r.ID, "gcp", p.ID, TypeBigtableBackup, attrs.RestoreInfo.BackupInfo.Backup, store.RelUses); err != nil {
			return err
		}
	}
	return nil
}

// resolveBigtableHotTabletRelationships wires HotTablet -> the Table it
// belongs to (`tableName`, full resource name).
func resolveBigtableHotTabletRelationships(p *project, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"gcp"}, AccountID: p.ID, Types: []string{TypeBigtableHotTablet},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	scannedTables, err := scannedIDSet(p, st, TypeBigtableTable)
	if err != nil {
		return err
	}
	if len(scannedTables) == 0 {
		return nil
	}
	for _, r := range rows {
		var attrs struct {
			TableName string `json:"tableName"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if err := upsertIfScanned(st, scannedTables, r.ID, "gcp", p.ID, TypeBigtableTable, attrs.TableName, store.RelAttachedTo); err != nil {
			return err
		}
	}
	return nil
}
