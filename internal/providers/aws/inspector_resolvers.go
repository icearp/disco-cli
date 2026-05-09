package aws

import (
	"encoding/json"
	"fmt"

	"codeberg.org/icearp/disco/store"
	"codeberg.org/icearp/disco/internal/util"
)

func init() {
	registerResolver(
		resolveInspector2MemberOrgAccount,
		EdgeDecl{TypeInspector2Member, TypeOrganizationsAccount, store.RelAttachedTo},
	)
}

// inspector2MemberAttrs mirrors the verbatim Member fields used by the
// resolver. PascalCase tags match mustJSON of the SDK v2 struct.
type inspector2MemberAttrs struct {
	AccountID *string `json:"AccountID"`
}

// resolveInspector2MemberOrgAccount emits an `attached-to` edge from each
// Inspector v2 member row to its corresponding AWS Organizations account,
// when the org tree is also scanned. FK-safe via loadOrgTargetIndex;
// partial-coverage scans (no Org tree) skip silently. Mirrors the
// Detective + SSO assignment → org-account precedent.
func resolveInspector2MemberOrgAccount(acct *account, st *store.Store) error {
	members, err := st.ListResources(store.ResourceFilter{
		Provider:  "aws",
		AccountID: acct.ID,
		Types:     []string{TypeInspector2Member},
		Limit:     util.AllResources,
	})
	if err != nil {
		return err
	}
	if len(members) == 0 {
		return nil
	}

	orgArnByID, _, err := loadOrgTargetIndex(acct, st)
	if err != nil {
		return err
	}
	if len(orgArnByID) == 0 {
		return nil
	}

	for _, m := range members {
		var attrs inspector2MemberAttrs
		if err := json.Unmarshal([]byte(m.AttributesJSON), &attrs); err != nil {
			continue
		}
		accountID := sv(attrs.AccountID)
		if accountID == "" {
			continue
		}
		orgARN, ok := orgArnByID[accountID]
		if !ok {
			continue
		}
		orgID := store.ResourceID("aws", acct.ID, TypeOrganizationsAccount, orgARN)
		if err := st.UpsertRelationship(m.ID, orgID, store.RelAttachedTo, "directed", nil); err != nil {
			return fmt.Errorf("upsert inspector2 member→org account: %w", err)
		}
	}
	return nil
}
