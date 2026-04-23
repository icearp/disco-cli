package aws

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"codeberg.org/icearp/disco/internal/store"
	"codeberg.org/icearp/disco/internal/util"
)

// ecrImageRe matches ECR image URIs:
//
//	<accountID>.dkr.ecr.<region>.amazonaws.com/<repo>[:<tag>|@<digest>]
var ecrImageRe = regexp.MustCompile(`^(\d+)\.dkr\.ecr\.([a-z0-9-]+)\.amazonaws\.com/([^:@]+)`)

func init() {
	registerResolver(resolveECSRelationships)
	registerResolver(resolveECSTaskDefinitionRelationships)
	registerResolver(resolveECSContainerRelationships)
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

// resolveECSContainerRelationships links each task definition to ECR
// repositories (via container image URIs) and CloudWatch log groups (via
// awslogs log driver configuration).
func resolveECSContainerRelationships(acct *account, st *store.Store) error {
	tds, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeECSTaskDefinition},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	for _, r := range tds {
		region := sv(r.Region)
		var attrs struct {
			ContainerDefinitions []struct {
				Image            *string `json:"Image"`
				LogConfiguration *struct {
					LogDriver *string           `json:"LogDriver"`
					Options   map[string]string `json:"Options"`
				} `json:"LogConfiguration"`
			} `json:"ContainerDefinitions"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		seen := make(map[string]bool)
		upsert := func(targetType, nativeID string) error {
			if nativeID == "" {
				return nil
			}
			targetID := store.ResourceID("aws", acct.ID, targetType, nativeID)
			if seen[targetID] {
				return nil
			}
			seen[targetID] = true
			if err := st.UpsertRelationship(r.ID, targetID, store.RelUses, "directed", nil); err != nil {
				return fmt.Errorf("upsert ecs-td→%s relationship: %w", targetType, err)
			}
			return nil
		}
		for _, c := range attrs.ContainerDefinitions {
			// ECR image → repository edge
			if img := sv(c.Image); img != "" {
				if m := ecrImageRe.FindStringSubmatch(img); m != nil {
					imgAcct, imgRegion, repo := m[1], m[2], m[3]
					// repo may have a path; use as-is (ECR repo names can contain /)
					repo = strings.SplitN(repo, ":", 2)[0] // strip tag if somehow present
					ecrARN := fmt.Sprintf("arn:aws:ecr:%s:%s:repository/%s", imgRegion, imgAcct, repo)
					if err := upsert(TypeECRRepository, ecrARN); err != nil {
						return err
					}
				}
			}
			// awslogs log group edge
			if c.LogConfiguration != nil && sv(c.LogConfiguration.LogDriver) == "awslogs" {
				lgName := c.LogConfiguration.Options["awslogs-group"]
				if lgName != "" {
					lgNativeID := logGroupNativeIDFromName(acct.ID, region, lgName)
					if err := upsert(TypeLogsLogGroup, lgNativeID); err != nil {
						return err
					}
				}
			}
		}
	}
	return nil
}
