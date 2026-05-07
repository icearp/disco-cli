package aws

import (
	"encoding/json"
	"fmt"

	"codeberg.org/icearp/disco/internal/store"
	"codeberg.org/icearp/disco/internal/util"
)

func init() {
	registerResolver(
		resolveBatchComputeEnvironmentTargets,
		EdgeDecl{TypeBatchComputeEnvironment, TypeIAMRole, store.RelAssumes},
		EdgeDecl{TypeBatchComputeEnvironment, TypeEC2Subnet, store.RelUses},
		EdgeDecl{TypeBatchComputeEnvironment, TypeEC2SecurityGroup, store.RelUses},
	)
	registerResolver(
		resolveBatchJobQueueComputeEnvs,
		EdgeDecl{TypeBatchJobQueue, TypeBatchComputeEnvironment, store.RelUses},
	)
	registerResolver(
		resolveBatchJobDefinitionTargets,
		EdgeDecl{TypeBatchJobDefinition, TypeIAMRole, store.RelAssumes},
		EdgeDecl{TypeBatchJobDefinition, TypeECRRepository, store.RelUses},
	)
	registerResolver(
		resolveBatchQuotaShareJobQueue,
		EdgeDecl{TypeBatchQuotaShare, TypeBatchJobQueue, store.RelAttachedTo},
	)
}

// batchComputeEnvAttrs mirrors verbatim ComputeEnvironmentDetail fields.
type batchComputeEnvAttrs struct {
	ServiceRole      *string `json:"ServiceRole"`
	ComputeResources *struct {
		InstanceRole     *string  `json:"InstanceRole"`
		Subnets          []string `json:"Subnets"`
		SecurityGroupIDs []string `json:"SecurityGroupIDs"`
	} `json:"ComputeResources"`
}

