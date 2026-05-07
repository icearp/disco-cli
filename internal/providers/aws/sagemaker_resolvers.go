package aws

import (
	"encoding/json"
	"fmt"

	"codeberg.org/icearp/disco/internal/store"
	"codeberg.org/icearp/disco/internal/util"
)

func init() {
	registerResolver(
		resolveSageMakerEndpointConfig,
		EdgeDecl{TypeSageMakerEndpoint, TypeSageMakerEndpointConfig, store.RelAttachedTo},
	)
	registerResolver(
		resolveSageMakerEndpointConfigModels,
		EdgeDecl{TypeSageMakerEndpointConfig, TypeSageMakerModel, store.RelAttachedTo},
	)
	registerResolver(
		resolveSageMakerModelRefs,
		EdgeDecl{TypeSageMakerModel, TypeIAMRole, store.RelAssumes},
		EdgeDecl{TypeSageMakerModel, TypeECRRepository, store.RelUses},
		EdgeDecl{TypeSageMakerModel, TypeEC2VPC, store.RelAttachedTo},
		EdgeDecl{TypeSageMakerModel, TypeEC2Subnet, store.RelAttachedTo},
		EdgeDecl{TypeSageMakerModel, TypeEC2SecurityGroup, store.RelUses},
	)
}

// sagemakerEndpointConfigARN rebuilds the canonical ARN from an
// EndpointConfigName. SageMaker's ARN shape is the same for endpoints,
// endpoint-configs, and models — `arn:aws:sagemaker:{r}:{a}:{kind}/{name}`.
func sagemakerEndpointConfigARN(region, acctID, name string) string {
	return fmt.Sprintf("arn:aws:sagemaker:%s:%s:endpoint-config/%s", region, acctID, name)
}

// sagemakerModelARN — same shape as endpoint-config but `:model/` segment.
func sagemakerModelARN(region, acctID, name string) string {
	return fmt.Sprintf("arn:aws:sagemaker:%s:%s:model/%s", region, acctID, name)
}

// resolveSageMakerEndpointConfig links each endpoint to the endpoint-config
// it activates. The endpoint scanner stores `EndpointConfigName`; we synth
// the canonical ARN to look up the local endpoint-config row.
func resolveSageMakerEndpointConfig(acct *account, st *store.Store) error {
	endpoints, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeSageMakerEndpoint},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	configSet, err := scannedIDSet(acct, st, TypeSageMakerEndpointConfig)
	if err != nil {
		return err
	}
	for _, r := range endpoints {
		var attrs struct {
			EndpointConfigName *string `json:"EndpointConfigName"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.EndpointConfigName == nil || *attrs.EndpointConfigName == "" {
			continue
		}
		cfgID := store.ResourceID("aws", acct.ID, TypeSageMakerEndpointConfig,
			sagemakerEndpointConfigARN(sv(r.Region), acct.ID, *attrs.EndpointConfigName))
		if !configSet[cfgID] {
			continue
		}
		if err := st.UpsertRelationship(r.ID, cfgID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert sagemaker-endpoint→endpoint-config: %w", err)
		}
	}
	return nil
}

// resolveSageMakerEndpointConfigModels links each endpoint-config to the
// models named in its `ProductionVariants[].ModelName` array. A single
// endpoint-config can fan-out to N models (multi-variant deployments).
func resolveSageMakerEndpointConfigModels(acct *account, st *store.Store) error {
	configs, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeSageMakerEndpointConfig},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	modelSet, err := scannedIDSet(acct, st, TypeSageMakerModel)
	if err != nil {
		return err
	}
	for _, r := range configs {
		var attrs struct {
			ProductionVariants []struct {
				ModelName *string `json:"ModelName"`
			} `json:"ProductionVariants"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		region := sv(r.Region)
		for _, v := range attrs.ProductionVariants {
			if v.ModelName == nil || *v.ModelName == "" {
				continue
			}
			modelID := store.ResourceID("aws", acct.ID, TypeSageMakerModel,
				sagemakerModelARN(region, acct.ID, *v.ModelName))
			if !modelSet[modelID] {
				continue
			}
			if err := st.UpsertRelationship(r.ID, modelID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert sagemaker-endpoint-config→model: %w", err)
			}
		}
	}
	return nil
}

