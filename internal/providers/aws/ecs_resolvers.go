package aws

import (
	"encoding/json"
	"fmt"

	"codeberg.org/icearp/disco/internal/store"
	"codeberg.org/icearp/disco/internal/util"
)

func init() {
	registerResolver(resolveECSRelationships)
	registerResolver(resolveECSTaskDefinitionRelationships)
}

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
			ClusterArn           *string `json:"ClusterArn"`
			TaskDefinition       *string `json:"TaskDefinition"` // ARN of the active task definition revision
			NetworkConfiguration *struct {
				AwsvpcConfiguration *struct {
					Subnets        []string `json:"Subnets"`
					SecurityGroups []string `json:"SecurityGroups"`
				} `json:"AwsvpcConfiguration"`
			} `json:"NetworkConfiguration"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		region := sv(r.Region)
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
		// Service → Subnet / SecurityGroup (awsvpc network mode only)
		if attrs.NetworkConfiguration != nil && attrs.NetworkConfiguration.AwsvpcConfiguration != nil {
			awsvpc := attrs.NetworkConfiguration.AwsvpcConfiguration
			for _, sn := range awsvpc.Subnets {
				if sn == "" {
					continue
				}
				subnetID := store.ResourceID("aws", acct.ID, TypeEC2Subnet, ec2ARN(region, acct.ID, "subnet", sn))
				if err := st.UpsertRelationship(r.ID, subnetID, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert ecs-service→subnet relationship: %w", err)
				}
			}
			for _, sg := range awsvpc.SecurityGroups {
				if sg == "" {
					continue
				}
				sgID := store.ResourceID("aws", acct.ID, TypeEC2SecurityGroup, ec2ARN(region, acct.ID, "security-group", sg))
				if err := st.UpsertRelationship(r.ID, sgID, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert ecs-service→security-group relationship: %w", err)
				}
			}
		}
	}
	return nil
}

// resolveECSTaskDefinitionRelationships links each task definition to its IAM
// task role and execution role.
func resolveECSTaskDefinitionRelationships(acct *account, st *store.Store) error {
	tds, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeECSTaskDefinition},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	for _, r := range tds {
		var attrs struct {
			TaskRoleArn      *string `json:"TaskRoleArn"`
			ExecutionRoleArn *string `json:"ExecutionRoleArn"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if sv(attrs.TaskRoleArn) != "" {
			roleID := store.ResourceID("aws", acct.ID, TypeIAMRole, *attrs.TaskRoleArn)
			if err := st.UpsertRelationship(r.ID, roleID, store.RelAssumes, "directed", nil); err != nil {
				return fmt.Errorf("upsert ecs-td→task-role relationship: %w", err)
			}
		}
		if sv(attrs.ExecutionRoleArn) != "" {
			roleID := store.ResourceID("aws", acct.ID, TypeIAMRole, *attrs.ExecutionRoleArn)
			if err := st.UpsertRelationship(r.ID, roleID, store.RelAssumes, "directed", nil); err != nil {
				return fmt.Errorf("upsert ecs-td→execution-role relationship: %w", err)
			}
		}
	}
	return nil
}
