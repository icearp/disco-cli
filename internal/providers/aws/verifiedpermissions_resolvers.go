package aws

import (
	"encoding/json"
	"fmt"
	"strings"

	"codeberg.org/icearp/disco/internal/util"
	"codeberg.org/icearp/disco/store"
)

func init() {
	registerResolver(
		resolveVPChildToPolicyStore,
		EdgeDecl{TypeVerifiedPermissionsPolicy, TypeVerifiedPermissionsPolicyStore, store.RelAttachedTo},
		EdgeDecl{TypeVerifiedPermissionsPolicyTemplate, TypeVerifiedPermissionsPolicyStore, store.RelAttachedTo},
		EdgeDecl{TypeVerifiedPermissionsIdentitySource, TypeVerifiedPermissionsPolicyStore, store.RelAttachedTo},
	)
	registerResolver(
		resolveVPPolicyStoreAliasParent,
		EdgeDecl{TypeVerifiedPermissionsPolicyStoreAlias, TypeVerifiedPermissionsPolicyStore, store.RelAttachedTo},
	)
}

// resolveVPPolicyStoreAliasParent wires each policy-store-alias to its policy
// store via the alias's PolicyStoreId attr, matched against the scanned
// policy-store's NativeID (the store ARN). Aliases carry no parent ARN in
// their own NativeID, so the link is rebuilt from the PolicyStoreId index.
func resolveVPPolicyStoreAliasParent(acct *account, st *store.Store) error {
	aliases, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID,
		Types: []string{TypeVerifiedPermissionsPolicyStoreAlias}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(aliases) == 0 {
		return nil
	}
	stores, err := st.ListResources(store.ResourceFilter{
		Providers: []string{"aws"}, AccountID: acct.ID,
		Types: []string{TypeVerifiedPermissionsPolicyStore}, Limit: util.AllResources,
	})
	if err != nil {
		return err
	}
	byPolicyStoreID := make(map[string]string, len(stores))
	for _, s := range stores {
		var attrs struct {
			PolicyStoreID *string `json:"PolicyStoreId"`
		}
		if err := json.Unmarshal([]byte(s.AttributesJSON), &attrs); err != nil {
			continue
		}
		if id := sv(attrs.PolicyStoreID); id != "" {
			byPolicyStoreID[id] = s.ID
		}
	}
	for _, a := range aliases {
		var attrs struct {
			PolicyStoreID *string `json:"PolicyStoreId"`
		}
		if err := json.Unmarshal([]byte(a.AttributesJSON), &attrs); err != nil {
			continue
		}
		tgtID, ok := byPolicyStoreID[sv(attrs.PolicyStoreID)]
		if !ok {
			continue
		}
		if err := st.UpsertRelationship(a.ID, tgtID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert vp policy-store-alias→policy-store: %w", err)
		}
	}
	return nil
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
			Providers: []string{"aws"}, AccountID: acct.ID, Types: []string{child.t}, Limit: util.AllResources,
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
			tgtID := store.ResourceID("aws", acct.ID, parent)
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
