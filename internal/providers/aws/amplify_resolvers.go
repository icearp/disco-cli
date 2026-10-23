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
		resolveAmplifyRelationships,
		EdgeDecl{TypeAmplifyApp, TypeIAMRole, store.RelUses},
		EdgeDecl{TypeAmplifyBranch, TypeIAMRole, store.RelUses},
	)
	registerResolver(
		noopAmplifyHierarchy,
		EdgeDecl{TypeAmplifyApp, TypeAmplifyBranch, store.RelContains},
		EdgeDecl{TypeAmplifyApp, TypeAmplifyDomain, store.RelContains},
		EdgeDecl{TypeAmplifyApp, TypeAmplifyWebhooks, store.RelContains},
	)
}

// noopAmplifyHierarchy emits nothing: the app → branch, domain association and
// webhook contains edges are written at scan time by the child phases of
// scanAmplifyAll. It is registered only so those edges are declared to
// coverage tooling.
func noopAmplifyHierarchy(_ *account, _ *store.Store) error {
	return nil
}

// resolveAmplifyRelationships emits app/branch → IAM role edges:
//   - app.IamServiceRoleArn (uses)
//   - app.ComputeRoleArn (uses, SSR)
//   - branch.ComputeRoleArn (uses, SSR per-branch)
//
// A row whose attributes do not decode is skipped and its error returned
// after the other rows' edges are written, so one bad row costs no other row
// its edge.
func resolveAmplifyRelationships(acct *account, st *store.Store) error {
	apps, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeAmplifyApp},
		Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	branches, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{TypeAmplifyBranch},
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
		roleID := store.ResourceID("aws", acct.ID, roleArn)
		if _, ok := roleIDs[roleID]; !ok {
			return nil
		}
		if err := st.UpsertRelationship(srcID, roleID, store.RelUses, "directed", nil); err != nil {
			return fmt.Errorf("upsert amplify→iam-role: %w", err)
		}
		return nil
	}

	var decodeErrs []error
	type appAttrs struct {
		IamServiceRoleArn *string `json:"IamServiceRoleArn"`
		ComputeRoleArn    *string `json:"ComputeRoleArn"`
	}
	for _, a := range apps {
		var aa appAttrs
		if err := json.Unmarshal([]byte(a.AttributesJSON), &aa); err != nil {
			decodeErrs = append(decodeErrs, fmt.Errorf("decode %s %s attributes: %w", TypeAmplifyApp, a.NativeID, err))
			continue
		}
		if err := emit(a.ID, sv(aa.IamServiceRoleArn)); err != nil {
			return errors.Join(append(decodeErrs, err)...)
		}
		if err := emit(a.ID, sv(aa.ComputeRoleArn)); err != nil {
			return errors.Join(append(decodeErrs, err)...)
		}
	}

	type branchAttrs struct {
		ComputeRoleArn *string `json:"ComputeRoleArn"`
	}
	for _, b := range branches {
		var ba branchAttrs
		if err := json.Unmarshal([]byte(b.AttributesJSON), &ba); err != nil {
			decodeErrs = append(decodeErrs, fmt.Errorf("decode %s %s attributes: %w", TypeAmplifyBranch, b.NativeID, err))
			continue
		}
		if err := emit(b.ID, sv(ba.ComputeRoleArn)); err != nil {
			return errors.Join(append(decodeErrs, err)...)
		}
	}
	return errors.Join(decodeErrs...)
}
