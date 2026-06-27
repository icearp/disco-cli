package aws

import (
	"fmt"

	"codeberg.org/icearp/disco/internal/util"
	"codeberg.org/icearp/disco/store"
)

func init() {
	registerResolver(
		resolveUXCAccountCustomizationToOrgAccount,
		EdgeDecl{TypeUXCAccountCustomization, TypeOrganizationsAccount, store.RelAttachedTo},
	)
}

// resolveUXCAccountCustomizationToOrgAccount links the per-account UXC
// console-customization singleton to the corresponding aws:organizations:account
// row. Short-circuits cleanly when the org tree was not scanned (member-only
// scan, standalone account) — emitting an edge to a phantom target would
// FK-fail. IncludeManaged is set because default-state customizations are
// flagged ManagedByProvider=true at scan time.
func resolveUXCAccountCustomizationToOrgAccount(acct *account, st *store.Store) error {
	rows, err := st.ListResources(store.ResourceFilter{
		Providers:      []string{"aws"},
		AccountID:      acct.ID,
		Types:          []string{TypeUXCAccountCustomization},
		IncludeManaged: true,
		Limit:          util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return nil
	}
	arnByID, _, err := loadOrgTargetIndex(acct, st)
	if err != nil {
		return err
	}
	acctARN, ok := arnByID[acct.ID]
	if !ok {
		return nil
	}
	toID := store.ResourceID("aws", acct.ID, TypeOrganizationsAccount, acctARN)
	for _, r := range rows {
		if err := st.UpsertRelationship(r.ID, toID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert uxc→org-account relationship: %w", err)
		}
	}
	return nil
}
