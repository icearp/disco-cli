package aws

import (
	"fmt"
	"strings"

	"codeberg.org/icearp/disco/internal/store"
	"codeberg.org/icearp/disco/internal/util"
)

func init() {
	registerResolver(
		resolveVPChildToPolicyStore,
		EdgeDecl{TypeVerifiedPermissionsPolicy, TypeVerifiedPermissionsPolicyStore, store.RelAttachedTo},
		EdgeDecl{TypeVerifiedPermissionsPolicyTemplate, TypeVerifiedPermissionsPolicyStore, store.RelAttachedTo},
		EdgeDecl{TypeVerifiedPermissionsIdentitySource, TypeVerifiedPermissionsPolicyStore, store.RelAttachedTo},
	)
}

// resolveVPChildToPolicyStore wires policy/policy-template/identity-source to
// their parent policy-store via NativeID `{psARN}/<seg>/{id}` strip.
func resolveVPChildToPolicyStore(acct *account, st *store.Store) error {
	psSet, err := scannedIDSet(acct, st, TypeVerifiedPermissionsPolicyStore)
	if err != nil {
		return err
	}
	for _, child := range []struct {
		t, seg string
	}{
		{TypeVerifiedPermissionsPolicy, "/policy/"},
		{TypeVerifiedPermissionsPolicyTemplate, "/policy-template/"},
		{TypeVerifiedPermissionsIdentitySource, "/identity-source/"},
	} {
		rows, err := st.ListResources(store.ResourceFilter{
			Provider: "aws", AccountID: acct.ID, Types: []string{child.t}, Limit: util.AllResources,
		})
		if err != nil {
			return err
		}
		for _, r := range rows {
			i := strings.LastIndex(r.NativeID, child.seg)
			if i <= 0 {
				continue
			}
			parent := r.NativeID[:i]
			tgtID := store.ResourceID("aws", acct.ID, TypeVerifiedPermissionsPolicyStore, parent)
			if !psSet[tgtID] {
				continue
			}
			if err := st.UpsertRelationship(r.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
				return fmt.Errorf("upsert vp %s→ps: %w", child.t, err)
			}
		}
	}
	return nil
}
