package aws

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/icearp/disco-cli/internal/util"
	"github.com/icearp/disco-cli/store"
)

func init() {
	registerResolver(
		resolveImageBuilderPipelineRefs,
		EdgeDecl{TypeImageBuilderImagePipeline, TypeImageBuilderImageRecipe, store.RelUses},
		EdgeDecl{TypeImageBuilderImagePipeline, TypeImageBuilderContainerRecipe, store.RelUses},
		EdgeDecl{TypeImageBuilderImagePipeline, TypeImageBuilderDistributionConfiguration, store.RelUses},
		EdgeDecl{TypeImageBuilderImagePipeline, TypeImageBuilderInfrastructureConfig, store.RelUses},
		EdgeDecl{TypeImageBuilderImagePipeline, TypeIAMRole, store.RelAssumes},
	)
	registerResolver(
		resolveImageBuilderInfraInstanceProfile,
		EdgeDecl{TypeImageBuilderInfrastructureConfig, TypeIAMInstanceProfile, store.RelAssumes},
	)
	registerResolver(
		resolveImageBuilderLifecycleRole,
		EdgeDecl{TypeImageBuilderLifecyclePolicy, TypeIAMRole, store.RelAssumes},
	)
}

// resolveImageBuilderPipelineRefs wires pipeline → recipe / container-recipe /
// distribution-config / infrastructure-config / IAM execution role.
func resolveImageBuilderPipelineRefs(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeImageBuilderImagePipeline},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	irSet, err := scannedIDSet(acct, st, TypeImageBuilderImageRecipe)
	if err != nil {
		return err
	}
	crSet, err := scannedIDSet(acct, st, TypeImageBuilderContainerRecipe)
	if err != nil {
		return err
	}
	dcSet, err := scannedIDSet(acct, st, TypeImageBuilderDistributionConfiguration)
	if err != nil {
		return err
	}
	icSet, err := scannedIDSet(acct, st, TypeImageBuilderInfrastructureConfig)
	if err != nil {
		return err
	}
	roleSet, err := scannedIDSet(acct, st, TypeIAMRole)
	if err != nil {
		return err
	}
	// A row that fails to decode is reported at the end so that it costs the
	// other rows none of their edges.
	var decodeErrs []error
	for _, r := range rows {
		var attrs struct {
			ImageRecipeArn                 *string `json:"ImageRecipeArn"`
			ContainerRecipeArn             *string `json:"ContainerRecipeArn"`
			DistributionConfigurationArn   *string `json:"DistributionConfigurationArn"`
			InfrastructureConfigurationArn *string `json:"InfrastructureConfigurationArn"`
			ExecutionRole                  *string `json:"ExecutionRole"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			decodeErrs = append(decodeErrs, fmt.Errorf("decode %s %s attributes: %w", TypeImageBuilderImagePipeline, r.NativeID, err))
			continue
		}
		pairs := []struct {
			arn  string
			ttyp string
			set  map[string]bool
			kind string
		}{
			{sv(attrs.ImageRecipeArn), TypeImageBuilderImageRecipe, irSet, store.RelUses},
			{sv(attrs.ContainerRecipeArn), TypeImageBuilderContainerRecipe, crSet, store.RelUses},
			{sv(attrs.DistributionConfigurationArn), TypeImageBuilderDistributionConfiguration, dcSet, store.RelUses},
			{sv(attrs.InfrastructureConfigurationArn), TypeImageBuilderInfrastructureConfig, icSet, store.RelUses},
			{sv(attrs.ExecutionRole), TypeIAMRole, roleSet, store.RelAssumes},
		}
		for _, p := range pairs {
			if p.arn == "" {
				continue
			}
			tgtID := store.ResourceID("aws", acct.ID, p.arn)
			if !p.set[tgtID] {
				continue
			}
			if err := st.UpsertRelationship(r.ID, tgtID, p.kind, "directed", nil); err != nil {
				return fmt.Errorf("upsert imagebuilder pipeline→%s: %w", p.ttyp, err)
			}
		}
	}
	return errors.Join(decodeErrs...)
}

// resolveImageBuilderInfraInstanceProfile wires infra-config → IAM instance
// profile via `InstanceProfileName` (bare name → canonical IAM ARN).
func resolveImageBuilderInfraInstanceProfile(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeImageBuilderInfrastructureConfig},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	ipSet, err := scannedIDSet(acct, st, TypeIAMInstanceProfile)
	if err != nil {
		return err
	}
	var decodeErrs []error
	for _, r := range rows {
		var attrs struct {
			InstanceProfileName *string `json:"InstanceProfileName"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			decodeErrs = append(decodeErrs, fmt.Errorf("decode %s %s attributes: %w", TypeImageBuilderInfrastructureConfig, r.NativeID, err))
			continue
		}
		name := sv(attrs.InstanceProfileName)
		if name == "" {
			continue
		}
		ipARN := iamInstanceProfileARN(acct.partition(), acct.ID, name)
		tgtID := store.ResourceID("aws", acct.ID, ipARN)
		if !ipSet[tgtID] {
			continue
		}
		if err := st.UpsertRelationship(r.ID, tgtID, store.RelAssumes, "directed", nil); err != nil {
			return fmt.Errorf("upsert imagebuilder infra→instance-profile: %w", err)
		}
	}
	return errors.Join(decodeErrs...)
}

// resolveImageBuilderLifecycleRole wires lifecycle-policy → IAM role
// (ExecutionRole).
func resolveImageBuilderLifecycleRole(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeImageBuilderLifecyclePolicy},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	roleSet, err := scannedIDSet(acct, st, TypeIAMRole)
	if err != nil {
		return err
	}
	var decodeErrs []error
	for _, r := range rows {
		var attrs struct {
			ExecutionRole *string `json:"ExecutionRole"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			decodeErrs = append(decodeErrs, fmt.Errorf("decode %s %s attributes: %w", TypeImageBuilderLifecyclePolicy, r.NativeID, err))
			continue
		}
		role := sv(attrs.ExecutionRole)
		if role == "" {
			continue
		}
		tgtID := store.ResourceID("aws", acct.ID, role)
		if !roleSet[tgtID] {
			continue
		}
		if err := st.UpsertRelationship(r.ID, tgtID, store.RelAssumes, "directed", nil); err != nil {
			return fmt.Errorf("upsert imagebuilder lifecycle→role: %w", err)
		}
	}
	return errors.Join(decodeErrs...)
}
