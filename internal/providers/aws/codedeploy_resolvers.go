package aws

import (
	"encoding/json"
	"fmt"

	"codeberg.org/icearp/disco/internal/store"
	"codeberg.org/icearp/disco/internal/util"
)

func init() {
	registerResolver(
		resolveCodeDeployDGRefs,
		EdgeDecl{TypeCodeDeployDeploymentGroup, TypeCodeDeployApplication, store.RelAttachedTo},
		EdgeDecl{TypeCodeDeployDeploymentGroup, TypeCodeDeployDeploymentConfig, store.RelUses},
		EdgeDecl{TypeCodeDeployDeploymentGroup, TypeIAMRole, store.RelUses},
	)
}

func codeDeployARN(region, acct, kind, key string) string {
	return fmt.Sprintf("arn:aws:codedeploy:%s:%s:%s:%s", region, acct, kind, key)
}

// resolveCodeDeployDGRefs wires each deployment-group to its application
// (ApplicationName), deployment-config (DeploymentConfigName), and IAM service
// role (ServiceRoleArn).
func resolveCodeDeployDGRefs(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeCodeDeployDeploymentGroup}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	appSet, err := scannedIDSet(acct, st, TypeCodeDeployApplication)
	if err != nil {
		return err
	}
	dcSet, err := scannedIDSet(acct, st, TypeCodeDeployDeploymentConfig)
	if err != nil {
		return err
	}
	roleSet, err := scannedIDSet(acct, st, TypeIAMRole)
	if err != nil {
		return err
	}
	for _, r := range rows {
		var attrs struct {
			ApplicationName      *string `json:"ApplicationName"`
			DeploymentConfigName *string `json:"DeploymentConfigName"`
			ServiceRoleArn       *string `json:"ServiceRoleArn"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		region := sv(r.Region)
		if n := sv(attrs.ApplicationName); n != "" {
			tgtID := store.ResourceID("aws", acct.ID, TypeCodeDeployApplication, codeDeployARN(region, acct.ID, "application", n))
			if appSet[tgtID] {
				if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert codedeploy dg→app: %w", err)
				}
			}
		}
		if n := sv(attrs.DeploymentConfigName); n != "" {
			tgtID := store.ResourceID("aws", acct.ID, TypeCodeDeployDeploymentConfig, codeDeployARN(region, acct.ID, "deploymentconfig", n))
			if dcSet[tgtID] {
				if err := st.UpsertRelationship(r.ID, tgtID, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert codedeploy dg→dc: %w", err)
				}
			}
		}
		if role := sv(attrs.ServiceRoleArn); role != "" {
			tgtID := store.ResourceID("aws", acct.ID, TypeIAMRole, role)
			if roleSet[tgtID] {
				if err := st.UpsertRelationship(r.ID, tgtID, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert codedeploy dg→role: %w", err)
				}
			}
		}
	}
	return nil
}