// resolveSageMakerModelRefs walks every Model row's outbound refs:
// ExecutionRoleArn → IAM role; Containers[].Image → ECR repository (via
// the apprunnerImageToRepoARN helper, shared across services that store
// container image URLs); VpcConfig.{VpcId, Subnets[], SecurityGroupIDs[]}
// → EC2 networking targets.
func resolveSageMakerModelRefs(acct *account, st *store.Store) error {
	models, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeSageMakerModel},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	roleSet, err := scannedIDSet(acct, st, TypeIAMRole)
	if err != nil {
		return err
	}
	repoSet, err := scannedIDSet(acct, st, TypeECRRepository)
	if err != nil {
		return err
	}
	vpcSet, err := scannedIDSet(acct, st, TypeEC2VPC)
	if err != nil {
		return err
	}
	subnetSet, err := scannedIDSet(acct, st, TypeEC2Subnet)
	if err != nil {
		return err
	}
	sgSet, err := scannedIDSet(acct, st, TypeEC2SecurityGroup)
	if err != nil {
		return err
	}
	for _, r := range models {
		var attrs struct {
			ExecutionRoleArn *string `json:"ExecutionRoleArn"`
			Containers       []struct {
				Image *string `json:"Image"`
			} `json:"Containers"`
			PrimaryContainer *struct {
				Image *string `json:"Image"`
			} `json:"PrimaryContainer"`
			VpcConfig *struct {
				Subnets          []string `json:"Subnets"`
				SecurityGroupIDs []string `json:"SecurityGroupIds"`
			} `json:"VpcConfig"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		region := sv(r.Region)
		// IAM execution role
		if attrs.ExecutionRoleArn != nil && *attrs.ExecutionRoleArn != "" {
			roleID := store.ResourceID("aws", acct.ID, TypeIAMRole, *attrs.ExecutionRoleArn)
			if roleSet[roleID] {
				if err := st.UpsertRelationship(r.ID, roleID, store.RelAssumes, "directed", nil); err != nil {
					return fmt.Errorf("upsert sagemaker-model→role: %w", err)
				}
			}
		}
		// Container images → ECR repos
		images := make([]string, 0, len(attrs.Containers)+1)
		for _, c := range attrs.Containers {
			if c.Image != nil {
				images = append(images, *c.Image)
			}
		}
		if attrs.PrimaryContainer != nil && attrs.PrimaryContainer.Image != nil {
			images = append(images, *attrs.PrimaryContainer.Image)
		}
		for _, img := range images {
			repoARN := apprunnerImageToRepoARN(img)
			if repoARN == "" {
				continue
			}
			repoID := store.ResourceID("aws", acct.ID, TypeECRRepository, repoARN)
			if !repoSet[repoID] {
				continue
			}
			if err := st.UpsertRelationship(r.ID, repoID, store.RelUses, "directed", nil); err != nil {
				return fmt.Errorf("upsert sagemaker-model→ecr-repo: %w", err)
			}
		}
		// VPC config — note: SageMaker's VpcConfig has no top-level VpcId; the
		// VPC is derived from the subnets. Skip the VPC edge here; subnet/SG
		// edges suffice. Subnets[] and SecurityGroupIDs[] are []*string in the
		// SDK; JSON-marshalled they appear as []string with bare IDs.
		if attrs.VpcConfig == nil {
			continue
		}
		for _, sn := range attrs.VpcConfig.Subnets {
			if sn == "" {
				continue
			}
			snID := store.ResourceID("aws", acct.ID, TypeEC2Subnet,
				ec2ARN(region, acct.ID, "subnet", sn))
			if !subnetSet[snID] {
				continue
			}
			if err := st.UpsertRelationship(r.ID, snID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert sagemaker-model→subnet: %w", err)
			}
		}
		for _, sg := range attrs.VpcConfig.SecurityGroupIDs {
			if sg == "" {
				continue
			}
			sgID := store.ResourceID("aws", acct.ID, TypeEC2SecurityGroup,
				ec2ARN(region, acct.ID, "security-group", sg))
			if !sgSet[sgID] {
				continue
			}
			if err := st.UpsertRelationship(r.ID, sgID, store.RelUses, "directed", nil); err != nil {
				return fmt.Errorf("upsert sagemaker-model→sg: %w", err)
			}
		}
		// Skip VPC edge per comment above; vpcSet kept for symmetry/future use.
		_ = vpcSet
	}
	return nil
}
