package aws

import (
	"encoding/json"
	"fmt"

	"codeberg.org/icearp/disco/internal/store"
	"codeberg.org/icearp/disco/internal/util"
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
	return resolveDynamoDBGlobalTableRelationships(acct, st)
}

func init() {
	registerResolver(
		resolveDynamoDBAll,
		EdgeDecl{TypeDynamoDBTable, TypeKMSKey, store.RelUses},
		EdgeDecl{TypeDynamoDBTable, TypeDynamoDBStream, store.RelContains},
		EdgeDecl{TypeDynamoDBGlobalTable, TypeDynamoDBTable, store.RelContains},
	)
}

// resolveDynamoDBTableRelationships links each table to its KMS key when a
// customer-managed key is used for server-side encryption. SSEDescription is
// absent when the table uses the default (AWS-owned) key.
func resolveDynamoDBTableRelationships(acct *account, st *store.Store) error {
	tables, err := st.ListResources(store.ResourceFilter{
		Provider:  "aws",
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
		keyID := store.ResourceID("aws", acct.ID, TypeKMSKey, *attrs.SSEDescription.KMSMasterKeyArn)
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
		Provider:  "aws",
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
		streamID := store.ResourceID("aws", acct.ID, TypeDynamoDBStream, arn)
		if err := st.UpsertRelationship(r.ID, streamID, store.RelContains, "directed", nil); err != nil {
			return fmt.Errorf("upsert dynamodb table→stream: %w", err)
		}
	}
	return nil
}

// resolveDynamoDBGlobalTableRelationships links each global table to the
// regional replica tables it contains. The ReplicationGroup field in the
// global table's attributes holds a ReplicaArn for each replica; each ARN
// matches the TableArn of an aws:dynamodb:table resource scanned in that region.
func resolveDynamoDBGlobalTableRelationships(acct *account, st *store.Store) error {
	gts, err := st.ListResources(store.ResourceFilter{
		Provider:  "aws",
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
			tableID := store.ResourceID("aws", acct.ID, TypeDynamoDBTable, arn)
			if err := st.UpsertRelationship(r.ID, tableID, store.RelContains, "directed", nil); err != nil {
				return fmt.Errorf("upsert dynamodb global-table→table: %w", err)
			}
		}
	}
	return nil
}
