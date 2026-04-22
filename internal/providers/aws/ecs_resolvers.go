package aws

import (
	"encoding/json"
	"fmt"

	"codeberg.org/icearp/disco/internal/store"
	"codeberg.org/icearp/disco/internal/util"
)

func init() { registerResolver(resolveECSRelationships) }

func resolveECSRelationships(acct *account, st *store.Store) error {
	services, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeECSService},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	for _, r := range services {
		var attrs struct {
			ClusterArn     *string `json:"clusterArn"`
			TaskDefinition *string `json:"taskDefinition"` // ARN of the active task definition revision
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		// Service is attached to its cluster.
		if attrs.ClusterArn != nil {
			clusterID := store.ResourceID("aws", acct.ID, TypeECSCluster, *attrs.ClusterArn)
			if err := st.UpsertRelationship(r.ID, clusterID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert ecs-service→cluster relationship: %w", err)
			}
		}
		// Service uses a specific task definition revision.
		if attrs.TaskDefinition != nil {
			tdID := store.ResourceID("aws", acct.ID, TypeECSTaskDefinition, *attrs.TaskDefinition)
			if err := st.UpsertRelationship(r.ID, tdID, store.RelUses, "directed", nil); err != nil {
				return fmt.Errorf("upsert ecs-service→task-definition relationship: %w", err)
			}
		}
	}
	return nil
}
