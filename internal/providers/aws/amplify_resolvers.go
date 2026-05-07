package aws

import (
	"encoding/json"
	"fmt"

	"codeberg.org/icearp/disco/internal/store"
	"codeberg.org/icearp/disco/internal/util"
)

func init() {
	registerResolver(
		resolveAmplifyRelationships,
		EdgeDecl{TypeAmplifyApp, TypeIAMRole, store.RelUses},
		EdgeDecl{TypeAmplifyBranch, TypeIAMRole, store.RelUses},
	)
}

// resolveAmplifyRelationships emits app/branch → IAM role edges:
//   - app.IamServiceRoleArn (uses)
//   - app.ComputeRoleArn (uses, SSR)
//   - branch.ComputeRoleArn (uses, SSR per-branch)
//
// Domain associations carry no cross-resource ARNs to scanned resources
// beyond the parent app (already wired via hierarchy closure at scan time).
func resolveAmplifyRelationships(acct *account, st *store.Store) error {
	apps, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeAmplifyApp},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	branches, err := st.ListResources(store.ResourceFilter{
		Provider: "aws", AccountID: acct.ID, Types: []string{TypeAmplifyBranch},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(apps)+len(branches) == 0 {
		return nil
	}
	roleIDs, err := scannedIDSet(acct, st, TypeIAMRole)
	if err != nil {
		return err
	}

	emit := func(srcID, roleArn string) error {
		if roleArn == "" {
			return nil
		}
		roleID := store.ResourceID("aws", acct.ID, TypeIAMRole, roleArn)
		if _, ok := roleIDs[roleID]; !ok {
			return nil
		}
		if err := st.UpsertRelationship(srcID, roleID, store.RelUses, "directed", nil); err != nil {
			return fmt.Errorf("upsert amplify→iam-role: %w", err)
		}
		return nil
	}

	type appAttrs struct {
		IamServiceRoleArn *string `json:"IamServiceRoleArn"`
		ComputeRoleArn    *string `json:"ComputeRoleArn"`
	}
	for _, a := range apps {
		var aa appAttrs
		if err := json.Unmarshal([]byte(a.AttributesJSON), &aa); err != nil {
			continue
		}
		if err := emit(a.ID, sv(aa.IamServiceRoleArn)); err != nil {
			return err
		}
		if err := emit(a.ID, sv(aa.ComputeRoleArn)); err != nil {
			return err
		}
	}

	type branchAttrs struct {
		ComputeRoleArn *string `json:"ComputeRoleArn"`
	}
	for _, b := range branches {
		var ba branchAttrs
		if err := json.Unmarshal([]byte(b.AttributesJSON), &ba); err != nil {
			continue
		}
		if err := emit(b.ID, sv(ba.ComputeRoleArn)); err != nil {
			return err
		}
	}
	return nil
}
