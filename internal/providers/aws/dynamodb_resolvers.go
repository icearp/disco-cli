package aws

import (
	"encoding/json"
	"fmt"

	"codeberg.org/icearp/disco/internal/store"
	"codeberg.org/icearp/disco/internal/util"
)

func init() {
	registerResolver(func(acct *account, st *store.Store) error {
		return resolveDynamoDBGlobalTableRelationships(acct, st)
	})
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
