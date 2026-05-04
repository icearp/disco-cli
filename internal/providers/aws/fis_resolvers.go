package aws

import (
	"encoding/json"
	"fmt"
	"strings"

	"codeberg.org/icearp/disco/internal/store"
	"codeberg.org/icearp/disco/internal/util"
)

func init() {
	registerResolver(resolveFISExperimentTemplateRefs,
		EdgeDecl{TypeFISExperimentTemplate, TypeIAMRole, store.RelUses},
	)
	registerResolver(resolveFISTargetAccountConfigRefs,
		EdgeDecl{TypeFISTargetAccountConfiguration, TypeFISExperimentTemplate, store.RelAttachedTo},
		EdgeDecl{TypeFISTargetAccountConfiguration, TypeIAMRole, store.RelUses},
	)
}

// resolveFISExperimentTemplateRefs wires each experiment-template to its IAM
// role (RoleArn).
func resolveFISExperimentTemplateRefs(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeFISExperimentTemplate}, Limit: util.AllResources,
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
			RoleArn *string `json:"RoleArn"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if role := sv(attrs.RoleArn); role != "" {
			tgtID := store.ResourceID("aws", acct.ID, TypeIAMRole, role)
			if roleSet[tgtID] {
				if err := st.UpsertRelationship(r.ID, tgtID, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert fis et→role: %w", err)
				}
			}
		}
	}
	return nil
}

// resolveFISTargetAccountConfigRefs wires target-account-configuration → its
// parent experiment-template (NativeID strip `/target-account-configuration/`)
// and IAM role (RoleArn).
func resolveFISTargetAccountConfigRefs(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeFISTargetAccountConfiguration}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	tplSet, err := scannedIDSet(acct, st, TypeFISExperimentTemplate)
	if err != nil {
		return err
	}
	roleSet, err := scannedIDSet(acct, st, TypeIAMRole)
	if err != nil {
		return err
	}
	for _, r := range rows {
		if idx := strings.LastIndex(r.NativeID, "/target-account-configuration/"); idx > 0 {
			parent := r.NativeID[:idx]
			tgtID := store.ResourceID("aws", acct.ID, TypeFISExperimentTemplate, parent)
			if tplSet[tgtID] {
				if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
					return fmt.Errorf("upsert fis tac→tpl: %w", err)
				}
			}
		}
		var attrs struct {
			RoleArn *string `json:"RoleArn"`
		}
		if err := json.Unmarshal([]byte(r.AttributesJSON), &attrs); err != nil {
			continue
		}
		if role := sv(attrs.RoleArn); role != "" {
			tgtID := store.ResourceID("aws", acct.ID, TypeIAMRole, role)
			if roleSet[tgtID] {
				if err := st.UpsertRelationship(r.ID, tgtID, store.RelUses, "directed", nil); err != nil {
					return fmt.Errorf("upsert fis tac→role: %w", err)
				}
			}
		}
	}
	return nil
}
