package aws

import (
	"encoding/json"
	"fmt"

	"codeberg.org/icearp/disco/internal/util"
	"codeberg.org/icearp/disco/store"
)

// resolveDynamoDBAll runs every DynamoDB sub-resolver in sequence,
// stopping at the first error.
func resolveDynamoDBAll(acct *account, st *store.Store) error {
	if err := resolveDynamoDBTableRelationships(acct, st); err != nil {
		return err
	}
	if err := resolveDynamoDBStreamRelationships(acct, st); err != nil {
		return err
	}
	if err := resolveDynamoDBGlobalTableRelationships(acct, st); err != nil {
		return err
	}
	return resolveDynamoDBBackupRelationships(acct, st)
}

func init() {
	registerResolver(
		resolveDynamoDBAll,
		EdgeDecl{TypeDynamoDBTable, TypeKMSKey, store.RelUses},
		EdgeDecl{TypeDynamoDBTable, TypeDynamoDBStream, store.RelContains},
		EdgeDecl{TypeDynamoDBGlobalTable, TypeDynamoDBTable, store.RelContains},
		EdgeDecl{TypeDynamoDBBackup, TypeDynamoDBTable, store.RelAttachedTo},
	)
}

// resolveDynamoDBBackupRelationships wires each backup to its source table
// (FK-safe — backups outlive deleted tables) via BackupSummary.TableArn.
func resolveDynamoDBBackupRelationships(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeDynamoDBBackup}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	tableSet, err := scannedIDSet(acct, st, TypeDynamoDBTable)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			TableArn *string `json:"TableArn"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if t := sv(attrs.TableArn); t != "" {
			tgtID := store.ResourceID("aws", acct.ID, t)
			if tableSet[tgtID] {
				if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert dynamodb backup→table: %w", err)
				}
			}
		}
	}
	return nil
}

// resolveDynamoDBTableRelationships links each table to its KMS key when a
// customer-managed key handles server-side encryption. SSEDescription is
// absent for the default (AWS-owned) key.
func resolveDynamoDBTableRelationships(acct *account, st *store.Store) error {
	tables, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"},
		AccountID: acct.ID,
		Types:     []string{TypeDynamoDBTable},
		Limit:     util.AllResources,
	})
	if err != nil {
		return err
	}
	for _, r := range tables {
		var attrs struct {
			SSEDescription *struct {
				KMSMasterKeyArn *string `json:"KMSMasterKeyArn"`
			} `json:"SSEDescription"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.SSEDescription == nil || sv(attrs.SSEDescription.KMSMasterKeyArn) == "" {
			continue
		}
		keyID := store.ResourceID("aws", acct.ID, *attrs.SSEDescription.KMSMasterKeyArn)
		if err := st.UpsertRelationship(r.ID, keyID, store.RelUses, "directed", nil); err != nil {
			return fmt.Errorf("upsert dynamodb table→kms: %w", err)
		}
	}
	return nil
}

// resolveDynamoDBStreamRelationships links each table that has streaming
// enabled to its DynamoDB stream via LatestStreamArn.
func resolveDynamoDBStreamRelationships(acct *account, st *store.Store) error {
	tables, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"},
		AccountID: acct.ID,
		Types:     []string{TypeDynamoDBTable},
		Limit:     util.AllResources,
	})
	if err != nil {
		return err
	}
	for _, r := range tables {
		var attrs struct {
			LatestStreamArn *string `json:"LatestStreamArn"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		arn := sv(attrs.LatestStreamArn)
		if arn == "" {
			continue
		}
		streamID := store.ResourceID("aws", acct.ID, arn)
		if err := st.UpsertRelationship(r.ID, streamID, store.RelContains, "directed", nil); err != nil {
			return fmt.Errorf("upsert dynamodb table→stream: %w", err)
		}
	}
	return nil
}

// resolveDynamoDBGlobalTableRelationships links each global table to its
// regional replica tables. ReplicationGroup holds a ReplicaArn per replica;
// each matches the TableArn of an aws:dynamodb:table resource scanned in
// that region.
func resolveDynamoDBGlobalTableRelationships(acct *account, st *store.Store) error {
	gts, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"},
		AccountID: acct.ID,
		Types:     []string{TypeDynamoDBGlobalTable},
		Limit:     util.AllResources,
	})
	if err != nil {
		return err
	}
	for _, r := range gts {
		var attrs struct {
			ReplicationGroup []struct {
				ReplicaArn *string `json:"ReplicaArn"`
			} `json:"ReplicationGroup"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		for _, replica := range attrs.ReplicationGroup {
			arn := sv(replica.ReplicaArn)
			if arn == "" {
				continue
			}
			tableID := store.ResourceID("aws", acct.ID, arn)
			if err := st.UpsertRelationship(r.ID, tableID, store.RelContains, "directed", nil); err != nil {
				return fmt.Errorf("upsert dynamodb global-table→table: %w", err)
			}
		}
	}
	return nil
}