// resolveBatchComputeEnvironmentTargets emits compute-env outbound edges:
//   - compute-env → IAM service role (assumes) via ServiceRole
//   - compute-env → IAM instance role (assumes) via ComputeResources.InstanceRole
//   - compute-env → subnet (uses) per ComputeResources.Subnets[]
//   - compute-env → security group (uses) per ComputeResources.SecurityGroupIDs[]
//
// FK-safe via per-type id sets. Cross-account refs skip silently.
// Fargate compute-envs have no ComputeResources.InstanceRole/SecurityGroups —
// the nil-check on ComputeResources covers that path.
func resolveBatchComputeEnvironmentTargets(acct *account, st *store.Store) error {
	envs, err := st.ListResources(store.ResourceFilter{
		Provider:  "aws",
		AccountID: acct.ID,
		Types:     []string{TypeBatchComputeEnvironment},
		Limit:     util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(envs) == 0 {
		return nil
	}

	roleIDs, err := resourceIDSet(st, acct.ID, TypeIAMRole)
	if err != nil {
		return err
	}
	subnetIDs, err := resourceIDSet(st, acct.ID, TypeEC2Subnet)
	if err != nil {
		return err
	}
	sgIDs, err := resourceIDSet(st, acct.ID, TypeEC2SecurityGroup)
	if err != nil {
		return err
	}

	for _, e := range envs {
		var attrs batchComputeEnvAttrs
		if err := json.Unmarshal([]byte(e.AttributesJSON), &attrs); err != nil {
			continue
		}
		region := ""
		if e.Region != nil {
			region = *e.Region
		}

		// Service role: ARN form. Note Batch InstanceRole is also an ARN
		// (instance profile or role). Both treated as IAM-role refs;
		// instance-profile ARNs simply skip (FK-safe via roleIDs).
		for _, roleARN := range []string{
			sv(attrs.ServiceRole),
			func() string {
				if attrs.ComputeResources != nil {
					return sv(attrs.ComputeResources.InstanceRole)
				}
				return ""
			}(),
		} {
			if roleARN == "" {
				continue
			}
			rID := store.ResourceID("aws", acct.ID, TypeIAMRole, roleARN)
			if _, ok := roleIDs[rID]; ok {
				if err := st.UpsertRelationship(e.ID, rID, store.RelAssumes, "directed", nil); err != nil {
					return fmt.Errorf("upsert batch compute-env→iam role: %w", err)
				}
			}
		}

		if attrs.ComputeResources != nil {
			for _, sn := range attrs.ComputeResources.Subnets {
				if sn == "" {
					continue
				}
				snARN := ec2ARN(region, acct.ID, "subnet", sn)
				sID := store.ResourceID("aws", acct.ID, TypeEC2Subnet, snARN)
				if _, ok := subnetIDs[sID]; ok {
					if err := st.UpsertRelationship(e.ID, sID, store.RelUses, "directed", nil); err != nil {
						return fmt.Errorf("upsert batch compute-env→subnet: %w", err)
					}
				}
			}
			for _, sg := range attrs.ComputeResources.SecurityGroupIDs {
				if sg == "" {
					continue
				}
				sgARN := ec2ARN(region, acct.ID, "security-group", sg)
				rID := store.ResourceID("aws", acct.ID, TypeEC2SecurityGroup, sgARN)
				if _, ok := sgIDs[rID]; ok {
					if err := st.UpsertRelationship(e.ID, rID, store.RelUses, "directed", nil); err != nil {
						return fmt.Errorf("upsert batch compute-env→sg: %w", err)
					}
				}
			}
		}
	}
	return nil
}

// batchJobQueueAttrs mirrors verbatim JobQueueDetail fields.
type batchJobQueueAttrs struct {
	ComputeEnvironmentOrder []struct {
		Order              *int32  `json:"Order"`
		ComputeEnvironment *string `json:"ComputeEnvironment"`
	} `json:"ComputeEnvironmentOrder"`
}

// resolveBatchJobQueueComputeEnvs emits job-queue → compute-env (uses)
// edges. Compute-env order preserved as edge attrs `{"order":N}` so
// graph consumers can reconstruct the dispatch priority.
func resolveBatchJobQueueComputeEnvs(acct *account, st *store.Store) error {
	queues, err := st.ListResources(store.ResourceFilter{
		Provider:  "aws",
		AccountID: acct.ID,
		Types:     []string{TypeBatchJobQueue},
		Limit:     util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(queues) == 0 {
		return nil
	}

	envIDs, err := resourceIDSet(st, acct.ID, TypeBatchComputeEnvironment)
	if err != nil {
		return err
	}
	if len(envIDs) == 0 {
		return nil
	}

	for _, q := range queues {
		var attrs batchJobQueueAttrs
		if err := json.Unmarshal([]byte(q.AttributesJSON), &attrs); err != nil {
			continue
		}
		for _, ceo := range attrs.ComputeEnvironmentOrder {
			ceARN := sv(ceo.ComputeEnvironment)
			if ceARN == "" {
				continue
			}
			eID := store.ResourceID("aws", acct.ID, TypeBatchComputeEnvironment, ceARN)
			if _, ok := envIDs[eID]; !ok {
				continue
			}
			var attrsJSON *string
			if ceo.Order != nil {
				j := fmt.Sprintf(`{"order":%d}`, *ceo.Order)
				attrsJSON = &j
			}
			if err := st.UpsertRelationship(q.ID, eID, store.RelUses, "directed", attrsJSON); err != nil {
				return fmt.Errorf("upsert batch job-queue→compute-env: %w", err)
			}
		}
	}
	return nil
}

// batchJobDefAttrs mirrors verbatim JobDefinition fields used by the
// resolver. Nests under ContainerProperties (single-container jobs).
// Multi-node parallel + ECS task-properties variants deferred.
type batchJobDefAttrs struct {
	ContainerProperties *struct {
		Image            *string `json:"Image"`
		JobRoleArn       *string `json:"JobRoleArn"`
		ExecutionRoleArn *string `json:"ExecutionRoleArn"`
	} `json:"ContainerProperties"`
}

// resolveBatchJobDefinitionTargets emits job-definition outbound edges:
//   - job-definition → IAM job role (assumes) via ContainerProperties.JobRoleArn
//   - job-definition → IAM execution role (assumes) via ContainerProperties.ExecutionRoleArn
//   - job-definition → ECR repository (uses) via ContainerProperties.Image
//     (parsed via existing apprunnerImageToRepoARN helper — same URL shape)
//
// FK-safe via per-type id sets. Multi-node parallel jobs (NodeProperties)
// + ECS task-properties variant + EKS pod-properties variant deferred.
func resolveBatchJobDefinitionTargets(acct *account, st *store.Store) error {
	defs, err := st.ListResources(store.ResourceFilter{
		Provider:  "aws",
		AccountID: acct.ID,
		Types:     []string{TypeBatchJobDefinition},
		Limit:     util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(defs) == 0 {
		return nil
	}

	roleIDs, err := resourceIDSet(st, acct.ID, TypeIAMRole)
	if err != nil {
		return err
	}
	repoIDs, err := resourceIDSet(st, acct.ID, TypeECRRepository)
	if err != nil {
		return err
	}

	for _, d := range defs {
		var attrs batchJobDefAttrs
		if err := json.Unmarshal([]byte(d.AttributesJSON), &attrs); err != nil {
			continue
		}
		if attrs.ContainerProperties == nil {
			continue
		}
		cp := attrs.ContainerProperties

		for _, roleARN := range []string{sv(cp.JobRoleArn), sv(cp.ExecutionRoleArn)} {
			if roleARN == "" {
				continue
			}
			rID := store.ResourceID("aws", acct.ID, TypeIAMRole, roleARN)
			if _, ok := roleIDs[rID]; ok {
				if err := st.UpsertRelationship(d.ID, rID, store.RelAssumes, "directed", nil); err != nil {
					return fmt.Errorf("upsert batch job-def→iam role: %w", err)
				}
			}
		}

		if repoARN := apprunnerImageToRepoARN(sv(cp.Image)); repoARN != "" {
			rID := store.ResourceID("aws", acct.ID, TypeECRRepository, repoARN)
			if _, ok := repoIDs[rID]; ok {
				if err := st.UpsertRelationship(d.ID, rID, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert batch job-def→ecr repo: %w", err)
				}
			}
		}
	}
	return nil
}

// batchQuotaShareAttrs mirrors verbatim QuotaShareDetail fields.
type batchQuotaShareAttrs struct {
	JobQueueArn *string `json:"JobQueueArn"`
}

// resolveBatchQuotaShareJobQueue emits quota-share → job-queue (attached-to)
// edges. JobQueueArn is required on the SDK shape; FK-safe via job-queue id
// set so cross-account or unscanned queues skip without dangling edges.
func resolveBatchQuotaShareJobQueue(acct *account, st *store.Store) error {
	shares, err := st.ListResources(store.ResourceFilter{
		Provider:  "aws",
		AccountID: acct.ID,
		Types:     []string{TypeBatchQuotaShare},
		Limit:     util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(shares) == 0 {
		return nil
	}
	queueIDs, err := resourceIDSet(st, acct.ID, TypeBatchJobQueue)
	if err != nil {
		return err
	}
	for _, s := range shares {
		var attrs batchQuotaShareAttrs
		if err := json.Unmarshal([]byte(s.AttributesJSON), &attrs); err != nil {
			continue
		}
		qARN := sv(attrs.JobQueueArn)
		if qARN == "" {
			continue
		}
		qID := store.ResourceID("aws", acct.ID, TypeBatchJobQueue, qARN)
		if _, ok := queueIDs[qID]; !ok {
			continue
		}
		if err := st.UpsertRelationship(s.ID, qID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert batch quota-share→job-queue: %w", err)
		}
	}
	return nil
}
