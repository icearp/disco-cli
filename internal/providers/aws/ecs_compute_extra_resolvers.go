package aws

import (
	"encoding/json"
	"fmt"

	"codeberg.org/icearp/disco/internal/util"
	"codeberg.org/icearp/disco/store"
)

func init() {
	registerResolver(
		resolveECSContainerInstanceRelationships,
		EdgeDecl{TypeECSContainerInstance, TypeEC2Instance, store.RelAttachedTo},
	)
	registerResolver(
		resolveECSTaskRelationships,
		EdgeDecl{TypeECSTask, TypeECSCluster, store.RelAttachedTo},
		EdgeDecl{TypeECSTask, TypeECSTaskDefinition, store.RelUses},
		EdgeDecl{TypeECSTask, TypeECSContainerInstance, store.RelAttachedTo},
	)
}

// resolveECSContainerInstanceRelationships wires each ECS container instance to
// the EC2 instance backing it.
func resolveECSContainerInstanceRelationships(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeECSContainerInstance}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	instSet, err := scannedIDSet(acct, st, TypeEC2Instance)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			Ec2InstanceID *string `json:"Ec2InstanceId"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if id := sv(attrs.Ec2InstanceID); id != "" {
			tgtID := store.ResourceID("aws", acct.ID, TypeEC2Instance, ec2ARN(sv(r.Region), acct.ID, "instance", id))
			if instSet[tgtID] {
				if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert ecs container-instance→ec2 instance: %w", err)
				}
			}
		}
	}
	return nil
}

// resolveECSTaskRelationships wires each running task to its cluster, task
// definition, and (for EC2-launch-type tasks) the container instance hosting it.
func resolveECSTaskRelationships(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeECSTask}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	clusterSet, err := scannedIDSet(acct, st, TypeECSCluster)
	if err != nil {
		return err
	}
	tdSet, err := scannedIDSet(acct, st, TypeECSTaskDefinition)
	if err != nil {
		return err
	}
	ciSet, err := scannedIDSet(acct, st, TypeECSContainerInstance)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			ClusterArn           *string `json:"ClusterArn"`
			TaskDefinitionArn    *string `json:"TaskDefinitionArn"`
			ContainerInstanceArn *string `json:"ContainerInstanceArn"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if arn := sv(attrs.ClusterArn); arn != "" {
			tgtID := store.ResourceID("aws", acct.ID, TypeECSCluster, arn)
			if clusterSet[tgtID] {
				if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert ecs task→cluster: %w", err)
				}
			}
		}
		if arn := sv(attrs.TaskDefinitionArn); arn != "" {
			tgtID := store.ResourceID("aws", acct.ID, TypeECSTaskDefinition, arn)
			if tdSet[tgtID] {
				if err := st.UpsertRelationship(r.ID, tgtID, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert ecs task→task-definition: %w", err)
				}
			}
		}
		if arn := sv(attrs.ContainerInstanceArn); arn != "" {
			tgtID := store.ResourceID("aws", acct.ID, TypeECSContainerInstance, arn)
			if ciSet[tgtID] {
				if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert ecs task→container-instance: %w", err)
				}
			}
		}
	}
	return nil
}
