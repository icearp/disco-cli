package aws

import (
	"encoding/json"
	"fmt"

	"codeberg.org/icearp/disco/internal/util"
	"codeberg.org/icearp/disco/store"
)

func init() {
	registerResolver(
		resolveSSMQuickSetupConfigManagerRoles,
		EdgeDecl{TypeSSMQuickSetupConfigurationManager, TypeIAMRole, store.RelAssumes},
	)
}

// resolveSSMQuickSetupConfigManagerRoles wires each configuration-manager to
// the IAM admin role on each ConfigurationDefinition
// (LocalDeploymentAdministrationRoleArn). Skips bare role-name fields
// (LocalDeploymentExecutionRoleName) — without region/acct context they
// can't be safely resolved to an ARN.
func resolveSSMQuickSetupConfigManagerRoles(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeSSMQuickSetupConfigurationManager}, Limit: util.AllResources,
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
	for _, r := range rows {
		var attrs struct {
			ConfigurationDefinitions []struct {
				LocalDeploymentAdministrationRoleArn *string `json:"LocalDeploymentAdministrationRoleArn"`
			} `json:"ConfigurationDefinitions"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		seen := map[string]struct{}{}
		for _, d := range attrs.ConfigurationDefinitions {
			ra := sv(d.LocalDeploymentAdministrationRoleArn)
			if ra == "" {
				continue
			}
			if _, ok := seen[ra]; ok {
				continue
			}
			seen[ra] = struct{}{}
			tgt := store.ResourceID("aws", acct.ID, TypeIAMRole, ra)
			if !roleSet[tgt] {
				continue
			}
			if err := st.UpsertRelationship(r.ID, tgt, store.RelAssumes, "directed", nil); err != nil {
				return fmt.Errorf("upsert ssm-quick-setup mgr→role: %w", err)
			}
		}
	}
	return nil
}
