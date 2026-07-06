package aws

import (
	"encoding/json"
	"fmt"
	"strings"

	"codeberg.org/icearp/disco/internal/util"
	"codeberg.org/icearp/disco/store"
)

func init() {
	registerResolver(
		resolveODBAutonomousDatabaseNetwork,
		EdgeDecl{TypeODBAutonomousDatabase, TypeODBOdbNetwork, store.RelUses},
	)
	registerResolver(
		resolveODBAutonomousDatabaseBackupParent,
		EdgeDecl{TypeODBAutonomousDatabaseBackup, TypeODBAutonomousDatabase, store.RelAttachedTo},
	)
	registerResolver(
		resolveODBDbNodeCluster,
		EdgeDecl{TypeODBDbNode, TypeODBCloudVMCluster, store.RelAttachedTo},
	)
}

// resolveODBAutonomousDatabaseNetwork wires each Autonomous Database to its ODB
// network via OdbNetworkArn (= the odb-network NativeID).
func resolveODBAutonomousDatabaseNetwork(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeODBAutonomousDatabase},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	netSet, err := scannedIDSet(acct, st, TypeODBOdbNetwork)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			OdbNetworkArn *string `json:"OdbNetworkArn"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		netARN := sv(attrs.OdbNetworkArn)
		if netARN == "" {
			continue
		}
		tgtID := store.ResourceID("aws", acct.ID, TypeODBOdbNetwork, netARN)
		if !netSet[tgtID] {
			continue
		}
		if err := st.UpsertRelationship(r.ID, tgtID, store.RelUses, "directed", nil); err != nil {
			return fmt.Errorf("upsert odb autonomous-database→odb-network: %w", err)
		}
	}
	return nil
}

// resolveODBAutonomousDatabaseBackupParent wires each backup to its parent
// Autonomous Database via AutonomousDatabaseId, using an id index of scanned
// Autonomous Databases.
func resolveODBAutonomousDatabaseBackupParent(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeODBAutonomousDatabaseBackup},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	idx, err := odbAutonomousDatabaseIDIndex(acct, st)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			AutonomousDatabaseID *string `json:"AutonomousDatabaseId"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		aid := sv(attrs.AutonomousDatabaseID)
		if aid == "" {
			continue
		}
		tgtID, ok := idx[aid]
		if !ok {
			continue
		}
		if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert odb backup→autonomous-database: %w", err)
		}
	}
	return nil
}

func odbAutonomousDatabaseIDIndex(acct *account, st *store.Store) (map[string]string, error) {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeODBAutonomousDatabase},
		Limit: util.AllResources,
	})
	if err != nil {
		return nil, err
	}
	idx := make(map[string]string, len(rows))
	for _, r := range rows {
		var attrs struct {
			ID *string `json:"AutonomousDatabaseId"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if id := sv(attrs.ID); id != "" {
			idx[id] = r.ID
		}
	}
	return idx, nil
}

// resolveODBDbNodeCluster wires each DB node to its parent VM cluster. Cluster
// ARN is recovered from the synthetic NativeID ({clusterARN}/db-node/{id});
// target is FK-verified against scanned VM clusters.
func resolveODBDbNodeCluster(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeODBDbNode},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	clusterSet, err := scannedIDSet(acct, st, TypeODBCloudVMCluster)
	if err != nil {
		return err
	}
	for _, r := range rows {
		idx := strings.Index(r.NativeID, "/db-node/")
		if idx < 0 {
			continue
		}
		clusterARN := r.NativeID[:idx]
		tgtID := store.ResourceID("aws", acct.ID, TypeODBCloudVMCluster, clusterARN)
		if !clusterSet[tgtID] {
			continue
		}
		if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert odb db-node→cloud-vm-cluster: %w", err)
		}
	}
	return nil
}
